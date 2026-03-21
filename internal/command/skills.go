package command

import (
	"fmt"
	"strings"

	"luckclaw/internal/paths"
	"luckclaw/internal/skills"
)

// SkillsHandler is the skills command handler
type SkillsHandler struct{}

// Execute executes the skills command
func (h *SkillsHandler) Execute(input Input) (Output, error) {
	if len(input.Args) == 0 {
		return h.listSkills(input)
	}

	// With arguments: run specified skill
	return h.runSkill(input)
}

func (h *SkillsHandler) listSkills(input Input) (Output, error) {
	ws, err := paths.ExpandUser(input.Config.Agents.Defaults.Workspace)
	if err != nil {
		return Output{Error: err}, nil
	}

	skillList, err := skills.Discover(ws)
	if err != nil {
		return Output{Error: err}, nil
	}

	if len(skillList) == 0 {
		return Output{
			Content:    "No skills found. Create workspace/skills/<name>/SKILL.md or install from ClawHub.",
			IsMarkdown: true,
			IsFinal:    true,
		}, nil
	}

	var b strings.Builder
	b.WriteString("**Available skills:**\n\n")
	for _, s := range skillList {
		state := "available"
		if !s.Available {
			state = "unavailable"
		}
		b.WriteString(fmt.Sprintf("  - **%s** (%s)\n", s.Name, state))
	}

	return Output{
		Content:    b.String(),
		IsMarkdown: true,
		IsFinal:    true,
	}, nil
}

func (h *SkillsHandler) runSkill(input Input) (Output, error) {
	ws, err := paths.ExpandUser(input.Config.Agents.Defaults.Workspace)
	if err != nil {
		return Output{Error: err}, nil
	}

	skillList, err := skills.Discover(ws)
	if err != nil {
		return Output{Error: err}, nil
	}

	name := strings.TrimSpace(strings.ToLower(input.Args[0]))
	for _, s := range skillList {
		if strings.ToLower(s.Name) == name {
			if !s.Available {
				return Output{
					Content: fmt.Sprintf("Skill %q is unavailable (missing deps).", s.Name),
				}, nil
			}

			// Return execution prompt, let agent execute the skill
			execPrompt := fmt.Sprintf("Please run the %s skill. User requested: /skill %s", s.Name, s.Name)
			if len(input.Args) > 1 {
				execPrompt += "\n\n[User additional input: " + strings.Join(input.Args[1:], " ") + "]"
			}

			return Output{
				ExecPrompt: execPrompt,
				IsFinal:    false,
			}, nil
		}
	}

	return Output{
		Content: fmt.Sprintf("Skill %q not found. Use `skill` to list available skills.", name),
	}, nil
}
