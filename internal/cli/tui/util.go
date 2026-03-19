package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
)

func exitf(cmd *cobra.Command, format string, args ...any) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
	return fmt.Errorf(format, args...)
}

func runePosToByteOffset(s string, runePos int) int {
	if runePos <= 0 {
		return 0
	}
	if runePos >= utf8.RuneCountInString(s) {
		return len(s)
	}
	count := 0
	for i := range s {
		if count == runePos {
			return i
		}
		count++
	}
	return len(s)
}

func renderSingleLineInput(input string, cursorPos int, maxWidth int) string {
	const cursorGlyph = "▌"
	if maxWidth <= 0 {
		return ""
	}
	cursorW := runewidth.StringWidth(cursorGlyph)
	if maxWidth <= cursorW {
		return cursorGlyph
	}

	input = strings.ReplaceAll(input, "\n", " ")
	runes := []rune(input)
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(runes) {
		cursorPos = len(runes)
	}

	prefix := make([]int, len(runes)+1)
	for i, r := range runes {
		prefix[i+1] = prefix[i] + runewidth.RuneWidth(r)
	}

	start, end := 0, len(runes)
	for start < end && (prefix[end]-prefix[start]+cursorW) > maxWidth {
		leftRoom := cursorPos - start
		rightRoom := end - cursorPos
		if leftRoom > rightRoom {
			if start < cursorPos {
				start++
			} else {
				end--
			}
		} else {
			if end > cursorPos {
				end--
			} else {
				start++
			}
		}
	}

	if cursorPos < start {
		cursorPos = start
	}
	if cursorPos > end {
		cursorPos = end
	}
	localCursor := cursorPos - start
	visible := runes[start:end]
	return string(visible[:localCursor]) + cursorGlyph + string(visible[localCursor:])
}
