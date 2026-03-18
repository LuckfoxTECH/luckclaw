package cli

import (
	"fmt"
	"os"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show luckclaw status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfgExists := exists(cfgPath)

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			wsExpanded, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
			if err != nil {
				return err
			}
			wsExists := exists(wsExpanded)

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "luckclaw Status")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Config: %s %s\n", cfgPath, mark(cfgExists))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s %s\n", wsExpanded, mark(wsExists))

			if cfgExists {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Model: %s\n", cfg.Agents.Defaults.Model)
				if cfg.Providers.OpenRouter.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OpenRouter API: %s\n", keyMark(cfg.Providers.OpenRouter.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "OpenRouter API: not set")
				}
				if cfg.Providers.Anthropic.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Anthropic API: %s\n", keyMark(cfg.Providers.Anthropic.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Anthropic API: not set")
				}
				if cfg.Providers.OpenAI.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OpenAI API: %s\n", keyMark(cfg.Providers.OpenAI.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "OpenAI API: not set")
				}
				if cfg.Providers.Gemini.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Gemini API: %s\n", keyMark(cfg.Providers.Gemini.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Gemini API: not set")
				}
				if cfg.Providers.Zhipu.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Zhipu AI API: %s\n", keyMark(cfg.Providers.Zhipu.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Zhipu AI API: not set")
				}
				if cfg.Providers.AiHubMix.APIKey != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "AiHubMix API: %s\n", keyMark(cfg.Providers.AiHubMix.APIKey))
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "AiHubMix API: not set")
				}
				if cfg.Providers.VLLM.APIBase != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "vLLM/Local: ✓ %s\n", cfg.Providers.VLLM.APIBase)
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "vLLM/Local: not set")
				}
				if cfg.Providers.Ollama.APIBase != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Ollama: ✓ %s\n", cfg.Providers.Ollama.APIBase)
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Ollama: not set")
				}
			}
			return nil
		},
	}
	return cmd
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func keyMark(value string) string {
	if value == "" {
		return "not set"
	}
	return "✓"
}
