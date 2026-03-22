package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"luckclaw/internal/paths"
	"luckclaw/internal/tools"
)

type TerminalEntry struct {
	Type         string        `json:"type"`
	SSH          tools.SSHConn `json:"ssh"`
	RemoteBins   []string      `json:"remote_bins,omitempty"`
	RemoteHome   string        `json:"remote_home,omitempty"`
	SyncedSkills []string      `json:"synced_skills,omitempty"`
	UpdatedAt    string        `json:"updated_at,omitempty"`
	EnvInfo      *EnvInfo      `json:"env_info,omitempty"` // Remote terminal environment info
}

// EnvInfo stores remote terminal environment information
type EnvInfo struct {
	OS        string `json:"os,omitempty"`             // OS type (linux, darwin, windows)
	Arch      string `json:"arch,omitempty"`           // System architecture (amd64, arm64)
	Display   string `json:"display,omitempty"`        // DISPLAY environment variable
	Desktop   string `json:"desktop,omitempty"`        // Desktop environment (GNOME, KDE, etc.)
	Browser   string `json:"browser,omitempty"`        // Default browser
	HasGUI    bool   `json:"has_gui"`                  // Has GUI
	Shell     string `json:"shell,omitempty"`          // Default shell
	UpdatedAt string `json:"env_updated_at,omitempty"` // Environment info update time
}

type TerminalStore struct {
	Default   string                   `json:"default,omitempty"`
	Terminals map[string]TerminalEntry `json:"terminals"`
	UpdatedAt string                   `json:"updated_at,omitempty"`
}

func (s TerminalStore) Names() []string {
	var out []string
	for k := range s.Terminals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s TerminalStore) Get(name string) (TerminalEntry, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TerminalEntry{}, false
	}
	it, ok := s.Terminals[name]
	return it, ok
}

func (s *TerminalStore) Set(name string, e TerminalEntry) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if s.Terminals == nil {
		s.Terminals = map[string]TerminalEntry{}
	}
	e.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	s.Terminals[name] = e
	return nil
}

func (s *TerminalStore) Remove(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if s.Terminals == nil {
		return nil
	}
	delete(s.Terminals, name)
	if s.Default == name {
		s.Default = ""
	}
	return nil
}

func terminalStorePath() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "terminals.json"), nil
}

func LoadTerminalStore() (TerminalStore, error) {
	p, err := terminalStorePath()
	if err != nil {
		return TerminalStore{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return TerminalStore{Terminals: map[string]TerminalEntry{}}, nil
		}
		return TerminalStore{}, err
	}
	var s TerminalStore
	if err := json.Unmarshal(b, &s); err != nil {
		return TerminalStore{}, err
	}
	if s.Terminals == nil {
		s.Terminals = map[string]TerminalEntry{}
	}
	return s, nil
}

func SaveTerminalStore(s TerminalStore) error {
	p, err := terminalStorePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if s.Terminals == nil {
		s.Terminals = map[string]TerminalEntry{}
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// TerminalHandler is the terminal command handler
type TerminalHandler struct{}

// Execute executes the terminal command
func (h *TerminalHandler) Execute(input Input) (Output, error) {
	if len(input.Args) == 0 {
		return h.listTerminals(input)
	}

	sub := strings.ToLower(strings.TrimSpace(input.Args[0]))
	args := input.Args[1:]

	switch sub {
	case "list", "status":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: terminal list", IsFinal: true}, nil
		}
		return h.listTerminals(input)
	case "info", "show":
		return h.showInfo(args, input)
	case "add":
		return h.addTerminal(args, input)
	case "rm", "remove", "del", "delete":
		return h.removeTerminal(args, input)
	case "use":
		return h.useTerminal(args, input)
	case "off", "local":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: terminal off", IsFinal: true}, nil
		}
		return h.clearDefault(input)
	case "auth":
		return h.updateAuth(args, input)
	case "refresh":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: terminal refresh", IsFinal: true}, nil
		}
		return h.refreshTerminal(input)
	case "upload":
		return h.uploadFile(args, input)
	case "download":
		return h.downloadFile(args, input)
	default:
		return Output{Content: fmt.Sprintf("Error: unknown subcommand %q\n\n%s", sub, h.helpText()), IsFinal: true}, nil
	}
}

