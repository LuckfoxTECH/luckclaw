package config

import (
	"strings"
)

// ContextWindowForModel returns the context window size for a model.
// Priority: config.Models.ContextWindow override > static table > 0 (unknown).
// Model IDs are matched case-insensitively; provider prefix is stripped for lookup
// (e.g. "zhipu/glm-4" -> "glm-4").
func (c Config) ContextWindowForModel(model string) int {
	m := strings.TrimSpace(model)
	if m == "" {
		return 0
	}
	ml := strings.ToLower(m)

	// 1. Config override (OpenClaw-style)
	if c.Models.ContextWindow != nil {
		// Try full model ID first
		if v, ok := c.Models.ContextWindow[m]; ok {
			return v
		}
		if v, ok := c.Models.ContextWindow[ml]; ok {
			return v
		}
		// Strip provider prefix (e.g. "zhipu/glm-4" -> "glm-4")
		if idx := strings.Index(ml, "/"); idx >= 0 && idx+1 < len(ml) {
			bare := strings.TrimSpace(ml[idx+1:])
			if bare != "" {
				for k, v := range c.Models.ContextWindow {
					if strings.EqualFold(k, bare) {
						return v
					}
				}
			}
		}
	}

	// 2. Static table (extended from guessContextWindow)
	return 0
}
