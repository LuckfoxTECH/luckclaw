package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type SelectedProvider struct {
	Name string
	ProviderConfig
}

// isProviderReady returns true when the provider is configured and usable.
// For ollama and vllm (local, no API key required), explicit apiBase is required.
func isProviderReady(name string, cfg ProviderConfig) bool {
	if cfg.APIKey != "" {
		return true
	}
	// ollama and vllm: must have apiBase explicitly set (no default = not configured)
	if (name == "ollama" || name == "vllm") && strings.TrimSpace(cfg.APIBase) != "" {
		return true
	}
	return false
}

func (c Config) SelectProvider(model string) *SelectedProvider {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		m = strings.ToLower(strings.TrimSpace(c.Agents.Defaults.Model))
	}

	p := c.Providers

	// Forced provider override: when agents.defaults.provider is set to a
	// specific name (not "" and not "auto"), bypass auto-detection entirely.
	forced := strings.ToLower(strings.TrimSpace(c.Agents.Defaults.Provider))
	if forced != "" && forced != "auto" {
		if cfg := c.providerByName(forced); cfg != nil && isProviderReady(forced, *cfg) {
			out := SelectedProvider{Name: forced, ProviderConfig: *cfg}
			if out.APIBase == "" {
				out.APIBase = DefaultAPIBase(out.Name)
			}
			return &out
		}
	}

	// Explicit prefix match first (avoids false positives from keyword match)
	prefixMap := []struct {
		prefix string
		name   string
		cfg    ProviderConfig
	}{
		{"aihubmix/", "aihubmix", p.AiHubMix},
		{"openrouter/", "openrouter", p.OpenRouter},
		{"deepseek/", "deepseek", p.DeepSeek},
		{"anthropic/", "anthropic", p.Anthropic},
		{"openai/", "openai", p.OpenAI},
		{"gemini/", "gemini", p.Gemini},
		{"zhipu/", "zhipu", p.Zhipu},
		{"dashscope/", "dashscope", p.DashScope},
		{"groq/", "groq", p.Groq},
		{"moonshot/", "moonshot", p.Moonshot},
		{"minimax/", "minimax", p.MiniMax},
		{"volcengine/", "volcengine", p.VolcEngine},
		{"siliconflow/", "siliconflow", p.SiliconFlow},
		{"vllm/", "vllm", p.VLLM},
		{"ollama/", "ollama", p.Ollama},
		{"custom/", "custom", p.Custom},
	}
	for _, item := range prefixMap {
		if strings.HasPrefix(m, item.prefix) && isProviderReady(item.name, item.cfg) {
			out := SelectedProvider{Name: item.name, ProviderConfig: item.cfg}
			if out.APIBase == "" {
				out.APIBase = DefaultAPIBase(out.Name)
			}
			return &out
		}
	}

	keywordMap := []struct {
		kw   string
		name string
		cfg  ProviderConfig
	}{
		{"aihubmix", "aihubmix", p.AiHubMix},
		{"openrouter", "openrouter", p.OpenRouter},
		{"deepseek", "deepseek", p.DeepSeek},
		{"anthropic", "anthropic", p.Anthropic},
		{"claude", "anthropic", p.Anthropic},
		{"openai", "openai", p.OpenAI},
		{"gpt", "openai", p.OpenAI},
		{"gemini", "gemini", p.Gemini},
		{"zhipu", "zhipu", p.Zhipu},
		{"glm", "zhipu", p.Zhipu},
		{"zai", "zhipu", p.Zhipu},
		{"dashscope", "dashscope", p.DashScope},
		{"qwen", "dashscope", p.DashScope},
		{"groq", "groq", p.Groq},
		{"moonshot", "moonshot", p.Moonshot},
		{"kimi", "moonshot", p.Moonshot},
		{"minimax", "minimax", p.MiniMax},
		{"abab", "minimax", p.MiniMax},
		{"volcengine", "volcengine", p.VolcEngine},
		{"doubao", "volcengine", p.VolcEngine},
		{"siliconflow", "siliconflow", p.SiliconFlow},
		{"vllm", "vllm", p.VLLM},
		{"ollama", "ollama", p.Ollama},
	}

	for _, item := range keywordMap {
		if strings.Contains(m, item.kw) && isProviderReady(item.name, item.cfg) {
			out := SelectedProvider{Name: item.name, ProviderConfig: item.cfg}
			if out.APIBase == "" {
				out.APIBase = DefaultAPIBase(out.Name)
			}
			return &out
		}
	}

	fallback := []struct {
		name string
		cfg  ProviderConfig
	}{
		{"openrouter", p.OpenRouter},
		{"aihubmix", p.AiHubMix},
		{"anthropic", p.Anthropic},
		{"openai", p.OpenAI},
		{"deepseek", p.DeepSeek},
		{"gemini", p.Gemini},
		{"zhipu", p.Zhipu},
		{"dashscope", p.DashScope},
		{"moonshot", p.Moonshot},
		{"minimax", p.MiniMax},
		{"volcengine", p.VolcEngine},
		{"siliconflow", p.SiliconFlow},
		{"vllm", p.VLLM},
		{"ollama", p.Ollama},
		{"groq", p.Groq},
		{"custom", p.Custom},
	}

	for _, item := range fallback {
		if isProviderReady(item.name, item.cfg) {
			out := SelectedProvider{Name: item.name, ProviderConfig: item.cfg}
			if out.APIBase == "" {
				out.APIBase = DefaultAPIBase(out.Name)
			}
			return &out
		}
	}

	return nil
}

