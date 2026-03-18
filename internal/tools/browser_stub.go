//go:build nobrowser

package tools

import (
	"context"
	"fmt"
)

// BrowserTool stub when built with -tags nobrowser (minimal binary, no go-rod).
type BrowserTool struct {
	RemoteURL   string
	Profile     string
	SnapshotDir string
	DebugPort   int
}

func (t *BrowserTool) Name() string { return "browser" }
func (t *BrowserTool) Description() string {
	return "Browser automation (excluded with -tags nobrowser)"
}

func (t *BrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"action": map[string]any{"type": "string"}},
		"required":   []any{"action"},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	return "", fmt.Errorf("browser tool not compiled in. Use default build (no -tags nobrowser) or: go build ./cmd/luckclaw")
}
