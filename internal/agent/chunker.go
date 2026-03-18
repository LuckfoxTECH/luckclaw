package agent

import (
	"regexp"
	"strings"
)

type BlockChunker struct {
	MinChars        int
	MaxChars        int
	BreakPreference string
}

const (
	BreakParagraph  = "paragraph"
	BreakNewline    = "newline"
	BreakSentence   = "sentence"
	BreakWhitespace = "whitespace"
)

var (
	reParagraph = regexp.MustCompile(`\n\s*\n`)
	reNewline   = regexp.MustCompile(`\n`)
	reSentence  = regexp.MustCompile(`[.!?。！？]\s+`)
)

func (c *BlockChunker) Emit(buf string, onChunk func(chunk string)) string {
	if c.MinChars <= 0 {
		c.MinChars = 80
	}
	if c.MaxChars <= 0 {
		c.MaxChars = 600
	}
	if c.BreakPreference == "" {
		c.BreakPreference = BreakNewline
	}
	if len(buf) < c.MinChars {
		return buf
	}
	chunk, rest := c.splitAtBoundary(buf)
	if chunk != "" {
		onChunk(chunk)
	}
	return rest
}

func (c *BlockChunker) Flush(buf string, onChunk func(chunk string)) {
	for buf != "" {
		if len(buf) <= c.MaxChars {
			onChunk(buf)
			return
		}
		chunk, rest := c.splitAtBoundary(buf)
		if chunk != "" {
			onChunk(chunk)
			buf = rest
		} else {
			onChunk(buf[:c.MaxChars])
			buf = buf[c.MaxChars:]
		}
	}
}

func (c *BlockChunker) splitAtBoundary(s string) (chunk, rest string) {
	if len(s) <= c.MaxChars {
		return s, ""
	}
	search := s[:c.MaxChars+1]
	var idx int
	switch c.BreakPreference {
	case BreakParagraph:
		idx = c.lastIndex(reParagraph, search)
	case BreakNewline:
		idx = c.lastIndex(reNewline, search)
	case BreakSentence:
		idx = c.lastIndex(reSentence, search)
	case BreakWhitespace:
		idx = strings.LastIndexAny(search, " \t\n")
	default:
		idx = strings.LastIndexAny(search, " \t\n")
	}
	if idx > c.MinChars {
		return strings.TrimSpace(s[:idx]), strings.TrimLeft(s[idx:], " \t\n")
	}
	return s[:c.MaxChars], s[c.MaxChars:]
}

func (c *BlockChunker) lastIndex(re *regexp.Regexp, s string) int {
	matches := re.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return -1
	}
	last := matches[len(matches)-1]
	return last[1]
}