// SelectAllProviders returns all configured providers (with APIKey and APIBase)
// in failover order: primary first, then fallback order.
func (c Config) SelectAllProviders(model string) []SelectedProvider {
	primary := c.SelectProvider(model)
	p := c.Providers
	order := []struct {
		name string
		cfg  ProviderConfig
	}{
		{"openrouter", p.OpenRouter},
		{"aihubmix", p.AiHubMix},
		{"anthropic", p.Anthropic},
		{"openai", p.OpenAI},
		{"deepseek", p.DeepSeek},
		{"gemini", p.Gemini},
		{"zhipu", p.Zhipu},
		{"dashscope", p.DashScope},
		{"moonshot", p.Moonshot},
		{"minimax", p.MiniMax},
		{"volcengine", p.VolcEngine},
		{"siliconflow", p.SiliconFlow},
		{"vllm", p.VLLM},
		{"ollama", p.Ollama},
		{"groq", p.Groq},
		{"custom", p.Custom},
	}
	seen := make(map[string]bool)
	var out []SelectedProvider
	if primary != nil {
		out = append(out, *primary)
		seen[primary.Name] = true
	}
	for _, item := range order {
		if !isProviderReady(item.name, item.cfg) || seen[item.name] {
			continue
		}
		seen[item.name] = true
		cfg := item.cfg
		if cfg.APIBase == "" {
			cfg.APIBase = DefaultAPIBase(item.name)
		}
		if cfg.APIBase == "" {
			continue
		}
		out = append(out, SelectedProvider{Name: item.name, ProviderConfig: cfg})
	}
	return out
}

// ProviderByName returns the ProviderConfig for the given provider name (e.g. "openrouter", "openai").
// Used by CLI config wizard for interactive setup. Returns a pointer to the config's field for mutation.
func (c *Config) ProviderByName(name string) *ProviderConfig {
	m := map[string]*ProviderConfig{
		"anthropic":   &c.Providers.Anthropic,
		"openai":      &c.Providers.OpenAI,
		"openrouter":  &c.Providers.OpenRouter,
		"deepseek":    &c.Providers.DeepSeek,
		"groq":        &c.Providers.Groq,
		"zhipu":       &c.Providers.Zhipu,
		"dashscope":   &c.Providers.DashScope,
		"vllm":        &c.Providers.VLLM,
		"ollama":      &c.Providers.Ollama,
		"gemini":      &c.Providers.Gemini,
		"moonshot":    &c.Providers.Moonshot,
		"aihubmix":    &c.Providers.AiHubMix,
		"minimax":     &c.Providers.MiniMax,
		"volcengine":  &c.Providers.VolcEngine,
		"siliconflow": &c.Providers.SiliconFlow,
		"custom":      &c.Providers.Custom,
	}
	return m[name]
}

func (c Config) providerByName(name string) *ProviderConfig {
	m := map[string]*ProviderConfig{
		"anthropic":   &c.Providers.Anthropic,
		"openai":      &c.Providers.OpenAI,
		"openrouter":  &c.Providers.OpenRouter,
		"deepseek":    &c.Providers.DeepSeek,
		"groq":        &c.Providers.Groq,
		"zhipu":       &c.Providers.Zhipu,
		"dashscope":   &c.Providers.DashScope,
		"vllm":        &c.Providers.VLLM,
		"ollama":      &c.Providers.Ollama,
		"gemini":      &c.Providers.Gemini,
		"moonshot":    &c.Providers.Moonshot,
		"aihubmix":    &c.Providers.AiHubMix,
		"minimax":     &c.Providers.MiniMax,
		"volcengine":  &c.Providers.VolcEngine,
		"siliconflow": &c.Providers.SiliconFlow,
		"custom":      &c.Providers.Custom,
	}
	return m[name]
}

