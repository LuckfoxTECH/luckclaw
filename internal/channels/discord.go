package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"

	"github.com/gorilla/websocket"
)

type DiscordChannel struct {
	cfg    config.DiscordConfig
	bus    *bus.MessageBus
	http   *http.Client
	webCfg config.WebToolsConfig

	mu        sync.Mutex
	conn      *websocket.Conn
	seq       *int64 // last received sequence number
	selfID    string // bot's own user ID
	sessionID string

	recorder PlaceholderRecorder
	globalUX *config.UXConfig
}

func NewDiscord(cfg config.DiscordConfig, b *bus.MessageBus, webCfg config.WebToolsConfig) *DiscordChannel {
	transport := &http.Transport{Proxy: webCfg.ProxyFunc()}
	return &DiscordChannel{
		cfg:    cfg,
		bus:    b,
		http:   &http.Client{Timeout: 30 * time.Second, Transport: transport},
		webCfg: webCfg,
	}
}

func (c *DiscordChannel) Name() string { return "discord" }

// SetPlaceholderRecorder injects the recorder for Typing/Placeholder UX.
func (c *DiscordChannel) SetPlaceholderRecorder(r PlaceholderRecorder) {
	c.recorder = r
}

// SetGlobalUX implements GlobalUXSetter.
func (c *DiscordChannel) SetGlobalUX(ux *config.UXConfig) {
	c.globalUX = ux
}

func (c *DiscordChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("discord token is not configured")
	}
	gatewayURL := c.cfg.GatewayURL
	if gatewayURL == "" {
		gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := c.connectAndRun(ctx, gatewayURL); err != nil {
			log.Printf("[discord] connection failed: %v", err)
			backoff := time.Duration(2+rand.Intn(3)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (c *DiscordChannel) Stop(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
	c.http.CloseIdleConnections()
	return nil
}

// --- Gateway WebSocket ---

type gatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

func (c *DiscordChannel) connectAndRun(ctx context.Context, gatewayURL string) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            c.webCfg.ProxyFunc(),
	}
	conn, _, err := dialer.DialContext(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("discord gateway dial: %w", err)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	// Read Hello (op 10)
	var hello gatewayPayload
	if err := conn.ReadJSON(&hello); err != nil {
		return fmt.Errorf("discord read hello: %w", err)
	}
	if hello.Op != 10 {
		return fmt.Errorf("discord expected op 10 hello, got op %d", hello.Op)
	}

	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &helloData); err != nil {
		return fmt.Errorf("discord parse hello: %w", err)
	}
	heartbeatMs := helloData.HeartbeatInterval
	if heartbeatMs <= 0 {
		heartbeatMs = 41250
	}

	// Send Identify (op 2)
	intents := c.cfg.Intents
	if intents == 0 {
		intents = 37377 // GUILDS | GUILD_MESSAGES | DIRECT_MESSAGES | MESSAGE_CONTENT
	}
	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   c.cfg.Token,
			"intents": intents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "luckclaw",
				"device":  "luckclaw",
			},
		},
	}
	if err := conn.WriteJSON(identify); err != nil {
		return fmt.Errorf("discord send identify: %w", err)
	}

	// Start heartbeat loop
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	go c.heartbeatLoop(heartbeatCtx, conn, time.Duration(heartbeatMs)*time.Millisecond)

	// Read dispatch events
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var payload gatewayPayload
		if err := conn.ReadJSON(&payload); err != nil {
			return fmt.Errorf("discord read: %w", err)
		}

		// Track sequence number
		if payload.S != nil {
			c.mu.Lock()
			c.seq = payload.S
			c.mu.Unlock()
		}

		switch payload.Op {
		case 0: // Dispatch
			c.handleDispatch(ctx, payload)
		case 1: // Heartbeat request
			c.sendHeartbeat(conn)
		case 7: // Reconnect
			return fmt.Errorf("discord server requested reconnect")
		case 9: // Invalid Session
			time.Sleep(time.Duration(1+rand.Intn(5)) * time.Second)
			return fmt.Errorf("discord invalid session")
		case 11: // Heartbeat ACK
			// OK
		}
	}
}

func (c *DiscordChannel) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	// First heartbeat after a random jitter
	jitter := time.Duration(rand.Int63n(int64(interval)))
	select {
	case <-time.After(jitter):
	case <-ctx.Done():
		return
	}
	c.sendHeartbeat(conn)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sendHeartbeat(conn)
		}
	}
}

func (c *DiscordChannel) sendHeartbeat(conn *websocket.Conn) {
	c.mu.Lock()
	seq := c.seq
	c.mu.Unlock()

	payload := map[string]any{"op": 1, "d": seq}
	_ = conn.WriteJSON(payload)
}

