package cli

import (
	"fmt"
	"strings"

	"luckclaw/internal/clawhub"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

// newClawhubCmd creates the clawhub subcommand (ClawHub skill marketplace, ref OpenClaw).
// Ref: https://openclaw-docs.dx3n.cn/tutorials/tools/clawhub
func newClawhubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clawhub",
		Short: "ClawHub skill marketplace: search, install, manage skill packages",
		Long:  "ClawHub is a skill and tool marketplace. Search, install, update, remove skill packages. Ref OpenClaw: https://openclaw-docs.dx3n.cn/tutorials/tools/clawhub",
	}
	cmd.AddCommand(newClawhubSearchCmd())
	cmd.AddCommand(newClawhubInstallCmd())
	cmd.AddCommand(newClawhubListCmd())
	cmd.AddCommand(newClawhubUpdateCmd())
	cmd.AddCommand(newClawhubRemoveCmd())
	cmd.AddCommand(newClawhubInfoCmd())
	return cmd
}

func newClawhubSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search skill packages on ClawHub",
		Example: `  luckclaw clawhub search "code review"
  luckclaw clawhub search "data analysis" --limit 5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clawhub.NewClient(clawhub.RegistryURL())
			resp, err := client.Search(args[0], limit)
			if err != nil {
				return err
			}
			if len(resp.Results) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No matching skill packages found.")
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Found %d matching skill packages:\n", len(resp.Results))
			for _, r := range resp.Results {
				slug := r.Slug
				if slug == "" {
					slug = "?"
				}
				name := r.DisplayName
				if name == "" {
					name = slug
				}
				ver := ""
				if r.Version != "" {
					ver = " v" + r.Version
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  - %s%s  ★ %.1f  %s\n", slug, ver, r.Score, name)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "Max results to return")
	return cmd
}

// parseSlugVersion parses "slug@version" format, e.g. code-review@2.1.0
func parseSlugVersion(s string) (slug, version string) {
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
	}
	return strings.TrimSpace(s), ""
}

func newClawhubInstallCmd() *cobra.Command {
	var workdir, version string
	var force bool
	cmd := &cobra.Command{
		Use:   "install <slug>",
		Short: "Install a skill package to workspace",
		Example: `  luckclaw clawhub install code-review
  luckclaw clawhub install code-review@2.1.0
  luckclaw clawhub install data-analyst --workdir ~/.luckclaw/workspace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			slug, verFromArg := parseSlugVersion(args[0])
			if version == "" {
				version = verFromArg
			}

			// Check if resource-constrained mode is enabled
			cfgPath, _ := paths.ConfigPath()
			resourceConstrained := false
			if cfg, err := config.Load(cfgPath); err == nil {
				resourceConstrained = cfg.Agents.Defaults.ResourceConstrained
			}

			client := clawhub.NewClient(clawhub.RegistryURL())
			// CLI commands bypass resource-constrained mode (user explicitly requested)
			if err := client.Install(ws, slug, version, force, false); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s -> %s/skills/%s\n", slug, ws, slug)
			if resourceConstrained {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Note: Resource-constrained mode is enabled. Auto-installation via tools is disabled.")
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Start a new session to load the skill.")
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workspace dir (default from config)")
	cmd.Flags().StringVar(&version, "version", "", "Specific version (default: latest)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite if already installed")
	return cmd
}

func newClawhubListCmd() *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed ClawHub skill packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			skills, err := clawhub.List(ws)
			if err != nil {
				return err
			}
			if len(skills) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No ClawHub skill packages installed.")
				return nil
			}
			for slug, e := range skills {
				ver := e.Version
				if ver == "" {
					ver = "latest"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s\n", slug, ver)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workspace dir (default from config)")
	return cmd
}

func newClawhubUpdateCmd() *cobra.Command {
	var workdir string
	var all, force bool
	cmd := &cobra.Command{
		Use:   "update [slug]",
		Short: "Update installed skill packages",
		Example: `  luckclaw clawhub update code-review
  luckclaw clawhub update --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			slug := ""
			if len(args) > 0 {
				slug, _ = parseSlugVersion(args[0])
			}
			if slug == "" && !all {
				return exitf(cmd, "provide <slug> or --all")
			}
			// CLI commands bypass resource-constrained mode (user explicitly requested)
			client := clawhub.NewClient(clawhub.RegistryURL())
			if err := client.Update(ws, slug, all, force, false); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Update complete.")
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workspace dir (default from config)")
	cmd.Flags().BoolVar(&all, "all", false, "Update all installed skills")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite local changes")
	return cmd
}

func newClawhubRemoveCmd() *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:     "remove <slug>",
		Short:   "Remove an installed skill package",
		Example: `  luckclaw clawhub remove code-review`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			slug, _ := parseSlugVersion(args[0])
			if err := clawhub.Remove(ws, slug); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", slug)
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workspace dir (default from config)")
	return cmd
}

func newClawhubInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "info <slug>",
		Short:   "Show skill package details",
		Example: `  luckclaw clawhub info data-analyst`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := strings.TrimSpace(args[0])
			client := clawhub.NewClient(clawhub.RegistryURL())
			meta, err := client.GetSkill(slug)
			if err != nil {
				return err
			}
			if meta.Skill != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", meta.Skill.DisplayName)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Slug: %s\n", meta.Skill.Slug)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Summary: %s\n", meta.Skill.Summary)
			}
			if meta.LatestVersion != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Latest version: %s\n", meta.LatestVersion.Version)
			}
			if meta.Owner != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Owner: %s\n", meta.Owner.DisplayName)
			}
			return nil
		},
	}
	return cmd
}
