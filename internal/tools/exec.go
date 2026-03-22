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
	"sync"
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

// downloadCommands defines command patterns related to downloads
var downloadCommands = []string{
	"wget", "curl", "git clone", "git pull", "git fetch",
	"pip install", "pip3 install", "pip download",
	"npm install", "yarn install", "pnpm install",
	"apt install", "apt-get install", "yum install", "dnf install",
	"brew install", "apk add",
	"go install", "go get", "go mod download",
	"cargo install",
}

// isDownloadCommand checks if a command is download-related
func isDownloadCommand(command string) bool {
	cmdLower := strings.ToLower(strings.TrimSpace(command))
	for _, pattern := range downloadCommands {
		if strings.HasPrefix(cmdLower, pattern) || strings.Contains(cmdLower, " "+pattern+" ") {
			return true
		}
	}
	return false
}

// downloadMonitor monitors download status
type downloadMonitor struct {
	mu             sync.Mutex
	lastOutputSize int
	lastOutputTime time.Time
	stallThreshold time.Duration
	checkInterval  time.Duration
	isStalled      bool
	stallStartTime time.Time
	totalBytes     int
	checkCount     int
}

// newDownloadMonitor creates a new download monitor
func newDownloadMonitor(stallThreshold time.Duration) *downloadMonitor {
	return &downloadMonitor{
		lastOutputTime: time.Now(),
		stallThreshold: stallThreshold,
		checkInterval:  30 * time.Second, // Check every 30 seconds
	}
}

// updateOutput updates output status
func (m *downloadMonitor) updateOutput(outputSize int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checkCount++

	if outputSize > m.lastOutputSize {
		// New output, reset stalled state
		bytesAdded := outputSize - m.lastOutputSize
		m.totalBytes += bytesAdded
		m.lastOutputSize = outputSize
		m.lastOutputTime = time.Now()
		if m.isStalled {
			m.isStalled = false
		}
	}
}

// checkStall checks if download is stalled
// Returns: (is stalled, is slow only)
func (m *downloadMonitor) checkStall() (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	silentDuration := time.Since(m.lastOutputTime)

	if silentDuration > m.stallThreshold {
		// No output beyond threshold
		if m.totalBytes > 0 && m.checkCount > 2 {
			// Previous output indicates it's working, just slow or large file
			return true, true // Stalled, but slow download
		}
		// No output at all, possibly truly stalled
		if !m.isStalled {
			m.isStalled = true
			m.stallStartTime = time.Now()
		}
		return true, false
	}
	return false, false
}

// getStallDuration gets the duration of stall
func (m *downloadMonitor) getStallDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isStalled {
		return time.Since(m.stallStartTime)
	}
	return 0
}

// getTotalBytes gets total bytes downloaded
func (m *downloadMonitor) getTotalBytes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalBytes
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

	// Download commands use longer timeout
	isDownload := isDownloadCommand(command)
	if isDownload && t.TimeoutSeconds <= 0 {
		timeout = 300 * time.Second // Download commands default to 5 minutes
	}

	if term := TerminalFromContext(ctx); term != nil && strings.EqualFold(strings.TrimSpace(term.Type), "ssh") && strings.TrimSpace(term.SSH.Host) != "" {
		if strings.HasPrefix(command, "ssh ") || strings.HasPrefix(command, "scp ") {
			return "", fmt.Errorf("active terminal is set; do not run ssh/scp inside exec. Run the remote command directly")
		}

		// Remote download commands use monitored execution
		if isDownload {
			return t.executeRemoteWithMonitor(ctx, term, command, workingDir, timeout)
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

	// Local download commands use monitored execution
	if isDownload {
		return t.executeLocalWithMonitor(ctx, command, workingDir, timeout)
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

// executeLocalWithMonitor executes local command and monitors download status
func (t *ExecTool) executeLocalWithMonitor(ctx context.Context, command, workingDir string, timeout time.Duration) (string, error) {
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

	// Create download monitor, consider stalled if no output for 90 seconds
	monitor := newDownloadMonitor(90 * time.Second)

	// Start command
	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Monitor goroutine
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Periodically check download status
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Timeout
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				waitDone := make(chan struct{})
				go func() { _ = cmd.Wait(); close(waitDone) }()
				select {
				case <-waitDone:
				case <-time.After(5 * time.Second):
				}
			}

			out := stdout.String()
			errOut := stderr.String()
			full := combineOutput(out, errOut)

			isStalled, isSlow := monitor.checkStall()
			if isStalled {
				stallDuration := monitor.getStallDuration()
				totalBytes := monitor.getTotalBytes()
				if isSlow {
					return full, fmt.Errorf("download slow or large file detected (%s elapsed, %d bytes downloaded). Consider increasing timeout or check network speed", stallDuration.Round(time.Second), totalBytes)
				}
				diagnosis := diagnoseStall(command, full)
				return full, fmt.Errorf("download stalled for %s. %s", stallDuration.Round(time.Second), diagnosis)
			}
			return full, fmt.Errorf("command timed out after %s", timeout)

		case err := <-done:
			// Command completed
			out := stdout.String()
			errOut := stderr.String()
			full := combineOutput(out, errOut)

			if err != nil {
				return full, err
			}
			return full, nil

		case <-ticker.C:
			// Periodic check
			monitor.updateOutput(stdout.Len() + stderr.Len())

			isStalled, isSlow := monitor.checkStall()
			if isStalled {
				stallDuration := monitor.getStallDuration()
				totalBytes := monitor.getTotalBytes()

				if isSlow {
					// Slow download, continue waiting but print warning
					fmt.Printf("[download-monitor] Slow download detected: %s elapsed, %d bytes downloaded\n", stallDuration.Round(time.Second), totalBytes)
					continue
				}

				// Truly stalled, terminate after 2 minutes
				if stallDuration > 120*time.Second {
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
						waitDone := make(chan struct{})
						go func() { _ = cmd.Wait(); close(waitDone) }()
						select {
						case <-waitDone:
						case <-time.After(5 * time.Second):
						}
					}

					out := stdout.String()
					errOut := stderr.String()
					full := combineOutput(out, errOut)

					diagnosis := diagnoseStall(command, full)
					return full, fmt.Errorf("download stalled for %s. %s", stallDuration.Round(time.Second), diagnosis)
				}
			}
		}
	}
}

