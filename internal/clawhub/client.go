package clawhub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultRegistry = "https://clawhub.ai"
	requestTimeout  = 15 * time.Second
)

// Client is a ClawHub API client. No Node.js required.
type Client struct {
	Registry   string
	HTTPClient *http.Client
	Token      string // optional auth token for publish
}

// NewClient creates a client. Registry can be empty to use default.
func NewClient(registry string) *Client {
	if registry == "" {
		registry = DefaultRegistry
	}
	registry = strings.TrimSuffix(registry, "/")
	return &Client{
		Registry: registry,
		HTTPClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// DiscoverRegistry fetches registry URL from well-known. Returns registry base URL.
func DiscoverRegistry(siteURL string) (string, error) {
	if siteURL == "" {
		siteURL = DefaultRegistry
	}
	siteURL = strings.TrimSuffix(siteURL, "/")
	wellKnown := siteURL + "/.well-known/clawhub.json"
	resp, err := http.Get(wellKnown)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("well-known: %s", resp.Status)
	}
	var cfg struct {
		Registry string `json:"registry"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", err
	}
	if cfg.Registry == "" {
		return siteURL, nil
	}
	return strings.TrimSuffix(cfg.Registry, "/"), nil
}

// SearchResult holds one search result.
type SearchResult struct {
	Slug        string  `json:"slug"`
	DisplayName string  `json:"displayName"`
	Summary     string  `json:"summary"`
	Version     string  `json:"version"`
	Score       float64 `json:"score"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// SearchResponse is the API response for /api/v1/search.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// Search searches skills by query.
func (c *Client) Search(query string, limit int) (*SearchResponse, error) {
	if query == "" {
		return nil, fmt.Errorf("query required")
	}
	u, _ := url.Parse(c.Registry + "/api/v1/search")
	q := u.Query()
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClawHub rate limit exceeded (429). Try again in a minute. %s", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search: %s: %s", resp.Status, string(body))
	}

	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SkillMeta holds skill metadata from /api/v1/skills/<slug>.
type SkillMeta struct {
	Skill         *SkillInfo  `json:"skill"`
	LatestVersion *Version    `json:"latestVersion"`
	Owner         *Owner      `json:"owner"`
	Moderation    *Moderation `json:"moderation"`
}

type SkillInfo struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Summary     string `json:"summary"`
}

type Version struct {
	Version   string `json:"version"`
	CreatedAt int64  `json:"createdAt"`
	Changelog string `json:"changelog"`
}

type Owner struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type Moderation struct {
	IsSuspicious     bool `json:"isSuspicious"`
	IsMalwareBlocked bool `json:"isMalwareBlocked"`
}

// GetSkill fetches skill metadata.
func (c *Client) GetSkill(slug string) (*SkillMeta, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || strings.Contains(slug, "/") || strings.Contains(slug, "\\") || strings.Contains(slug, "..") {
		return nil, fmt.Errorf("invalid slug: %s", slug)
	}
	u := c.Registry + "/api/v1/skills/" + url.PathEscape(slug)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get skill: %s: %s", resp.Status, string(body))
	}

	var out SkillMeta
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadZip downloads a skill as zip bytes.
func (c *Client) DownloadZip(slug, version string) ([]byte, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || strings.Contains(slug, "/") || strings.Contains(slug, "\\") || strings.Contains(slug, "..") {
		return nil, fmt.Errorf("invalid slug: %s", slug)
	}
	u, _ := url.Parse(c.Registry + "/api/v1/download")
	q := u.Query()
	q.Set("slug", slug)
	if version != "" {
		q.Set("version", version)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download: %s: %s", resp.Status, string(body))
	}
	return io.ReadAll(resp.Body)
}
