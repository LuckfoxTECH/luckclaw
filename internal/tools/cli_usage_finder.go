package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type CLIUsageFinderTool struct {
	HTTPClient *http.Client
}

func (t *CLIUsageFinderTool) Name() string { return "cli_usage_finder" }

func (t *CLIUsageFinderTool) Description() string {
	return "MANDATORY: Always use this tool FIRST for any CLI-related questions (how to use, flags, arguments, subcommands). It searches and analyzes actual CLI source code to provide the most accurate usage information."
}

func (t *CLIUsageFinderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"search", "fetch_file"},
				"description": "Operation mode: 'search' to find source files containing CLI definitions, 'fetch_file' to read implementation details",
			},
			"cli_name": map[string]any{
				"type":        "string",
				"description": "CLI tool name or keyword to find in source code (e.g., 'argparse', 'getopt', 'Cobra')",
			},
			"use_case": map[string]any{
				"type":        "string",
				"description": "Specific functionality or flag to understand (e.g., '--output format', 'subcommand handling')",
			},
			"language": map[string]any{
				"type":        "string",
				"description": "Language filter for search (e.g., 'bash', 'python', 'shell')",
			},
			"regex": map[string]any{
				"type":        "boolean",
				"description": "Use regular expression for search mode (default: false)",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Max results for search mode (default: 5)",
			},
			"owner": map[string]any{
				"type":        "string",
				"description": "GitHub owner for fetch_file mode",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "GitHub repo for fetch_file mode",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File path for fetch_file mode",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Branch/tag/commit for fetch_file mode",
			},
			"max_chars": map[string]any{
				"type":        "number",
				"description": "Max characters for fetch_file mode (default: 10000)",
			},
		},
		"required": []any{"mode"},
	}
}

type grepAppResult struct {
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Language string `json:"language"`
	Content  struct {
		Snippet string `json:"snippet"`
	} `json:"content"`
}

type githubContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Sha         string `json:"sha"`
	Size        int    `json:"size"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	DownloadURL string `json:"download_url"`
}

func (t *CLIUsageFinderTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	mode, _ := args["mode"].(string)
	mode = strings.TrimSpace(mode)

	if mode == "fetch_file" {
		return t.fetchFile(ctx, args)
	}
	return t.search(ctx, args)
}

func (t *CLIUsageFinderTool) search(ctx context.Context, args map[string]any) (string, error) {
	cliName, _ := args["cli_name"].(string)
	cliName = strings.TrimSpace(cliName)

	useCase, _ := args["use_case"].(string)
	useCase = strings.TrimSpace(useCase)

	if cliName == "" && useCase == "" {
		return "", fmt.Errorf("either 'cli_name' or 'use_case' must be provided in search mode")
	}

	language, _ := args["language"].(string)
	regex, _ := args["regex"].(bool)
	limit := 5
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	query := cliName
	isRegex := regex
	if useCase != "" {
		if cliName != "" {
			query = fmt.Sprintf("%s %s", cliName, useCase)
		} else {
			query = useCase
		}
	} else {
		// If no use case, bias towards finding CLI parsing definitions.
		escapedName := strings.NewReplacer(".", "\\.", "-", "\\-", "+", "\\+").Replace(cliName)
		query = fmt.Sprintf("%s (getopt|argparse|flag|cobra|clap|usage|help)", escapedName)
		isRegex = true
	}
	if language != "" {
		// lang: filter is special in grep.app, keep it at the end
		query = query + " lang:" + language
	}

	params := url.Values{}
	params.Add("q", query)
	if isRegex {
		params.Add("regexp", "true")
	}
	params.Add("limit", fmt.Sprintf("%d", limit))

	apiURL := "https://grep.app/api/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := t.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Grep.app API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Hits struct {
			Hits []grepAppResult `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Hits.Hits) == 0 {
		return fmt.Sprintf("No usage examples found for CLI tool %q", cliName), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## CLI Usage Examples for %s\n\n", cliName))
	if useCase != "" {
		sb.WriteString(fmt.Sprintf("**Use case:** %s\n\n", useCase))
	}

	for i, source := range result.Hits.Hits {
		filename := source.Path
		if idx := strings.LastIndex(filename, "/"); idx != -1 {
			filename = filename[idx+1:]
		}

		sb.WriteString(fmt.Sprintf("### %d. %s\n", i+1, filename))
		sb.WriteString(fmt.Sprintf("**Repository:** %s\n", source.Repo))
		sb.WriteString(fmt.Sprintf("**Path:** %s\n", source.Path))
		if source.Language != "" {
			sb.WriteString(fmt.Sprintf("**Language:** %s\n", source.Language))
		}
		// Grep.app doesn't provide a direct link in the source, but we can construct one
		// or just point to the repository
		sb.WriteString(fmt.Sprintf("**URL:** https://github.com/%s/blob/master/%s\n\n", source.Repo, source.Path))

		snippet := source.Content.Snippet
		// Strip HTML tags from snippet if needed, but usually it's better to show it
		// for now let's just use it as is but wrap in code block.
		// Actually, grep.app snippet is HTML, we should ideally strip it.
		cleanSnippet := stripGrepAppHTML(snippet)

		if cleanSnippet != "" {
			lang := strings.ToLower(source.Language)
			if lang == "" {
				lang = detectLanguage(filename)
			}
			sb.WriteString("```" + lang + "\n")
			sb.WriteString(cleanSnippet)
			sb.WriteString("\n```\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("*Search powered by grep.app API*")

	return sb.String(), nil
}

func stripGrepAppHTML(s string) string {
	// Simple HTML tag stripper for grep.app snippets
	// <mark> is used for highlighting, <br/> for breaks
	// Other tags like <table>, <tr>, <td> are for layout
	// We want to extract the text content and handle line breaks

	// 1. Replace <br/> and </div> with newline to preserve line breaks
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "</div>", "\n")
	s = strings.ReplaceAll(s, "</td>", " ")

	// 2. Remove all other tags
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}

	// 3. Unescape common HTML entities
	res := sb.String()
	res = strings.ReplaceAll(res, "&quot;", "\"")
	res = strings.ReplaceAll(res, "&amp;", "&")
	res = strings.ReplaceAll(res, "&lt;", "<")
	res = strings.ReplaceAll(res, "&gt;", ">")
	res = strings.ReplaceAll(res, "&#39;", "'")
	res = strings.ReplaceAll(res, "&nbsp;", " ")

	// 4. Cleanup multiple newlines
	lines := strings.Split(res, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Grep.app often has line numbers at the start of lines in the snippet
		// but they are often separated or in their own cells.
		// If the line is just a number, skip it to keep code clean.
		if isNumber(trimmed) {
			continue
		}
		if trimmed != "" || (len(cleanLines) > 0 && cleanLines[len(cleanLines)-1] != "") {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (t *CLIUsageFinderTool) fetchFile(ctx context.Context, args map[string]any) (string, error) {
	owner, _ := args["owner"].(string)
	repo, _ := args["repo"].(string)
	path, _ := args["path"].(string)
	ref, _ := args["ref"].(string)
	maxChars := 10000
	if mc, ok := args["max_chars"].(float64); ok {
		maxChars = int(mc)
	}

	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	path = strings.Trim(strings.TrimSpace(path), "/")

	if owner == "" || repo == "" {
		return "", fmt.Errorf("owner and repo are required in fetch_file mode")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	if ref != "" {
		apiURL += "?ref=" + strings.TrimSpace(ref)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "LuckClaw/1.0")

	resp, err := t.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found: %s/%s/%s", owner, repo, path)
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("rate limit exceeded or access denied")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if len(body) > 0 && body[0] == '[' {
		var items []githubContent
		if err := json.Unmarshal(body, &items); err != nil {
			return "", fmt.Errorf("failed to parse directory contents: %w", err)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Path %q is a directory. Contents:\n\n", path))
		for _, item := range items {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", item.Name, item.Type))
		}
		sb.WriteString("\nTo fetch a specific file, please provide its full path.")
		return sb.String(), nil
	}

	var content githubContent
	if err := json.Unmarshal(body, &content); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if content.Type == "dir" {
		return fmt.Sprintf("Path %q is a directory, not a file. Use search mode to find files.", path), nil
	}

	var fileContent string
	if content.Content != "" {
		// GitHub base64 may contain newlines, which base64.StdEncoding doesn't like.
		// Use a replacer or StdEncoding.WithPadding(base64.StdPadding) if needed,
		// but simple newline removal is most robust for GitHub's output.
		cleanContent := strings.ReplaceAll(content.Content, "\n", "")
		cleanContent = strings.ReplaceAll(cleanContent, "\r", "")
		decoded, err := base64.StdEncoding.DecodeString(cleanContent)
		if err != nil {
			return "", fmt.Errorf("failed to decode base64: %w", err)
		}
		fileContent = string(decoded)
	} else if content.DownloadURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", content.DownloadURL, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create download request: %w", err)
		}
		resp, err := t.HTTPClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to download: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read: %w", err)
		}
		fileContent = string(body)
	}

	if len(fileContent) > maxChars {
		fileContent = fileContent[:maxChars] + fmt.Sprintf("\n\n... (truncated, total %d chars)", len(fileContent))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s\n\n", content.Name))
	sb.WriteString(fmt.Sprintf("**Repository:** %s/%s\n", owner, repo))
	sb.WriteString(fmt.Sprintf("**Path:** %s\n", content.Path))
	sb.WriteString(fmt.Sprintf("**Size:** %d bytes\n", content.Size))
	if ref != "" {
		sb.WriteString(fmt.Sprintf("**Ref:** %s\n", ref))
	}
	sb.WriteString(fmt.Sprintf("**SHA:** %s\n\n", content.Sha))

	language := detectLanguage(content.Path)
	if language != "" {
		sb.WriteString(fmt.Sprintf("**Language:** %s\n\n", language))
	}

	sb.WriteString("```" + language + "\n")
	sb.WriteString(fileContent)
	sb.WriteString("\n```")

	return sb.String(), nil
}

func detectLanguage(filename string) string {
	idx := strings.LastIndex(filename, ".")
	if idx == -1 {
		if strings.EqualFold(filename, "Dockerfile") {
			return "dockerfile"
		}
		return ""
	}
	ext := strings.ToLower(filename[idx+1:])
	switch ext {
	case "sh", "bash", "zsh":
		return "bash"
	case "py":
		return "python"
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	case "go":
		return "go"
	case "rs":
		return "rust"
	case "java":
		return "java"
	case "rb":
		return "ruby"
	case "php":
		return "php"
	case "c", "h":
		return "c"
	case "cpp", "cc", "hpp":
		return "cpp"
	case "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	case "toml":
		return "toml"
	case "md":
		return "markdown"
	case "dockerfile":
		return "dockerfile"
	default:
		return ""
	}
}
