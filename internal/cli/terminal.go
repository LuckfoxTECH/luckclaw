package cli

import (
	"fmt"
	"strconv"
	"strings"

	"luckclaw/internal/command"

	"github.com/spf13/cobra"
)

func newTerminalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Manage saved SSH terminals (persistent)",
	}
	cmd.AddCommand(
		newTerminalListCmd(),
		newTerminalInfoCmd(),
		newTerminalAddCmd(),
		newTerminalRmCmd(),
		newTerminalUseCmd(),
		newTerminalOffCmd(),
	)
	return cmd
}

func newTerminalListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved terminals",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   []string{"list"},
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
}

func newTerminalInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show terminal details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   []string{"info", args[0]},
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
}

func newTerminalAddCmd() *cobra.Command {
	var port int
	var identity string
	var passwordEnv string
	var password string
	var strict bool
	cmd := &cobra.Command{
		Use:   "add <name> ssh <user@host>",
		Short: "Add or update a saved SSH terminal",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			typ := strings.TrimSpace(args[1])
			target := strings.TrimSpace(args[2])
			if name == "" || target == "" || typ == "" {
				return exitf(cmd, "Error: name, type (ssh), and target are required")
			}

			flags := map[string]string{
				"port":         strconv.Itoa(port),
				"identity":     identity,
				"password-env": passwordEnv,
				"password":     password,
				"strict":       strconv.FormatBool(strict),
			}

			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   append([]string{"add", name, typ, target}, args[3:]...),
				Flags:  flags,
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 22, "SSH port")
	cmd.Flags().StringVar(&identity, "identity", "", "Private key file path")
	cmd.Flags().StringVar(&passwordEnv, "password-env", "", "Password environment variable name")
	cmd.Flags().StringVar(&password, "password", "", "Password (stored encrypted)")
	cmd.Flags().BoolVar(&strict, "strict", false, "Enable StrictHostKeyChecking")
	return cmd
}

func newTerminalRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a saved terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   []string{"rm", args[0]},
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
}

func newTerminalUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set default terminal used for new sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   []string{"use", args[0]},
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
}

func newTerminalOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Clear default terminal (new sessions use local)",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.TerminalHandler{}
			input := command.Input{
				Args:   []string{"off"},
				Writer: cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
}
