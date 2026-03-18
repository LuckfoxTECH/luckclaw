package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

type gatewayPIDInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	StartedAt string `json:"startedAt"`
}

func newGatewayStartCmd() *cobra.Command {
	var port int
	var logPath string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the luckclaw gateway in background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return exitf(cmd, "gateway start is not supported on %s", runtime.GOOS)
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

			pidPath, err := paths.GatewayPIDPath()
			if err != nil {
				return err
			}
			if info, ok := readGatewayPIDInfo(pidPath); ok && info.PID > 0 && processAlive(info.PID) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gateway already running (pid=%d, port=%d)\n", info.PID, info.Port)
				return nil
			}

			exe, err := os.Executable()
			if err != nil {
				return err
			}

			if logPath == "" {
				logPath, err = paths.GatewayLogPath()
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
			defer func() { _ = logFile.Close() }()

			child := exec.Command(exe, "gateway", "--foreground", "--port", strconv.Itoa(port))
			child.Stdout = logFile
			child.Stderr = logFile
			child.Stdin = nil
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

			if err := child.Start(); err != nil {
				return err
			}

			info := gatewayPIDInfo{
				PID:       child.Process.Pid,
				Port:      port,
				StartedAt: time.Now().Format(time.RFC3339),
			}
			_ = child.Process.Release()

			if err := writeGatewayPIDInfo(pidPath, info); err != nil {
				return err
			}

			waitGatewayTCP(port, 2*time.Second)

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gateway started (pid=%d, port=%d, log=%s)\n", info.PID, info.Port, logPath)
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Gateway port")
	cmd.Flags().StringVar(&logPath, "log", "", "Gateway log file path")

	return cmd
}

func newGatewayStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the background luckclaw gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return exitf(cmd, "gateway stop is not supported on %s", runtime.GOOS)
			}

			pidPath, err := paths.GatewayPIDPath()
			if err != nil {
				return err
			}
			info, ok := readGatewayPIDInfo(pidPath)
			if !ok || info.PID <= 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "gateway not running")
				return nil
			}
			if !processAlive(info.PID) {
				_ = os.Remove(pidPath)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "gateway not running")
				return nil
			}

			if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}

			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if !processAlive(info.PID) {
					_ = os.Remove(pidPath)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gateway stopped (pid=%d)\n", info.PID)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}

			_ = syscall.Kill(info.PID, syscall.SIGKILL)
			time.Sleep(150 * time.Millisecond)
			_ = os.Remove(pidPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gateway stopped (pid=%d)\n", info.PID)
			return nil
		},
	}
	return cmd
}

func newGatewayStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show gateway background status",
		RunE: func(cmd *cobra.Command, args []string) error {
			pidPath, err := paths.GatewayPIDPath()
			if err != nil {
				return err
			}
			info, ok := readGatewayPIDInfo(pidPath)
			if ok && info.PID > 0 && processAlive(info.PID) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gateway: running (pid=%d, port=%d)\n", info.PID, info.Port)
				return nil
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "gateway: not running")
			return nil
		},
	}
	return cmd
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func readGatewayPIDInfo(pidPath string) (gatewayPIDInfo, bool) {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return gatewayPIDInfo{}, false
	}
	var info gatewayPIDInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return gatewayPIDInfo{}, false
	}
	return info, true
}

func writeGatewayPIDInfo(pidPath string, info gatewayPIDInfo) error {
	tmp := pidPath + ".tmp"
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, pidPath)
}

func waitGatewayTCP(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
