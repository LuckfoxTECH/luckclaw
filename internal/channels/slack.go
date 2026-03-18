package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"

	"github.com/gorilla/websocket"
)

type SlackChannel struct {
	cfg  config.SlackConfig
	bus  *bus.MessageBus
	http *http.Client
}

func NewSlack(cfg config.SlackConfig, b *bus.MessageBus) *SlackChannel {
	return &SlackChannel{
		cfg: cfg,
		bus: b,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *SlackChannel) Name() string { return "slack" }

func (c *SlackChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.BotToken) == "" {
		return fmt.Errorf("slack botToken required")
	}
	if strings.TrimSpace(c.cfg.AppToken) == "" {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := c.connectSocketMode(ctx); err != nil {
			time.Sleep(5 * time.Second)
		}
	}
}

func (c *SlackChannel) Stop(ctx context.Context) error {
	c.http.CloseIdleConnections()
	return nil
}

func (c *SlackChannel) connectSocketMode(ctx context.Context) error {
	// Connect to Slack Socket Mode
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/apps.connections.open", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AppToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var openResp struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &openResp) != nil || !openResp.OK || openResp.URL == "" {
		return fmt.Errorf("slack connections.open failed: %s", openResp.Error)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, openResp.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		c.handleSocketMessage(ctx, conn, data)
	}
}

func (c *SlackChannel) handleSocketMessage(ctx context.Context, conn *websocket.Conn, data []byte) {
	var envelope struct {
		Type       string          `json:"type"`
		EnvelopeID string          `json:"envelope_id"`
		Payload    json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return
	}
	switch envelope.Type {
	case "events_api":
		var payload struct {
			Event json.RawMessage `json:"event"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			return
		}
		c.handleEvent(ctx, conn, envelope.EnvelopeID, payload.Event)
	case "disconnect":
		// Reconnect
	}
}

func (c *SlackChannel) handleEvent(ctx context.Context, conn *websocket.Conn, envelopeID string, raw json.RawMessage) {
	var evt struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		User     string `json:"user"`
		Channel  string `json:"channel"`
		Ts       string `json:"ts"`
		ThreadTs string `json:"thread_ts"`
		BotID    string `json:"bot_id"`
	}
	if json.Unmarshal(raw, &evt) != nil {
		return
	}
	if evt.BotID != "" {
		return
	}
	if evt.Type != "message" && evt.Type != "app_mention" {
		return
	}
	text := strings.TrimSpace(evt.Text)
	if text == "" {
		return
	}
	if !IsAllowed(c.cfg.AllowFrom, evt.User) {
		return
	}
	// Group trigger: app_mention = mentioned; in channels (C/G prefix) apply mention_only/prefixes
	mentioned := evt.Type == "app_mention"
	isChannel := len(evt.Channel) > 0 && (evt.Channel[0] == 'C' || evt.Channel[0] == 'G')
	if isChannel {
		if respond, trimmed := ShouldRespondInGroup(c.cfg.GroupTrigger, mentioned, text); !respond {
			return
		} else {
			text = trimmed
		}
		if text == "" {
			return
		}
	}
	chatID := evt.Channel
	if evt.ThreadTs != "" {
		chatID = evt.Channel + ":" + evt.ThreadTs
	}
	metadata := map[string]any{"ts": evt.Ts, "channel": evt.Channel}
	if evt.ThreadTs != "" {
		metadata["thread_ts"] = evt.ThreadTs
		metadata["session_key_override"] = "slack:" + evt.Channel + ":thread:" + evt.ThreadTs
	}
	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "slack",
		SenderID: evt.User,
		ChatID:   chatID,
		Content:  text,
		Metadata: metadata,
	})
	ack, _ := json.Marshal(map[string]any{"envelope_id": envelopeID})
	_ = conn.WriteMessage(websocket.TextMessage, ack)
}

func (c *SlackChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.BotToken) == "" {
		return fmt.Errorf("slack botToken not configured")
	}
	chatID := msg.ChatID
	threadTs := ""
	if idx := strings.Index(chatID, ":"); idx > 0 {
		chatID, threadTs = chatID[:idx], chatID[idx+1:]
	}
	body := map[string]any{"channel": chatID, "text": msg.Content}
	if c.cfg.ReplyInThread && threadTs != "" {
		body["thread_ts"] = threadTs
	}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.BotToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack api error: %d", resp.StatusCode)
	}
	return nil
}