func (h *TerminalHandler) listTerminals(input Input) (Output, error) {
	// CLI mode: use global store
	if input.Agent == nil {
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		names := s.Names()
		if len(names) == 0 {
			return Output{
				Content: "No terminals configured.\n\nUse `terminal add <name> ssh <user@host>` to add one.",
				IsFinal: true,
			}, nil
		}
		var b strings.Builder
		b.WriteString("Configured terminals:\n\n")
		for _, n := range names {
			it := s.Terminals[n]
			target := formatSSHHost(it.SSH)
			line := fmt.Sprintf("  - %s (%s %s)", n, it.Type, target)
			if s.Default == n {
				line += " [default]"
			}
			b.WriteString(line + "\n")
		}
		return Output{Content: b.String(), IsFinal: true}, nil
	}

	// Slash mode: use session-scoped state
	st := input.Agent.GetTerminalState(input.SessionKey)
	if len(st.Terminals) == 0 {
		return Output{Content: "No terminals configured.\n\n" + h.helpText(), IsFinal: true}, nil
	}
	var names []string
	for k := range st.Terminals {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("Terminals:\n")
	for _, name := range names {
		it := st.Terminals[name]
		activeMark := " "
		if name == st.Active {
			activeMark = "*"
		}
		target := formatSSHHost(it.SSH)
		b.WriteString(fmt.Sprintf(" %s %s (%s %s)\n", activeMark, name, it.Type, target))
	}
	b.WriteString("\n" + h.helpText())
	return Output{Content: strings.TrimSpace(b.String()), IsFinal: true}, nil
}

func (h *TerminalHandler) showInfo(args []string, input Input) (Output, error) {
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: terminal info [name]", IsFinal: true}, nil
	}
	if name == "" && input.Agent != nil {
		st := input.Agent.GetTerminalState(input.SessionKey)
		name = st.Active
	}
	if name == "" {
		return Output{Content: "Error: missing [name]\nUsage: terminal info [name]", IsFinal: true}, nil
	}

	var it TerminalEntry
	var isDefault bool

	if input.Agent == nil {
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		var ok bool
		it, ok = s.Terminals[name]
		if !ok {
			return Output{Content: fmt.Sprintf("Error: terminal %q not found. Use `terminal list`.", name), IsFinal: true}, nil
		}
		isDefault = s.Default == name
	} else {
		st := input.Agent.GetTerminalState(input.SessionKey)
		var ok bool
		it, ok = st.Terminals[name]
		if !ok {
			return Output{Content: fmt.Sprintf("Error: terminal %q not found. Use `terminal list`.", name), IsFinal: true}, nil
		}
		isDefault = st.Active == name
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Terminal: %s\n\n", name))
	b.WriteString(fmt.Sprintf("Type: %s\n", it.Type))
	b.WriteString(fmt.Sprintf("Target: %s\n", formatSSHHost(it.SSH)))
	if strings.TrimSpace(it.SSH.IdentityFile) != "" {
		b.WriteString(fmt.Sprintf("Identity: %s\n", it.SSH.IdentityFile))
	}
	if isDefault {
		b.WriteString("Default: yes\n")
	}
	if strings.TrimSpace(it.RemoteHome) != "" {
		b.WriteString(fmt.Sprintf("Remote home: %s\n", it.RemoteHome))
	}
	if len(it.RemoteBins) > 0 {
		b.WriteString(fmt.Sprintf("Remote bins: %s\n", strings.Join(it.RemoteBins, ", ")))
	}
	if it.UpdatedAt != "" {
		b.WriteString(fmt.Sprintf("Updated: %s\n", it.UpdatedAt))
	}

	return Output{Content: b.String(), IsFinal: true}, nil
}

func (h *TerminalHandler) addTerminal(args []string, input Input) (Output, error) {
	if len(args) < 3 {
		return Output{Content: "Error: missing arguments\nUsage: terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa] [--password-env ENV] [--password PASS] [--strict]", IsFinal: true}, nil
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return Output{Content: "Error: name is required\nUsage: terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa] [--password-env ENV] [--password PASS] [--strict]", IsFinal: true}, nil
	}
	typ := strings.ToLower(strings.TrimSpace(args[1]))
	if typ != "ssh" {
		return Output{Content: fmt.Sprintf("Error: unsupported terminal type %q (only ssh supported).", typ), IsFinal: true}, nil
	}

	conn, err := parseSSHConnFromArgs(args[2:], input.Flags)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	conn.BatchMode = true

	entry := TerminalEntry{Type: "ssh", SSH: conn}

	if input.Agent == nil {
		// CLI mode
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		if err := s.Set(name, entry); err != nil {
			return Output{Error: err}, nil
		}
		if err := SaveTerminalStore(s); err != nil {
			return Output{Error: err}, nil
		}
		return Output{Content: fmt.Sprintf("Saved terminal %q.", name), IsFinal: true}, nil
	}

	// Slash mode
	st := input.Agent.GetTerminalState(input.SessionKey)
	st.Terminals[name] = entry
	if st.Active == "" {
		resolved, err := input.Agent.ResolveSSHConn(input.SessionKey, name, conn)
		if err != nil {
			input.Agent.SaveTerminalState(input.SessionKey, st)
			return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
		}
		home, bins, err := detectRemoteCapabilities(ensureContext(input.Context), resolved, 20*time.Second)
		if err != nil {
			input.Agent.SaveTerminalState(input.SessionKey, st)
			return Output{Content: "Error: ssh auth failed or unreachable. Terminal saved but not activated.\n" + err.Error(), IsFinal: true}, nil
		}
		st.Active = name
		entry.RemoteHome = home
		entry.RemoteBins = bins
		entry.UpdatedAt = time.Now().Format(time.RFC3339Nano)
		st.Terminals[name] = entry
	}
	input.Agent.SaveTerminalState(input.SessionKey, st)
	return Output{Content: fmt.Sprintf("Added terminal %q (ssh %s).", name, conn.Host), IsFinal: true}, nil
}

func (h *TerminalHandler) removeTerminal(args []string, input Input) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <name>\nUsage: terminal rm <name>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: terminal rm <name>", IsFinal: true}, nil
	}
	name := strings.TrimSpace(args[0])

	if input.Agent == nil {
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		if _, ok := s.Terminals[name]; !ok {
			return Output{Content: fmt.Sprintf("Error: terminal %q not found.", name), IsFinal: true}, nil
		}
		if err := s.Remove(name); err != nil {
			return Output{Error: err}, nil
		}
		if err := SaveTerminalStore(s); err != nil {
			return Output{Error: err}, nil
		}
		return Output{Content: fmt.Sprintf("Removed terminal %q.", name), IsFinal: true}, nil
	}

	st := input.Agent.GetTerminalState(input.SessionKey)
	if _, ok := st.Terminals[name]; !ok {
		return Output{Content: fmt.Sprintf("Error: terminal %q not found.", name), IsFinal: true}, nil
	}
	delete(st.Terminals, name)
	if st.Active == name {
		st.Active = ""
	}
	input.Agent.DeleteTerminalPassword(input.SessionKey, name)
	input.Agent.SaveTerminalState(input.SessionKey, st)
	return Output{Content: fmt.Sprintf("Removed terminal %q.", name), IsFinal: true}, nil
}

