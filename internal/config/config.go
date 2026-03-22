package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Agents        AgentsConfig              `json:"agents"`
	Channels      ChannelsConfig            `json:"channels"`
	Providers     ProvidersConfig           `json:"providers"`
	Models        ModelsConfig              `json:"models,omitempty"`
	Gateway       GatewayConfig             `json:"gateway"`
	Tools         ToolsConfig               `json:"tools"`
	SlashCommands map[string]SlashCmdConfig `json:"slashCommands,omitempty"`
	UX            UXConfig                  `json:"ux,omitempty"` // Global typing/placeholder switches for agent, tui, channels
}

// UXConfig provides global switches for typing indicator and placeholder message.
// When enabled, applies to agent CLI, TUI, and all channels that support it.
type UXConfig struct {
	Typing      bool              `json:"typing,omitempty"`      // Show typing/thinking indicator
	Placeholder PlaceholderConfig `json:"placeholder,omitempty"` // "Thinking..." placeholder (enabled + text)
}

// UXPtr returns a pointer to UX config if any UX option is set; otherwise nil.
func (c *Config) UXPtr() *UXConfig {
	if c == nil {
		return nil
	}
	if c.UX.Typing || c.UX.Placeholder.Enabled || c.UX.Placeholder.Text != "" {
		return &c.UX
	}
	return nil
}

// ModelsConfig holds per-model overrides (OpenClaw-style). Ref: models.providers in OpenClaw.
type ModelsConfig struct {
	// ContextWindow overrides per model ID (e.g. "glm-4.6": 128000, "claude-opus-4": 200000).
	// Keys are matched case-insensitively; provider prefix is stripped (e.g. "zhipu/glm-4" -> "glm-4").
	ContextWindow map[string]int `json:"contextWindow,omitempty"`
}

// SlashCmdConfig defines a custom slash command. See OpenClaw-style slash commands.
type SlashCmdConfig struct {
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Action      string `json:"action,omitempty"` // clearContext, toggleVerbose
	Model       string `json:"model,omitempty"`  // optional model override for this command
}

type AgentsConfig struct {
	Defaults  AgentDefaults  `json:"defaults"`
	SubAgents SubAgentConfig `json:"subagents,omitempty"`
}

// SubAgentConfig configures sub-agent support (OpenClaw-style).
// Sub-agents let the main agent spawn and coordinate other agents to complete complex tasks in parallel.
type SubAgentConfig struct {
	Enabled         bool                `json:"enabled"`
	MaxConcurrent   int                 `json:"maxConcurrent"`   // default 3
	Timeout         int                 `json:"timeout"`         // ms, default 120000
	MaxNestingDepth int                 `json:"maxNestingDepth"` // default 2, 0=no nesting
	Model           string              `json:"model,omitempty"` // override model for subagents (cheaper model for cost savings)
	Inherit         SubAgentInherit     `json:"inherit,omitempty"`
	ToolPolicy      SubAgentToolPolicy  `json:"toolPolicy,omitempty"`
	ContextPassing  SubAgentContextPass `json:"contextPassing,omitempty"`
}

type SubAgentInherit struct {
	Tools   bool `json:"tools"`   // sub-agent inherits main agent's tools (default true)
	Context bool `json:"context"` // sub-agent inherits conversation history (default false)
}

type SubAgentToolPolicy struct {
	Allowed  []string `json:"allowed,omitempty"`  // if non-empty, only these tools
	Disabled []string `json:"disabled,omitempty"` // tools sub-agent cannot use
}

type SubAgentContextPass struct {
	IncludeSystemPrompt bool `json:"includeSystemPrompt"` // default true
	IncludeConversation bool `json:"includeConversation"` // default false
	IncludeSkills       bool `json:"includeSkills"`       // default true
}

type AgentDefaults struct {
	Workspace            string  `json:"workspace"`
	Model                string  `json:"model"`
	Provider             string  `json:"provider,omitempty"` // "auto" (default) or explicit provider name
	MaxTokens            int     `json:"maxTokens"`
	Temperature          float64 `json:"temperature"`
	MaxToolIterations    int     `json:"maxToolIterations"`
	ReasoningEffort      string  `json:"reasoningEffort,omitempty"`
	MemoryWindow         int     `json:"memoryWindow,omitempty"`         // Message count threshold (fallback)
	MemoryWindowTokens   int     `json:"memoryWindowTokens,omitempty"`   // Token threshold (preferred)
	MaxContextTokens     int     `json:"maxContextTokens,omitempty"`     // Total context token truncation threshold
	MaxMemoryInjectChars int     `json:"maxMemoryInjectChars,omitempty"` // Memory injection character limit
	MaxMessages          int     `json:"maxMessages,omitempty"`
	ConsolidationTimeout int     `json:"consolidationTimeout,omitempty"`
	MaxRetries           int     `json:"maxRetries,omitempty"`
	RetryBaseDelay       int     `json:"retryBaseDelay,omitempty"`
	RetryMaxDelay        int     `json:"retryMaxDelay,omitempty"`
	VerboseDefault       bool    `json:"verboseDefault,omitempty"` // default verbose mode for new sessions

	// Concurrency: limit total agent runs (global semaphore).
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
	// Collect debounce: merge queued messages per session after debounceMs silence to reduce token usage.
	DebounceMs int `json:"debounceMs,omitempty"`

	// Block streaming: stream output in chunks while generating (OpenClaw-style).
	BlockStreamingDefault bool   `json:"blockStreamingDefault,omitempty"` // default false
	BlockStreamingBreak   string `json:"blockStreamingBreak,omitempty"`   // "text_end" | "message_end"
	// StreamingToolExecution: OpenCode-style streaming tool execution (start tools before the LLM finishes).
	StreamingToolExecution bool `json:"streamingToolExecution,omitempty"`
	// ParallelToolExecution: execute multiple tool calls in the same round in parallel (OpenCode-style).
	ParallelToolExecution bool `json:"parallelToolExecution,omitempty"`
	BlockStreamingChunk   *struct {
		MinChars        int    `json:"minChars,omitempty"`
		MaxChars        int    `json:"maxChars,omitempty"`
		BreakPreference string `json:"breakPreference,omitempty"` // paragraph | newline | sentence | whitespace
	} `json:"blockStreamingChunk,omitempty"`

	// Routing: complexity-based model selection (light vs heavy).
	// When enabled, messages scoring below threshold use LightModel; others use primary model.
	Routing *RoutingConfig `json:"routing,omitempty"`

	// TokenBudget: reduce system prompt for simple tasks to save tokens and latency.
	// When enabled, messages scoring below SimpleThreshold use compact context (no always-skills, no USER/SOUL, no memory).
	TokenBudget *TokenBudgetConfig `json:"tokenBudget,omitempty"`

	// ShortTermMemory: token-based tiered history retention.
	// Controls how recent conversation detail is preserved in both normal and simple mode.
	ShortTermMemory *ShortTermMemoryConfig `json:"shortTermMemory,omitempty"`

	// ResourceConstrained: if true, don't auto-download packages, only provide suggestions
	// Useful for resource-limited environments (e.g., embedded devices, low-storage systems)
	ResourceConstrained bool `json:"resourceConstrained,omitempty"`
}

