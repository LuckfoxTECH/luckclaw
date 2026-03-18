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

func truncStr(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

type sessionWebhookEntry struct {
	URL           string
	Expire        int64 // unix ms
	SenderStaffID string
}

type DingTalkChannel struct {
	cfg  config.DingTalkConfig
	bus  *bus.MessageBus
	http *http.Client

	// sessionWebhookCache: conversationId -> sessionWebhookEntry
	webhookMu    sync.RWMutex
	webhookCache map[string]*sessionWebhookEntry
}

func NewDingTalk(cfg config.DingTalkConfig, b *bus.MessageBus) *DingTalkChannel {
	return &DingTalkChannel{
		cfg:          cfg,
		bus:          b,
		http:         &http.Client{Timeout: 30 * time.Second},
		webhookCache: make(map[string]*sessionWebhookEntry),
	}
}

func (c *DingTalkChannel) Name() string { return "dingtalk" }

func (c *DingTalkChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.AppKey) == "" || strings.TrimSpace(c.cfg.AppSecret) == "" {
		return fmt.Errorf("dingtalk appKey/appSecret required")
	}
	log.Printf("[dingtalk] starting Stream mode (appKey=%s, robotCode=%s), allowFrom=%v", c.cfg.AppKey, c.cfg.RobotCode, c.cfg.AllowFrom)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[dingtalk] context cancelled, stopping")
			return nil
		default:
		}

		err := c.connectStream(ctx)
		if err != nil {
			log.Printf("[dingtalk] Stream connection failed: %v, retrying in 5s", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (c *DingTalkChannel) Stop(ctx context.Context) error {
	c.http.CloseIdleConnections()
	return nil
}

func (c *DingTalkChannel) connectStream(ctx context.Context) error {
	// Step 1: Register the connection credentials
	regBody, _ := json.Marshal(map[string]any{
		"clientId":     c.cfg.AppKey,
		"clientSecret": c.cfg.AppSecret,
		"subscriptions": []map[string]string{
			{"topic": "/v1.0/im/bot/messages/get", "type": "CALLBACK"},
		},
		"ua": "luckclaw-dingtalk-go/1.0",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.dingtalk.com/v1.0/gateway/connections/open",
		bytes.NewReader(regBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway/connections/open failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var regResp struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
	}
	if json.Unmarshal(body, &regResp) != nil || regResp.Endpoint == "" || regResp.Ticket == "" {
		return fmt.Errorf("gateway/connections/open invalid response: %s", string(body))
	}

	// Step 2: Establish the WebSocket connection
	wsURL := regResp.Endpoint
	if !strings.HasPrefix(wsURL, "ws") {
		wsURL = "wss://" + strings.TrimPrefix(strings.TrimPrefix(wsURL, "https://"), "http://")
	}
	sep := "?"
	if strings.Contains(wsURL, "?") {
		sep = "&"
	}
	wsURL = wsURL + sep + "ticket=" + regResp.Ticket
	log.Printf("[dingtalk] connecting WebSocket to %s", truncStr(wsURL, 60))

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()
	log.Printf("[dingtalk] WebSocket connected, listening for messages")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[dingtalk] WebSocket read error: %v", err)
			return err
		}
		log.Printf("[dingtalk] recv len=%d", len(data))
		if c.handleStreamMessage(ctx, conn, data) {
			return nil
		}
	}
}

func (c *DingTalkChannel) handleStreamMessage(ctx context.Context, conn *websocket.Conn, data []byte) bool {
	var frame struct {
		SpecVersion string            `json:"specVersion"`
		Type        string            `json:"type"`
		Headers     map[string]string `json:"headers"`
		Data        string            `json:"data"`
	}
	if json.Unmarshal(data, &frame) != nil {
		return false
	}
	messageID := frame.Headers["messageId"]
	topic := frame.Headers["topic"]
	contentType := frame.Headers["contentType"]
	if contentType == "" {
		contentType = "application/json"
	}

	ack := func(code int, dataStr string) {
		resp := map[string]any{
			"code":    code,
			"message": "OK",
			"headers": map[string]string{
				"messageId":   messageID,
				"contentType": contentType,
			},
			"data": dataStr,
		}
		if code != 200 {
			resp["message"] = "ERROR"
		}
		b, _ := json.Marshal(resp)
		_ = conn.WriteMessage(websocket.TextMessage, b)
	}

	switch frame.Type {
	case "SYSTEM":
		switch topic {
		case "ping":
			var pingData struct {
				Opaque string `json:"opaque"`
			}
			_ = json.Unmarshal([]byte(frame.Data), &pingData)
			opaqueJSON, _ := json.Marshal(map[string]string{"opaque": pingData.Opaque})
			ack(200, string(opaqueJSON))
		case "disconnect":
			log.Printf("[dingtalk] disconnect: %s", frame.Data)
			ack(200, `{"response":null}`)
			return true
		default:
			ack(200, `{"response":null}`)
		}
		return false

	case "CALLBACK":
		if topic == "/v1.0/im/bot/messages/get" {
			c.handleBotMessage(ctx, frame.Data, ack)
			return false
		}
		ack(200, `{"response":null}`)
		return false

	case "EVENT":
		ack(200, `{"status":"SUCCESS","message":"ok"}`)
		return false

	default:
		ack(200, `{"response":null}`)
		return false
	}
}

func (c *DingTalkChannel) handleBotMessage(ctx context.Context, dataStr string, ack func(int, string)) {
	var data struct {
		ConversationID            string `json:"conversationId"`
		ConversationType          string `json:"conversationType"`
		SenderStaffID             string `json:"senderStaffId"`
		SenderID                  string `json:"senderId"`
		SessionWebhook            string `json:"sessionWebhook"`
		SessionWebhookExpiredTime int64  `json:"sessionWebhookExpiredTime"`
		Text                      struct {
			Content string `json:"content"`
		} `json:"text"`
		Msgtype   string `json:"msgtype"`
		RobotCode string `json:"robotCode"`
	}
	if json.Unmarshal([]byte(dataStr), &data) != nil {
		ack(500, `{"response":null}`)
		return
	}

	if data.Msgtype != "text" {
		log.Printf("[dingtalk] skip msgtype=%q (need text)", data.Msgtype)
		ack(200, `{"response":null}`)
		return
	}

	text := strings.TrimSpace(data.Text.Content)
	if text == "" {
		ack(200, `{"response":null}`)
		return
	}

	senderID := data.SenderStaffID
	if senderID == "" {
		senderID = data.SenderID
	}
	if !IsAllowed(c.cfg.AllowFrom, senderID) {
		log.Printf("[dingtalk] message from %s not in allowFrom (config=%v), ignored", senderID, c.cfg.AllowFrom)
		ack(200, `{"response":null}`)
		return
	}

	if data.ConversationID != "" {
		c.webhookMu.Lock()
		c.webhookCache[data.ConversationID] = &sessionWebhookEntry{
			URL:           data.SessionWebhook,
			Expire:        data.SessionWebhookExpiredTime,
			SenderStaffID: data.SenderStaffID,
		}
		c.webhookMu.Unlock()
	}

	chatID := data.ConversationID
	//if data.ConversationType == "1" {
	// One-on-one chat: Use the conversationId as the chatID
	//} else {
	// Group Chat: Divide the conversation based on the conversationId and session_key
	//}
	sessionKeyOverride := "dingtalk:" + data.ConversationID

	log.Printf("[dingtalk] inbound: sender=%s chat=%s text=%q", senderID, chatID, truncStr(text, 80))

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "dingtalk",
		SenderID: senderID,
		ChatID:   chatID,
		Content:  text,
		Metadata: map[string]any{
			"session_key_override": sessionKeyOverride,
			"conversationType":     data.ConversationType,
			"senderStaffId":        data.SenderStaffID,
		},
	})
	ack(200, `{"response":null}`)
}

