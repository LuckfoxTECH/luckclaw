package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Skill struct {
	Name        string
	Description string
	Path        string
	Available   bool
	Requires    Requires
	Always      bool
	IsRemote    bool // Mark as remote skill
}

type Requires struct {
	Bins []string
	Env  []string
}

func Discover(workspace string) ([]Skill, error) {
	root := filepath.Join(workspace, "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Skill{}, nil
		}
		return nil, err
	}

	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		skillPath := filepath.Join(root, name, "SKILL.md")
		b, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		meta := parseFrontmatter(string(b))
		desc := strings.TrimSpace(meta["description"])
		req, always := parseRequires(meta["metadata"], meta["always"])
		missingBins, missingEnv := MissingRequires(req)
		available := len(missingBins) == 0 && len(missingEnv) == 0
		out = append(out, Skill{
			Name:        name,
			Description: desc,
			Path:        skillPath,
			Available:   available,
			Requires:    req,
			Always:      always,
		})
	}
	return out, nil
}

// BuildOptions controls what to include in the system prompt.
// Used by Token Budget Scheduler: compact mode omits heavy sections to save tokens.
type BuildOptions struct {
	// Compact: when true, omit always-skills body, USER.md, SOUL.md to reduce tokens.
	Compact bool
}

func BuildSystemPrompt(workspace string) (string, error) {
	return BuildSystemPromptWithOptions(workspace, BuildOptions{})
}

func BuildSystemPromptWithOptions(workspace string, opts BuildOptions) (string, error) {
	var b strings.Builder

	// Core identity: IDENTITY.md when present, else built-in
	identityContent, err := readIfExists(filepath.Join(workspace, "IDENTITY.md"))
	if err != nil {
		return "", err
	}
	identityContent = strings.TrimSpace(identityContent)
	if identityContent != "" {
		b.WriteString(identityContent)
		b.WriteString("\n\n")
		b.WriteString(buildWorkspaceSection(workspace))
	} else {
		b.WriteString(buildIdentity(workspace))
	}
	b.WriteString("\n\n---\n\n")

	// Bootstrap files: AGENTS, TOOLS always; SOUL, USER omitted in compact mode
	bootstrapFiles := []string{"AGENTS.md", "TOOLS.md"}
	if !opts.Compact {
		bootstrapFiles = []string{"AGENTS.md", "SOUL.md", "USER.md", "TOOLS.md"}
	}
	for _, name := range bootstrapFiles {
		content, err := readIfExists(filepath.Join(workspace, name))
		if err != nil {
			return "", err
		}
		content = strings.TrimSpace(content)
		if content != "" {
			b.WriteString(fmt.Sprintf("## %s\n\n%s\n\n", name, content))
		}
	}

	skills, err := Discover(workspace)
	if err != nil {
		return "", err
	}
	if len(skills) == 0 {
		return strings.TrimSpace(b.String()), nil
	}

	b.WriteString("<skills>\n")
	for _, s := range skills {
		b.WriteString("<skill>\n")
		b.WriteString("<name>" + xmlEscape(s.Name) + "</name>\n")
		if s.Description != "" {
			b.WriteString("<description>" + xmlEscape(s.Description) + "</description>\n")
		}
		b.WriteString("<location>" + xmlEscape(s.Path) + "</location>\n")
		if s.Available {
			b.WriteString("<available>true</available>\n")
		} else {
			b.WriteString("<available>false</available>\n")
			if len(s.Requires.Bins) > 0 || len(s.Requires.Env) > 0 {
				b.WriteString("<requires>\n")
				for _, bin := range s.Requires.Bins {
					b.WriteString("<bin>" + xmlEscape(bin) + "</bin>\n")
				}
				for _, env := range s.Requires.Env {
					b.WriteString("<env>" + xmlEscape(env) + "</env>\n")
				}
				b.WriteString("</requires>\n")
			}
		}
		b.WriteString("</skill>\n")
	}
	b.WriteString("</skills>\n\n")

	b.WriteString("If you want to use a skill, read its SKILL.md using the read_file tool, then follow it.\n")
	b.WriteString("Skills with available=\"false\" need dependencies installed first - you can try installing them with apt/brew.\n")

	// In compact mode, skip always-skills body (saves ~300-800 tokens)
	if !opts.Compact {
		for _, s := range skills {
			if !s.Always || !s.Available {
				continue
			}
			body, err := readIfExists(s.Path)
			if err != nil {
				return "", err
			}
			body = stripFrontmatter(body)
			body = strings.TrimSpace(body)
			if body == "" {
				continue
			}
			b.WriteString("\n<always-skill name=\"" + xmlEscape(s.Name) + "\">\n")
			b.WriteString(body)
			b.WriteString("\n</always-skill>\n")
		}
	}

	return strings.TrimSpace(b.String()), nil
}

