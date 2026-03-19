package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"luckclaw/internal/skills"
	"luckclaw/internal/tools"
)

func containsSkill(list []string, v string) bool {
	for _, it := range list {
		if it == v {
			return true
		}
	}
	return false
}

func stripFrontmatter(md string) string {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return md
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return md
}

func (a *AgentLoop) runRemoteSkill(ctx context.Context, sessionKey string, channel string, chatID string, termName string, term terminalItem, skill skills.Skill, args []string) (string, terminalItem, error) {
	info, home, err := skills.EnsureRemoteSkillWorkspace(ctx, term.SSH, termName, sessionKey, term.RemoteHome)
	if err != nil {
		return "", term, err
	}
	term.RemoteHome = home

	localDir := filepath.Dir(skill.Path)
	if _, err := os.Stat(localDir); err != nil {
		return "", term, err
	}
	remoteSkillDir := info.RemoteSkillDir + "/" + skill.Name
	if _, err := tools.UploadPath(ctx, term.SSH, localDir, remoteSkillDir, true, 120*time.Second); err != nil {
		return "", term, err
	}
	if !containsSkill(term.SyncedSkills, skill.Name) {
		term.SyncedSkills = append(term.SyncedSkills, skill.Name)
	}

	b, err := os.ReadFile(skill.Path)
	if err != nil {
		return "", term, err
	}
	body := strings.TrimSpace(stripFrontmatter(string(b)))
	if body == "" {
		body = strings.TrimSpace(string(b))
	}

	userExtra := ""
	if len(args) > 1 {
		userExtra = strings.Join(args[1:], " ")
	}

	prompt := strings.TrimSpace(strings.Join([]string{
		"You are running a skill in a remote terminal-only sandbox.",
		"",
		"Constraints:",
		"- Do NOT read/write/edit/list local files. Do NOT use read_file/write_file/edit_file/list_dir/clawhub_install/send_file.",
		"- Use exec (remote) to operate inside the remote workspace only.",
		"",
		"Remote workspace:",
		"- workspace: " + info.RemoteWorkspace,
		"- skill dir: " + remoteSkillDir,
		"",
		"User input: " + strings.TrimSpace("/skill "+skill.Name+" "+userExtra),
		"",
		"Skill instructions:",
		"```markdown",
		body,
		"```",
		"",
		"Execution rules:",
		"- For every exec call, set working_dir to the remote workspace path above (or cd to it inside the command).",
		"- If artifacts need to be returned, instruct the user to use /terminal download from the remote workspace.",
	}, "\n"))

	allowed := []string{"exec", "web_fetch", "web_search", "browser", "tool_search"}
	subCtx := tools.WithSubAgentContext(ctx, tools.SubAgentMeta{Depth: 1, Allowed: allowed})
	subCtx = tools.WithTerminalContext(subCtx, &tools.TerminalContext{Name: termName, Type: "ssh", SSH: term.SSH})

	subSessionKey := fmt.Sprintf("skill:%s:%s:%d", sessionKey, skills.SafeName(skill.Name), time.Now().UnixNano())
	out, _, err := a.processDirect(subCtx, prompt, subSessionKey, channel, chatID, nil)
	if err != nil {
		return out, term, err
	}
	return out, term, nil
}