// TokenBudgetConfig controls context reduction for simple tasks.
type TokenBudgetConfig struct {
	Enabled         bool    `json:"enabled"`
	SimpleThreshold float64 `json:"simpleThreshold"` // 0-1; score < threshold → compact mode. Default 0.35.
}

// ShortTermMemoryConfig controls token-based tiered history retention.
type ShortTermMemoryConfig struct {
	// RecentTokenBudget: token budget for recent full-detail messages.
	// Messages included from newest backwards until budget fills.
	// Default 4000 (~1000 words, ~5 conversation turns).
	RecentTokenBudget int `json:"recentTokenBudget,omitempty"`

	// EnableMiddleCompression: compress messages outside budget into
	// key-entity summary injected into system prompt. Default true.
	EnableMiddleCompression bool `json:"enableMiddleCompression,omitempty"`

	// MiddleSummaryMaxChars: max chars for middle-layer summary in system prompt.
	// Default 2000.
	MiddleSummaryMaxChars int `json:"middleSummaryMaxChars,omitempty"`
}

// RoutingConfig controls intelligent model routing by message complexity.
type RoutingConfig struct {
	Enabled    bool    `json:"enabled"`
	LightModel string  `json:"lightModel"` // model to use for simple tasks (e.g. groq/llama-3.1-8b)
	Threshold  float64 `json:"threshold"`  // score in [0,1]; score >= threshold → primary model
}

type ProviderConfig struct {
	APIKey       string            `json:"apiKey"`
	APIBase      string            `json:"apiBase,omitempty"`
	ExtraHeaders map[string]string `json:"extraHeaders,omitempty"`
}

type ProvidersConfig struct {
	Anthropic   ProviderConfig `json:"anthropic"`
	OpenAI      ProviderConfig `json:"openai"`
	OpenRouter  ProviderConfig `json:"openrouter"`
	DeepSeek    ProviderConfig `json:"deepseek"`
	Groq        ProviderConfig `json:"groq"`
	Zhipu       ProviderConfig `json:"zhipu"`
	DashScope   ProviderConfig `json:"dashscope"`
	VLLM        ProviderConfig `json:"vllm"`
	Ollama      ProviderConfig `json:"ollama"`
	Gemini      ProviderConfig `json:"gemini"`
	Moonshot    ProviderConfig `json:"moonshot"`
	AiHubMix    ProviderConfig `json:"aihubmix"`
	MiniMax     ProviderConfig `json:"minimax"`
	VolcEngine  ProviderConfig `json:"volcengine"`
	SiliconFlow ProviderConfig `json:"siliconflow"`
	Custom      ProviderConfig `json:"custom,omitempty"`
}

type ChannelsConfig struct {
	Telegram   TelegramConfig   `json:"telegram"`
	Discord    DiscordConfig    `json:"discord"`
	Feishu     FeishuConfig     `json:"feishu"`
	Slack      SlackConfig      `json:"slack"`
	DingTalk   DingTalkConfig   `json:"dingtalk"`
	QQ         QQConfig         `json:"qq"`
	WorkWeixin WorkWeixinConfig `json:"workweixin"`
}

// GroupTriggerConfig controls when the bot responds in group chats.
type GroupTriggerConfig struct {
	MentionOnly bool     `json:"mentionOnly,omitempty"` // Only respond when @mentioned
	Prefixes    []string `json:"prefixes,omitempty"`    // Or when content starts with any prefix (stripped)
}

type TelegramConfig struct {
	Enabled        bool               `json:"enabled"`
	Token          string             `json:"token"`
	AllowFrom      []string           `json:"allowFrom,omitempty"`
	SendProgress   bool               `json:"sendProgress,omitempty"`
	SendToolHints  bool               `json:"sendToolHints,omitempty"`
	ReplyToMessage bool               `json:"replyToMessage,omitempty"`
	GroupTrigger   GroupTriggerConfig `json:"groupTrigger,omitempty"`
	Typing         bool               `json:"typing,omitempty"`      // Persistent typing until response
	Placeholder    PlaceholderConfig  `json:"placeholder,omitempty"` // "Thinking..." message edited on response
}

type PlaceholderConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Text    string `json:"text,omitempty"` // e.g. "Thinking... 💭"
}

type DiscordConfig struct {
	Enabled      bool               `json:"enabled"`
	Token        string             `json:"token"`
	AllowFrom    []string           `json:"allowFrom,omitempty"`
	GatewayURL   string             `json:"gatewayUrl,omitempty"`
	Intents      int                `json:"intents,omitempty"`
	GroupPolicy  string             `json:"groupPolicy,omitempty"`  // Legacy: "mention"|"all"
	GroupTrigger GroupTriggerConfig `json:"groupTrigger,omitempty"` // Overrides GroupPolicy when set
	Typing       bool               `json:"typing,omitempty"`
	Placeholder  PlaceholderConfig  `json:"placeholder,omitempty"`
}

type FeishuConfig struct {
	Enabled           bool     `json:"enabled"`
	AppID             string   `json:"appId"`
	AppSecret         string   `json:"appSecret"`
	EncryptKey        string   `json:"encryptKey"`
	VerificationToken string   `json:"verificationToken"`
	AllowFrom         []string `json:"allowFrom,omitempty"`
	ReactionEmoji     string   `json:"reactionEmoji,omitempty"`
	OnConnectMessage  string   `json:"onConnectMessage,omitempty"`
	OnConnectChatID   string   `json:"onConnectChatID,omitempty"`
	BlockStreaming    bool     `json:"blockStreaming,omitempty"` // Enable block streaming output
}

type SlackConfig struct {
	Enabled       bool               `json:"enabled"`
	BotToken      string             `json:"botToken"`
	AppToken      string             `json:"appToken"`
	AllowFrom     []string           `json:"allowFrom,omitempty"`
	ReplyInThread bool               `json:"replyInThread,omitempty"`
	ReactionEmoji string             `json:"reactionEmoji,omitempty"`
	GroupTrigger  GroupTriggerConfig `json:"groupTrigger,omitempty"`
}

type DingTalkConfig struct {
	Enabled   bool     `json:"enabled"`
	AppKey    string   `json:"appKey"`
	AppSecret string   `json:"appSecret"`
	RobotCode string   `json:"robotCode"`
	AllowFrom []string `json:"allowFrom,omitempty"`
}

type QQConfig struct {
	Enabled   bool     `json:"enabled"`
	AppID     string   `json:"appId"`
	Secret    string   `json:"secret"`
	AllowFrom []string `json:"allowFrom,omitempty"`
}

