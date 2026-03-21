package openaiapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"luckclaw/internal/config"
)

type Client struct {
	APIKey                string
	APIBase               string
	ExtraHeaders          map[string]string
	HTTPClient            *http.Client
	SupportsPromptCaching bool // When true, inject cache_control for system/tools (Anthropic-style)
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"` // string or []ContentPart for multimodal
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	RunMode    string     `json:"run_mode,omitempty"` // To track build/plan mode in history
	RequestRaw string     `json:"-"`                  // Raw input before context injection
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ContentPart interface {
	contentPart()
}

type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) contentPart() {}

type ImageURLDetail string

const (
	ImageURLDetailAuto ImageURLDetail = "auto"
	ImageURLDetailLow  ImageURLDetail = "low"
	ImageURLDetailHigh ImageURLDetail = "high"
)

type ImageURL struct {
	URL    string         `json:"url"`
	Detail ImageURLDetail `json:"detail,omitempty"`
}

type ImageURLPart struct {
	Type     string   `json:"type"`
	ImageURL ImageURL `json:"image_url"`
}

func (ImageURLPart) contentPart() {}

func NewTextPart(text string) TextPart {
	return TextPart{Text: text}
}

func NewImageURLPart(url string) ImageURLPart {
	return ImageURLPart{
		Type:     "image_url",
		ImageURL: ImageURL{URL: url},
	}
}

func NewImageURLPartWithDetail(url string, detail ImageURLDetail) ImageURLPart {
	return ImageURLPart{
		Type:     "image_url",
		ImageURL: ImageURL{URL: url, Detail: detail},
	}
}

func ContentPartsToAny(parts []ContentPart) []any {
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out
}

type ChatRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      string           `json:"tool_choice,omitempty"`
	Temperature     float64          `json:"temperature,omitempty"`
	MaxTokens       int              `json:"max_tokens,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	PromptCacheKey  string           `json:"prompt_cache_key,omitempty"` // Codex-style cache key (SHA256 of messages)
}

type ChatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content,omitempty"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type ChatResult struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	Usage            struct {
		PromptTokens     int
		CompletionTokens int
		TotalTokens      int
	}
}

// ChatClient is the interface for LLM chat. Both Client and FailoverClient implement it.
type ChatClient interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
}

// StreamingChatClient supports streaming. Client implements it; FailoverClient tries each client.
type StreamingChatClient interface {
	ChatClient
	ChatStream(ctx context.Context, req ChatRequest, cb StreamCallbacks) (ChatResult, error)
}

// SanitizeEmptyContent replaces empty content that causes provider 400 errors.
// Empty content can appear when MCP tools return nothing.
func SanitizeEmptyContent(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		dup := m
		content := m.Content

		if s, ok := content.(string); ok {
			if s == "" {
				if m.Role == "assistant" && len(m.ToolCalls) > 0 {
					dup.Content = nil
				} else {
					dup.Content = "(empty)"
				}
			}
			out = append(out, dup)
			continue
		}

		if parts, ok := content.([]any); ok {
			var filtered []any
			for _, item := range parts {
				if itemMap, ok := item.(map[string]any); ok {
					t := itemMap["type"]
					text, _ := itemMap["text"].(string)
					if (t == "text" || t == "input_text" || t == "output_text") && text == "" {
						continue
					}
				}
				filtered = append(filtered, item)
			}
			if len(filtered) != len(parts) {
				if len(filtered) > 0 {
					dup.Content = filtered
				} else if m.Role == "assistant" && len(m.ToolCalls) > 0 {
					dup.Content = nil
				} else {
					dup.Content = "(empty)"
				}
			}
		}
		out = append(out, dup)
	}
	return out
}

// NewHTTPClientWithProxy creates an HTTP client with proxy configuration from WebToolsConfig.
// If webCfg is nil or has no proxy settings, falls back to http.ProxyFromEnvironment.
func NewHTTPClientWithProxy(webCfg *config.WebToolsConfig, timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if webCfg != nil && (webCfg.HTTPProxy != "" || webCfg.HTTPSProxy != "" || webCfg.AllProxy != "") {
		transport.Proxy = webCfg.ProxyFunc()
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	if c.APIBase == "" {
		return ChatResult{}, fmt.Errorf("apiBase is empty")
	}
	if c.HTTPClient == nil {
		var transport http.RoundTripper
		if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt != nil {
			t := dt.Clone()
			t.Proxy = http.ProxyFromEnvironment
			transport = t
		} else {
			transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
		}
		c.HTTPClient = &http.Client{Timeout: 120 * time.Second, Transport: transport}
	}

	body, err := c.buildRequestBody(req)
	if err != nil {
		return ChatResult{}, err
	}

	url := strings.TrimRight(c.APIBase, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ChatResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.ExtraHeaders {
		if strings.TrimSpace(k) == "" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResult{}, ClassifyNetworkError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, ClassifyNetworkError(err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResult{}, ClassifyHTTPError(resp.StatusCode, string(respBody))
	}

	var parsed ChatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ChatResult{}, &FailoverError{Reason: ReasonFormat, Body: string(respBody), Wrapped: err}
	}
	if len(parsed.Choices) == 0 {
		return ChatResult{}, &FailoverError{Reason: ReasonFormat, Body: string(respBody), Wrapped: fmt.Errorf("empty choices")}
	}

	choice := parsed.Choices[0]
	msg := choice.Message
	normalizeToolCallIDs(msg.ToolCalls)
	result := ChatResult{
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
		ToolCalls:        msg.ToolCalls,
		FinishReason:     choice.FinishReason,
	}
	result.Usage.PromptTokens = parsed.Usage.PromptTokens
	result.Usage.CompletionTokens = parsed.Usage.CompletionTokens
	result.Usage.TotalTokens = parsed.Usage.TotalTokens
	return result, nil
}

// buildRequestBody marshals the request and optionally injects cache_control
// for Anthropic-style prompt caching when SupportsPromptCaching is true.
func (c *Client) buildRequestBody(req ChatRequest) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if !c.SupportsPromptCaching {
		return body, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, nil
	}
	applyCacheControl(raw)
	return json.Marshal(raw)
}

// applyCacheControl injects cache_control: {"type": "ephemeral"} into system
// message content and the last tool definition (Anthropic prompt caching).
func applyCacheControl(raw map[string]any) {
	cacheBlock := map[string]any{"type": "ephemeral"}
	if msgs, ok := raw["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok || msg["role"] != "system" {
				continue
			}
			content := msg["content"]
			if s, ok := content.(string); ok && s != "" {
				msg["content"] = []any{map[string]any{"type": "text", "text": s, "cache_control": cacheBlock}}
			} else if parts, ok := content.([]any); ok && len(parts) > 0 {
				last := len(parts) - 1
				if p, ok := parts[last].(map[string]any); ok {
					p["cache_control"] = cacheBlock
				}
			}
			break
		}
	}
	if tools, ok := raw["tools"].([]any); ok && len(tools) > 0 {
		if t, ok := tools[len(tools)-1].(map[string]any); ok {
			t["cache_control"] = cacheBlock
		}
	}
}

// normalizeToolCallIDs ensures all tool_call IDs are short alphanumeric
// strings. Some providers (e.g. Mistral, GitHub Copilot) return IDs that
// are too long or contain characters rejected by other providers in the
// subsequent tool-result message.
func normalizeToolCallIDs(calls []ToolCall) {
	for i := range calls {
		id := calls[i].ID
		if id == "" || len(id) > 40 || !isAlphanumeric(id) {
			calls[i].ID = shortCallID()
		}
	}
}

func isAlphanumeric(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func shortCallID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}
