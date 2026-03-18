package tools

import (
	"context"
	"fmt"
	"luckclaw/internal/memory"
)

// RecordCorrectionTool lets the agent record user corrections for self-improvement.
type RecordCorrectionTool struct {
	Store *memory.SelfImprovingStore
}

func (t *RecordCorrectionTool) Name() string { return "record_correction" }
func (t *RecordCorrectionTool) Description() string {
	return "Record a user correction or preference for future reference. Use when the user corrects your output, clarifies a preference, or provides feedback you should remember."
}
func (t *RecordCorrectionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"correction": map[string]any{
				"type":        "string",
				"description": "The user's correction, preference, or feedback to remember",
			},
		},
		"required": []any{"correction"},
	}
}

func (t *RecordCorrectionTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Store == nil {
		return "", fmt.Errorf("self-improving store not configured")
	}
	correction, _ := args["correction"].(string)
	if correction == "" {
		return "", fmt.Errorf("correction is required")
	}
	if err := t.Store.RecordCorrection(correction); err != nil {
		return "", fmt.Errorf("failed to record correction: %w", err)
	}
	return "Correction recorded. Future responses will consider this feedback.", nil
}
