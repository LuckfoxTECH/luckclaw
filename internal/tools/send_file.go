package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"luckclaw/internal/bus"
)

// channelsSupportingMedia lists channels that can receive and display images/files.
// webui: images displayed inline; other files as download links.
// TUI, cli, session, slack, feishu, etc. do not support media in their current implementation.
// workweixin excluded: image send may hang; agent will inform user file is saved instead of trying send_file
var channelsSupportingMedia = map[string]bool{
	"telegram": true, "discord": true, "webui": true,
}

// ChannelSupportsMedia returns whether the channel can receive and display images/files.
func ChannelSupportsMedia(channel string) bool {
	return channelsSupportingMedia[channel]
}

// SendFileTool sends a file to the current channel. Path must be within AllowedDir.
type SendFileTool struct {
	Bus            *bus.MessageBus
	AllowedDir     string
	DefaultChannel string
	DefaultChatID  string
}

func (t *SendFileTool) Name() string { return "send_file" }

func (t *SendFileTool) Description() string {
	return "Send a file to the current chat. Path must be within workspace. Use for images, documents, or any file to share with the user."
}

func (t *SendFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path relative to workspace or absolute path within workspace",
			},
			"caption": map[string]any{
				"type":        "string",
				"description": "Optional caption for the file",
			},
		},
		"required": []any{"path"},
	}
}

func (t *SendFileTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Bus == nil {
		return "", fmt.Errorf("no message bus configured")
	}
	pathArg, _ := args["path"].(string)
	pathArg = strings.TrimSpace(pathArg)
	if pathArg == "" {
		return "", fmt.Errorf("path is required")
	}
	caption, _ := args["caption"].(string)

	// Resolve path and ensure it's within AllowedDir (workspace)
	if t.AllowedDir == "" {
		return "", fmt.Errorf("send_file requires workspace to be configured")
	}
	abs, err := resolvePath(pathArg, t.AllowedDir, t.AllowedDir)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", pathArg)
		}
		return "", err
	}

	channel := t.DefaultChannel
	chatID := t.DefaultChatID
	if channel == "" || chatID == "" {
		channel, chatID = ChannelFromContext(ctx)
	}
	if channel == "" || chatID == "" {
		return "", fmt.Errorf("no channel/chat context (send_file requires an active chat)")
	}

	content := caption
	if content == "" {
		content = "File: " + filepath.Base(abs)
	}

	// If channel doesn't support media (e.g. TUI, webui, cli), save locally and return path instead of sending.
	if !channelsSupportingMedia[channel] {
		return fmt.Sprintf("File saved locally (channel %q does not support images): %s", channel, abs), nil
	}

	err = t.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: content,
		Media:   []string{abs},
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("File sent: %s", filepath.Base(abs)), nil
}