// WorkWeixinConfig is the Work Weixin intelligent bot (Bot ID + Secret via long connection).
// Ref: https://open.work.weixin.qq.com/help2/pc/cat?doc_id=21657
type WorkWeixinConfig struct {
	Enabled   bool     `json:"enabled"`
	BotID     string   `json:"botId"`
	Secret    string   `json:"secret"`
	AllowFrom []string `json:"allowFrom,omitempty"`
}

type MCPServerConfig struct {
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ToolTimeout int               `json:"toolTimeout,omitempty"`
}

type GatewayConfig struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	InboundQueueCap   int    `json:"inboundQueueCap,omitempty"`  // MessageBus inbound channel capacity (default 100)
	OutboundQueueCap  int    `json:"outboundQueueCap,omitempty"` // MessageBus outbound channel capacity (default 100)
	HeartbeatInterval int    `json:"heartbeatInterval,omitempty"`
	HeartbeatChannel  string `json:"heartbeatChannel,omitempty"`
	HeartbeatChatID   string `json:"heartbeatChatID,omitempty"`
}

type ToolsConfig struct {
	RestrictToWorkspace bool                       `json:"restrictToWorkspace"`
	Exec                ExecToolConfig             `json:"exec"`
	Web                 WebToolsConfig             `json:"web"`
	Browser             BrowserConfig              `json:"browser,omitempty"`
	MCPServers          map[string]MCPServerConfig `json:"mcpServers,omitempty"`

	// Built-in capabilities (no external download, no Node.js). Ref: https://www.cnblogs.com/informatics/p/19679935
	AgentBrowser      bool `json:"agentBrowser,omitempty"`      // Browser automation (uses tools.browser when enabled)
	AgentMemory       bool `json:"agentMemory,omitempty"`       // Long-term memory (MEMORY.md + consolidation)
	SelfImproving     bool `json:"selfImproving,omitempty"`     // Capture errors, learn corrections
	ClawdStrike       bool `json:"clawdstrike,omitempty"`       // Security audit tool
	Evolver           bool `json:"evolver,omitempty"`           // Self-evolution, record lessons
	AdaptiveReasoning bool `json:"adaptiveReasoning,omitempty"` // Dynamic reasoning depth by task complexity
}

// BrowserConfig for remote browser (Browserless etc). Reference: https://openclaw-docs.dx3n.cn/tutorials/tools/browser
type BrowserConfig struct {
	Enabled     bool   `json:"enabled"`
	RemoteURL   string `json:"remoteUrl"`   // default wss://chrome.browserless.io
	Token       string `json:"token"`       // Browserless token; also supports BROWSERLESS_TOKEN env
	Profile     string `json:"profile"`     // default "default"
	SnapshotDir string `json:"snapshotDir"` // default workspace/screenshots
	DebugPort   int    `json:"debugPort"`   // CDP debug: 0=disabled; for remote, connect via service dashboard
}

// BuildRemoteURL returns the final WebSocket URL by merging remoteUrl and token.
// Token is taken from config or BROWSERLESS_TOKEN env. If remoteUrl contains ${BROWSERLESS_TOKEN}, it is expanded.
func (c *BrowserConfig) BuildRemoteURL() string {
	base := strings.TrimSpace(c.RemoteURL)
	if base == "" {
		base = "wss://chrome.browserless.io"
	}
	// Env expansion (e.g. ${BROWSERLESS_TOKEN} in remoteUrl)
	base = os.ExpandEnv(base)
	token := strings.TrimSpace(c.Token)
	if token == "" {
		token = os.Getenv("BROWSERLESS_TOKEN")
	}
	if token == "" || strings.Contains(base, "token=") {
		return base
	}
	if strings.Contains(base, "?") {
		return base + "&token=" + token
	}
	return base + "?token=" + token
}

type ExecToolConfig struct {
	Timeout    int    `json:"timeout"`
	PathAppend string `json:"pathAppend,omitempty"`
}

type WebToolsConfig struct {
	Search     WebSearchConfig `json:"search"`
	Fetch      WebFetchConfig  `json:"fetch,omitempty"`
	HTTPProxy  string          `json:"httpProxy,omitempty"`  // http_proxy
	HTTPSProxy string          `json:"httpsProxy,omitempty"` // https_proxy
	AllProxy   string          `json:"allProxy,omitempty"`   // all_proxy
}

// ProxyForScheme returns the proxy URL for the given scheme (http, https, etc).
// Precedence: scheme-specific > allProxy.
func (c WebToolsConfig) ProxyForScheme(scheme string) string {
	switch scheme {
	case "https":
		if c.HTTPSProxy != "" {
			return c.HTTPSProxy
		}
		return c.AllProxy
	case "http":
		if c.HTTPProxy != "" {
			return c.HTTPProxy
		}
		return c.AllProxy
	default:
		return c.AllProxy
	}
}

// ProxyFunc returns a proxy function for HTTP/WebSocket. Uses tools.web proxy if set, else system env (HTTP_PROXY/HTTPS_PROXY).
func (c WebToolsConfig) ProxyFunc() func(*http.Request) (*url.URL, error) {
	if c.HTTPProxy != "" || c.HTTPSProxy != "" || c.AllProxy != "" {
		return func(req *http.Request) (*url.URL, error) {
			proxyURL := c.ProxyForScheme(req.URL.Scheme)
			if proxyURL == "" {
				return nil, nil
			}
			return url.Parse(proxyURL)
		}
	}
	return http.ProxyFromEnvironment
}

type WebSearchConfig struct {
	// Legacy: apiKey/maxResults for Brave (backward compat)
	APIKey     string `json:"apiKey"`
	MaxResults int    `json:"maxResults"`

	// TimeoutSeconds: HTTP timeout for search requests (default 30). DuckDuckGo HTML can be slow.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// Per-provider config (picoclaw-style)
	Brave      WebSearchProviderConfig `json:"brave,omitempty"`
	Tavily     WebSearchProviderConfig `json:"tavily,omitempty"`
	DuckDuckGo WebSearchProviderConfig `json:"duckduckgo,omitempty"`
	Perplexity WebSearchProviderConfig `json:"perplexity,omitempty"`
	SearXNG    SearXNGConfig           `json:"searxng,omitempty"`
}

