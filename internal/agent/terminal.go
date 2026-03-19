package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"luckclaw/internal/paths"
	"luckclaw/internal/session"
	"luckclaw/internal/terminal"
	"luckclaw/internal/tools"
)

const terminalMetaKey = "terminal_v1"

type terminalState struct {
	Active    string                  `json:"active"`
	Terminals map[string]terminalItem `json:"terminals"`
}

type terminalItem struct {
	Type         string        `json:"type"`
	SSH          tools.SSHConn `json:"ssh"`
	RemoteBins   []string      `json:"remote_bins"`
	RemoteHome   string        `json:"remote_home"`
	SyncedSkills []string      `json:"synced_skills"`
	RefreshedAt  string        `json:"refreshed_at"`
}

func (st terminalState) activeContext() *tools.TerminalContext {
	name := strings.TrimSpace(st.Active)
	if name == "" {
		return nil
	}
	it, ok := st.Terminals[name]
	if !ok {
		return nil
	}
	if strings.TrimSpace(it.Type) == "" {
		return nil
	}
	return &tools.TerminalContext{
		Name: name,
		Type: it.Type,
		SSH:  it.SSH,
	}
}

func (a *AgentLoop) setTerminalPassword(sessionKey string, termName string, password string) {
	sessionKey = strings.TrimSpace(sessionKey)
	termName = strings.TrimSpace(termName)
	if sessionKey == "" || termName == "" || strings.TrimSpace(password) == "" {
		return
	}
	a.terminalSecretsMu.Lock()
	defer a.terminalSecretsMu.Unlock()
	if a.terminalPasswords == nil {
		a.terminalPasswords = make(map[string]map[string]string)
	}
	m := a.terminalPasswords[sessionKey]
	if m == nil {
		m = make(map[string]string)
		a.terminalPasswords[sessionKey] = m
	}
	m[termName] = password
}

func (a *AgentLoop) getTerminalPassword(sessionKey string, termName string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	termName = strings.TrimSpace(termName)
	if sessionKey == "" || termName == "" {
		return ""
	}
	a.terminalSecretsMu.Lock()
	defer a.terminalSecretsMu.Unlock()
	if a.terminalPasswords == nil {
		return ""
	}
	if m := a.terminalPasswords[sessionKey]; m != nil {
		return m[termName]
	}
	return ""
}

func (a *AgentLoop) deleteTerminalPassword(sessionKey string, termName string) {
	sessionKey = strings.TrimSpace(sessionKey)
	termName = strings.TrimSpace(termName)
	if sessionKey == "" || termName == "" {
		return
	}
	a.terminalSecretsMu.Lock()
	defer a.terminalSecretsMu.Unlock()
	if a.terminalPasswords == nil {
		return
	}
	if m := a.terminalPasswords[sessionKey]; m != nil {
		delete(m, termName)
		if len(m) == 0 {
			delete(a.terminalPasswords, sessionKey)
		}
	}
}

func (a *AgentLoop) resolveSSHConn(sessionKey string, termName string, c tools.SSHConn) tools.SSHConn {
	if strings.TrimSpace(c.PasswordEnv) != "" {
		if v := os.Getenv(strings.TrimSpace(c.PasswordEnv)); strings.TrimSpace(v) != "" {
			c.Password = v
			return c
		}
	}
	if v := a.getTerminalPassword(sessionKey, termName); strings.TrimSpace(v) != "" {
		c.Password = v
	}
	return c
}

func (a *AgentLoop) activeTerminalContext(sessionKey string, st terminalState) (*tools.TerminalContext, []string, string) {
	name := strings.TrimSpace(st.Active)
	if name == "" {
		return nil, nil, ""
	}
	it, ok := st.Terminals[name]
	if !ok || strings.TrimSpace(it.Type) == "" {
		return nil, nil, ""
	}
	sshConn := a.resolveSSHConn(sessionKey, name, it.SSH)
	return &tools.TerminalContext{Name: name, Type: it.Type, SSH: sshConn}, it.RemoteBins, it.RemoteHome
}

