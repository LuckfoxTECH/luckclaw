package cli

import (
	"fmt"
	"strings"

	"luckclaw/internal/terminal"
	"luckclaw/internal/tools"

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
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			names := terminal.Names(s)
			if len(names) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No terminals configured.")
				return nil
			}
			for _, n := range names {
				it := s.Terminals[n]
				target := it.SSH.Host
				if strings.TrimSpace(it.SSH.User) != "" {
					target = it.SSH.User + "@" + it.SSH.Host
				}
				line := fmt.Sprintf("- %s (%s %s)", n, it.Type, target)
				if s.Default == n {
					line += " [default]"
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
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
			name := strings.TrimSpace(args[0])
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			it, ok := s.Terminals[name]
			if !ok {
				return exitf(cmd, "Error: terminal %q not found", name)
			}
			target := it.SSH.Host
			if strings.TrimSpace(it.SSH.User) != "" {
				target = it.SSH.User + "@" + it.SSH.Host
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\nType: %s\nTarget: %s\n", name, it.Type, target)
			if strings.TrimSpace(it.SSH.IdentityFile) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Identity: %s\n", it.SSH.IdentityFile)
			}
			if strings.TrimSpace(it.SSH.PasswordEnv) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "PasswordEnv: %s\n", it.SSH.PasswordEnv)
			}
			if s.Default == name {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Default: yes")
			}
			return nil
		},
	}
}

func newTerminalAddCmd() *cobra.Command {
	var port int
	var identity string
	var passwordEnv string
	var strict bool
	cmd := &cobra.Command{
		Use:   "add <name> <user@host>",
		Short: "Add or update a saved SSH terminal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			target := strings.TrimSpace(args[1])
			if name == "" || target == "" {
				return exitf(cmd, "Error: name and target are required")
			}
			user, host := splitUserHost(target)
			if host == "" {
				return exitf(cmd, "Error: invalid target %q (expected user@host or host)", target)
			}
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			e := terminal.Entry{
				Type: "ssh",
				SSH: tools.SSHConn{
					Host:                  host,
					User:                  user,
					Port:                  port,
					IdentityFile:          identity,
					PasswordEnv:           passwordEnv,
					BatchMode:             true,
					StrictHostKeyChecking: strict,
				},
			}
			s, err = terminal.Set(s, name, e)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			if err := terminal.Save(s); err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved terminal %q.\n", name)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 22, "SSH port")
	cmd.Flags().StringVar(&identity, "identity", "", "Private key file path")
	cmd.Flags().StringVar(&passwordEnv, "password-env", "", "Password environment variable name")
	cmd.Flags().BoolVar(&strict, "strict", false, "Enable StrictHostKeyChecking")
	return cmd
}

func newTerminalRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a saved terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			if _, ok := s.Terminals[name]; !ok {
				return exitf(cmd, "Error: terminal %q not found", name)
			}
			s, err = terminal.Remove(s, name)
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			if err := terminal.Save(s); err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed terminal %q.\n", name)
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
			name := strings.TrimSpace(args[0])
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			if _, ok := s.Terminals[name]; !ok {
				return exitf(cmd, "Error: terminal %q not found", name)
			}
			s.Default = name
			if err := terminal.Save(s); err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Default terminal set to %q.\n", name)
			return nil
		},
	}
}

func newTerminalOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Clear default terminal (new sessions use local)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := terminal.Load()
			if err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			s.Default = ""
			if err := terminal.Save(s); err != nil {
				return exitf(cmd, "Error: %v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Default terminal cleared.")
			return nil
		},
	}
}

func splitUserHost(target string) (user string, host string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if at := strings.Index(target, "@"); at >= 0 {
		return strings.TrimSpace(target[:at]), strings.TrimSpace(target[at+1:])
	}
	return "", target
}
