package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"luckclaw/internal/cli/tui"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const (
	configMainMenu = iota
	configAgent
	configAgentWorkspace
	configAgentModelInput
	configAgentProvider
	configProvidersList
	configProviderDetail
	configProviderApiKey
	configProviderApiBase
	configChannelsList
	configChannelDetail
	configGateway
	configToolsList
	configToolDetail
	configWebSearchList
	configWebSearchDetail
	configDone
)

var providerNames = []string{
	"openrouter", "openai", "anthropic", "deepseek", "groq",
	"zhipu", "dashscope", "moonshot", "aihubmix", "minimax",
	"volcengine", "siliconflow", "gemini", "vllm", "ollama", "custom",
}

var webSearchProviderNames = []string{"brave", "tavily", "duckduckgo", "perplexity", "searxng"}
var webSearchProviderLabels = map[string]string{
	"brave":      "Brave Search",
	"tavily":     "Tavily",
	"duckduckgo": "DuckDuckGo",
	"perplexity": "Perplexity",
	"searxng":    "SearXNG",
}

var channelNames = []string{
	"telegram", "discord", "feishu", "slack", "dingtalk", "qq", "workweixin",
}

var channelLabels = map[string]string{
	"telegram":   "Telegram",
	"discord":    "Discord",
	"feishu":     "Feishu",
	"slack":      "Slack",
	"dingtalk":   "DingTalk",
	"qq":         "QQ",
	"workweixin": "Work Weixin",
}

var builtInNames = []string{"agentBrowser", "agentMemory", "selfImproving", "clawdstrike", "evolver", "adaptiveReasoning"}

var allToolNames = append(
	[]string{"exec", "web.search", "web.fetch.firecrawl", "web.proxy", "browser"},
	builtInNames...,
)

const modelsShowLimit = 12 // Model list is collapsed by default above this count

var allToolLabels = map[string]string{
	"exec":                "Exec (shell commands)",
	"web.search":          "Web Search (Brave/Tavily/DuckDuckGo/Perplexity/SearXNG)",
	"web.fetch.firecrawl": "Web Fetch (Firecrawl)",
	"web.proxy":           "Web Proxy",
	"browser":             "Browser (remote)",
	"agentBrowser":        "Agent Browser (uses tools.browser when configured)",
	"agentMemory":         "Agent Memory (MEMORY.md + consolidation)",
	"selfImproving":       "Self-Improving (learn from errors)",
	"clawdstrike":         "ClawdStrike (security audit)",
	"evolver":             "Evolver (record lessons)",
	"adaptiveReasoning":   "Adaptive Reasoning (dynamic depth)",
}

func isBuiltInTool(name string) bool {
	for _, n := range builtInNames {
		if n == name {
			return true
		}
	}
	return false
}

type configModel struct {
	cfg       config.Config
	cfgPath   string
	step      int
	menuIndex int
	// agent
	modelIndex      int
	models          []string
	modelFetchErr   string
	modelsCollapsed bool // Collapse the configuration interface. Expand by pressing 'Tab'.
	providerIndex   int
	// providers: providerDetailIndex = which provider, providerDetailMenuIndex = which field (0=APIKey, 1=ApiBase)
	providerDetailIndex     int
	providerDetailMenuIndex int
	apiKeyInput             string
	apiBaseInput            string
	// channels
	channelIndex int
	channelInput string
	// gateway
	gatewayFieldIndex int
	gatewayInput      string
	// tools
	toolIndex int
	toolInput string
	// web search sub-menu (like providers)
	webSearchProviderIndex int
	// generic input
	inputLabel      string
	channelInputPos int // cursor position in runes
	width           int
	height          int
	saveErr         string
}