func (c *DiscordChannel) handleDispatch(ctx context.Context, payload gatewayPayload) {
	switch payload.T {
	case "READY":
		var ready struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(payload.D, &ready); err == nil {
			c.mu.Lock()
			c.selfID = ready.User.ID
			c.sessionID = ready.SessionID
			c.mu.Unlock()
			log.Printf("[discord] connected as %s (ID: %s)", ready.User.Username, ready.User.ID)
		}

	case "MESSAGE_CREATE":
		c.handleMessageCreate(ctx, payload.D)
	}
}

func (c *DiscordChannel) handleMessageCreate(ctx context.Context, raw json.RawMessage) {
	var msg struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
		Content   string `json:"content"`
		Author    struct {
			ID  string `json:"id"`
			Bot bool   `json:"bot"`
		} `json:"author"`
		Mentions []struct {
			ID string `json:"id"`
		} `json:"mentions"`
		Attachments []struct {
			ID          string `json:"id"`
			Filename    string `json:"filename"`
			URL         string `json:"url"`
			ProxyURL    string `json:"proxy_url"`
			ContentType string `json:"content_type"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// Ignore bot messages (including self)
	if msg.Author.Bot {
		return
	}

	c.mu.Lock()
	selfID := c.selfID
	c.mu.Unlock()

	// AllowFrom check
	if !IsAllowed(c.cfg.AllowFrom, msg.Author.ID) {
		return
	}

	content := strings.TrimSpace(msg.Content)
	var media []string
	for _, att := range msg.Attachments {
		urlStr := att.ProxyURL
		if urlStr == "" {
			urlStr = att.URL
		}
		if urlStr == "" {
			continue
		}
		ct := strings.ToLower(att.ContentType)
		if strings.HasPrefix(ct, "image/") {
			media = append(media, urlStr)
		} else if content == "" {
			content = "[Attachment: " + att.Filename + " " + urlStr + "]"
		} else {
			content += "\n[Attachment: " + att.Filename + " " + urlStr + "]"
		}
	}
	if content == "" && len(media) == 0 {
		return
	}
	if content == "" && len(media) > 0 {
		content = "[Image]"
	}

	// Group trigger: in guilds, apply mention_only / prefixes
	if msg.GuildID != "" {
		mentioned := false
		for _, m := range msg.Mentions {
			if m.ID == selfID {
				mentioned = true
				break
			}
		}
		if mentioned {
			content = strings.ReplaceAll(content, "<@"+selfID+">", "")
			content = strings.ReplaceAll(content, "<@!"+selfID+">", "")
			content = strings.TrimSpace(content)
		}
		// Use GroupTrigger if configured; else fall back to GroupPolicy
		gt := c.cfg.GroupTrigger
		if len(gt.Prefixes) == 0 && !gt.MentionOnly {
			policy := strings.ToLower(strings.TrimSpace(c.cfg.GroupPolicy))
			if policy == "mention" {
				gt.MentionOnly = true
			}
			if policy == "" && !mentioned {
				gt.MentionOnly = true
			}
		}
		if respond, trimmed := ShouldRespondInGroup(gt, mentioned, content); !respond {
			return
		} else {
			content = trimmed
		}
		if content == "" && len(media) == 0 {
			return
		}
	}

	c.maybeStartTypingAndPlaceholder(ctx, msg.ChannelID)

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "discord",
		SenderID: msg.Author.ID,
		ChatID:   msg.ChannelID,
		Content:  content,
		Media:    media,
		Metadata: map[string]any{
			"message_id": msg.ID,
			"guild_id":   msg.GuildID,
		},
	})
}

// --- REST API (Send / Edit) ---

func (c *DiscordChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return c.sendWithRetry(ctx, msg, 3)
}

func (c *DiscordChannel) sendWithRetry(ctx context.Context, msg bus.OutboundMessage, maxRetries int) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("discord token is not configured")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("discord chat_id is required (use channel ID)")
	}

	apiURL := "https://discord.com/api/v10/channels/" + msg.ChatID + "/messages"
	content := msg.Content
	chunks := splitMessage(content, 2000)

	// If we have media, send content + all files in one multipart request
	if len(msg.Media) > 0 {
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		payloadBytes, _ := json.Marshal(map[string]any{"content": content})
		_ = mw.WriteField("payload_json", string(payloadBytes))
		for i, path := range msg.Media {
			data, err := os.ReadFile(path)
			if err != nil {
				log.Printf("[discord] Send: read media %s: %v", path, err)
				continue
			}
			if len(data) > 25*1024*1024 {
				log.Printf("[discord] Send: file too large %s (>25MB)", path)
				continue
			}
			fw, _ := mw.CreateFormFile("files["+strconv.Itoa(i)+"]", filepath.Base(path))
			_, _ = fw.Write(data)
		}
		_ = mw.Close()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+c.cfg.Token)
		req.Header.Set("Content-Type", mw.FormDataContentType())

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("[discord] Send: ok channel=%s with %d file(s)", msg.ChatID, len(msg.Media))
		} else {
			log.Printf("[discord] Send: failed status=%d body=%s", resp.StatusCode, string(respBody))
			return fmt.Errorf("discord api error: status=%d", resp.StatusCode)
		}
		return nil
	}

	for _, chunk := range chunks {
		body, _ := json.Marshal(map[string]any{"content": chunk})

		for attempt := 0; attempt <= maxRetries; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bot "+c.cfg.Token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := c.http.Do(req)
			if err != nil {
				log.Printf("[discord] Send: request error channel=%s: %v", msg.ChatID, err)
				return err
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == 429 && attempt < maxRetries {
				retryAfter := parseRetryAfter(respBody)
				log.Printf("[discord] Send: rate limited channel=%s, retry after %v", msg.ChatID, retryAfter)
				select {
				case <-time.After(retryAfter):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				err := fmt.Errorf("discord api error: status=%d body=%s", resp.StatusCode, string(respBody))
				log.Printf("[discord] Send: failed channel=%s: %v", msg.ChatID, err)
				return err
			}
			var parsed struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(respBody, &parsed)
			preview := chunk
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			if parsed.ID != "" {
				log.Printf("[discord] Send: ok channel=%s msgId=%s len=%d content=%q", msg.ChatID, parsed.ID, len(chunk), preview)
			} else {
				log.Printf("[discord] Send: ok channel=%s len=%d content=%q", msg.ChatID, len(chunk), preview)
			}
			break
		}
	}
	return nil
}

func (c *DiscordChannel) SendAndTrack(ctx context.Context, msg bus.OutboundMessage) (string, error) {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return "", fmt.Errorf("discord token is not configured")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return "", fmt.Errorf("discord chat_id is required")
	}

	content := msg.Content
	if len(content) > 2000 {
		content = content[:2000]
	}
	body, _ := json.Marshal(map[string]any{"content": content})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://discord.com/api/v10/channels/"+msg.ChatID+"/messages",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord api error: status=%d", resp.StatusCode)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}

func (c *DiscordChannel) EditMessage(ctx context.Context, chatID, messageID, newContent string) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("discord token is not configured")
	}
	if len(newContent) > 2000 {
		newContent = newContent[:2000]
	}
	body, _ := json.Marshal(map[string]any{"content": newContent})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		"https://discord.com/api/v10/channels/"+chatID+"/messages/"+messageID,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord edit error: status=%d", resp.StatusCode)
	}
	return nil
}

func parseRetryAfter(body []byte) time.Duration {
	var data struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &data); err == nil && data.RetryAfter > 0 {
		return time.Duration(data.RetryAfter*1000) * time.Millisecond
	}
	return 2 * time.Second
}

// maybeStartTypingAndPlaceholder triggers Typing and Placeholder before publishing inbound.
func (c *DiscordChannel) maybeStartTypingAndPlaceholder(ctx context.Context, channelID string) {
	if c.recorder == nil {
		return
	}
	typingEnabled := c.cfg.Typing || (c.globalUX != nil && c.globalUX.Typing)
	if typingEnabled {
		if stop, err := c.StartTyping(ctx, channelID, nil); err == nil {
			c.recorder.RecordTypingStop("discord", channelID, stop)
		}
	}
	placeholderEnabled := c.cfg.Placeholder.Enabled || (c.globalUX != nil && c.globalUX.Placeholder.Enabled)
	if placeholderEnabled {
		if phID, err := c.SendPlaceholder(ctx, channelID, nil); err == nil && phID != "" {
			c.recorder.RecordPlaceholder("discord", channelID, phID)
		}
	}
}

// StartTyping implements TypingCapable. Discord typing lasts ~10s, so we repeat every 8s.
func (c *DiscordChannel) StartTyping(ctx context.Context, channelID string, metadata map[string]any) (func(), error) {
	c.sendTypingOnce(ctx, channelID)
	typingCtx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				c.sendTypingOnce(typingCtx, channelID)
			}
		}
	}()
	return cancel, nil
}

func (c *DiscordChannel) sendTypingOnce(ctx context.Context, channelID string) {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://discord.com/api/v10/channels/"+channelID+"/typing", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bot "+c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// SendPlaceholder implements PlaceholderCapable.
func (c *DiscordChannel) SendPlaceholder(ctx context.Context, channelID string, metadata map[string]any) (string, error) {
	text := c.cfg.Placeholder.Text
	if c.globalUX != nil && c.globalUX.Placeholder.Text != "" {
		text = c.globalUX.Placeholder.Text
	}
	if text == "" {
		text = "Thinking... 💭"
	}
	body, _ := json.Marshal(map[string]any{"content": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://discord.com/api/v10/channels/"+channelID+"/messages",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord send placeholder failed: status=%d", resp.StatusCode)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}

// getGatewayURL fetches the gateway URL from Discord API.
func (c *DiscordChannel) getGatewayURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://discord.com/api/v10/gateway/bot", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("discord gateway/bot error: status=%d body=%s",
			resp.StatusCode, strconv.Quote(string(body)))
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	if data.URL == "" {
		return "", fmt.Errorf("discord returned empty gateway URL")
	}
	return data.URL + "?v=10&encoding=json", nil
}