type WebSearchProviderConfig struct {
	Enabled    bool   `json:"enabled"`
	APIKey     string `json:"apiKey"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type SearXNGConfig struct {
	Enabled    bool   `json:"enabled"`
	BaseURL    string `json:"baseUrl"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type WebFetchConfig struct {
	Firecrawl FirecrawlConfig `json:"firecrawl"`
}

type FirecrawlConfig struct {
	APIKey string `json:"apiKey"`
}

func Default() Config {
	return Config{
		Agents: AgentsConfig{
			SubAgents: SubAgentConfig{
				Enabled:         true,
				MaxConcurrent:   3,
				Timeout:         120000,
				MaxNestingDepth: 2,
				Inherit:         SubAgentInherit{Tools: true, Context: false},
				ContextPassing:  SubAgentContextPass{IncludeSystemPrompt: true, IncludeConversation: false, IncludeSkills: true},
			},
			Defaults: AgentDefaults{
				Workspace:            "~/.luckclaw/workspace",
				Model:                "anthropic/claude-opus-4-5",
				Provider:             "auto",
				MaxTokens:            8192,
				Temperature:          0.1,
				MaxToolIterations:    40,
				MemoryWindow:         20,
				MemoryWindowTokens:   8000,
				MaxContextTokens:     100000,
				MaxMemoryInjectChars: 4000,
				MaxMessages:          500,
				ShortTermMemory: &ShortTermMemoryConfig{
					RecentTokenBudget:       4000,
					EnableMiddleCompression: true,
					MiddleSummaryMaxChars:   2000,
				},
				ConsolidationTimeout: 30,
				VerboseDefault:       true,
				MaxConcurrent:        4,
				DebounceMs:           1000,
			},
		},
		Providers: defaultProvidersWithAPIBase(),
		Channels: ChannelsConfig{
			Telegram: TelegramConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
			Discord: DiscordConfig{
				Enabled:     false,
				GatewayURL:  "wss://gateway.discord.gg/?v=10&encoding=json",
				Intents:     37377,
				GroupPolicy: "mention",
				AllowFrom:   []string{"*"},
			},
			Feishu: FeishuConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
			Slack: SlackConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
			DingTalk: DingTalkConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
			QQ: QQConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
			WorkWeixin: WorkWeixinConfig{
				Enabled:   false,
				AllowFrom: []string{"*"},
			},
		},
		Gateway: GatewayConfig{
			Host:              "0.0.0.0",
			Port:              18790,
			InboundQueueCap:   100,
			OutboundQueueCap:  100,
			HeartbeatInterval: 300,
		},
		Tools: ToolsConfig{
			Exec: ExecToolConfig{Timeout: 60},
			Web: WebToolsConfig{
				Search: WebSearchConfig{
					APIKey:     "",
					MaxResults: 5,
					Brave:      WebSearchProviderConfig{Enabled: false, APIKey: "", MaxResults: 5},
					Tavily:     WebSearchProviderConfig{Enabled: false, APIKey: "", MaxResults: 5},
					DuckDuckGo: WebSearchProviderConfig{Enabled: true, MaxResults: 5},
					Perplexity: WebSearchProviderConfig{Enabled: false, APIKey: "", MaxResults: 5},
					SearXNG:    SearXNGConfig{Enabled: false, BaseURL: "", MaxResults: 5},
				},
				Fetch: WebFetchConfig{
					Firecrawl: FirecrawlConfig{APIKey: ""},
				},
			},
			Browser: BrowserConfig{
				Enabled:   false,
				RemoteURL: "wss://chrome.browserless.io",
				Token:     "",
				Profile:   "default",
			},
			AgentBrowser:      true, // requires tools.browser
			AgentMemory:       true,
			SelfImproving:     true,
			ClawdStrike:       true,
			Evolver:           true,
			AdaptiveReasoning: true,
			MCPServers: map[string]MCPServerConfig{
				"example": {
					Type:        "",
					URL:         "",
					Headers:     map[string]string{"Authorization": "Bearer TOKEN"},
					ToolTimeout: 60,
				},
			},
		},
	}
}

func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			applyEnvOverrides(&c)
			normalizeConfig(&c)
			return c, nil
		}
		return Config{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return Config{}, fmt.Errorf("%s: invalid JSON: %w\n\nHint: Check config file format (e.g. missing commas between object members, matching quotes)", path, err)
	}
	migrateConfig(raw)
	migrated, _ := json.Marshal(raw)
	if err := json.Unmarshal(migrated, &c); err != nil {
		return Config{}, fmt.Errorf("%s: invalid JSON after migration: %w\n\nHint: Check config file format (e.g. missing commas between object members, matching quotes)", path, err)
	}
	applyEnvOverrides(&c)
	normalizeConfig(&c)
	return c, nil
}

// normalizeConfig applies defaults for missing/zero values.
func normalizeConfig(c *Config) {
	Normalize(c)
}

// Normalize applies defaults for missing/zero values. Call after unmarshaling
// user-provided config (e.g. from UI) before Validate/Save.
func Normalize(c *Config) {
	normalizeWebSearch(&c.Tools.Web.Search)
	if strings.TrimSpace(c.Tools.Browser.RemoteURL) == "" {
		c.Tools.Browser.RemoteURL = "wss://chrome.browserless.io"
	}
	if c.Gateway.InboundQueueCap <= 0 {
		c.Gateway.InboundQueueCap = 100
	}
	if c.Gateway.OutboundQueueCap <= 0 {
		c.Gateway.OutboundQueueCap = 100
	}
	// Fill empty provider apiBase with default URLs
	NormalizeProviderAPIBase(c)
	// Fill allowFrom for enabled channels to avoid startup validation errors.
	normalizeChannelAllowFrom(&c.Channels)
}

func normalizeWebSearch(c *WebSearchConfig) {
	// Legacy: apiKey/maxResults -> Brave
	if c.APIKey != "" && c.Brave.APIKey == "" {
		c.Brave.APIKey = c.APIKey
		c.Brave.Enabled = true
	}
	if c.MaxResults > 0 && c.Brave.MaxResults <= 0 {
		c.Brave.MaxResults = c.MaxResults
	}
	// DuckDuckGo: no API key, enable by default as fallback
	if !c.Brave.Enabled && !c.Tavily.Enabled && !c.Perplexity.Enabled && !c.SearXNG.Enabled {
		c.DuckDuckGo.Enabled = true
	}
	if c.DuckDuckGo.MaxResults <= 0 {
		c.DuckDuckGo.MaxResults = 5
	}
}

func normalizeChannelAllowFrom(ch *ChannelsConfig) {
	defaultAllow := []string{"*"}
	if ch.Telegram.Enabled && len(ch.Telegram.AllowFrom) == 0 {
		ch.Telegram.AllowFrom = defaultAllow
	}
	if ch.Discord.Enabled && len(ch.Discord.AllowFrom) == 0 {
		ch.Discord.AllowFrom = defaultAllow
	}
	if ch.Feishu.Enabled && len(ch.Feishu.AllowFrom) == 0 {
		ch.Feishu.AllowFrom = defaultAllow
	}
	if ch.Slack.Enabled && len(ch.Slack.AllowFrom) == 0 {
		ch.Slack.AllowFrom = defaultAllow
	}
	if ch.DingTalk.Enabled && len(ch.DingTalk.AllowFrom) == 0 {
		ch.DingTalk.AllowFrom = defaultAllow
	}
	if ch.QQ.Enabled && len(ch.QQ.AllowFrom) == 0 {
		ch.QQ.AllowFrom = defaultAllow
	}
	if ch.WorkWeixin.Enabled && len(ch.WorkWeixin.AllowFrom) == 0 {
		ch.WorkWeixin.AllowFrom = defaultAllow
	}
}

