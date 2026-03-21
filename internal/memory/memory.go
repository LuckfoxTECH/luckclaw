package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"luckclaw/internal/providers/openaiapi"
	"luckclaw/internal/session"
	"luckclaw/internal/utils"
)

type Store struct {
	MemoryDir      string
	MemoryFile     string
	HistoryFile    string
	MaxInjectChars int
}

func NewStore(workspace string, maxInjectChars int) *Store {
	dir := filepath.Join(workspace, "memory")
	if maxInjectChars <= 0 {
		maxInjectChars = 4000
	}
	return &Store{
		MemoryDir:      dir,
		MemoryFile:     filepath.Join(dir, "MEMORY.md"),
		HistoryFile:    filepath.Join(dir, "HISTORY.md"),
		MaxInjectChars: maxInjectChars,
	}
}

func (s *Store) ReadLongTerm() string {
	b, err := os.ReadFile(s.MemoryFile)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) WriteLongTerm(content string) error {
	if err := os.MkdirAll(s.MemoryDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.MemoryFile, []byte(content), 0o644)
}

func (s *Store) AppendHistory(entry string) error {
	if err := os.MkdirAll(s.MemoryDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.HistoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimRight(entry, "\n") + "\n\n")
	return err
}

func (s *Store) GetMemoryContext() string {
	lt := s.ReadLongTerm()
	if lt == "" {
		return ""
	}

	prefix := "## Long-term Memory\n"
	maxContentLen := s.MaxInjectChars - len(prefix) - 20

	if maxContentLen > 0 && len(lt) > maxContentLen {
		return prefix + lt[:maxContentLen] + "\n\n[... memory truncated ...]"
	}
	return prefix + lt
}

var saveMemoryToolDef = []openaiapi.ToolDefinition{
	{
		Type: "function",
		Function: openaiapi.ToolFunction{
			Name:        "save_memory",
			Description: "Save the memory consolidation result to persistent storage.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"history_entry": map[string]any{
						"type":        "string",
						"description": "A paragraph (2-5 sentences) summarizing key events/decisions/topics. Start with [YYYY-MM-DD HH:MM]. Include detail useful for grep search.",
					},
					"memory_update": map[string]any{
						"type":        "string",
						"description": "Full updated long-term memory as markdown. Include all existing facts plus new ones. Return unchanged if nothing new.",
					},
				},
				"required": []string{"history_entry", "memory_update"},
			},
		},
	},
}

