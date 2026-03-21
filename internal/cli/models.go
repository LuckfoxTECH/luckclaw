package cli

import (
	"fmt"

	"luckclaw/internal/command"
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

			// Use unified command handler
			handler := &command.ModelsHandler{}
			input := command.Input{
				Args:   args,
				Config: &cfg,
				Writer: cmd.OutOrStdout(),
			}

			output, err := handler.Execute(input)
			if err != nil {
				return err
			}
			if output.Error != nil {
				return output.Error
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}
