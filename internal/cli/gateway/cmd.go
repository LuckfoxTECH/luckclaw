package gateway

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"luckclaw/internal/agent"
	"luckclaw/internal/bus"
	"luckclaw/internal/channels"
	"luckclaw/internal/config"
	"luckclaw/internal/cron"
	"luckclaw/internal/heartbeat"
	"luckclaw/internal/logging"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	sessionpkg "luckclaw/internal/session"

	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	var port int
	var foreground bool

	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Gateway management",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !foreground {
				return cmd.Help()
			}
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if err := cfg.ValidateForGateway(); err != nil {
				return exitf(cmd, "Config validation failed: %v", err)
			}
			if port == 0 {
				port = cfg.Gateway.Port
			}
			if port == 0 {
				port = 18790
			}

			selected := cfg.SelectProvider(cfg.Agents.Defaults.Model)
			allProviders := cfg.SelectAllProviders(cfg.Agents.Defaults.Model)
			if len(allProviders) == 0 {
				return exitf(cmd, "Config validation failed: no provider with API key for model %s", cfg.Agents.Defaults.Model)
			}
			var chatClient openaiapi.ChatClient
			if len(allProviders) > 1 {
				clients := make([]*openaiapi.Client, 0, len(allProviders))
				for _, p := range allProviders {
					headers := p.ExtraHeaders
					if headers == nil {
						headers = map[string]string{}
					}
					if _, ok := headers["x-session-affinity"]; !ok {
						headers["x-session-affinity"] = "true"
					}
					clients = append(clients, &openaiapi.Client{
						APIKey:                p.APIKey,
						APIBase:               p.APIBase,
						ExtraHeaders:          headers,
						SupportsPromptCaching: config.SupportsPromptCaching(p.Name),
						HTTPClient:            openaiapi.NewHTTPClientWithProxy(&cfg.Tools.Web, 120*time.Second),
					})
				}
				chatClient = openaiapi.NewFailoverClient(clients)
			} else {
				headers := selected.ExtraHeaders
				if headers == nil {
					headers = map[string]string{}
				}
				if _, ok := headers["x-session-affinity"]; !ok {
					headers["x-session-affinity"] = "true"
				}
				chatClient = &openaiapi.Client{
					APIKey:                selected.APIKey,
					APIBase:               selected.APIBase,
					ExtraHeaders:          headers,
					SupportsPromptCaching: config.SupportsPromptCaching(selected.Name),
					HTTPClient:            openaiapi.NewHTTPClientWithProxy(&cfg.Tools.Web, 120*time.Second),
				}
			}
			sessions := sessionpkg.NewManager()
			if ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace); err == nil && ws != "" {
				sessions.Workspace = ws
			}
			agentLogger := &logging.StdLogger{Prefix: "[agent]"}
			loop := agent.New(cfg, chatClient, sessions, cfg.Agents.Defaults.Model, agentLogger)
			messageBus := bus.NewWithCapacity(cfg.Gateway.InboundQueueCap, cfg.Gateway.OutboundQueueCap)
			loop.SetBus(messageBus)
			chMgr := channels.NewManager(cfg, messageBus)

			webuiHub := channels.NewWebUIHub()
			workspace, _ := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
			chMgr.RegisterChannel("webui", channels.NewWebUI(webuiHub, messageBus, workspace))

			loop.OnModelResolved = func(channel, chatID string, model string) {
				if channel == "webui" && chatID != "" {
					webuiHub.SendToSession(chatID, map[string]any{
						"type":  "status",
						"model": model,
					})
				}
			}
			loop.OnContextInfo = func(channel, chatID string, count string, mode string) {
				if channel == "webui" && chatID != "" {
					webuiHub.SendToSession(chatID, map[string]any{
						"type":            "status",
						"context_sources": count,
						"context_mode":    mode,
					})
				}
			}
			loop.OnTurnComplete = func(channel, chatID string, model string, promptTok, completionTok, totalTok int) {
				if channel == "webui" && chatID != "" {
					webuiHub.SendToSession(chatID, map[string]any{
						"type":  "status",
						"model": model,
						"usage": map[string]int{
							"prompt_tokens":     promptTok,
							"completion_tokens": completionTok,
							"total_tokens":      totalTok,
						},
					})
				}
			}

			cronPath, err := paths.CronJobsPath()
			if err != nil {
				return err
			}
			cronSvc := cron.NewService(cronPath)
			if err := cronSvc.Load(); err != nil {
				log.Printf("[cron] Load warning: %v", err)
			} else if jobs, _ := cronSvc.List(true); len(jobs) > 0 {
				log.Printf("[cron] Load: %d job(s) from %s", len(jobs), cronPath)
			}
			loop.SetCron(cronSvc)
			cronSvc.SetCallback(func(ctx context.Context, job cron.Job) (string, error) {
				log.Printf("[cron] callback: job=%s running msg=%q reminderOnly=%v", job.ID, job.Payload.Message, job.Payload.ReminderOnly)
				deliver, ch, to := job.Payload.Deliver, job.Payload.Channel, job.Payload.To
				reminder := strings.TrimSpace(job.Payload.Message)

				if job.Payload.ReminderOnly {
					if deliver && ch != "" && to != "" && reminder != "" {
						log.Printf("[cron] callback: job=%s reminderOnly: sending to %s:%s msg=%q", job.ID, ch, to, reminder)
						_ = messageBus.PublishOutbound(ctx, bus.OutboundMessage{
							Channel: ch,
							ChatID:  to,
							Content: reminder,
						})
					}
					return reminder, nil
				}

				out, err := loop.ProcessDirect(ctx, job.Payload.Message, "cron:"+job.ID)
				if err != nil {
					log.Printf("[cron] callback: job=%s ProcessDirect error: %v", job.ID, err)
					return "", err
				}
				if deliver && ch != "" && to != "" {
					if reminder != "" {
						log.Printf("[cron] callback: job=%s sending reminder to %s:%s msg=%q", job.ID, ch, to, reminder)
						_ = messageBus.PublishOutbound(ctx, bus.OutboundMessage{
							Channel: ch,
							ChatID:  to,
							Content: reminder,
						})
					}
					if out != "" && strings.TrimSpace(out) != reminder {
						preview := out
						if len(preview) > 60 {
							preview = preview[:60] + "..."
						}
						log.Printf("[cron] callback: job=%s publishing agent response to %s:%s content=%q", job.ID, ch, to, preview)
						_ = messageBus.PublishOutbound(ctx, bus.OutboundMessage{
							Channel: ch,
							ChatID:  to,
							Content: out,
						})
					}
				} else {
					log.Printf("[cron] callback: job=%s SKIP deliver (deliver=%v channel=%q to=%q)", job.ID, deliver, ch, to)
				}
				return out, nil
			})

			ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
			if err != nil {
				return err
			}
			hb := heartbeat.New(ws, cfg.Gateway.HeartbeatInterval)
			hb.OnDecide = func(ctx context.Context, content string) (string, error) {
				res, err := chatClient.Chat(ctx, openaiapi.ChatRequest{
					Model: cfg.ModelIDForAPI(cfg.Agents.Defaults.Model),
					Messages: []openaiapi.Message{
						{Role: "system", Content: "You are a heartbeat scheduler. Based on the HEARTBEAT.md content below, decide if the agent should run now. Reply with exactly 'run' or 'skip'. Consider time-based triggers and whether new work is needed."},
						{Role: "user", Content: content},
					},
					MaxTokens: 10,
				})
				if err != nil {
					return "run", nil
				}
				return res.Content, nil
			}
			hb.OnHeartbeat = func(ctx context.Context, content string) (string, error) {
				return loop.ProcessDirect(ctx, content, "heartbeat:default")
			}
			if cfg.Gateway.HeartbeatChannel != "" && cfg.Gateway.HeartbeatChatID != "" {
				hbCh, hbTo := cfg.Gateway.HeartbeatChannel, cfg.Gateway.HeartbeatChatID
				hb.OnResult = func(ctx context.Context, result string) {
					_ = messageBus.PublishOutbound(ctx, bus.OutboundMessage{
						Channel: hbCh,
						ChatID:  hbTo,
						Content: result,
					})
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigc := make(chan os.Signal, 1)
			signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigc
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down...")
				cancel()
			}()

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Starting luckclaw gateway on port %d...\n", port)

			var wg sync.WaitGroup

			if err := chMgr.StartAll(ctx); err != nil {
				return exitf(cmd, "Error: %v", err)
			}

			wg.Add(4)
			go func() { defer wg.Done(); _ = loop.Run(ctx, messageBus) }()
			go func() { defer wg.Done(); _ = cronSvc.Run(ctx) }()
			go func() { defer wg.Done(); _ = hb.Run(ctx) }()
			go func() { defer wg.Done(); _ = serveGatewayHTTP(ctx, port, webuiHub, messageBus, &cfg) }()

			<-ctx.Done()

			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()

			_ = chMgr.StopAll(shutdownCtx)
			messageBus.Close()

			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-shutdownCtx.Done():
			}

			loop.Close()
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Gateway port")
	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run gateway in foreground")
	cmd.AddCommand(newGatewayStartCmd())
	cmd.AddCommand(newGatewayStopCmd())
	cmd.AddCommand(newGatewayStatusCmd())

	return cmd
}

func exitf(cmd *cobra.Command, format string, args ...any) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
	return fmt.Errorf(format, args...)
}
