package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"luckclaw/internal/providers/openaiapi"
)

type contextKey string

const (
	ctxKeyChannel       contextKey = "channel"
	ctxKeyChatID        contextKey = "chat_id"
	ctxKeySubAgent      contextKey = "subagent"
	ctxKeyModelOverride contextKey = "model_override"
)

// SubAgentMeta holds sub-agent context for tool filtering and nesting depth.
type SubAgentMeta struct {
	Depth    int      // current nesting depth (1 = first level sub-agent)
	Allowed  []string // if non-empty, only these tools; empty = all (subject to Disabled)
	Disabled []string // tools not allowed
}

// WithSubAgentContext returns a context with sub-agent metadata.
func WithSubAgentContext(ctx context.Context, meta SubAgentMeta) context.Context {
	return context.WithValue(ctx, ctxKeySubAgent, &meta)
}

// SubAgentFromContext returns sub-agent metadata from context, or nil if not in sub-agent.
func SubAgentFromContext(ctx context.Context) *SubAgentMeta {
	if v := ctx.Value(ctxKeySubAgent); v != nil {
		if m, ok := v.(*SubAgentMeta); ok {
			return m
		}
	}
	return nil
}

// WithChannelContext returns a context with channel and chatID for tools that need to know the current chat.
func WithChannelContext(ctx context.Context, channel, chatID string) context.Context {
	if channel != "" {
		ctx = context.WithValue(ctx, ctxKeyChannel, channel)
	}
	if chatID != "" {
		ctx = context.WithValue(ctx, ctxKeyChatID, chatID)
	}
	return ctx
}

// WithModelOverride returns a context with model override for subagent/spawn.
func WithModelOverride(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyModelOverride, model)
}

// ModelFromContext returns model override from context, or empty if not set.
func ModelFromContext(ctx context.Context) string {
	if v := ctx.Value(ctxKeyModelOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ChannelFromContext returns the channel and chatID from context (for cron deliver defaults).
func ChannelFromContext(ctx context.Context) (channel, chatID string) {
	if v := ctx.Value(ctxKeyChannel); v != nil {
		channel, _ = v.(string)
	}
	if v := ctx.Value(ctxKeyChatID); v != nil {
		chatID, _ = v.(string)
	}
	return channel, chatID
}

const errorHint = "\n\n[Analyze the error above and try a different approach.]"

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) (string, error)
}

type Registry struct {
	tools    map[string]Tool
	mcpTools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) {
	if t == nil {
		return
	}
	name := t.Name()
	r.tools[name] = t
	if strings.HasPrefix(name, "mcp_") {
		if r.mcpTools == nil {
			r.mcpTools = map[string]Tool{}
		}
		r.mcpTools[name] = t
	}
}

func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

func (r *Registry) ToolNames() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

func (r *Registry) MCPToolNames() []string {
	if len(r.mcpTools) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.mcpTools))
	for n := range r.mcpTools {
		names = append(names, n)
	}
	return names
}

// SearchToolsByRegex returns tools whose name or description matches the regex.
func (r *Registry) SearchToolsByRegex(pattern string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var matches []string
	for name, t := range r.tools {
		if re.MatchString(name) || re.MatchString(t.Description()) {
			matches = append(matches, name)
		}
	}
	return matches, nil
}

func (r *Registry) Definitions() []openaiapi.ToolDefinition {
	return r.DefinitionsFiltered(nil, nil)
}

