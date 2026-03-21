package command

import (
	"context"
	"io"

	"luckclaw/internal/config"
	"luckclaw/internal/session"
	"luckclaw/internal/tools"
)

// TerminalState represents session-scoped terminal state
type TerminalState struct {
	Active    string
	Terminals map[string]TerminalEntry
}

// AgentInterface defines methods that AgentLoop exposes to command handlers
type AgentInterface interface {
	// Session management
	GetSession(sessionKey string) (*session.Session, error)
	SaveSession(s *session.Session) error

	// Terminal state management
	GetTerminalState(sessionKey string) TerminalState
	SaveTerminalState(sessionKey string, state TerminalState)

	// Password management
	GetTerminalPassword(sessionKey, name string) string
	SetTerminalPassword(sessionKey, name, password string)
	DeleteTerminalPassword(sessionKey, name string)

	// SSH connection resolution
	ResolveSSHConn(sessionKey, name string, conn tools.SSHConn) (tools.SSHConn, error)
}

// Input represents command input
type Input struct {
	Args       []string          // Argument list
	Flags      map[string]string // Flags/options
	Context    context.Context   // Context
	SessionKey string            // Session key (for slash commands)
	Config     *config.Config    // Configuration
	Sessions   *session.Manager  // Session manager
	Writer     io.Writer         // Output stream (for CLI)

	// Slash command specific
	Channel string          // Channel (tui, feishu, etc.)
	ChatID  string          // Chat ID
	Tools   *tools.Registry // Tool registry
	Agent   AgentInterface  // Agent interface for advanced features
}

// Output represents command output
type Output struct {
	Content    string // Output content
	IsMarkdown bool   // Whether content is Markdown format
	Error      error  // Error information
	IsFinal    bool   // Whether this is final output (for slash commands)
	ExecPrompt string // Execution prompt (for slash commands)
}

// Handler is the command handler interface
type Handler interface {
	Execute(input Input) (Output, error)
}
