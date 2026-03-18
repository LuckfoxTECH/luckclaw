package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
)

// SpawnRun represents a tracked spawn/subagent run for /subagents commands.
type SpawnRun struct {
	RunID           string
	ChildSessionKey string
	ParentSession   string
	Task            string
	Status          string // "running", "success", "error", "timeout", "cancelled"
	Result          string
	StartedAt       time.Time
	FinishedAt      time.Time
	Err             string
}

// SpawnRegistry tracks spawn runs for /subagents list|info|kill.
type SpawnRegistry struct {
	mu   sync.RWMutex
	runs map[string]*SpawnRun
}

// NewSpawnRegistry creates a new spawn registry.
func NewSpawnRegistry() *SpawnRegistry {
	return &SpawnRegistry{runs: make(map[string]*SpawnRun)}
}

// GenerateRunID returns a short unique run ID.
func GenerateRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(b)
}

// Register adds a run. Call when spawn starts.
func (r *SpawnRegistry) Register(run *SpawnRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.RunID] = run
}

// UpdateStatus updates run status. Call when spawn completes.
func (r *SpawnRegistry) UpdateStatus(runID, status, result, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[runID]; ok {
		run.Status = status
		run.Result = result
		run.Err = errMsg
		run.FinishedAt = time.Now()
	}
}

// Get returns a run by ID.
func (r *SpawnRegistry) Get(runID string) *SpawnRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runs[runID]
}

// ListByParent returns all runs for a parent session.
func (r *SpawnRegistry) ListByParent(parentSession string) []*SpawnRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*SpawnRun
	for _, run := range r.runs {
		if run.ParentSession == parentSession {
			out = append(out, run)
		}
	}
	return out
}

// ListAll returns all runs (for admin/debug).
func (r *SpawnRegistry) ListAll() []*SpawnRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SpawnRun, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run)
	}
	return out
}

// SpawnTool runs a task in the background. The result is sent via the message
// tool when complete. Use for long-running tasks that shouldn't block the chat.
// Returns runId for /subagents commands. Uses announce template for delivery.
type SpawnTool struct {
	Bus            *bus.MessageBus
	DefaultChannel string
	DefaultChatID  string
	RunAgent       func(ctx context.Context, message, sessionKey string) (string, error)
	Registry       *SpawnRegistry     // optional, for runId tracking and /subagents
	Sem            *SubAgentSemaphore // optional, limits concurrent spawns (shared with subagent)
	ModelOverride  string             // optional, use cheaper model for spawn tasks
}

func (t *SpawnTool) Name() string { return "spawn" }

func (t *SpawnTool) Description() string {
	return "Run a task in the background. The result will be sent to the chat when complete. Use for long-running tasks (e.g. research, multi-step workflows)."
}

func (t *SpawnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "The task or prompt to execute in the background",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Channel to deliver the result (default: current)",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Chat ID to deliver the result (default: current)",
			},
		},
		"required": []any{"message"},
	}
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	if message == "" {
		return "", fmt.Errorf("message is required")
	}
	channel, _ := args["channel"].(string)
	if channel == "" {
		channel = t.DefaultChannel
	}
	chatID, _ := args["chat_id"].(string)
	if chatID == "" {
		chatID = t.DefaultChatID
	}
	if t.RunAgent == nil || t.Bus == nil {
		return "", fmt.Errorf("spawn not configured")
	}

	// Acquire semaphore if configured (shared with subagent tool)
	if t.Sem != nil && !t.Sem.Acquire(ctx) {
		return "", fmt.Errorf("max concurrent spawn/subagent tasks reached, try again later")
	}

	parentSession := channel + ":" + chatID
	runID := GenerateRunID()
	childSessionKey := fmt.Sprintf("spawn:%s:%s", parentSession, runID)

	run := &SpawnRun{
		RunID:           runID,
		ChildSessionKey: childSessionKey,
		ParentSession:   parentSession,
		Task:            message,
		Status:          "running",
		StartedAt:       time.Now(),
	}
	if t.Registry != nil {
		t.Registry.Register(run)
	}

	go func() {
		if t.Sem != nil {
			defer t.Sem.Release()
		}
		bgCtx := context.Background()
		if t.ModelOverride != "" {
			bgCtx = WithModelOverride(bgCtx, t.ModelOverride)
		}
		start := time.Now()
		result, err := t.RunAgent(bgCtx, message, childSessionKey)
		elapsed := time.Since(start)

		status := "success"
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
			if err == context.Canceled || strings.Contains(errMsg, "canceled") || strings.Contains(errMsg, "cancelled") {
				status = "cancelled"
			} else if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
				status = "timeout"
			} else {
				status = "error"
			}
			result = "Error: " + errMsg
		}
		if t.Registry != nil {
			t.Registry.UpdateStatus(runID, status, result, errMsg)
		}

		content := FormatSpawnAnnounce(status, result, errMsg, elapsed, childSessionKey, runID)
		_ = t.Bus.PublishOutbound(bgCtx, bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
	}()

	out := map[string]any{
		"status":          "accepted",
		"runId":           runID,
		"childSessionKey": childSessionKey,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// FormatSpawnAnnounce formats spawn result per OpenClaw-style announce template.
func FormatSpawnAnnounce(status, result, errMsg string, elapsed time.Duration, sessionKey, runID string) string {
	var b strings.Builder
	b.WriteString("**Status:** " + status + "\n")
	b.WriteString("**Result:** ")
	if result == "" {
		b.WriteString("(not available)")
	} else {
		b.WriteString(result)
	}
	b.WriteString("\n")
	if errMsg != "" {
		b.WriteString("**Notes:** " + errMsg + "\n")
	}
	b.WriteString("\n---\n")
	b.WriteString(fmt.Sprintf("runtime %s | sessionKey %s | runId %s", elapsed.Round(time.Millisecond), sessionKey, runID))
	return b.String()
}
