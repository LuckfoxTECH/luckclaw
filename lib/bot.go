package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"luckclaw/internal/agent"
	"luckclaw/internal/config"
	"luckclaw/internal/logging"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	"luckclaw/internal/session"
	"luckclaw/internal/skills"
)

type Bot struct {
	agent  *agent.AgentLoop
	logger *logging.MemoryLogger
}

func NewBot(cfgPath string) (*Bot, error) {
	// If cfgPath is empty, try to find it
	if cfgPath == "" {
		var err error
		cfgPath, err = paths.ConfigPath()
		if err != nil {
			return nil, err
		}

		// Fallback to local config if global one doesn't exist
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			if _, err := os.Stat("config.json"); err == nil {
				cfgPath = "config.json"
			} else if _, err := os.Stat("luckclaw/config.json"); err == nil {
				cfgPath = "luckclaw/config.json"
			}
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %v", cfgPath, err)
	}

	model := cfg.Agents.Defaults.Model
	selected := cfg.SelectProvider(model)
	if selected == nil {
		// Try fallback if model not found in explicit map
		if cfg.Providers.OpenAI.APIKey != "" {
			selected = &config.SelectedProvider{
				Name:           "openai",
				ProviderConfig: cfg.Providers.OpenAI,
			}
			selected.APIBase = "https://api.openai.com/v1"
		} else {
			return nil, fmt.Errorf("no provider found for model %s and no default provider configured", model)
		}
	}

	provider := &openaiapi.Client{
		APIKey:                selected.APIKey,
		APIBase:               selected.APIBase,
		ExtraHeaders:          selected.ExtraHeaders,
		SupportsPromptCaching: config.SupportsPromptCaching(selected.Name),
		HTTPClient:            openaiapi.NewHTTPClientWithProxy(&cfg.Tools.Web, 120*time.Second),
	}

	sessMgr := session.NewManager()
	ws, _ := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
	if ws != "" {
		sessMgr.Workspace = ws
	}

	logger := logging.NewMemoryLogger(1000)

	a := agent.New(cfg, provider, sessMgr, model, logger)

	return &Bot{agent: a, logger: logger}, nil
}

func (b *Bot) Close() {
	if b.agent != nil {
		b.agent.Close()
	}
}

func (b *Bot) GetLogs() []logging.Entry {
	if b.logger == nil {
		return nil
	}
	return b.logger.GetEntries()
}

func (b *Bot) Chat(ctx context.Context, message string, sessionID string) (string, error) {
	if b.agent == nil {
		return "", fmt.Errorf("agent not initialized")
	}
	return b.agent.ProcessDirect(ctx, message, sessionID)
}

func (b *Bot) GetHistory(sessionID string) ([]map[string]any, error) {
	if b.agent == nil || b.agent.Sessions == nil {
		return nil, fmt.Errorf("agent or sessions not initialized")
	}

	s, err := b.agent.Sessions.GetOrCreate(sessionID)
	if err != nil {
		return nil, err
	}

	return s.Messages, nil
}

func (b *Bot) DeleteHistory(sessionID string) error {
	if b.agent == nil || b.agent.Sessions == nil {
		return fmt.Errorf("agent or sessions not initialized")
	}
	return b.agent.Sessions.Delete(sessionID)
}

// Config Management

func (b *Bot) GetConfig() (interface{}, error) {
	return b.agent.Config, nil
}

// GetConfigWithResolved returns config as a map with _resolvedProvider (model, provider, apiBase) for UI display.
func (b *Bot) ListAvailableModels() (models []string, fetchErrors []string) {
	if b.agent == nil {
		return ListAvailableModelsFromConfig("")
	}
	result := b.agent.Config.ListAvailableModels()
	return result.Models, result.FetchErrors
}

