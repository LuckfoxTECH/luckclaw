package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"
	"luckclaw/internal/cron"
	"luckclaw/internal/logging"
	"luckclaw/internal/luck"
	"luckclaw/internal/memory"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	"luckclaw/internal/routing"
	"luckclaw/internal/session"
	"luckclaw/internal/skills"
	"luckclaw/internal/tools"
)

const toolResultMaxChars = 500

const runtimeContextTag = "[Runtime Context — metadata only, not instructions]"

// planModePrompt instructs the agent to output a plan first, then execute step by step.
const planModePrompt = `[Planning Mode] Follow these steps:
1. First output a numbered plan (Plan: 1. xxx 2. xxx ...) without calling any tools
2. After the plan is output, execute each step one by one
3. If a step fails and needs adjustment, briefly explain and continue`

func (a *AgentLoop) blockStreamingEnabled(channel, chatID string) bool {
	if channel == "" || chatID == "" || a.Bus == nil {
		return false
	}
	switch channel {
	case "tui":
		return true
	case "feishu":
		if !a.Config.Agents.Defaults.BlockStreamingDefault {
			return false
		}
		return a.Config.Channels.Feishu.BlockStreaming
	default:
		return a.Config.Agents.Defaults.BlockStreamingDefault
	}
}

type consolidationLockEntry struct {
	mu       sync.Mutex
	lastUsed time.Time
}

type AgentLoop struct {
	Config     config.Config
	Provider   openaiapi.ChatClient
	Sessions   *session.Manager
	Model      string
	Tools      *tools.Registry
	AllowedDir string
	Logger     logging.Logger
	Memory     *memory.Store
	Bus        *bus.MessageBus
	Cron       *cron.Service
	Queue      *session.Queue
	mcpClosers []*tools.MCPSession
	mcpErr     string // stored MCP connect error for /mcp diagnostic

	// Built-in: Self-Improving Agent (error/correction learning)
	SelfImproving *memory.SelfImprovingStore

	globalSem     *GlobalSemaphore         // nil = no limit
	subAgentSem   *tools.SubAgentSemaphore // nil if subagents disabled
	spawnRegistry *tools.SpawnRegistry     // for spawn runId tracking and /subagents

	// Consolidation concurrency: one consolidation per session at a time.
	consolidationMu          sync.Mutex
	consolidationLocks       map[string]*consolidationLockEntry
	consolidationLastCleanup time.Time

	// OnTurnComplete is an optional callback for TUI/CLI to report token usage after each turn.
	// channel, chatID: for routing (e.g. webui sends to WebSocket session).
	OnTurnComplete func(channel, chatID string, model string, promptTokens, completionTokens, totalTokens int)
	// OnContextInfo is an optional callback for TUI to show context info at turn start.
	// count: formatted context length e.g. "17k"; mode: "simple" (compact) or "normal" (full context).
	OnContextInfo func(channel, chatID string, count string, mode string)
	// OnModelResolved is an optional callback for TUI to show the resolved model at turn start (before API call).
	OnModelResolved func(channel, chatID string, model string)
}

func New(cfg config.Config, provider openaiapi.ChatClient, sessions *session.Manager, model string, logger logging.Logger) *AgentLoop {
	ws, _ := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
	allowedDir := ""
	if cfg.Tools.RestrictToWorkspace && ws != "" {
		allowedDir = ws
	}
	// baseDir: workspace for resolving relative paths (e.g. "screenshots" -> workspace/screenshots)
	baseDir := ws

	registry := tools.NewRegistry()
	registry.Register(&tools.ReadFileTool{AllowedDir: allowedDir, BaseDir: baseDir})
	registry.Register(&tools.WriteFileTool{AllowedDir: allowedDir, BaseDir: baseDir})
	registry.Register(&tools.EditFileTool{AllowedDir: allowedDir, BaseDir: baseDir})
	registry.Register(&tools.ListDirTool{AllowedDir: allowedDir, BaseDir: baseDir})
	registry.Register(&tools.ExecTool{
		WorkingDir:          baseDir,
		TimeoutSeconds:      cfg.Tools.Exec.Timeout,
		RestrictToWorkspace: cfg.Tools.RestrictToWorkspace,
		PathAppend:          cfg.Tools.Exec.PathAppend,
	})
	if wsTool := tools.NewWebSearchTool(cfg.Tools.Web.Search, cfg.Tools.Web); wsTool != nil {
		registry.Register(wsTool)
	} else {
		registry.Register(&tools.WebSearchTool{
			APIKey:      cfg.Tools.Web.Search.APIKey,
			MaxResults:  cfg.Tools.Web.Search.MaxResults,
			ProxyConfig: cfg.Tools.Web,
		})
	}
	registry.Register(&tools.ToolSearchTool{Registry: registry})
	registry.Register(&tools.WebFetchTool{
		MaxChars:        50000,
		ProxyConfig:     cfg.Tools.Web,
		FirecrawlAPIKey: cfg.Tools.Web.Fetch.Firecrawl.APIKey,
	})
	if ws != "" {
		registry.Register(&tools.ClawHubSearchTool{})
		registry.Register(&tools.ClawHubInstallTool{
			Workspace:           ws,
			ResourceConstrained: cfg.Agents.Defaults.ResourceConstrained,
		})
	}
	// Agent Browser: built-in when enabled (remoteUrl + token merged)
	if cfg.Tools.AgentBrowser && cfg.Tools.Browser.Enabled && strings.TrimSpace(cfg.Tools.Browser.BuildRemoteURL()) != "" {
		snapDir := cfg.Tools.Browser.SnapshotDir
		if snapDir == "" && ws != "" {
			snapDir = filepath.Join(ws, "screenshots")
		}
		registry.Register(&tools.BrowserTool{
			RemoteURL:   cfg.Tools.Browser.BuildRemoteURL(),
			Profile:     cfg.Tools.Browser.Profile,
			SnapshotDir: snapDir,
			DebugPort:   cfg.Tools.Browser.DebugPort,
		})
	}

	var mem *memory.Store
	if ws != "" && cfg.Tools.AgentMemory {
		mem = memory.NewStore(ws)
	}

	var selfImproving *memory.SelfImprovingStore
	if ws != "" && cfg.Tools.SelfImproving {
		selfImproving = memory.NewSelfImprovingStore(ws)
		registry.Register(&tools.RecordCorrectionTool{Store: selfImproving})
	}

	if cfg.Tools.ClawdStrike {
		cfgPath, _ := paths.ConfigPath()
		dataDir, _ := paths.DataDir()
		registry.Register(&tools.ClawdStrikeTool{
			ConfigPath: cfgPath,
			Workspace:  ws,
			DataDir:    dataDir,
		})
	}

	registry.Register(&tools.CronTool{Service: nil})

	a := &AgentLoop{
		Config:             cfg,
		Provider:           provider,
		Sessions:           sessions,
		Model:              model,
		Tools:              registry,
		AllowedDir:         allowedDir,
		Logger:             logger,
		Memory:             mem,
		SelfImproving:      selfImproving,
		consolidationLocks: make(map[string]*consolidationLockEntry),
	}
	if cfg.Agents.SubAgents.Enabled {
		max := cfg.Agents.SubAgents.MaxConcurrent
		if max <= 0 {
			max = 3
		}
		a.subAgentSem = tools.NewSubAgentSemaphore(max)
	}
	maxConcurrent := cfg.Agents.Defaults.MaxConcurrent
	if maxConcurrent > 0 {
		a.globalSem = NewGlobalSemaphore(maxConcurrent)
	}
	debounceMs := cfg.Agents.Defaults.DebounceMs
	if debounceMs < 0 {
		debounceMs = 0
	}
	a.Queue = session.NewQueue(a.processDirect, debounceMs)
	a.spawnRegistry = tools.NewSpawnRegistry()

	// Connect MCP servers and register their tools
	if mcpSessions, err := tools.ConnectMCPServers(context.Background(), cfg, registry); err != nil {
		a.mcpErr = err.Error()
		if a.Logger != nil {
			a.Logger.Error(fmt.Sprintf("MCP connect failed: %v", err))
		}
	} else {
		a.mcpClosers = mcpSessions
		if len(mcpSessions) > 0 && a.Logger != nil {
			a.Logger.Info(fmt.Sprintf("MCP: %d server(s) connected", len(mcpSessions)))
		}
	}

	return a
}

func (a *AgentLoop) Close() {
	for _, s := range a.mcpClosers {
		if s != nil {
			_ = s.Close()
		}
	}
	a.mcpClosers = nil
	if a.Queue != nil {
		a.Queue.Shutdown()
	}
}

func (a *AgentLoop) SetBus(b *bus.MessageBus) {
	a.Bus = b
}

func (a *AgentLoop) SetCron(c *cron.Service) {
	a.Cron = c
	a.Tools.Register(&tools.CronTool{Service: c})
}

func (a *AgentLoop) ProcessDirect(ctx context.Context, message string, sessionKey string) (string, error) {
	out, _, err := a.Queue.Submit(ctx, message, sessionKey, "", "", nil)
	return out, err
}

func (a *AgentLoop) ProcessDirectWithContext(ctx context.Context, message string, sessionKey string, channel string, chatID string, media []string) (string, bool, error) {
	return a.Queue.Submit(ctx, message, sessionKey, channel, chatID, media)
}

