package config

import (
	"errors"
	"fmt"
	"strings"
)

// ValidationError collects multiple validation issues.
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "validation failed"
	}
	if len(e.Issues) == 1 {
		return e.Issues[0]
	}
	return "config validation failed:\n  - " + strings.Join(e.Issues, "\n  - ") + "\n\n(Multiple channels are supported; each enabled channel needs its credentials. Run 'luckclaw config' to fix.)"
}

// Validate checks config for common issues. Returns nil if valid.
// Use when starting gateway or saving config to catch errors early.
func (c *Config) Validate() error {
	var issues []string

	// Agent defaults
	d := &c.Agents.Defaults
	if strings.TrimSpace(d.Workspace) == "" {
		issues = append(issues, "agents.defaults.workspace is required")
	}
	if strings.TrimSpace(d.Model) == "" {
		issues = append(issues, "agents.defaults.model is required")
	}
	if d.MaxTokens <= 0 {
		issues = append(issues, "agents.defaults.maxTokens must be positive")
	}
	if d.MaxToolIterations <= 0 {
		issues = append(issues, "agents.defaults.maxToolIterations must be positive")
	}
	if d.MaxConcurrent < 0 {
		issues = append(issues, "agents.defaults.maxConcurrent must be >= 0")
	}
	if d.DebounceMs < 0 {
		issues = append(issues, "agents.defaults.debounceMs must be >= 0")
	}

	// SubAgents
	if c.Agents.SubAgents.Enabled {
		if c.Agents.SubAgents.MaxConcurrent <= 0 {
			issues = append(issues, "agents.subagents.maxConcurrent must be positive when enabled")
		}
		if c.Agents.SubAgents.Timeout <= 0 {
			issues = append(issues, "agents.subagents.timeout must be positive when enabled")
		}
	}

	// Enabled channels: required credentials
	ch := &c.Channels
	if ch.Telegram.Enabled && strings.TrimSpace(ch.Telegram.Token) == "" {
		issues = append(issues, "channels.telegram.token is required when telegram is enabled")
	}
	if ch.Discord.Enabled && strings.TrimSpace(ch.Discord.Token) == "" {
		issues = append(issues, "channels.discord.token is required when discord is enabled")
	}
	if ch.Feishu.Enabled {
		if strings.TrimSpace(ch.Feishu.AppID) == "" || strings.TrimSpace(ch.Feishu.AppSecret) == "" {
			issues = append(issues, "channels.feishu.appId and appSecret are required when feishu is enabled")
		}
	}
	if ch.Slack.Enabled {
		if strings.TrimSpace(ch.Slack.BotToken) == "" || strings.TrimSpace(ch.Slack.AppToken) == "" {
			issues = append(issues, "channels.slack.botToken and appToken are required when slack is enabled")
		}
	}
	if ch.DingTalk.Enabled {
		if strings.TrimSpace(ch.DingTalk.AppKey) == "" || strings.TrimSpace(ch.DingTalk.AppSecret) == "" {
			issues = append(issues, "channels.dingtalk.appKey and appSecret are required when dingtalk is enabled")
		}
	}
	if ch.QQ.Enabled {
		if strings.TrimSpace(ch.QQ.AppID) == "" || strings.TrimSpace(ch.QQ.Secret) == "" {
			issues = append(issues, "channels.qq.appId and secret are required when qq is enabled")
		}
	}
	if ch.WorkWeixin.Enabled {
		if strings.TrimSpace(ch.WorkWeixin.BotID) == "" || strings.TrimSpace(ch.WorkWeixin.Secret) == "" {
			issues = append(issues, "channels.workweixin.botId and secret are required when workweixin is enabled")
		}
	}
	// Gateway
	if c.Gateway.InboundQueueCap < 1 {
		issues = append(issues, "gateway.inboundQueueCap must be >= 1")
	}
	if c.Gateway.OutboundQueueCap < 1 {
		issues = append(issues, "gateway.outboundQueueCap must be >= 1")
	}

	// Tools
	if c.Tools.Exec.Timeout <= 0 {
		issues = append(issues, "tools.exec.timeout must be positive")
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

// ValidateForGateway performs stricter validation when starting gateway:
// at least one provider with API key must be configured.
func (c *Config) ValidateForGateway() error {
	if err := c.Validate(); err != nil {
		return err
	}
	selected := c.SelectProvider(c.Agents.Defaults.Model)
	if selected == nil {
		return errors.New("no provider configured for model " + c.Agents.Defaults.Model + "; set providers in config.json (e.g. providers.openai.apiKey or providers.ollama.apiBase)")
	}
	// ollama and vllm don't require API key; APIBase is sufficient
	if selected.APIKey == "" && selected.Name != "ollama" && selected.Name != "vllm" {
		return errors.New("no API key configured for model " + c.Agents.Defaults.Model + "; set providers in config.json (e.g. providers.openai.apiKey)")
	}
	if strings.TrimSpace(selected.APIBase) == "" {
		return fmt.Errorf("providers.%s.apiBase is required for OpenAI-compatible API", selected.Name)
	}
	return nil
}
