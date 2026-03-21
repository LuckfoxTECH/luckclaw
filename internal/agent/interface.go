package agent

import (
	"luckclaw/internal/command"
	"luckclaw/internal/session"
	"luckclaw/internal/tools"
)

// Compile-time check that AgentLoop implements command.AgentInterface
var _ command.AgentInterface = (*AgentLoop)(nil)

// GetSession returns or creates a session
func (a *AgentLoop) GetSession(sessionKey string) (*session.Session, error) {
	return a.Sessions.GetOrCreate(sessionKey)
}

// SaveSession saves the session
func (a *AgentLoop) SaveSession(s *session.Session) error {
	return a.Sessions.Save(s)
}

// GetTerminalState returns the terminal state for a session
func (a *AgentLoop) GetTerminalState(sessionKey string) command.TerminalState {
	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return command.TerminalState{Terminals: map[string]command.TerminalEntry{}}
	}
	st := a.loadTerminalState(s)
	result := command.TerminalState{
		Active:    st.Active,
		Terminals: make(map[string]command.TerminalEntry),
	}
	for name, it := range st.Terminals {
		result.Terminals[name] = command.TerminalEntry{
			Type:         it.Type,
			SSH:          it.SSH,
			RemoteBins:   it.RemoteBins,
			RemoteHome:   it.RemoteHome,
			SyncedSkills: it.SyncedSkills,
			UpdatedAt:    it.RefreshedAt,
		}
	}
	return result
}

// SaveTerminalState saves the terminal state for a session
func (a *AgentLoop) SaveTerminalState(sessionKey string, state command.TerminalState) {
	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return
	}
	st := terminalState{
		Active:    state.Active,
		Terminals: make(map[string]terminalItem),
	}
	for name, e := range state.Terminals {
		st.Terminals[name] = terminalItem{
			Type:         e.Type,
			SSH:          e.SSH,
			RemoteBins:   e.RemoteBins,
			RemoteHome:   e.RemoteHome,
			SyncedSkills: e.SyncedSkills,
			RefreshedAt:  e.UpdatedAt,
		}
	}
	a.saveTerminalState(s, st)
}

// GetTerminalPassword returns the terminal password for a session
func (a *AgentLoop) GetTerminalPassword(sessionKey, name string) string {
	return a.getTerminalPassword(sessionKey, name)
}

// SetTerminalPassword sets the terminal password for a session
func (a *AgentLoop) SetTerminalPassword(sessionKey, name, password string) {
	a.setTerminalPassword(sessionKey, name, password)
}

// DeleteTerminalPassword deletes the terminal password for a session
func (a *AgentLoop) DeleteTerminalPassword(sessionKey, name string) {
	a.deleteTerminalPassword(sessionKey, name)
}

// ResolveSSHConn resolves SSH connection with password
func (a *AgentLoop) ResolveSSHConn(sessionKey, name string, conn tools.SSHConn) (tools.SSHConn, error) {
	return a.resolveSSHConn(sessionKey, name, conn)
}