// SpawnAsync starts a background task and returns immediately with runId.
// Used by /subagents spawn and internally. Result is published to Bus when complete.
func (a *AgentLoop) SpawnAsync(ctx context.Context, task, channel, chatID string, b *bus.MessageBus) string {
	if b == nil {
		return "Error: bus not configured"
	}
	parentSession := channel + ":" + chatID

	if a.subAgentSem != nil && !a.subAgentSem.Acquire(ctx) {
		return "Error: max concurrent spawn/subagent tasks reached, try again later"
	}

	runID := tools.GenerateRunID()
	childSessionKey := fmt.Sprintf("spawn:%s:%s", parentSession, runID)

	run := &tools.SpawnRun{
		RunID:           runID,
		ChildSessionKey: childSessionKey,
		ParentSession:   parentSession,
		Task:            task,
		Status:          "running",
		StartedAt:       time.Now(),
	}
	if a.spawnRegistry != nil {
		a.spawnRegistry.Register(run)
	}

	go func() {
		if a.subAgentSem != nil {
			defer a.subAgentSem.Release()
		}
		bgCtx := context.Background()
		if a.Config.Agents.SubAgents.Model != "" {
			bgCtx = tools.WithModelOverride(bgCtx, a.Config.Agents.SubAgents.Model)
		}
		start := time.Now()
		result, _, err := a.ProcessDirectWithContext(bgCtx, task, childSessionKey, channel, chatID, nil)
		elapsed := time.Since(start)

		status := "success"
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
			if err == context.Canceled || strings.Contains(errMsg, "canceled") || strings.Contains(errMsg, "cancelled") {
				status = "cancelled"
			} else if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
				status = "timeout"
			} else {
				status = "error"
			}
			result = "Error: " + errMsg
		}
		if a.spawnRegistry != nil {
			a.spawnRegistry.UpdateStatus(runID, status, result, errMsg)
		}

		content := tools.FormatSpawnAnnounce(status, result, errMsg, elapsed, childSessionKey, runID)
		_ = b.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
	}()

	return fmt.Sprintf("**Spawn started**\nrunId: `%s`\nTask will complete in background. Result will be sent when done.", runID)
}