func (c *DingTalkChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.AppKey) == "" || strings.TrimSpace(c.cfg.AppSecret) == "" {
		return fmt.Errorf("dingtalk appKey/appSecret not configured")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("dingtalk chat_id (conversationId) required")
	}

	c.webhookMu.Lock()
	entry := c.webhookCache[msg.ChatID]
	c.webhookMu.Unlock()
	if entry != nil && entry.URL != "" && time.Now().UnixMilli() < entry.Expire {
		err := c.sendBySessionWebhook(ctx, entry.URL, msg.Content)
		if err == nil {
			log.Printf("[dingtalk] reply via sessionWebhook: conversationId=%s", msg.ChatID)
			return nil
		}
		log.Printf("[dingtalk] sessionWebhook failed (expired?): %v, fallback to batchSend", err)
	}

	userID := msg.ChatID
	if entry != nil && entry.SenderStaffID != "" {
		userID = entry.SenderStaffID
	}
	if msg.Metadata != nil {
		if sid, ok := msg.Metadata["senderStaffId"].(string); ok && sid != "" {
			userID = sid
		}
	}
	if userID == msg.ChatID && strings.Contains(msg.ChatID, "cid") {
		return fmt.Errorf("dingtalk sessionWebhook expired and no senderStaffId cached for conversationId %s", msg.ChatID)
	}
	return c.sendByBatchSend(ctx, userID, msg.Content)
}

func (c *DingTalkChannel) sendBySessionWebhook(ctx context.Context, webhookURL, content string) error {
	body, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sessionWebhook status=%d body=%s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *DingTalkChannel) sendByBatchSend(ctx context.Context, userID, content string) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"robotCode": c.cfg.RobotCode,
		"userIds":   []string{userID},
		"msgKey":    "sampleText",
		"msgParam":  fmt.Sprintf(`{"content":"%s"}`, escapeJSONString(content)),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.dingtalk.com/v1.0/robot/oToMessages/batchSend",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("Content-Type", "application/json")
	log.Printf("[dingtalk] batchSend: robotCode=%s userIds=%v content=%q", c.cfg.RobotCode, userID, truncStr(content, 80))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		log.Printf("[dingtalk] batchSend failed: status=%d body=%s", resp.StatusCode, string(respBody))
		return fmt.Errorf("dingtalk api error: %d", resp.StatusCode)
	}
	return nil
}

func (c *DingTalkChannel) getToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.dingtalk.com/v1.0/oauth2/accessToken",
		bytes.NewReader([]byte(fmt.Sprintf(`{"appKey":"%s","appSecret":"%s"}`, c.cfg.AppKey, c.cfg.AppSecret))))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int64  `json:"expireIn"`
		Code        string `json:"code"`
		Message     string `json:"message"`
	}
	if json.Unmarshal(body, &data) != nil || data.AccessToken == "" {
		log.Printf("[dingtalk] token failed: status=%d body=%s", resp.StatusCode, string(body))
		return "", fmt.Errorf("dingtalk token failed")
	}
	log.Printf("[dingtalk] token obtained, expireIn=%d", data.ExpireIn)
	return data.AccessToken, nil
}

func escapeJSONString(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "\"", "\\\""), "\n", "\\n")
}