func (h *TerminalHandler) useTerminal(args []string, input Input) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <name>\nUsage: terminal use <name>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: terminal use <name>", IsFinal: true}, nil
	}
	name := strings.TrimSpace(args[0])

	if input.Agent == nil {
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		if _, ok := s.Terminals[name]; !ok {
			return Output{Content: fmt.Sprintf("Error: terminal %q not found.", name), IsFinal: true}, nil
		}
		s.Default = name
		if err := SaveTerminalStore(s); err != nil {
			return Output{Error: err}, nil
		}
		return Output{Content: fmt.Sprintf("Default terminal set to %q.", name), IsFinal: true}, nil
	}

	st := input.Agent.GetTerminalState(input.SessionKey)
	it, ok := st.Terminals[name]
	if !ok {
		return Output{Content: fmt.Sprintf("Error: terminal %q not found. Use `terminal list`.", name), IsFinal: true}, nil
	}
	if strings.EqualFold(strings.TrimSpace(it.Type), "ssh") && strings.TrimSpace(it.SSH.Host) != "" {
		conn, err := input.Agent.ResolveSSHConn(input.SessionKey, name, it.SSH)
		if err != nil {
			return Output{Content: "Error: " + err.Error() + "\nTip: set LUCKCLAW_TERMINAL_KEY_SEED or use `--password-env ENV`.", IsFinal: true}, nil
		}
		home, bins, err := detectRemoteCapabilities(ensureContext(input.Context), conn, 20*time.Second)
		if err != nil {
			return Output{Content: "Error: ssh auth failed or unreachable. Set `--password-env ENV` (recommended) or `--password PASS`, then retry `terminal use " + name + "`.\n" + err.Error(), IsFinal: true}, nil
		}
		it.RemoteHome = home
		it.RemoteBins = bins
		it.UpdatedAt = time.Now().Format(time.RFC3339Nano)

		// Detect environment info
		envInfo, err := detectRemoteEnvInfo(ensureContext(input.Context), conn, 20*time.Second)
		if err == nil && envInfo != nil {
			it.EnvInfo = envInfo
		}

		st.Terminals[name] = it
	}
	st.Active = name
	input.Agent.SaveTerminalState(input.SessionKey, st)

	target := formatSSHHost(it.SSH)
	return Output{Content: fmt.Sprintf("Switched terminal control to %s (%s). exec runs remotely.", name, target), IsFinal: true}, nil
}

