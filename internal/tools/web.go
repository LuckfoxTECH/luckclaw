package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"

	"luckclaw/internal/config"
)

const (
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36"
	maxRedirects    = 5
	defaultMaxChars = 50000
)

// newHTTPClientWithProxyConfig creates an http.Client with proxy from WebToolsConfig.
// Supports httpProxy, httpsProxy, allProxy.
func newHTTPClientWithProxyConfig(cfg config.WebToolsConfig, timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if cfg.HTTPProxy != "" || cfg.HTTPSProxy != "" || cfg.AllProxy != "" {
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			proxyURL := cfg.ProxyForScheme(req.URL.Scheme)
			if proxyURL == "" {
				return nil, nil
			}
			return url.Parse(proxyURL)
		}
	} else {
		// When not explicitly configured, it falls back to the system environment proxy (HTTP(S)_PROXY / ALL_PROXY)
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

type WebSearchTool struct {
	// Legacy: direct Brave API (when chain is nil)
	APIKey      string
	MaxResults  int
	ProxyConfig config.WebToolsConfig
	HTTP        *http.Client

	// Multi-provider chain (Brave, Tavily, DuckDuckGo, Perplexity, SearXNG)
	chain *webSearchProviderChain
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web for up-to-date information. Supports Brave, Tavily, DuckDuckGo, Perplexity, SearXNG. NOTE: For CLI usage, flags, and arguments, always use cli_usage_finder first as it provides more accurate source-based information."
}

func (t *WebSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query. For CLI usage questions, include the CLI name and context.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Max results (optional)",
			},
		},
		"required": []any{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	maxResults := t.MaxResults
	if v, ok := args["max_results"].(float64); ok && int(v) > 0 {
		maxResults = int(v)
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	// Use multi-provider chain when available
	if t.chain != nil {
		result, err := t.chain.Search(ctx, query, maxResults)
		if err != nil {
			return "", err
		}
		return result, nil
	}

	// Legacy: Brave-only
	if strings.TrimSpace(t.APIKey) == "" {
		if v, ok := os.LookupEnv("BRAVE_API_KEY"); ok {
			t.APIKey = v
		}
	}
	if strings.TrimSpace(t.APIKey) == "" {
		if v, ok := os.LookupEnv("BRAVE_SEARCH_API_KEY"); ok {
			t.APIKey = v
		}
	}
	if strings.TrimSpace(t.APIKey) == "" {
		return "", fmt.Errorf("no web search provider configured. Set tools.web.search.apiKey (Brave), or tools.web.search.duckduckgo.enabled=true for free DuckDuckGo")
	}

	client := t.HTTP
	if client == nil {
		client = newHTTPClientWithProxyConfig(t.ProxyConfig, 20*time.Second)
	}

	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=" + fmt.Sprintf("%d", maxResults)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web search failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse search response: %w", err)
	}
	if len(parsed.Web.Results) == 0 {
		return "No results.", nil
	}

	var b strings.Builder
	for i, r := range parsed.Web.Results {
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL), strings.TrimSpace(r.Description))
	}
	return strings.TrimSpace(b.String()), nil
}

type WebFetchTool struct {
	MaxChars        int
	ProxyConfig     config.WebToolsConfig
	FirecrawlAPIKey string // optional; used as fallback when built-in fetch fails
	HTTP            *http.Client
}

func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch URL and extract readable content (HTML → markdown/text)."
}
func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
			"extractMode": map[string]any{
				"type":        "string",
				"description": "Output format: markdown or text",
				"enum":        []any{"markdown", "text"},
			},
			"maxChars": map[string]any{
				"type":        "integer",
				"description": "Max characters to return (optional)",
				"minimum":     100,
			},
		},
		"required": []any{"url"},
	}
}

