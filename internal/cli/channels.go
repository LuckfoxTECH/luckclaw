package cli

import (
	"fmt"
	"text/tabwriter"

	"luckclaw/internal/bus"
	"luckclaw/internal/channels"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func newChannelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "Manage channels",
	}
	cmd.AddCommand(newChannelsStatusCmd())
	//cmd.AddCommand(newChannelsLoginCmd())
	return cmd
}

func newChannelsStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show channel status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			mgr := channels.NewManager(cfg, bus.New())
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 4, 0, 2, ' ', 0)
			for _, line := range mgr.StatusLines() {
				_, _ = fmt.Fprintln(w, line)
			}
			_ = w.Flush()
			return nil
		},
	}
	return cmd
}

func newChannelsLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Link channel (e.g. QR for some channels)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Use `luckclaw gateway` to start channels. Configure channels in config.json.")
			return nil
		},
	}
	return cmd
}