func (h *TerminalHandler) clearDefault(input Input) (Output, error) {
	if input.Agent == nil {
		s, err := LoadTerminalStore()
		if err != nil {
			return Output{Error: err}, nil
		}
		s.Default = ""
		if err := SaveTerminalStore(s); err != nil {
			return Output{Error: err}, nil
		}
		return Output{Content: "Default terminal cleared.", IsFinal: true}, nil
	}

	st := input.Agent.GetTerminalState(input.SessionKey)
	st.Active = ""
	input.Agent.SaveTerminalState(input.SessionKey, st)
	return Output{Content: "Terminal control OFF. exec runs locally.", IsFinal: true}, nil
}

func (h *TerminalHandler) updateAuth(args []string, input Input) (Output, error) {
	if input.Agent == nil {
		return Output{Content: "Error: auth update only available in interactive mode.", IsFinal: true}, nil
	}
	if len(args) < 1 {
		return Output{Content: "Error: missing <name>\nUsage: terminal auth <name> [--password-env ENV] [--password PASS]", IsFinal: true}, nil
	}
	name := strings.TrimSpace(args[0])
	st := input.Agent.GetTerminalState(input.SessionKey)
	it, ok := st.Terminals[name]
	if !ok {
		return Output{Content: fmt.Sprintf("Error: terminal %q not found.", name), IsFinal: true}, nil
	}

	passwordEnv := input.Flags["password-env"]
	password := input.Flags["password"]
	for i := 1; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: terminal auth <name> [--password-env ENV] [--password PASS]", args[i]), IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for %s\nUsage: terminal auth <name> [--password-env ENV] [--password PASS]", args[i]), IsFinal: true}, nil
		}
		switch strings.ToLower(args[i]) {
		case "--password-env":
			passwordEnv = strings.TrimSpace(args[i+1])
		case "--password":
			password = strings.TrimSpace(args[i+1])
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag %s\nUsage: terminal auth <name> [--password-env ENV] [--password PASS]", args[i]), IsFinal: true}, nil
		}
		i++
	}

	if strings.TrimSpace(passwordEnv) != "" {
		it.SSH.PasswordEnv = passwordEnv
		it.SSH.PasswordEnc = ""
		input.Agent.DeleteTerminalPassword(input.SessionKey, name)
	}
	if strings.TrimSpace(password) != "" {
		enc, err := EncryptTerminalPassword(password)
		if err != nil {
			return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
		}
		input.Agent.SetTerminalPassword(input.SessionKey, name, password)
		it.SSH.PasswordEnc = enc
		it.SSH.PasswordEnv = ""
	}
	st.Terminals[name] = it
	input.Agent.SaveTerminalState(input.SessionKey, st)
	return Output{Content: fmt.Sprintf("Updated auth for %q.", name), IsFinal: true}, nil
}

