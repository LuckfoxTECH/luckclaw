package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"

	"github.com/gorilla/websocket"
)

// QQChannel implements the QQ Open Platform bot channel (C2C + group chat).
// Uses WebSocket for inbound (gateway + Identify + heartbeat) and HTTP API for outbound.
type QQChannel struct {
	cfg           config.QQConfig
	bus           *bus.MessageBus
	http          *http.Client
	dedup         map[string]struct{}
	dedupMu       sync.Mutex
	chatTypeCache map[string]string
	msgSeq        int
}

const (
	defaultHeartbeatIntervalMs = 45000
	reconnectDelay             = 5 * time.Second
)

func NewQQ(cfg config.QQConfig, b *bus.MessageBus) *QQChannel {
	return &QQChannel{
		cfg:           cfg,
		bus:           b,
		http:          &http.Client{Timeout: 30 * time.Second},
		dedup:         make(map[string]struct{}),
		chatTypeCache: make(map[string]string),
	}
}

func (c *QQChannel) Name() string { return "qq" }

func (c *QQChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.AppID) == "" || strings.TrimSpace(c.cfg.Secret) == "" {
		return fmt.Errorf("qq appId and secret not configured")
	}
	log.Printf("[qq] starting (appId=%s), allowFrom=%v", c.cfg.AppID, c.cfg.AllowFrom)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[qq] context cancelled, stopping")
			return nil
		default:
		}
		err := c.runWebSocket(ctx)
		if err != nil {
			log.Printf("[qq] WebSocket failed: %v, retrying in 5s", err)
			select {
			case <-time.After(reconnectDelay):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (c *QQChannel) runWebSocket(ctx context.Context) error {
	// 1. Get access_token (bots.qq.com)
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	log.Printf("[qq] token obtained")

	// 2. Get gateway URL (api.sgroup.qq.com)
	gatewayURL, err := c.getGateway(ctx, token)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	log.Printf("[qq] gateway: %s", truncStr(gatewayURL, 50))

	// 3. Establish WebSocket connection
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()
	log.Printf("[qq] WebSocket connected")

	// 4. Receive OpCode 10 Hello and read heartbeat_interval
	_, data, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("ws read hello: %w", err)
	}
	log.Printf("[qq] recv len=%d", len(data))

	var hello struct {
		Op int `json:"op"`
		D  struct {
			HeartbeatInterval int `json:"heartbeat_interval"`
		} `json:"d"`
	}
	if err := json.Unmarshal(data, &hello); err != nil {
		return fmt.Errorf("parse hello: %w", err)
	}
	if hello.Op != 10 {
		return fmt.Errorf("unexpected hello: %s", string(data))
	}
	interval := hello.D.HeartbeatInterval
	if interval <= 0 {
		interval = defaultHeartbeatIntervalMs
	}
	log.Printf("[qq] heartbeat_interval=%dms", interval)

	// 5. Send OpCode 2 Identify
	// intents: 1<<25 (GROUP_AND_C2C_EVENT) + 1<<30 (PUBLIC_GUILD_MESSAGES)
	intents := (1 << 25) | (1 << 30)
	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   "QQBot " + token,
			"intents": intents,
			"shard":   []int{0, 1},
			"properties": map[string]string{
				"$os":      "linux",
				"$browser": "luckclaw",
				"$device":  "luckclaw",
			},
		},
	}
	identifyBody, _ := json.Marshal(identify)
	if err := conn.WriteMessage(websocket.TextMessage, identifyBody); err != nil {
		return fmt.Errorf("identify: %w", err)
	}
	log.Printf("[qq] Identify sent")

	// 6. Receive Ready or Invalid Session
	_, data, err = conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("ws read after identify: %w", err)
	}
	log.Printf("[qq] recv len=%d", len(data))

	var resp struct {
		Op int             `json:"op"`
		T  string          `json:"t"`
		D  json.RawMessage `json:"d"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse identify response: %w", err)
	}
	switch resp.Op {
	case 9:
		return fmt.Errorf("invalid session")
	case 0:
		if resp.T == "READY" {
			log.Printf("[qq] READY, session established")
		}
	}

	var seq int
	if s, ok := parseSeq(data); ok {
		seq = s
	}

	// 7. Start heartbeat goroutine
	var seqMu sync.Mutex
	heartbeatTicker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer heartbeatTicker.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-heartbeatTicker.C:
				seqMu.Lock()
				s := seq
				seqMu.Unlock()
				hb := map[string]any{"op": 1, "d": s}
				if s == 0 {
					hb["d"] = nil
				}
				hbBody, _ := json.Marshal(hb)
				if err := conn.WriteMessage(websocket.TextMessage, hbBody); err != nil {
					return
				}
			}
		}
	}()

	// 8. Message loop
	for {
		select {
		case <-ctx.Done():
			close(done)
			return nil
		default:
		}
		conn.SetReadDeadline(time.Now().Add(time.Duration(interval)*time.Millisecond + 10*time.Second))
		_, data, err := conn.ReadMessage()
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			close(done)
			return fmt.Errorf("ws read: %w", err)
		}

		log.Printf("[qq] recv len=%d", len(data))

		if s, ok := parseSeq(data); ok {
			seqMu.Lock()
			seq = s
			seqMu.Unlock()
		}

		var frame struct {
			Op int             `json:"op"`
			T  string          `json:"t"`
			D  json.RawMessage `json:"d"`
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}

		switch frame.Op {
		case 7:
			log.Printf("[qq] Reconnect requested")
			close(done)
			return nil
		case 11:
			continue
		case 0:
			c.handleDispatch(ctx, frame.T, frame.D)
		default:
			log.Printf("[qq] unhandled op=%d t=%s", frame.Op, frame.T)
		}
	}
}

func parseSeq(data []byte) (int, bool) {
	var v struct {
		S int `json:"s"`
	}
	if json.Unmarshal(data, &v) == nil && v.S > 0 {
		return v.S, true
	}
	return 0, false
}

func (c *QQChannel) getAccessToken(ctx context.Context) (string, error) {
	body := fmt.Sprintf(`{"appId":"%s","clientSecret":"%s"}`, c.cfg.AppID, c.cfg.Secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://bots.qq.com/app/getAppAccessToken", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	// Supports two formats: direct {access_token,expires_in} or {code,data:{access_token,expires_in}}.
	// expires_in may be a string or a number.
	var direct struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   interface{} `json:"expires_in"`
	}
	if json.Unmarshal(b, &direct) == nil && direct.AccessToken != "" {
		log.Printf("[qq] token obtained, expires_in=%v", direct.ExpiresIn)
		return direct.AccessToken, nil
	}
	var wrapped struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string      `json:"access_token"`
			ExpiresIn   interface{} `json:"expires_in"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &wrapped) == nil && wrapped.Code == 0 && wrapped.Data.AccessToken != "" {
		log.Printf("[qq] token obtained, expires_in=%v", wrapped.Data.ExpiresIn)
		return wrapped.Data.AccessToken, nil
	}
	log.Printf("[qq] token failed: status=%d body=%s", resp.StatusCode, string(b))
	return "", fmt.Errorf("token api failed")
}

