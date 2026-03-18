package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"
	"luckclaw/internal/skills"

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
			ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
			if err != nil {
				return err
			}
			ss, err := skills.Discover(ws)
			if err != nil {
				return err
			}
			if len(ss) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No skills found. Create workspace/skills/<name>/SKILL.md")
				return nil
			}
			const maxDesc = 56
			trunc := func(s string, n int) string {
				s = strings.TrimSpace(s)
				r := []rune(s)
				if len(r) <= n {
					return s
				}
				return string(r[:n-3]) + "..."
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 4, 0, 2, ' ', 0)
			for _, s := range ss {
				state := "available"
				if !s.Available {
					state = "unavailable"
				}
				desc := strings.TrimSpace(s.Description)
				if desc == "" {
					desc = "(no description)"
				}
				desc = trunc(desc, maxDesc)
				reason := ""
				if !s.Available {
					missingBins, missingEnv := skills.MissingRequires(s.Requires)
					parts := []string{}
					if len(missingEnv) > 0 {
						parts = append(parts, "missing env: "+strings.Join(missingEnv, ", "))
					}
					if len(missingBins) > 0 {
						parts = append(parts, "missing bins: "+strings.Join(missingBins, ", "))
					}
					reason = strings.Join(parts, "; ")
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Name, state, s.Path, desc, reason)
			}
			_ = w.Flush()
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
