package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"luckclaw/internal/agent"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	sessionpkg "luckclaw/internal/session"

	"github.com/spf13/cobra"
)

func newHeartbeatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Run heartbeat tasks",
	}
	cmd.AddCommand(newHeartbeatRunCmd())
	return cmd
}

func newHeartbeatRunCmd() *cobra.Command {
	var input string
	var write string
	var model string
	var session string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run heartbeat once and print response",
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
			if strings.TrimSpace(session) == "" {
				session = "heartbeat:manual"
			}

			selected := cfg.SelectProvider(model)
			if selected == nil || strings.TrimSpace(selected.APIKey) == "" {
				return exitf(cmd, "Error: No API key configured. Set one in ~/.luckclaw/config.json under providers section")
			}
			if strings.TrimSpace(selected.APIBase) == "" {
				return exitf(cmd, "Error: No apiBase configured for provider %s. Go version currently uses OpenAI-compatible /chat/completions. Recommended: set providers.openrouter.apiKey or providers.openai.apiKey; otherwise set providers.%s.apiBase to an OpenAI-compatible gateway URL", selected.Name, selected.Name)
			}

			ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
			if err != nil {
				return err
			}

			content := strings.TrimSpace(input)
			if content == "" {
				b, err := os.ReadFile(filepath.Join(ws, "HEARTBEAT.md"))
				if err != nil {
					return exitf(cmd, "Error: HEARTBEAT.md not found in workspace %s", ws)
				}
				content = strings.TrimSpace(string(b))
			}
			if content == "" {
				return exitf(cmd, "Error: heartbeat content is empty (provide --input or write to HEARTBEAT.md)")
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

			out, err := loop.ProcessDirect(context.Background(), content, session)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), out)

			if strings.TrimSpace(write) != "" {
				p, err := paths.ExpandUser(write)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(p, []byte(out+"\n"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "", "Override heartbeat content (otherwise read workspace/HEARTBEAT.md)")
	cmd.Flags().StringVar(&write, "write", "", "Write response to file path")
	cmd.Flags().StringVar(&model, "model", "", "Model override")
	cmd.Flags().StringVar(&session, "session", "heartbeat:manual", "Session ID")

	return cmd
}
