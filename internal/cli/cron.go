package cli

import (
	"context"
	"fmt"
	"time"

	"luckclaw/internal/agent"
	"luckclaw/internal/config"
	"luckclaw/internal/cron"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	sessionpkg "luckclaw/internal/session"

	"github.com/spf13/cobra"
)

func newCronCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage scheduled tasks",
	}

	cmd.AddCommand(newCronListCmd())
	cmd.AddCommand(newCronAddCmd())
	cmd.AddCommand(newCronRemoveCmd())
	cmd.AddCommand(newCronEnableCmd())
	cmd.AddCommand(newCronRunCmd())

	return cmd
}

func newCronService() (*cron.Service, error) {
	path, err := paths.CronJobsPath()
	if err != nil {
		return nil, err
	}
	svc := cron.NewService(path)
	_ = svc.Load()
	return svc, nil
}

func newCronListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scheduled jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newCronService()
			if err != nil {
				return err
			}
			jobs, err := svc.List(all)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No scheduled jobs.")
				return nil
			}
			for _, j := range jobs {
				status := "disabled"
				if j.Enabled {
					status = "enabled"
				}
				sched := j.Schedule.Kind
				switch j.Schedule.Kind {
				case "every":
					sched = fmt.Sprintf("every %ds", j.Schedule.EveryMs/1000)
				case "cron":
					sched = j.Schedule.Expr
				case "at":
					sched = time.UnixMilli(j.Schedule.AtMs).Format(time.RFC3339)
				}
				next := ""
				if j.State.NextRunAtMs > 0 {
					next = time.UnixMilli(j.State.NextRunAtMs).Format("2006-01-02 15:04")
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", j.ID, j.Name, sched, status, next)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include disabled jobs")
	return cmd
}

func newCronAddCmd() *cobra.Command {
	var name string
	var message string
	var every int
	var cronExpr string
	var at string
	var deliver bool
	var reminderOnly bool
	var to string
	var channel string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a scheduled job",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || message == "" {
				return exitf(cmd, "Error: --name and --message are required")
			}
			svc, err := newCronService()
			if err != nil {
				return err
			}
			var job cron.Job
			switch {
			case every > 0:
				job, err = svc.AddEvery(name, message, every, deliver, reminderOnly, channel, to)
			case cronExpr != "":
				job, err = svc.AddCron(name, message, cronExpr, deliver, reminderOnly, channel, to)
			case at != "":
				dt, e := time.Parse(time.RFC3339, at)
				if e != nil {
					dt, e = time.Parse("2006-01-02T15:04:05", at)
				}
				if e != nil {
					return exitf(cmd, "Error: invalid --at time, use RFC3339")
				}
				job, err = svc.AddAt(name, message, dt, deliver, reminderOnly, channel, to)
			default:
				return exitf(cmd, "Error: Must specify --every, --cron, or --at")
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Added job '%s' (%s)\n", job.Name, job.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Job name")
	cmd.Flags().StringVarP(&message, "message", "m", "", "Message for agent")
	cmd.Flags().IntVarP(&every, "every", "e", 0, "Run every N seconds")
	cmd.Flags().StringVarP(&cronExpr, "cron", "c", "", "Cron expression (e.g. '0 9 * * *')")
	cmd.Flags().StringVar(&at, "at", "", "Run once at time (RFC3339)")
	cmd.Flags().BoolVarP(&deliver, "deliver", "d", false, "Deliver response to channel")
	cmd.Flags().BoolVar(&reminderOnly, "reminder-only", false, "Only send reminder, do not invoke agent")
	cmd.Flags().StringVar(&to, "to", "", "Recipient for delivery")
	cmd.Flags().StringVar(&channel, "channel", "", "Channel for delivery (e.g. 'telegram', 'whatsapp')")

	return cmd
}

func newCronRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <job_id>",
		Short: "Remove a scheduled job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newCronService()
			if err != nil {
				return err
			}
			ok, err := svc.Remove(args[0])
			if err != nil {
				return err
			}
			if !ok {
				return exitf(cmd, "Job %s not found", args[0])
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ Removed job")
			return nil
		},
	}
	return cmd
}

func newCronEnableCmd() *cobra.Command {
	var disable bool
	cmd := &cobra.Command{
		Use:   "enable <job_id>",
		Short: "Enable or disable a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newCronService()
			if err != nil {
				return err
			}
			job, err := svc.Enable(args[0], !disable)
			if err != nil {
				return err
			}
			if job == nil {
				return exitf(cmd, "Job %s not found", args[0])
			}
			status := "enabled"
			if disable {
				status = "disabled"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Job '%s' %s\n", job.Name, status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&disable, "disable", false, "Disable instead of enable")
	return cmd
}

func newCronRunCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "run <job_id>",
		Short: "Manually run a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newCronService()
			if err != nil {
				return err
			}

			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			selected := cfg.SelectProvider(cfg.Agents.Defaults.Model)
			if selected == nil || selected.APIKey == "" {
				return exitf(cmd, "Error: No API key configured. Set one in ~/.luckclaw/config.json under providers section")
			}
			if selected.APIBase == "" {
				return exitf(cmd, "Error: No apiBase configured for provider %s. Go version currently uses OpenAI-compatible /chat/completions. Recommended: set providers.openrouter.apiKey or providers.openai.apiKey; otherwise set providers.%s.apiBase to an OpenAI-compatible gateway URL", selected.Name, selected.Name)
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
			loop := agent.New(cfg, client, sessions, cfg.Agents.Defaults.Model, nil)
			svc.SetCallback(func(ctx context.Context, job cron.Job) (string, error) {
				return loop.ProcessDirect(ctx, job.Payload.Message, "cron:"+job.ID)
			})

			ok, err := svc.RunJob(context.Background(), args[0], force)
			if err != nil {
				return err
			}
			if !ok {
				return exitf(cmd, "Failed to run job %s", args[0])
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ Job executed")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Run even if disabled")
	return cmd
}
