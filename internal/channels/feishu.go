package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
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

func stringTrunc(b []byte, max int) string {
	s := string(b)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

type FeishuChannel struct {
	cfg    config.FeishuConfig
	bus    *bus.MessageBus
	http   *http.Client
	webCfg config.WebToolsConfig

	mu       sync.Mutex
	token    string
	tokenExp time.Time

	// Message dedup: track recently processed message IDs with bounded capacity
	dedup     map[string]struct{}
	dedupKeys []string
	dedupMu   sync.Mutex
}

const dedupMaxSize = 10000

func NewFeishu(cfg config.FeishuConfig, b *bus.MessageBus, webCfg config.WebToolsConfig) *FeishuChannel {
	transport := &http.Transport{Proxy: webCfg.ProxyFunc()}
	return &FeishuChannel{
		cfg:    cfg,
		bus:    b,
		webCfg: webCfg,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		dedup: make(map[string]struct{}),
	}
}

func (c *FeishuChannel) Name() string { return "feishu" }

func (c *FeishuChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.AppID) == "" || strings.TrimSpace(c.cfg.AppSecret) == "" {
		return fmt.Errorf("feishu appId/appSecret not configured")
	}

	log.Printf("[feishu] starting long connection (appId=%s)", c.cfg.AppID)

	// Start dedup cleaner
	go c.dedupCleaner(ctx)

	// Long connection loop with reconnect
	for {
		select {
		case <-ctx.Done():
			log.Printf("[feishu] context cancelled, stopping")
			return nil
		default:
		}

		err := c.connectWebSocket(ctx)
		if err != nil {
			log.Printf("[feishu] connection failed: %v, retrying in 5s", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (c *FeishuChannel) Stop(ctx context.Context) error {
	c.http.CloseIdleConnections()
	return nil
}

// --- WebSocket Long Connection ---

func (c *FeishuChannel) connectWebSocket(ctx context.Context) error {
	log.Printf("[feishu] requesting WebSocket endpoint...")
	wsURL, err := c.getWebSocketURL(ctx)
	if err != nil {
		return fmt.Errorf("feishu ws endpoint: %w", err)
	}
	log.Printf("[feishu] WebSocket URL obtained (host=%s)", extractHost(wsURL))

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy:            c.webCfg.ProxyFunc(),
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("feishu ws dial: %w", err)
	}
	defer conn.Close()
	log.Printf("[feishu] WebSocket connected, listening for events")

	if msg := strings.TrimSpace(c.cfg.OnConnectMessage); msg != "" && strings.TrimSpace(c.cfg.OnConnectChatID) != "" {
		chatID := strings.TrimSpace(c.cfg.OnConnectChatID)
		log.Printf("[feishu] sending onConnect message to %s", chatID)
		if err := c.Send(ctx, bus.OutboundMessage{ChatID: chatID, Content: msg}); err != nil {
			log.Printf("[feishu] onConnectMessage FAILED: %v", err)
		} else {
			log.Printf("[feishu] onConnectMessage sent ok")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[feishu] WebSocket read error (connection closed): %v", err)
			return fmt.Errorf("feishu ws read: %w", err)
		}

		kind := "text"
		if msgType == websocket.BinaryMessage {
			kind = "binary"
		}
		log.Printf("[feishu] DEBUG recv: type=%s len=%d", kind, len(data))

		c.handleWSMessage(ctx, msgType, data)
	}
}

// parseFeishuFrame parses the protobuf binary frame of the long connection of Feishu, extracting the type and Payload.
// Returns (payload, type, ok). The type is derived from the Headers, such as "event" or "pong".
func parseFeishuFrame(b []byte) (payload []byte, frameType string, ok bool) {
	const (
		wireVarint          = 0
		wireLengthDelimited = 2
	)
	readVarint := func(data []byte) (v uint64, n int) {
		for i := 0; i < len(data); i++ {
			v |= uint64(data[i]&0x7f) << (7 * i)
			if data[i]&0x80 == 0 {
				return v, i + 1
			}
		}
		return 0, 0
	}

	i := 0
	for i < len(b) {
		if i >= len(b) {
			break
		}
		tag, n := readVarint(b[i:])
		if n == 0 {
			break
		}
		i += n
		fieldNum := int(tag >> 3)
		wire := int(tag & 7)

		switch wire {
		case wireVarint:
			_, n = readVarint(b[i:])
			if n == 0 {
				return nil, "", false
			}
			i += n
		case wireLengthDelimited:
			l, n := readVarint(b[i:])
			if n == 0 || i+n+int(l) > len(b) {
				return nil, "", false
			}
			i += n
			chunk := b[i : i+int(l)]
			i += int(l)

			switch fieldNum {
			case 5: // Headers (repeated Header), chunk represents the protobuf bytes of a single Header
				si := 0
				var k, v string
				for si < len(chunk) {
					st, sn := readVarint(chunk[si:])
					if sn == 0 {
						break
					}
					si += sn
					sf := int(st >> 3)
					sw := int(st & 7)
					if sw == wireLengthDelimited {
						sl, ssn := readVarint(chunk[si:])
						if ssn == 0 || si+ssn+int(sl) > len(chunk) {
							break
						}
						si += ssn
						val := string(chunk[si : si+int(sl)])
						si += int(sl)
						if sf == 1 {
							k = val
							if (k == "type" || k == "Type") && v != "" {
								frameType = v
							}
						} else if sf == 2 {
							v = val
							if (k == "type" || k == "Type") && v != "" {
								frameType = v
							}
						}
					} else if sw == wireVarint {
						_, ssn := readVarint(chunk[si:])
						if ssn == 0 {
							break
						}
						si += ssn
					}
				}
			case 8: // Payload
				payload = chunk
			}
		default:
			return nil, "", false
		}
	}
	return payload, frameType, payload != nil || frameType != ""
}

func extractHost(url string) string {
	if idx := strings.Index(url, "://"); idx >= 0 {
		url = url[idx+3:]
	}
	if idx := strings.Index(url, "/"); idx >= 0 {
		url = url[:idx]
	}
	return url
}

func (c *FeishuChannel) getWebSocketURL(ctx context.Context) (string, error) {
	// Use the endpoint format of the official SDK: POST AppID/AppSecret, and the path is /callback/ws/endpoint (not /open-apis/...)
	body, _ := json.Marshal(map[string]string{
		"AppID":     c.cfg.AppID,
		"AppSecret": c.cfg.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/callback/ws/endpoint", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("locale", "zh")
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[feishu] ws endpoint request failed: %v", err)
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[feishu] ws endpoint response read failed: %v", err)
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("[feishu] ws endpoint HTTP %d, body=%s", resp.StatusCode, string(b))
		return "", fmt.Errorf("feishu ws endpoint HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			URL  string `json:"url"`
			URL2 string `json:"URL"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		log.Printf("[feishu] ws endpoint response parse failed: %v, body=%s", err, string(b))
		return "", err
	}
	if parsed.Code != 0 {
		log.Printf("[feishu] ws endpoint error: code=%d msg=%s body=%s", parsed.Code, parsed.Msg, string(b))
		return "", fmt.Errorf("feishu ws endpoint error: code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	wsURL := parsed.Data.URL
	if wsURL == "" {
		wsURL = parsed.Data.URL2
	}
	if wsURL == "" {
		log.Printf("[feishu] ws endpoint data.url empty, body=%s", string(b))
		return "", fmt.Errorf("feishu ws endpoint: data.url empty")
	}
	return wsURL, nil
}

func (c *FeishuChannel) handleWSMessage(ctx context.Context, msgType int, raw []byte) {
	var payload []byte
	if msgType == websocket.BinaryMessage {
		// The long connection of Feishu uses protobuf binary frames, and the Payload is located in field 8
		pl, frameType, ok := parseFeishuFrame(raw)

		pref := ""
		if len(raw) > 0 {
			n := 32
			if len(raw) < n {
				n = len(raw)
			}
			pref = fmt.Sprintf("%x", raw[:n])
		}
		log.Printf("[feishu] DEBUG parseFrame: ok=%v type=%q payloadLen=%d rawLen=%d rawPrefix=%s", ok, frameType, len(pl), len(raw), pref)

		if !ok || pl == nil {
			log.Printf("[feishu] parseFrame failed (ok=%v pl=%v), rawLen=%d", ok, pl != nil, len(raw))
			return
		}
		if frameType != "event" {
			if frameType != "pong" {
				log.Printf("[feishu] skip frame type=%q (need event)", frameType)
			}
			return
		}
		if len(pl) == 0 {
			return
		}
		payload = pl
	} else {
		payload = raw
	}

	var envelope struct {
		Schema string          `json:"schema"`
		Header json.RawMessage `json:"header"`
		Event  json.RawMessage `json:"event"`
		// Encrypted events
		Encrypt string `json:"encrypt"`
		// Challenge for URL verification
		Challenge string `json:"challenge"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		log.Printf("[feishu] envelope unmarshal err=%v payloadPreview=%s", err, stringTrunc(payload, 200))
		return
	}

	// Handle encrypted messages
	if envelope.Encrypt != "" {
		decrypted, err := c.decryptEvent(envelope.Encrypt)
		if err != nil {
			return
		}
		c.handleWSMessage(ctx, websocket.TextMessage, decrypted)
		return
	}

	// Parse header to determine event type
	var header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
	}
	if envelope.Header != nil {
		_ = json.Unmarshal(envelope.Header, &header)
	}
	log.Printf("[feishu] event: event_type=%q event_id=%s", header.EventType, header.EventID)

	// Dedup by event_id (bounded capacity to prevent memory leak)
	if header.EventID != "" {
		c.dedupMu.Lock()
		if _, seen := c.dedup[header.EventID]; seen {
			c.dedupMu.Unlock()
			log.Printf("[feishu] DEBUG duplicate event_id=%s, skipped", header.EventID)
			return
		}
		c.dedup[header.EventID] = struct{}{}
		c.dedupKeys = append(c.dedupKeys, header.EventID)
		if len(c.dedupKeys) > dedupMaxSize {
			evict := len(c.dedupKeys) / 2
			for _, k := range c.dedupKeys[:evict] {
				delete(c.dedup, k)
			}
			c.dedupKeys = c.dedupKeys[evict:]
		}
		c.dedupMu.Unlock()
	}

	switch header.EventType {
	case "im.message.receive_v1":
		c.handleIMMessage(ctx, envelope.Event)
	default:
		log.Printf("[feishu] unhandled event_type=%q", header.EventType)
	}
}

func (c *FeishuChannel) handleIMMessage(ctx context.Context, raw json.RawMessage) {
	var event struct {
		Sender struct {
			SenderID struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Printf("[feishu] handleIMMessage unmarshal error: %v", err)
		return
	}

	// Only handle text messages
	if event.Message.MessageType != "text" {
		log.Printf("[feishu] skip message_type=%q (need text)", event.Message.MessageType)
		return
	}

	// Ignore bot messages (including self)
	if event.Sender.SenderType == "bot" {
		log.Printf("[feishu] skip bot message: sender_type=%q event_id=%s", event.Sender.SenderType, event.Message.MessageID)
		return
	}

	// Extract text from content JSON
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(event.Message.Content), &content); err != nil {
		return
	}
	text := strings.TrimSpace(content.Text)
	if text == "" {
		return
	}

	senderID := event.Sender.SenderID.OpenID
	if senderID == "" {
		senderID = event.Sender.SenderID.UserID
	}
	if senderID == "" {
		senderID = event.Sender.SenderID.UnionID
	}
	if senderID == "" {
		log.Printf("[feishu] handleIMMessage: no sender_id (open_id/user_id/union_id)")
		return
	}
	if !IsAllowed(c.cfg.AllowFrom, senderID) {
		log.Printf("[feishu] message from %s not in allowFrom (config=%v), ignored", senderID, c.cfg.AllowFrom)
		return
	}

	// Use chat_id for groups, sender open_id for DMs (p2p)
	chatID := senderID
	if event.Message.ChatType == "group" {
		chatID = event.Message.ChatID
	}
	log.Printf("[feishu] inbound: sender=%s chat=%s text=%q", senderID, chatID, stringTrunc([]byte(text), 80))

	// Pre-fetch the token to reduce the waiting time during the response process
	go func() {
		_, _ = c.getTenantToken(context.Background())
	}()

	// Send reaction if configured
	if c.cfg.ReactionEmoji != "" {
		go c.addReaction(context.Background(), event.Message.MessageID, c.cfg.ReactionEmoji)
	}

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "feishu",
		SenderID: senderID,
		ChatID:   chatID,
		Content:  text,
		Metadata: map[string]any{
			"message_id": event.Message.MessageID,
			"chat_type":  event.Message.ChatType,
		},
	})
}

func (c *FeishuChannel) decryptEvent(encrypted string) ([]byte, error) {
	key := c.cfg.EncryptKey
	if key == "" {
		return nil, fmt.Errorf("no encrypt key configured")
	}
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	if len(data) < aes.BlockSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	h := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(h[:])
	if err != nil {
		return nil, err
	}
	iv := data[:aes.BlockSize]
	data = data[aes.BlockSize:]
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(data, data)

	// PKCS7 unpadding
	if len(data) == 0 {
		return nil, fmt.Errorf("empty decrypted data")
	}
	padLen := int(data[len(data)-1])
	if padLen > aes.BlockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	return data[:len(data)-padLen], nil
}

func (c *FeishuChannel) addReaction(ctx context.Context, messageID, emoji string) {
	token, err := c.getTenantToken(ctx)
	if err != nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"reaction_type": map[string]string{"emoji_type": emoji},
	})
	url := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/reactions", messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
}

