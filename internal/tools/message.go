package tools

import (
	"context"
	"fmt"
	"sync"

	"luckclaw/internal/bus"
)

type MessageTool struct {
	Bus              *bus.MessageBus
	DefaultChannel   string
	DefaultChatID    string
	DefaultMessageID string

	mu         sync.Mutex
	sentInTurn bool
}

func (t *MessageTool) StartTurn() {
	t.mu.Lock()
	t.sentInTurn = false
	t.mu.Unlock()
}

func (t *MessageTool) SentInTurn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sentInTurn
}

func (t *MessageTool) Name() string { return "message" }

func (t *MessageTool) Description() string {
	return "Send a message to a specific channel and chat. Use this to proactively communicate with users across different channels."
}

func (t *MessageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The message content to send.",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Target channel name (e.g. telegram, discord, slack). Defaults to the current channel.",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Target chat/thread ID. Defaults to the current chat.",
			},
			"media": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional: list of file paths to attach (images, audio, documents)",
			},
		},
		"required": []string{"content"},
	}
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	content, _ := args["content"].(string)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}

	channel, _ := args["channel"].(string)
	if channel == "" {
		channel = t.DefaultChannel
	}
	chatID, _ := args["chat_id"].(string)
	if chatID == "" {
		chatID = t.DefaultChatID
	}

	var media []string
	if m, ok := args["media"]; ok {
		if arr, ok := m.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					media = append(media, s)
				}
			}
		}
	}

	if t.Bus == nil {
		return "Message not sent: no message bus configured", nil
	}

	err := t.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: content,
		Media:   media,
		Metadata: map[string]any{
			"message_id": t.DefaultMessageID,
		},
	})
	if err != nil {
		return "", err
	}

	if channel == t.DefaultChannel && chatID == t.DefaultChatID {
		t.mu.Lock()
		t.sentInTurn = true
		t.mu.Unlock()
	}

	mediaInfo := ""
	if len(media) > 0 {
		mediaInfo = fmt.Sprintf(" with %d attachments", len(media))
	}
	return fmt.Sprintf("Message sent to %s:%s%s", channel, chatID, mediaInfo), nil
}
