package tools

import (
	"bytes"
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

	"luckclaw/internal/config"
)

const (
	searchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36"
	searchTimeout   = 30 * time.Second // default; increased for DuckDuckGo HTML which can be slow
)

// webSearchProvider is the interface for web search backends.
type webSearchProvider interface {
	Search(ctx context.Context, query string, count int) (string, error)
}

// webSearchProviderChain tries providers in order until one succeeds.
type webSearchProviderChain struct {
	providers []webSearchProvider
}

func (c *webSearchProviderChain) Search(ctx context.Context, query string, count int) (string, error) {
	var lastErr error
	for _, p := range c.providers {
		result, err := p.Search(ctx, query, count)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "No search provider configured.", nil
}

// braveSearchProvider uses Brave Search API.
type braveSearchProvider struct {
	apiKey string
	client *http.Client
}

func (p *braveSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		if v, ok := os.LookupEnv("BRAVE_API_KEY"); ok {
			apiKey = v
		}
	}
	if apiKey == "" {
		if v, ok := os.LookupEnv("BRAVE_SEARCH_API_KEY"); ok {
			apiKey = v
		}
	}
	if apiKey == "" {
		return "", fmt.Errorf("Brave Search API key not configured")
	}

	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=" + fmt.Sprintf("%d", count)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Brave API error: status=%d", resp.StatusCode)
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
		return "", err
	}
	if len(parsed.Web.Results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var b strings.Builder
	for i, r := range parsed.Web.Results {
		if i >= count {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL), strings.TrimSpace(r.Description))
	}
	return strings.TrimSpace(b.String()), nil
}

// tavilySearchProvider uses Tavily API.
type tavilySearchProvider struct {
	apiKey string
	client *http.Client
}

