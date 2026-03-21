package session

import (
	"path/filepath"
	"regexp"
	"strings"

	"luckclaw/internal/utils"
)

// CompressedHistory is the result of tiered history extraction.
type CompressedHistory struct {
	RecentFull     []map[string]any // Layer 1: full detail
	MiddleNote     string           // Layer 2: key-entity summary
	RecentTokens   int              // tokens used by Layer 1
	MiddleMsgCount int              // messages in Layer 2
}

// GetTieredHistoryByTokens returns token-budgeted recent messages + middle summary.
func (m *Manager) GetTieredHistoryByTokens(
	s *Session,
	recentTokenBudget int,
	maxMiddleChars int,
) CompressedHistory {
	if s == nil || recentTokenBudget <= 0 {
		return CompressedHistory{}
	}
	unconsolidated := s.Messages[s.LastConsolidated:]
	if len(unconsolidated) == 0 {
		return CompressedHistory{}
	}

	// Walk backwards to fill token budget
	var tokensUsed int
	cutIdx := len(unconsolidated)
	for i := len(unconsolidated) - 1; i >= 0; i-- {
		t := utils.EstimateMessageTokens(unconsolidated[i])
		if tokensUsed+t > recentTokenBudget && tokensUsed > 0 {
			break
		}
		tokensUsed += t
		cutIdx = i
	}

	// Split into middle + recent
	middleMsgs := unconsolidated[:cutIdx]
	recentMsgs := unconsolidated[cutIdx:]

	// Align recent to user turn
	for i, msg := range recentMsgs {
		if role, _ := msg["role"].(string); role == "user" {
			recentMsgs = recentMsgs[i:]
			break
		}
	}

	// Extract key entities from middle layer
	var note string
	if len(middleMsgs) > 0 && maxMiddleChars > 0 {
		note = extractKeyEntities(middleMsgs, maxMiddleChars)
	}

	return CompressedHistory{
		RecentFull:     recentMsgs,
		MiddleNote:     note,
		RecentTokens:   tokensUsed,
		MiddleMsgCount: len(middleMsgs),
	}
}

var fileRe = regexp.MustCompile(`(?:^|[\s"'` + "`" + `])([\w./-]+\.\w{1,6})(?:$|[\s"'` + "`" + `.,;:)\]}])`)

// extractKeyEntities extracts key entities, decisions, and constraints
// from messages using heuristic rules (no LLM call).
func extractKeyEntities(messages []map[string]any, maxChars int) string {
	var userIntents []string
	var files []string
	var decisions []string
	var toolsUsed []string
	seen := make(map[string]bool)

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		// Extract file paths from content
		if content != "" {
			for _, match := range fileRe.FindAllStringSubmatch(content, -1) {
				f := match[1]
				key := "file:" + f
				if !seen[key] && len(f) > 2 {
					ext := strings.TrimPrefix(filepath.Ext(f), ".")
					if isRelevantExt(ext) {
						files = append(files, f)
						seen[key] = true
					}
				}
			}
		}

		// Extract tool calls summary
		if role == "assistant" {
			if tcs, ok := msg["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					if tcMap, ok := tc.(map[string]any); ok {
						if fn, ok := tcMap["function"].(map[string]any); ok {
							name, _ := fn["name"].(string)
							if name != "" && !seen["tool:"+name] {
								toolsUsed = append(toolsUsed, name)
								seen["tool:"+name] = true
							}
						}
					}
				}
			}
		}

		// Extract user intent (first ~80 chars of user messages)
		if role == "user" && len(content) > 0 {
			preview := strings.TrimSpace(content)
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			userIntents = append(userIntents, preview)
		}

		// Extract decision-like statements from assistant
		if role == "assistant" && len(content) > 0 {
			lower := strings.ToLower(content)
			decisionKeywords := []string{
				"will", "going to", "decided", "going with",
				"using", "choose", "selected",
				"建议", "决定", "采用", "使用",
			}
			for _, kw := range decisionKeywords {
				if strings.Contains(lower, kw) {
					sentences := splitSentences(content)
					for _, s := range sentences {
						if strings.Contains(strings.ToLower(s), kw) && len(s) < 200 {
							decKey := "dec:" + s
							if !seen[decKey] {
								decisions = append(decisions, s)
								seen[decKey] = true
							}
							break
						}
					}
					break
				}
			}
		}
	}

	// Build summary
	var b strings.Builder
	b.WriteString("[Recent Context Summary]\n")

	if len(userIntents) > 0 {
		start := len(userIntents) - 3
		if start < 0 {
			start = 0
		}
		b.WriteString("- User requests:\n")
		for _, e := range userIntents[start:] {
			b.WriteString("  • " + e + "\n")
		}
	}
	if len(files) > 0 {
		b.WriteString("- Files: " + strings.Join(files, ", ") + "\n")
	}
	if len(decisions) > 0 {
		b.WriteString("- Decisions:\n")
		for _, d := range decisions {
			if len(d) > 150 {
				d = d[:150] + "..."
			}
			b.WriteString("  • " + d + "\n")
		}
	}
	if len(toolsUsed) > 0 {
		b.WriteString("- Tools used: " + strings.Join(toolsUsed, ", ") + "\n")
	}

	result := b.String()
	if len(result) > maxChars {
		result = result[:maxChars-3] + "..."
	}
	// Don't emit empty summary
	if strings.TrimSpace(result) == "[Recent Context Summary]" {
		return ""
	}
	return result
}

func splitSentences(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) > 10 {
			result = append(result, p)
		}
	}
	return result
}

func isRelevantExt(ext string) bool {
	relevant := map[string]bool{
		"go": true, "py": true, "js": true, "ts": true, "tsx": true, "jsx": true,
		"rs": true, "c": true, "cpp": true, "h": true, "java": true, "rb": true,
		"md": true, "txt": true, "json": true, "yaml": true, "yml": true, "toml": true,
		"sh": true, "bash": true, "sql": true, "html": true, "css": true,
		"xml": true, "csv": true, "env": true, "conf": true, "cfg": true, "ini": true,
		"proto": true, "graphql": true, "gql": true,
	}
	return relevant[strings.ToLower(ext)]
}
