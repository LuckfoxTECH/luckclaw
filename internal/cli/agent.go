package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"luckclaw/internal/agent"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	sessionpkg "luckclaw/internal/session"

	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	var message string
	var session string
	var model string

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Interact with the agent directly",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(model) == "" {
				model = cfg.Agents.Defaults.Model
			}
			selected := cfg.SelectProvider(model)
			if selected == nil || strings.TrimSpace(selected.APIKey) == "" {
				return exitf(cmd, "Error: No API key configured. Set one in ~/.luckclaw/config.json under providers section")
			}
			if strings.TrimSpace(selected.APIBase) == "" {
				return exitf(cmd, "Error: No apiBase configured for provider %s. Go version currently uses OpenAI-compatible /chat/completions. Recommended: set providers.openrouter.apiKey (openrouter.ai) or providers.openai.apiKey; otherwise set providers.%s.apiBase to an OpenAI-compatible gateway URL", selected.Name, selected.Name)
			}

			client := &openaiapi.Client{
				APIKey:       selected.APIKey,
				APIBase:      selected.APIBase,
				ExtraHeaders: selected.ExtraHeaders,
				HTTPClient:   openaiapi.NewHTTPClientWithProxy(&cfg.Tools.Web, 120*time.Second),
			}
			sessions := sessionpkg.NewManager()
			if ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace); err == nil && ws != "" {
				sessions.Workspace = ws
			}
			loop := agent.New(cfg, client, sessions, model, nil)

			runOnce := func(input string) error {
				ux := (&cfg).UXPtr()
				if ux != nil && (ux.Typing || ux.Placeholder.Enabled) {
					placeholderText := ux.Placeholder.Text
					if placeholderText == "" {
						placeholderText = "Thinking... 💭"
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), placeholderText)
				}
				out, err := loop.ProcessDirect(context.Background(), input, session)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nluckclaw %s\n", out)
				return nil
			}

			if message != "" {
				return runOnce(message)
			}

			in := bufio.NewScanner(os.Stdin)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "luckclaw interactive mode (Ctrl+D to exit)")
			for {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), "> ")
				if !in.Scan() {
					return nil
				}
				line := strings.TrimSpace(in.Text())
				if line == "" {
					continue
				}
				if err := runOnce(line); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
				}
			}
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "Message to send to the agent")
	cmd.Flags().StringVarP(&session, "session", "s", "cli:default", "Session ID")
	cmd.Flags().StringVar(&model, "model", "", "Model override")

	return cmd
}