func (p *tavilySearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		if v, ok := os.LookupEnv("TAVILY_API_KEY"); ok {
			apiKey = v
		}
	}
	if apiKey == "" {
		return "", fmt.Errorf("Tavily API key not configured")
	}

	payload := map[string]any{
		"api_key":      apiKey,
		"query":        query,
		"search_depth": "basic",
		"max_results":  count,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Tavily API error: status=%d", resp.StatusCode)
	}

	var searchResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", err
	}
	if len(searchResp.Results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var b strings.Builder
	for i, r := range searchResp.Results {
		if i >= count {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return strings.TrimSpace(b.String()), nil
}

// duckDuckGoSearchProvider scrapes html.duckduckgo.com (no API key).
var (
	reDDGLink    = regexp.MustCompile(`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`)
	reDDGSnippet = regexp.MustCompile(`<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>([\s\S]*?)</a>`)
	reDDGStrip   = regexp.MustCompile(`<[^>]+>`)
)

type duckDuckGoSearchProvider struct {
	client *http.Client
}

func (p *duckDuckGoSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return p.extractResults(string(body), count, query)
}

func (p *duckDuckGoSearchProvider) extractResults(html string, count int, query string) (string, error) {
	matches := reDDGLink.FindAllStringSubmatch(html, count+5)
	if len(matches) == 0 {
		return fmt.Sprintf("No results for: %s (via DuckDuckGo)", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Results for: %s (via DuckDuckGo)\n", query)
	snippetMatches := reDDGSnippet.FindAllStringSubmatch(html, count+5)
	maxItems := count
	if len(matches) < maxItems {
		maxItems = len(matches)
	}

	for i := 0; i < maxItems; i++ {
		urlStr := matches[i][1]
		title := strings.TrimSpace(reDDGStrip.ReplaceAllString(matches[i][2], ""))
		if strings.Contains(urlStr, "uddg=") {
			if u, err := url.QueryUnescape(urlStr); err == nil {
				if _, after, ok := strings.Cut(u, "uddg="); ok {
					urlStr = after
				}
			}
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n", i+1, title, urlStr)
		if i < len(snippetMatches) {
			snippet := strings.TrimSpace(reDDGStrip.ReplaceAllString(snippetMatches[i][1], ""))
			if snippet != "" {
				fmt.Fprintf(&b, "   %s\n", snippet)
			}
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// perplexitySearchProvider uses Perplexity API (LLM-based search).
type perplexitySearchProvider struct {
	apiKey string
	client *http.Client
}

func (p *perplexitySearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		if v, ok := os.LookupEnv("PERPLEXITY_API_KEY"); ok {
			apiKey = v
		}
	}
	if apiKey == "" {
		return "", fmt.Errorf("Perplexity API key not configured")
	}

	payload := map[string]any{
		"model": "sonar",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", query, count),
			},
		},
		"max_tokens": 1000,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.perplexity.ai/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Perplexity API error: status=%d", resp.StatusCode)
	}

	var searchResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", err
	}
	if len(searchResp.Choices) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}
	return fmt.Sprintf("Results for: %s (via Perplexity)\n%s", query, searchResp.Choices[0].Message.Content), nil
}

// searxngSearchProvider uses self-hosted SearXNG instance.
type searxngSearchProvider struct {
	baseURL string
	client  *http.Client
}

func (p *searxngSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	base := strings.TrimSuffix(p.baseURL, "/")
	if base == "" {
		return "", fmt.Errorf("SearXNG baseURL not configured")
	}
	searchURL := base + "/search?q=" + url.QueryEscape(query) + "&format=json&categories=general"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SearXNG returned status %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Engine  string  `json:"engine"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Results for: %s (via SearXNG)\n", query)
	for i, r := range result.Results {
		if i >= count {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return strings.TrimSpace(b.String()), nil
}

// NewWebSearchTool builds a WebSearchTool from config with multi-provider fallback.
// Priority: Brave > Tavily > SearXNG > Perplexity > DuckDuckGo (no API key).
func NewWebSearchTool(cfg config.WebSearchConfig, proxyCfg config.WebToolsConfig) *WebSearchTool {
	timeout := searchTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	client := newHTTPClientWithProxyConfig(proxyCfg, timeout)
	var providers []webSearchProvider
	maxResults := 5

	// Brave
	if cfg.Brave.Enabled && strings.TrimSpace(cfg.Brave.APIKey) != "" {
		key := cfg.Brave.APIKey
		if key == "" {
			key = cfg.APIKey
		}
		if key != "" {
			providers = append(providers, &braveSearchProvider{apiKey: key, client: client})
			if cfg.Brave.MaxResults > 0 {
				maxResults = cfg.Brave.MaxResults
			} else if cfg.MaxResults > 0 {
				maxResults = cfg.MaxResults
			}
		}
	}

	// Tavily
	if cfg.Tavily.Enabled && strings.TrimSpace(cfg.Tavily.APIKey) != "" {
		providers = append(providers, &tavilySearchProvider{apiKey: cfg.Tavily.APIKey, client: client})
		if cfg.Tavily.MaxResults > 0 && len(providers) == 1 {
			maxResults = cfg.Tavily.MaxResults
		}
	}

	// SearXNG
	if cfg.SearXNG.Enabled && strings.TrimSpace(cfg.SearXNG.BaseURL) != "" {
		providers = append(providers, &searxngSearchProvider{baseURL: cfg.SearXNG.BaseURL, client: client})
		if cfg.SearXNG.MaxResults > 0 && len(providers) == 1 {
			maxResults = cfg.SearXNG.MaxResults
		}
	}

	// Perplexity (slower, LLM-based)
	if cfg.Perplexity.Enabled && strings.TrimSpace(cfg.Perplexity.APIKey) != "" {
		providers = append(providers, &perplexitySearchProvider{apiKey: cfg.Perplexity.APIKey, client: client})
		if cfg.Perplexity.MaxResults > 0 && len(providers) == 1 {
			maxResults = cfg.Perplexity.MaxResults
		}
	}

	// DuckDuckGo (no API key, always last fallback)
	if cfg.DuckDuckGo.Enabled {
		providers = append(providers, &duckDuckGoSearchProvider{client: client})
		if cfg.DuckDuckGo.MaxResults > 0 && len(providers) == 1 {
			maxResults = cfg.DuckDuckGo.MaxResults
		}
	}

	// Legacy: only APIKey set (Brave)
	if len(providers) == 0 && strings.TrimSpace(cfg.APIKey) != "" {
		providers = append(providers, &braveSearchProvider{apiKey: cfg.APIKey, client: client})
		if cfg.MaxResults > 0 {
			maxResults = cfg.MaxResults
		}
	}

	if len(providers) == 0 {
		return nil
	}

	return &WebSearchTool{
		chain:       &webSearchProviderChain{providers: providers},
		MaxResults:  maxResults,
		ProxyConfig: proxyCfg,
		HTTP:        client,
	}
}
