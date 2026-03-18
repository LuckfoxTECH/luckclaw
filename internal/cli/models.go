package cli

import (
	"fmt"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List available AI models",
	}
	cmd.AddCommand(newModelsListCmd())
	return cmd
}

func newModelsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available models from configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			result := cfg.ListAvailableModels()
			for _, e := range result.FetchErrors {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: "+e)
			}
			if len(result.Models) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No models available. Configure API keys and apiBase in ~/.luckclaw/config.json. If API fetch failed, check the warnings above.")
				return nil
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Available models:")
			for _, m := range result.Models {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  • %s\n", m)
			}
			return nil
		},
	}
	return cmd
}
