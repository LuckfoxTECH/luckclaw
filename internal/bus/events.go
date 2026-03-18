package bus

const (
	MsgResponse     = ""
	MsgToolProgress = "tool_progress"
)

type InboundMessage struct {
	Channel  string
	SenderID string
	ChatID   string
	Content  string
	Media    []string
	Metadata map[string]any
}

type OutboundMessage struct {
	Type     string // MsgResponse (default) or MsgToolProgress
	Channel  string
	ChatID   string
	Content  string
	Media    []string
	Metadata map[string]any

	// ReplyMessageID is set by channels that support message editing.
	// For tool_progress, this allows updating a previous status message
	// instead of sending a new one.
	ReplyMessageID string
}

// ToolProgressInfo carries structured data for tool_progress messages.
type ToolProgressInfo struct {
	Iteration int              `json:"iteration"`
	MaxIter   int              `json:"maxIter"`
	Tools     []ToolCallStatus `json:"tools"`
}

type ToolCallStatus struct {
	Name       string `json:"name"`
	Args       string `json:"args"`
	Status     string `json:"status"` // "running", "done", "error"
	DurationMs int64  `json:"durationMs,omitempty"`
	Error      string `json:"error,omitempty"`
}