func (h *TerminalHandler) refreshTerminal(input Input) (Output, error) {
	if input.Agent == nil {
		return Output{Content: "Error: refresh only available in interactive mode.", IsFinal: true}, nil
	}
	st := input.Agent.GetTerminalState(input.SessionKey)
	active := st.Active
	if strings.TrimSpace(active) == "" {
		return Output{Content: "Error: no active terminal. Use `terminal use <name>`.", IsFinal: true}, nil
	}
	it, ok := st.Terminals[active]
	if !ok || !strings.EqualFold(strings.TrimSpace(it.Type), "ssh") {
		return Output{Content: "Error: active terminal is not a supported ssh terminal.", IsFinal: true}, nil
	}
	conn, err := input.Agent.ResolveSSHConn(input.SessionKey, active, it.SSH)
	if err != nil {
		return Output{Content: "Error: " + err.Error() + "\nTip: set LUCKCLAW_TERMINAL_KEY_SEED or use `--password-env ENV`.", IsFinal: true}, nil
	}
	home, bins, err := detectRemoteCapabilities(ensureContext(input.Context), conn, 20*time.Second)
	if err != nil {
		return Output{Content: "Error: ssh auth failed or unreachable.\n" + err.Error(), IsFinal: true}, nil
	}
	it.RemoteHome = home
	it.RemoteBins = bins
	it.UpdatedAt = time.Now().Format(time.RFC3339Nano)

	// Detect environment info
	envInfo, err := detectRemoteEnvInfo(ensureContext(input.Context), conn, 20*time.Second)
	if err == nil && envInfo != nil {
		it.EnvInfo = envInfo
	}

	st.Terminals[active] = it
	input.Agent.SaveTerminalState(input.SessionKey, st)
	return Output{Content: fmt.Sprintf("Refreshed remote capabilities for %q: %s", active, strings.Join(bins, ", ")), IsFinal: true}, nil
}

func (h *TerminalHandler) uploadFile(args []string, input Input) (Output, error) {
	if input.Agent == nil {
		return Output{Content: "Error: upload only available in interactive mode.", IsFinal: true}, nil
	}
	if len(args) < 2 {
		return Output{Content: "Error: missing arguments\nUsage: terminal upload [name] <local_path> <remote_path>", IsFinal: true}, nil
	}
	if len(args) > 3 {
		return Output{Content: "Error: too many arguments\nUsage: terminal upload [name] <local_path> <remote_path>", IsFinal: true}, nil
	}
	st := input.Agent.GetTerminalState(input.SessionKey)
	termName := ""
	localPath := ""
	remotePath := ""

	if len(args) == 3 {
		candidate := strings.TrimSpace(args[0])
		if _, ok := st.Terminals[candidate]; !ok {
			return Output{Content: fmt.Sprintf("Error: unknown terminal %q\nUsage: terminal upload [name] <local_path> <remote_path>", candidate), IsFinal: true}, nil
		}
		termName = candidate
		localPath = args[1]
		remotePath = args[2]
	}
	if termName == "" {
		localPath = args[0]
		remotePath = args[1]
	}

	return h.runTerminalTransfer(input, st, "upload", termName, localPath, remotePath)
}

