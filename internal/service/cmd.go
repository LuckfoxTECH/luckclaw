package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services (web_design and more)",
		Long: `Service manager for luckclaw.

Manages HTTP+WebSocket services independently of tui/gateway.
Services are automatically registered when created.

Usage:
  luckclaw service list                      List all services
  luckclaw service info <service_id>         Show service details
  luckclaw service start <service_id>        Start a service
  luckclaw service stop <service_id>         Stop a service
  luckclaw service delete <service_id>        Delete a service
  luckclaw service create web_design [opts]  Create a web_design service
  luckclaw service daemon start               Start the service daemon
  luckclaw service daemon stop                Stop the service daemon
  luckclaw service daemon status              Check daemon status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !foreground {
				return cmd.Help()
			}
			return runServiceDaemon(cmd)
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run service daemon in foreground")
	cmd.AddCommand(newServiceListCmd())
	cmd.AddCommand(newServiceInfoCmd())
	cmd.AddCommand(newServiceStartCmd())
	cmd.AddCommand(newServiceStopCmd())
	cmd.AddCommand(newServiceDeleteCmd())
	cmd.AddCommand(newServiceCreateCmd())
	cmd.AddCommand(newServiceDaemonCmd())
	cmd.AddCommand(newServiceSearchCmd())

	return cmd
}

func newServiceListCmd() *cobra.Command {
	var serviceType string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered services",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			var services []*ServiceInfo
			if serviceType != "" {
				services = reg.ListByType(serviceType)
			} else {
				services = reg.List()
			}

			if jsonOutput {
				b, err := json.MarshalIndent(services, "", "  ")
				if err != nil {
					return err
				}
				_, _ = cmd.OutOrStdout().Write(append(b, '\n'))
				return nil
			}

			out := cmd.OutOrStdout()
			if len(services) == 0 {
				fmt.Fprintln(out, "No services registered.")
				return nil
			}

			fmt.Fprintf(out, "Services (%d):\n", len(services))
			for _, s := range services {
				status := "stopped"
				if s.Running {
					status = fmt.Sprintf("running (: %d)", s.Port)
				}
				fmt.Fprintf(out, "  %s  %s  %s  %s\n", s.ID, s.Type, status, s.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&serviceType, "type", "", "Filter by service type (e.g. web_design)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newServiceInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <service_id>",
		Short: "Show service details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceID := args[0]
			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			svc, ok := reg.Get(serviceID)
			if !ok {
				return fmt.Errorf("service not found: %s", serviceID)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ID:       %s\n", svc.ID)
			fmt.Fprintf(out, "Type:     %s\n", svc.Type)
			fmt.Fprintf(out, "Name:     %s\n", svc.Name)
			if svc.Description != "" {
				fmt.Fprintf(out, "Desc:     %s\n", svc.Description)
			}
			fmt.Fprintf(out, "Dir:      %s\n", svc.Dir)
			fmt.Fprintf(out, "Host:     %s\n", svc.Host)
			fmt.Fprintf(out, "Port:     %d\n", svc.Port)
			fmt.Fprintf(out, "Running:  %v\n", svc.Running)
			fmt.Fprintf(out, "AutoStart: %v\n", svc.AutoStart)
			fmt.Fprintf(out, "Created:  %s\n", svc.CreatedAt)
			if svc.Running && svc.Port > 0 {
				fmt.Fprintf(out, "URL:      http://%s:%d/\n", svc.Host, svc.Port)
				fmt.Fprintf(out, "WS:       ws://%s:%d/ws\n", svc.Host, svc.Port)
			}
			return nil
		},
	}
	return cmd
}

func newServiceStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <service_id>",
		Short: "Start a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceID := args[0]

			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			if _, ok := reg.Get(serviceID); !ok {
				return fmt.Errorf("service not found: %s", serviceID)
			}

			if err := reg.StartService(cmd.Context(), serviceID); err != nil {
				return fmt.Errorf("failed to start: %v", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Service %s started\n", serviceID)
			return nil
		},
	}
	return cmd
}

func newServiceStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <service_id>",
		Short: "Stop a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceID := args[0]

			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			if _, ok := reg.Get(serviceID); !ok {
				return fmt.Errorf("service not found: %s", serviceID)
			}

			if err := reg.StopService(serviceID); err != nil {
				return fmt.Errorf("failed to stop: %v", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Service %s stopped\n", serviceID)
			return nil
		},
	}
	return cmd
}

func newServiceDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <service_id>",
		Short: "Delete a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceID := args[0]

			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			svc, ok := reg.Get(serviceID)
			if !ok {
				return fmt.Errorf("service not found: %s", serviceID)
			}

			if svc.Running && !force {
				return fmt.Errorf("service %s is running (use --force to stop and delete)", serviceID)
			}

			// Stop if running
			if svc.Running {
				_ = reg.StopService(serviceID)
			}

			// Clean up type-specific resources
			if svc.Type == TypeWebDesign {
				mgr := WebDesignManagerGlobal()
				_ = mgr.Delete(serviceID)
			}

			if force && svc.Dir != "" {
				_ = os.RemoveAll(svc.Dir)
			}

			reg.Unregister(serviceID)
			fmt.Fprintf(cmd.OutOrStdout(), "Service %s deleted\n", serviceID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force stop if running and delete files")
	return cmd
}

func newServiceSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search services by name, type, or description",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}

			reg := GlobalRegistry()
			if err := reg.Load(); err != nil {
				return fmt.Errorf("failed to load registry: %v", err)
			}

			var services []*ServiceInfo
			if query == "" {
				services = reg.List()
			} else {
				services = reg.Search(query)
			}

			out := cmd.OutOrStdout()
			if len(services) == 0 {
				fmt.Fprintln(out, "No matching services found.")
				return nil
			}

			fmt.Fprintf(out, "Found %d service(s):\n", len(services))
			for _, s := range services {
				status := "stopped"
				if s.Running {
					status = fmt.Sprintf("running (: %d)", s.Port)
				}
				fmt.Fprintf(out, "  %s  %s  %s  %s\n", s.ID, s.Type, status, s.Name)
			}
			return nil
		},
	}
	return cmd
}

func newServiceCreateCmd() *cobra.Command {
	var title string
	var dir string
	var host string
	var port int
	var autoStart bool

	cmd := &cobra.Command{
		Use:   "create <type>",
		Short: "Create a new service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svcType := args[0]

			if !GlobalTypeRegistry().HasType(svcType) {
				return fmt.Errorf("unsupported service type: %s (registered: %v)", svcType, GlobalTypeRegistry().RegisteredTypes())
			}

			mgr := WebDesignManagerGlobal()
			session, err := mgr.Create("", title, dir, host, port, autoStart, nil)
			if err != nil {
				return fmt.Errorf("failed to create: %v", err)
			}

			out := cmd.OutOrStdout()
			if session.IsRunning() && session.Port > 0 {
				fmt.Fprintf(out, "Service %s created and started\n", session.ID)
				fmt.Fprintf(out, "  URL: http://%s:%d/\n", session.Host, session.Port)
				fmt.Fprintf(out, "  WS:  ws://%s:%d/ws\n", session.Host, session.Port)
			} else {
				fmt.Fprintf(out, "Service %s created (not running)\n", session.ID)
				fmt.Fprintf(out, "  Dir: %s\n", session.Dir)
				fmt.Fprintf(out, "  Run 'luckclaw service start %s' to start\n", session.ID)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "Web Design", "Service title")
	cmd.Flags().StringVar(&dir, "dir", "", "Service directory")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Bind host")
	cmd.Flags().IntVar(&port, "port", 0, "Bind port (0 = auto)")
	cmd.Flags().BoolVar(&autoStart, "auto-start", true, "Auto-start after creation")
	return cmd
}

func newServiceDaemonCmd() *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage service daemon",
	}
	daemonCmd.AddCommand(newServiceDaemonStartCmd())
	daemonCmd.AddCommand(newServiceDaemonStopCmd())
	daemonCmd.AddCommand(newServiceDaemonStatusCmd())
	return daemonCmd
}

func newServiceDaemonStartCmd() *cobra.Command {
	var logPath string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start service daemon in background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("service daemon start is not supported on %s", runtime.GOOS)
			}

			pidPath, err := paths.ServicePIDPath()
			if err != nil {
				return err
			}

			if info, ok := readServicePIDInfo(pidPath); ok && info.PID > 0 && serviceProcessAlive(info.PID) {
				fmt.Fprintf(cmd.OutOrStdout(), "Service daemon already running (pid=%d)\n", info.PID)
				return nil
			}

			exe, err := os.Executable()
			if err != nil {
				return err
			}

			if logPath == "" {
				logPath, err = paths.ServiceLogPath()
				if err != nil {
					return err
				}
			}

			if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				return err
			}

			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer logFile.Close()

			import_os := func() interface{} { return nil }
			_ = import_os

			child := newServiceDaemonChild(exe, logFile)
			if child == nil {
				return fmt.Errorf("failed to start daemon")
			}

			info := servicePIDInfo{
				PID:       child.Process.Pid,
				StartedAt: servicePIDStartedAt(),
			}
			_ = child.Process.Release()

			if err := writeServicePIDInfo(pidPath, info); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Service daemon started (pid=%d, log=%s)\n", info.PID, logPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&logPath, "log", "", "Log file path")
	return cmd
}

func newServiceDaemonChild(exe string, logFile *os.File) *serviceExecCmd {
	return serviceStartDaemon(exe, logFile)
}

func newServiceDaemonStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop service daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("service daemon stop is not supported on %s", runtime.GOOS)
			}

			pidPath, err := paths.ServicePIDPath()
			if err != nil {
				return err
			}

			info, ok := readServicePIDInfo(pidPath)
			if !ok || info.PID <= 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Service daemon not running")
				return nil
			}

			if !serviceProcessAlive(info.PID) {
				os.Remove(pidPath)
				fmt.Fprintln(cmd.OutOrStdout(), "Service daemon not running")
				return nil
			}

			_ = syscallKill(info.PID, syscall.SIGTERM)
			deadline := serviceNow().Add(serviceStopTimeout())
			for serviceNow().Before(deadline) {
				if !serviceProcessAlive(info.PID) {
					os.Remove(pidPath)
					fmt.Fprintf(cmd.OutOrStdout(), "Service daemon stopped (pid=%d)\n", info.PID)
					return nil
				}
				serviceSleep(100)
			}

			_ = syscallKill(info.PID, syscall.SIGKILL)
			serviceSleep(150)
			os.Remove(pidPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Service daemon stopped (pid=%d)\n", info.PID)
			return nil
		},
	}
	return cmd
}

func newServiceDaemonStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show service daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			pidPath, err := paths.ServicePIDPath()
			if err != nil {
				return err
			}

			info, ok := readServicePIDInfo(pidPath)
			if ok && info.PID > 0 && serviceProcessAlive(info.PID) {
				fmt.Fprintf(cmd.OutOrStdout(), "Service daemon: running (pid=%d)\n", info.PID)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Service daemon: not running")
			}
			return nil
		},
	}
	return cmd
}

func runServiceDaemon(cmd *cobra.Command) error {
	reg := GlobalRegistry()
	if err := reg.Load(); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "Warning: failed to load registry: %v\n", err)
	}

	if errs := reg.RestoreAutoStart(cmd.Context()); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintf(cmd.OutOrStderr(), "Warning: %v\n", err)
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Service daemon running. Press Ctrl+C to stop.")

	select {}
}