// ModelIDForAPI returns the model ID to send to the provider's API.
// For direct providers (moonshot, openai, etc.), strips the "provider/" prefix
// since their API expects bare model IDs (e.g. "kimi-k2.5" not "moonshot/kimi-k2.5").
// For openrouter, returns model as-is (e.g. "anthropic/claude-sonnet-4").
// For aihubmix, strips "aihubmix/" prefix since their API expects bare IDs (e.g. "kimi-for-coding-free").
func (c Config) ModelIDForAPI(model string) string {
	selected := c.SelectProvider(model)
	if selected == nil {
		return model
	}
	if selected.Name == "openrouter" {
		return model
	}
	if selected.Name == "aihubmix" {
		prefix := "aihubmix/"
		if strings.HasPrefix(model, prefix) {
			return strings.TrimPrefix(model, prefix)
		}
		return model
	}
	prefix := selected.Name + "/"
	if strings.HasPrefix(model, prefix) {
		return strings.TrimPrefix(model, prefix)
	}
	return model
}

// SupportsPromptCaching returns true if the provider supports Anthropic-style
// cache_control on content blocks (prompt caching). Used for OpenRouter and Anthropic.
func SupportsPromptCaching(provider string) bool {
	switch provider {
	case "openrouter", "anthropic":
		return true
	default:
		return false
	}
}

// NormalizeProviderAPIBase fills empty provider apiBase with default URLs.
// Call from Normalize so config always has apiBase for display and persistence.
func NormalizeProviderAPIBase(c *Config) {
	p := &c.Providers
	for _, item := range []struct {
		name string
		cfg  *ProviderConfig
	}{
		{"openrouter", &p.OpenRouter}, {"aihubmix", &p.AiHubMix}, {"anthropic", &p.Anthropic},
		{"openai", &p.OpenAI}, {"deepseek", &p.DeepSeek}, {"groq", &p.Groq},
		{"zhipu", &p.Zhipu}, {"dashscope", &p.DashScope}, {"moonshot", &p.Moonshot},
		{"minimax", &p.MiniMax}, {"volcengine", &p.VolcEngine}, {"siliconflow", &p.SiliconFlow},
		{"vllm", &p.VLLM}, {"ollama", &p.Ollama}, {"gemini", &p.Gemini}, {"custom", &p.Custom},
	} {
		if strings.TrimSpace(item.cfg.APIBase) == "" {
			item.cfg.APIBase = DefaultAPIBase(item.name)
		}
	}
}

// fetchModelsFromAPI fetches model IDs from GET {apiBase}/models (OpenAI-compatible).
// Returns (nil, errorMsg) on failure.
// providerName: used for provider-specific auth (e.g. Anthropic uses x-api-key, not Authorization).
func fetchModelsFromAPI(apiBase, apiKey string, extraHeaders map[string]string, providerName string) ([]string, string) {
	base := strings.TrimRight(apiBase, "/")
	if base == "" {
		return nil, "apiBase is empty"
	}
	url := base + "/models"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "failed to create request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		switch providerName {
		case "anthropic":
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		default:
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	for k, v := range extraHeaders {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "request failed: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "GET /models returned " + fmt.Sprintf("%d", resp.StatusCode)
	}
	var raw struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, "invalid response format: " + err.Error()
	}
	var ids []string
	for _, d := range raw.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids, ""
}

// ListAvailableModelsResult holds models and any fetch errors.
type ListAvailableModelsResult struct {
	Models      []string
	FetchErrors []string // e.g. "openai: request failed: timeout"
}