func (h *TerminalHandler) downloadFile(args []string, input Input) (Output, error) {
	if input.Agent == nil {
		return Output{Content: "Error: download only available in interactive mode.", IsFinal: true}, nil
	}
	if len(args) < 2 {
		return Output{Content: "Error: missing arguments\nUsage: terminal download [name] <remote_path> <local_path>", IsFinal: true}, nil
	}
	if len(args) > 3 {
		return Output{Content: "Error: too many arguments\nUsage: terminal download [name] <remote_path> <local_path>", IsFinal: true}, nil
	}
	st := input.Agent.GetTerminalState(input.SessionKey)
	termName := ""
	remotePath := ""
	localPath := ""

	if len(args) == 3 {
		candidate := strings.TrimSpace(args[0])
		if _, ok := st.Terminals[candidate]; !ok {
			return Output{Content: fmt.Sprintf("Error: unknown terminal %q\nUsage: terminal download [name] <remote_path> <local_path>", candidate), IsFinal: true}, nil
		}
		termName = candidate
		remotePath = args[1]
		localPath = args[2]
	}
	if termName == "" {
		remotePath = args[0]
		localPath = args[1]
	}

	return h.runTerminalTransfer(input, st, "download", termName, localPath, remotePath)
}

func (h *TerminalHandler) runTerminalTransfer(input Input, st TerminalState, direction string, termName string, localPath string, remotePath string) (Output, error) {
	termName = strings.TrimSpace(termName)
	if termName == "" {
		termName = strings.TrimSpace(st.Active)
	}
	if termName == "" {
		if store, err := LoadTerminalStore(); err == nil {
			termName = strings.TrimSpace(store.Default)
		}
	}
	if termName == "" {
		return Output{Content: "Error: no terminal selected. Use `terminal use <name>` or `terminal upload <name> ...`.", IsFinal: true}, nil
	}

	it, ok := st.Terminals[termName]
	if !ok {
		return Output{Content: fmt.Sprintf("Error: terminal %q not found. Use `terminal list`.", termName), IsFinal: true}, nil
	}
	if !strings.EqualFold(strings.TrimSpace(it.Type), "ssh") {
		return Output{Content: fmt.Sprintf("Error: unsupported terminal type %q", it.Type), IsFinal: true}, nil
	}

	sshConn, err := input.Agent.ResolveSSHConn(input.SessionKey, termName, it.SSH)
	if err != nil {
		return Output{Content: "Error: " + err.Error() + "\nTip: set LUCKCLAW_TERMINAL_KEY_SEED or use `--password-env ENV`.", IsFinal: true}, nil
	}

	ws, allowedDir := resolveWorkspaceAndAllowedDir(input)
	ctx := tools.WithTerminalContext(ensureContext(input.Context), &tools.TerminalContext{Name: termName, Type: it.Type, SSH: sshConn})
	timeoutSeconds := 0
	if input.Config != nil {
		timeoutSeconds = input.Config.Tools.Exec.Timeout
	}
	tool := &tools.TerminalTransferTool{
		AllowedDir:     allowedDir,
		BaseDir:        ws,
		TimeoutSeconds: timeoutSeconds,
	}
	out, err := tool.Execute(ctx, map[string]any{
		"direction":   direction,
		"local_path":  localPath,
		"remote_path": remotePath,
	})
	if err != nil {
		msg := "Error: " + err.Error()
		if strings.TrimSpace(out) != "" {
			msg += "\n" + strings.TrimSpace(out)
		}
		return Output{Content: msg, IsFinal: true}, nil
	}
	return Output{Content: out, IsFinal: true}, nil
}