func (m configModel) Init() tea.Cmd {
	return nil
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch m.step {
		case configMainMenu:
			return m.updateMainMenu(msg)
		case configAgent:
			return m.updateAgentMenu(msg)
		case configAgentWorkspace, configProviderApiKey, configProviderApiBase, configAgentModelInput:
			return m.updateTextInput(msg)
		case configAgentProvider:
			return m.updateProviderSelect(msg)
		case configProvidersList:
			return m.updateProvidersList(msg)
		case configProviderDetail:
			return m.updateProviderDetail(msg)
		case configChannelsList:
			return m.updateChannelsList(msg)
		case configChannelDetail:
			return m.updateChannelDetail(msg)
		case configGateway:
			return m.updateGateway(msg)
		case configToolsList:
			return m.updateToolsList(msg)
		case configToolDetail:
			return m.updateToolDetail(msg)
		case configWebSearchList:
			return m.updateWebSearchList(msg)
		case configWebSearchDetail:
			return m.updateWebSearchDetail(msg)
		case configDone:
			switch msg.String() {
			case "q", "esc":
				m.step = configMainMenu
				m.menuIndex = 0
			case "enter":
				return m, tea.Quit
			case "ctrl+c":
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m configModel) updateMainMenu(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		if m.menuIndex < 5 {
			m.menuIndex++
		}
	case "enter":
		switch m.menuIndex {
		case 0:
			m.step = configAgent
			m.menuIndex = 0
		case 1:
			m.step = configProvidersList
			m.menuIndex = 0
		case 2:
			m.step = configChannelsList
			m.channelIndex = 0
		case 3:
			m.step = configGateway
			m.gatewayFieldIndex = 0
			m.menuIndex = 0
		case 4:
			m.step = configToolsList
			m.toolIndex = 0
		case 5:
			m.step = configDone
			config.Normalize(&m.cfg)
			if err := config.Save(m.cfgPath, m.cfg); err != nil {
				m.saveErr = err.Error()
			} else if saved, err := config.Load(m.cfgPath); err == nil {
				m.cfg = saved
			}
		}
	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m configModel) updateAgentMenu(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		if m.menuIndex < 2 {
			m.menuIndex++
		}
	case "enter":
		switch m.menuIndex {
		case 0:
			m.step = configAgentWorkspace
			m.inputLabel = "Workspace"
			m = m.withChannelInput(m.cfg.Agents.Defaults.Workspace)
		case 1:
			m.step = configAgentModelInput
			m = m.withChannelInput(m.cfg.Agents.Defaults.Model)
			result := m.cfg.ListAvailableModels()
			m.models = result.Models
			m.modelFetchErr = ""
			if len(result.FetchErrors) > 0 {
				m.modelFetchErr = strings.Join(result.FetchErrors, "; ")
			}
			if len(m.models) == 0 {
				m.models = []string{
					"anthropic/claude-opus-4-5", "anthropic/claude-sonnet-4",
					"openai/gpt-4o", "openai/gpt-4o-mini",
					"openrouter/anthropic/claude-sonnet-4",
					"deepseek/deepseek-chat", "deepseek/deepseek-r1",
					"zhipu/glm-4-flash", "moonshot/moonshot-v1",
				}
				m.modelFetchErr = "Could not fetch models. Configure API key first."
			}
			m.modelsCollapsed = len(m.models) > modelsShowLimit
		case 2:
			m.step = configAgentProvider
			m.providerIndex = 0
		}
	case "q", "esc":
		m.step = configMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateProviderSelect(msg tea.KeyMsg) (configModel, tea.Cmd) {
	opts := append([]string{"auto"}, providerNames...)
	switch msg.String() {
	case "up", "k":
		if m.providerIndex > 0 {
			m.providerIndex--
		}
	case "down", "j":
		if m.providerIndex < len(opts)-1 {
			m.providerIndex++
		}
	case "enter":
		opts := append([]string{"auto"}, providerNames...)
		if m.providerIndex >= 0 && m.providerIndex < len(opts) {
			m.cfg.Agents.Defaults.Provider = opts[m.providerIndex]
		}
		m.step = configAgent
		m.menuIndex = 0
	case "q", "esc":
		m.step = configAgent
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateProvidersList(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		if m.menuIndex < len(providerNames)-1 {
			m.menuIndex++
		}
	case "enter":
		m.providerDetailIndex = m.menuIndex
		m.providerDetailMenuIndex = 0
		m.step = configProviderDetail
		m.menuIndex = 0
	case "q", "esc":
		m.step = configMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateProviderDetail(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.providerDetailMenuIndex > 0 {
			m.providerDetailMenuIndex--
		}
	case "down", "j":
		if m.providerDetailMenuIndex < 1 {
			m.providerDetailMenuIndex++
		}
	case "enter":
		p := providerNames[m.providerDetailIndex]
		cfg := (&m.cfg).ProviderByName(p)
		if cfg == nil {
			break
		}
		if m.providerDetailMenuIndex == 0 {
			m.step = configProviderApiKey
			m = m.withChannelInput(cfg.APIKey)
		} else {
			m.step = configProviderApiBase
			m = m.withChannelInput(cfg.APIBase)
			if m.channelInput == "" {
				m = m.withChannelInput(config.DefaultAPIBase(p))
			}
		}
	case "q", "esc":
		m.step = configProvidersList
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateChannelsList(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.channelIndex > 0 {
			m.channelIndex--
		}
	case "down", "j":
		if m.channelIndex < len(channelNames)-1 {
			m.channelIndex++
		}
	case " ":
		if m.channelIndex >= 0 && m.channelIndex < len(channelNames) {
			name := channelNames[m.channelIndex]
			switch name {
			case "telegram":
				m.cfg.Channels.Telegram.Enabled = !m.cfg.Channels.Telegram.Enabled
			case "discord":
				m.cfg.Channels.Discord.Enabled = !m.cfg.Channels.Discord.Enabled
			case "feishu":
				m.cfg.Channels.Feishu.Enabled = !m.cfg.Channels.Feishu.Enabled
			case "slack":
				m.cfg.Channels.Slack.Enabled = !m.cfg.Channels.Slack.Enabled
			case "dingtalk":
				m.cfg.Channels.DingTalk.Enabled = !m.cfg.Channels.DingTalk.Enabled
			case "qq":
				m.cfg.Channels.QQ.Enabled = !m.cfg.Channels.QQ.Enabled
			case "workweixin":
				m.cfg.Channels.WorkWeixin.Enabled = !m.cfg.Channels.WorkWeixin.Enabled
			}
		}
	case "enter":
		m.step = configChannelDetail
		m.menuIndex = 0
	case "q", "esc":
		m.step = configMainMenu
		m.channelIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateChannelDetail(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		maxOpt := m.channelDetailOptCount()
		if m.menuIndex < maxOpt-1 {
			m.menuIndex++
		}
	case "enter":
		name := channelNames[m.channelIndex]
		m = m.enterChannelField(name)
	case "q", "esc":
		m.step = configChannelsList
		m.channelIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) enterChannelField(name string) configModel {
	switch name {
	case "telegram":
		if m.menuIndex == 0 {
			m.cfg.Channels.Telegram.Enabled = !m.cfg.Channels.Telegram.Enabled
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "Token"
			m = m.withChannelInput(m.cfg.Channels.Telegram.Token)
			m.providerDetailIndex = -1
		}
	case "discord":
		if m.menuIndex == 0 {
			m.cfg.Channels.Discord.Enabled = !m.cfg.Channels.Discord.Enabled
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "Token"
			m = m.withChannelInput(m.cfg.Channels.Discord.Token)
			m.providerDetailIndex = -6
		}
	case "feishu":
		if m.menuIndex == 0 {
			m.cfg.Channels.Feishu.Enabled = !m.cfg.Channels.Feishu.Enabled
		} else if m.menuIndex == 1 {
			m.step = configProviderApiKey
			m.inputLabel = "App ID"
			m = m.withChannelInput(m.cfg.Channels.Feishu.AppID)
			m.providerDetailIndex = -20
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "App Secret"
			m = m.withChannelInput(m.cfg.Channels.Feishu.AppSecret)
			m.providerDetailIndex = -21
		}
	case "slack":
		if m.menuIndex == 0 {
			m.cfg.Channels.Slack.Enabled = !m.cfg.Channels.Slack.Enabled
		} else if m.menuIndex == 1 {
			m.step = configProviderApiKey
			m.inputLabel = "Bot Token"
			m = m.withChannelInput(m.cfg.Channels.Slack.BotToken)
			m.providerDetailIndex = -22
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "App Token"
			m = m.withChannelInput(m.cfg.Channels.Slack.AppToken)
			m.providerDetailIndex = -23
		}
	case "dingtalk":
		if m.menuIndex == 0 {
			m.cfg.Channels.DingTalk.Enabled = !m.cfg.Channels.DingTalk.Enabled
		} else if m.menuIndex == 1 {
			m.step = configProviderApiKey
			m.inputLabel = "App Key"
			m = m.withChannelInput(m.cfg.Channels.DingTalk.AppKey)
			m.providerDetailIndex = -24
		} else if m.menuIndex == 2 {
			m.step = configProviderApiKey
			m.inputLabel = "App Secret"
			m = m.withChannelInput(m.cfg.Channels.DingTalk.AppSecret)
			m.providerDetailIndex = -25
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "Robot Code"
			m = m.withChannelInput(m.cfg.Channels.DingTalk.RobotCode)
			m.providerDetailIndex = -26
		}
	case "qq":
		if m.menuIndex == 0 {
			m.cfg.Channels.QQ.Enabled = !m.cfg.Channels.QQ.Enabled
		} else if m.menuIndex == 1 {
			m.step = configProviderApiKey
			m.inputLabel = "App ID"
			m = m.withChannelInput(m.cfg.Channels.QQ.AppID)
			m.providerDetailIndex = -27
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "Secret"
			m = m.withChannelInput(m.cfg.Channels.QQ.Secret)
			m.providerDetailIndex = -28
		}
	case "workweixin":
		if m.menuIndex == 0 {
			m.cfg.Channels.WorkWeixin.Enabled = !m.cfg.Channels.WorkWeixin.Enabled
		} else if m.menuIndex == 1 {
			m.step = configProviderApiKey
			m.inputLabel = "Bot ID"
			m = m.withChannelInput(m.cfg.Channels.WorkWeixin.BotID)
			m.providerDetailIndex = -29
		} else {
			m.step = configProviderApiKey
			m.inputLabel = "Secret"
			m = m.withChannelInput(m.cfg.Channels.WorkWeixin.Secret)
			m.providerDetailIndex = -30
		}
	default:
		m.step = configChannelsList
	}
	return m
}

func (m configModel) channelDetailOptCount() int {
	name := channelNames[m.channelIndex]
	switch name {
	case "telegram":
		return 2 // enable, token
	case "discord":
		return 2 // enable, token
	case "feishu":
		return 3 // enable, appId, appSecret
	case "slack":
		return 3 // enable, botToken, appToken
	case "dingtalk":
		return 4 // enable, appKey, appSecret, robotCode
	case "qq":
		return 3 // enable, appId, secret
	case "workweixin":
		return 3 // enable, botId, secret
	}
	return 1
}

func (m configModel) updateGateway(msg tea.KeyMsg) (configModel, tea.Cmd) {
	fields := []string{"host", "port", "heartbeatInterval"}
	switch msg.String() {
	case "up", "k":
		if m.gatewayFieldIndex > 0 {
			m.gatewayFieldIndex--
		}
	case "down", "j":
		if m.gatewayFieldIndex < len(fields)-1 {
			m.gatewayFieldIndex++
		}
	case "enter":
		switch m.gatewayFieldIndex {
		case 0:
			m.inputLabel = "Host"
			m = m.withChannelInput(m.cfg.Gateway.Host)
			m.providerDetailIndex = -2
			m.step = configProviderApiKey
		case 1:
			m.inputLabel = "Port"
			m = m.withChannelInput(strconv.Itoa(m.cfg.Gateway.Port))
			m.providerDetailIndex = -3
			m.step = configProviderApiKey
		case 2:
			m.inputLabel = "Heartbeat interval (sec)"
			m = m.withChannelInput(strconv.Itoa(m.cfg.Gateway.HeartbeatInterval))
			m.providerDetailIndex = -4
			m.step = configProviderApiKey
		}
	case "q", "esc":
		m.step = configMainMenu
		m.gatewayFieldIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateToolsList(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.toolIndex > 0 {
			m.toolIndex--
		}
	case "down", "j":
		if m.toolIndex < len(allToolNames)-1 {
			m.toolIndex++
		}
	case "enter":
		if m.toolIndex < len(allToolNames) && allToolNames[m.toolIndex] == "web.search" {
			m.step = configWebSearchList
			m.webSearchProviderIndex = 0
			m.menuIndex = 0
		} else {
			m.step = configToolDetail
			m.menuIndex = 0
		}
	case "q", "esc":
		m.step = configMainMenu
		m.toolIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateToolDetail(msg tea.KeyMsg) (configModel, tea.Cmd) {
	if m.toolIndex >= len(allToolNames) {
		m.step = configToolsList
		m.toolIndex = 0
		m.menuIndex = 0
		return m, nil
	}
	tool := allToolNames[m.toolIndex]
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		maxOpt := 1
		if !isBuiltInTool(tool) {
			switch tool {
			case "exec", "web.fetch.firecrawl":
				maxOpt = 1
			case "web.proxy":
				maxOpt = 3
			case "browser":
				maxOpt = 4 // enabled, remoteUrl, token, profile
			}
		}
		if m.menuIndex < maxOpt-1 {
			m.menuIndex++
		}
	case "enter", " ":
		switch tool {
		case "exec":
			if m.menuIndex == 0 {
				m.inputLabel = "Timeout (sec)"
				m = m.withChannelInput(strconv.Itoa(m.cfg.Tools.Exec.Timeout))
				m.providerDetailIndex = -10
				m.step = configProviderApiKey
			}
		case "web.fetch.firecrawl":
			if m.menuIndex == 0 {
				m.inputLabel = "API Key (Firecrawl)"
				m = m.withChannelInput(m.cfg.Tools.Web.Fetch.Firecrawl.APIKey)
				m.providerDetailIndex = -12
				m.step = configProviderApiKey
			}
		case "web.proxy":
			switch m.menuIndex {
			case 0:
				m.inputLabel = "httpProxy"
				m = m.withChannelInput(m.cfg.Tools.Web.HTTPProxy)
				m.providerDetailIndex = -13
				m.step = configProviderApiKey
			case 1:
				m.inputLabel = "httpsProxy"
				m = m.withChannelInput(m.cfg.Tools.Web.HTTPSProxy)
				m.providerDetailIndex = -14
				m.step = configProviderApiKey
			case 2:
				m.inputLabel = "allProxy"
				m = m.withChannelInput(m.cfg.Tools.Web.AllProxy)
				m.providerDetailIndex = -15
				m.step = configProviderApiKey
			}
		case "browser":
			switch m.menuIndex {
			case 0:
				m.cfg.Tools.Browser.Enabled = !m.cfg.Tools.Browser.Enabled
			case 1:
				m.inputLabel = "Remote URL (default wss://chrome.browserless.io)"
				m = m.withChannelInput(m.cfg.Tools.Browser.RemoteURL)
				m.providerDetailIndex = -16
				m.step = configProviderApiKey
			case 2:
				m.inputLabel = "Token (or BROWSERLESS_TOKEN env)"
				m = m.withChannelInput(m.cfg.Tools.Browser.Token)
				m.providerDetailIndex = -18
				m.step = configProviderApiKey
			case 3:
				m.inputLabel = "Profile"
				m = m.withChannelInput(m.cfg.Tools.Browser.Profile)
				m.providerDetailIndex = -17
				m.step = configProviderApiKey
			}
		default:
			// built-in tools: toggle on Space/Enter
			if isBuiltInTool(tool) && m.menuIndex == 0 {
				switch tool {
				case "agentBrowser":
					m.cfg.Tools.AgentBrowser = !m.cfg.Tools.AgentBrowser
				case "agentMemory":
					m.cfg.Tools.AgentMemory = !m.cfg.Tools.AgentMemory
				case "selfImproving":
					m.cfg.Tools.SelfImproving = !m.cfg.Tools.SelfImproving
				case "clawdstrike":
					m.cfg.Tools.ClawdStrike = !m.cfg.Tools.ClawdStrike
				case "evolver":
					m.cfg.Tools.Evolver = !m.cfg.Tools.Evolver
				case "adaptiveReasoning":
					m.cfg.Tools.AdaptiveReasoning = !m.cfg.Tools.AdaptiveReasoning
				}
			}
		}
	case "q", "esc":
		m.step = configToolsList
		m.toolIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateWebSearchList(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down", "j":
		if m.menuIndex < len(webSearchProviderNames)-1 {
			m.menuIndex++
		}
	case "enter":
		m.webSearchProviderIndex = m.menuIndex
		m.providerDetailMenuIndex = 0
		m.step = configWebSearchDetail
		m.menuIndex = 0
	case "q", "esc":
		m.step = configToolsList
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) updateWebSearchDetail(msg tea.KeyMsg) (configModel, tea.Cmd) {
	name := webSearchProviderNames[m.webSearchProviderIndex]
	opts := m.webSearchDetailOptCount(name)
	switch msg.String() {
	case "up", "k":
		if m.providerDetailMenuIndex > 0 {
			m.providerDetailMenuIndex--
		}
	case "down", "j":
		if m.providerDetailMenuIndex < opts-1 {
			m.providerDetailMenuIndex++
		}
	case "enter", " ":
		m = m.enterWebSearchField(name, m.providerDetailMenuIndex)
	case "q", "esc":
		m.step = configWebSearchList
		m.providerDetailMenuIndex = 0
		m.menuIndex = 0
	}
	return m, nil
}

func (m configModel) enterWebSearchField(providerName string, menuIndex int) configModel {
	switch providerName {
	case "brave":
		switch menuIndex {
		case 0:
			m.cfg.Tools.Web.Search.Brave.Enabled = !m.cfg.Tools.Web.Search.Brave.Enabled
		case 1:
			m.inputLabel = "Brave API Key"
			m = m.withChannelInput(m.cfg.Tools.Web.Search.Brave.APIKey)
			m.providerDetailIndex = -32
			m.step = configProviderApiKey
		}
	case "tavily":
		switch menuIndex {
		case 0:
			m.cfg.Tools.Web.Search.Tavily.Enabled = !m.cfg.Tools.Web.Search.Tavily.Enabled
		case 1:
			m.inputLabel = "Tavily API Key"
			m = m.withChannelInput(m.cfg.Tools.Web.Search.Tavily.APIKey)
			m.providerDetailIndex = -34
			m.step = configProviderApiKey
		}
	case "duckduckgo":
		m.cfg.Tools.Web.Search.DuckDuckGo.Enabled = !m.cfg.Tools.Web.Search.DuckDuckGo.Enabled
	case "perplexity":
		switch menuIndex {
		case 0:
			m.cfg.Tools.Web.Search.Perplexity.Enabled = !m.cfg.Tools.Web.Search.Perplexity.Enabled
		case 1:
			m.inputLabel = "Perplexity API Key"
			m = m.withChannelInput(m.cfg.Tools.Web.Search.Perplexity.APIKey)
			m.providerDetailIndex = -37
			m.step = configProviderApiKey
		}
	case "searxng":
		switch menuIndex {
		case 0:
			m.cfg.Tools.Web.Search.SearXNG.Enabled = !m.cfg.Tools.Web.Search.SearXNG.Enabled
		case 1:
			m.inputLabel = "SearXNG baseUrl"
			m = m.withChannelInput(m.cfg.Tools.Web.Search.SearXNG.BaseURL)
			m.providerDetailIndex = -39
			m.step = configProviderApiKey
		}
	}
	return m
}

func (m configModel) webSearchDetailOptCount(providerName string) int {
	switch providerName {
	case "brave", "tavily", "perplexity", "searxng":
		return 2 // enabled, apiKey/baseUrl
	case "duckduckgo":
		return 1 // enabled only
	default:
		return 1
	}
}

func (m *configModel) applyGenericInput() {
	val := strings.TrimSpace(m.channelInput)
	switch m.providerDetailIndex {
	case -1:
		m.cfg.Channels.Telegram.Token = val
	case -6:
		m.cfg.Channels.Discord.Token = val
	case -20:
		m.cfg.Channels.Feishu.AppID = val
	case -21:
		m.cfg.Channels.Feishu.AppSecret = val
	case -22:
		m.cfg.Channels.Slack.BotToken = val
	case -23:
		m.cfg.Channels.Slack.AppToken = val
	case -24:
		m.cfg.Channels.DingTalk.AppKey = val
	case -25:
		m.cfg.Channels.DingTalk.AppSecret = val
	case -26:
		m.cfg.Channels.DingTalk.RobotCode = val
	case -27:
		m.cfg.Channels.QQ.AppID = val
	case -28:
		m.cfg.Channels.QQ.Secret = val
	case -29:
		m.cfg.Channels.WorkWeixin.BotID = val
	case -30:
		m.cfg.Channels.WorkWeixin.Secret = val
	case -2:
		m.cfg.Gateway.Host = val
	case -3:
		if p, err := strconv.Atoi(val); err == nil && p > 0 {
			m.cfg.Gateway.Port = p
		}
	case -4:
		if v, err := strconv.Atoi(val); err == nil && v >= 0 {
			m.cfg.Gateway.HeartbeatInterval = v
		}
	case -10:
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			m.cfg.Tools.Exec.Timeout = v
		}
	case -32:
		m.cfg.Tools.Web.Search.Brave.APIKey = val
		if val != "" {
			m.cfg.Tools.Web.Search.Brave.Enabled = true
		}
	case -34:
		m.cfg.Tools.Web.Search.Tavily.APIKey = val
		if val != "" {
			m.cfg.Tools.Web.Search.Tavily.Enabled = true
		}
	case -37:
		m.cfg.Tools.Web.Search.Perplexity.APIKey = val
		if val != "" {
			m.cfg.Tools.Web.Search.Perplexity.Enabled = true
		}
	case -39:
		m.cfg.Tools.Web.Search.SearXNG.BaseURL = val
		if val != "" {
			m.cfg.Tools.Web.Search.SearXNG.Enabled = true
		}
	case -12:
		m.cfg.Tools.Web.Fetch.Firecrawl.APIKey = val
	case -13:
		m.cfg.Tools.Web.HTTPProxy = val
	case -14:
		m.cfg.Tools.Web.HTTPSProxy = val
	case -15:
		m.cfg.Tools.Web.AllProxy = val
	case -16:
		m.cfg.Tools.Browser.RemoteURL = val
	case -17:
		m.cfg.Tools.Browser.Profile = val
	case -18:
		m.cfg.Tools.Browser.Token = val
	}
}

func (m configModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	selStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b strings.Builder

	switch m.step {
	case configMainMenu:
		b.WriteString(titleStyle.Render("luckclaw config wizard") + "\n\n")
		menuItems := []string{
			"1. Agent (workspace, model, provider)",
			"2. Providers (API key, API base per provider)",
			"3. Channels (enable + options per platform)",
			"4. Gateway",
			"5. Tools (exec, web, browser, built-in)",
			"6. Save and exit",
		}
		for i, item := range menuItems {
			if i == m.menuIndex {
				b.WriteString(selStyle.Render("▸ "+item) + "\n")
			} else {
				b.WriteString("  " + item + "\n")
			}
		}
		b.WriteString("\n" + helpStyle.Render("↑/k ↓/j select  Enter confirm  q quit") + "\n")

	case configAgent:
		b.WriteString(titleStyle.Render("Agent config") + "\n\n")
		b.WriteString(dimStyle.Render("Workspace: "+m.cfg.Agents.Defaults.Workspace) + "\n")
		b.WriteString(dimStyle.Render("Model: "+m.cfg.Agents.Defaults.Model) + "\n")
		b.WriteString(dimStyle.Render("Provider: "+m.cfg.Agents.Defaults.Provider) + "\n\n")
		items := []string{"1. Workspace", "2. Model", "3. Provider"}
		for i, item := range items {
			if i == m.menuIndex {
				b.WriteString(selStyle.Render("▸ "+item) + "\n")
			} else {
				b.WriteString("  " + item + "\n")
			}
		}
		b.WriteString("\n" + helpStyle.Render("Enter select  q back") + "\n")

	case configAgentModelInput:
		b.WriteString(titleStyle.Render("Model (editable, type model name)") + "\n\n")
		bytePos := runePosToByteOffset(m.channelInput, m.channelInputPos)
		b.WriteString(m.channelInput[:bytePos] + "▌" + m.channelInput[bytePos:] + "\n\n")
		if m.modelFetchErr != "" {
			b.WriteString(dimStyle.Render("Note: "+m.modelFetchErr) + "\n\n")
		}
		b.WriteString(dimStyle.Render("Available models (for reference only):") + "\n")
		showModels := m.models
		if m.modelsCollapsed && len(m.models) > modelsShowLimit {
			showModels = m.models[:modelsShowLimit]
		}
		for _, mod := range showModels {
			b.WriteString("  " + mod + "\n")
		}
		if m.modelsCollapsed && len(m.models) > modelsShowLimit {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more (Tab to expand)", len(m.models)-modelsShowLimit)) + "\n")
		} else if !m.modelsCollapsed && len(m.models) > modelsShowLimit {
			b.WriteString(dimStyle.Render("  (Tab to collapse)") + "\n")
		}
		tabHint := ""
		if len(m.models) > modelsShowLimit {
			tabHint = "  Tab expand/collapse  "
		}
		b.WriteString("\n" + helpStyle.Render("Type value, Enter save  Esc back"+tabHint+"←→ move cursor") + "\n")

	case configAgentWorkspace, configProviderApiKey, configProviderApiBase:
		if m.providerDetailIndex < 0 {
			b.WriteString(titleStyle.Render(m.inputLabel) + "\n\n")
		} else if m.step == configAgentWorkspace {
			b.WriteString(titleStyle.Render("Workspace") + "\n\n")
		} else {
			p := ""
			if m.providerDetailIndex >= 0 && m.providerDetailIndex < len(providerNames) {
				p = providerNames[m.providerDetailIndex]
			}
			if m.step == configProviderApiKey {
				b.WriteString(titleStyle.Render("API Key: "+p) + "\n\n")
			} else {
				b.WriteString(titleStyle.Render("API Base: "+p) + "\n\n")
			}
		}
		bytePos := runePosToByteOffset(m.channelInput, m.channelInputPos)
		b.WriteString(m.channelInput[:bytePos] + "▌" + m.channelInput[bytePos:] + "\n\n")
		b.WriteString(helpStyle.Render("Type value, Enter save  Esc back  ←→ move cursor") + "\n")

	case configAgentProvider:
		b.WriteString(titleStyle.Render("Select provider (auto = detect from model)") + "\n\n")
		opts := append([]string{"auto"}, providerNames...)
		cur := m.cfg.Agents.Defaults.Provider
		for i, p := range opts {
			mark := "○"
			if p == cur {
				mark = "●"
			}
			line := p + " " + mark
			sel := ""
			if i == m.providerIndex {
				sel = selStyle.Render("▸ " + line)
			} else {
				sel = "  " + line
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("↑/k ↓/j select  Enter confirm  q back") + "\n")

	case configProvidersList:
		b.WriteString(titleStyle.Render("Providers") + "\n\n")
		for i, p := range providerNames {
			cfg := (&m.cfg).ProviderByName(p)
			hasKey := cfg != nil && (cfg.APIKey != "" || (p == "ollama" || p == "vllm") && cfg.APIBase != "")
			mark := "○"
			if hasKey {
				mark = "●"
			}
			sel := ""
			if i == m.menuIndex {
				sel = selStyle.Render(fmt.Sprintf("▸ %s %s", p, mark))
			} else {
				sel = fmt.Sprintf("  %s %s", p, mark)
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configProviderDetail:
		p := providerNames[m.providerDetailIndex]
		cfg := (&m.cfg).ProviderByName(p)
		apiKey := ""
		if cfg != nil && cfg.APIKey != "" {
			apiKey = "****"
		}
		apiBase := ""
		if cfg != nil {
			apiBase = cfg.APIBase
			if apiBase == "" {
				apiBase = config.DefaultAPIBase(p)
			}
		}
		b.WriteString(titleStyle.Render("Provider: "+p) + "\n\n")
		items := []string{fmt.Sprintf("1. API Key %s", apiKey), fmt.Sprintf("2. API Base %s", trunc(apiBase, 40))}
		for i, item := range items {
			sel := ""
			if i == m.providerDetailMenuIndex {
				sel = selStyle.Render("▸ " + item)
			} else {
				sel = "  " + item
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configChannelsList:
		b.WriteString(titleStyle.Render("Channels") + "\n\n")
		b.WriteString(dimStyle.Render("Space toggle enable  Enter edit options") + "\n\n")
		for i, name := range channelNames {
			label := channelLabels[name]
			if label == "" {
				label = name
			}
			enabled := m.getChannelEnabled(name)
			mark := "○"
			if enabled {
				mark = "●"
			}
			line := fmt.Sprintf("%s %s %s", name, label, mark)
			sel := ""
			if i == m.channelIndex {
				sel = selStyle.Render("▸ " + line)
			} else {
				sel = "  " + line
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Space toggle  Enter edit  q back") + "\n")

	case configChannelDetail:
		name := channelNames[m.channelIndex]
		b.WriteString(titleStyle.Render("Channel: "+name) + "\n\n")
		opts := m.channelDetailOptions(name)
		for i, opt := range opts {
			sel := ""
			if i == m.menuIndex {
				sel = selStyle.Render("▸ " + opt)
			} else {
				sel = "  " + opt
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configGateway:
		b.WriteString(titleStyle.Render("Gateway") + "\n\n")
		items := []string{
			fmt.Sprintf("Host: %s", m.cfg.Gateway.Host),
			fmt.Sprintf("Port: %d", m.cfg.Gateway.Port),
			fmt.Sprintf("Heartbeat interval: %d", m.cfg.Gateway.HeartbeatInterval),
		}
		for i, item := range items {
			sel := ""
			if i == m.gatewayFieldIndex {
				sel = selStyle.Render("▸ " + item)
			} else {
				sel = "  " + item
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configToolsList:
		b.WriteString(titleStyle.Render("Tools") + "\n\n")
		for i, t := range allToolNames {
			label := allToolLabels[t]
			if label == "" {
				label = t
			}
			sel := ""
			if i == m.toolIndex {
				sel = selStyle.Render("▸ " + label)
			} else {
				sel = "  " + label
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configWebSearchList:
		b.WriteString(titleStyle.Render("Web Search providers") + "\n\n")
		for i, p := range webSearchProviderNames {
			label := webSearchProviderLabels[p]
			if label == "" {
				label = p
			}
			mark := "○"
			if m.webSearchProviderEnabled(p) {
				mark = "●"
			}
			sel := ""
			if i == m.menuIndex {
				sel = selStyle.Render(fmt.Sprintf("▸ %s %s", label, mark))
			} else {
				sel = fmt.Sprintf("  %s %s", label, mark)
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  q back") + "\n")

	case configWebSearchDetail:
		p := webSearchProviderNames[m.webSearchProviderIndex]
		label := webSearchProviderLabels[p]
		if label == "" {
			label = p
		}
		b.WriteString(titleStyle.Render("Web Search: "+label) + "\n\n")
		opts := m.webSearchDetailOptions(p)
		for i, opt := range opts {
			sel := ""
			if i == m.providerDetailMenuIndex {
				sel = selStyle.Render("▸ " + opt)
			} else {
				sel = "  " + opt
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  Space toggle  q back") + "\n")

	case configToolDetail:
		tool := allToolNames[m.toolIndex]
		b.WriteString(titleStyle.Render("Tool: "+tool) + "\n\n")
		opts := m.toolDetailOptions(tool)
		for i, opt := range opts {
			sel := ""
			if i == m.menuIndex {
				sel = selStyle.Render("▸ " + opt)
			} else {
				sel = "  " + opt
			}
			b.WriteString(sel + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("Enter edit  Space toggle  q back") + "\n")

	case configDone:
		if m.saveErr != "" {
			b.WriteString(titleStyle.Render("Save failed") + "\n\n")
			b.WriteString(dimStyle.Render("Error: "+m.saveErr) + "\n\n")
		} else {
			b.WriteString(titleStyle.Render("Config saved") + "\n\n")
		}
		b.WriteString(fmt.Sprintf("  Config file: %s\n", m.cfgPath))
		b.WriteString(fmt.Sprintf("  Model: %s\n", m.cfg.Agents.Defaults.Model))
		var enabled []string
		for _, name := range channelNames {
			if m.getChannelEnabled(name) {
				enabled = append(enabled, name)
			}
		}
		b.WriteString(fmt.Sprintf("  Enabled channels: %s\n", strings.Join(enabled, ", ")))
		b.WriteString("\n" + helpStyle.Render("q back  Enter exit") + "\n")
	}

	out := b.String()
	w := m.width
	if w <= 0 {
		w = 80
	}
	return tui.WordWrapANSI(out, w)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func (m configModel) getBuiltInEnabled(name string) bool {
	switch name {
	case "agentBrowser":
		return m.cfg.Tools.AgentBrowser
	case "agentMemory":
		return m.cfg.Tools.AgentMemory
	case "selfImproving":
		return m.cfg.Tools.SelfImproving
	case "clawdstrike":
		return m.cfg.Tools.ClawdStrike
	case "evolver":
		return m.cfg.Tools.Evolver
	case "adaptiveReasoning":
		return m.cfg.Tools.AdaptiveReasoning
	}
	return false
}

func (m configModel) getChannelEnabled(name string) bool {
	switch name {
	case "telegram":
		return m.cfg.Channels.Telegram.Enabled
	case "discord":
		return m.cfg.Channels.Discord.Enabled
	case "feishu":
		return m.cfg.Channels.Feishu.Enabled
	case "slack":
		return m.cfg.Channels.Slack.Enabled
	case "dingtalk":
		return m.cfg.Channels.DingTalk.Enabled
	case "qq":
		return m.cfg.Channels.QQ.Enabled
	case "workweixin":
		return m.cfg.Channels.WorkWeixin.Enabled
	}
	return false
}

func (m configModel) channelDetailOptions(name string) []string {
	en := func(b bool) string {
		if b {
			return "enabled"
		}
		return "disabled"
	}
	hasValue := func(s string) string {
		if strings.TrimSpace(s) != "" {
			return "****"
		}
		return ""
	}
	switch name {
	case "telegram":
		token := hasValue(m.cfg.Channels.Telegram.Token)
		return []string{"1. " + en(m.cfg.Channels.Telegram.Enabled), "2. Token " + token}
	case "discord":
		token := hasValue(m.cfg.Channels.Discord.Token)
		return []string{"1. " + en(m.cfg.Channels.Discord.Enabled), "2. Token " + token}
	case "feishu":
		appID := hasValue(m.cfg.Channels.Feishu.AppID)
		appSecret := hasValue(m.cfg.Channels.Feishu.AppSecret)
		return []string{"1. " + en(m.cfg.Channels.Feishu.Enabled), "2. App ID " + appID, "3. App Secret " + appSecret}
	case "slack":
		botToken := hasValue(m.cfg.Channels.Slack.BotToken)
		appToken := hasValue(m.cfg.Channels.Slack.AppToken)
		return []string{"1. " + en(m.cfg.Channels.Slack.Enabled), "2. Bot Token " + botToken, "3. App Token " + appToken}
	case "dingtalk":
		appKey := hasValue(m.cfg.Channels.DingTalk.AppKey)
		appSecret := hasValue(m.cfg.Channels.DingTalk.AppSecret)
		robotCode := hasValue(m.cfg.Channels.DingTalk.RobotCode)
		return []string{"1. " + en(m.cfg.Channels.DingTalk.Enabled), "2. App Key " + appKey, "3. App Secret " + appSecret, "4. Robot Code " + robotCode}
	case "qq":
		appID := hasValue(m.cfg.Channels.QQ.AppID)
		secret := hasValue(m.cfg.Channels.QQ.Secret)
		return []string{"1. " + en(m.cfg.Channels.QQ.Enabled), "2. App ID " + appID, "3. Secret " + secret}
	case "workweixin":
		botID := hasValue(m.cfg.Channels.WorkWeixin.BotID)
		secret := hasValue(m.cfg.Channels.WorkWeixin.Secret)
		return []string{"1. " + en(m.cfg.Channels.WorkWeixin.Enabled), "2. Bot ID " + botID, "3. Secret " + secret}
	default:
		return []string{"1. " + en(m.getChannelEnabled(name))}
	}
}

func (m configModel) webSearchProviderEnabled(name string) bool {
	switch name {
	case "brave":
		return m.cfg.Tools.Web.Search.Brave.Enabled || strings.TrimSpace(m.cfg.Tools.Web.Search.Brave.APIKey) != ""
	case "tavily":
		return m.cfg.Tools.Web.Search.Tavily.Enabled || strings.TrimSpace(m.cfg.Tools.Web.Search.Tavily.APIKey) != ""
	case "duckduckgo":
		return m.cfg.Tools.Web.Search.DuckDuckGo.Enabled
	case "perplexity":
		return m.cfg.Tools.Web.Search.Perplexity.Enabled || strings.TrimSpace(m.cfg.Tools.Web.Search.Perplexity.APIKey) != ""
	case "searxng":
		return m.cfg.Tools.Web.Search.SearXNG.Enabled || strings.TrimSpace(m.cfg.Tools.Web.Search.SearXNG.BaseURL) != ""
	}
	return false
}

func (m configModel) webSearchDetailOptions(providerName string) []string {
	en := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	hasVal := func(s string) string {
		if strings.TrimSpace(s) != "" {
			return "***"
		}
		return ""
	}
	switch providerName {
	case "brave":
		return []string{
			"1. Enabled " + en(m.cfg.Tools.Web.Search.Brave.Enabled),
			"2. API Key " + hasVal(m.cfg.Tools.Web.Search.Brave.APIKey),
		}
	case "tavily":
		return []string{
			"1. Enabled " + en(m.cfg.Tools.Web.Search.Tavily.Enabled),
			"2. API Key " + hasVal(m.cfg.Tools.Web.Search.Tavily.APIKey),
		}
	case "duckduckgo":
		return []string{"1. Enabled " + en(m.cfg.Tools.Web.Search.DuckDuckGo.Enabled)}
	case "perplexity":
		return []string{
			"1. Enabled " + en(m.cfg.Tools.Web.Search.Perplexity.Enabled),
			"2. API Key " + hasVal(m.cfg.Tools.Web.Search.Perplexity.APIKey),
		}
	case "searxng":
		return []string{
			"1. Enabled " + en(m.cfg.Tools.Web.Search.SearXNG.Enabled),
			"2. baseUrl " + hasVal(m.cfg.Tools.Web.Search.SearXNG.BaseURL),
		}
	default:
		return []string{}
	}
}

func (m configModel) toolDetailOptions(tool string) []string {
	switch tool {
	case "exec":
		return []string{fmt.Sprintf("Timeout: %d", m.cfg.Tools.Exec.Timeout)}
	case "web.fetch.firecrawl":
		return []string{fmt.Sprintf("API Key: %s", m.cfg.Tools.Web.Fetch.Firecrawl.APIKey)}
	case "web.proxy":
		return []string{
			fmt.Sprintf("httpProxy: %s", m.cfg.Tools.Web.HTTPProxy),
			fmt.Sprintf("httpsProxy: %s", m.cfg.Tools.Web.HTTPSProxy),
			fmt.Sprintf("allProxy: %s", m.cfg.Tools.Web.AllProxy),
		}
	case "browser":
		en := "off"
		if m.cfg.Tools.Browser.Enabled {
			en = "on"
		}
		tok := "(env)"
		if m.cfg.Tools.Browser.Token != "" {
			tok = "***"
		}
		return []string{
			fmt.Sprintf("Enabled: %s", en),
			fmt.Sprintf("Remote URL: %s", m.cfg.Tools.Browser.RemoteURL),
			fmt.Sprintf("Token: %s", tok),
			fmt.Sprintf("Profile: %s", m.cfg.Tools.Browser.Profile),
		}
	case "agentBrowser", "agentMemory", "selfImproving", "clawdstrike", "evolver", "adaptiveReasoning":
		en := "off"
		if m.getBuiltInEnabled(tool) {
			en = "on"
		}
		return []string{fmt.Sprintf("Enabled: %s", en)}
	default:
		return []string{}
	}
}

func (m configModel) updateTextInput(msg tea.KeyMsg) (configModel, tea.Cmd) {
	runeCount := utf8.RuneCountInString(m.channelInput)
	if m.channelInputPos < 0 {
		m.channelInputPos = 0
	}
	if m.channelInputPos > runeCount {
		m.channelInputPos = runeCount
	}

	val := strings.TrimSpace(m.channelInput)
	isEnter := msg.String() == "enter" || msg.Type == tea.KeyEnter

	switch {
	case isEnter:
		switch m.step {
		case configAgentWorkspace:
			if val != "" {
				m.cfg.Agents.Defaults.Workspace = val
			}
			m.step = configAgent
		case configAgentModelInput:
			if val != "" {
				m.cfg.Agents.Defaults.Model = val
			}
			m.step = configAgent
		case configProviderApiKey, configProviderApiBase:
			if m.providerDetailIndex >= 0 && m.providerDetailIndex < len(providerNames) {
				p := providerNames[m.providerDetailIndex]
				cfg := (&m.cfg).ProviderByName(p)
				if cfg != nil {
					if m.step == configProviderApiKey {
						cfg.APIKey = val
					} else {
						cfg.APIBase = val
					}
				}
				m.step = configProviderDetail
				m.providerDetailMenuIndex = 0
			} else if m.providerDetailIndex < 0 {
				(&m).applyGenericInput()
				m.step = m.genericInputReturnStep()
				switch m.step {
				case configChannelDetail:
					m.menuIndex = 0
				case configGateway:
					m.gatewayFieldIndex = 0
					m.menuIndex = 0
				case configToolDetail:
					m.menuIndex = 0
				case configWebSearchDetail:
					m.providerDetailMenuIndex = 0
				case configChannelsList:
					m.channelIndex = 0
					m.menuIndex = 0
				}
			}
		}
	case msg.String() == "esc":
		switch m.step {
		case configAgentWorkspace, configAgentModelInput:
			m.step = configAgent
			m.menuIndex = 0
		case configProviderApiKey, configProviderApiBase:
			if m.providerDetailIndex >= 0 && m.providerDetailIndex < len(providerNames) {
				m.step = configProviderDetail
				m.providerDetailMenuIndex = 0
			} else {
				m.step = m.genericInputReturnStep()
				switch m.step {
				case configChannelDetail:
					m.menuIndex = 0
				case configGateway:
					m.gatewayFieldIndex = 0
					m.menuIndex = 0
				case configToolDetail:
					m.menuIndex = 0
				case configWebSearchDetail:
					m.providerDetailMenuIndex = 0
				case configChannelsList:
					m.channelIndex = 0
					m.menuIndex = 0
				}
			}
		}
	case msg.String() == "tab" && m.step == configAgentModelInput && len(m.models) > modelsShowLimit:
		m.modelsCollapsed = !m.modelsCollapsed
	case msg.String() == "left":
		if m.channelInputPos > 0 {
			m.channelInputPos--
		}
	case msg.String() == "right":
		if m.channelInputPos < runeCount {
			m.channelInputPos++
		}
	case msg.String() == "backspace":
		if m.channelInputPos > 0 {
			bytePos := runePosToByteOffset(m.channelInput, m.channelInputPos-1)
			_, size := utf8.DecodeRuneInString(m.channelInput[bytePos:])
			m.channelInput = m.channelInput[:bytePos] + m.channelInput[bytePos+size:]
			m.channelInputPos--
		}
	default:
		if msg.Type == tea.KeyRunes {
			// Use Runes directly: msg.String() wraps paste in [brackets] for binding
			toInsert := string(msg.Runes)
			if toInsert != "" {
				bytePos := runePosToByteOffset(m.channelInput, m.channelInputPos)
				m.channelInput = m.channelInput[:bytePos] + toInsert + m.channelInput[bytePos:]
				m.channelInputPos += utf8.RuneCountInString(toInsert)
			}
		}
	}
	return m, nil
}

func runePosToByteOffset(s string, runePos int) int {
	for i := range s {
		if runePos == 0 {
			return i
		}
		runePos--
	}
	return len(s)
}

func (m configModel) withChannelInput(s string) configModel {
	m.channelInput = s
	m.channelInputPos = utf8.RuneCountInString(s)
	return m
}

func (m configModel) genericInputReturnStep() int {
	switch m.providerDetailIndex {
	case -1, -6, -20, -21, -22, -23, -24, -25, -26, -27, -28, -29, -30:
		return configChannelDetail
	case -2, -3, -4:
		return configGateway
	case -32, -34, -37, -39:
		return configWebSearchDetail
	case -10, -12, -13, -14, -15, -16, -17, -18:
		return configToolDetail
	}
	return configChannelsList
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Interactive config wizard (agent, providers, channels, gateway, tools)",
		Long:  "Launch interactive config wizard to configure agent, providers, channels, gateway, and tools.",
	}
	exampleCmd := &cobra.Command{
		Use:   "example",
		Short: "Write full config template (all options, defaults filled) to file or stdout",
		Long:  "Output the complete config template with all configurable options. Default values are filled; others left empty.",
		RunE: func(c *cobra.Command, args []string) error {
			out, _ := c.Flags().GetString("output")
			if out != "" {
				if err := config.WriteDefaultFullTemplate(out); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(c.OutOrStdout(), "Wrote %s\n", out)
				return nil
			}
			b, err := json.MarshalIndent(config.DefaultFullTemplateMap(), "", "  ")
			if err != nil {
				return err
			}
			_, _ = c.OutOrStdout().Write(append(b, '\n'))
			return nil
		},
	}
	exampleCmd.Flags().StringP("output", "o", "", "Write to file (default: stdout)")
	cmd.AddCommand(exampleCmd)

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Merge new config options into existing config without overwriting",
		Long:  "Add any new config keys from the default template to your existing config. Existing values are preserved.",
		RunE: func(c *cobra.Command, args []string) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			if err := config.UpdateConfig(cfgPath); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(c.OutOrStdout(), "✓ Updated config at %s (new options added, existing values preserved)\n", cfgPath)
			return nil
		},
	}
	cmd.AddCommand(updateCmd)

	cmd.RunE = func(c *cobra.Command, args []string) error {
		cfgPath, err := paths.ConfigPath()
		if err != nil {
			return err
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return err
		}

		model := configModel{
			cfg:       cfg,
			cfgPath:   cfgPath,
			step:      configMainMenu,
			menuIndex: 0,
		}
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(c.OutOrStdout(), "Config done. Run `luckclaw gateway` to start.")
		return nil
	}
	return cmd
}