// migrateConfig applies schema migrations for backward compatibility.
func migrateConfig(raw map[string]any) {
	if agents, ok := raw["agents"].(map[string]any); ok {
		if defaults, ok := agents["defaults"].(map[string]any); ok && defaults != nil {
			if v, ok := defaults["blockStreamingDefault"].(string); ok {
				defaults["blockStreamingDefault"] = strings.ToLower(strings.TrimSpace(v)) == "on"
			}
		}
	}
	if tools, ok := raw["tools"].(map[string]any); ok {
		if exec, ok := tools["exec"].(map[string]any); ok {
			if v, ok := exec["restrictToWorkspace"].(bool); ok && v {
				tools["restrictToWorkspace"] = true
			}
		}
		// Migrate from old builtIn nested structure
		if bi, ok := tools["builtIn"].(map[string]any); ok {
			for k, v := range bi {
				tools[k] = v
			}
			delete(tools, "builtIn")
		}
		for _, k := range []string{"agentBrowser", "agentMemory", "selfImproving", "clawdstrike", "evolver", "adaptiveReasoning"} {
			if _, has := tools[k]; !has {
				switch k {
				case "agentMemory", "selfImproving", "clawdstrike":
					tools[k] = true
				default:
					tools[k] = false
				}
			}
		}
		// Migrate browser: remoteUrl with ?token=xxx -> remoteUrl + token
		if browser, ok := tools["browser"].(map[string]any); ok && browser != nil {
			if ru, ok := browser["remoteUrl"].(string); ok && ru != "" {
				if tok, ok := browser["token"].(string); !ok || tok == "" {
					if idx := strings.Index(ru, "?token="); idx >= 0 {
						rest := ru[idx+7:]
						if end := strings.IndexAny(rest, "&"); end >= 0 {
							browser["token"] = rest[:end]
							browser["remoteUrl"] = strings.TrimSuffix(ru[:idx], "?")
						} else {
							browser["token"] = rest
							browser["remoteUrl"] = ru[:idx]
						}
					} else if idx := strings.Index(ru, "&token="); idx >= 0 {
						rest := ru[idx+7:]
						if end := strings.IndexAny(rest, "&"); end >= 0 {
							browser["token"] = rest[:end]
						} else {
							browser["token"] = rest
						}
						browser["remoteUrl"] = ru[:idx]
					}
				}
			}
		}
	}
	if channels, ok := raw["channels"].(map[string]any); ok {
		allowChannels := []string{"telegram", "discord", "feishu", "slack", "dingtalk", "qq", "workweixin"}
		for _, name := range allowChannels {
			if ch, ok := channels[name].(map[string]any); ok && ch != nil {
				if _, has := ch["allowFrom"]; !has {
					ch["allowFrom"] = []any{"*"}
				}
			}
		}
		// Discord: groupPolicy "mention" -> groupTrigger.mentionOnly
		if dc, ok := channels["discord"].(map[string]any); ok && dc != nil {
			if _, has := dc["groupTrigger"]; !has {
				if gp, ok := dc["groupPolicy"].(string); ok && strings.ToLower(strings.TrimSpace(gp)) == "mention" {
					dc["groupTrigger"] = map[string]any{"mentionOnly": true}
				}
			}
		}
		// Migrate telegram/discord proxy -> tools.web (proxy removed from channels, use tools.web)
		var proxyURL string
		if tg, ok := channels["telegram"].(map[string]any); ok && tg != nil {
			if p, ok := tg["proxy"].(string); ok && strings.TrimSpace(p) != "" {
				proxyURL = strings.TrimSpace(p)
			}
			delete(tg, "proxy")
		}
		if dc, ok := channels["discord"].(map[string]any); ok && dc != nil {
			if proxyURL == "" {
				if p, ok := dc["proxy"].(string); ok && strings.TrimSpace(p) != "" {
					proxyURL = strings.TrimSpace(p)
				}
			}
			delete(dc, "proxy")
		}
		if proxyURL != "" {
			if tools, ok := raw["tools"].(map[string]any); ok {
				if web, ok := tools["web"].(map[string]any); ok && web != nil {
					if all, _ := web["allProxy"].(string); strings.TrimSpace(all) == "" {
						web["allProxy"] = proxyURL
					}
				}
			}
		}
	}
}

// applyEnvOverrides reads LUCKCLAW_* environment variables and overlays them
// onto the config. Nested keys use __ (double underscore) as separator.
// Example: LUCKCLAW_PROVIDERS__OPENAI__APIKEY=sk-xxx
//
// Special: LUCKCLAW_HOME sets the data root; when set, agents.defaults.workspace
// is overridden to LUCKCLAW_HOME/workspace if not explicitly set via LUCKCLAW_AGENTS__DEFAULTS__WORKSPACE.
func applyEnvOverrides(c *Config) {
	home := os.Getenv("LUCKCLAW_HOME")
	if home != "" {
		defaultWorkspace := filepath.Join(home, "workspace")
		if strings.TrimSpace(c.Agents.Defaults.Workspace) == "" {
			c.Agents.Defaults.Workspace = defaultWorkspace
		}
	}
	applyEnvOverridesPrefix(c, "LUCKCLAW_")
}

func applyEnvOverridesPrefix(c *Config, prefix string) {
	for _, env := range os.Environ() {
		eq := strings.IndexByte(env, '=')
		if eq < 0 {
			continue
		}
		key, val := env[:eq], env[eq+1:]
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		path := strings.ToLower(strings.TrimPrefix(key, prefix))
		parts := strings.Split(path, "__")
		if len(parts) == 0 {
			continue
		}
		applyEnvValue(c, parts, val)
	}
}