func (a *AgentLoop) processDirect(ctx context.Context, message string, sessionKey string, channel string, chatID string, media []string) (string, bool, error) {
	if a.Provider == nil {
		return "", false, fmt.Errorf("provider is nil")
	}
	if a.Sessions == nil {
		return "", false, fmt.Errorf("sessions is nil")
	}
	rawMessage := message

	// Log sub-agent workload for multi-agent visibility
	if subMeta := tools.SubAgentFromContext(ctx); subMeta != nil && a.Logger != nil {
		taskPreview := message
		if len(taskPreview) > 100 {
			taskPreview = taskPreview[:100] + "..."
		}
		a.Logger.Info(fmt.Sprintf("[SubAgent] WORKING session=%s depth=%d task=%q", sessionKey, subMeta.Depth, taskPreview))
	}

	ctx = tools.WithChannelContext(ctx, channel, chatID)

	// Derive channel/chatID from sessionKey when empty (e.g. TUI uses "tui:main")
	if channel == "" && chatID == "" && sessionKey != "" {
		if idx := strings.Index(sessionKey, ":"); idx >= 0 {
			channel = sessionKey[:idx]
			chatID = sessionKey[idx+1:]
		} else {
			channel = "session"
			chatID = sessionKey
		}
	}

	// Handle slash commands
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "/") {
		resp, handled, execPrompt := a.handleSlashCommand(ctx, trimmed, sessionKey, channel, chatID)
		if handled && execPrompt == "" {
			return resp, false, nil
		}
		if execPrompt != "" {
			message = execPrompt
		}
	}

	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "", false, err
	}
	if (s.Metadata == nil || s.Metadata["summary"] == nil || s.Metadata["summary"] == "") && len(s.Messages) == 0 {
		if s.Metadata == nil {
			s.Metadata = make(map[string]any)
		}
		summary := rawMessage
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		s.Metadata["summary"] = summary
		_ = a.Sessions.Save(s)
	}
	verbose := sessionVerbose(s, a.Config.Agents.Defaults.VerboseDefault)

	// Resolve workspace from defaults
	ws, err := paths.ExpandUser(a.Config.DefaultWorkspace())
	if err != nil || ws == "" {
		ws, err = paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
		if err != nil {
			return "", false, err
		}
	}
	// Token Budget Scheduler: use compact context for simple tasks to save tokens
	maxMessages := a.Config.Agents.Defaults.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 500
	}
	history := a.Sessions.GetHistoryAligned(s, maxMessages)
	turnMode := false
	if s.Metadata != nil {
		if on, ok := s.Metadata["turn_mode"].(bool); ok && on {
			turnMode = true
		}
	}
	modeSetting := sessionSimpleModeSetting(s)
	useCompact := false
	if modeSetting == simpleModeOn {
		useCompact = true
	} else if modeSetting == simpleModeAuto && a.Config.Agents.Defaults.TokenBudget != nil && a.Config.Agents.Defaults.TokenBudget.Enabled {
		threshold := a.Config.Agents.Defaults.TokenBudget.SimpleThreshold
		if threshold <= 0 {
			threshold = 0.35
		}
		router := routing.NewRouter(routing.RouterConfig{Threshold: threshold})
		_, usedLight, _ := router.SelectModel(message, history, a.Model)
		if usedLight {
			useCompact = true
		}
	}

	if turnMode && modeSetting == simpleModeAuto {
		useCompact = false
	}

	buildOpts := skills.BuildOptions{Compact: useCompact}
	sysPrompt, err := skills.BuildSystemPromptWithOptions(ws, buildOpts)
	if err != nil {
		return "", false, err
	}

	if turnMode {
		turnCtx, err := luck.ReadTurnContext(ws)
		if err != nil {
			return "", false, err
		}
		sysPrompt = strings.TrimSpace(turnCtx)
	}

	if badCtx := luck.BuildBadLuckContext(ws, 5); strings.TrimSpace(badCtx) != "" {
		if strings.TrimSpace(sysPrompt) == "" {
			sysPrompt = strings.TrimSpace(badCtx)
		} else {
			sysPrompt = sysPrompt + "\n\n" + badCtx
		}
	}

	// Add memory context (skip in compact mode)
	if !useCompact && a.Memory != nil {
		memCtx := a.Memory.GetMemoryContext()
		if memCtx != "" {
			sysPrompt = sysPrompt + "\n\n" + memCtx
		}
	}

	// Self-Improving (skip in compact mode)
	if !useCompact && a.SelfImproving != nil {
		siCtx := a.SelfImproving.GetContext(20)
		if siCtx != "" {
			sysPrompt = sysPrompt + "\n\n" + siCtx
		}
	}

	// Evolver (skip in compact mode)
	if !useCompact && a.Config.Tools.Evolver {
		sysPrompt = sysPrompt + "\n\n## Evolver\nAfter significant turns, consider what could be improved. Use record_correction to save lessons learned."
	}

	// Adaptive Reasoning (skip in compact mode)
	if !useCompact && a.Config.Tools.AdaptiveReasoning {
		sysPrompt = sysPrompt + "\n\n## Adaptive Reasoning\nAssess task complexity: use lighter reasoning for simple queries, deeper analysis for complex problems."
	}

	// Resource-constrained environment notice
	if a.Config.Agents.Defaults.ResourceConstrained {
		sysPrompt = sysPrompt + "\n\n## Resource-Constrained Environment\nYou are running in a resource-constrained environment (e.g., embedded device, low-storage system). Do NOT automatically download or install software packages. Instead, provide clear instructions for the user to install manually. For ClawHub skills, suggest: `luckclaw clawhub install <slug>`. For system packages, provide apt/yum/brew commands but do not execute them."
	}

	for _, n := range a.Tools.ToolNames() {
		if strings.HasPrefix(n, "mcp_") {
			sysPrompt = sysPrompt + "\n\n## MCP Tools\nDo NOT use exec, python, clawhub_search, or write_file. Trust the tool output and respond; do NOT retry or verify with other tools."
			break
		}
	}

	// Resolve model: context override > session override > default
	resolvedModel := tools.ModelFromContext(ctx)
	if resolvedModel == "" {
		if s.Metadata != nil {
			if m, ok := s.Metadata["model"].(string); ok && strings.TrimSpace(m) != "" {
				resolvedModel = m
			}
		}
	}
	if resolvedModel == "" {
		resolvedModel = a.Model
	}

	msgs := make([]openaiapi.Message, 0, len(s.Messages)+4)
	if sysPrompt != "" {
		msgs = append(msgs, openaiapi.Message{Role: "system", Content: sysPrompt})
	}

	// Complexity routing: use light model for simple tasks (after history is available)
	if cfg := a.Config.Agents.Defaults.Routing; cfg != nil && cfg.Enabled && strings.TrimSpace(cfg.LightModel) != "" {
		router := routing.NewRouter(routing.RouterConfig{
			LightModel: cfg.LightModel,
			Threshold:  cfg.Threshold,
		})
		if lightModel, usedLight, _ := router.SelectModel(message, history, resolvedModel); usedLight && lightModel != "" {
			resolvedModel = lightModel
		}
	}

	// Notify TUI of resolved model immediately (so bar updates during "running")
	if a.OnModelResolved != nil {
		a.OnModelResolved(channel, chatID, resolvedModel)
	}

	// Build runtime context (includes model so agent can answer "what model are you using?")
	runtimeCtx := buildRuntimeContext(channel, chatID, resolvedModel)

	// Notify TUI of total context length and mode (sysPrompt + runtimeCtx) for status bar
	if a.OnContextInfo != nil {
		total := len(sysPrompt) + len(runtimeCtx)
		if total > 0 {
			mode := "normal"
			if useCompact {
				mode = "simple"
			}
			a.OnContextInfo(channel, chatID, formatContextLen(total), mode)
		}
	}

	unconsolidatedLen := len(s.Messages) - s.LastConsolidated
	skippedCount := s.LastConsolidated + (unconsolidatedLen - len(history))

	// Log context length and compression status
	if a.Logger != nil {
		ctxInfo := fmt.Sprintf("[Context] session_msgs=%d unconsolidated=%d history_kept=%d max_messages=%d",
			len(s.Messages), unconsolidatedLen, len(history), maxMessages)
		if skippedCount > 0 {
			ctxInfo += fmt.Sprintf(" | COMPRESSION: skipped=%d (truncated)", skippedCount)
			if a.Memory != nil && a.Memory.ReadLongTerm() != "" {
				ctxInfo += " [long-term memory note injected]"
			}
		}
		a.Logger.Info(ctxInfo)
	}

	// Inject a recovery note when messages were skipped.
	// Merge into existing system message to avoid consecutive same-role messages;
	// MiniMax API rejects with error 2013 "invalid chat setting" when multiple messages share the same role.
	if skippedCount > 0 {
		var note string
		if a.Memory != nil {
			note = a.Memory.BuildOverflowNote(skippedCount)
		} else {
			note = fmt.Sprintf("[Context: %d earlier messages were truncated due to context length limits.]", skippedCount)
		}
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == "system" {
			last := &msgs[len(msgs)-1]
			if s, ok := last.Content.(string); ok {
				last.Content = s + "\n\n" + note
			} else {
				last.Content = fmt.Sprintf("%v\n\n%s", last.Content, note)
			}
		} else {
			msgs = append(msgs, openaiapi.Message{Role: "system", Content: note})
		}
	}

	// Track tool_call IDs from assistant messages to avoid orphaned tool results
	// (e.g. when history truncation removes the assistant but keeps tool results)
	validToolCallIDs := make(map[string]bool)
	for _, m := range history {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "" {
			continue
		}
		toolCallID, _ := m["tool_call_id"].(string)
		if role == "tool" {
			if validToolCallIDs[toolCallID] {
				msgs = append(msgs, openaiapi.Message{Role: "tool", ToolCallID: toolCallID, Content: content})
			}
			continue
		}
		if content == "" && role != "assistant" {
			continue
		}
		msg := openaiapi.Message{Role: role, Content: content}
		if role == "assistant" {
			if tcs, ok := m["tool_calls"]; ok {
				if tcList, ok := tcs.([]any); ok {
					for _, tc := range tcList {
						if tcMap, ok := tc.(map[string]any); ok {
							call := openaiapi.ToolCall{}
							call.ID, _ = tcMap["id"].(string)
							call.Type = "function"
							if fn, ok := tcMap["function"].(map[string]any); ok {
								call.Function.Name, _ = fn["name"].(string)
								call.Function.Arguments, _ = fn["arguments"].(string)
							}
							msg.ToolCalls = append(msg.ToolCalls, call)
							if call.ID != "" {
								validToolCallIDs[call.ID] = true
							}
						}
					}
				}
			}
		}
		msgs = append(msgs, msg)
	}

	// Merge runtime context into the user message to avoid consecutive
	// same-role messages that some providers reject.
	userContent := message
	if runtimeCtx != "" {
		userContent = runtimeCtx + "\n\n" + message
	}

	// Multimodal: build content as array when media (images) are present
	var userMsg openaiapi.Message
	if len(media) > 0 {
		parts := []map[string]any{{"type": "text", "text": userContent}}
		for _, m := range media {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": m}})
		}
		userMsg = openaiapi.Message{Role: "user", Content: parts}
	} else {
		userMsg = openaiapi.Message{Role: "user", Content: userContent}
	}
	msgs = append(msgs, userMsg)

	historyStart := len(msgs) // index of the first new message in this turn

	if a.Logger != nil {
		a.Logger.Info(fmt.Sprintf("New user message: %s", message))
	}

	// Register message and spawn tools with current channel context
	var msgTool *tools.MessageTool
	if a.Bus != nil {
		msgTool = &tools.MessageTool{
			Bus:            a.Bus,
			DefaultChannel: channel,
			DefaultChatID:  chatID,
		}
		msgTool.StartTurn()
		a.Tools.Register(msgTool)
		spawnTool := &tools.SpawnTool{
			Bus:            a.Bus,
			DefaultChannel: channel,
			DefaultChatID:  chatID,
			RunAgent:       a.ProcessDirect,
			Registry:       a.spawnRegistry,
		}
		if a.subAgentSem != nil {
			spawnTool.Sem = a.subAgentSem
		}
		if a.Config.Agents.SubAgents.Model != "" {
			spawnTool.ModelOverride = a.Config.Agents.SubAgents.Model
		}
		a.Tools.Register(spawnTool)
		// SendFileTool always uses workspace for AllowedDir (files must be within workspace)
		wsForSend, _ := paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
		a.Tools.Register(&tools.SendFileTool{
			Bus:            a.Bus,
			AllowedDir:     wsForSend,
			DefaultChannel: channel,
			DefaultChatID:  chatID,
		})
	}
	// Register subagent tool when enabled and nesting depth allows
	subMeta := tools.SubAgentFromContext(ctx)
	maxDepth := a.Config.Agents.SubAgents.MaxNestingDepth
	if maxDepth <= 0 {
		maxDepth = config.Default().Agents.SubAgents.MaxNestingDepth
	}
	if a.Config.Agents.SubAgents.Enabled && (subMeta == nil || subMeta.Depth < maxDepth) {
		a.Tools.Register(&tools.SubAgentTool{
			RunAgent: a.ProcessDirect,
			Config:   a.Config.Agents.SubAgents,
			Sem:      a.subAgentSem,
			Logger:   a.Logger,
		})
	}

	maxIters := a.Config.Agents.Defaults.MaxToolIterations
	if maxIters <= 0 {
		maxIters = config.Default().Agents.Defaults.MaxToolIterations
	}

	var final string
	hitIterLimit := true
	var toolCallLog []string
	taskTools := make([]luck.ToolTrace, 0, 8)
	var totalUsage struct {
		PromptTokens     int
		CompletionTokens int
		TotalTokens      int
	}
	var streamed bool

	useStreaming := a.blockStreamingEnabled(channel, chatID)
	chunkCfg := a.Config.Agents.Defaults.BlockStreamingChunk
	breakMode := strings.ToLower(a.Config.Agents.Defaults.BlockStreamingBreak)
	if breakMode == "" {
		breakMode = "text_end"
	}

	for i := 0; i < maxIters; i++ {
		if a.Logger != nil {
			a.Logger.Info(fmt.Sprintf("Iteration %d/%d: Calling LLM model=%s (context: %d messages)", i+1, maxIters, resolvedModel, len(msgs)))
		}

		sanitized := openaiapi.SanitizeEmptyContent(msgs)
		toolDefs := a.Tools.Definitions()
		if subMeta != nil && a.Config.Agents.SubAgents.Inherit.Tools {
			allowed := a.Config.Agents.SubAgents.ToolPolicy.Allowed
			disabled := a.Config.Agents.SubAgents.ToolPolicy.Disabled
			toolDefs = a.Tools.DefinitionsFiltered(allowed, disabled)
		}
		model := a.Config.ModelIDForAPI(resolvedModel)
		req := openaiapi.ChatRequest{
			Model:           model,
			Messages:        sanitized,
			Tools:           toolDefs,
			ToolChoice:      "auto",
			Temperature:     a.Config.Agents.Defaults.Temperature,
			MaxTokens:       a.Config.Agents.Defaults.MaxTokens,
			ReasoningEffort: a.Config.Agents.Defaults.ReasoningEffort,
		}

		var res openaiapi.ChatResult
		var err error
		type earlyToolResult struct {
			result string
			err    error
		}
		earlyResults := make(map[string]chan earlyToolResult)
		var earlyMu sync.Mutex
		var buf strings.Builder
		var chunker *BlockChunker
		var cb openaiapi.StreamCallbacks
		var publishChunk func(string)
		if useStreaming {
			chunker = &BlockChunker{
				MinChars:        80,
				MaxChars:        600,
				BreakPreference: BreakNewline,
			}
			if chunkCfg != nil {
				if chunkCfg.MinChars > 0 {
					chunker.MinChars = chunkCfg.MinChars
				}
				if chunkCfg.MaxChars > 0 {
					chunker.MaxChars = chunkCfg.MaxChars
				}
				if chunkCfg.BreakPreference != "" {
					chunker.BreakPreference = chunkCfg.BreakPreference
				}
			}
			publishChunk = func(chunk string) {
				if chunk == "" {
					return
				}
				_ = a.Bus.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: channel,
					ChatID:  chatID,
					Content: chunk,
				})
			}
			onDelta := func(delta string) {
				buf.WriteString(delta)
				if breakMode == "text_end" {
					rest := chunker.Emit(buf.String(), publishChunk)
					buf.Reset()
					buf.WriteString(rest)
				}
			}
			cb = openaiapi.StreamCallbacks{OnDelta: onDelta}
			// OpenCode-style: execute tools as soon as parsed from stream
			if a.Config.Agents.Defaults.StreamingToolExecution {
				cb.OnToolCall = func(tc openaiapi.ToolCall) {
					ch := make(chan earlyToolResult, 1)
					earlyMu.Lock()
					earlyResults[tc.ID] = ch
					earlyMu.Unlock()
					go func() {
						r, e := a.Tools.ExecuteJSON(ctx, tc.Function.Name, tc.Function.Arguments)
						ch <- earlyToolResult{result: r, err: e}
						close(ch)
					}()
				}
			}
			res, err = a.chatWithRetryStream(ctx, req, cb)
			if err == nil {
				chunker.Flush(buf.String(), publishChunk)
			}
		} else {
			res, err = a.chatWithRetry(ctx, req)
		}

		if err != nil {
			if a.Logger != nil {
				a.Logger.Error(fmt.Sprintf("LLM error: %v", err))
			}
			return "", false, err
		}

		totalUsage.PromptTokens += res.Usage.PromptTokens
		totalUsage.CompletionTokens += res.Usage.CompletionTokens
		totalUsage.TotalTokens += res.Usage.TotalTokens

		if a.Logger != nil {
			a.Logger.Info(fmt.Sprintf("[Token] iter=%d prompt=%d completion=%d total=%d (cumulative: %d)",
				i+1, res.Usage.PromptTokens, res.Usage.CompletionTokens, res.Usage.TotalTokens, totalUsage.TotalTokens))
		}

	executeToolCalls:
		if len(res.ToolCalls) > 0 {
			msgs = append(msgs, openaiapi.Message{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls})

			// Emit "running" progress before executing tools (only when verbose)
			toolStatuses := make([]bus.ToolCallStatus, len(res.ToolCalls))
			for j, tc := range res.ToolCalls {
				toolStatuses[j] = bus.ToolCallStatus{
					Name:   tc.Function.Name,
					Args:   toolArgsSummary(tc.Function.Name, tc.Function.Arguments),
					Status: "running",
				}
			}
			var progressMsgID string
			if verbose {
				progressMsgID = a.emitToolProgress(ctx, channel, chatID, i+1, maxIters, toolStatuses)
			}

			// Execute tools: parallel when enabled, else sequential
			results := make([]string, len(res.ToolCalls))
			execErrs := make([]error, len(res.ToolCalls))
			elapsedMs := make([]int64, len(res.ToolCalls))

			if a.Config.Agents.Defaults.ParallelToolExecution {
				var wg sync.WaitGroup
				for j, tc := range res.ToolCalls {
					wg.Add(1)
					go func(j int, tc openaiapi.ToolCall) {
						defer wg.Done()
						start := time.Now()
						earlyMu.Lock()
						ch := earlyResults[tc.ID]
						earlyMu.Unlock()
						if ch != nil {
							tr := <-ch
							results[j] = tr.result
							execErrs[j] = tr.err
							earlyMu.Lock()
							delete(earlyResults, tc.ID)
							earlyMu.Unlock()
						} else {
							results[j], execErrs[j] = a.Tools.ExecuteJSON(ctx, tc.Function.Name, tc.Function.Arguments)
						}
						elapsedMs[j] = time.Since(start).Milliseconds()
					}(j, tc)
				}
				wg.Wait()
			}

			for j, tc := range res.ToolCalls {
				if a.Logger != nil {
					prefix := ""
					if subMeta != nil {
						prefix = fmt.Sprintf("[SubAgent] session=%s ", sessionKey)
					}
					a.Logger.Info(fmt.Sprintf("%sTool call: %s (args: %s)", prefix, tc.Function.Name, toolArgsSummary(tc.Function.Name, tc.Function.Arguments)))
				}

				var result string
				var execErr error
				if a.Config.Agents.Defaults.ParallelToolExecution {
					result = results[j]
					execErr = execErrs[j]
				} else {
					start := time.Now()
					earlyMu.Lock()
					ch := earlyResults[tc.ID]
					earlyMu.Unlock()
					if ch != nil {
						tr := <-ch
						result = tr.result
						execErr = tr.err
						earlyMu.Lock()
						delete(earlyResults, tc.ID)
						earlyMu.Unlock()
					} else {
						result, execErr = a.Tools.ExecuteJSON(ctx, tc.Function.Name, tc.Function.Arguments)
					}
					elapsedMs[j] = time.Since(start).Milliseconds()
				}

				toolStatuses[j].DurationMs = elapsedMs[j]
				if a.Logger != nil && elapsedMs[j] > 1000 {
					a.Logger.Info(fmt.Sprintf("Tool %s took %.1fs (may explain pause)", tc.Function.Name, float64(elapsedMs[j])/1000))
				}
				if execErr != nil {
					if a.Logger != nil {
						a.Logger.Error(fmt.Sprintf("Tool error: %v", execErr))
					}
					if strings.TrimSpace(result) == "" {
						result = fmt.Sprintf("Error: %v", execErr)
					}
					toolStatuses[j].Status = "error"
					toolStatuses[j].Error = execErr.Error()
					if a.SelfImproving != nil {
						_ = a.SelfImproving.RecordError(tc.Function.Name, tc.Function.Arguments, execErr.Error())
					}
				} else {
					toolStatuses[j].Status = "done"
				}

				if a.Logger != nil {
					a.Logger.Info(fmt.Sprintf("Tool result: %s", result))
				}

				if verbose {
					resultPreview := result
					if len(resultPreview) > 200 {
						resultPreview = resultPreview[:200] + "..."
					}
					toolCallLog = append(toolCallLog, fmt.Sprintf("• %s(%s) → %s", tc.Function.Name, toolArgsSummary(tc.Function.Name, tc.Function.Arguments), resultPreview))
				}

				traceResult := result
				if len(traceResult) > toolResultMaxChars {
					traceResult = traceResult[:toolResultMaxChars] + "\n... (truncated)"
				}
				taskTools = append(taskTools, luck.ToolTrace{
					Name:       tc.Function.Name,
					Args:       toolArgsSummary(tc.Function.Name, tc.Function.Arguments),
					RawArgs:    tc.Function.Arguments,
					Result:     traceResult,
					DurationMs: elapsedMs[j],
					Status:     toolStatuses[j].Status,
					Error:      toolStatuses[j].Error,
				})

				msgs = append(msgs, openaiapi.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
			}

			// Emit completed progress (only when verbose)
			if verbose {
				a.emitToolProgressUpdate(ctx, channel, chatID, i+1, maxIters, toolStatuses, progressMsgID)
			}
			continue
		}

		final = res.Content
		hitIterLimit = false // Not exiting due to the iteration limit reached
		streamed = useStreaming

		// Retry if an empty response is encountered
		const maxEmptyRetries = 2
		for emptyRetries := 0; strings.TrimSpace(final) == "" && emptyRetries < maxEmptyRetries; emptyRetries++ {
			if a.Logger != nil {
				a.Logger.Info(fmt.Sprintf("Model returned empty response, retrying (%d/%d)...", emptyRetries+1, maxEmptyRetries))
			}
			req.Messages = openaiapi.SanitizeEmptyContent(msgs)
			if useStreaming {
				buf.Reset()
				res, err = a.chatWithRetryStream(ctx, req, cb)
				if err == nil {
					chunker.Flush(buf.String(), publishChunk)
				}
			} else {
				res, err = a.chatWithRetry(ctx, req)
			}
			if err != nil {
				if a.Logger != nil {
					a.Logger.Error(fmt.Sprintf("LLM retry error: %v", err))
				}
				break
			}
			totalUsage.PromptTokens += res.Usage.PromptTokens
			totalUsage.CompletionTokens += res.Usage.CompletionTokens
			totalUsage.TotalTokens += res.Usage.TotalTokens
			final = res.Content
			streamed = useStreaming
			if len(res.ToolCalls) > 0 {
				goto executeToolCalls
			}
		}

		if a.Logger != nil {
			a.Logger.Info(fmt.Sprintf("Final response: %s", final))
		}

		// Don't persist error responses to session — they can poison context
		// and cause permanent 400 loops (#1303).
		if res.FinishReason != "error" {
			msgs = append(msgs, openaiapi.Message{Role: "assistant", Content: final})
		} else {
			if a.Logger != nil {
				a.Logger.Error(fmt.Sprintf("LLM returned error: %.200s", final))
			}
			if final == "" {
				final = "Sorry, I encountered an error calling the AI model."
			}
		}
		break
	}

	if final == "" {
		if hitIterLimit {
			final = "I've reached the maximum number of tool iterations. The task may be partially complete."
		} else {
			final = "The model returned an empty response. The task may be partially complete."
		}
		msgs = append(msgs, openaiapi.Message{Role: "assistant", Content: final})
	}

	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}
	trace := luck.TaskTrace{
		FinishedAt:  time.Now().Format(time.RFC3339Nano),
		SessionKey:  sessionKey,
		Channel:     channel,
		ChatID:      chatID,
		RequestRaw:  rawMessage,
		RequestExec: message,
		Response:    final,
		Tools:       taskTools,
	}
	s.Metadata["last_task_trace"] = trace
	s.Metadata["last_task_trace_fp"] = trace.Fingerprint()

	// Save all new-turn messages into session with filtering
	a.saveTurn(s, msgs, historyStart)

	if err := a.Sessions.Save(s); err != nil {
		return "", false, err
	}

	// Prepend tool call log when verbose; skip if we already sent step-by-step progress to the channel
	if verbose && len(toolCallLog) > 0 && channel == "" {
		final = "--- Tool calls ---\n" + strings.Join(toolCallLog, "\n") + "\n\n" + final
	}

	// If message tool sent to same channel/chat, suppress the final reply
	if msgTool != nil && msgTool.SentInTurn() {
		return "", false, nil
	}

	// Trigger memory consolidation if needed
	a.maybeConsolidate(ctx, s)

	if a.Logger != nil {
		a.Logger.Info(fmt.Sprintf("[Token] TURN TOTAL: prompt=%d completion=%d total=%d | context_compression_triggered=%v",
			totalUsage.PromptTokens, totalUsage.CompletionTokens, totalUsage.TotalTokens, skippedCount > 0))
	}
	if a.OnTurnComplete != nil {
		a.OnTurnComplete(channel, chatID, resolvedModel, totalUsage.PromptTokens, totalUsage.CompletionTokens, totalUsage.TotalTokens)
	}

	return final, streamed, nil
}

