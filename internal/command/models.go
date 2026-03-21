package command

import (
	"fmt"
	"strings"
)

// ModelsHandler is the models command handler
type ModelsHandler struct{}

// Execute executes the models command
func (h *ModelsHandler) Execute(input Input) (Output, error) {
	if input.Config == nil {
		return Output{Error: fmt.Errorf("config not available")}, nil
	}

	// No arguments: list available models
	if len(input.Args) == 0 {
		return h.listModels(input)
	}

	// With arguments: switch model
	return h.switchModel(input)
}

func (h *ModelsHandler) listModels(input Input) (Output, error) {
	result := input.Config.ListAvailableModels()

	var b strings.Builder
	b.WriteString("**Available models:**\n\n")

	if len(result.FetchErrors) > 0 {
		b.WriteString("**API fetch errors:**\n")
		for _, e := range result.FetchErrors {
			b.WriteString("  - " + e + "\n")
		}
		b.WriteString("\n")
	}

	if len(result.Models) == 0 {
		b.WriteString("No models available. Configure API keys and apiBase in ~/.luckclaw/config.json.\n")
	} else {
		for _, m := range result.Models {
			b.WriteString("  - " + m + "\n")
		}
	}

	return Output{
		Content:    b.String(),
		IsMarkdown: true,
		IsFinal:    true,
	}, nil
}

func (h *ModelsHandler) switchModel(input Input) (Output, error) {
	target := strings.TrimSpace(strings.Join(input.Args, " "))
	if target == "" {
		return Output{Content: "Usage: /models <modelId>"}, nil
	}

	selected := input.Config.SelectProvider(target)
	if selected == nil || selected.APIKey == "" {
		return Output{
			Content: fmt.Sprintf("Error: No provider configured for model %q", target),
		}, nil
	}

	// Update session metadata
	if input.Sessions != nil && input.SessionKey != "" {
		s, err := input.Sessions.GetOrCreate(input.SessionKey)
		if err == nil {
			if s.Metadata == nil {
				s.Metadata = make(map[string]any)
			}
			s.Metadata["model"] = target
			_ = input.Sessions.Save(s)
		}
	}

	return Output{
		Content:    fmt.Sprintf("Switched to **%s** for this session.", target),
		IsMarkdown: true,
		IsFinal:    true,
	}, nil
}
