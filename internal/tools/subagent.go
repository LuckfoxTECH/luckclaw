package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"luckclaw/internal/config"
	"luckclaw/internal/logging"
)

// SubAgentTool lets the main agent spawn sub-agents to complete tasks in parallel.
// Results are returned synchronously to the main agent (unlike spawn which is async).
// See OpenClaw sub-agents: https://openclaw-docs.dx3n.cn/tutorials/tools/subagents
type SubAgentTool struct {
	RunAgent func(ctx context.Context, message, sessionKey string) (string, error)
	Config   config.SubAgentConfig
	Sem      *SubAgentSemaphore // limits maxConcurrent
	Logger   logging.Logger     // for multi-agent workload visibility
}

// SubAgentSemaphore limits max concurrent sub-agents.
type SubAgentSemaphore struct {
	ch chan struct{}
}

func NewSubAgentSemaphore(max int) *SubAgentSemaphore {
	if max <= 0 {
		max = 3
	}
	return &SubAgentSemaphore{ch: make(chan struct{}, max)}
}

func (s *SubAgentSemaphore) Acquire(ctx context.Context) bool {
	select {
	case s.ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *SubAgentSemaphore) Release() {
	select {
	case <-s.ch:
	default:
	}
}

func (t *SubAgentTool) Name() string { return "subagent" }

func (t *SubAgentTool) Description() string {
	return "Spawn a sub-agent to complete a specific task. The sub-agent runs with its own context and returns the result to you. Use for parallelizable subtasks (e.g. analyzing multiple codebases, researching different topics). You can spawn multiple sub-agents in parallel."
}

func (t *SubAgentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task or prompt for the sub-agent to execute",
			},
		},
		"required": []any{"task"},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if !t.Config.Enabled {
		return "", fmt.Errorf("sub-agents are disabled in config")
	}
	if t.RunAgent == nil {
		return "", fmt.Errorf("subagent not configured")
	}

	task, _ := args["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}

	// Check nesting depth from parent context
	parentMeta := SubAgentFromContext(ctx)
	depth := 1
	if parentMeta != nil {
		depth = parentMeta.Depth + 1
	}
	maxDepth := t.Config.MaxNestingDepth
	if maxDepth <= 0 {
		maxDepth = config.Default().Agents.SubAgents.MaxNestingDepth
	}
	if depth > maxDepth {
		return "", fmt.Errorf("sub-agent nesting depth %d exceeds max %d", depth, maxDepth)
	}

	// Acquire semaphore for maxConcurrent
	if t.Sem != nil && !t.Sem.Acquire(ctx) {
		return "", fmt.Errorf("max concurrent sub-agents reached, try again later")
	}
	if t.Sem != nil {
		defer t.Sem.Release()
	}

	// Build sub-agent context with tool policy and nesting
	subMeta := SubAgentMeta{Depth: depth}
	if t.Config.Inherit.Tools && len(t.Config.ToolPolicy.Allowed) > 0 {
		subMeta.Allowed = t.Config.ToolPolicy.Allowed
	}
	if len(t.Config.ToolPolicy.Disabled) > 0 {
		subMeta.Disabled = t.Config.ToolPolicy.Disabled
	}
	subCtx := WithSubAgentContext(ctx, subMeta)
	if t.Config.Model != "" {
		subCtx = WithModelOverride(subCtx, t.Config.Model)
	}

	// Timeout
	timeout := time.Duration(t.Config.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(config.Default().Agents.SubAgents.Timeout) * time.Millisecond
	}
	subCtx, cancel := context.WithTimeout(subCtx, timeout)
	defer cancel()

	// Use unique session for sub-agent (parent from channel context if available)
	channel, chatID := ChannelFromContext(ctx)
	parentSession := "main"
	if channel != "" || chatID != "" {
		parentSession = channel + ":" + chatID
	}
	sessionKey := fmt.Sprintf("subagent:%s:%d", parentSession, time.Now().UnixNano())

	if t.Logger != nil {
		taskPreview := task
		if len(taskPreview) > 120 {
			taskPreview = taskPreview[:120] + "..."
		}
		t.Logger.Info(fmt.Sprintf("[SubAgent] START session=%s depth=%d task=%q", sessionKey, depth, taskPreview))
	}

	start := time.Now()
	result, err := t.RunAgent(subCtx, task, sessionKey)
	elapsed := time.Since(start)

	if t.Logger != nil {
		status := "OK"
		if err != nil {
			status = "ERROR"
		}
		resultPreview := result
		if len(resultPreview) > 150 {
			resultPreview = resultPreview[:150] + "..."
		}
		t.Logger.Info(fmt.Sprintf("[SubAgent] DONE session=%s depth=%d status=%s elapsed=%.1fs result=%q", sessionKey, depth, status, elapsed.Seconds(), resultPreview))
	}

	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("sub-agent timed out or was cancelled")
		}
		return "", err
	}

	return result, nil
}