func (s *Store) Consolidate(
	ctx context.Context,
	sess *session.Session,
	provider openaiapi.ChatClient,
	model string,
	archiveAll bool,
	memoryWindow int,
) (bool, error) {
	if memoryWindow <= 0 {
		memoryWindow = 50
	}

	var oldMessages []map[string]any
	keepCount := 0

	if archiveAll {
		oldMessages = sess.Messages
	} else {
		keepCount = memoryWindow / 2
		if len(sess.Messages) <= keepCount {
			return true, nil
		}
		unconsolidated := len(sess.Messages) - sess.LastConsolidated
		if unconsolidated <= 0 {
			return true, nil
		}
		end := len(sess.Messages) - keepCount
		if end <= sess.LastConsolidated {
			return true, nil
		}
		oldMessages = sess.Messages[sess.LastConsolidated:end]
		if len(oldMessages) == 0 {
			return true, nil
		}
	}

	var lines []string
	for _, m := range oldMessages {
		content, _ := m["content"].(string)
		if content == "" {
			continue
		}
		role, _ := m["role"].(string)
		ts, _ := m["timestamp"].(string)
		if len(ts) > 16 {
			ts = ts[:16]
		}
		line := fmt.Sprintf("[%s] %s: %s", ts, strings.ToUpper(role), content)
		if toolsUsed, ok := m["tools_used"].([]any); ok && len(toolsUsed) > 0 {
			var names []string
			for _, t := range toolsUsed {
				if s, ok := t.(string); ok {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				line += " [tools: " + strings.Join(names, ", ") + "]"
			}
		}
		lines = append(lines, line)
	}

	currentMemory := s.ReadLongTerm()
	prompt := fmt.Sprintf(`Process this conversation and call the save_memory tool with your consolidation.

## Current Long-term Memory
%s

## Conversation to Process
%s`, orDefault(currentMemory, "(empty)"), strings.Join(lines, "\n"))

	res, err := provider.Chat(ctx, openaiapi.ChatRequest{
		Model: model,
		Messages: []openaiapi.Message{
			{Role: "system", Content: "You are a memory consolidation agent. Call the save_memory tool with your consolidation of the conversation."},
			{Role: "user", Content: prompt},
		},
		Tools:      saveMemoryToolDef,
		ToolChoice: "auto",
	})
	if err != nil {
		return false, err
	}

	if len(res.ToolCalls) == 0 {
		return false, nil
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(res.ToolCalls[0].Function.Arguments), &args); err != nil {
		return false, err
	}

	if entry, ok := args["history_entry"].(string); ok && entry != "" {
		_ = s.AppendHistory(entry)
	}
	if update, ok := args["memory_update"].(string); ok && update != "" && update != currentMemory {
		_ = s.WriteLongTerm(update)
	}

	if archiveAll {
		sess.LastConsolidated = 0
	} else {
		sess.LastConsolidated = len(sess.Messages) - keepCount
	}

	return true, nil
}

func (s *Store) ConsolidateWithTimeout(
	ctx context.Context,
	sess *session.Session,
	provider openaiapi.ChatClient,
	model string,
	archiveAll bool,
	memoryWindow int,
	timeout time.Duration,
) (bool, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	snapshot := s.ReadLongTerm()

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ok, err := s.Consolidate(tctx, sess, provider, model, archiveAll, memoryWindow)
	if err != nil {
		if current := s.ReadLongTerm(); current != snapshot && snapshot != "" {
			_ = s.WriteLongTerm(snapshot)
		}
		return false, err
	}
	return ok, nil
}

type ConsolidationThreshold struct {
	MessageCount int
	TokenCount   int
}

func (s *Store) ShouldConsolidate(sess *session.Session, threshold ConsolidationThreshold) (bool, string) {
	unconsolidated := sess.Messages[sess.LastConsolidated:]
	if len(unconsolidated) == 0 {
		return false, ""
	}

	if threshold.TokenCount > 0 {
		unconsolidatedTokens := utils.EstimateTokens(unconsolidated)
		if unconsolidatedTokens >= threshold.TokenCount {
			return true, fmt.Sprintf("token threshold: %d >= %d", unconsolidatedTokens, threshold.TokenCount)
		}
	}

	if threshold.MessageCount > 0 && len(unconsolidated) >= threshold.MessageCount {
		return true, fmt.Sprintf("message threshold: %d >= %d", len(unconsolidated), threshold.MessageCount)
	}

	return false, ""
}

func (s *Store) ShouldTruncate(sess *session.Session, totalTokenLimit int) (bool, string) {
	if totalTokenLimit <= 0 {
		return false, ""
	}

	totalTokens := utils.EstimateTokens(sess.Messages)
	if totalTokens >= totalTokenLimit {
		return true, fmt.Sprintf("total token limit: %d >= %d", totalTokens, totalTokenLimit)
	}

	return false, ""
}

func (s *Store) BuildOverflowNote(skipped int) string {
	if s.ReadLongTerm() != "" {
		return fmt.Sprintf(
			"[Context: %d earlier messages from this conversation have been consolidated. "+
				"Their key information is preserved in the Long-term Memory section of the system prompt above.]",
			skipped,
		)
	}
	return fmt.Sprintf(
		"[Context: %d earlier messages were truncated due to context length limits. "+
			"Some conversation history may be unavailable.]",
		skipped,
	)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