func (a *AgentLoop) loadTerminalState(s *session.Session) terminalState {
	st := terminalState{Terminals: map[string]terminalItem{}}
	store, err := terminal.Load()
	if err == nil {
		for name, e := range store.Terminals {
			st.Terminals[name] = terminalItem{
				Type:         e.Type,
				SSH:          e.SSH,
				RemoteBins:   e.RemoteBins,
				RemoteHome:   e.RemoteHome,
				SyncedSkills: e.SyncedSkills,
				RefreshedAt:  e.UpdatedAt,
			}
		}
	}
	if s == nil || s.Metadata == nil {
		return st
	}
	raw := s.Metadata[terminalMetaKey]
	if raw == nil {
		if store.Default != "" {
			st.Active = store.Default
		}
		return st
	}
	b, err := json.Marshal(raw)
	if err != nil {
		if store.Default != "" {
			st.Active = store.Default
		}
		return st
	}

	var legacy terminalState
	if err := json.Unmarshal(b, &legacy); err == nil {
		if strings.TrimSpace(legacy.Active) != "" {
			st.Active = strings.TrimSpace(legacy.Active)
		} else if store.Default != "" {
			st.Active = store.Default
		}
		if len(legacy.Terminals) > 0 {
			changed := false
			for name, it := range legacy.Terminals {
				if _, ok := st.Terminals[name]; !ok {
					st.Terminals[name] = it
					changed = true
				}
			}
			if changed {
				_ = a.saveterminal(store, st.Terminals)
			}
		}
		return st
	}
	if store.Default != "" {
		st.Active = store.Default
	}
	return st
}

func (a *AgentLoop) saveTerminalState(s *session.Session, st terminalState) {
	if s == nil {
		return
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}
	s.Metadata[terminalMetaKey] = map[string]any{"active": strings.TrimSpace(st.Active)}

	store, err := terminal.Load()
	if err != nil {
		store = terminal.Store{Terminals: map[string]terminal.Entry{}}
	}
	_ = a.saveterminal(store, st.Terminals)
}

func (a *AgentLoop) saveterminal(store terminal.Store, terminals map[string]terminalItem) error {
	store.Terminals = map[string]terminal.Entry{}
	for name, it := range terminals {
		store.Terminals[name] = terminal.Entry{
			Type:         it.Type,
			SSH:          it.SSH,
			RemoteBins:   it.RemoteBins,
			RemoteHome:   it.RemoteHome,
			SyncedSkills: it.SyncedSkills,
			UpdatedAt:    time.Now().Format(time.RFC3339Nano),
		}
	}
	return terminal.Save(store)
}

