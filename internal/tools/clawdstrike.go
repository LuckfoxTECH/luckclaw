package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClawdStrikeTool performs a lightweight security audit of the luckclaw deployment.
// Ref: https://www.cnblogs.com/informatics/p/19679935 (ClawdStrike)
type ClawdStrikeTool struct {
	ConfigPath string
	Workspace  string
	DataDir    string
}

func (t *ClawdStrikeTool) Name() string { return "security_audit" }
func (t *ClawdStrikeTool) Description() string {
	return "Run a security audit of the luckclaw deployment. Checks config file permissions, workspace skills, and common exposure risks. Use when user asks for security check or audit."
}
func (t *ClawdStrikeTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ClawdStrikeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	var report strings.Builder
	report.WriteString("# Security Audit Report\n\n")

	// 1. Config file permissions
	if t.ConfigPath != "" {
		info, err := os.Stat(t.ConfigPath)
		if err == nil {
			mode := info.Mode()
			if mode.Perm()&0044 != 0 {
				report.WriteString(fmt.Sprintf("## Config file: %s\n", t.ConfigPath))
				report.WriteString(fmt.Sprintf("- Permissions: %s\n", mode.String()))
				if mode.Perm()&0002 != 0 {
					report.WriteString("- **WARNING**: Config is world-writable. Consider chmod 600.\n")
				} else if mode.Perm()&0020 != 0 {
					report.WriteString("- **WARNING**: Config is group-writable. Consider chmod 600.\n")
				} else {
					report.WriteString("- OK: Permissions look reasonable.\n")
				}
				report.WriteString("\n")
			}
		}
	}

	// 2. Workspace skills scan
	if t.Workspace != "" {
		skillsDir := filepath.Join(t.Workspace, "skills")
		entries, err := os.ReadDir(skillsDir)
		if err == nil {
			report.WriteString("## Installed Skills\n")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				skillPath := filepath.Join(skillsDir, e.Name(), "SKILL.md")
				content, err := os.ReadFile(skillPath)
				if err != nil {
					continue
				}
				s := string(content)
				// Basic suspicious pattern check
				suspicious := []string{"eval(", "exec(", "child_process", "require('child_process')", "os.system", "subprocess."}
				for _, pat := range suspicious {
					if strings.Contains(s, pat) {
						report.WriteString(fmt.Sprintf("- **%s**: Contains potentially risky pattern %q\n", e.Name(), pat))
						break
					}
				}
			}
			report.WriteString("\n")
		}
	}

	// 3. Data directory
	if t.DataDir != "" {
		info, err := os.Stat(t.DataDir)
		if err == nil && info.IsDir() {
			report.WriteString("## Data Directory\n")
			report.WriteString(fmt.Sprintf("- Path: %s\n", t.DataDir))
			report.WriteString(fmt.Sprintf("- Permissions: %s\n", info.Mode().String()))
			report.WriteString("\n")
		}
	}

	report.WriteString("## Summary\n")
	report.WriteString("Audit complete. Review warnings above. For comprehensive checks, consider external tools.\n")
	return report.String(), nil
}