func (a *AgentLoop) handleSlashCommand(ctx context.Context, cmd string, sessionKey string, channel string, chatID string) (string, bool, string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", false, ""
	}

	cmdName := strings.ToLower(parts[0])

	switch cmdName {
	case "/help":
		return a.buildHelpMessage(channel), true, ""

	case "/new":
		s, err := a.Sessions.GetOrCreate(sessionKey)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if a.Memory != nil && len(s.Messages) > 0 {
			lock := a.consolidationLockFor(sessionKey)
			lock.Lock()
			defer lock.Unlock()
			timeout := a.consolidationTimeout()
			ok, err := a.Memory.ConsolidateWithTimeout(ctx, s, a.Provider, a.Config.ModelIDForAPI(a.Model), true, a.Config.Agents.Defaults.MemoryWindow, timeout)
			if err != nil && a.Logger != nil {
				a.Logger.Error(fmt.Sprintf("Memory consolidation failed: %v", err))
			}
			if ok {
				_ = a.Sessions.Save(s)
			}
		}
		a.Sessions.ClearSession(s)
		_ = a.Sessions.Save(s)
		a.Sessions.Invalidate(sessionKey)
		a.removeConsolidationLock(sessionKey)
		return "Conversation cleared. Memory has been consolidated.", true, ""

	case "/reset":
		s, err := a.Sessions.GetOrCreate(sessionKey)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		a.Sessions.ClearSession(s)
		_ = a.Sessions.Save(s)
		a.Sessions.Invalidate(sessionKey)
		a.removeConsolidationLock(sessionKey)
		return "Conversation cleared.", true, ""

	case "/verbose":
		s, err := a.Sessions.GetOrCreate(sessionKey)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if s.Metadata == nil {
			s.Metadata = map[string]any{}
		}
		cur, _ := s.Metadata["verbose"].(bool)
		if len(parts) >= 2 {
			switch strings.ToLower(parts[1]) {
			case "on", "1", "true", "yes":
				s.Metadata["verbose"] = true
			case "off", "0", "false", "no":
				s.Metadata["verbose"] = false
			default:
				s.Metadata["verbose"] = !cur
			}
		} else {
			s.Metadata["verbose"] = !cur
		}
		_ = a.Sessions.Save(s)
		if s.Metadata["verbose"].(bool) {
			return "Verbose mode ON. Tool calls will be shown in output.", true, ""
		}
		return "Verbose mode OFF.", true, ""

	case "/summary":
		s, err := a.Sessions.GetOrCreate(sessionKey)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if len(s.Messages) == 0 {
			return "No conversation to summarize.", true, ""
		}
		if a.Provider == nil {
			return "Error: provider not configured", true, ""
		}
		var lines []string
		for _, m := range s.Messages {
			role, _ := m["role"].(string)
			content, _ := m["content"].(string)
			if content == "" {
				continue
			}
			// Skip or truncate tool results to avoid token overflow
			if role == "tool" {
				if len(content) > 100 {
					content = content[:100] + "..."
				}
			} else if len(content) > 500 {
				content = content[:500] + "..."
			}
			ts, _ := m["timestamp"].(string)
			if len(ts) > 16 {
				ts = ts[:16]
			}
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", ts, strings.ToUpper(role), content))
		}
		prompt := "Summarize this conversation in 2-5 sentences. Focus on key topics, decisions, and outcomes.\n\n" + strings.Join(lines, "\n")
		res, err := a.chatWithRetry(ctx, openaiapi.ChatRequest{
			Model:       a.Config.ModelIDForAPI(a.Model),
			Messages:    []openaiapi.Message{{Role: "user", Content: prompt}},
			MaxTokens:   500,
			Temperature: 0.3,
		})
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		return "**Summary:**\n\n" + res.Content, true, ""

	case "/simple":
		return a.handleSimpleCommand(ctx, sessionKey, channel, chatID, parts[1:])

	case "/luck":
		return a.handleLuckCommand(ctx, sessionKey, parts[1:])

	case "/badluck":
		return a.handleBadLuckCommand(ctx, sessionKey, parts[1:])

	case "/turn":
		return a.handleTurnCommand(ctx, sessionKey, parts[1:])

	case "/stop":
		return "Processing stopped.", true, ""

	case "/heartbeat":
		if a.Cron == nil {
			return "Heartbeat is only available in gateway mode. Start luckclaw with gateway to use heartbeat.", true, ""
		}
		return "Heartbeat runs automatically in gateway mode. Configure gateway.heartbeatChannel and gateway.heartbeatChatID to receive results.", true, ""

	case "/plan":
		task := strings.TrimSpace(strings.Join(parts[1:], " "))
		if task == "" {
			return "Usage: /plan <task>\n\nExample: /plan Create 3 empty files a.txt, b.txt, c.txt in workspace", true, ""
		}
		execPrompt := planModePrompt + "\n\n" + task
		return "", false, execPrompt

	case "/model":
		return a.handleModelCommand(ctx, sessionKey, parts[1:])

	case "/models":
		return a.handleModelsListCommand(), true, ""

	case "/skill":
		return a.handleSkillCommand(ctx, sessionKey, parts[1:])

	case "/subagents":
		if handled, content := a.handleSubagentsCmd(ctx, cmd, sessionKey, channel, chatID, a.Bus); handled {
			return content, true, ""
		}
		return "", false, ""

	case "/mcp":
		return a.handleMCPCommand(), true, ""

	default:
		// Check custom slash commands from config
		if a.Config.SlashCommands != nil {
			cmdAlt := strings.TrimPrefix(cmdName, "/")
			for _, key := range []string{cmdName, cmdAlt, "/" + cmdAlt} {
				if cfg, ok := a.Config.SlashCommands[key]; ok {
					return a.runCustomSlashCommand(ctx, sessionKey, cmdName, parts[1:], cfg)
				}
			}
		}
		return "", false, ""
	}
}

