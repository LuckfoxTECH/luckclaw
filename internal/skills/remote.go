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

// DiscoverRemoteSkills discovers skills installed on remote hosts via SSH connection
func DiscoverRemoteSkills(ctx context.Context, conn tools.SSHConn, remoteHome string, termName string, parentSessionKey string) ([]Skill, error) {
	// Get remote home directory
	home := strings.TrimSpace(remoteHome)
	if home == "" {
		h, _ := tools.RunSSHCommand(ctx, conn, `printf "%s" "$HOME"`, 10*time.Second)
		home = strings.TrimSpace(h)
	}
	if home == "" {
		return nil, fmt.Errorf("failed to resolve remote $HOME")
	}

	// Deduplication map
	skillMap := make(map[string]Skill)

	// 1. First check local workspace directory on remote host: ~/.luckclaw/workspace/skills/
	localWorkspaceSkillsDir := home + "/.luckclaw/workspace/skills"
	discoverSkillsFromDir(ctx, conn, localWorkspaceSkillsDir, false, skillMap)

	// 2. Then check remote workspace directory: ~/.luckclaw_remote/ws/{termName}/{sessionHash}/skills/
	info, _, err := EnsureRemoteSkillWorkspace(ctx, conn, termName, parentSessionKey, home)
	if err == nil {
		discoverSkillsFromDir(ctx, conn, info.RemoteSkillDir, true, skillMap)
	}

	// Convert to slice
	var skills []Skill
	for _, s := range skillMap {
		skills = append(skills, s)
	}

	return skills, nil
}

// discoverSkillsFromDir discovers skills from a given directory
func discoverSkillsFromDir(ctx context.Context, conn tools.SSHConn, skillDir string, isRemoteWorkspace bool, skillMap map[string]Skill) {
	// Use SSH command to list skills directory
	cmd := "ls -1 " + shQuoteRemote(skillDir)
	output, err := tools.RunSSHCommand(ctx, conn, cmd, 10*time.Second)
	if err != nil {
		// Skip if directory does not exist or is empty
		return
	}

	// Parse output to get skill name list
	skillNames := strings.Split(strings.TrimSpace(output), "\n")

	for _, name := range skillNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// Skip if skill with same name already exists (local workspace takes priority)
		key := strings.ToLower(name)
		if _, exists := skillMap[key]; exists {
			continue
		}

		// Read remote SKILL.md file
		skillPath := skillDir + "/" + name + "/SKILL.md"
		readCmd := "cat " + shQuoteRemote(skillPath)
		content, err := tools.RunSSHCommand(ctx, conn, readCmd, 10*time.Second)
		if err != nil {
			continue // Skip skills that cannot be read
		}

		// Parse frontmatter and metadata
		meta := parseFrontmatter(content)
		desc := strings.TrimSpace(meta["description"])
		req, always := parseRequires(meta["metadata"], meta["always"])
		missingBins, missingEnv := MissingRequires(req)
		available := len(missingBins) == 0 && len(missingEnv) == 0

		skillMap[key] = Skill{
			Name:        name,
			Description: desc,
			Path:        skillPath,
			Available:   available,
			Requires:    req,
			Always:      always,
			IsRemote:    true, // Mark as remote skill
		}
	}
}
