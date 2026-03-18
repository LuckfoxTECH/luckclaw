package tools

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"luckclaw/internal/config"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPToolWrapper wraps a single MCP server tool as a luckclaw Tool.
type MCPToolWrapper struct {
	Session      *mcp.ClientSession
	ServerName   string
	OriginalName string
	Desc         string
	InputSchema  map[string]any
	Timeout      time.Duration
}

func (t *MCPToolWrapper) Name() string {
	return "mcp_" + t.ServerName + "_" + t.OriginalName
}

func (t *MCPToolWrapper) Description() string {
	base := t.Desc
	if base == "" {
		base = t.OriginalName
	}

	return base + " [MCP: call with JSON args, not exec]"
}

// setProtocolVersion sets the unexported protocolVersion field on ClientSessionOptions
// for SSE compatibility with TypeScript MCP SDK 0.5.0 (expects 2024-11-05).
func setProtocolVersion(opts *mcp.ClientSessionOptions, v string) error {
	if opts == nil {
		return fmt.Errorf("nil options")
	}
	rv := reflect.ValueOf(opts).Elem()
	rf := rv.FieldByName("protocolVersion")
	if !rf.IsValid() {
		return fmt.Errorf("protocolVersion field not found")
	}
	reflect.NewAt(rf.Type(), unsafe.Pointer(rf.UnsafeAddr())).Elem().Set(reflect.ValueOf(v))
	return nil
}

// coerceNumericArgs converts string values that look like numbers to float64.
// LLMs sometimes send {"a": "4545", "b": "54122"} instead of numbers; MCP servers expect numeric types.
func coerceNumericArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok && s != "" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				out[k] = f
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (t *MCPToolWrapper) Parameters() map[string]any {
	if t.InputSchema != nil {
		return t.InputSchema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *MCPToolWrapper) Execute(ctx context.Context, args map[string]any) (string, error) {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Coerce string numbers to float64; MCP servers (e.g. addition) expect numeric types, not strings
	args = coerceNumericArgs(args)

	res, err := t.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.OriginalName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("MCP tool error: %s", res.Content)
	}

	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		} else if c != nil {
			parts = append(parts, fmt.Sprint(c))
		}
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

// MCPSession holds an MCP client session and implements io.Closer.
type MCPSession struct {
	*mcp.ClientSession
}

// ConnectMCPServers connects to configured MCP servers and registers their tools.
// Sessions must be kept alive for the lifetime of the agent.
func ConnectMCPServers(ctx context.Context, cfg config.Config, registry *Registry) ([]*MCPSession, error) {
	servers := cfg.Tools.MCPServers
	if len(servers) == 0 {
		return nil, nil
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "luckclaw", Version: "1.0"}, nil)
	var sessions []*MCPSession

	for name, scfg := range servers {
		transportType := strings.TrimSpace(strings.ToLower(scfg.Type))
		if transportType == "" {
			if scfg.Command != "" {
				transportType = "stdio"
			} else if scfg.URL != "" {
				url := strings.TrimRight(scfg.URL, "/")
				if strings.HasSuffix(url, "/sse") {
					transportType = "sse"
				} else {
					transportType = "streamablehttp"
				}
			} else {
				continue
			}
		}
		// Tolerate typo: "see" -> "sse" when URL suggests SSE
		if transportType == "see" && strings.Contains(strings.ToLower(scfg.URL), "/sse") {
			transportType = "sse"
		}

		var transport mcp.Transport
		switch transportType {
		case "stdio":
			if scfg.Command == "" {
				continue
			}
			cmd := exec.CommandContext(ctx, scfg.Command, scfg.Args...)
			cmd.Env = envSlice(scfg.Env)
			transport = &mcp.CommandTransport{Command: cmd}
		case "sse":
			if scfg.URL == "" {
				continue
			}
			transport = &mcp.SSEClientTransport{
				Endpoint:   strings.TrimRight(scfg.URL, "/"),
				HTTPClient: newMCPHTTPClient(cfg, scfg, 60*time.Second),
			}
		case "streamablehttp":
			if scfg.URL == "" {
				continue
			}
			transport = &mcp.StreamableClientTransport{
				Endpoint:   strings.TrimRight(scfg.URL, "/"),
				HTTPClient: newMCPHTTPClient(cfg, scfg, 60*time.Second),
			}
		default:
			continue
		}

		var session *mcp.ClientSession
		var err error
		if transportType == "sse" {
			// TypeScript MCP SDK 0.5.0 expects 2024-11-05; Go SDK defaults to 2025-06-18
			opts := &mcp.ClientSessionOptions{}
			if err := setProtocolVersion(opts, "2024-11-05"); err != nil {
				return nil, fmt.Errorf("MCP server %q: SSE protocolVersion override not supported by current MCP SDK: %w", name, err)
			}
			session, err = client.Connect(ctx, transport, opts)
		} else {
			session, err = client.Connect(ctx, transport, nil)
		}
		if err != nil {
			return nil, fmt.Errorf("MCP server %q: %w", name, err)
		}
		sessions = append(sessions, &MCPSession{session})

		toolTimeout := time.Duration(scfg.ToolTimeout) * time.Second
		if toolTimeout <= 0 {
			toolTimeout = 30 * time.Second
		}

		res, err := session.ListTools(ctx, nil)
		if err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("MCP server %q list tools: %w", name, err)
		}

		for _, tool := range res.Tools {
			schema := map[string]any{"type": "object", "properties": map[string]any{}}
			if tool.InputSchema != nil {
				if m, ok := tool.InputSchema.(map[string]any); ok {
					schema = m
				}
			}
			wrapper := &MCPToolWrapper{
				Session:      session,
				ServerName:   name,
				OriginalName: tool.Name,
				Desc:         tool.Description,
				InputSchema:  schema,
				Timeout:      toolTimeout,
			}
			registry.Register(wrapper)
		}
	}

	return sessions, nil
}

func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// newMCPHTTPClient creates an HTTP client for MCP SSE/streamable transports.
// Uses proxy from tools.web config and optional headers from mcp server config.
func newMCPHTTPClient(cfg config.Config, scfg config.MCPServerConfig, timeout time.Duration) *http.Client {
	base := newHTTPClientWithProxyConfig(cfg.Tools.Web, timeout)
	if len(scfg.Headers) == 0 {
		return base
	}
	// Wrap transport to add custom headers
	orig := base.Transport
	if orig == nil {
		orig = http.DefaultTransport
	}
	base.Transport = &headerRoundTripper{
		RoundTripper: orig,
		Headers:      scfg.Headers,
	}
	return base
}

// headerRoundTripper adds custom headers to outgoing requests.
type headerRoundTripper struct {
	http.RoundTripper
	Headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	return h.RoundTripper.RoundTrip(req)
}
