package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"luckclaw/internal/clawhub"
)

func isClawHubHTTPError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "download: ") || strings.Contains(s, "get skill: ") || strings.Contains(s, "search: ") || strings.Contains(s, "well-known: ") {
		return strings.Contains(s, " 4") || strings.Contains(s, " 5") || strings.Contains(s, "429") || strings.Contains(strings.ToLower(s), "too many requests")
	}
	return false
}

func clawHubInstallFallbackText(workspace, registry, slug string) string {
	slug = strings.TrimSpace(slug)
	registry = strings.TrimSuffix(strings.TrimSpace(registry), "/")
	var b strings.Builder
	b.WriteString("Next steps (ClawHub download is currently limited):\n")
	b.WriteString("1) Install essential local skills:\n")
	b.WriteString("   - luckclaw onboard --skills\n")
	b.WriteString("   - luckclaw skills list\n")
	b.WriteString("2) Retry later (rate limit):\n")
	if slug != "" {
		b.WriteString(fmt.Sprintf("   - luckclaw clawhub install %s\n", slug))
	} else {
		b.WriteString("   - luckclaw clawhub install <slug>\n")
	}
	b.WriteString("3) If you need real-time info now, enable tools.web.search and tools.web.fetch, then use web_search/web_fetch.\n")
	b.WriteString("4) For stable extensibility, configure tools.mcpServers and use mcp_* tools.\n")
	if registry != "" {
		b.WriteString("\nDownload endpoint:\n")
		if slug != "" {
			b.WriteString(fmt.Sprintf("  %s/api/v1/download?slug=%s\n", registry, slug))
		} else {
			b.WriteString(fmt.Sprintf("  %s/api/v1/download?slug=<slug>\n", registry))
		}
	}
	b.WriteString("\nRecommended skills:\n")
	b.WriteString("  - weather (real-time info via web_search/web_fetch)\n")
	b.WriteString("  - clawhub (search/install workflow helper)\n")
	if strings.TrimSpace(workspace) != "" {
		b.WriteString(fmt.Sprintf("\nWorkspace: %s\n", workspace))
	}
	return b.String()
}

// ClawHubSearchTool searches skills on ClawHub. No Node.js required.
type ClawHubSearchTool struct {
	Registry string
}

func (t *ClawHubSearchTool) Name() string { return "clawhub_search" }
func (t *ClawHubSearchTool) Description() string {
	return "Search agent skills on ClawHub (public skill registry). Only use when user explicitly asks to find/search skills. For web fetching or content extraction, use web_fetch instead."
}
func (t *ClawHubSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (e.g. 'web scraping', 'weather')",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max results (default 10)",
			},
		},
		"required": []any{"query"},
	}
}

func (t *ClawHubSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := 10
	if v, ok := args["limit"]; ok {
		switch n := v.(type) {
		case int:
			limit = n
		case float64:
			limit = int(n)
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				limit = i
			}
		}
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	reg := t.Registry
	if reg == "" {
		reg = clawhub.RegistryURL()
	}
	client := clawhub.NewClient(reg)
	resp, err := client.Search(query, limit)
	if err != nil {
		return "", err
	}
	if len(resp.Results) == 0 {
		return "No skills found.", nil
	}
	out, _ := json.MarshalIndent(resp.Results, "", "  ")
	return string(out), nil
}

// ClawHubInstallTool installs a skill from ClawHub. No Node.js required.
type ClawHubInstallTool struct {
	Workspace           string
	Registry            string
	ResourceConstrained bool // if true, only provide suggestions, don't download
}

func (t *ClawHubInstallTool) Name() string { return "clawhub_install" }
func (t *ClawHubInstallTool) Description() string {
	return "Install a skill from ClawHub to workspace/skills/. Only use when user explicitly asks to install a skill. For web fetching or content extraction, use web_fetch instead."
}
func (t *ClawHubInstallTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{
				"type":        "string",
				"description": "Skill slug from search results",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "Specific version (optional, default: latest)",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "Overwrite if already installed",
			},
		},
		"required": []any{"slug"},
	}
}

func (t *ClawHubInstallTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	slug, _ := args["slug"].(string)
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", fmt.Errorf("slug is required")
	}
	version, _ := args["version"].(string)
	force, _ := args["force"].(bool)

	ws := t.Workspace
	if ws == "" {
		return "", fmt.Errorf("workspace not configured")
	}

	reg := t.Registry
	if reg == "" {
		reg = clawhub.RegistryURL()
	}
	client := clawhub.NewClient(reg)
	if err := client.Install(ws, slug, version, force, t.ResourceConstrained); err != nil {
		// In resource-constrained mode, return the suggestion message
		if t.ResourceConstrained && strings.Contains(err.Error(), "resource-constrained mode") {
			return err.Error(), err
		}
		if isClawHubHTTPError(err) {
			return clawHubInstallFallbackText(ws, reg, slug), err
		}
		return "", err
	}
	return fmt.Sprintf("Installed %s -> %s/skills/%s. Start a new session to load the skill.", slug, ws, slug), nil
}
