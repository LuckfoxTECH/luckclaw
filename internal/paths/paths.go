package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Env vars (like picoclaw):
// - LUCKCLAW_CONFIG: override config file path directly
// - LUCKCLAW_HOME: override root directory (~/.luckclaw); affects DataDir, WorkspaceDir, etc.

func ExpandUser(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func HomeDir() (string, error) {
	return os.UserHomeDir()
}

func DataDir() (string, error) {
	if h := os.Getenv("LUCKCLAW_HOME"); h != "" {
		return filepath.Clean(h), nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".luckclaw"), nil
}

func ConfigPath() (string, error) {
	if p := os.Getenv("LUCKCLAW_CONFIG"); p != "" {
		return filepath.Clean(p), nil
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func WorkspaceDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspace"), nil
}

func SessionsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

func CronJobsPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cron", "jobs.json"), nil
}

func GatewayDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway"), nil
}

func GatewayPIDPath() (string, error) {
	dir, err := GatewayDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway.pid.json"), nil
}

func GatewayLogPath() (string, error) {
	dir, err := GatewayDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway.log"), nil
}
