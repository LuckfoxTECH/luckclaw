package tools

import (
	"context"
	"fmt"
	"strings"
)

// ToolSearchTool lets the agent discover available tools by regex.
type ToolSearchTool struct {
	Registry *Registry
}

func (t *ToolSearchTool) Name() string { return "tool_search" }

func (t *ToolSearchTool) Description() string {
	return "Search available tools by name or description using a regex pattern. Use to discover which tools exist before calling them."
}

func (t *ToolSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match tool names or descriptions (e.g. 'web', 'file', 'read')",
			},
		},
		"required": []any{"pattern"},
	}
}

func (t *ToolSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Registry == nil {
		return "", fmt.Errorf("tool registry not configured")
	}
	pattern, _ := args["pattern"].(string)
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	matches, err := t.Registry.SearchToolsByRegex(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No tools matching pattern %q", pattern), nil
	}
	return fmt.Sprintf("Tools matching %q: %s", pattern, strings.Join(matches, ", ")), nil
}
