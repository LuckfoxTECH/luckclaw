package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/spf13/cobra"
)

func newOnboardCmd() *cobra.Command {
	var force, skillsOnly bool

	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Initialize luckclaw configuration and workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			ws, err := paths.WorkspaceDir()
			if err != nil {
				return err
			}

			if skillsOnly {
				// Add missing default skills without touching config
				cfg, err := config.Load(cfgPath)
				if err == nil && strings.TrimSpace(cfg.Agents.Defaults.Workspace) != "" {
					expanded, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
					if err == nil && expanded != "" {
						ws = expanded
					}
				}
				if err := os.MkdirAll(ws, 0o755); err != nil {
					return err
				}
				if err := createSkillsTemplates(ws, nil); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Updated default skills in %s\n", ws)
				return nil
			}

			if _, err := os.Stat(cfgPath); err == nil && !force {
				return exitf(cmd, "Config already exists at %s (use --force to overwrite)", cfgPath)
			}

			if err := config.WriteDefaultFullTemplate(cfgPath); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Created config at %s\n", cfgPath)

			if err := os.MkdirAll(ws, 0o755); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Created workspace at %s\n", ws)

			if err := createWorkspaceTemplates(ws); err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nluckclaw is ready!")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nNext steps:")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  1. Add your API key to ~/.luckclaw/config.json")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "     Get one at: https://openrouter.ai/keys")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  2. Chat: luckclaw agent -m \"Hello!\"")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config")
	cmd.Flags().BoolVar(&skillsOnly, "skills", false, "Only add missing default skills to workspace (no config changes)")

	return cmd
}

func createWorkspaceTemplates(workspace string) error {
	templates := map[string]string{
		"IDENTITY.md":  identityTemplate,
		"AGENTS.md":    agentsTemplate,
		"SOUL.md":      soulTemplate,
		"USER.md":      userTemplate,
		"HEARTBEAT.md": heartbeatTemplate,
	}

	for name, content := range templates {
		path := filepath.Join(workspace, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}

	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return err
	}

	memFile := filepath.Join(memoryDir, "MEMORY.md")
	if _, err := os.Stat(memFile); err == nil {
		return createSkillsTemplates(workspace, createCronTemplate())
	}
	if err := os.WriteFile(memFile, []byte(memoryTemplate), 0o644); err != nil {
		return err
	}
	return createSkillsTemplates(workspace, createCronTemplate())
}

func createCronTemplate() error {
	cronPath, err := paths.CronJobsPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cronPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cronPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cronPath, []byte("{\n  \"version\": 1,\n  \"jobs\": []\n}\n"), 0o600)
}

func createSkillsTemplates(workspace string, prevErr error) error {
	if prevErr != nil {
		return prevErr
	}
	skills := []struct {
		dir     string
		content string
	}{
		{"clawhub", clawhubSkillTemplate},
	}
	for _, s := range skills {
		dir := filepath.Join(workspace, "skills", s.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "SKILL.md")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(s.content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const identityTemplate = `# Identity

## Name
luckclaw 🍀

## Description
Personal AI assistant written in Go, inspired by nanobot (legacy). Integrates with luckfox-webui and supports multi-channel messaging.

## Purpose
- Provide intelligent AI assistance
- Support multiple LLM providers (OpenRouter, Anthropic, Zhipu, etc.)
- Enable customization through skills system
- Run on various hardware (desktop, embedded)

## Capabilities
- Web search and content fetching
- File system operations (read, write, edit)
- Shell command execution
- Multi-channel messaging (Telegram, Discord, Feishu, etc.)
- Skill-based extensibility
- Memory and context management

## Philosophy
- Accuracy over speed
- User control and privacy
- Transparent operation
- Ask for clarification when ambiguous
`

const agentsTemplate = `# Agent Instructions

You are a helpful AI assistant. Be concise, accurate, and friendly.

## Guidelines

- Always explain what you're doing before taking actions
- Ask for clarification when the request is ambiguous
- Use tools to help accomplish tasks
- Remember important information in your memory files

## Tools Available

You have access to:
- File operations (read, write, edit, list)
- Shell commands (exec)
- Web access (search, fetch)
- Messaging (message)
- Background tasks (spawn)

## Memory

- Use memory/ directory for daily notes
- Use MEMORY.md for long-term information
`

const soulTemplate = `# Soul

I am luckclaw 🍀, a personal AI assistant.

## Personality

- Helpful and friendly
- Concise and to the point
- Curious and eager to learn

## Values

- Accuracy over speed
- User privacy and safety
- Transparency in actions

## Communication Style

- Be clear and direct
- Explain reasoning when helpful
- Ask clarifying questions when needed
`

const userTemplate = `# User

Information about the user goes here.

## Preferences
- Communication style: (casual/formal)
- Timezone: (e.g. Asia/Shanghai)
- Language: (e.g. zh-CN, en)

## Personal Information
- Name: (optional)
- Location: (optional)
- Occupation: (optional)

## Learning Goals
- What the user wants to learn from AI
- Preferred interaction style
- Areas of interest
`

const memoryTemplate = `# Long-term Memory

This file stores important information that should persist across sessions.

## User Information

(Important facts about the user)

## Preferences

(User preferences learned over time)

## Important Notes

(Things to remember)
`

const heartbeatTemplate = `# HEARTBEAT

Write periodic reminders or checklists here. When luckclaw gateway runs, it will periodically read this file and respond as a proactive check-in.

Example:

- Summarize today's progress
- Suggest the next 3 tasks
- Remind me of any deadlines
`

const clawhubSkillTemplate = `---
name: clawhub
description: Search and install agent skills from ClawHub. No Node.js required.
metadata: {"luckclaw":{"emoji":"🍀"}}
---

# ClawHub

Public skill registry for AI agents. Use built-in tools (no Node.js).

## When to use

- "find a skill for …"
- "search for skills"
- "install a skill"
- "what skills are available?"

## When NOT to use

- Fetching web pages, scraping content, extracting forum posts → use web_fetch instead.

## Tools

- **clawhub_search**: Search skills by query. Returns slug, displayName, version, score.
- **clawhub_install**: Install a skill by slug to workspace/skills/.

## Workflow

1. Use clawhub_search with user's keyword.
2. Pick a slug from results.
3. Use clawhub_install with that slug.
4. Remind user to start a new session to load the skill.

## CLI (optional)

User can also run: luckclaw clawhub search "query", luckclaw clawhub install <slug>.
`
