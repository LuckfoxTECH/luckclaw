package cli

import (
	"fmt"

	"luckclaw/internal/command"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage skills in workspace (local discovery)",
		Long:  "List skills discovered under workspace/skills/. Use luckclaw clawhub for ClawHub search/install.",
	}
	cmd.AddCommand(newSkillsListCmd())
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List discovered skills in workspace",
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
			handler := &command.SkillsHandler{}
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

func resolveWorkdir(flag string) (string, error) {
	if flag != "" {
		return paths.ExpandUser(flag)
	}
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", err
	}
	return paths.ExpandUser(cfg.Agents.Defaults.Workspace)
}