func parseFrontmatter(md string) map[string]string {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]string{}
	}
	out := map[string]string{}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			break
		}
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		out[k] = v
	}
	return out
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

func parseRequires(metadata string, alwaysFlag string) (Requires, bool) {
	var req Requires
	always := strings.EqualFold(strings.TrimSpace(alwaysFlag), "true")
	if strings.TrimSpace(metadata) == "" {
		return req, always
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		return req, always
	}
	n, _ := parsed["luckclaw"].(map[string]any)
	if n == nil {
		return req, always
	}
	if v, ok := n["always"].(bool); ok {
		always = v
	}
	r, _ := n["requires"].(map[string]any)
	if r == nil {
		return req, always
	}
	if bins, ok := r["bins"].([]any); ok {
		for _, it := range bins {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				req.Bins = append(req.Bins, strings.TrimSpace(s))
			}
		}
	}
	if env, ok := r["env"].([]any); ok {
		for _, it := range env {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				req.Env = append(req.Env, strings.TrimSpace(s))
			}
		}
	}
	return req, always
}

func MissingRequires(req Requires) (missingBins []string, missingEnv []string) {
	for _, bin := range req.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			missingBins = append(missingBins, bin)
		}
	}
	for _, env := range req.Env {
		if _, ok := os.LookupEnv(env); !ok {
			missingEnv = append(missingEnv, env)
		}
	}
	return missingBins, missingEnv
}

// ReadIfExists reads a file if it exists; returns empty string on not-found.
func ReadIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func readIfExists(path string) (string, error) {
	return ReadIfExists(path)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func buildWorkspaceSection(workspace string) string {
	goos := runtime.GOOS
	var platformPolicy string
	if goos == "windows" {
		platformPolicy = `## Platform Policy (Windows)
- You are running on Windows. Do not assume GNU tools like grep, sed, or awk exist.
- Prefer Windows-native commands or file tools when they are more reliable.
- If terminal output is garbled, retry with UTF-8 output enabled.`
	} else {
		platformPolicy = `## Platform Policy (POSIX)
- You are running on a POSIX system. Prefer UTF-8 and standard shell tools.
- Use file tools when they are simpler or more reliable than shell commands.`
	}
	abs, _ := filepath.Abs(workspace)
	return fmt.Sprintf(`## Runtime
%s %s, Go %s

## Workspace
Your workspace is at: %s
- Long-term memory: %s/memory/MEMORY.md (write important facts here)
- History log: %s/memory/HISTORY.md (grep-searchable). Each entry starts with [YYYY-MM-DD HH:MM].
- Custom skills: %s/skills/{skill-name}/SKILL.md

%s

## Guidelines
- State intent before tool calls, but NEVER predict or claim results before receiving them.
- Before modifying a file, read it first. Do not assume files or directories exist.
- After writing or editing a file, re-read it if accuracy matters.
- If a tool call fails, analyze the error before retrying with a different approach.
- Ask for clarification when the request is ambiguous.

Reply directly with text for conversations. Only use the 'message' tool to send to a specific chat channel.

## Image Recognition
When asked to recognize, describe, or analyze images:
- If your model supports vision (check model name: gpt-4o, claude-3-5, gemini-1.5, qwen-vl, kimi-k2, etc.):
  1. Use the read_file tool to read the image file (returns base64 data URI)
  2. Analyze the base64 image content directly
  3. Provide your analysis/description
- If your model does NOT support vision:
  - Suggest using a vision-capable model, or
  - Suggest external OCR tools (tesseract, pytesseract)

The read_file tool supports: .jpg, .jpeg, .png, .gif, .webp, .bmp (max 10MB).`,
		runtime.GOOS, runtime.GOARCH, runtime.Version(), abs, abs, abs, abs, platformPolicy)
}

func buildIdentity(workspace string) string {
	return fmt.Sprintf(`# luckclaw 🍀

You are luckclaw, a helpful AI assistant.

%s`, buildWorkspaceSection(workspace))
}
