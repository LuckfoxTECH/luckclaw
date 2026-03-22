package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ANSI escape codes (dark theme)
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiItalic = "\033[3m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
	ansiYellow = "\033[33m"
	ansiThink  = ansiItalic + "\033[38;5;109m" // less dim, specific color (e.g. 109 is a muted cyan/teal)
)

// renderMarkdownSimple renders basic markdown to ANSI. No syntax highlighting.
// Supports: **bold**, *italic*, `code`, ```blocks```, # headers, - lists, [links](url)
func renderMarkdownSimple(s string, width int) string {
	if s == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(s, "\n")
	var out []string
	inCodeBlock := false
	inThink := false

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")

		// Code block fence
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			out = append(out, ansiGray+trimmed+ansiReset)
			continue
		}

		if inCodeBlock {
			out = append(out, ansiGray+line+ansiReset)
			continue
		}

		if inThink || strings.Contains(trimmed, "<think>") || strings.Contains(trimmed, "</think>") {
			rendered, nextInThink := renderThinkSegments(trimmed, inThink)
			inThink = nextInThink
			out = append(out, rendered)
			continue
		}

		// Header
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for i := 0; i < len(trimmed) && trimmed[i] == '#'; i++ {
				level++
			}
			rest := strings.TrimSpace(trimmed[level:])
			rest = applyInline(rest)
			out = append(out, ansiBold+ansiYellow+rest+ansiReset)
			continue
		}

		// List item
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			rest := strings.TrimSpace(trimmed[2:])
			rest = applyInline(rest)
			out = append(out, "  "+ansiBold+"•"+ansiReset+" "+rest)
			continue
		}

		// Numbered list
		if prefix, consume := numListPrefix(trimmed); prefix != "" {
			if consume > len(trimmed) {
				consume = len(trimmed)
			}
			rest := strings.TrimSpace(trimmed[consume:])
			rest = applyInline(rest)
			out = append(out, "  "+prefix+rest)
			continue
		}

		// Blockquote
		if strings.HasPrefix(trimmed, ">") {
			rest := strings.TrimSpace(trimmed[1:])
			rest = applyInline(rest)
			out = append(out, ansiGray+"│ "+rest+ansiReset)
			continue
		}

		// Normal line
		rendered := applyInline(trimmed)
		out = append(out, rendered)
	}

	result := strings.Join(out, "\n")
	return WordWrapANSI(result, width)
}

func renderThinkSegments(line string, inThink bool) (string, bool) {
	const startTag = "<think>"
	const endTag = "</think>"

	var b strings.Builder
	rest := line

	for {
		startIdx := strings.Index(rest, startTag)
		endIdx := strings.Index(rest, endTag)
		if startIdx == -1 && endIdx == -1 {
			if inThink {
				b.WriteString(ansiThink)
				b.WriteString(rest)
				b.WriteString(ansiReset)
			} else {
				b.WriteString(applyInline(rest))
			}
			break
		}

		idx := len(rest)
		isStart := false
		if startIdx != -1 && startIdx < idx {
			idx = startIdx
			isStart = true
		}
		if endIdx != -1 && endIdx < idx {
			idx = endIdx
			isStart = false
		}

		before := rest[:idx]
		if before != "" {
			if inThink {
				b.WriteString(ansiThink)
				b.WriteString(before)
				b.WriteString(ansiReset)
			} else {
				b.WriteString(applyInline(before))
			}
		}

		if isStart {
			inThink = true
			rest = rest[idx+len(startTag):]
		} else {
			inThink = false
			rest = rest[idx+len(endTag):]
		}
	}

	return b.String(), inThink
}

func numListPrefix(s string) (prefix string, consume int) {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			continue
		}
		if i > 0 && (s[i] == '.' || s[i] == ')') {
			consume = i + 1
			if i+1 < len(s) && s[i+1] == ' ' {
				consume = i + 2
			}
			return s[:i+1] + " ", consume
		}
		break
	}
	return "", 0
}

// applyInline applies inline markdown: **bold**, *italic*, `code`, [link](url)
func applyInline(s string) string {
	// Order matters: do code first (backticks), then bold, italic, links
	// Use placeholder to avoid re-processing
	s = replaceInlineCode(s)
	s = replaceBold(s)
	s = replaceItalic(s)
	s = replaceLinks(s)
	return s
}

var (
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reBold       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic     = regexp.MustCompile(`\*([^*]+)\*`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func replaceInlineCode(s string) string {
	return reInlineCode.ReplaceAllString(s, ansiDim+"$1"+ansiReset)
}

func replaceBold(s string) string {
	return reBold.ReplaceAllString(s, ansiBold+"$1"+ansiReset)
}

func replaceItalic(s string) string {
	return reItalic.ReplaceAllString(s, ansiItalic+"$1"+ansiReset)
}

func replaceLinks(s string) string {
	return reLink.ReplaceAllString(s, ansiCyan+"$1"+ansiReset+" ($2)")
}

// WordWrapANSI wraps text at width, ignoring ANSI codes when measuring.
func WordWrapANSI(s string, width int) string {
	return wordWrapANSI(s, width)
}

// wordWrapANSI wraps text at width, ignoring ANSI codes when measuring.
func wordWrapANSI(s string, width int) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		out = append(out, wrapLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) []string {
	if width <= 0 || visibleLen(line) <= width {
		return []string{line}
	}
	var result []string
	var buf strings.Builder
	visible := 0
	i := 0
	for i < len(line) {
		// Copy ANSI escape sequences
		if line[i] == '\033' && i+1 < len(line) && line[i+1] == '[' {
			start := i
			i += 2
			for i < len(line) && line[i] != 'm' {
				i++
			}
			if i < len(line) {
				i++
				buf.WriteString(line[start:i])
			}
			continue
		}

		cluster, clusterW, size := nextGrapheme(line[i:])
		// Break before this grapheme if we'd exceed width
		if visible > 0 && visible+clusterW > width {
			content := buf.String()
			lastSpaceIdx := findLastSpace(content)
			if lastSpaceIdx >= 0 {
				result = append(result, strings.TrimRight(content[:lastSpaceIdx], " \t"))
				remain := strings.TrimLeft(content[lastSpaceIdx:], " \t")
				buf.Reset()
				buf.WriteString(remain)
				visible = visibleLen(remain)
			} else {
				// No space, break at width
				result = append(result, content)
				buf.Reset()
				visible = 0
			}
		}
		buf.WriteString(cluster)
		visible += clusterW
		i += size
	}
	if buf.Len() > 0 {
		result = append(result, buf.String())
	}
	return result
}

func findLastSpace(s string) int {
	lastIdx := -1
	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if r == ' ' || r == '\t' {
			lastIdx = i
		}
		i += sz
	}
	return lastIdx
}

func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		_, w, size := nextGrapheme(s[i:])
		n += w
		i += size
	}
	return n
}