// ListAvailableModels returns model IDs from GET {apiBase}/models for each configured provider.
// When API fetch fails, no fallback is used; FetchErrors contains the failure messages (in English).
func (c Config) ListAvailableModels() ListAvailableModelsResult {
	p := c.Providers
	order := []struct {
		name string
		cfg  ProviderConfig
	}{
		{"openrouter", p.OpenRouter}, {"aihubmix", p.AiHubMix}, {"anthropic", p.Anthropic},
		{"openai", p.OpenAI}, {"deepseek", p.DeepSeek}, {"groq", p.Groq},
		{"zhipu", p.Zhipu}, {"dashscope", p.DashScope}, {"moonshot", p.Moonshot},
		{"minimax", p.MiniMax}, {"volcengine", p.VolcEngine}, {"siliconflow", p.SiliconFlow},
		{"vllm", p.VLLM}, {"ollama", p.Ollama}, {"gemini", p.Gemini}, {"custom", p.Custom},
	}
	seen := make(map[string]bool)
	var out []string
	var fetchErrors []string
	for _, item := range order {
		if !isProviderReady(item.name, item.cfg) {
			continue
		}
		apiBase := item.cfg.APIBase
		if apiBase == "" {
			apiBase = DefaultAPIBase(item.name)
		}
		models, errMsg := fetchModelsFromAPI(apiBase, item.cfg.APIKey, item.cfg.ExtraHeaders, item.name)
		if errMsg != "" {
			fetchErrors = append(fetchErrors, item.name+": "+errMsg)
			continue
		}
		prefix := item.name + "/"
		for _, m := range models {
			full := m
			if !strings.Contains(m, "/") {
				full = prefix + m
			}
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
		}
	}
	// Prepend default model if configured and not already in list
	defaultModel := strings.TrimSpace(c.Agents.Defaults.Model)
	if defaultModel != "" && !seen[defaultModel] {
		if c.SelectProvider(defaultModel) != nil {
			out = append([]string{defaultModel}, out...)
		}
	}
	return ListAvailableModelsResult{Models: out, FetchErrors: fetchErrors}
}

// ResolvedProviderInfo holds the resolved provider details for display (model, provider, apiBase).
type ResolvedProviderInfo struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	APIBase  string `json:"apiBase"`
}

// ResolvedProvider returns the provider info for the given model (for display in UI).
func (c Config) ResolvedProvider(model string) *ResolvedProviderInfo {
	if model == "" {
		model = strings.TrimSpace(c.Agents.Defaults.Model)
	}
	selected := c.SelectProvider(model)
	if selected == nil {
		return nil
	}
	apiBase := selected.APIBase
	if apiBase == "" {
		apiBase = DefaultAPIBase(selected.Name)
	}
	return &ResolvedProviderInfo{
		Model:    model,
		Provider: selected.Name,
		APIBase:  apiBase,
	}
}

// defaultProvidersWithAPIBase returns ProvidersConfig with apiBase set for each provider (for default config).
func defaultProvidersWithAPIBase() ProvidersConfig {
	p := ProvidersConfig{}
	for _, name := range []string{"openrouter", "aihubmix", "anthropic", "openai", "deepseek", "groq", "zhipu", "dashscope", "moonshot", "minimax", "volcengine", "siliconflow", "vllm", "ollama", "gemini", "custom"} {
		base := DefaultAPIBase(name)
		switch name {
		case "openrouter":
			p.OpenRouter.APIBase = base
		case "aihubmix":
			p.AiHubMix.APIBase = base
		case "anthropic":
			p.Anthropic.APIBase = base
		case "openai":
			p.OpenAI.APIBase = base
		case "deepseek":
			p.DeepSeek.APIBase = base
		case "groq":
			p.Groq.APIBase = base
		case "zhipu":
			p.Zhipu.APIBase = base
		case "dashscope":
			p.DashScope.APIBase = base
		case "moonshot":
			p.Moonshot.APIBase = base
		case "minimax":
			p.MiniMax.APIBase = base
		case "volcengine":
			p.VolcEngine.APIBase = base
		case "siliconflow":
			p.SiliconFlow.APIBase = base
		case "vllm":
			p.VLLM.APIBase = base
		case "ollama":
			p.Ollama.APIBase = base
		case "gemini":
			p.Gemini.APIBase = base
		case "custom":
			p.Custom.APIBase = base
		}
	}
	return p
}

func DefaultAPIBase(provider string) string {
	switch provider {
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "aihubmix":
		return "https://aihubmix.com/v1"
	case "openai":
		return "https://api.openai.com/v1"
	case "zhipu":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "deepseek":
		return "https://api.deepseek.com"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "dashscope":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "moonshot":
		return "https://api.moonshot.ai/v1"
	case "minimax":
		return "https://api.minimax.chat/v1"
	case "volcengine":
		return "https://ark.cn-beijing.volces.com/api/v3"
	case "siliconflow":
		return "https://api.siliconflow.cn/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case "custom":
		return "https://api.example.com/v1"
	case "ollama":
		return ""
	default:
		return ""
	}
}