// --- Send ---

func (c *FeishuChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.AppID) == "" || strings.TrimSpace(c.cfg.AppSecret) == "" {
		return fmt.Errorf("feishu appId/appSecret not configured")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("feishu chat_id is required (open_id)")
	}

	token, err := c.getTenantToken(ctx)
	if err != nil {
		return err
	}

	// Determine receive_id_type based on chat_id prefix
	receiveIDType := "open_id"
	if strings.HasPrefix(msg.ChatID, "oc_") {
		receiveIDType = "chat_id"
	}

	contentBytes, _ := json.Marshal(map[string]string{"text": msg.Content})
	body, _ := json.Marshal(map[string]any{
		"receive_id": msg.ChatID,
		"msg_type":   "text",
		"content":    string(contentBytes),
	})

	url := "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=" + receiveIDType
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu api error: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	return nil
}

// --- Token Management ---

func (c *FeishuChannel) getTenantToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	log.Printf("[feishu] requesting tenant_access_token (appId=%s)", c.cfg.AppID)
	body, _ := json.Marshal(map[string]string{
		"app_id":     c.cfg.AppID,
		"app_secret": c.cfg.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal/", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[feishu] token request failed: %v", err)
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[feishu] token response read failed: %v", err)
		return "", err
	}
	var parsed struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int64  `json:"expire"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		log.Printf("[feishu] token response parse failed: %v, body=%s", err, string(b))
		return "", err
	}
	if parsed.Code != 0 || parsed.TenantAccessToken == "" {
		log.Printf("[feishu] token error: code=%d msg=%s body=%s", parsed.Code, parsed.Msg, string(b))
		return "", fmt.Errorf("feishu token error: %s", parsed.Msg)
	}

	c.mu.Lock()
	c.token = parsed.TenantAccessToken
	if parsed.Expire <= 0 {
		parsed.Expire = 3600
	}
	c.tokenExp = time.Now().Add(time.Duration(parsed.Expire-60) * time.Second)
	c.mu.Unlock()
	return parsed.TenantAccessToken, nil
}

// dedupCleaner periodically trims dedup if it grew large (safety net).
func (c *FeishuChannel) dedupCleaner(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.dedupMu.Lock()
			if len(c.dedupKeys) > dedupMaxSize {
				evict := len(c.dedupKeys) - dedupMaxSize
				for _, k := range c.dedupKeys[:evict] {
					delete(c.dedup, k)
				}
				c.dedupKeys = c.dedupKeys[evict:]
			}
			c.dedupMu.Unlock()
		}
	}
}