// executeRemoteWithMonitor executes remote command and monitors download status
func (t *ExecTool) executeRemoteWithMonitor(ctx context.Context, term *TerminalContext, command, workingDir string, timeout time.Duration) (string, error) {
	remoteCmd := command
	if strings.TrimSpace(workingDir) != "" {
		remoteCmd = "cd " + shQuote(strings.TrimSpace(workingDir)) + " && " + remoteCmd
	}

	// Execute with longer timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute directly and monitor output
	// For remote commands, we rely on SSH timeout mechanism
	out, err := RunSSHCommand(ctx, term.SSH, remoteCmd, timeout)

	if err != nil && ctx.Err() == context.DeadlineExceeded {
		diagnosis := diagnoseStall(command, out)
		return out, fmt.Errorf("download timed out. %s", diagnosis)
	}

	return out, err
}

// combineOutput combines stdout and stderr
func combineOutput(stdout, stderr string) string {
	full := stdout
	if stderr != "" {
		if full != "" {
			full += "\n"
		}
		full += stderr
	}
	if len(full) > 10000 {
		full = full[:10000] + "\n...(truncated)"
	}
	return full
}

// diagnoseStall diagnoses why download is stalled
func diagnoseStall(command, output string) string {
	var reasons []string

	cmdLower := strings.ToLower(command)
	outputLower := strings.ToLower(output)

	// Check network-related issues
	if strings.Contains(outputLower, "connection refused") ||
		strings.Contains(outputLower, "connection timed out") ||
		strings.Contains(outputLower, "network is unreachable") {
		reasons = append(reasons, "Network connection issue detected")
	}

	// Check DNS issues
	if strings.Contains(outputLower, "could not resolve host") ||
		strings.Contains(outputLower, "name resolution") {
		reasons = append(reasons, "DNS resolution failed")
	}

	// Check authentication issues
	if strings.Contains(outputLower, "permission denied") ||
		strings.Contains(outputLower, "authentication failed") ||
		strings.Contains(outputLower, "401 unauthorized") ||
		strings.Contains(outputLower, "403 forbidden") {
		reasons = append(reasons, "Authentication or permission issue")
	}

	// Check disk space
	if strings.Contains(outputLower, "no space left") ||
		strings.Contains(outputLower, "disk full") {
		reasons = append(reasons, "Disk space issue")
	}

	// Check proxy issues
	if strings.Contains(outputLower, "proxy") &&
		(strings.Contains(outputLower, "error") || strings.Contains(outputLower, "failed")) {
		reasons = append(reasons, "Proxy configuration issue")
	}

	// Check package manager lock
	if strings.Contains(outputLower, "could not get lock") ||
		strings.Contains(outputLower, "another process") {
		reasons = append(reasons, "Package manager locked by another process")
	}

	// Check git-related issues
	if strings.Contains(cmdLower, "git") {
		if strings.Contains(outputLower, "repository not found") ||
			strings.Contains(outputLower, "does not exist") {
			reasons = append(reasons, "Git repository not found or inaccessible")
		}
	}

	// Check pip-related issues
	if strings.Contains(cmdLower, "pip") {
		if strings.Contains(outputLower, "no matching distribution") {
			reasons = append(reasons, "Package not found for current platform/Python version")
		}
	}

	if len(reasons) > 0 {
		return "Possible causes: " + strings.Join(reasons, "; ")
	}

	return "Check network connectivity and try again, or run with verbose flags for more details"
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
