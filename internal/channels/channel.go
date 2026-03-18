package channels

import (
	"context"
	"strings"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"
)

// TypingCapable — channels that can show a typing/thinking indicator.
// StartTyping begins the indicator and returns a stop function.
// metadata may be nil; used for platform-specific params (e.g. thread_id).
type TypingCapable interface {
	StartTyping(ctx context.Context, chatID string, metadata map[string]any) (stop func(), err error)
}

// PlaceholderCapable — channels that can send a placeholder message
// (e.g. "Thinking... 💭") that will later be edited to the actual response.
// metadata may be nil; used for platform-specific params (e.g. thread_id).
type PlaceholderCapable interface {
	SendPlaceholder(ctx context.Context, chatID string, metadata map[string]any) (messageID string, err error)
}

// PlaceholderRecorder is injected into channels by Manager.
// Channels call these on inbound to register typing/placeholder state.
// Manager uses the state on outbound to stop typing and edit placeholders.
type PlaceholderRecorder interface {
	RecordPlaceholder(channel, chatID, placeholderID string)
	RecordTypingStop(channel, chatID string, stop func())
}

// GlobalUXSetter is optionally implemented by channels that support global typing/placeholder.
// When set, global UX overrides or augments per-channel config.
type GlobalUXSetter interface {
	SetGlobalUX(ux *config.UXConfig)
}

type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, msg bus.OutboundMessage) error
}

// IsAllowed checks if a sender is permitted by the allowFrom list.
func IsAllowed(allowFrom []string, senderID string) bool {
	if len(allowFrom) == 0 {
		return false
	}
	senderID = strings.TrimSpace(senderID)
	for _, entry := range allowFrom {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			return true
		}
		for _, part := range strings.Split(entry, "|") {
			if strings.TrimSpace(part) == senderID {
				return true
			}
		}
	}
	return false
}

// EditableChannel is optionally implemented by channels that support
// editing a previously sent message (e.g. Telegram editMessageText).
type EditableChannel interface {
	Channel
	// SendAndTrack sends a message and returns the platform message ID
	// so it can be edited later.
	SendAndTrack(ctx context.Context, msg bus.OutboundMessage) (messageID string, err error)
	// EditMessage updates a previously sent message identified by messageID.
	EditMessage(ctx context.Context, chatID, messageID, newContent string) error
}

// splitMessage breaks long content into chunks at natural boundaries.
func splitMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}
	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			chunks = append(chunks, content)
			break
		}
		cut := maxLen
		if idx := strings.LastIndex(content[:cut], "\n"); idx > maxLen/2 {
			cut = idx + 1
		} else if idx := strings.LastIndex(content[:cut], " "); idx > maxLen/2 {
			cut = idx + 1
		}
		chunks = append(chunks, content[:cut])
		content = content[cut:]
	}
	return chunks
}
