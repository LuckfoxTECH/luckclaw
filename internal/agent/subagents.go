package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/tools"
)

// handleSubagentsCmd handles /subagents list|kill|info|spawn. Returns (handled, content).
func (a *AgentLoop) handleSubagentsCmd(ctx context.Context, content, sessionKey, channel, chatID string, b *bus.MessageBus) (bool, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "/subagents") {
		return false, ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(content, "/subagents"))
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return true, a.subagentsHelp()
	}
	sub := strings.ToLower(parts[0])
	switch sub {
	case "list":
		return true, a.subagentsList(sessionKey)
	case "kill":
		if len(parts) < 2 {
			return true, "Usage: /subagents kill <runId|#|all>"
		}
		return true, a.subagentsKill(parts[1], sessionKey)
	case "info":
		if len(parts) < 2 {
			return true, "Usage: /subagents info <runId|#>"
		}
		return true, a.subagentsInfo(parts[1], sessionKey)
	case "spawn":
		if len(parts) < 2 {
			return true, "Usage: /subagents spawn <task>"
		}
		task := strings.Join(parts[1:], " ")
		return true, a.subagentsSpawn(ctx, task, sessionKey, channel, chatID, b)
	default:
		return true, a.subagentsHelp()
	}
}

func (a *AgentLoop) subagentsHelp() string {
	return `**/subagents** — manage subagent/spawn runs

Commands:
• /subagents list — list runs for this chat
• /subagents kill <runId|#|all> — cancel run(s)
• /subagents info <runId|#> — show run details
• /subagents spawn <task> — start background task`
}

func (a *AgentLoop) subagentsList(sessionKey string) string {
	if a.spawnRegistry == nil {
		return "Subagents not available."
	}
	runs := a.spawnRegistry.ListByParent(sessionKey)
	if len(runs) == 0 {
		return "No subagent/spawn runs for this chat."
	}
	var b strings.Builder
	b.WriteString("**Runs:**\n")
	for i, r := range runs {
		elapsed := ""
		if r.Status == "running" {
			elapsed = time.Since(r.StartedAt).Round(time.Second).String()
		} else if !r.FinishedAt.IsZero() {
			elapsed = r.FinishedAt.Sub(r.StartedAt).Round(time.Second).String()
		}
		taskPreview := r.Task
		if len(taskPreview) > 40 {
			taskPreview = taskPreview[:40] + "..."
		}
		b.WriteString(fmt.Sprintf("  %d. `%s` %s %s — %s\n", i+1, r.RunID, r.Status, elapsed, taskPreview))
	}
	return b.String()
}

func (a *AgentLoop) subagentsKill(target, sessionKey string) string {
	if a.spawnRegistry == nil {
		return "Subagents not available."
	}
	runs := a.spawnRegistry.ListByParent(sessionKey)
	if len(runs) == 0 {
		return "No runs to kill."
	}

	if strings.ToLower(target) == "all" {
		n := a.Queue.CancelSessionsWithPrefix("spawn:" + sessionKey)
		n += a.Queue.CancelSessionsWithPrefix("subagent:" + sessionKey + ":")
		return fmt.Sprintf("⏹ Killed %d run(s).", n)
	}

	// Try runId first
	if run := a.spawnRegistry.Get(target); run != nil && run.ParentSession == sessionKey {
		n := a.Queue.CancelSessionWithCount(run.ChildSessionKey)
		if n > 0 {
			return fmt.Sprintf("⏹ Killed run %s.", target)
		}
		return fmt.Sprintf("Run %s not running (may have finished).", target)
	}

	// Try index #
	if strings.HasPrefix(target, "#") {
		idx, err := strconv.Atoi(strings.TrimPrefix(target, "#"))
		if err != nil || idx < 1 || idx > len(runs) {
			return "Invalid index. Use #1, #2, ..."
		}
		run := runs[idx-1]
		n := a.Queue.CancelSessionWithCount(run.ChildSessionKey)
		if n > 0 {
			return fmt.Sprintf("⏹ Killed run %s (#%d).", run.RunID, idx)
		}
		return fmt.Sprintf("Run #%d not running.", idx)
	}

	return "Run not found. Use runId or #index."
}

func (a *AgentLoop) subagentsInfo(target, sessionKey string) string {
	if a.spawnRegistry == nil {
		return "Subagents not available."
	}
	runs := a.spawnRegistry.ListByParent(sessionKey)

	var run *tools.SpawnRun
	if strings.HasPrefix(target, "#") {
		idx, err := strconv.Atoi(strings.TrimPrefix(target, "#"))
		if err != nil || idx < 1 || idx > len(runs) {
			return "Invalid index."
		}
		run = runs[idx-1]
	} else {
		run = a.spawnRegistry.Get(target)
		if run != nil && run.ParentSession != sessionKey {
			run = nil
		}
	}
	if run == nil {
		return "Run not found."
	}

	elapsed := ""
	if run.Status == "running" {
		elapsed = time.Since(run.StartedAt).Round(time.Millisecond).String()
	} else if !run.FinishedAt.IsZero() {
		elapsed = run.FinishedAt.Sub(run.StartedAt).Round(time.Millisecond).String()
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Run:** %s\n", run.RunID))
	b.WriteString(fmt.Sprintf("**Status:** %s\n", run.Status))
	b.WriteString(fmt.Sprintf("**Task:** %s\n", run.Task))
	b.WriteString(fmt.Sprintf("**Session:** %s\n", run.ChildSessionKey))
	b.WriteString(fmt.Sprintf("**Started:** %s\n", run.StartedAt.Format(time.RFC3339)))
	if elapsed != "" {
		b.WriteString(fmt.Sprintf("**Runtime:** %s\n", elapsed))
	}
	if run.Result != "" {
		resultPreview := run.Result
		if len(resultPreview) > 300 {
			resultPreview = resultPreview[:300] + "..."
		}
		b.WriteString("**Result:** " + resultPreview + "\n")
	}
	if run.Err != "" {
		b.WriteString("**Error:** " + run.Err + "\n")
	}
	return b.String()
}

func (a *AgentLoop) subagentsSpawn(ctx context.Context, task, _, channel, chatID string, b *bus.MessageBus) string {
	return a.SpawnAsync(ctx, task, channel, chatID, b)
}
