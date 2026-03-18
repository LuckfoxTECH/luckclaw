package luck

import (
	"path/filepath"
	"sort"
	"strings"
)

func PreferenceNoteFromLuck(title string, trace TaskTrace) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(trace.DefaultTitle())
	}
	tools := uniqueToolNames(trace.Tools)
	files := ModifiedFiles(trace.Tools)
	files = shortenPaths(files, 6)
	toolsStr := strings.Join(tools, ", ")
	filesStr := strings.Join(files, ", ")

	var b strings.Builder
	b.WriteString("Preference signal (luck): user approved this result/strategy.")
	if title != "" {
		b.WriteString(" Event: " + title + ".")
	}
	if toolsStr != "" {
		b.WriteString(" Tools: " + toolsStr + ".")
	}
	if filesStr != "" {
		b.WriteString(" Modified files: " + filesStr + ".")
	}
	b.WriteString(" For similar tasks, prefer the same approach and output style.")
	return strings.TrimSpace(b.String())
}

func PreferenceNoteFromBadLuck(title string, avoid string, trace TaskTrace) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(trace.DefaultTitle())
	}
	avoid = strings.TrimSpace(avoid)
	avoid = oneLine(avoid, 220)
	tools := uniqueToolNames(trace.Tools)
	files := ModifiedFiles(trace.Tools)
	files = shortenPaths(files, 6)
	toolsStr := strings.Join(tools, ", ")
	filesStr := strings.Join(files, ", ")

	var b strings.Builder
	b.WriteString("Preference signal (badluck): user disliked this result/strategy.")
	if title != "" {
		b.WriteString(" Event: " + title + ".")
	}
	if avoid != "" {
		b.WriteString(" Avoid: " + avoid + ".")
	}
	if toolsStr != "" {
		b.WriteString(" Tools: " + toolsStr + ".")
	}
	if filesStr != "" {
		b.WriteString(" Files: " + filesStr + ".")
	}
	b.WriteString(" For similar tasks, avoid repeating this strategy and use alternatives.")
	return strings.TrimSpace(b.String())
}

func uniqueToolNames(tools []ToolTrace) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func shortenPaths(paths []string, max int) []string {
	if max <= 0 {
		max = 6
	}
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, filepath.Base(p))
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
