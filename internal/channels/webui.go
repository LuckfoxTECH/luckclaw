package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"luckclaw/internal/bus"

	"github.com/gorilla/websocket"
)

// WebUIHub holds WebSocket connections for the WebUI channel.
type WebUIHub struct {
	mu          sync.RWMutex
	connections map[string][]*websocket.Conn // session_id -> conns
}

// NewWebUIHub creates a new WebUI hub.
func NewWebUIHub() *WebUIHub {
	return &WebUIHub{
		connections: make(map[string][]*websocket.Conn),
	}
}

// Register adds a WebSocket connection for the given session.
func (h *WebUIHub) Register(sessionID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connections[sessionID] = append(h.connections[sessionID], conn)
}

// Unregister removes a connection.
func (h *WebUIHub) Unregister(sessionID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.connections[sessionID]
	for i, c := range conns {
		if c == conn {
			h.connections[sessionID] = append(conns[:i], conns[i+1:]...)
			if len(h.connections[sessionID]) == 0 {
				delete(h.connections, sessionID)
			}
			break
		}
	}
}

// SendToSession sends a JSON message to all connections for the session.
func (h *WebUIHub) SendToSession(sessionID string, msg any) {
	h.mu.RLock()
	conns := h.connections[sessionID]
	if len(conns) > 0 {
		conns = append([]*websocket.Conn{}, conns...)
	}
	h.mu.RUnlock()

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	for _, ch := range conns {
		if err := ch.WriteMessage(websocket.TextMessage, payload); err != nil {
			log.Printf("[webui] write error: %v", err)
		}
	}
}

// WebUIChannel implements Channel for WebUI WebSocket clients.
type WebUIChannel struct {
	hub       *WebUIHub
	bus       *bus.MessageBus
	workspace string // absolute path for resolving media URLs
	mu        sync.Mutex
	idSeq     int
}

// NewWebUI creates a WebUI channel. workspace is the absolute workspace path for resolving media file URLs.
func NewWebUI(hub *WebUIHub, b *bus.MessageBus, workspace string) *WebUIChannel {
	return &WebUIChannel{hub: hub, bus: b, workspace: workspace}
}

func (c *WebUIChannel) Name() string { return "webui" }

func (c *WebUIChannel) Start(ctx context.Context) error { return nil }

func (c *WebUIChannel) Stop(ctx context.Context) error { return nil }

func (c *WebUIChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	payload := map[string]any{"type": "message", "content": msg.Content}
	if len(msg.Media) > 0 && c.workspace != "" {
		media := make([]map[string]string, 0, len(msg.Media))
		for _, p := range msg.Media {
			rel, err := filepath.Rel(c.workspace, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			media = append(media, map[string]string{
				"url":  "/api/luckclaw/workspace/file?path=" + url.QueryEscape(filepath.ToSlash(rel)),
				"name": filepath.Base(p),
			})
		}
		if len(media) > 0 {
			payload["media"] = media
		}
	}
	c.hub.SendToSession(msg.ChatID, payload)
	return nil
}

// SendAndTrack implements EditableChannel for tool progress.
func (c *WebUIChannel) SendAndTrack(ctx context.Context, msg bus.OutboundMessage) (string, error) {
	c.mu.Lock()
	c.idSeq++
	id := fmt.Sprintf("p%d", c.idSeq)
	c.mu.Unlock()

	if msg.Metadata != nil {
		if p, ok := msg.Metadata["progress"].(bus.ToolProgressInfo); ok {
			c.hub.SendToSession(msg.ChatID, map[string]any{
				"type":     "tool_progress",
				"content":  msg.Content,
				"progress": p,
				"msg_id":   id,
			})
			return id, nil
		}
	}
	c.hub.SendToSession(msg.ChatID, map[string]any{
		"type":    "tool_progress",
		"content": msg.Content,
		"msg_id":  id,
	})
	return id, nil
}

// EditMessage updates tool progress (sends as new progress message).
func (c *WebUIChannel) EditMessage(ctx context.Context, chatID, messageID, newContent string) error {
	c.hub.SendToSession(chatID, map[string]any{
		"type":    "tool_progress",
		"content": newContent,
		"msg_id":  messageID,
		"update":  true,
	})
	return nil
}