func applyEnvValue(c *Config, parts []string, val string) {
	if len(parts) < 2 {
		return
	}
	section := parts[0]
	switch section {
	case "agents":
		if len(parts) >= 3 && parts[1] == "defaults" {
			field := parts[2]
			if len(parts) > 3 {
				field = strings.Join(parts[2:], "_")
			}
			applyAgentDefault(&c.Agents.Defaults, field, val)
		}
		if len(parts) >= 3 && parts[1] == "subagents" {
			applySubAgentField(&c.Agents.SubAgents, parts[2], val)
		}
	case "providers":
		if len(parts) >= 3 {
			cfg := c.providerByName(parts[1])
			if cfg != nil {
				applyProviderField(cfg, parts[2], val)
			}
		}
	case "channels":
		if len(parts) >= 3 {
			switch parts[1] {
			case "telegram":
				applyTelegramField(&c.Channels.Telegram, parts[2], val)
			case "discord":
				applyDiscordField(&c.Channels.Discord, parts[2], val)
			case "feishu":
				applyFeishuField(&c.Channels.Feishu, parts[2], val)
			case "slack":
				applySlackField(&c.Channels.Slack, parts[2], val)
			case "dingtalk":
				applyDingTalkField(&c.Channels.DingTalk, parts[2], val)
			case "qq":
				applyQQField(&c.Channels.QQ, parts[2], val)
			case "workweixin":
				applyWorkWeixinField(&c.Channels.WorkWeixin, parts[2], val)
			}
		}
	case "tools":
		if len(parts) >= 2 {
			switch parts[1] {
			case "restricttoworkspace":
				c.Tools.RestrictToWorkspace = (val == "true" || val == "1")
			case "agentbrowser":
				c.Tools.AgentBrowser = (val == "true" || val == "1")
			case "agentmemory":
				c.Tools.AgentMemory = (val == "true" || val == "1")
			case "selfimproving":
				c.Tools.SelfImproving = (val == "true" || val == "1")
			case "clawdstrike":
				c.Tools.ClawdStrike = (val == "true" || val == "1")
			}
			if len(parts) >= 3 && parts[1] == "web" {
				if parts[2] == "search" {
					if len(parts) >= 4 && parts[3] == "apikey" {
						c.Tools.Web.Search.APIKey = val
						c.Tools.Web.Search.Brave.APIKey = val
						c.Tools.Web.Search.Brave.Enabled = true
					}
					if len(parts) >= 4 && parts[3] == "maxresults" {
						if v := parseInt(val); v > 0 {
							c.Tools.Web.Search.MaxResults = v
							c.Tools.Web.Search.Brave.MaxResults = v
						}
					}
					if len(parts) >= 4 {
						switch parts[3] {
						case "brave":
							if len(parts) >= 5 {
								switch parts[4] {
								case "apikey":
									c.Tools.Web.Search.Brave.APIKey = val
									c.Tools.Web.Search.Brave.Enabled = true
								case "enabled":
									c.Tools.Web.Search.Brave.Enabled = (val == "true" || val == "1")
								case "maxresults":
									if v := parseInt(val); v > 0 {
										c.Tools.Web.Search.Brave.MaxResults = v
									}
								}
							}
						case "tavily":
							if len(parts) >= 5 {
								switch parts[4] {
								case "apikey":
									c.Tools.Web.Search.Tavily.APIKey = val
									c.Tools.Web.Search.Tavily.Enabled = true
								case "enabled":
									c.Tools.Web.Search.Tavily.Enabled = (val == "true" || val == "1")
								case "maxresults":
									if v := parseInt(val); v > 0 {
										c.Tools.Web.Search.Tavily.MaxResults = v
									}
								}
							}
						case "duckduckgo":
							if len(parts) >= 5 {
								switch parts[4] {
								case "enabled":
									c.Tools.Web.Search.DuckDuckGo.Enabled = (val == "true" || val == "1")
								case "maxresults":
									if v := parseInt(val); v > 0 {
										c.Tools.Web.Search.DuckDuckGo.MaxResults = v
									}
								}
							}
						case "perplexity":
							if len(parts) >= 5 {
								switch parts[4] {
								case "apikey":
									c.Tools.Web.Search.Perplexity.APIKey = val
									c.Tools.Web.Search.Perplexity.Enabled = true
								case "enabled":
									c.Tools.Web.Search.Perplexity.Enabled = (val == "true" || val == "1")
								case "maxresults":
									if v := parseInt(val); v > 0 {
										c.Tools.Web.Search.Perplexity.MaxResults = v
									}
								}
							}
						case "searxng":
							if len(parts) >= 5 {
								switch parts[4] {
								case "baseurl":
									c.Tools.Web.Search.SearXNG.BaseURL = val
									c.Tools.Web.Search.SearXNG.Enabled = true
								case "enabled":
									c.Tools.Web.Search.SearXNG.Enabled = (val == "true" || val == "1")
								case "maxresults":
									if v := parseInt(val); v > 0 {
										c.Tools.Web.Search.SearXNG.MaxResults = v
									}
								}
							}
						}
					}
				}
				if parts[2] == "httpproxy" {
					c.Tools.Web.HTTPProxy = val
				}
				if parts[2] == "httpsproxy" {
					c.Tools.Web.HTTPSProxy = val
				}
				if parts[2] == "allproxy" {
					c.Tools.Web.AllProxy = val
				}
				if parts[2] == "fetch" && len(parts) >= 5 && parts[3] == "firecrawl" && parts[4] == "apikey" {
					c.Tools.Web.Fetch.Firecrawl.APIKey = val
				}
			}
		}
	case "gateway":
		if len(parts) >= 2 {
			switch parts[1] {
			case "port":
				if v := parseInt(val); v > 0 {
					c.Gateway.Port = v
				}
			case "host":
				c.Gateway.Host = val
			case "inboundqueuecap":
				if v := parseInt(val); v > 0 {
					c.Gateway.InboundQueueCap = v
				}
			case "outboundqueuecap":
				if v := parseInt(val); v > 0 {
					c.Gateway.OutboundQueueCap = v
				}
			case "heartbeatinterval":
				if v := parseInt(val); v > 0 {
					c.Gateway.HeartbeatInterval = v
				}
			case "heartbeatchannel":
				c.Gateway.HeartbeatChannel = val
			case "heartbeatchatid":
				c.Gateway.HeartbeatChatID = val
			}
		}
	}
}

func applySubAgentField(s *SubAgentConfig, field, val string) {
	switch field {
	case "enabled":
		s.Enabled = (val == "true" || val == "1")
	case "maxconcurrent":
		if v := parseInt(val); v > 0 {
			s.MaxConcurrent = v
		}
	case "timeout":
		if v := parseInt(val); v > 0 {
			s.Timeout = v
		}
	case "maxnestingdepth":
		if v := parseInt(val); v >= 0 {
			s.MaxNestingDepth = v
		}
	case "model":
		s.Model = val
	}
}