func (a *AgentLoop) handleModelCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	_ = ctx
	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}

	// /model — show current
	if len(args) == 0 {
		current, _ := s.Metadata["model"].(string)
		if current == "" {
			current = a.Model
		}
		return fmt.Sprintf("**Current model:** %s\n(Use `/model <modelId>` to switch; `/new` resets to default.)", current), true, ""
	}

	// /model <id> — set session override
	target := strings.TrimSpace(strings.Join(args, " "))
	if target == "" {
		return "Usage: /model <modelId>", true, ""
	}
	selected := a.Config.SelectProvider(target)
	if selected == nil || selected.APIKey == "" {
		return fmt.Sprintf("Error: No provider configured for model %q. Use /models to see available models.", target), true, ""
	}
	s.Metadata["model"] = target
	_ = a.Sessions.Save(s)
	return fmt.Sprintf("Switched to **%s** for this session.", target), true, ""
}

func (a *AgentLoop) handleLuckCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	_ = ctx
	ws, err := paths.ExpandUser(a.Config.DefaultWorkspace())
	if err != nil || strings.TrimSpace(ws) == "" {
		ws, err = paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
	}

	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}

	if len(args) > 0 && strings.ToLower(strings.TrimSpace(args[0])) == "list" {
		items, err := luck.ListLuckyEvents(ws)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if len(items) == 0 {
			return "No lucky events found.", true, ""
		}
		var b strings.Builder
		b.WriteString("**Lucky events:**\n")
		for _, it := range items {
			line := "- " + it.Title
			if strings.TrimSpace(it.CreatedAt) != "" {
				line += " (" + it.CreatedAt + ")"
			}
			if strings.TrimSpace(it.ID) != "" {
				line += " id=" + it.ID
			}
			b.WriteString(line + "\n")
		}
		return b.String(), true, ""
	}

	traceAny := s.Metadata["last_task_trace"]
	trace, err := luck.DecodeTaskTrace(traceAny)
	if err != nil {
		return "No completed task found to record. Run a task first, then send /luck.", true, ""
	}

	if len(args) > 0 && strings.ToLower(strings.TrimSpace(args[0])) == "last" {
		var b strings.Builder
		b.WriteString("**Last completed task (preview):**\n")
		if strings.TrimSpace(trace.FinishedAt) != "" {
			b.WriteString("- finishedAt: " + strings.TrimSpace(trace.FinishedAt) + "\n")
		}
		if strings.TrimSpace(trace.RequestRaw) != "" {
			b.WriteString("- request: " + strings.TrimSpace(trace.DefaultTitle()) + "\n")
		}
		modified := luck.ModifiedFiles(trace.Tools)
		if len(modified) > 0 {
			b.WriteString("- modifiedFiles:\n")
			for _, p := range modified {
				b.WriteString("  - " + p + "\n")
			}
		} else {
			b.WriteString("- modifiedFiles: (none)\n")
		}
		if len(trace.Tools) > 0 {
			b.WriteString("- tools:\n")
			for _, t := range trace.Tools {
				line := "  - " + strings.TrimSpace(t.Name)
				if strings.TrimSpace(t.Args) != "" {
					line += "(" + strings.TrimSpace(t.Args) + ")"
				}
				if strings.TrimSpace(t.Status) != "" {
					line += " [" + strings.TrimSpace(t.Status) + "]"
				}
				b.WriteString(line + "\n")
			}
		}
		b.WriteString("\nUse `/luck` (or `/luck <title>`) to record this into LUCK.md.")
		return b.String(), true, ""
	}

	fp := trace.Fingerprint()
	if prevFP, ok := s.Metadata["last_luck_fp"].(string); ok && prevFP != "" && prevFP == fp {
		if prevID, ok := s.Metadata["last_luck_id"].(string); ok && prevID != "" {
			return luck.LuckHitBanner() + fmt.Sprintf("🎉 Lucky event already recorded (id=%s). Use `/luck list` to view.", prevID), true, ""
		}
		return luck.LuckHitBanner() + "🎉 Lucky event already recorded. Use `/luck list` to view.", true, ""
	}

	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		title = trace.DefaultTitle()
	}

	ev := luck.LuckyEvent{
		ID:          luck.NewEventID(fp),
		CreatedAt:   time.Now(),
		Title:       luck.FormatLuckyTitle(title),
		Fingerprint: fp,
		Trace:       *trace,
	}
	if err := luck.AppendLuckyEvent(ws, ev); err != nil {
		return "Error: " + err.Error(), true, ""
	}

	s.Metadata["last_luck_fp"] = fp
	s.Metadata["last_luck_id"] = ev.ID
	_ = a.Sessions.Save(s)

	if a.SelfImproving != nil {
		_ = a.SelfImproving.RecordCorrection(luck.PreferenceNoteFromLuck(ev.Title, ev.Trace))
	}

	return luck.LuckHitBanner() + fmt.Sprintf("🎉 Lucky event recorded (id=%s). Use `/luck list` to view.", ev.ID), true, ""
}

