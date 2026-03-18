package routing

import (
	"strings"
	"unicode/utf8"
)

const lookbackWindow = 6

// Features holds structural signals for complexity scoring.
// All dimensions are language-agnostic.
type Features struct {
	TokenEstimate     int
	CodeBlockCount    int
	RecentToolCalls   int
	ConversationDepth int
	HasAttachments    bool
	AsksForHistory    bool
}

// ExtractFeatures computes the feature vector from message and history.
// history: []map[string]any with "role", "content", "tool_calls" (optional)
func ExtractFeatures(msg string, history []map[string]any) Features {
	return Features{
		TokenEstimate:     estimateTokens(msg),
		CodeBlockCount:    countCodeBlocks(msg),
		RecentToolCalls:   countRecentToolCalls(history),
		ConversationDepth: len(history),
		HasAttachments:    hasAttachments(msg),
		AsksForHistory:    looksLikeHistoryQuery(msg),
	}
}

func estimateTokens(msg string) int {
	total := utf8.RuneCountInString(msg)
	if total == 0 {
		return 0
	}
	cjk := 0
	for _, r := range msg {
		if r >= 0x2E80 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0xAC00 && r <= 0xD7AF {
			cjk++
		}
	}
	return cjk + (total-cjk)/4
}

func countCodeBlocks(msg string) int {
	n := strings.Count(msg, "```")
	return n / 2
}

func countRecentToolCalls(history []map[string]any) int {
	start := len(history) - lookbackWindow
	if start < 0 {
		start = 0
	}
	count := 0
	for _, m := range history[start:] {
		if tc, ok := m["tool_calls"]; ok && tc != nil {
			if arr, ok := tc.([]any); ok {
				count += len(arr)
			}
		}
	}
	return count
}

func hasAttachments(msg string) bool {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "data:image/") ||
		strings.Contains(lower, "data:audio/") ||
		strings.Contains(lower, "data:video/") {
		return true
	}
	mediaExts := []string{
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp",
		".mp3", ".wav", ".ogg", ".m4a", ".flac",
		".mp4", ".avi", ".mov", ".webm",
	}
	for _, ext := range mediaExts {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

func looksLikeHistoryQuery(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}
	keywords := []string{
		"history", "previous", "earlier", "last time", "recap", "remind me", "what did we",
		"memory", "remember", "you said",
		"历史", "记忆", "回顾", "复盘", "总结一下", "之前", "上次", "刚才", "前面", "前文", "你还记得", "你说过",
	}
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}