func applyAgentDefault(d *AgentDefaults, field, val string) {
	switch field {
	case "model":
		d.Model = val
	case "workspace":
		d.Workspace = val
	case "provider":
		d.Provider = val
	case "maxtokens":
		if v := parseInt(val); v > 0 {
			d.MaxTokens = v
		}
	case "temperature":
		if v := parseFloat(val); v >= 0 {
			d.Temperature = v
		}
	case "maxtooliterations":
		if v := parseInt(val); v > 0 {
			d.MaxToolIterations = v
		}
	case "memorywindow":
		if v := parseInt(val); v > 0 {
			d.MemoryWindow = v
		}
	case "maxmessages":
		if v := parseInt(val); v > 0 {
			d.MaxMessages = v
		}
	case "maxconcurrent":
		if v := parseInt(val); v > 0 {
			d.MaxConcurrent = v
		}
	case "debouncems", "debouncemns":
		if v := parseInt(val); v >= 0 {
			d.DebounceMs = v
		}
	case "maxretries":
		if v := parseInt(val); v > 0 {
			d.MaxRetries = v
		}
	case "retrybasedelay":
		if v := parseInt(val); v >= 0 {
			d.RetryBaseDelay = v
		}
	case "retrymaxdelay":
		if v := parseInt(val); v >= 0 {
			d.RetryMaxDelay = v
		}
	case "verbosedefault":
		d.VerboseDefault = (val == "true" || val == "1")
	case "streamingtoolexecution":
		d.StreamingToolExecution = (val == "true" || val == "1" || val == "on")
	case "paralleltoolexecution", "paralleltoolsexecution":
		d.ParallelToolExecution = (val == "true" || val == "1" || val == "on")
	case "blockstreamingdefault":
		d.BlockStreamingDefault = (val == "true" || val == "1" || val == "on")
	case "resourceconstrained":
		d.ResourceConstrained = (val == "true" || val == "1" || val == "on")
	case "tokenbudget_enabled":
		if d.TokenBudget == nil {
			d.TokenBudget = &TokenBudgetConfig{}
		}
		d.TokenBudget.Enabled = (val == "true" || val == "1" || val == "on")
	case "tokenbudget_simplethreshold":
		if v := parseFloat(val); v >= 0 && v <= 1 {
			if d.TokenBudget == nil {
				d.TokenBudget = &TokenBudgetConfig{}
			}
			d.TokenBudget.SimpleThreshold = v
		}
	case "shorttermmemory_recenttokenbudget":
		if v := parseInt(val); v > 0 {
			if d.ShortTermMemory == nil {
				d.ShortTermMemory = &ShortTermMemoryConfig{}
			}
			d.ShortTermMemory.RecentTokenBudget = v
		}
	case "shorttermmemory_enablemiddlecompression":
		if d.ShortTermMemory == nil {
			d.ShortTermMemory = &ShortTermMemoryConfig{}
		}
		d.ShortTermMemory.EnableMiddleCompression = (val == "true" || val == "1" || val == "on")
	case "shorttermmemory_middlesummarymaxchars":
		if v := parseInt(val); v > 0 {
			if d.ShortTermMemory == nil {
				d.ShortTermMemory = &ShortTermMemoryConfig{}
			}
			d.ShortTermMemory.MiddleSummaryMaxChars = v
		}
	}
}

func applyProviderField(cfg *ProviderConfig, field, val string) {
	switch field {
	case "apikey":
		cfg.APIKey = val
	case "apibase":
		cfg.APIBase = val
	}
}

func applyTelegramField(cfg *TelegramConfig, field, val string) {
	switch field {
	case "token":
		cfg.Token = val
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	}
}

