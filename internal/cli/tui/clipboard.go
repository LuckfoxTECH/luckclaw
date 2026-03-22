package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
)

// CopyToClipboard copies text to clipboard
func CopyToClipboard(text string) error {
	if text == "" {
		return fmt.Errorf("empty text")
	}

	// OSC 52 (Terminal clipboard protocol)
	osc52.New(text).WriteTo(os.Stderr)

	// Also try system clipboard (as backup)
	_ = copyToSystemClipboard(text)

	return nil
}

// copyToSystemClipboard copies to clipboard using system command
func copyToSystemClipboard(text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try xclip or xsel or wl-copy
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		}
	case "windows":
		cmd = exec.Command("clip")
	}

	if cmd == nil {
		return fmt.Errorf("no clipboard command available")
	}

	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
