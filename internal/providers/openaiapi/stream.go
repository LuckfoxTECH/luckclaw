package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StreamCallbacks holds optional callbacks for streaming. OnDelta is invoked for
// each content delta; OnToolCall is invoked when a complete tool call is parsed
// from the stream (OpenCode-style early execution).
type StreamCallbacks struct {
	OnDelta    func(delta string)
	OnToolCall func(tc ToolCall)
}

// toolCallDelta is the raw streaming format: each element has index to merge by.
type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolCallAccumulator merges streaming tool call deltas by index.
// OpenAI sends partial updates per index; we merge until arguments form valid JSON.
type toolCallAccumulator struct {
	byIndex map[int]*ToolCall
	emitted map[int]bool // already invoked OnToolCall for this index
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{
		byIndex: make(map[int]*ToolCall),
		emitted: make(map[int]bool),
	}
}

func (acc *toolCallAccumulator) merge(d toolCallDelta) {
	if acc.byIndex[d.Index] == nil {
		acc.byIndex[d.Index] = &ToolCall{}
	}
	tc := acc.byIndex[d.Index]
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Type != "" {
		tc.Type = d.Type
	}
	if d.Function.Name != "" {
		tc.Function.Name = d.Function.Name
	}
	if d.Function.Arguments != "" {
		tc.Function.Arguments += d.Function.Arguments
	}
}

// tryEmit checks if the tool call at index has complete arguments (valid JSON)
// and invokes onToolCall if so. Returns true if emitted.
func (acc *toolCallAccumulator) tryEmit(index int, onToolCall func(tc ToolCall)) bool {
	if onToolCall == nil || acc.emitted[index] {
		return false
	}
	tc := acc.byIndex[index]
	if tc == nil || tc.Function.Name == "" {
		return false
	}
	args := strings.TrimSpace(tc.Function.Arguments)
	if args == "" {
		return false
	}
	// Arguments must be valid JSON
	var check json.RawMessage
	if err := json.Unmarshal([]byte(args), &check); err != nil {
		return false
	}
	acc.emitted[index] = true
	copy := *tc
	normalizeToolCallIDs([]ToolCall{copy})
	onToolCall(copy)
	return true
}

// toOrderedSlice returns tool calls in index order for the final result.
func (acc *toolCallAccumulator) toOrderedSlice() []ToolCall {
	maxIdx := -1
	for i := range acc.byIndex {
		if i > maxIdx {
			maxIdx = i
		}
	}
	if maxIdx < 0 {
		return nil
	}
	out := make([]ToolCall, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		if tc := acc.byIndex[i]; tc != nil {
			out = append(out, *tc)
		}
	}
	return out
}

// ChatStream calls the API with stream=true and invokes callbacks.
// OnDelta receives each content delta; OnToolCall receives complete tool calls
// as soon as they are parsed (OpenCode-style). Returns full ChatResult when done.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, cb StreamCallbacks) (ChatResult, error) {
	if c.APIBase == "" {
		return ChatResult{}, fmt.Errorf("apiBase is empty")
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 120 * time.Second}
		// Configure proxy from environment if not explicitly set
		if transport, ok := c.HTTPClient.Transport.(*http.Transport); !ok || transport == nil {
			transport = &http.Transport{}
			transport.Proxy = http.ProxyFromEnvironment
			c.HTTPClient.Transport = transport
		}
	}

	body, err := c.buildRequestBody(req)
	if err != nil {
		return ChatResult{}, err
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChatResult{}, err
	}
	raw["stream"] = true
	body, _ = json.Marshal(raw)

	url := strings.TrimRight(c.APIBase, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return ChatResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.ExtraHeaders {
		if strings.TrimSpace(k) != "" {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResult{}, ClassifyNetworkError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return ChatResult{}, ClassifyHTTPError(resp.StatusCode, string(b))
	}

	var fullContent strings.Builder
	acc := newToolCallAccumulator()
	var finishReason string
	var usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string          `json:"content"`
						ToolCalls []toolCallDelta `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					fullContent.WriteString(delta.Content)
					if cb.OnDelta != nil {
						cb.OnDelta(delta.Content)
					}
				}
				for _, d := range delta.ToolCalls {
					acc.merge(d)
					acc.tryEmit(d.Index, cb.OnToolCall)
				}
				if chunk.Choices[0].FinishReason != "" {
					finishReason = chunk.Choices[0].FinishReason
				}
			}
			if chunk.Usage.TotalTokens > 0 {
				usage = chunk.Usage
			} else if usage.TotalTokens == 0 && (chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0) {
				usage = chunk.Usage
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatResult{}, ClassifyNetworkError(err)
	}

	toolCalls := acc.toOrderedSlice()
	normalizeToolCallIDs(toolCalls)
	return ChatResult{
		Content:      fullContent.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: struct {
			PromptTokens     int
			CompletionTokens int
			TotalTokens      int
		}{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		},
	}, nil
}