func parseSSHConnFromArgs(tokens []string, flags map[string]string) (tools.SSHConn, error) {
	var c tools.SSHConn
	if len(tokens) == 0 {
		return c, fmt.Errorf("missing ssh target")
	}
	c.StrictHostKeyChecking = false
	c.BatchMode = true

	target := ""
	for i := 0; i < len(tokens); i++ {
		t := strings.TrimSpace(tokens[i])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "--") {
			key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "--")))
			if key == "" {
				return c, fmt.Errorf("invalid flag")
			}
			if key == "strict" {
				c.StrictHostKeyChecking = true
				continue
			}
			if i+1 >= len(tokens) {
				return c, fmt.Errorf("missing value for --%s", key)
			}
			val := strings.TrimSpace(tokens[i+1])
			switch key {
			case "user":
				c.User = val
			case "host":
				c.Host = val
			case "port":
				p, err := strconv.Atoi(val)
				if err != nil || p <= 0 || p > 65535 {
					return c, fmt.Errorf("invalid port")
				}
				c.Port = p
			case "identity", "identity_file":
				c.IdentityFile = val
			case "password":
				c.Password = val
			case "password_env", "password-env":
				c.PasswordEnv = val
			default:
				return c, fmt.Errorf("unknown flag --%s", key)
			}
			i++
			continue
		}
		if target != "" {
			return c, fmt.Errorf("unexpected argument %q", t)
		}
		target = t
	}

	// Parse flags map
	for k := range flags {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "port", "identity", "password-env", "password", "strict":
		default:
			return c, fmt.Errorf("unknown flag --%s", k)
		}
	}
	if port, ok := flags["port"]; ok {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 {
			return c, fmt.Errorf("invalid port")
		}
		c.Port = p
	}
	if identity, ok := flags["identity"]; ok {
		c.IdentityFile = identity
	}
	if passwordEnv, ok := flags["password-env"]; ok {
		c.PasswordEnv = passwordEnv
	}
	if password, ok := flags["password"]; ok {
		c.Password = password
	}
	if strict, ok := flags["strict"]; ok && strict != "" && strict != "false" && strict != "0" {
		c.StrictHostKeyChecking = true
	}

	if strings.TrimSpace(c.Host) == "" {
		if target == "" {
			return c, fmt.Errorf("missing ssh target")
		}
		if at := strings.Index(target, "@"); at >= 0 {
			c.User = strings.TrimSpace(target[:at])
			c.Host = strings.TrimSpace(target[at+1:])
		} else {
			c.Host = strings.TrimSpace(target)
		}
	}
	if strings.TrimSpace(c.Host) == "" {
		return c, fmt.Errorf("host is required")
	}
	return c, nil
}

func formatSSHHost(conn tools.SSHConn) string {
	target := conn.Host
	if strings.TrimSpace(conn.User) != "" {
		target = conn.User + "@" + conn.Host
	}
	if conn.Port > 0 {
		target += ":" + strconv.Itoa(conn.Port)
	}
	return target
}

func (h *TerminalHandler) helpText() string {
	return `Terminal commands:
  terminal list                              List saved terminals
  terminal info [name]                       Show terminal details
  terminal add <name> ssh <user@host>        Add or update a terminal
  terminal rm <name>                         Remove a saved terminal
  terminal use <name>                        Set active/default terminal
  terminal off                               Clear active/default terminal
  terminal auth <name> [--password-env ENV]  Update terminal auth
  terminal refresh                           Refresh remote capabilities
  terminal upload [name] <local> <remote>    Upload local path to remote
  terminal download [name] <remote> <local>  Download remote path to local`
}

func ensureContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func resolveWorkspaceAndAllowedDir(input Input) (string, string) {
	ws := ""
	if input.Config != nil {
		if v, err := paths.ExpandUser(input.Config.DefaultWorkspace()); err == nil && strings.TrimSpace(v) != "" {
			ws = v
		}
		if strings.TrimSpace(ws) == "" {
			if v, err := paths.ExpandUser(input.Config.Agents.Defaults.Workspace); err == nil && strings.TrimSpace(v) != "" {
				ws = v
			}
		}
	}
	if strings.TrimSpace(ws) == "" {
		if v, err := paths.ExpandUser("~/luckclaw"); err == nil {
			ws = v
		}
	}
	ws = strings.TrimSpace(ws)
	if ws == "" {
		ws = "/"
	}

	allowedDir := "/"
	if input.Config != nil && input.Config.Tools.RestrictToWorkspace {
		allowedDir = ws
	}
	return ws, allowedDir
}