func (a *AgentLoop) handleTerminalCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	st := a.loadTerminalState(s)

	if len(args) == 0 || strings.EqualFold(args[0], "help") {
		return terminalHelp(), true, ""
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		if len(st.Terminals) == 0 {
			return "No terminals configured. Use `/terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa]`.", true, ""
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
			target := it.SSH.Host
			if strings.TrimSpace(it.SSH.User) != "" {
				target = it.SSH.User + "@" + it.SSH.Host
			}
			if it.SSH.Port > 0 {
				target += ":" + strconv.Itoa(it.SSH.Port)
			}
			caps := ""
			if len(it.RemoteBins) > 0 {
				caps = " bins=" + strings.Join(it.RemoteBins, ",")
			}
			b.WriteString(fmt.Sprintf(" %s %s (%s %s)%s\n", activeMark, name, it.Type, target, caps))
		}
		return strings.TrimSpace(b.String()), true, ""

	case "info":
		name := st.Active
		if len(args) >= 2 && strings.TrimSpace(args[1]) != "" {
			name = strings.TrimSpace(args[1])
		}
		if name == "" {
			return "Usage: /terminal info [name]", true, ""
		}
		it, ok := st.Terminals[name]
		if !ok {
			return fmt.Sprintf("Error: terminal %q not found. Use `/terminal list`.", name), true, ""
		}
		target := it.SSH.Host
		if strings.TrimSpace(it.SSH.User) != "" {
			target = it.SSH.User + "@" + it.SSH.Host
		}
		if it.SSH.Port > 0 {
			target += ":" + strconv.Itoa(it.SSH.Port)
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Terminal: %s\n", name))
		b.WriteString(fmt.Sprintf("Type: %s\n", it.Type))
		b.WriteString(fmt.Sprintf("Target: %s\n", target))
		if strings.TrimSpace(it.SSH.IdentityFile) != "" {
			b.WriteString(fmt.Sprintf("Identity: %s\n", it.SSH.IdentityFile))
		}
		if it.RefreshedAt != "" {
			b.WriteString(fmt.Sprintf("Refreshed: %s\n", it.RefreshedAt))
		}
		if strings.TrimSpace(it.RemoteHome) != "" {
			b.WriteString(fmt.Sprintf("Remote home: %s\n", it.RemoteHome))
		}
		if len(it.RemoteBins) > 0 {
			b.WriteString(fmt.Sprintf("Remote bins: %s\n", strings.Join(it.RemoteBins, ", ")))
		} else {
			b.WriteString("Remote bins: (empty)\n")
		}
		auth := "key/agent"
		if strings.TrimSpace(it.SSH.PasswordEnv) != "" {
			auth = "password_env"
		} else if strings.TrimSpace(a.getTerminalPassword(sessionKey, name)) != "" {
			auth = "password(in-memory)"
		}
		b.WriteString(fmt.Sprintf("Auth: %s\n", auth))
		if len(it.SyncedSkills) > 0 {
			b.WriteString(fmt.Sprintf("Synced skills: %s\n", strings.Join(it.SyncedSkills, ", ")))
		} else {
			b.WriteString("Synced skills: (empty)\n")
		}
		return strings.TrimSpace(b.String()), true, ""

	case "off", "local":
		st.Active = ""
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		return "Terminal control OFF. exec runs locally.", true, ""

	case "use":
		if len(args) < 2 {
			return "Usage: /terminal use <name>", true, ""
		}
		name := strings.TrimSpace(args[1])
		if name == "" {
			return "Usage: /terminal use <name>", true, ""
		}
		it, ok := st.Terminals[name]
		if !ok {
			return fmt.Sprintf("Error: terminal %q not found. Use `/terminal list`.", name), true, ""
		}
		if strings.EqualFold(strings.TrimSpace(it.Type), "ssh") && strings.TrimSpace(it.SSH.Host) != "" {
			conn := a.resolveSSHConn(sessionKey, name, it.SSH)
			home, bins, err := detectRemoteInfo(ctx, conn, 20*time.Second)
			if err != nil {
				return "Error: ssh auth failed or unreachable. Set `--password-env ENV` (recommended) or `--password PASS`, then retry `/terminal use " + name + "`.\n" + err.Error(), true, ""
			}
			st.Active = name
			it.RemoteHome = home
			it.RemoteBins = bins
			it.RefreshedAt = time.Now().Format(time.RFC3339Nano)
			st.Terminals[name] = it
		} else {
			st.Active = name
		}
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		target := it.SSH.Host
		if strings.TrimSpace(it.SSH.User) != "" {
			target = it.SSH.User + "@" + it.SSH.Host
		}
		return fmt.Sprintf("Switched terminal control to %s (%s). exec runs remotely.", name, target), true, ""

	case "rm", "remove", "del", "delete":
		if len(args) < 2 {
			return "Usage: /terminal rm <name>", true, ""
		}
		name := strings.TrimSpace(args[1])
		if name == "" {
			return "Usage: /terminal rm <name>", true, ""
		}
		if _, ok := st.Terminals[name]; !ok {
			return fmt.Sprintf("Error: terminal %q not found.", name), true, ""
		}
		delete(st.Terminals, name)
		a.deleteTerminalPassword(sessionKey, name)
		if st.Active == name {
			st.Active = ""
		}
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		return fmt.Sprintf("Removed terminal %q.", name), true, ""

	case "add":
		if len(args) < 4 {
			return "Usage: /terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa] [--password-env ENV] [--password PASS] [--strict]", true, ""
		}
		name := strings.TrimSpace(args[1])
		typ := strings.ToLower(strings.TrimSpace(args[2]))
		if name == "" || typ == "" {
			return "Usage: /terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa] [--password-env ENV] [--password PASS] [--strict]", true, ""
		}
		if typ != "ssh" {
			return fmt.Sprintf("Error: unsupported terminal type %q (only ssh supported).", typ), true, ""
		}
		conn, err := parseSSHConn(args[3:])
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if strings.TrimSpace(conn.Password) != "" {
			a.setTerminalPassword(sessionKey, name, conn.Password)
			conn.Password = ""
		}
		conn.BatchMode = true
		it := terminalItem{Type: "ssh", SSH: conn}
		st.Terminals[name] = it
		if st.Active == "" {
			st.Active = name
			resolved := a.resolveSSHConn(sessionKey, name, conn)
			home, bins, err := detectRemoteInfo(ctx, resolved, 20*time.Second)
			if err != nil {
				st.Active = ""
				a.saveTerminalState(s, st)
				_ = a.Sessions.Save(s)
				return "Error: ssh auth failed or unreachable. Terminal saved but not activated.\n" + err.Error(), true, ""
			}
			it.RemoteHome = home
			it.RemoteBins = bins
			it.RefreshedAt = time.Now().Format(time.RFC3339Nano)
			st.Terminals[name] = it
		}
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		return fmt.Sprintf("Added terminal %q (ssh %s).", name, conn.Host), true, ""

	case "refresh":
		active := st.Active
		if strings.TrimSpace(active) == "" {
			return "No active terminal. Use `/terminal use <name>`.", true, ""
		}
		it, ok := st.Terminals[active]
		if !ok || !strings.EqualFold(strings.TrimSpace(it.Type), "ssh") {
			return "Active terminal is not a supported ssh terminal.", true, ""
		}
		conn := a.resolveSSHConn(sessionKey, active, it.SSH)
		home, bins, err := detectRemoteInfo(ctx, conn, 20*time.Second)
		if err != nil {
			return "Error: ssh auth failed or unreachable.\n" + err.Error(), true, ""
		}
		it.RemoteHome = home
		it.RemoteBins = bins
		it.RefreshedAt = time.Now().Format(time.RFC3339Nano)
		st.Terminals[active] = it
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		return fmt.Sprintf("Refreshed remote capabilities for %q: %s", active, strings.Join(bins, ", ")), true, ""

	case "auth":
		if len(args) < 2 {
			return "Usage: /terminal auth <name> [--password-env ENV] [--password PASS]\nTip: password is kept in memory only; password-env is recommended.", true, ""
		}
		name := strings.TrimSpace(args[1])
		if name == "" {
			return "Usage: /terminal auth <name> [--password-env ENV] [--password PASS]", true, ""
		}
		it, ok := st.Terminals[name]
		if !ok {
			return fmt.Sprintf("Error: terminal %q not found. Use `/terminal list`.", name), true, ""
		}
		target := strings.TrimSpace(it.SSH.Host)
		if strings.TrimSpace(it.SSH.User) != "" {
			target = strings.TrimSpace(it.SSH.User) + "@" + strings.TrimSpace(it.SSH.Host)
		}
		conn, err := parseSSHConn(append([]string{target}, args[2:]...))
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if strings.TrimSpace(conn.PasswordEnv) != "" {
			it.SSH.PasswordEnv = conn.PasswordEnv
			a.deleteTerminalPassword(sessionKey, name)
		}
		if strings.TrimSpace(conn.Password) != "" {
			a.setTerminalPassword(sessionKey, name, conn.Password)
			it.SSH.PasswordEnv = ""
		}
		st.Terminals[name] = it
		a.saveTerminalState(s, st)
		_ = a.Sessions.Save(s)
		return fmt.Sprintf("Updated auth for %q. Use `/terminal use %s` or `/terminal refresh` to validate.", name, name), true, ""

	case "upload":
		if len(args) < 3 {
			return "Usage: /terminal upload <local_path> <remote_path>", true, ""
		}
		return a.runTerminalTransfer(ctx, s, st, "upload", args[1], args[2])

	case "download":
		if len(args) < 3 {
			return "Usage: /terminal download <remote_path> <local_path>", true, ""
		}
		return a.runTerminalTransfer(ctx, s, st, "download", args[2], args[1])

	default:
		return terminalHelp(), true, ""
	}
}