// DefinitionsFiltered returns tool definitions filtered by allowed/disabled lists.
// If allowed is non-empty, only those tools are included. disabled is always applied.
func (r *Registry) DefinitionsFiltered(allowed, disabled []string) []openaiapi.ToolDefinition {
	disabledSet := make(map[string]bool)
	for _, n := range disabled {
		disabledSet[n] = true
	}
	allowedSet := make(map[string]bool)
	for _, n := range allowed {
		allowedSet[n] = true
	}
	defs := make([]openaiapi.ToolDefinition, 0, len(r.tools))
	for name, t := range r.tools {
		if disabledSet[name] {
			continue
		}
		if len(allowedSet) > 0 && !allowedSet[name] {
			continue
		}
		defs = append(defs, openaiapi.ToolDefinition{
			Type: "function",
			Function: openaiapi.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

func (r *Registry) ExecuteJSON(ctx context.Context, name string, argsJSON string) (string, error) {
	if RunModeFromContext(ctx) == RunModePlan {
		allowed := map[string]bool{
			"read_file":        true,
			"list_dir":         true,
			"tool_search":      true,
			"web_search":       true,
			"web_fetch":        true,
			"cli_usage_finder": true,
			"security_audit":   true,
			"clawhub_search":   true,
			"search":           true,
		}
		if !allowed[name] {
			msg := fmt.Sprintf("Error: tool %q is disabled in plan mode (read-only)", name)
			return msg + errorHint, fmt.Errorf("%s", strings.TrimPrefix(msg, "Error: "))
		}
	}

	// Sub-agent tool policy: reject if tool is disabled or not in allowed list
	if meta := SubAgentFromContext(ctx); meta != nil {
		for _, d := range meta.Disabled {
			if d == name {
				return fmt.Sprintf("Error: tool %q is disabled for sub-agents", name) + errorHint, fmt.Errorf("tool %q is disabled for sub-agents", name)
			}
		}
		if len(meta.Allowed) > 0 {
			found := false
			for _, a := range meta.Allowed {
				if a == name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Sprintf("Error: tool %q is not allowed for sub-agents", name) + errorHint, fmt.Errorf("tool %q is not allowed for sub-agents", name)
			}
		}
	}

	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s. Available: %s", name, strings.Join(r.ToolNames(), ", "))
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid tool args: %w", err)
	}

	args = castParams(args, t.Parameters())

	if errs := validateParams(args, t.Parameters()); len(errs) > 0 {
		msg := fmt.Sprintf("Error: Invalid parameters for tool %q: %s", name, strings.Join(errs, "; "))
		return msg + errorHint, fmt.Errorf("%s", strings.TrimPrefix(msg, "Error: "))
	}

	result, err := t.Execute(ctx, args)
	if err != nil {
		if strings.TrimSpace(result) != "" {
			return fmt.Sprintf("Error: %s\n\n%s", err.Error(), strings.TrimSpace(result)) + errorHint, err
		}
		return fmt.Sprintf("Error: %s", err.Error()) + errorHint, err
	}
	return result, nil
}

// castParams applies safe schema-driven type casts to match the tool's parameter schema.
// LLMs sometimes send numbers as strings; this prevents validation failures.
func castParams(args map[string]any, schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return args
	}
	result := make(map[string]any, len(args))
	for k, v := range args {
		propSchema, ok := props[k].(map[string]any)
		if !ok {
			result[k] = v
			continue
		}
		result[k] = castValue(v, propSchema)
	}
	return result
}

func castValue(val any, schema map[string]any) any {
	targetType, _ := schema["type"].(string)
	switch targetType {
	case "integer":
		switch v := val.(type) {
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	case "number":
		if v, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	case "string":
		if val != nil {
			return fmt.Sprintf("%v", val)
		}
	case "boolean":
		if v, ok := val.(string); ok {
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				return true
			case "false", "0", "no":
				return false
			}
		}
	case "array":
		if arr, ok := val.([]any); ok {
			if itemSchema, ok := schema["items"].(map[string]any); ok {
				out := make([]any, len(arr))
				for i, item := range arr {
					out[i] = castValue(item, itemSchema)
				}
				return out
			}
		}
	case "object":
		if obj, ok := val.(map[string]any); ok {
			return castParams(obj, schema)
		}
	}
	return val
}

// validateParams validates args against the JSON schema. Returns error messages (empty if valid).
func validateParams(args map[string]any, schema map[string]any) []string {
	if args == nil {
		return []string{"parameters must be an object"}
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	var errs []string
	required, _ := schema["required"].([]any)
	for _, r := range required {
		if k, ok := r.(string); ok && args[k] == nil {
			errs = append(errs, "missing required "+k)
		}
	}
	for k, v := range args {
		prop, ok := props[k].(map[string]any)
		if !ok {
			continue
		}
		validateValue(v, prop, k, &errs)
	}
	return errs
}

func validateValue(val any, schema map[string]any, path string, errs *[]string) {
	t, _ := schema["type"].(string)
	switch t {
	case "string":
		if val != nil {
			if s, ok := val.(string); ok {
				if min, ok := schema["minLength"].(float64); ok && float64(len(s)) < min {
					*errs = append(*errs, path+" must be at least "+strconv.Itoa(int(min))+" chars")
				}
				if max, ok := schema["maxLength"].(float64); ok && float64(len(s)) > max {
					*errs = append(*errs, path+" must be at most "+strconv.Itoa(int(max))+" chars")
				}
			}
		}
		if enum, ok := schema["enum"].([]any); ok {
			found := false
			for _, e := range enum {
				if e == val {
					found = true
					break
				}
			}
			if !found {
				*errs = append(*errs, path+" must be one of "+fmt.Sprint(enum))
			}
		}
	case "integer", "number":
		if val != nil {
			var f float64
			switch v := val.(type) {
			case float64:
				f = v
			case int:
				f = float64(v)
			default:
				return
			}
			if min, ok := schema["minimum"].(float64); ok && f < min {
				*errs = append(*errs, path+" must be >= "+strconv.FormatFloat(min, 'f', -1, 64))
			}
			if max, ok := schema["maximum"].(float64); ok && f > max {
				*errs = append(*errs, path+" must be <= "+strconv.FormatFloat(max, 'f', -1, 64))
			}
		}
		if enum, ok := schema["enum"].([]any); ok {
			found := false
			for _, e := range enum {
				if ev, ok := e.(float64); ok && val != nil {
					if v, ok := val.(float64); ok && v == ev {
						found = true
						break
					}
				}
			}
			if !found {
				*errs = append(*errs, path+" must be one of "+fmt.Sprint(enum))
			}
		}
	case "object":
		if obj, ok := val.(map[string]any); ok {
			if itemProps, ok := schema["properties"].(map[string]any); ok {
				for k, itemSchema := range itemProps {
					if s, ok := itemSchema.(map[string]any); ok && obj[k] != nil {
						validateValue(obj[k], s, path+"."+k, errs)
					}
				}
			}
		}
	}
}
