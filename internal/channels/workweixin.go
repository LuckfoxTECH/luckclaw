package channels

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"

	"github.com/gorilla/websocket"
)

func genReqID(prefix string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return prefix + "_" + string(b)
}

const (
	workWeixinWSURL = "wss://openws.work.weixin.qq.com"
	wsCmdSubscribe  = "aibot_subscribe"
	wsCmdHeartbeat  = "ping"
	wsCmdCallback   = "aibot_msg_callback"
	wsCmdEvent      = "aibot_event_callback"
	wsCmdResponse   = "aibot_respond_msg"
	wsCmdSendMsg    = "aibot_send_msg"
)

type WorkWeixinChannel struct {
	cfg  config.WorkWeixinConfig
	bus  *bus.MessageBus
	http *http.Client

	conn   *websocket.Conn
	connMu sync.RWMutex
}

func NewWorkWeixin(cfg config.WorkWeixinConfig, b *bus.MessageBus, webCfg config.WebToolsConfig) *WorkWeixinChannel {
	transport := &http.Transport{Proxy: webCfg.ProxyFunc()}
	return &WorkWeixinChannel{
		cfg:  cfg,
		bus:  b,
		http: &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}
}

func (c *WorkWeixinChannel) Name() string { return "workweixin" }

func (c *WorkWeixinChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.BotID) == "" || strings.TrimSpace(c.cfg.Secret) == "" {
		return fmt.Errorf("workweixin botId/secret required")
	}
	log.Printf("[workweixin] starting (botId=%s), allowFrom=%v", c.cfg.BotID, c.cfg.AllowFrom)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[workweixin] context cancelled, stopping")
			return nil
		default:
		}

		err := c.connect(ctx)
		if err != nil {
			log.Printf("[workweixin] connection failed: %v, retrying in 5s", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (c *WorkWeixinChannel) Stop(ctx context.Context) error {
	return nil
}

func (c *WorkWeixinChannel) connect(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, workWeixinWSURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	defer func() {
		c.connMu.Lock()
		c.conn = nil
		c.connMu.Unlock()
		conn.Close()
	}()
	log.Printf("[workweixin] WebSocket connected, sending auth")

	botID := strings.TrimSpace(c.cfg.BotID)
	secret := strings.TrimSpace(c.cfg.Secret)
	reqID := genReqID(wsCmdSubscribe)
	authBody := map[string]any{
		"cmd": wsCmdSubscribe,
		"headers": map[string]string{
			"req_id": reqID,
		},
		"body": map[string]string{
			"bot_id": botID,
			"secret": secret,
		},
	}
	if err := conn.WriteJSON(authBody); err != nil {
		return fmt.Errorf("auth send: %w", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("auth read: %w", err)
	}
	var authResp struct {
		Headers struct {
			ReqID string `json:"req_id"`
		} `json:"headers"`
		Errcode int    `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}
	if json.Unmarshal(data, &authResp) != nil {
		return fmt.Errorf("auth parse: %s", string(data))
	}
	if authResp.Errcode != 0 {
		hint := ""
		if authResp.Errcode == 600041 {
			hint = "(Please verify that the Bot ID and Secret are correct and free of any extra spaces; the Secret should be obtained from the 'Intelligent Robot' - 'API Mode' page, not from the application Secret.)"
		}
		return fmt.Errorf("auth failed: errcode=%d errmsg=%s%s", authResp.Errcode, authResp.Errmsg, hint)
	}
	log.Printf("[workweixin] authenticated, listening for messages")

	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ticker.C:
				hbReqID := genReqID(wsCmdHeartbeat)
				_ = conn.WriteJSON(map[string]any{
					"cmd":     wsCmdHeartbeat,
					"headers": map[string]string{"req_id": hbReqID},
				})
			}
		}
	}()
	defer close(heartbeatDone)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[workweixin] read error: %v", err)
			return err
		}

		var frame struct {
			Cmd     string `json:"cmd"`
			Headers struct {
				ReqID string `json:"req_id"`
			} `json:"headers"`
			Body json.RawMessage `json:"body"`
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}

		reqID := frame.Headers.ReqID

		if frame.Cmd == "" {
			var ack struct {
				Errcode int `json:"errcode"`
			}
			_ = json.Unmarshal(data, &ack)
			if ack.Errcode != 0 {
				log.Printf("[workweixin] ack errcode=%d", ack.Errcode)
			}
			continue
		}

		switch frame.Cmd {
		case wsCmdCallback:
			c.handleMessage(ctx, conn, reqID, frame.Body)
		case wsCmdEvent:
			continue
		default:
			continue
		}
	}
}

func (c *WorkWeixinChannel) handleMessage(ctx context.Context, _ *websocket.Conn, reqID string, body []byte) {
	var msg struct {
		MsgID    string `json:"msgid"`
		Msgtype  string `json:"msgtype"`
		ChatID   string `json:"chatid"`
		Chattype string `json:"chattype"`
		From     struct {
			UserID string `json:"userid"`
		} `json:"from"`
		Text struct {
			Content string `json:"content"`
		} `json:"text"`
		Voice struct {
			Content string `json:"content"`
		} `json:"voice"`
	}
	if json.Unmarshal(body, &msg) != nil {
		return
	}

	senderID := msg.From.UserID
	if !IsAllowed(c.cfg.AllowFrom, senderID) {
		log.Printf("[workweixin] message from %s not in allowFrom, ignored", senderID)
		return
	}

	chatID := msg.ChatID
	if msg.Chattype == "single" || chatID == "" {
		chatID = senderID
	}

	content := strings.TrimSpace(msg.Text.Content)
	if msg.Msgtype == "voice" {
		content = strings.TrimSpace(msg.Voice.Content)
	}
	if content == "" {
		return
	}

	log.Printf("[workweixin] inbound: sender=%s chat=%s text=%q", senderID, chatID, truncStr(content, 80))

	c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "workweixin",
		SenderID: senderID,
		ChatID:   chatID,
		Content:  content,
		Metadata: map[string]any{
			"req_id":   reqID,
			"msgid":    msg.MsgID,
			"chattype": msg.Chattype,
		},
	})
}

func (c *WorkWeixinChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.BotID) == "" || strings.TrimSpace(c.cfg.Secret) == "" {
		return fmt.Errorf("workweixin botId/secret not configured")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("workweixin chat_id required")
	}

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("workweixin WebSocket not connected")
	}

	if len(msg.Media) > 0 {
		for _, path := range msg.Media {
			if err := c.sendImage(ctx, conn, msg.ChatID, path); err != nil {
				log.Printf("[workweixin] sendImage failed: %v", err)
			}
		}
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return nil
	}

	reqID := genReqID(wsCmdSendMsg)
	body := map[string]any{
		"chatid":   msg.ChatID,
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": content},
	}
	frame := map[string]any{
		"cmd":     wsCmdSendMsg,
		"headers": map[string]string{"req_id": reqID},
		"body":    body,
	}

	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := conn.WriteJSON(frame); err != nil {
		return fmt.Errorf("workweixin send: %w", err)
	}
	conn.SetWriteDeadline(time.Time{})
	log.Printf("[workweixin] sent to %s: %s", msg.ChatID, truncStr(content, 60))
	return nil
}

// The WebSocket API of the Enterprise WeChat intelligent robot supports sending images via base64 encoding.
func (c *WorkWeixinChannel) sendImage(_ context.Context, conn *websocket.Conn, chatID, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read image file: %w", err)
	}
	if len(data) > 10*1024*1024 {
		return fmt.Errorf("image file too large (>10MB)")
	}

	hash := md5.Sum(data)
	md5Str := fmt.Sprintf("%x", hash)

	base64Str := base64.StdEncoding.EncodeToString(data)

	reqID := genReqID(wsCmdSendMsg)
	body := map[string]any{
		"chatid":  chatID,
		"msgtype": "image",
		"image": map[string]string{
			"base64": base64Str,
			"md5":    md5Str,
		},
	}
	frame := map[string]any{
		"cmd":     wsCmdSendMsg,
		"headers": map[string]string{"req_id": reqID},
		"body":    body,
	}

	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := conn.WriteJSON(frame); err != nil {
		return fmt.Errorf("workweixin send image: %w", err)
	}
	conn.SetWriteDeadline(time.Time{}) // clear deadline
	log.Printf("[workweixin] sent image to %s: %s (%d bytes)", chatID, filepath.Base(path), len(data))
	return nil
}