type webFetchResult struct {
	URL       string `json:"url,omitempty"`
	FinalURL  string `json:"finalUrl,omitempty"`
	Status    int    `json:"status,omitempty"`
	Extractor string `json:"extractor,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Length    int    `json:"length,omitempty"`
	Text      string `json:"text,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	rawURL, _ := args["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return marshalResult(webFetchResult{Error: "url is required", URL: rawURL}), nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return marshalResult(webFetchResult{Error: "invalid url", URL: rawURL}), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return marshalResult(webFetchResult{Error: "only http/https is allowed", URL: rawURL}), nil
	}

	extractMode := "markdown"
	if m, ok := args["extractMode"].(string); ok && (m == "markdown" || m == "text") {
		extractMode = m
	}
	maxChars := t.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if v, ok := args["maxChars"].(float64); ok && int(v) >= 100 {
		maxChars = int(v)
	}

	// If Firecrawl is configured, try it first; fall back to built-in on failure
	apiKey := strings.TrimSpace(t.FirecrawlAPIKey)
	if apiKey == "" {
		if v, ok := os.LookupEnv("FIRECRAWL_API_KEY"); ok {
			apiKey = strings.TrimSpace(v)
		}
	}
	if apiKey != "" {
		if fcResult := t.fetchFirecrawl(ctx, rawURL, maxChars); fcResult != nil {
			return marshalResult(*fcResult), nil
		}
	}

	result := t.fetchBuiltin(ctx, rawURL, parsed, extractMode, maxChars)
	return marshalResult(result), nil
}

func (t *WebFetchTool) fetchBuiltin(ctx context.Context, rawURL string, parsed *url.URL, extractMode string, maxChars int) webFetchResult {
	client := t.HTTP
	if client == nil {
		client = newHTTPClientWithProxyConfig(t.ProxyConfig, 30*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return webFetchResult{Error: err.Error(), URL: rawURL}
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return webFetchResult{Error: err.Error(), URL: rawURL}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	if err != nil {
		return webFetchResult{Error: err.Error(), URL: rawURL}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return webFetchResult{
			Error: fmt.Sprintf("fetch failed: status=%d", resp.StatusCode),
			URL:   rawURL, Status: resp.StatusCode,
		}
	}

	ctype := resp.Header.Get("Content-Type")
	bodyStr := string(body)

	// JSON response: return as-is (truncated)
	if strings.Contains(ctype, "application/json") {
		var j map[string]any
		if json.Unmarshal(body, &j) == nil {
			text, _ := json.MarshalIndent(j, "", "  ")
			truncated := len(text) > maxChars
			if truncated {
				text = text[:maxChars]
			}
			return webFetchResult{
				URL: rawURL, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode,
				Extractor: "json", Truncated: truncated, Length: len(text), Text: string(text),
			}
		}
	}

	// HTML: use Readability
	prefix := bodyStr
	if len(prefix) > 256 {
		prefix = prefix[:256]
	}
	prefixLower := strings.ToLower(prefix)
	isHTML := strings.Contains(ctype, "text/html") || strings.HasPrefix(prefixLower, "<!doctype") || strings.HasPrefix(prefixLower, "<html")
	if isHTML {
		article, err := readability.FromReader(strings.NewReader(bodyStr), parsed)
		if err == nil && (article.Content != "" || article.TextContent != "") {
			var text string
			if extractMode == "markdown" {
				text = htmlToMarkdown(article.Content)
			} else {
				text = article.TextContent
			}
			if article.Title != "" {
				text = "# " + article.Title + "\n\n" + text
			}
			text = strings.TrimSpace(text)
			truncated := len(text) > maxChars
			if truncated {
				text = text[:maxChars]
			}
			return webFetchResult{
				URL: rawURL, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode,
				Extractor: "readability", Truncated: truncated, Length: len(text), Text: text,
			}
		}
		// Readability failed, fallback to heuristic
		text := extractContent(bodyStr)
		text = strings.TrimSpace(text)
		if text != "" {
			truncated := len(text) > maxChars
			if truncated {
				text = text[:maxChars]
			}
			return webFetchResult{
				URL: rawURL, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode,
				Extractor: "heuristic", Truncated: truncated, Length: len(text), Text: text,
			}
		}
	}

	// Raw text
	text := strings.TrimSpace(bodyStr)
	if text == "" {
		text = "(empty)"
	}
	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars]
	}
	return webFetchResult{
		URL: rawURL, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode,
		Extractor: "raw", Truncated: truncated, Length: len(text), Text: text,
	}
}

