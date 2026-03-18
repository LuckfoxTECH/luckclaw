package luck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ToolTrace struct {
	Name       string `json:"name"`
	Args       string `json:"args,omitempty"`
	RawArgs    string `json:"rawArgs,omitempty"`
	Result     string `json:"result,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type TaskTrace struct {
	FinishedAt  string      `json:"finishedAt"`
	SessionKey  string      `json:"sessionKey,omitempty"`
	Channel     string      `json:"channel,omitempty"`
	ChatID      string      `json:"chatId,omitempty"`
	RequestRaw  string      `json:"requestRaw,omitempty"`
	RequestExec string      `json:"requestExec,omitempty"`
	Response    string      `json:"response,omitempty"`
	Tools       []ToolTrace `json:"tools,omitempty"`
}

func DecodeTaskTrace(v any) (*TaskTrace, error) {
	if v == nil {
		return nil, fmt.Errorf("empty task trace")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out TaskTrace
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.FinishedAt) == "" && strings.TrimSpace(out.RequestRaw) == "" && strings.TrimSpace(out.RequestExec) == "" && strings.TrimSpace(out.Response) == "" {
		return nil, fmt.Errorf("invalid task trace")
	}
	return &out, nil
}

func (t *TaskTrace) Fingerprint() string {
	if t == nil {
		return ""
	}
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (t *TaskTrace) DefaultTitle() string {
	if t == nil {
		return "Lucky event"
	}
	title := strings.TrimSpace(t.RequestRaw)
	if title == "" {
		title = strings.TrimSpace(t.RequestExec)
	}
	if title == "" {
		return "Lucky event"
	}
	title = strings.ReplaceAll(title, "\n", " ")
	title = strings.Join(strings.Fields(title), " ")
	if len(title) > 80 {
		title = title[:80] + "..."
	}
	return title
}

type LuckyEvent struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	Title       string    `json:"title"`
	Fingerprint string    `json:"fingerprint"`
	Trace       TaskTrace `json:"trace"`
}

type EventSummary struct {
	ID        string
	CreatedAt string
	Title     string
}

func LuckFilePath(workspace string) string {
	return filepath.Join(workspace, "LUCK.md")
}

var luckFileMu sync.Mutex

func AppendLuckyEvent(workspace string, ev LuckyEvent) error {
	if strings.TrimSpace(workspace) == "" {
		return fmt.Errorf("workspace is empty")
	}
	path := LuckFilePath(workspace)

	luckFileMu.Lock()
	defer luckFileMu.Unlock()

	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte("# Lucky Events\n\n"), 0o644); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(renderEventMarkdown(ev))
	return err
}

func ListLuckyEvents(workspace string) ([]EventSummary, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	path := LuckFilePath(workspace)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []EventSummary
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	var cur *EventSummary
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "## ") {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &EventSummary{Title: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "- id:") {
			cur.ID = strings.TrimSpace(strings.TrimPrefix(line, "- id:"))
		}
		if strings.HasPrefix(line, "- createdAt:") {
			cur.CreatedAt = strings.TrimSpace(strings.TrimPrefix(line, "- createdAt:"))
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func NewEventID(fingerprint string) string {
	if strings.TrimSpace(fingerprint) == "" {
		fingerprint = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:6])
}

func BuildFlow(tools []ToolTrace) []string {
	if len(tools) == 0 {
		return []string{"Generated final response."}
	}
	var steps []string
	seen := map[string]bool{}
	for _, t := range tools {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		var step string
		switch name {
		case "read_file":
			step = "Read required files."
		case "list_dir":
			step = "Inspected directory structure."
		case "edit_file":
			step = "Edited files to implement changes."
		case "write_file":
			step = "Wrote new files or outputs."
		case "run_command":
			step = "Ran local commands for verification or setup."
		case "web_search":
			step = "Searched the web for references."
		case "web_fetch":
			step = "Fetched web content for analysis."
		default:
			step = fmt.Sprintf("Executed tool %s.", t.Name)
		}
		if step != "" && !seen[step] {
			steps = append(steps, step)
			seen[step] = true
		}
	}
	steps = append(steps, "Generated final response.")
	return steps
}

func LuckHitBanner() string {
	return "(＾▽＾) ✨ \n" +
		"🍀 LUCK HIT\n\n"
}

func FormatLuckyTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "🍀 Lucky event"
	}
	if strings.HasPrefix(title, "🍀") || strings.HasPrefix(title, "🎉") {
		return title
	}
	return "🍀 " + title
}

func ModifiedFiles(tools []ToolTrace) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, t := range tools {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		switch name {
		case "write_file", "edit_file":
			if strings.TrimSpace(t.Args) != "" {
				add(t.Args)
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(t.RawArgs), &m); err == nil {
				if p, ok := m["path"].(string); ok {
					add(p)
				}
			}
		}
	}
	return out
}

func renderEventMarkdown(ev LuckyEvent) string {
	var b strings.Builder

	title := FormatLuckyTitle(ev.Title)
	createdAt := ev.CreatedAt.Format(time.RFC3339)

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("## %s\n", title))
	b.WriteString(fmt.Sprintf("- id: %s\n", strings.TrimSpace(ev.ID)))
	b.WriteString(fmt.Sprintf("- createdAt: %s\n", createdAt))
	if strings.TrimSpace(ev.Fingerprint) != "" {
		b.WriteString(fmt.Sprintf("- fingerprint: %s\n", strings.TrimSpace(ev.Fingerprint)))
	}
	if strings.TrimSpace(ev.Trace.SessionKey) != "" {
		b.WriteString(fmt.Sprintf("- session: %s\n", strings.TrimSpace(ev.Trace.SessionKey)))
	}
	if strings.TrimSpace(ev.Trace.Channel) != "" || strings.TrimSpace(ev.Trace.ChatID) != "" {
		b.WriteString(fmt.Sprintf("- channel: %s chatId: %s\n", strings.TrimSpace(ev.Trace.Channel), strings.TrimSpace(ev.Trace.ChatID)))
	}

	if strings.TrimSpace(ev.Trace.RequestRaw) != "" {
		b.WriteString("\n### Request (raw)\n")
		b.WriteString("```text\n")
		b.WriteString(strings.TrimRight(ev.Trace.RequestRaw, "\n"))
		b.WriteString("\n```\n")
	}
	if strings.TrimSpace(ev.Trace.RequestExec) != "" && strings.TrimSpace(ev.Trace.RequestExec) != strings.TrimSpace(ev.Trace.RequestRaw) {
		b.WriteString("\n### Request (executed)\n")
		b.WriteString("```text\n")
		b.WriteString(strings.TrimRight(ev.Trace.RequestExec, "\n"))
		b.WriteString("\n```\n")
	}

	if strings.TrimSpace(ev.Trace.Response) != "" {
		b.WriteString("\n### Response\n")
		b.WriteString("```text\n")
		b.WriteString(strings.TrimRight(ev.Trace.Response, "\n"))
		b.WriteString("\n```\n")
	}

	modified := ModifiedFiles(ev.Trace.Tools)
	b.WriteString("\n### Modified Files\n")
	if len(modified) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, p := range modified {
			b.WriteString("- " + p + "\n")
		}
	}

	b.WriteString("\n### Tool Calls\n")
	if len(ev.Trace.Tools) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, t := range ev.Trace.Tools {
			line := fmt.Sprintf("- %s(%s)", strings.TrimSpace(t.Name), strings.TrimSpace(t.Args))
			if strings.TrimSpace(t.Status) != "" {
				line += fmt.Sprintf(" [%s]", strings.TrimSpace(t.Status))
			}
			if t.DurationMs > 0 {
				line += fmt.Sprintf(" (%dms)", t.DurationMs)
			}
			if strings.TrimSpace(t.Result) != "" {
				line += fmt.Sprintf(" -> %s", strings.TrimSpace(t.Result))
			}
			if strings.TrimSpace(t.Error) != "" {
				line += fmt.Sprintf(" err=%s", strings.TrimSpace(t.Error))
			}
			b.WriteString(line + "\n")
		}
	}

	flow := BuildFlow(ev.Trace.Tools)
	b.WriteString("\n### Implementation Flow\n")
	for i, s := range flow {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(s)))
	}

	b.WriteString("\n")
	return b.String()
}