func (c *QQChannel) getGateway(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.sgroup.qq.com/gateway", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		log.Printf("[qq] gateway failed: status=%d body=%s", resp.StatusCode, string(b))
		return "", fmt.Errorf("gateway: status=%d", resp.StatusCode)
	}
	var result struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(b, &result) != nil || result.URL == "" {
		return "", fmt.Errorf("gateway invalid response: %s", string(b))
	}
	return result.URL, nil
}

func (c *QQChannel) handleDispatch(ctx context.Context, eventType string, d json.RawMessage) {
	switch eventType {
	case "C2C_MESSAGE_CREATE", "GROUP_AT_MESSAGE_CREATE":
		c.handleMessage(ctx, eventType, d)
	default:
		log.Printf("[qq] skip event t=%s", eventType)
	}
}

func (c *QQChannel) handleMessage(ctx context.Context, eventType string, d json.RawMessage) {
	var evt struct {
		ID          string `json:"id"`
		Content     string `json:"content"`
		GroupOpenID string `json:"group_openid"`
		Author      struct {
			ID           string `json:"id"`
			MemberOpenID string `json:"member_openid"`
			UserOpenID   string `json:"user_openid"`
		} `json:"author"`
	}
	if err := json.Unmarshal(d, &evt); err != nil {
		log.Printf("[qq] parse message error: %v", err)
		return
	}

	c.dedupMu.Lock()
	if _, ok := c.dedup[evt.ID]; ok {
		c.dedupMu.Unlock()
		return
	}
	c.dedup[evt.ID] = struct{}{}
	if len(c.dedup) > 1000 {
		c.dedup = make(map[string]struct{})
	}
	c.dedupMu.Unlock()

	content := strings.TrimSpace(evt.Content)
	if content == "" {
		return
	}

	var chatID, userID string
	if evt.GroupOpenID != "" {
		chatID = evt.GroupOpenID
		userID = evt.Author.MemberOpenID
		if userID == "" {
			userID = evt.Author.UserOpenID
		}
		c.chatTypeCache[chatID] = "group"
	} else {
		chatID = evt.Author.UserOpenID
		if chatID == "" {
			chatID = evt.Author.ID
		}
		userID = chatID
		c.chatTypeCache[chatID] = "c2c"
	}

	if !IsAllowed(c.cfg.AllowFrom, userID) {
		log.Printf("[qq] message from %s not in allowFrom (config=%v), ignored", userID, c.cfg.AllowFrom)
		return
	}

	log.Printf("[qq] inbound: sender=%s chat=%s type=%s text=%q", userID, chatID, eventType, truncStr(content, 80))

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "qq",
		SenderID: userID,
		ChatID:   chatID,
		Content:  content,
		Metadata: map[string]any{"message_id": evt.ID},
	})
}

func (c *QQChannel) Stop(ctx context.Context) error {
	c.http.CloseIdleConnections()
	return nil
}

func (c *QQChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	c.msgSeq++
	msgType := c.chatTypeCache[msg.ChatID]
	if msgType == "" {
		msgType = "c2c"
	}

	body := map[string]any{
		"msg_type": 2,
		"markdown": map[string]string{"content": msg.Content},
		"msg_seq":  c.msgSeq,
	}
	if msg.Metadata != nil {
		if mid, ok := msg.Metadata["message_id"].(string); ok && mid != "" {
			body["msg_id"] = mid
		}
	}

	var path string
	if msgType == "group" {
		path = fmt.Sprintf("/v2/groups/%s/messages", msg.ChatID)
	} else {
		path = fmt.Sprintf("/v2/users/%s/messages", msg.ChatID)
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.sgroup.qq.com"+path, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("[qq] send: path=%s content=%q", path, truncStr(msg.Content, 60))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		log.Printf("[qq] send failed: status=%d body=%s", resp.StatusCode, string(respBody))
		return fmt.Errorf("qq send %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
