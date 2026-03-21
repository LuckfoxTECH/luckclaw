package utils

const (
	CharsPerToken      = 4
	OverheadPerMessage = 10
)

func EstimateTokens(messages []map[string]any) int {
	var total int
	for _, m := range messages {
		if m == nil {
			continue
		}
		content, _ := m["content"].(string)
		if content != "" {
			total += len(content) / CharsPerToken
		}
		if toolCalls, ok := m["tool_calls"].([]any); ok && len(toolCalls) > 0 {
			for _, tc := range toolCalls {
				if tcMap, ok := tc.(map[string]any); ok {
					if fn, ok := tcMap["function"].(map[string]any); ok {
						if args, ok := fn["arguments"].(string); ok {
							total += len(args) / CharsPerToken
						}
						if name, ok := fn["name"].(string); ok {
							total += len(name) / CharsPerToken
						}
					}
				}
			}
		}
		total += OverheadPerMessage
	}
	return total
}

func EstimateStringTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s) / CharsPerToken
}

// EstimateMessageTokens estimates tokens for a single message map.
func EstimateMessageTokens(msg map[string]any) int {
	if msg == nil {
		return 0
	}
	total := OverheadPerMessage
	if content, ok := msg["content"].(string); ok && content != "" {
		total += len(content) / CharsPerToken
	}
	if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]any); ok {
				if fn, ok := tcMap["function"].(map[string]any); ok {
					if args, ok := fn["arguments"].(string); ok {
						total += len(args) / CharsPerToken
					}
					if name, ok := fn["name"].(string); ok {
						total += len(name) / CharsPerToken
					}
				}
			}
		}
	}
	return total
}