func (a *AgentLoop) handleBadLuckCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	_ = ctx
	ws, err := paths.ExpandUser(a.Config.DefaultWorkspace())
	if err != nil || strings.TrimSpace(ws) == "" {
		ws, err = paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
	}

	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}

	if len(args) > 0 && strings.ToLower(strings.TrimSpace(args[0])) == "list" {
		items, err := luck.ListBadLuckEvents(ws)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		if len(items) == 0 {
			return "No bad luck events found.", true, ""
		}
		var b strings.Builder
		b.WriteString("**Bad luck events:**\n")
		for _, it := range items {
			line := "- " + it.Title
			if strings.TrimSpace(it.CreatedAt) != "" {
				line += " (" + it.CreatedAt + ")"
			}
			if strings.TrimSpace(it.ID) != "" {
				line += " id=" + it.ID
			}
			if strings.TrimSpace(it.Avoid) != "" {
				line += " avoid=" + it.Avoid
			}
			b.WriteString(line + "\n")
		}
		return b.String(), true, ""
	}

	traceAny := s.Metadata["last_task_trace"]
	trace, err := luck.DecodeTaskTrace(traceAny)
	if err != nil {
		return "No completed task found to record. Run a task first, then send /badluck.", true, ""
	}

	if len(args) > 0 && strings.ToLower(strings.TrimSpace(args[0])) == "last" {
		var b strings.Builder
		b.WriteString("**Last completed task (preview):**\n")
		if strings.TrimSpace(trace.FinishedAt) != "" {
			b.WriteString("- finishedAt: " + strings.TrimSpace(trace.FinishedAt) + "\n")
		}
		if strings.TrimSpace(trace.RequestRaw) != "" {
			b.WriteString("- request: " + strings.TrimSpace(trace.DefaultTitle()) + "\n")
		}
		modified := luck.ModifiedFiles(trace.Tools)
		if len(modified) > 0 {
			b.WriteString("- modifiedFiles:\n")
			for _, p := range modified {
				b.WriteString("  - " + p + "\n")
			}
		} else {
			b.WriteString("- modifiedFiles: (none)\n")
		}
		b.WriteString("\nUse `/badluck <what went wrong / what to avoid>` to record this into BADLUCK.md.")
		return b.String(), true, ""
	}

	fp := trace.Fingerprint()
	if prevFP, ok := s.Metadata["last_badluck_fp"].(string); ok && prevFP != "" && prevFP == fp {
		if prevID, ok := s.Metadata["last_badluck_id"].(string); ok && prevID != "" {
			return luck.BadLuckBanner() + fmt.Sprintf("😵 Bad luck already recorded (id=%s). Use `/badluck list` to view.", prevID), true, ""
		}
		return luck.BadLuckBanner() + "😵 Bad luck already recorded. Use `/badluck list` to view.", true, ""
	}

	avoid := strings.TrimSpace(strings.Join(args, " "))
	title := trace.DefaultTitle()
	ev := luck.BadLuckEvent{
		ID:          luck.NewBadLuckID(fp),
		CreatedAt:   time.Now(),
		Title:       luck.FormatBadLuckTitle(title),
		Fingerprint: fp,
		Avoid:       avoid,
		Trace:       *trace,
	}
	if err := luck.AppendBadLuckEvent(ws, ev); err != nil {
		return "Error: " + err.Error(), true, ""
	}

	s.Metadata["last_badluck_fp"] = fp
	s.Metadata["last_badluck_id"] = ev.ID
	_ = a.Sessions.Save(s)

	if a.SelfImproving != nil {
		_ = a.SelfImproving.RecordCorrection(luck.PreferenceNoteFromBadLuck(ev.Title, avoid, ev.Trace))
	}

	if avoid == "" {
		return luck.BadLuckBanner() + fmt.Sprintf("😵 Bad luck recorded (id=%s). Tip: `/badluck <what to avoid next time>` adds a note.", ev.ID), true, ""
	}
	return luck.BadLuckBanner() + fmt.Sprintf("😵 Bad luck recorded (id=%s). Use `/badluck list` to view.", ev.ID), true, ""
}

func (a *AgentLoop) handleTurnCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	_ = ctx
	ws, err := paths.ExpandUser(a.Config.DefaultWorkspace())
	if err != nil || strings.TrimSpace(ws) == "" {
		ws, err = paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
	}

	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}

	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch sub {
	case "status":
		on, _ := s.Metadata["turn_mode"].(bool)
		profile, _ := s.Metadata["turn_profile"].(string)
		hasTmp := luck.HasAnyTmp(ws)
		mode := "OFF"
		if on {
			mode = "ON"
		}
		msg := fmt.Sprintf("**Turn mode:** %s\n**Tmp files present:** %v", mode, hasTmp)
		if strings.TrimSpace(profile) != "" {
			msg += "\n**Profile:** " + profile
		}
		if on && !hasTmp {
			msg += "\n\nTmp files are missing. Use `/turn reroll` to generate them."
		}
		return msg, true, ""

	case "off":
		s.Metadata["turn_mode"] = false
		_ = a.Sessions.Save(s)
		return "Turn mode OFF.", true, ""

	case "on":
		var generated *luck.Profile
		if !luck.HasAnyTmp(ws) {
			p := luck.GenerateProfile()
			if _, err := luck.WriteTmpFiles(ws, p); err != nil {
				return "Error: " + err.Error(), true, ""
			}
			s.Metadata["turn_profile"] = p.Name
			generated = &p
		}
		s.Metadata["turn_mode"] = true
		_ = a.Sessions.Save(s)
		profile, _ := s.Metadata["turn_profile"].(string)
		if strings.TrimSpace(profile) == "" {
			profile = "Custom"
		}
		var b strings.Builder
		b.WriteString(luck.TurnShiftBanner())
		b.WriteString("**Turn mode ON.**\n")
		b.WriteString("**Profile:** " + profile + "\n")
		if generated != nil && len(generated.Summary) > 0 {
			b.WriteString("\n**How this perspective changes the approach:**\n")
			for _, s := range generated.Summary {
				b.WriteString("- " + strings.TrimSpace(s) + "\n")
			}
		} else {
			b.WriteString("\nUsing existing tmp files.\n")
		}
		b.WriteString("Tip: `/turn save` makes this permanent (overwrites IDENTITY.md/SOUL.md/USER.md).")
		return b.String(), true, ""

	case "clear":
		if err := luck.DeleteTmpFiles(ws); err != nil {
			return "Error: " + err.Error(), true, ""
		}
		s.Metadata["turn_mode"] = false
		delete(s.Metadata, "turn_profile")
		_ = a.Sessions.Save(s)
		return "Turn mode cleared and tmp files removed.", true, ""

	case "save":
		written, err := luck.SaveTmpAsBase(ws)
		if err != nil {
			return "Error: " + err.Error(), true, ""
		}
		_ = luck.DeleteTmpFiles(ws)
		s.Metadata["turn_mode"] = false
		_ = a.Sessions.Save(s)
		var b strings.Builder
		b.WriteString("Turn mode saved into base identity files:\n")
		for _, p := range written {
			b.WriteString("- " + filepath.Base(p) + "\n")
		}
		b.WriteString("\nTmp files removed. Turn mode OFF.\n")
		b.WriteString("This affects future runs that use the same workspace.")
		return b.String(), true, ""

	case "", "reroll", "roll", "random":
		p := luck.GenerateProfile()
		if _, err := luck.WriteTmpFiles(ws, p); err != nil {
			return "Error: " + err.Error(), true, ""
		}
		s.Metadata["turn_mode"] = true
		s.Metadata["turn_profile"] = p.Name
		_ = a.Sessions.Save(s)
		var b strings.Builder
		b.WriteString(luck.TurnShiftBanner())
		b.WriteString("**Turn mode rerolled.**\n")
		b.WriteString("**Profile:** " + p.Name + "\n")
		if len(p.Summary) > 0 {
			b.WriteString("\n**How this perspective changes the approach:**\n")
			for _, s := range p.Summary {
				b.WriteString("- " + strings.TrimSpace(s) + "\n")
			}
		}
		b.WriteString("Use `/turn status` to inspect, `/turn off` to disable, `/turn save` to make permanent.")
		return b.String(), true, ""
	}

	return "Usage: /turn [reroll|status|on|off|save|clear]\nTip: /turn reroll generates IDENTITY.md.tmp/SOUL.md.tmp/USER.md.tmp and enables the mode.", true, ""
}

func (a *AgentLoop) handleSkillCommand(ctx context.Context, sessionKey string, args []string) (string, bool, string) {
	ws, err := paths.ExpandUser(a.Config.Agents.Defaults.Workspace)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	skillList, err := skills.Discover(ws)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if len(args) == 0 {
		// /skill — list available skills
		if len(skillList) == 0 {
			return "No skills found. Create workspace/skills/<name>/SKILL.md or install from ClawHub (luckclaw clawhub install <slug>).", true, ""
		}
		var b strings.Builder
		b.WriteString("**Available skills:**\n")
		for _, s := range skillList {
			state := "available"
			if !s.Available {
				state = "unavailable"
			}
			b.WriteString(fmt.Sprintf("  - %s (%s)\n", s.Name, state))
		}
		b.WriteString("\nUse `/skill <name>` to run a skill (e.g. `/skill weather`).")
		return b.String(), true, ""
	}
	// /skill <name> — run the named skill
	name := strings.TrimSpace(strings.ToLower(args[0]))
	for _, s := range skillList {
		if strings.ToLower(s.Name) == name {
			if !s.Available {
				return fmt.Sprintf("Skill %q is unavailable (missing deps). Use read_file to check %s for requirements.", s.Name, s.Path), true, ""
			}
			execPrompt := fmt.Sprintf("Please run the %s skill. User requested: /skill %s", s.Name, s.Name)
			if len(args) > 1 {
				execPrompt += "\n\n[User additional input: " + strings.Join(args[1:], " ") + "]"
			}
			return "", false, execPrompt
		}
	}
	return fmt.Sprintf("Skill %q not found. Use `/skill` to list available skills.", name), true, ""
}