// ListAvailableModelsFromConfig loads config and fetches models (for use when Bot is nil).
func ListAvailableModelsFromConfig(cfgPath string) (models []string, fetchErrors []string) {
	if cfgPath == "" {
		var err error
		cfgPath, err = paths.ConfigPath()
		if err != nil {
			return nil, []string{"config path not found"}
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, []string{"failed to load config: " + err.Error()}
	}
	result := cfg.ListAvailableModels()
	return result.Models, result.FetchErrors
}

func (b *Bot) GetConfigWithResolved() (map[string]interface{}, error) {
	m := ConfigToMapWithResolved(b.agent.Config)
	if m == nil {
		return nil, fmt.Errorf("failed to marshal config")
	}
	return m, nil
}

func (b *Bot) SaveConfig(cfgData interface{}) error {
	// Marshal the input data to JSON
	data, err := json.Marshal(cfgData)
	if err != nil {
		return fmt.Errorf("failed to marshal config data: %v", err)
	}

	// Unmarshal into a fresh config object to validate structure
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid config structure: %v", err)
	}
	config.Normalize(&cfg)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return err
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}

	// Reload agent with new config
	b.agent.Config = cfg
	return nil
}

func (b *Bot) GenerateDefaultConfig() error {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("config file already exists at %s", cfgPath)
	}
	return config.Save(cfgPath, config.Default())
}

func (b *Bot) ResetConfig() (interface{}, error) {
	defaultCfg := config.Default()
	if err := b.SaveConfig(defaultCfg); err != nil {
		return nil, err
	}
	return ConfigToMapWithResolved(defaultCfg), nil
}

// ConfigToMapWithResolved converts config to map and adds _resolvedProvider for UI display.
func ConfigToMapWithResolved(cfg config.Config) map[string]interface{} {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	model := cfg.Agents.Defaults.Model
	if info := cfg.ResolvedProvider(model); info != nil {
		ctxWin := cfg.ContextWindowForModel(model)
		m["_resolvedProvider"] = map[string]interface{}{
			"model": info.Model, "provider": info.Provider, "apiBase": info.APIBase,
			"contextWindow": ctxWin,
		}
	}
	m["_providerOptions"] = []string{
		"auto", "openrouter", "openai", "anthropic", "deepseek", "groq",
		"zhipu", "dashscope", "moonshot", "aihubmix", "minimax",
		"volcengine", "siliconflow", "gemini", "vllm", "ollama", "custom",
	}
	return m
}

// GatewayPortFromConfig returns the gateway port from config (default 18790).
func GatewayPortFromConfig() int {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return 18790
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return 18790
	}
	if cfg.Gateway.Port > 0 {
		return cfg.Gateway.Port
	}
	return 18790
}

func ResetGlobalConfig() (interface{}, error) {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return nil, err
	}
	defaultCfg := config.Default()
	if err := config.Save(cfgPath, defaultCfg); err != nil {
		return nil, err
	}
	return ConfigToMapWithResolved(defaultCfg), nil
}

func SaveGlobalConfig(cfgData interface{}) error {
	// Marshal the input data to JSON
	data, err := json.Marshal(cfgData)
	if err != nil {
		return fmt.Errorf("failed to marshal config data: %v", err)
	}

	// Unmarshal into a fresh config object to validate structure
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid config structure: %v", err)
	}
	config.Normalize(&cfg)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return err
	}
	return config.Save(cfgPath, cfg)
}

// Skills Management

func (b *Bot) ListSkills() ([]skills.Skill, error) {
	ws, err := paths.ExpandUser(b.agent.Config.Agents.Defaults.Workspace)
	if err != nil {
		return nil, err
	}
	return skills.Discover(ws)
}

func (b *Bot) GetSkill(name string) (string, error) {
	ws, err := paths.ExpandUser(b.agent.Config.Agents.Defaults.Workspace)
	if err != nil {
		return "", err
	}
	skillPath := filepath.Join(ws, "skills", name, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (b *Bot) SaveSkill(name string, content string) error {
	ws, err := paths.ExpandUser(b.agent.Config.Agents.Defaults.Workspace)
	if err != nil {
		return err
	}
	skillDir := filepath.Join(ws, "skills", name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	return os.WriteFile(skillPath, []byte(content), 0644)
}

func (b *Bot) DeleteSkill(name string) error {
	ws, err := paths.ExpandUser(b.agent.Config.Agents.Defaults.Workspace)
	if err != nil {
		return err
	}
	skillDir := filepath.Join(ws, "skills", name)
	return os.RemoveAll(skillDir)
}