func detectRemoteCapabilities(ctx context.Context, c tools.SSHConn, timeout time.Duration) (string, []string, error) {
	c.BatchMode = true
	bins := []string{"python3", "python", "node", "npm", "npx", "yarn", "pnpm", "bun", "go", "java"}
	var parts []string
	parts = append(parts, `printf "__HOME__%s\n" "$HOME"`)
	for _, b := range bins {
		parts = append(parts, "command -v "+b+" >/dev/null 2>&1 && echo "+b+" || true")
	}
	out, err := tools.RunSSHCommand(ensureContext(ctx), c, strings.Join(parts, "; "), timeout)
	if err != nil {
		return "", nil, err
	}
	home := ""
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "__HOME__") {
			home = strings.TrimSpace(strings.TrimPrefix(v, "__HOME__"))
			continue
		}
		set[v] = true
	}
	var found []string
	for _, b := range bins {
		if set[b] {
			found = append(found, b)
		}
	}
	return home, found, nil
}

// detectRemoteEnvInfo detects remote terminal environment information
func detectRemoteEnvInfo(ctx context.Context, c tools.SSHConn, timeout time.Duration) (*EnvInfo, error) {
	c.BatchMode = true
	// Commands to collect environment info
	commands := []string{
		`uname -s`,                        // OS
		`uname -m`,                        // Architecture
		`echo "${DISPLAY:-}"`,             // DISPLAY variable
		`echo "${XDG_CURRENT_DESKTOP:-}"`, // Desktop environment
		`echo "${SHELL:-}"`,               // Default shell
		// Detect browsers
		`command -v firefox >/dev/null 2>&1 && echo "firefox" || true`,
		`command -v google-chrome >/dev/null 2>&1 && echo "google-chrome" || true`,
		`command -v chromium-browser >/dev/null 2>&1 && echo "chromium-browser" || true`,
		`command -v chromium >/dev/null 2>&1 && echo "chromium" || true`,
		`command -v safari >/dev/null 2>&1 && echo "safari" || true`,
		`command -v open >/dev/null 2>&1 && echo "open" || true`, // macOS open command
	}

	out, err := tools.RunSSHCommand(ensureContext(ctx), c, strings.Join(commands, "; "), timeout)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out, "\n")
	info := &EnvInfo{
		UpdatedAt: time.Now().Format(time.RFC3339Nano),
	}

	// Parse output
	if len(lines) > 0 {
		info.OS = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		info.Arch = strings.TrimSpace(lines[1])
	}
	if len(lines) > 2 {
		info.Display = strings.TrimSpace(lines[2])
	}
	if len(lines) > 3 {
		info.Desktop = strings.TrimSpace(lines[3])
	}
	if len(lines) > 4 {
		info.Shell = strings.TrimSpace(lines[4])
	}

	// Detect browsers
	var browsers []string
	for i := 5; i < len(lines); i++ {
		b := strings.TrimSpace(lines[i])
		if b != "" {
			browsers = append(browsers, b)
		}
	}
	if len(browsers) > 0 {
		info.Browser = browsers[0] // Use first detected browser
	}

	// Determine if GUI is available
	// macOS doesn't have DISPLAY variable but has GUI
	// Linux needs DISPLAY variable or desktop environment
	info.HasGUI = false
	if info.OS == "darwin" {
		// macOS always has GUI
		info.HasGUI = true
	} else if info.Display != "" || info.Desktop != "" {
		// Linux has DISPLAY or desktop environment
		info.HasGUI = true
	}

	return info, nil
}