func (a *AgentLoop) mcpDiagnostic() string {
	cfgPath, _ := paths.ConfigPath()
	servers := a.Config.Tools.MCPServers
	var b strings.Builder
	b.WriteString("**No MCP tools connected.**\n\n")
	b.WriteString(fmt.Sprintf("Config: %s\n", cfgPath))
	b.WriteString(fmt.Sprintf("tools.mcpServers: %d configured\n", len(servers)))
	if len(servers) > 0 {
		for name, scfg := range servers {
			hasCmd := strings.TrimSpace(scfg.Command) != ""
			hasURL := strings.TrimSpace(scfg.URL) != ""
			typ := strings.TrimSpace(strings.ToLower(scfg.Type))
			b.WriteString(fmt.Sprintf("  - %q: type=%q command=%v url=%v\n", name, typ, hasCmd, hasURL))
		}
		if a.mcpErr != "" {
			b.WriteString("\n**Connect error:** " + a.mcpErr + "\n")
		} else {
			b.WriteString("\nServers have no valid command/url; add command (stdio) or url (sse/streamablehttp).\n")
		}
	} else {
		b.WriteString("\nAdd mcpServers under tools in config. Example (addition server):\n")
		b.WriteString("```json\n\"tools\": {\n  \"mcpServers\": {\n    \"addition\": {\n      \"type\": \"stdio\",\n      \"command\": \"node\",\n      \"args\": [\"/path/to/mcp_server/dist/index.js\", \"stdio\"]\n    }\n  }\n}\n```\n")
	}
	return b.String()
}

func (a *AgentLoop) handleMCPCommand() string {
	mcpTools := a.Tools.MCPToolNames()
	if len(mcpTools) == 0 {
		return a.mcpDiagnostic()
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**MCP tools (%d):**\n", len(mcpTools)))
	for _, n := range mcpTools {
		t := a.Tools.Get(n)
		desc := ""
		if t != nil {
			desc = t.Description()
		}
		b.WriteString(fmt.Sprintf("  - %s: %s\n", n, desc))
	}
	b.WriteString("\nUse tool name with JSON args, not exec.")
	return b.String()
}

func (a *AgentLoop) handleModelsListCommand() string {
	result := a.Config.ListAvailableModels()
	if len(result.FetchErrors) > 0 {
		var b strings.Builder
		b.WriteString("**API fetch failed (no fallback):**\n")
		for _, e := range result.FetchErrors {
			b.WriteString("  • " + e + "\n")
		}
		b.WriteString("\nCheck apiBase and API key in config. ")
		if len(result.Models) > 0 {
			b.WriteString("Some providers succeeded:\n")
			for _, m := range result.Models {
				b.WriteString("  • " + m + "\n")
			}
		}
		b.WriteString("\nUse `/model <modelId>` to switch for this session.")
		return b.String()
	}
	if len(result.Models) == 0 {
		return "No models available. Configure API keys and apiBase in ~/.luckclaw/config.json."
	}
	var b strings.Builder
	b.WriteString("**Available models:**\n")
	for _, m := range result.Models {
		b.WriteString("  • " + m + "\n")
	}
	b.WriteString("\nUse `/model <modelId>` to switch for this session.")
	return b.String()
}

func (a *AgentLoop) buildHelpMessage(channel string) string {
	lines := []string{
		"Available commands:",
		"  /new     - Start a new conversation (consolidates memory first)",
		"  /reset   - Clear conversation without memory consolidation",
		"  /verbose - Toggle verbose mode; /verbose on | off to set explicitly",
		"  /model   - Show or switch model; /model <id> to switch",
		"  /models  - List available models",
		"  /plan    - Plan first, then execute: /plan <task>",
		"  /skill   - List skills; /skill <name> to run a skill",
		"  /summary - Generate a summary of the current conversation",
		"  /luck    - Record last completed task as lucky; /luck last to preview; /luck list to show events",
		"  /badluck - Record last completed task as bad luck; /badluck last to preview; /badluck list to show events",
		"  /turn    - Temporary perspective shift; /turn reroll | status | on | off | save | clear",
		"  /help    - Show this help message",
		"  /simple  - Control simple mode: /simple on | off | auto (compact context saves tokens)",
		"  /stop    - Cancel current processing",
		"  /heartbeat - Show heartbeat status (gateway only)",
		"  /mcp      - List connected MCP tools",
	}
	if channel == "tui" {
		lines = append(lines, "  /sessions - Manage and switch between sessions")
	}
	if a.Config.SlashCommands != nil {
		for name, cfg := range a.Config.SlashCommands {
			desc := cfg.Description
			if desc == "" {
				desc = name
			}
			if !strings.HasPrefix(name, "/") {
				name = "/" + name
			}
			lines = append(lines, fmt.Sprintf("  %-10s - %s", name, desc))
		}
	}
	return strings.Join(lines, "\n")
}

func (a *AgentLoop) runCustomSlashCommand(ctx context.Context, sessionKey string, cmdName string, args []string, cfg config.SlashCmdConfig) (string, bool, string) {
	_ = ctx
	_ = cmdName
	action := strings.TrimSpace(strings.ToLower(cfg.Action))
	prompt := strings.TrimSpace(cfg.Prompt)

	if action != "" {
		switch action {
		case "clearcontext":
			s, err := a.Sessions.GetOrCreate(sessionKey)
			if err != nil {
				return "Error: " + err.Error(), true, ""
			}
			a.Sessions.ClearSession(s)
			_ = a.Sessions.Save(s)
			a.Sessions.Invalidate(sessionKey)
			return "Conversation cleared.", true, ""
		case "toggleverbose":
			s, err := a.Sessions.GetOrCreate(sessionKey)
			if err != nil {
				return "Error: " + err.Error(), true, ""
			}
			if s.Metadata == nil {
				s.Metadata = map[string]any{}
			}
			cur, _ := s.Metadata["verbose"].(bool)
			s.Metadata["verbose"] = !cur
			_ = a.Sessions.Save(s)
			if s.Metadata["verbose"].(bool) {
				return "Verbose mode ON.", true, ""
			}
			return "Verbose mode OFF.", true, ""
		}
	}

	if prompt != "" {
		execPrompt := prompt
		if len(args) > 0 {
			execPrompt = prompt + "\n\n[User additional input: " + strings.Join(args, " ") + "]"
		}
		return "", false, execPrompt
	}

	return "", false, ""
}

func sessionVerbose(s *session.Session, verboseDefault bool) bool {
	if s == nil || s.Metadata == nil {
		return verboseDefault
	}
	v, ok := s.Metadata["verbose"].(bool)
	if !ok {
		return verboseDefault
	}
	return v
}

type simpleModeSetting int

const (
	simpleModeAuto simpleModeSetting = iota
	simpleModeOn
	simpleModeOff
)

func sessionSimpleModeSetting(s *session.Session) simpleModeSetting {
	if s == nil || s.Metadata == nil {
		return simpleModeAuto
	}
	if raw, ok := s.Metadata["simple_mode"]; ok {
		if v, ok := raw.(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "on", "1", "true", "yes":
				return simpleModeOn
			case "off", "0", "false", "no":
				return simpleModeOff
			case "auto":
				return simpleModeAuto
			}
		}
		if v, ok := raw.(bool); ok {
			if v {
				return simpleModeOn
			}
			return simpleModeAuto
		}
	}
	return simpleModeAuto
}

func (a *AgentLoop) handleSimpleCommand(ctx context.Context, sessionKey string, channel, chatID string, args []string) (string, bool, string) {
	_ = ctx
	s, err := a.Sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "Error: " + err.Error(), true, ""
	}
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}
	cur := sessionSimpleModeSetting(s)
	if len(args) >= 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "on", "1", "true", "yes":
			s.Metadata["simple_mode"] = "on"
		case "off", "0", "false", "no":
			s.Metadata["simple_mode"] = "off"
		case "auto":
			s.Metadata["simple_mode"] = "auto"
		default:
			if cur == simpleModeOn {
				s.Metadata["simple_mode"] = "off"
			} else {
				s.Metadata["simple_mode"] = "on"
			}
		}
	} else {
		if cur == simpleModeOn {
			s.Metadata["simple_mode"] = "off"
		} else {
			s.Metadata["simple_mode"] = "on"
		}
	}
	_ = a.Sessions.Save(s)

	modeSet := sessionSimpleModeSetting(s)
	if a.OnContextInfo != nil {
		mode := "normal"
		if modeSet == simpleModeOn {
			mode = "simple"
		}
		// sources remains unchanged; we don't recalculate context length here
		a.OnContextInfo(channel, chatID, "", mode)
	}

	switch modeSet {
	case simpleModeOn:
		return "**Simple mode ON.** Next messages use compact context (no memory, no USER/SOUL).", true, ""
	case simpleModeOff:
		return "**Simple mode OFF.** Full context forced (ignores tokenBudget auto).", true, ""
	default:
		return "**Simple mode AUTO.** Uses tokenBudget when enabled; otherwise normal.", true, ""
	}
}

