package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SelfImprovingStore records tool errors and user corrections for learning.
// Ref: https://www.cnblogs.com/informatics/p/19679935 (Self-Improving Agent)
type SelfImprovingStore struct {
	FilePath string
}

// Entry is a single error or correction record.
type SelfImprovingEntry struct {
	Timestamp  string `json:"timestamp"`
	Type       string `json:"type"` // "error" | "correction"
	Tool       string `json:"tool,omitempty"`
	Context    string `json:"context,omitempty"`
	Error      string `json:"error,omitempty"`
	Correction string `json:"correction,omitempty"`
}

func NewSelfImprovingStore(workspace string) *SelfImprovingStore {
	dir := filepath.Join(workspace, "memory")
	return &SelfImprovingStore{
		FilePath: filepath.Join(dir, "self-improving.jsonl"),
	}
}

// RecordError appends a tool error to the store.
func (s *SelfImprovingStore) RecordError(tool, context, errMsg string) error {
	if err := os.MkdirAll(filepath.Dir(s.FilePath), 0o755); err != nil {
		return err
	}
	entry := SelfImprovingEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Type:      "error",
		Tool:      tool,
		Context:   truncate(context, 500),
		Error:     truncate(errMsg, 500),
	}
	return s.append(entry)
}

// RecordCorrection appends a user correction to the store.
func (s *SelfImprovingStore) RecordCorrection(correction string) error {
	if err := os.MkdirAll(filepath.Dir(s.FilePath), 0o755); err != nil {
		return err
	}
	entry := SelfImprovingEntry{
		Timestamp:  time.Now().Format("2006-01-02 15:04:05"),
		Type:       "correction",
		Correction: truncate(correction, 1000),
	}
	return s.append(entry)
}

func (s *SelfImprovingStore) append(entry SelfImprovingEntry) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// GetContext returns recent entries as markdown for system prompt injection.
func (s *SelfImprovingStore) GetContext(maxEntries int) string {
	if maxEntries <= 0 {
		maxEntries = 20
	}
	b, err := os.ReadFile(s.FilePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 {
		return ""
	}
	// Take last N entries
	start := 0
	if len(lines) > maxEntries {
		start = len(lines) - maxEntries
	}
	var sb strings.Builder
	// Frequent failures: guide model to reduce usage frequency, prefer alternatives when possible
	if deprioritized := s.deprioritizedToolsFromLines(lines, DeprioritizeRecentWindow); len(deprioritized) > 0 {
		sb.WriteString("## Tool Deprioritization\n\nThe following tools have failed frequently recently. Prefer alternatives when possible: ")
		sb.WriteString(strings.Join(deprioritized, ", "))
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Past Errors & Corrections (learn from these)\n\n")
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e SelfImprovingEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Type == "error" {
			sb.WriteString(fmt.Sprintf("- [%s] **Error** (tool=%s): %s\n", e.Timestamp, e.Tool, e.Error))
			if e.Context != "" {
				sb.WriteString(fmt.Sprintf("  Context: %s\n", e.Context))
			}
		} else if e.Type == "correction" {
			sb.WriteString(fmt.Sprintf("- [%s] **Correction**: %s\n", e.Timestamp, e.Correction))
		}
	}
	return strings.TrimSpace(sb.String())
}

// DeprioritizeThreshold: If a tool fails in more than this specified number of recent records (i.e., more than the value of DeprioritizeRecentWindow), its usage frequency will be reduced.
const DeprioritizeThreshold = 3
const DeprioritizeRecentWindow = 50

// DeprioritizedTools returns a list of tools that have failed frequently recently.
// These tools should be deprioritized, and the model should prefer using alternative tools when possible.
func (s *SelfImprovingStore) DeprioritizedTools(recentN int) []string {
	if recentN <= 0 {
		recentN = DeprioritizeRecentWindow
	}
	b, err := os.ReadFile(s.FilePath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return s.deprioritizedToolsFromLines(lines, recentN)
}

func (s *SelfImprovingStore) deprioritizedToolsFromLines(lines []string, recentN int) []string {
	if recentN <= 0 {
		recentN = DeprioritizeRecentWindow
	}
	if len(lines) == 0 {
		return nil
	}
	start := 0
	if len(lines) > recentN {
		start = len(lines) - recentN
	}
	counts := make(map[string]int)
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e SelfImprovingEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Type == "error" && e.Tool != "" {
			counts[e.Tool]++
		}
	}
	var out []string
	for tool, n := range counts {
		if n >= DeprioritizeThreshold {
			out = append(out, tool)
		}
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
