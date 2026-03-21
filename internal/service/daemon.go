package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

type servicePIDInfo struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
}

type serviceExecCmd struct {
	Process *os.Process
}

func serviceProcessAlive(pid int) bool {
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

func readServicePIDInfo(pidPath string) (servicePIDInfo, bool) {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return servicePIDInfo{}, false
	}
	var info servicePIDInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return servicePIDInfo{}, false
	}
	return info, true
}

func writeServicePIDInfo(pidPath string, info servicePIDInfo) error {
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

func serviceStartDaemon(exe string, logFile *os.File) *serviceExecCmd {
	cmd := exec.Command(exe, "service", "--foreground")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	return &serviceExecCmd{Process: cmd.Process}
}

func servicePIDStartedAt() string {
	return time.Now().Format(time.RFC3339)
}

func serviceNow() time.Time {
	return time.Now()
}

func serviceStopTimeout() time.Duration {
	return 5 * time.Second
}

func serviceSleep(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

func syscallKill(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}
