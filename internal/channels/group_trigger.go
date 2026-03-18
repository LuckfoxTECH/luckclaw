package channels

import (
	"strings"

	"luckclaw/internal/config"
)

// ShouldRespondInGroup determines if the bot should respond in a group chat.
// Returns (shouldRespond, trimmedContent).
// - If isMentioned → always respond.
// - If MentionOnly and not mentioned → ignore.
// - If Prefixes set and content starts with any prefix → respond, strip prefix.
// - If Prefixes set but no match and not mentioned → ignore.
// - Otherwise (no group_trigger) → respond to all.
func ShouldRespondInGroup(gt config.GroupTriggerConfig, isMentioned bool, content string) (bool, string) {
	if isMentioned {
		return true, strings.TrimSpace(content)
	}
	if gt.MentionOnly {
		return false, content
	}
	if len(gt.Prefixes) > 0 {
		for _, prefix := range gt.Prefixes {
			if prefix != "" && strings.HasPrefix(content, prefix) {
				return true, strings.TrimSpace(strings.TrimPrefix(content, prefix))
			}
		}
		return false, content
	}
	return true, strings.TrimSpace(content)
}
