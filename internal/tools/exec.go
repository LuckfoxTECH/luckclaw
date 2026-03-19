package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ExecTool struct {
	WorkingDir          string
	TimeoutSeconds      int
	RestrictToWorkspace bool
	PathAppend          string
}

func (t *ExecTool) Name() string { return "exec" }
func (t *ExecTool) Description() string {
	return "Execute a shell command and return its output. Use with caution."
}
func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
		},
		"required": []any{"command"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	workingDir, _ := args["working_dir"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if first := strings.Fields(command); len(first) > 0 && strings.HasPrefix(first[0], "mcp_") && strings.Count(first[0], "_") >= 2 {
		return "", fmt.Errorf("mcp_* tools are MCP function calls, not shell commands. Use the tool with JSON arguments instead of exec")
	}
	if isDangerous(command) {
		return "", fmt.Errorf("blocked potentially dangerous command")
	}

	timeout := 60 * time.Second
	if t.TimeoutSeconds > 0 {
		timeout = time.Duration(t.TimeoutSeconds) * time.Second
	}

	if term := TerminalFromContext(ctx); term != nil && strings.EqualFold(strings.TrimSpace(term.Type), "ssh") && strings.TrimSpace(term.SSH.Host) != "" {
		if strings.HasPrefix(command, "ssh ") || strings.HasPrefix(command, "scp ") {
			return "", fmt.Errorf("active terminal is set; do not run ssh/scp inside exec. Run the remote command directly")
		}
		remoteCmd := command
		if strings.TrimSpace(workingDir) != "" {
			remoteCmd = "cd " + shQuote(strings.TrimSpace(workingDir)) + " && " + remoteCmd
		}
		out, err := RunSSHCommand(ctx, term.SSH, remoteCmd, timeout)
		if err != nil {
			return out, err
		}
		return out, nil
	}

	if err := t.guardWorkspace(command, workingDir); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	dir, err := t.resolveWorkingDir(workingDir)
	if err != nil {
		return "", err
	}
	if dir != "" {
		cmd.Dir = dir
	}

	if t.PathAppend != "" {
		env := os.Environ()
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = e + string(os.PathListSeparator) + t.PathAppend
				break
			}
		}
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
		return "", fmt.Errorf("command timed out after %s", timeout)
	}

	out := stdout.String()
	errOut := stderr.String()
	full := out
	if errOut != "" {
		if full != "" {
			full += "\n"
		}
		full += errOut
	}
	if len(full) > 10000 {
		full = full[:10000] + "\n...(truncated)"
	}
	if err != nil {
		return strings.TrimSpace(full), err
	}
	return full, nil
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (t *ExecTool) resolveWorkingDir(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	base := strings.TrimSpace(t.WorkingDir)
	if t.RestrictToWorkspace && base == "" {
		return "", fmt.Errorf("restrictToWorkspace enabled but workspace is not set")
	}
	if arg == "" {
		return base, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			real = abs
		} else {
			return "", err
		}
	}
	if !t.RestrictToWorkspace {
		return real, nil
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	baseReal, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		if os.IsNotExist(err) {
			baseReal = baseAbs
		} else {
			return "", err
		}
	}
	baseReal = filepath.Clean(baseReal)
	real = filepath.Clean(real)
	if real == baseReal {
		return real, nil
	}
	if !strings.HasPrefix(real, baseReal+string(filepath.Separator)) {
		return "", fmt.Errorf("working_dir %s is outside workspace %s", arg, baseReal)
	}
	return real, nil
}

var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
	regexp.MustCompile(`\bdel\s+/[fq]\b`),
	regexp.MustCompile(`\brmdir\s+/s\b`),
	regexp.MustCompile(`(?:^|[;&|]\s*)format\b`),
	regexp.MustCompile(`\b(mkfs|diskpart)\b`),
	regexp.MustCompile(`\bdd\s+if=`),
	regexp.MustCompile(`>\s*/dev/sd`),
	regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
	regexp.MustCompile(`:\(\)\{.*\|.*&\}\;`),
}

func isDangerous(command string) bool {
	c := strings.ToLower(command)
	for _, p := range dangerousPatterns {
		if p.MatchString(c) {
			return true
		}
	}
	return false
}

var (
	reWinPath   = regexp.MustCompile(`[A-Za-z]:\\[^\s"'|><;]+`)
	rePosixPath = regexp.MustCompile(`(?:^|[\s|>])(/[^\s"'>]+)`)
)

// guardWorkspace blocks commands that reference paths outside the workspace
// when RestrictToWorkspace is enabled.
func (t *ExecTool) guardWorkspace(command, workingDir string) error {
	if !t.RestrictToWorkspace || t.WorkingDir == "" {
		return nil
	}
	cwd := workingDir
	if cwd == "" {
		cwd = t.WorkingDir
	}
	cwdPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	cwdPath = filepath.Clean(cwdPath)

	winPaths := reWinPath.FindAllString(command, -1)
	posixMatches := rePosixPath.FindAllStringSubmatch(command, -1)
	var posixPaths []string
	for _, m := range posixMatches {
		if len(m) > 1 {
			posixPaths = append(posixPaths, m[1])
		}
	}

	for _, raw := range append(winPaths, posixPaths...) {
		p, err := filepath.Abs(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		p = filepath.Clean(p)
		if !filepath.IsAbs(p) {
			continue
		}
		if p != cwdPath && !strings.HasPrefix(p, cwdPath+string(filepath.Separator)) {
			return fmt.Errorf("command blocked by safety guard (path outside working dir)")
		}
	}
	return nil
}