func (a *AgentLoop) runTerminalTransfer(ctx context.Context, s *session.Session, st terminalState, direction string, localPath string, remotePath string) (string, bool, string) {
	termCtx, _, _ := a.activeTerminalContext(sessionKeyFromSession(s), st)
	if termCtx == nil {
		return "No active terminal. Use `/terminal use <name>`.", true, ""
	}
	ws, err := paths.ExpandUser(a.Config.DefaultWorkspace())
	if err != nil || strings.TrimSpace(ws) == "" {
		ws, _ = paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
	}
	ctx = tools.WithTerminalContext(ctx, termCtx)
	tool := &tools.TerminalTransferTool{
		AllowedDir:     a.AllowedDir,
		BaseDir:        ws,
		TimeoutSeconds: a.Config.Tools.Exec.Timeout,
	}
	out, err := tool.Execute(ctx, map[string]any{
		"direction":   direction,
		"local_path":  localPath,
		"remote_path": remotePath,
	})
	if err != nil {
		return "Error: " + err.Error() + "\n" + strings.TrimSpace(out), true, ""
	}
	_ = s
	return out, true, ""
}

func sessionKeyFromSession(s *session.Session) string {
	if s == nil {
		return ""
	}
	return s.Key
}

func terminalHelp() string {
	return strings.Join([]string{
		"Terminal commands:",
		"  /terminal list",
		"  /terminal info [name]",
		"  /terminal add <name> ssh <user@host> [--port 22] [--identity ~/.ssh/id_rsa] [--password-env ENV] [--password PASS] [--strict]",
		"  /terminal auth <name> [--password-env ENV] [--password PASS]",
		"  /terminal use <name>",
		"  /terminal off",
		"  /terminal refresh",
		"  /terminal upload <local_path> <remote_path>",
		"  /terminal download <remote_path> <local_path>",
	}, "\n")
}

