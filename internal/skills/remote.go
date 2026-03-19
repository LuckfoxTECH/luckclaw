package skills

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"luckclaw/internal/tools"
)

var reSafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func SafeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	s = reSafeName.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "default"
	}
	return s
}

func ShortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

type RemoteWorkspaceInfo struct {
	RemoteWorkspace string
	RemoteSkillDir  string
}

func EnsureRemoteSkillWorkspace(ctx context.Context, conn tools.SSHConn, termName string, parentSessionKey string, remoteHome string) (RemoteWorkspaceInfo, string, error) {
	home := strings.TrimSpace(remoteHome)
	if home == "" {
		h, _ := tools.RunSSHCommand(ctx, conn, `printf "%s" "$HOME"`, 10*time.Second)
		home = strings.TrimSpace(h)
	}
	if home == "" {
		return RemoteWorkspaceInfo{}, "", fmt.Errorf("failed to resolve remote $HOME")
	}
	ws := home + "/.luckclaw_remote/ws/" + SafeName(termName) + "/" + ShortHash(parentSessionKey)
	skillRoot := ws + "/skills"
	cmd := "mkdir -p " + shQuoteRemote(ws) + " " + shQuoteRemote(skillRoot) + " " + shQuoteRemote(ws+"/runs")
	if _, err := tools.RunSSHCommand(ctx, conn, cmd, 20*time.Second); err != nil {
		return RemoteWorkspaceInfo{}, home, err
	}
	return RemoteWorkspaceInfo{RemoteWorkspace: ws, RemoteSkillDir: skillRoot}, home, nil
}

func shQuoteRemote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
