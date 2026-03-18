package tui

import (
	"fmt"
	"unicode/utf8"

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