func parseSSHConn(tokens []string) (tools.SSHConn, error) {
	var c tools.SSHConn
	if len(tokens) == 0 {
		return c, fmt.Errorf("missing ssh target")
	}
	c.StrictHostKeyChecking = false
	c.BatchMode = true

	i := 0
	target := ""
	for i < len(tokens) {
		t := strings.TrimSpace(tokens[i])
		if t == "" {
			i++
			continue
		}
		if strings.HasPrefix(t, "--") {
			switch strings.TrimPrefix(t, "--") {
			case "user":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --user")
				}
				c.User = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			case "host":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --host")
				}
				c.Host = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			case "port":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --port")
				}
				p, err := strconv.Atoi(strings.TrimSpace(tokens[i+1]))
				if err != nil || p <= 0 || p > 65535 {
					return c, fmt.Errorf("invalid --port %q", tokens[i+1])
				}
				c.Port = p
				i += 2
				continue
			case "identity", "identity_file":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --identity")
				}
				c.IdentityFile = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			case "password":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --password")
				}
				c.Password = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			case "password_env", "password-env":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for --password_env")
				}
				c.PasswordEnv = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			case "strict":
				c.StrictHostKeyChecking = true
				i++
				continue
			default:
				return c, fmt.Errorf("unknown flag %q", t)
			}
		}
		if strings.HasPrefix(t, "-") {
			switch t {
			case "-p":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for -p")
				}
				p, err := strconv.Atoi(strings.TrimSpace(tokens[i+1]))
				if err != nil || p <= 0 || p > 65535 {
					return c, fmt.Errorf("invalid -p %q", tokens[i+1])
				}
				c.Port = p
				i += 2
				continue
			case "-i":
				if i+1 >= len(tokens) {
					return c, fmt.Errorf("missing value for -i")
				}
				c.IdentityFile = strings.TrimSpace(tokens[i+1])
				i += 2
				continue
			default:
				return c, fmt.Errorf("unknown flag %q", t)
			}
		}
		if target == "" {
			target = t
			i++
			continue
		}
		i++
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

func detectRemoteInfo(ctx context.Context, c tools.SSHConn, timeout time.Duration) (string, []string, error) {
	c.BatchMode = true
	bins := []string{"python3", "python", "node", "npm", "npx", "yarn", "pnpm", "bun", "go", "java"}
	var parts []string
	parts = append(parts, `printf "__HOME__%s\n" "$HOME"`)
	for _, b := range bins {
		parts = append(parts, "command -v "+b+" >/dev/null 2>&1 && echo "+b+" || true")
	}
	out, err := tools.RunSSHCommand(ctx, c, strings.Join(parts, "; "), timeout)
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
