package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"luckclaw/internal/memory"
)

type MemorySearchTool struct {
	Store *memory.Store
}

func (t *MemorySearchTool) Name() string { return "memory_search" }

func (t *MemorySearchTool) Description() string {
	return "Mandatory recall step: semantically search MEMORY.md and history. " +
		"Use this tool before answering questions about past conversations, " +
		"user preferences, or project context that may be stored in memory."
}

func (t *MemorySearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for memory - describe what you're looking for",
			},
		},
		"required": []any{"query"},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Store == nil {
		return "", fmt.Errorf("memory store not configured")
	}

	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	query = strings.ToLower(strings.TrimSpace(query))
	results := t.searchFiles(query)

	if len(results) == 0 {
		return "No relevant memory found.", nil
	}

	return formatSearchResults(results), nil
}

func (t *MemorySearchTool) searchFiles(query string) []searchResult {
	var results []searchResult

	memoryContent := t.Store.ReadLongTerm()
	if memoryContent != "" {
		score := fuzzyScore(memoryContent, query)
		if score > 0 {
			results = append(results, searchResult{
				File:    "MEMORY.md",
				Score:   score,
				Content: memoryContent,
			})
		}
	}

	historyContent := t.readHistoryFile()
	if historyContent != "" {
		lines := strings.Split(historyContent, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			score := fuzzyScore(line, query)
			if score > 0 {
				results = append(results, searchResult{
					File:    "HISTORY.md",
					Score:   score,
					Content: line,
				})
			}
		}
	}

	for _, pattern := range []string{"*.md", "*.txt"} {
		matches, _ := filepath.Glob(filepath.Join(t.Store.MemoryDir, pattern))
		for _, match := range matches {
			if strings.HasSuffix(match, "MEMORY.md") || strings.HasSuffix(match, "HISTORY.md") {
				continue
			}
			content, err := os.ReadFile(match)
			if err != nil {
				continue
			}
			score := fuzzyScore(string(content), query)
			if score > 0 {
				results = append(results, searchResult{
					File:    filepath.Base(match),
					Score:   score,
					Content: string(content),
				})
			}
		}
	}

	for i := range results {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > 10 {
		results = results[:10]
	}

	return results
}

func (t *MemorySearchTool) readHistoryFile() string {
	b, err := os.ReadFile(t.Store.HistoryFile)
	if err != nil {
		return ""
	}
	return string(b)
}

type searchResult struct {
	File    string
	Score   int
	Content string
}

func formatSearchResults(results []searchResult) string {
	if len(results) == 0 {
		return "No relevant memory found."
	}

	var sb strings.Builder
	sb.WriteString("## Memory Search Results\n\n")

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("### %s (relevance: %d%%)\n\n", r.File, r.Score))
		content := r.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func fuzzyScore(text, query string) int {
	text = strings.ToLower(text)
	queryLower := strings.ToLower(query)

	if strings.Contains(text, queryLower) {
		return 100
	}

	words := strings.Fields(queryLower)
	matchCount := 0
	for _, word := range words {
		if strings.Contains(text, word) {
			matchCount++
		}
	}

	if matchCount == 0 {
		return 0
	}

	return (matchCount * 100) / len(words)
}

type MemoryGetTool struct {
	Store *memory.Store
}

func (t *MemoryGetTool) Name() string { return "memory_get" }

func (t *MemoryGetTool) Description() string {
	return "Read a specific portion of memory files. " +
		"Use after memory_search to read the full content of relevant files. " +
		"Supports line number (from) and line count (lines) parameters."
}

func (t *MemoryGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "File name to read (e.g., MEMORY.md, HISTORY.md)",
			},
			"from": map[string]any{
				"type":        "integer",
				"description": "Line number to start from (1-based, default: 1)",
			},
			"lines": map[string]any{
				"type":        "integer",
				"description": "Number of lines to read (default: all)",
			},
		},
	}
}

func (t *MemoryGetTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Store == nil {
		return "", fmt.Errorf("memory store not configured")
	}

	fileName, _ := args["file"].(string)
	if fileName == "" {
		fileName = "MEMORY.md"
	}

	from, _ := args["from"].(float64)
	lines, _ := args["lines"].(float64)

	var content string
	switch strings.ToLower(fileName) {
	case "memory.md":
		content = t.Store.ReadLongTerm()
	case "history.md":
		content = t.readHistoryFile()
	default:
		return "", fmt.Errorf("unknown file: %s", fileName)
	}

	if content == "" {
		return fmt.Sprintf("%s is empty or not found.", fileName), nil
	}

	return t.extractLines(content, int(from), int(lines)), nil
}

func (t *MemoryGetTool) readHistoryFile() string {
	b, err := os.ReadFile(t.Store.HistoryFile)
	if err != nil {
		return ""
	}
	return string(b)
}

func (t *MemoryGetTool) extractLines(content string, from, lines int) string {
	if from <= 0 {
		from = 1
	}

	allLines := strings.Split(content, "\n")
	if from > len(allLines) {
		return "Requested start line exceeds file length."
	}

	start := from - 1
	end := len(allLines)
	if lines > 0 {
		end = start + lines
		if end > len(allLines) {
			end = len(allLines)
		}
	}

	result := strings.Join(allLines[start:end], "\n")
	if end < len(allLines) {
		result += "\n... (more content available)"
	}

	return result
}
