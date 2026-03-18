package luck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type BadLuckEvent struct {
	ID          string
	CreatedAt   time.Time
	Title       string
	Fingerprint string
	Avoid       string
	Trace       TaskTrace
}

type BadLuckSummary struct {
	ID        string
	CreatedAt string
	Title     string
	Avoid     string
}

func BadLuckFilePath(workspace string) string {
	return filepath.Join(workspace, "BADLUCK.md")
}

var badLuckFileMu sync.Mutex

func NewBadLuckID(fingerprint string) string {
	if strings.TrimSpace(fingerprint) == "" {
		fingerprint = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:6])
}

func BadLuckBanner() string {
	return "( T﹏T) 💦 \n" +
		"💥 BAD LUCK\n\n"
}

func FormatBadLuckTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "💥  Bad luck"
	}
	if strings.HasPrefix(title, "💥") || strings.HasPrefix(title, "😵") || strings.HasPrefix(title, "⚠️") {
		return title
	}
	return "💥 " + title
}

func AppendBadLuckEvent(workspace string, ev BadLuckEvent) error {
	if strings.TrimSpace(workspace) == "" {
		return fmt.Errorf("workspace is empty")
	}
	path := BadLuckFilePath(workspace)

	badLuckFileMu.Lock()
	defer badLuckFileMu.Unlock()

	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte("# Bad Luck Events\n\n"), 0o644); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(renderBadLuckMarkdown(ev))
	return err
}

func ListBadLuckEvents(workspace string) ([]BadLuckSummary, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	path := BadLuckFilePath(workspace)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []BadLuckSummary
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	var cur *BadLuckSummary
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "## ") {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &BadLuckSummary{Title: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
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
		if strings.HasPrefix(line, "- avoid:") {
			cur.Avoid = strings.TrimSpace(strings.TrimPrefix(line, "- avoid:"))
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

func BuildBadLuckContext(workspace string, limit int) string {
	if limit <= 0 {
		limit = 5
	}
	items, err := ListBadLuckEvents(workspace)
	if err != nil || len(items) == 0 {
		return ""
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}

	var b strings.Builder
	b.WriteString("## Bad Luck Guardrails\n")
	b.WriteString("Avoid repeating strategies that previously failed.\n\n")
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		title := strings.TrimSpace(it.Title)
		if len(title) > 80 {
			title = title[:80] + "..."
		}
		avoid := strings.TrimSpace(it.Avoid)
		if len(avoid) > 160 {
			avoid = avoid[:160] + "..."
		}
		if avoid != "" {
			b.WriteString("- " + title + " — avoid: " + avoid + "\n")
		} else {
			b.WriteString("- " + title + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func renderBadLuckMarkdown(ev BadLuckEvent) string {
	var b strings.Builder

	title := FormatBadLuckTitle(ev.Title)
	createdAt := ev.CreatedAt.Format(time.RFC3339)
	avoid := strings.TrimSpace(ev.Avoid)
	avoidOneLine := strings.ReplaceAll(avoid, "\n", " ")
	avoidOneLine = strings.Join(strings.Fields(avoidOneLine), " ")
	if len(avoidOneLine) > 160 {
		avoidOneLine = avoidOneLine[:160] + "..."
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("## %s\n", title))
	b.WriteString(fmt.Sprintf("- id: %s\n", strings.TrimSpace(ev.ID)))
	b.WriteString(fmt.Sprintf("- createdAt: %s\n", createdAt))
	if strings.TrimSpace(ev.Fingerprint) != "" {
		b.WriteString(fmt.Sprintf("- fingerprint: %s\n", strings.TrimSpace(ev.Fingerprint)))
	}
	if avoidOneLine != "" {
		b.WriteString(fmt.Sprintf("- avoid: %s\n", avoidOneLine))
	}
	if strings.TrimSpace(ev.Trace.SessionKey) != "" {
		b.WriteString(fmt.Sprintf("- session: %s\n", strings.TrimSpace(ev.Trace.SessionKey)))
	}
	if strings.TrimSpace(ev.Trace.Channel) != "" || strings.TrimSpace(ev.Trace.ChatID) != "" {
		b.WriteString(fmt.Sprintf("- channel: %s chatId: %s\n", strings.TrimSpace(ev.Trace.Channel), strings.TrimSpace(ev.Trace.ChatID)))
	}

	if avoid != "" {
		b.WriteString("\n### Avoid Next Time\n")
		b.WriteString("```text\n")
		b.WriteString(strings.TrimRight(avoid, "\n"))
		b.WriteString("\n```\n")
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
