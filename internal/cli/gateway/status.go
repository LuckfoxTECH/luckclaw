package gateway

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"
)

func StatusLine() string {
	if pidPath, err := paths.GatewayPIDPath(); err == nil {
		if info, ok := readGatewayPIDInfo(pidPath); ok && info.PID > 0 && processAlive(info.PID) {
			port := info.Port
			if port == 0 {
				port, _ = gatewayPort()
			}
			return fmt.Sprintf("gateway: running (pid=%d, port=%d, source=pidfile)", info.PID, port)
		}
	}

	port, portSource := gatewayPort()

	running, pid := gatewayRunningAndPID(port)
	if !running {
		return fmt.Sprintf("gateway: not running (port=%d, source=%s)", port, portSource)
	}
	if pid > 0 {
		return fmt.Sprintf("gateway: running (pid=%d, port=%d, source=%s)", pid, port, portSource)
	}
	return fmt.Sprintf("gateway: running (pid=unknown, port=%d, source=%s)", port, portSource)
}

func gatewayPort() (int, string) {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return 18790, "default"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return 18790, "default"
	}
	if cfg.Gateway.Port != 0 {
		return cfg.Gateway.Port, "config"
	}
	return 18790, "default"
}

func gatewayRunningAndPID(port int) (bool, int) {
	if runtime.GOOS != "linux" {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		c, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err != nil {
			return false, 0
		}
		_ = c.Close()
		return true, 0
	}
	inode, ok := findListeningSocketInode(port)
	if !ok {
		return false, 0
	}
	pid, ok := findPIDBySocketInode(inode)
	if !ok {
		return true, 0
	}
	return true, pid
}

func findListeningSocketInode(port int) (string, bool) {
	inode, ok := findListeningSocketInodeInFile("/proc/net/tcp", port)
	if ok {
		return inode, true
	}
	return findListeningSocketInodeInFile("/proc/net/tcp6", port)
}

func findListeningSocketInodeInFile(path string, port int) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(b), "\n")
	for i := 1; i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		if len(fields) < 10 {
			continue
		}
		local := fields[1]
		state := fields[3]
		if state != "0A" {
			continue
		}
		colon := strings.LastIndexByte(local, ':')
		if colon < 0 {
			continue
		}
		phex := local[colon+1:]
		pv, err := strconv.ParseInt(phex, 16, 32)
		if err != nil || int(pv) != port {
			continue
		}
		inode := fields[9]
		if inode != "" && inode != "0" {
			return inode, true
		}
	}
	return "", false
}

func findPIDBySocketInode(inode string) (int, bool) {
	want := "socket:[" + inode + "]"

	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, e := range procEntries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == want {
				return pid, true
			}
		}
	}
	return 0, false
}
