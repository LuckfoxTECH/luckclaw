package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CLIAdapter is the CLI command adapter
type CLIAdapter struct {
	registry *Registry
}

// NewCLIAdapter creates a CLI adapter
func NewCLIAdapter(registry *Registry) *CLIAdapter {
	return &CLIAdapter{registry: registry}
}

// CreateCobraCmd creates a Cobra command
func (a *CLIAdapter) CreateCobraCmd(name string, handler Handler) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("%s command", name),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := Input{
				Args:   args,
				Flags:  make(map[string]string),
				Writer: cmd.OutOrStdout(),
			}

			// Collect all flags
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				if flag.Changed {
					input.Flags[flag.Name] = flag.Value.String()
				}
			})

			output, err := handler.Execute(input)
			if err != nil {
				return err
			}
			if output.Error != nil {
				return output.Error
			}

			fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

// SlashAdapter is the slash command adapter
type SlashAdapter struct {
	registry *Registry
}

// NewSlashAdapter creates a slash command adapter
func NewSlashAdapter(registry *Registry) *SlashAdapter {
	return &SlashAdapter{registry: registry}
}

// Execute executes a slash command
func (a *SlashAdapter) Execute(name string, args []string, input Input) (Output, error) {
	handler, ok := a.registry.Get(name)
	if !ok {
		return Output{
			Content: fmt.Sprintf("Unknown command: %s", name),
			Error:   fmt.Errorf("unknown command: %s", name),
		}, nil
	}

	input.Args = args
	return handler.Execute(input)
}

// ParseSlashCommand parses a slash command
func ParseSlashCommand(cmd string) (name string, args []string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", nil
	}
	name = parts[0]
	if len(parts) > 1 {
		args = parts[1:]
	}
	return name, args
}
