package agent

import (
	"os"
	"strings"
	"time"

	"luckclaw/internal/command"
	"luckclaw/internal/session"
	"luckclaw/internal/tools"
)

type terminalState struct {
	Active    string                  `json:"active"`
	Terminals map[string]terminalItem `json:"terminals"`
}

type terminalItem struct {
	Type         string           `json:"type"`
	SSH          tools.SSHConn    `json:"ssh"`
	RemoteBins   []string         `json:"remote_bins"`
	RemoteHome   string           `json:"remote_home"`
	SyncedSkills []string         `json:"synced_skills"`
	RefreshedAt  string           `json:"refreshed_at"`
	EnvInfo      *command.EnvInfo `json:"env_info,omitempty"` // Remote terminal environment info
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

// ClearActiveTerminal clears active terminal state for session
func (a *AgentLoop) ClearActiveTerminal(sessionKey string) {
	a.activeTerminalsMu.Lock()
	defer a.activeTerminalsMu.Unlock()
	delete(a.activeTerminals, sessionKey)
}

func (a *AgentLoop) resolveSSHConn(sessionKey string, termName string, c tools.SSHConn) (tools.SSHConn, error) {
	if strings.TrimSpace(c.PasswordEnv) != "" {
		if v := os.Getenv(strings.TrimSpace(c.PasswordEnv)); strings.TrimSpace(v) != "" {
			c.Password = v
			return c, nil
		}
	}
	if strings.TrimSpace(c.PasswordEnc) != "" {
		plain, err := command.DecryptTerminalPassword(c.PasswordEnc)
		if err != nil {
			return c, err
		}
		if strings.TrimSpace(plain) != "" {
			c.Password = plain
			return c, nil
		}
	}
	if v := a.getTerminalPassword(sessionKey, termName); strings.TrimSpace(v) != "" {
		c.Password = v
	}
	return c, nil
}

func (a *AgentLoop) activeTerminalContext(sessionKey string, st terminalState) (*tools.TerminalContext, []string, string, *command.EnvInfo, error) {
	name := strings.TrimSpace(st.Active)
	if name == "" {
		return nil, nil, "", nil, nil
	}
	it, ok := st.Terminals[name]
	if !ok || strings.TrimSpace(it.Type) == "" {
		return nil, nil, "", nil, nil
	}
	sshConn, err := a.resolveSSHConn(sessionKey, name, it.SSH)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return &tools.TerminalContext{Name: name, Type: it.Type, SSH: sshConn}, it.RemoteBins, it.RemoteHome, it.EnvInfo, nil
}

func (a *AgentLoop) terminalContextForName(sessionKey string, st terminalState, name string) (*tools.TerminalContext, []string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, "", nil
	}
	it, ok := st.Terminals[name]
	if !ok || strings.TrimSpace(it.Type) == "" {
		return nil, nil, "", nil
	}
	sshConn, err := a.resolveSSHConn(sessionKey, name, it.SSH)
	if err != nil {
		return nil, nil, "", err
	}
	return &tools.TerminalContext{Name: name, Type: it.Type, SSH: sshConn}, it.RemoteBins, it.RemoteHome, nil
}

func (a *AgentLoop) loadTerminalState(s *session.Session) terminalState {
	st := terminalState{Terminals: map[string]terminalItem{}}
	store, err := command.LoadTerminalStore()
	if err == nil {
		for name, e := range store.Terminals {
			st.Terminals[name] = terminalItem{
				Type:         e.Type,
				SSH:          e.SSH,
				RemoteBins:   e.RemoteBins,
				RemoteHome:   e.RemoteHome,
				SyncedSkills: e.SyncedSkills,
				RefreshedAt:  e.UpdatedAt,
				EnvInfo:      e.EnvInfo,
			}
		}
	}

	sessionKey := sessionKeyFromSession(s)
	a.activeTerminalsMu.Lock()
	active, ok := a.activeTerminals[sessionKey]
	a.activeTerminalsMu.Unlock()

	if ok {
		st.Active = strings.TrimSpace(active)
	} else {
		st.Active = strings.TrimSpace(store.Default)
	}

	return st
}

func (a *AgentLoop) saveTerminalState(s *session.Session, st terminalState) {
	if s == nil {
		return
	}
	sessionKey := sessionKeyFromSession(s)
	a.activeTerminalsMu.Lock()
	a.activeTerminals[sessionKey] = strings.TrimSpace(st.Active)
	a.activeTerminalsMu.Unlock()

	store, err := command.LoadTerminalStore()
	if err != nil {
		store = command.TerminalStore{Terminals: map[string]command.TerminalEntry{}}
	}
	_ = a.saveterminal(store, st.Terminals)
}

func (a *AgentLoop) saveterminal(store command.TerminalStore, terminals map[string]terminalItem) error {
	store.Terminals = map[string]command.TerminalEntry{}
	for name, it := range terminals {
		store.Terminals[name] = command.TerminalEntry{
			Type:         it.Type,
			SSH:          it.SSH,
			RemoteBins:   it.RemoteBins,
			RemoteHome:   it.RemoteHome,
			SyncedSkills: it.SyncedSkills,
			UpdatedAt:    time.Now().Format(time.RFC3339Nano),
			EnvInfo:      it.EnvInfo,
		}
	}
	return command.SaveTerminalStore(store)
}

func sessionKeyFromSession(s *session.Session) string {
	if s == nil {
		return ""
	}
	return s.Key
}