func applyDiscordField(cfg *DiscordConfig, field, val string) {
	switch field {
	case "token":
		cfg.Token = val
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func applyFeishuField(cfg *FeishuConfig, field, val string) {
	switch field {
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "appid":
		cfg.AppID = val
	case "appsecret":
		cfg.AppSecret = val
	case "encryptkey":
		cfg.EncryptKey = val
	case "verificationtoken":
		cfg.VerificationToken = val
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func applySlackField(cfg *SlackConfig, field, val string) {
	switch field {
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "bottoken":
		cfg.BotToken = val
	case "apptoken":
		cfg.AppToken = val
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func applyDingTalkField(cfg *DingTalkConfig, field, val string) {
	switch field {
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "appkey":
		cfg.AppKey = val
	case "appsecret":
		cfg.AppSecret = val
	case "robotcode":
		cfg.RobotCode = val
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func applyQQField(cfg *QQConfig, field, val string) {
	switch field {
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "appid":
		cfg.AppID = val
	case "secret":
		cfg.Secret = val
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func applyWorkWeixinField(cfg *WorkWeixinConfig, field, val string) {
	switch field {
	case "enabled":
		cfg.Enabled = (val == "true" || val == "1")
	case "botid":
		cfg.BotID = val
	case "secret":
		cfg.Secret = val
	case "allowfrom":
		cfg.AllowFrom = splitAllowFrom(val)
	}
}

func splitAllowFrom(val string) []string {
	var out []string
	for _, s := range strings.Split(val, ",") {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseInt(s string) int {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int(c-'0')
	}
	return v
}

func parseFloat(s string) float64 {
	var v float64
	var seenDot bool
	var frac float64 = 1
	for _, c := range s {
		if c == '.' && !seenDot {
			seenDot = true
			continue
		}
		if c >= '0' && c <= '9' {
			if seenDot {
				frac *= 0.1
				v += float64(c-'0') * frac
			} else {
				v = v*10 + float64(c-'0')
			}
			continue
		}
		return 0
	}
	return v
}

func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

// DefaultFullTemplateMap returns a map with all configurable options for JSON output.
// Keys with known defaults are filled; others are empty/zero. Use for config.json.example.
func DefaultFullTemplateMap() map[string]any {
	p := defaultProvidersWithAPIBase()
	providerMap := func(c ProviderConfig) map[string]any {
		m := map[string]any{"apiKey": c.APIKey, "apiBase": c.APIBase}
		if len(c.ExtraHeaders) > 0 {
			m["extraHeaders"] = c.ExtraHeaders
		}
		return m
	}
	return map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{
				"workspace":              "~/.luckclaw/workspace",
				"model":                  "",
				"provider":               "auto",
				"maxTokens":              8192,
				"temperature":            0.1,
				"maxToolIterations":      40,
				"reasoningEffort":        "",
				"memoryWindow":           20,
				"maxMessages":            500,
				"consolidationTimeout":   30,
				"maxRetries":             3,
				"retryBaseDelay":         1000,
				"retryMaxDelay":          30000,
				"verboseDefault":         true,
				"maxConcurrent":          4,
				"debounceMs":             1000,
				"blockStreamingDefault":  false,
				"blockStreamingBreak":    "",
				"streamingToolExecution": true,
				"parallelToolExecution":  true,
				"routing": map[string]any{
					"enabled":    false,
					"lightModel": "",
					"threshold":  0.35,
				},
				"tokenBudget": map[string]any{
					"enabled":         true,
					"simpleThreshold": 0.35,
				},
				"shortTermMemory": map[string]any{
					"recentTokenBudget":       4000,
					"enableMiddleCompression": true,
					"middleSummaryMaxChars":   2000,
				},
			},
			"subagents": map[string]any{
				"enabled":         true,
				"maxConcurrent":   3,
				"timeout":         120000,
				"maxNestingDepth": 2,
				"model":           "",
				"inherit":         map[string]any{"tools": true, "context": false},
				"toolPolicy":      map[string]any{"allowed": []string{}, "disabled": []string{}},
				"contextPassing": map[string]any{
					"includeSystemPrompt": true,
					"includeConversation": false,
					"includeSkills":       true,
				},
			},
		},
		"channels": map[string]any{
			"telegram": map[string]any{
				"enabled":        false,
				"token":          "",
				"allowFrom":      []string{"*"},
				"sendProgress":   false,
				"sendToolHints":  false,
				"replyToMessage": false,
				"groupTrigger":   map[string]any{"mentionOnly": false, "prefixes": []string{}},
				"typing":         false,
				"placeholder":    map[string]any{"enabled": false, "text": ""},
			},
			"discord": map[string]any{
				"enabled":      false,
				"token":        "",
				"allowFrom":    []string{"*"},
				"gatewayUrl":   "wss://gateway.discord.gg/?v=10&encoding=json",
				"intents":      37377,
				"groupPolicy":  "mention",
				"groupTrigger": map[string]any{"mentionOnly": true, "prefixes": []string{}},
				"typing":       false,
				"placeholder":  map[string]any{"enabled": false, "text": ""},
			},
			"feishu": map[string]any{
				"enabled":           false,
				"appId":             "",
				"appSecret":         "",
				"encryptKey":        "",
				"verificationToken": "",
				"allowFrom":         []string{"*"},
				"reactionEmoji":     "",
				"onConnectMessage":  "",
				"onConnectChatID":   "",
				"blockStreaming":    false,
			},
			"slack": map[string]any{
				"enabled":       false,
				"botToken":      "",
				"appToken":      "",
				"allowFrom":     []string{"*"},
				"replyInThread": false,
				"reactionEmoji": "",
				"groupTrigger":  map[string]any{"mentionOnly": false, "prefixes": []string{}},
			},
			"dingtalk": map[string]any{
				"enabled":   false,
				"appKey":    "",
				"appSecret": "",
				"robotCode": "",
				"allowFrom": []string{"*"},
			},
			"qq": map[string]any{
				"enabled":   false,
				"appId":     "",
				"secret":    "",
				"allowFrom": []string{"*"},
			},
			"workweixin": map[string]any{
				"enabled":   false,
				"botId":     "",
				"secret":    "",
				"allowFrom": []string{"*"},
			},
		},
		"providers": map[string]any{
			"anthropic":   providerMap(p.Anthropic),
			"openai":      providerMap(p.OpenAI),
			"openrouter":  providerMap(p.OpenRouter),
			"deepseek":    providerMap(p.DeepSeek),
			"groq":        providerMap(p.Groq),
			"zhipu":       providerMap(p.Zhipu),
			"dashscope":   providerMap(p.DashScope),
			"vllm":        providerMap(p.VLLM),
			"ollama":      providerMap(p.Ollama),
			"gemini":      providerMap(p.Gemini),
			"moonshot":    providerMap(p.Moonshot),
			"aihubmix":    providerMap(p.AiHubMix),
			"minimax":     providerMap(p.MiniMax),
			"volcengine":  providerMap(p.VolcEngine),
			"siliconflow": providerMap(p.SiliconFlow),
			"custom":      providerMap(p.Custom),
		},
		"models": map[string]any{
			"contextWindow": map[string]any{},
		},
		"gateway": map[string]any{
			"host":              "0.0.0.0",
			"port":              18790,
			"inboundQueueCap":   100,
			"outboundQueueCap":  100,
			"heartbeatInterval": 300,
			"heartbeatChannel":  "",
			"heartbeatChatID":   "",
		},
		"tools": map[string]any{
			"restrictToWorkspace": false,
			"exec": map[string]any{
				"timeout":    60,
				"pathAppend": "",
			},
			"web": map[string]any{
				"search": map[string]any{
					"maxResults":     5,
					"timeoutSeconds": 30,
					"brave":          map[string]any{"enabled": false, "apiKey": "", "maxResults": 5},
					"tavily":         map[string]any{"enabled": false, "apiKey": "", "maxResults": 5},
					"duckduckgo":     map[string]any{"enabled": true, "apiKey": "", "maxResults": 5},
					"perplexity":     map[string]any{"enabled": false, "apiKey": "", "maxResults": 5},
					"searxng":        map[string]any{"enabled": false, "baseUrl": "", "maxResults": 5},
				},
				"fetch": map[string]any{
					"firecrawl": map[string]any{"apiKey": ""},
				},
				"httpProxy":  "",
				"httpsProxy": "",
				"allProxy":   "",
			},
			"browser": map[string]any{
				"enabled":     false,
				"remoteUrl":   "wss://chrome.browserless.io",
				"token":       "",
				"profile":     "default",
				"snapshotDir": "",
				"debugPort":   0,
			},
			"agentBrowser":      true,
			"agentMemory":       true,
			"selfImproving":     true,
			"clawdstrike":       true,
			"evolver":           true,
			"adaptiveReasoning": true,
			"mcpServers": map[string]any{
				"example": map[string]any{
					"type":        "",
					"url":         "",
					"headers":     map[string]any{"Authorization": "Bearer TOKEN"},
					"toolTimeout": 60,
				},
			},
		},
		"slashCommands": map[string]any{},
	}
}

// WriteDefaultFullTemplate writes the full config template (all options, defaults filled) to path.
func WriteDefaultFullTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(DefaultFullTemplateMap(), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

// mergeConfigMaps recursively merges template into current. Only adds keys that are missing in current;
// existing values in current are never overwritten.
func mergeConfigMaps(current, template map[string]any) {
	for k, tVal := range template {
		cVal, exists := current[k]
		if !exists {
			current[k] = deepCopyAny(tVal)
			continue
		}
		cMap, cIsMap := cVal.(map[string]any)
		tMap, tIsMap := tVal.(map[string]any)
		if cIsMap && tIsMap {
			mergeConfigMaps(cMap, tMap)
		}
	}
}

func deepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = deepCopyAny(val)
		}
		return out
	default:
		return v
	}
}

// UpdateConfig merges new config options from the default template into the existing config at path.
// Existing values are preserved; only missing keys are added. Creates the file if it does not exist.
func UpdateConfig(path string) error {
	var current map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			current = make(map[string]any)
		} else {
			return err
		}
	} else {
		if err := json.Unmarshal(b, &current); err != nil {
			return fmt.Errorf("%s: invalid JSON: %w", path, err)
		}
	}
	template := DefaultFullTemplateMap()
	mergeConfigMaps(current, template)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}

// DefaultModel returns the default model from agents.defaults.
func (c *Config) DefaultModel() string {
	return strings.TrimSpace(c.Agents.Defaults.Model)
}

// DefaultWorkspace returns the default workspace from agents.defaults.
func (c *Config) DefaultWorkspace() string {
	return strings.TrimSpace(c.Agents.Defaults.Workspace)
}