func (t *WebFetchTool) fetchFirecrawl(ctx context.Context, rawURL string, maxChars int) *webFetchResult {
	apiKey := strings.TrimSpace(t.FirecrawlAPIKey)
	if apiKey == "" {
		if v, ok := os.LookupEnv("FIRECRAWL_API_KEY"); ok {
			apiKey = strings.TrimSpace(v)
		}
	}
	if apiKey == "" {
		return nil
	}

	client := t.HTTP
	if client == nil {
		client = newHTTPClientWithProxyConfig(t.ProxyConfig, 60*time.Second)
	}

	reqBody := map[string]any{
		"url":             rawURL,
		"formats":         []string{"markdown"},
		"onlyMainContent": true,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.firecrawl.dev/v2/scrape", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var fcResp struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
		} `json:"data"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &fcResp); err != nil {
		return nil
	}
	if !fcResp.Success || fcResp.Data.Markdown == "" {
		if fcResp.Error != "" {
			return &webFetchResult{Error: "Firecrawl: " + fcResp.Error, URL: rawURL}
		}
		return nil
	}

	text := strings.TrimSpace(fcResp.Data.Markdown)
	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars]
	}
	return &webFetchResult{
		URL: rawURL, FinalURL: rawURL, Status: 200,
		Extractor: "firecrawl", Truncated: truncated, Length: len(text), Text: text,
	}
}

func marshalResult(r webFetchResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// htmlToMarkdown converts simple HTML to markdown (links, headings, lists).
func htmlToMarkdown(html string) string {
	// Links [text](url)
	linkRe := regexp.MustCompile(`(?i)<a\s+[^>]*href=["']([^"']+)["'][^>]*>([\s\S]*?)</a>`)
	html = linkRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		if len(sub) >= 3 {
			return "[" + stripTags(sub[2]) + "](" + sub[1] + ")"
		}
		return m
	})
	// Headings h1-h6
	for i := 6; i >= 1; i-- {
		hRe := regexp.MustCompile(fmt.Sprintf(`(?i)<h%d[^>]*>([\s\S]*?)</h%d>`, i, i))
		html = hRe.ReplaceAllString(html, "\n"+strings.Repeat("#", i)+" $1\n")
	}
	// List items
	liRe := regexp.MustCompile(`(?i)<li[^>]*>([\s\S]*?)</li>`)
	html = liRe.ReplaceAllString(html, "\n- $1")
	// Block elements to newlines
	blockRe := regexp.MustCompile(`(?i)</(p|div|section|article)>`)
	html = blockRe.ReplaceAllString(html, "\n\n")
	brRe := regexp.MustCompile(`(?i)<(br|hr)\s*/?>`)
	html = brRe.ReplaceAllString(html, "\n")
	return normalizeWhitespace(stripTags(html))
}

func stripTags(s string) string {
	return regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
}

func normalizeWhitespace(s string) string {
	s = regexp.MustCompile(`[ \t]+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n"))
}

// extractContent applies a lightweight readability heuristic:
// 1. Strip <script>, <style>, <nav>, <header>, <footer>, <aside> blocks entirely
// 2. Convert block tags to newlines
// 3. Strip remaining HTML tags
// 4. Collapse excessive whitespace
func extractContent(s string) string {
	work := s

	// Remove noise blocks entirely
	for _, tag := range []string{"script", "style", "nav", "header", "footer", "aside", "noscript", "svg"} {
		for {
			open := "<" + tag
			idx := strings.Index(strings.ToLower(work), open)
			if idx < 0 {
				break
			}
			tagEnd := strings.Index(work[idx:], ">")
			if tagEnd < 0 {
				break
			}
			closeTag := "</" + tag + ">"
			closeIdx := strings.Index(strings.ToLower(work[idx:]), closeTag)
			if closeIdx < 0 {
				work = work[:idx] + work[idx+tagEnd+1:]
			} else {
				work = work[:idx] + work[idx+closeIdx+len(closeTag):]
			}
		}
	}

	// Convert block elements to newlines
	var out strings.Builder
	inTag := false
	tagBuf := strings.Builder{}
	for _, r := range work {
		switch {
		case r == '<':
			inTag = true
			tagBuf.Reset()
			tagBuf.WriteRune(r)
		case r == '>' && inTag:
			inTag = false
			tagBuf.WriteRune(r)
			tagStr := strings.ToLower(tagBuf.String())
			// Block-level tags get a newline
			for _, bt := range []string{"<p", "<div", "<br", "<li", "<h1", "<h2", "<h3", "<h4", "<h5", "<h6", "<tr", "<td", "<th", "<article", "<section", "<blockquote"} {
				if strings.HasPrefix(tagStr, bt) || strings.HasPrefix(tagStr, "</"+bt[1:]) {
					out.WriteByte('\n')
					break
				}
			}
		case inTag:
			tagBuf.WriteRune(r)
		default:
			out.WriteRune(r)
		}
	}

	// Collapse whitespace
	text := out.String()
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// stripHTML is a simple fallback that just removes all HTML tags.
func stripHTML(s string) string {
	return extractContent(s)
}
