package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const ctxKeyTerminal contextKey = "terminal"

type TerminalContext struct {
	Name string  `json:"name"`
	Type string  `json:"type"`
	SSH  SSHConn `json:"ssh"`
}

type SSHConn struct {
	Host                  string `json:"host"`
	User                  string `json:"user"`
	Port                  int    `json:"port"`
	IdentityFile          string `json:"identity_file"`
	PasswordEnv           string `json:"password_env"`
	PasswordEnc           string `json:"password_enc,omitempty"`
	Password              string `json:"-"`
	BatchMode             bool   `json:"batch_mode"`
	StrictHostKeyChecking bool   `json:"strict_host_key_checking"`
}

func WithTerminalContext(ctx context.Context, t *TerminalContext) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyTerminal, t)
}

func TerminalFromContext(ctx context.Context) *TerminalContext {
	if v := ctx.Value(ctxKeyTerminal); v != nil {
		if t, ok := v.(*TerminalContext); ok {
			return t
		}
	}
	return nil
}

type TerminalTransferTool struct {
	AllowedDir     string
	BaseDir        string
	TimeoutSeconds int
}

func (t *TerminalTransferTool) Name() string { return "terminal_transfer" }

func (t *TerminalTransferTool) Description() string {
	return "Transfer files between local workspace and the active remote terminal via SFTP."
}

func (t *TerminalTransferTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"terminal": map[string]any{
				"type":        "string",
				"description": "Optional terminal name. If provided, the agent may select this terminal without activating it.",
			},
			"direction": map[string]any{
				"type":        "string",
				"description": "upload or download",
				"enum":        []any{"upload", "download"},
			},
			"local_path": map[string]any{
				"type":        "string",
				"description": "Local file/dir path (relative to workspace or absolute within workspace)",
			},
			"remote_path": map[string]any{
				"type":        "string",
				"description": "Remote path on the active terminal",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"description": "Transfer directories recursively (default false)",
			},
		},
		"required": []any{"direction", "local_path", "remote_path"},
	}
}

func (t *TerminalTransferTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	term := TerminalFromContext(ctx)
	if term == nil || strings.TrimSpace(term.Type) == "" {
		return "", fmt.Errorf("no active terminal (use /terminal use <name>)")
	}
	if strings.ToLower(strings.TrimSpace(term.Type)) != "ssh" {
		return "", fmt.Errorf("unsupported terminal type %q", term.Type)
	}

	direction, _ := args["direction"].(string)
	localPath, _ := args["local_path"].(string)
	remotePath, _ := args["remote_path"].(string)
	recursive, _ := args["recursive"].(bool)

	direction = strings.ToLower(strings.TrimSpace(direction))
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return "", fmt.Errorf("local_path and remote_path are required")
	}

	if strings.TrimSpace(t.BaseDir) == "" {
		return "", fmt.Errorf("terminal_transfer requires workspace to be configured")
	}
	absLocal, err := resolvePath(localPath, t.BaseDir, t.AllowedDir)
	if err != nil {
		return "", err
	}

	timeout := 60 * time.Second
	if t.TimeoutSeconds > 0 {
		timeout = time.Duration(t.TimeoutSeconds) * time.Second
	}

	switch direction {
	case "upload":
		out, err := UploadPath(ctx, term.SSH, absLocal, remotePath, recursive, timeout)
		if err != nil {
			return out, err
		}
		return fmt.Sprintf("Uploaded %s -> %s", filepath.Base(absLocal), remotePath), nil
	case "download":
		out, err := DownloadPath(ctx, term.SSH, remotePath, absLocal, recursive, timeout)
		if err != nil {
			return out, err
		}
		return fmt.Sprintf("Downloaded %s -> %s", remotePath, filepath.Base(absLocal)), nil
	default:
		return "", fmt.Errorf("invalid direction %q", direction)
	}
}
