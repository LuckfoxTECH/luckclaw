package cli

import (
	"fmt"
	"os"

	"luckclaw/internal/cli/gateway"
	"luckclaw/internal/cli/tui"
	"luckclaw/internal/service"

	"github.com/charmbracelet/lipgloss"
	"github.com/common-nighthawk/go-figure"
	"github.com/spf13/cobra"
)

var version = "0.0.1"

func RootBanner() string {
	luckStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9600")).Bold(true)
	clawStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)

	luckFig := figure.NewFigure("LUCK", "3-d", true)
	clawFig := figure.NewFigure("CLAW", "3-d", true)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		luckStyle.Render(luckFig.String()),
		clawStyle.Render(clawFig.String()),
	) + "\n"
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use: "luckclaw",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprint(out, RootBanner())
			_, _ = fmt.Fprintf(out, "🍀 luckclaw v%s\n", version)
			_, _ = fmt.Fprintln(out, gateway.StatusLine())
			_, _ = fmt.Fprintln(out)
			return cmd.Help()
		},
	}

	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	root.Version = version
	root.SetVersionTemplate("luckclaw v{{.Version}}\n")

	root.AddCommand(newOnboardCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(tui.NewCmd())
	root.AddCommand(gateway.NewCmd())
	root.AddCommand(service.NewCmd())
	root.AddCommand(newModelsCmd())
	root.AddCommand(newCronCmd())
	root.AddCommand(newChannelsCmd())
	root.AddCommand(newHeartbeatCmd())
	root.AddCommand(newTerminalCmd())
	root.AddCommand(newSkillsCmd())
	root.AddCommand(newClawhubCmd())
	root.AddCommand(newMqttCmd())

	return root
}

func exitf(cmd *cobra.Command, format string, args ...any) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
	return fmt.Errorf(format, args...)
}