func (a *AgentLoop) consolidationLockFor(sessionKey string) *sync.Mutex {
	now := time.Now()
	a.consolidationMu.Lock()
	defer a.consolidationMu.Unlock()

	if a.consolidationLocks == nil {
		a.consolidationLocks = make(map[string]*consolidationLockEntry)
	}
	e := a.consolidationLocks[sessionKey]
	if e == nil {
		e = &consolidationLockEntry{lastUsed: now}
		a.consolidationLocks[sessionKey] = e
	} else {
		e.lastUsed = now
	}

	if len(a.consolidationLocks) > 256 && (a.consolidationLastCleanup.IsZero() || now.Sub(a.consolidationLastCleanup) > 10*time.Minute) {
		a.consolidationLastCleanup = now
		cutoff := now.Add(-1 * time.Hour)
		for k, v := range a.consolidationLocks {
			if v == nil || v.lastUsed.Before(cutoff) {
				delete(a.consolidationLocks, k)
			}
		}
	}

	return &e.mu
}

func (a *AgentLoop) removeConsolidationLock(sessionKey string) {
	a.consolidationMu.Lock()
	defer a.consolidationMu.Unlock()
	delete(a.consolidationLocks, sessionKey)
}

func (a *AgentLoop) maybeConsolidate(ctx context.Context, s *session.Session) {
	if a.Memory == nil || a.Provider == nil {
		return
	}
	window := a.Config.Agents.Defaults.MemoryWindow
	if window <= 0 {
		window = 20
	}
	unconsolidated := a.Sessions.UnconsolidatedCount(s)
	if unconsolidated < window {
		return
	}

	lock := a.consolidationLockFor(s.Key)
	if !lock.TryLock() {
		return
	}

	go func() {
		defer lock.Unlock()
		bgCtx := context.Background()
		timeout := a.consolidationTimeout()
		ok, err := a.Memory.ConsolidateWithTimeout(bgCtx, s, a.Provider, a.Config.ModelIDForAPI(a.Model), false, window, timeout)
		if err != nil && a.Logger != nil {
			a.Logger.Error(fmt.Sprintf("Memory consolidation failed (timeout=%v): %v", timeout, err))
		}
		if ok {
			_ = a.Sessions.Save(s)
		}
	}()
}

func (a *AgentLoop) consolidationTimeout() time.Duration {
	sec := a.Config.Agents.Defaults.ConsolidationTimeout
	if sec <= 0 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

// saveTurn persists new-turn messages into the session with filtering:
// - Skips empty assistant messages (no content, no tool_calls) to prevent context poisoning
// - Truncates large tool results to avoid context overflow
// - Strips runtime context tags from user messages
// - Replaces base64 images with placeholders
func (a *AgentLoop) saveTurn(s *session.Session, msgs []openaiapi.Message, skip int) {
	ts := time.Now().Format(time.RFC3339Nano)

	for _, m := range msgs[skip:] {
		entry := map[string]any{
			"role":      m.Role,
			"content":   m.Content,
			"timestamp": ts,
		}

		switch m.Role {
		case "assistant":
			if msgContentString(m.Content) == "" && len(m.ToolCalls) == 0 {
				continue // skip empty assistant messages — they poison session context
			}
			if len(m.ToolCalls) > 0 {
				entry["tool_calls"] = m.ToolCalls
				toolsUsed := make([]string, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					if tc.Function.Name != "" {
						toolsUsed = append(toolsUsed, tc.Function.Name)
					}
				}
				if len(toolsUsed) > 0 {
					entry["tools_used"] = toolsUsed
				}
			}

		case "tool":
			if m.ToolCallID != "" {
				entry["tool_call_id"] = m.ToolCallID
			}
			tc := msgContentString(m.Content)
			if len(tc) > toolResultMaxChars {
				entry["content"] = tc[:toolResultMaxChars] + "\n... (truncated)"
			}

		case "user":
			content := msgContentString(m.Content)
			if strings.HasPrefix(content, runtimeContextTag) {
				parts := strings.SplitN(content, "\n\n", 2)
				if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
					content = parts[1]
				} else {
					continue
				}
			}
			content = stripBase64Images(content)
			entry["content"] = content
		}

		s.Messages = append(s.Messages, entry)
	}
	s.UpdatedAt = time.Now()
}

func msgContentString(c any) string {
	if c == nil {
		return ""
	}
	if s, ok := c.(string); ok {
		return s
	}
	if parts, ok := c.([]map[string]any); ok {
		for _, p := range parts {
			if t, ok := p["text"].(string); ok && t != "" {
				return t
			}
		}
	}
	return ""
}

// stripBase64Images replaces inline base64 image data URIs with [image] placeholders.
func stripBase64Images(s string) string {
	const prefix = "data:image/"
	start := strings.Index(s, prefix)
	if start < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	last := 0
	i := start
	for {
		rel := strings.Index(s[i:], prefix)
		if rel < 0 {
			break
		}
		idx := i + rel

		end := idx
		for end < len(s) {
			c := s[end]
			if c == '"' || c == '\'' || c == ')' || c == ' ' || c == '\n' {
				break
			}
			end++
		}
		data := s[idx:end]
		replaced := false
		if colonIdx := strings.Index(data, ";base64,"); colonIdx > 0 {
			payload := data[colonIdx+len(";base64,"):]
			if len(payload) > 100 {
				replaced = true
			} else if _, err := base64.StdEncoding.DecodeString(payload); err == nil {
				replaced = true
			}
		}

		if replaced {
			b.WriteString(s[last:idx])
			b.WriteString("[image]")
			last = end
			i = end
			continue
		}

		next := idx + len(prefix)
		if next > len(s) {
			break
		}
		b.WriteString(s[last:next])
		last = next
		i = next
	}

	b.WriteString(s[last:])
	return b.String()
}

func formatContextLen(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func buildRuntimeContext(channel, chatID, model string) string {
	now := time.Now()
	tz := now.Location().String()
	parts := []string{
		fmt.Sprintf("Current Time: %s", now.Format("2006-01-02 15:04 (Monday)")),
		fmt.Sprintf("Timezone: %s", tz),
	}
	if model != "" {
		parts = append(parts, fmt.Sprintf("Current Model: %s", model))
	}
	if channel != "" {
		parts = append(parts, fmt.Sprintf("Channel: %s", channel))
	}
	if chatID != "" {
		parts = append(parts, fmt.Sprintf("Chat ID: %s", chatID))
	}
	if channel != "" {
		if tools.ChannelSupportsMedia(channel) {
			parts = append(parts, "Media/File sending: supported. Use send_file to deliver screenshots/files to users.")
		} else {
			parts = append(parts, "Media/File sending: NOT supported. Do NOT suggest get_ref or send_file. Instead, inform the user the file is saved at the path and that this platform cannot display images.")
		}
	}
	return runtimeContextTag + "\n" + strings.Join(parts, "\n")
}

// emitToolProgress sends a tool_progress message showing tools about to execute.
// Returns the formatted content string used as a key for message editing.
func (a *AgentLoop) emitToolProgress(ctx context.Context, channel, chatID string, iteration, maxIter int, statuses []bus.ToolCallStatus) string {
	if a.Bus == nil || channel == "" {
		return ""
	}
	content := formatToolProgress(iteration, statuses)
	_ = a.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Type:    bus.MsgToolProgress,
		Channel: channel,
		ChatID:  chatID,
		Content: content,
		Metadata: map[string]any{
			"progress": bus.ToolProgressInfo{
				Iteration: iteration,
				MaxIter:   maxIter,
				Tools:     statuses,
			},
		},
	})
	return content
}

// emitToolProgressUpdate sends an updated tool_progress after tools finish.
// prevMsgID allows channels that support editing to update the previous message.
func (a *AgentLoop) emitToolProgressUpdate(ctx context.Context, channel, chatID string, iteration, maxIter int, statuses []bus.ToolCallStatus, prevMsgID string) {
	if a.Bus == nil || channel == "" {
		return
	}
	content := formatToolProgress(iteration, statuses)
	_ = a.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Type:           bus.MsgToolProgress,
		Channel:        channel,
		ChatID:         chatID,
		Content:        content,
		ReplyMessageID: prevMsgID,
		Metadata: map[string]any{
			"progress": bus.ToolProgressInfo{
				Iteration: iteration,
				MaxIter:   maxIter,
				Tools:     statuses,
			},
		},
	})
}

func formatToolProgress(iteration int, statuses []bus.ToolCallStatus) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔧 Step %d\n", iteration)

	for i, s := range statuses {
		prefix := "├─"
		if i == len(statuses)-1 {
			prefix = "└─"
		}

		icon := "⏳"
		switch s.Status {
		case "done":
			icon = "✅"
		case "error":
			icon = "❌"
		}

		dur := ""
		if s.DurationMs > 0 {
			if s.DurationMs < 1000 {
				dur = fmt.Sprintf(" (%dms)", s.DurationMs)
			} else {
				dur = fmt.Sprintf(" (%.1fs)", float64(s.DurationMs)/1000)
			}
		}

		if s.Args != "" {
			fmt.Fprintf(&sb, "%s %s %s: %s%s", prefix, icon, s.Name, s.Args, dur)
		} else {
			fmt.Fprintf(&sb, "%s %s %s%s", prefix, icon, s.Name, dur)
		}

		if s.Error != "" {
			fmt.Fprintf(&sb, "\n│     %s", s.Error)
		}
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// toolArgsSummary extracts the most relevant argument for display.
func toolArgsSummary(name string, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	var summary string
	switch name {
	case "exec":
		if cmd, ok := args["command"].(string); ok {
			summary = cmd
		}
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			summary = p
		}
	case "list_dir":
		if p, ok := args["path"].(string); ok {
			summary = p
		} else {
			summary = "."
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			summary = q
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			summary = u
		}
	case "message":
		if ch, ok := args["channel"].(string); ok {
			summary = "→ " + ch
		}
	case "cron":
		if act, ok := args["action"].(string); ok {
			summary = act
		}
	}
	if len(summary) > 100 {
		summary = summary[:97] + "..."
	}
	return summary
}
