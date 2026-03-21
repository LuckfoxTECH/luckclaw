package tui

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"luckclaw/internal/agent"
	"luckclaw/internal/bus"
	"luckclaw/internal/config"
	"luckclaw/internal/logging"
	"luckclaw/internal/paths"
	"luckclaw/internal/providers/openaiapi"
	"luckclaw/internal/service"
	sessionpkg "luckclaw/internal/session"
	"luckclaw/internal/tools"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// tuiStatus holds shared state for the TUI status bar (OpenClaw-style).
type tuiStatus struct {
	mu sync.Mutex

	// Version info
	Version string
	// Status: idle | running | thinking | stop
	Status string
	// Running duration when status=running
	RunningSince time.Time
	// Connected/ready
	Connected bool
	// Current model ID
	Model string
	// Current terminal label (local or name(user@host))
	Terminal string
	// Token usage from last completed turn
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// Context window size (approximate, for display)
	ContextWindow int
	// Context sources loaded (USER, SOUL, MEMORY) as compact label e.g. "U+S+M"
	ContextSources string
	// Context mode: "simple" (compact) or "normal" (full context)
	ContextMode string
	// Config for ContextWindowForModel (nil = use default guess)
	cfg *config.Config
}

func (s *tuiStatus) SetStatus(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = v
	if v == "running" || v == "thinking" {
		s.RunningSince = time.Now()
	}
}

func (s *tuiStatus) SetModel(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Model = m
	if s.cfg != nil {
		s.ContextWindow = s.cfg.ContextWindowForModel(m)
	} else {
		s.ContextWindow = config.Config{}.ContextWindowForModel(m)
	}
}

func (s *tuiStatus) SetTerminal(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(v) == "" {
		return
	}
	s.Terminal = v
}

func (s *tuiStatus) SetTokens(prompt, completion, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PromptTokens = prompt
	s.CompletionTokens = completion
	s.TotalTokens = total
}

func (s *tuiStatus) SetContextSources(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v != "" {
		s.ContextSources = v
	}
}

func (s *tuiStatus) SetContextMode(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v != "" {
		s.ContextMode = v
	}
}

func (s *tuiStatus) SetVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Version = v
}

func (s *tuiStatus) Get() (status string, runningDur time.Duration, connected bool, model string, terminal string, promptTok, completionTok, totalTok, ctxWindow int, ctxSources, ctxMode string, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status = s.Status
	if s.Status == "running" || s.Status == "thinking" {
		runningDur = time.Since(s.RunningSince)
	}
	connected = s.Connected
	model = s.Model
	terminal = s.Terminal
	promptTok = s.PromptTokens
	completionTok = s.CompletionTokens
	totalTok = s.TotalTokens
	ctxWindow = s.ContextWindow
	ctxSources = s.ContextSources
	ctxMode = s.ContextMode
	version = s.Version
	return
}

func formatTokens(used, max int) string {
	if max <= 0 {
		//return fmt.Sprintf("%d/unknown", used)
		return fmt.Sprintf("%d", used)
	}
	if max >= 1000 {
		//return fmt.Sprintf("%d/%dk", used, max/1000)
		return fmt.Sprintf("%d", used)
	}
	//return fmt.Sprintf("%d/%d", used, max)
	return fmt.Sprintf("%d", used)
}

// slashCommand holds a slash command for completion.
type slashCommand struct {
	Name string
	Desc string
}

func getSlashCommands(cfg config.Config) []slashCommand {
	cmdList := []slashCommand{
		{"/help", "Show help message"},
		{"/new", "Start new conversation"},
		{"/compact", "Consolidate memory and start new conversation"},
		{"/reset", "Clear conversation without memory consolidation"},
		{"/verbose", "Toggle verbose mode"},
		{"/terminal", "Manage and switch remote terminal control"},
		{"/models", "List available models or switch model: /models <id>"},
		{"/plan", "Planning mode: /plan <task> | /plan on | /plan off"},
		{"/skill", "List skills; /skill <name> to run a skill"},
		{"/simple", "Toggle simple mode (compact context, saves tokens)"},
		{"/summary", "Summarize current conversation"},
		{"/luck", "Record last completed task as lucky; /luck last to preview; /luck list to show events"},
		{"/badluck", "Record last completed task as bad luck; /badluck last to preview; /badluck list to show events"},
		{"/turn", "Temporary perspective shift; /turn reroll | status | on | off | save | clear"},
		{"/modbus", "Manage per-workspace Modbus devices and config: list, add, info, rm, use, off"},
		{"/mqtt", "Manage MQTT connections: list, add, connect, disconnect, info, rm, logs"},
		{"/stop", "Cancel current processing"},
		{"/sessions", "Manage and switch between sessions"},
		{"/heartbeat", "Show heartbeat status (gateway only)"},
		{"/mcp", "List connected MCP tools"},
		{"/subagents", "List/kill/info/spawn subagent runs"},
		{"/service", "Manage services: /service list | add | rm | info | start | stop | search"},
	}
	if cfg.SlashCommands != nil {
		for name, c := range cfg.SlashCommands {
			desc := c.Description
			if desc == "" {
				desc = name
			}
			if !strings.HasPrefix(name, "/") {
				name = "/" + name
			}
			cmdList = append(cmdList, slashCommand{name, desc})
		}
	}
	return cmdList
}

func filterSlashCompletions(prefix string, all []slashCommand) []slashCommand {
	prefix = strings.TrimSpace(prefix)
	// Only show completions when input starts with /
	if prefix == "" || !strings.HasPrefix(prefix, "/") {
		return nil
	}
	prefixLower := strings.ToLower(prefix)
	if prefixLower == "/" {
		return all
	}
	var out []slashCommand
	for _, c := range all {
		if strings.HasPrefix(strings.ToLower(c.Name), prefixLower) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func NewCmd() *cobra.Command {
	var session string
	var model string

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive TUI mode (OpenClaw-style) with status bar",
		Long:  "Start luckclaw in TUI mode with running status, current model, and token usage in the status bar.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("session") {
				session = fmt.Sprintf("tui:%d", time.Now().Unix())
			}
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(model) == "" {
				model = cfg.Agents.Defaults.Model
			}
			selected := cfg.SelectProvider(model)
			if selected == nil || strings.TrimSpace(selected.APIKey) == "" {
				return exitf(cmd, "Error: No API key configured. Set one in ~/.luckclaw/config.json under providers section")
			}
			if strings.TrimSpace(selected.APIBase) == "" {
				return exitf(cmd, "Error: No apiBase configured for provider %s", selected.Name)
			}

			client := &openaiapi.Client{
				APIKey:       selected.APIKey,
				APIBase:      selected.APIBase,
				ExtraHeaders: selected.ExtraHeaders,
				HTTPClient:   openaiapi.NewHTTPClientWithProxy(&cfg.Tools.Web, 120*time.Second),
			}
			sessions := sessionpkg.NewManager()
			if ws, err := paths.ExpandUser(cfg.Agents.Defaults.Workspace); err == nil && ws != "" {
				sessions.Workspace = ws
			}
			logger := logging.NewMemoryLogger(2000)
			loop := agent.New(cfg, client, sessions, model, logger)
			tuiBus := bus.New()
			loop.SetBus(tuiBus)

			// Restore auto-start services
			if svcReg := service.GlobalRegistry(); svcReg.Load() == nil {
				if errs := svcReg.RestoreAutoStart(context.Background()); len(errs) > 0 {
					for _, err := range errs {
						log.Printf("[service] auto-start warning: %v", err)
					}
				}
			}

			status := &tuiStatus{
				Status:        "idle",
				Connected:     true,
				Model:         model,
				ContextWindow: cfg.ContextWindowForModel(model),
				cfg:           &cfg,
				Version:       cmd.Root().Version,
			}
			loop.OnModelResolved = func(channel, chatID string, m string) {
				status.SetModel(m)
			}
			loop.OnTerminalResolved = func(channel, chatID string, term string) {
				status.SetTerminal(term)
			}
			loop.OnTurnComplete = func(channel, chatID string, m string, prompt, completion, total int) {
				status.SetModel(m)
				status.SetTokens(prompt, completion, total)
			}
			loop.OnContextInfo = func(channel, chatID string, sources string, mode string) {
				status.SetContextSources(sources)
				status.SetContextMode(mode)
			}

			thinkingText := ""
			if ux := (&cfg).UXPtr(); ux != nil && (ux.Typing || ux.Placeholder.Enabled) && ux.Placeholder.Text != "" {
				thinkingText = ux.Placeholder.Text
			}
			prog := &tuiProgram{
				status:        status,
				loop:          loop,
				session:       session,
				messages:      []chatMessage{},
				input:         "",
				runMode:       tools.RunModeBuild,
				historyIndex:  -1,
				slashCommands: getSlashCommands(cfg),
				thinkingText:  thinkingText,
				renamingIdx:   -1,
				mouseEnabled:  true,
			}
			p := tea.NewProgram(prog, tea.WithMouseCellMotion())
			go func() {
				for msg := range tuiBus.Outbound {
					p.Send(outboundMsg{Content: msg.Content, Type: msg.Type})
				}
			}()
			_, err = p.Run()
			return err
		},
	}

	cmd.Flags().StringVarP(&session, "session", "s", "tui:main", "Session ID")
	cmd.Flags().StringVar(&model, "model", "", "Model override")

	return cmd
}

type chatMessage struct {
	role    string // "user" | "assistant"
	content string
	runMode tools.RunMode
}

type tuiProgram struct {
	status        *tuiStatus
	loop          *agent.AgentLoop
	session       string
	messages      []chatMessage
	input         string
	inputPos      int // Cursor position (rune count): 0=start, len=end
	runMode       tools.RunMode
	inputHistory  []string
	historyIndex  int
	historyDraft  string
	slashCommands []slashCommand
	// Completion state: when input starts with /, show completions
	completionIndex int // selected index in filtered list
	animFrame       int // loading animation frame (0-3)
	width           int
	height          int
	// progressContent: tool progress when verbose, updated in real-time
	progressContent string
	// scrollOffset for content area (lines)
	scrollOffset int
	// thinkingText from ux.placeholder.text for typing indicator
	thinkingText string
	mouseEnabled bool

	// Sessions management
	showSessions  bool
	sessionList   []sessionpkg.SessionInfo
	sessionIdx    int
	sessionScroll int
	renamingIdx   int // -1 if not renaming
	renameInput   string
}

type (
	agentDoneMsg struct {
		out      string
		err      error
		streamed bool
	}
	agentStartMsg     struct{}
	tickMsg           struct{}
	animTickMsg       struct{}
	outboundMsg       struct{ Content, Type string }
	sessionsLoadedMsg struct {
		sessions []sessionpkg.SessionInfo
	}
)

func (p *tuiProgram) Init() tea.Cmd {
	return nil
}

func (p *tuiProgram) filteredCompletions() []slashCommand {
	return filterSlashCompletions(p.input, p.slashCommands)
}

func (p *tuiProgram) resetHistoryBrowsing() {
	p.historyIndex = -1
	p.historyDraft = ""
}

func (p *tuiProgram) addInputHistory(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if n := len(p.inputHistory); n > 0 && p.inputHistory[n-1] == s {
		return
	}
	p.inputHistory = append(p.inputHistory, s)
	const maxHistory = 200
	if len(p.inputHistory) > maxHistory {
		p.inputHistory = p.inputHistory[len(p.inputHistory)-maxHistory:]
	}
}

func (p *tuiProgram) historyUp() bool {
	if len(p.inputHistory) == 0 {
		return false
	}
	if p.historyIndex == -1 {
		if strings.TrimSpace(p.input) != "" {
			return false
		}
		p.historyDraft = p.input
		p.historyIndex = len(p.inputHistory) - 1
	} else if p.historyIndex > 0 {
		p.historyIndex--
	}
	p.input = p.inputHistory[p.historyIndex]
	p.inputPos = utf8.RuneCountInString(p.input)
	return true
}

func (p *tuiProgram) historyDown() bool {
	if p.historyIndex == -1 {
		return false
	}
	if p.historyIndex < len(p.inputHistory)-1 {
		p.historyIndex++
		p.input = p.inputHistory[p.historyIndex]
		p.inputPos = utf8.RuneCountInString(p.input)
		return true
	}
	p.historyIndex = -1
	p.input = p.historyDraft
	p.inputPos = utf8.RuneCountInString(p.input)
	p.historyDraft = ""
	return true
}

// contentLineCount returns (totalLines, contentHeight) for scroll clamping.
func (p *tuiProgram) contentLineCount() (int, int) {
	contentHeight := p.height - 5 // TopBar(1) + Header(1) + Input(1) + Status(1) + Footer(1)
	if len(p.filteredCompletions()) > 0 {
		contentHeight = p.height - 7 // Including completions block (approx)
	}
	if contentHeight < 3 {
		contentHeight = 16
	}
	content := p.buildContentString()
	lines := strings.Split(content, "\n")
	return len(lines), contentHeight
}

func (p *tuiProgram) scrollMax(totalLines, contentHeight int) int {
	if totalLines <= contentHeight {
		return 0
	}
	return totalLines - contentHeight
}

// renderMarkdown renders markdown to ANSI for terminal display (lightweight, no syntax highlighting).
func (p *tuiProgram) renderMarkdown(s string) string {
	if s == "" {
		return ""
	}
	width := p.width
	if width <= 0 {
		width = 80
	}
	return renderMarkdownSimple(s, width)
}

// buildContentString returns the full content string (for line count and display).
func (p *tuiProgram) buildContentString() string {
	width := p.width
	if width <= 0 {
		width = 80
	}
	assistantStyle := lipgloss.NewStyle().Width(width)
	var contentBlocks []string
	for _, msg := range p.messages {
		if msg.role == "user" {
			wrapped := wordWrapANSI(msg.content, width-4)
			// Apply user background and border color dynamically based on runMode when sent
			accentColor := lipgloss.Color("62") // default Build mode border color
			if msg.runMode == tools.RunModePlan {
				accentColor = lipgloss.Color("39") // Plan mode border color
			}
			userStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("252")). // Ensure text is visible
				Padding(0, 1).
				Border(lipgloss.Border{
					Left: "┃",
				}, false, false, false, true).
				BorderForeground(accentColor).
				Width(width)

			contentBlocks = append(contentBlocks, userStyle.Render(wrapped))
		} else {
			rendered := p.renderMarkdown(msg.content)
			contentBlocks = append(contentBlocks, assistantStyle.Render(rendered))
		}
	}
	content := strings.Join(contentBlocks, "\n\n")
	st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
	isRunning := st == "running" || st == "thinking"
	if isRunning {
		if p.progressContent != "" {
			thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
			content += "\n\n" + thinkingStyle.Render(wordWrapANSI(p.progressContent, width))
		} else {
			anim := loadingFrames[p.animFrame%len(loadingFrames)]
			thinkingText := p.thinkingText
			if thinkingText == "" {
				thinkingText = "thinking..."
			}
			thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("109")).Italic(true)
			content += "\n\n" + thinkingStyle.Render(anim+" "+thinkingText)
		}
	}
	return content
}

// handleSend returns tea.Cmd for sending a message. For /stop, cancels in-flight
// processing and sets status to "stop" instead of running the agent.
func (p *tuiProgram) handleSend(input string) tea.Cmd {
	trimmed := strings.TrimSpace(input)
	if trimmed == "/stop" {
		p.loop.Queue.CancelSessionAndChildren(p.session)
		p.status.SetStatus("stop")
		p.messages = append(p.messages, chatMessage{role: "assistant", content: "Stopped."})
		return nil
	}
	if trimmed == "/sessions" {
		p.showSessions = true
		p.sessionIdx = 0
		return p.loadSessionsCmd()
	}
	if trimmed == "/new" {
		newSession := fmt.Sprintf("tui:%d", time.Now().Unix())
		p.session = newSession
		p.messages = []chatMessage{}
		p.scrollOffset = 0
		p.resetHistoryBrowsing()
		p.addInputHistory(trimmed)
		p.messages = append(p.messages, chatMessage{role: "assistant", content: "New session created."})
		return nil
	}
	return tea.Batch(
		tea.Cmd(func() tea.Msg { return agentStartMsg{} }),
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{} }),
		tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return animTickMsg{} }),
		p.runAgentAsync(input),
	)
}

func (p *tuiProgram) loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		allInfos, _ := p.loop.Sessions.ListSessionInfos()
		var tuiInfos []sessionpkg.SessionInfo
		for _, info := range allInfos {
			if strings.HasPrefix(info.Key, "tui:") {
				tuiInfos = append(tuiInfos, info)
			}
		}
		return sessionsLoadedMsg{sessions: tuiInfos}
	}
}

func (p *tuiProgram) updateSessions(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case sessionsLoadedMsg:
		p.sessionList = m.sessions
		return p, nil
	case tea.WindowSizeMsg:
		p.width = m.Width
		p.height = m.Height
		return p, nil
	case tea.KeyMsg:
		if p.renamingIdx != -1 {
			switch m.String() {
			case "enter":
				newSummary := strings.TrimSpace(p.renameInput)
				if newSummary != "" {
					_ = p.loop.Sessions.SetSummary(p.sessionList[p.renamingIdx].Key, newSummary)
				}
				p.renamingIdx = -1
				return p, p.loadSessionsCmd()
			case "esc":
				p.renamingIdx = -1
				return p, nil
			case "backspace":
				if len(p.renameInput) > 0 {
					runes := []rune(p.renameInput)
					p.renameInput = string(runes[:len(runes)-1])
				}
				return p, nil
			default:
				if len(m.Runes) > 0 {
					p.renameInput += string(m.Runes)
				}
				return p, nil
			}
		}

		switch m.String() {
		case "esc", "q":
			p.showSessions = false
			return p, nil
		case "up":
			if p.sessionIdx > 0 {
				p.sessionIdx--
				if p.sessionIdx < p.sessionScroll {
					p.sessionScroll = p.sessionIdx
				}
			}
			return p, nil
		case "down":
			if p.sessionIdx < len(p.sessionList)-1 {
				p.sessionIdx++
				contentHeight := p.height - 4
				if p.sessionIdx >= p.sessionScroll+contentHeight {
					p.sessionScroll = p.sessionIdx - contentHeight + 1
				}
			}
			return p, nil
		case "enter":
			if p.sessionIdx >= 0 && p.sessionIdx < len(p.sessionList) {
				oldSession := p.session
				p.session = p.sessionList[p.sessionIdx].Key
				if oldSession != p.session {
					if trimmed := strings.TrimSpace(p.input); trimmed != "" {
						p.addInputHistory(trimmed)
					}
					p.input = ""
					p.inputPos = 0
					p.messages = []chatMessage{}
					p.scrollOffset = 0
					p.resetHistoryBrowsing()
					s, _ := p.loop.Sessions.GetOrCreate(p.session)
					for _, msg := range s.Messages {
						role, _ := msg["role"].(string)
						content, _ := msg["content"].(string)

						mode := tools.RunModeBuild
						if rm, ok := msg["run_mode"].(string); ok && rm != "" {
							mode = tools.RunMode(rm)
						}

						p.messages = append(p.messages, chatMessage{role: role, content: content, runMode: mode})
						if role == "user" {
							p.addInputHistory(content)
						}
					}

					// Scroll to bottom when loading an existing session
					totalLines, contentHeight := p.contentLineCount()
					p.scrollOffset = p.scrollMax(totalLines, contentHeight)
				}
				p.showSessions = false
				return p, nil
			}
		case "d":
			if p.sessionIdx >= 0 && p.sessionIdx < len(p.sessionList) {
				_ = p.loop.Sessions.Delete(p.sessionList[p.sessionIdx].Key)
				if p.sessionIdx >= len(p.sessionList)-1 && p.sessionIdx > 0 {
					p.sessionIdx--
				}
				return p, p.loadSessionsCmd()
			}
		case "r":
			if p.sessionIdx >= 0 && p.sessionIdx < len(p.sessionList) {
				p.renamingIdx = p.sessionIdx
				p.renameInput = p.sessionList[p.sessionIdx].Summary
				return p, nil
			}
		}
	}
	return p, nil
}

func (p *tuiProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if p.showSessions {
		return p.updateSessions(msg)
	}
	switch m := msg.(type) {
	case tea.MouseMsg:
		if m.Button == tea.MouseButtonWheelUp {
			if p.scrollOffset > 0 {
				p.scrollOffset--
			}
			return p, nil
		}
		if m.Button == tea.MouseButtonWheelDown {
			totalLines, contentHeight := p.contentLineCount()
			maxScroll := p.scrollMax(totalLines, contentHeight)
			if p.scrollOffset < maxScroll {
				p.scrollOffset++
			}
			return p, nil
		}
		return p, nil
	case tea.KeyMsg:
		filtered := p.filteredCompletions()
		hasCompletions := len(filtered) > 0

		switch m.String() {
		case "ctrl+c", "ctrl+d":
			return p, tea.Quit
		case "alt+m":
			p.mouseEnabled = !p.mouseEnabled
			if p.mouseEnabled {
				return p, func() tea.Msg { return tea.EnableMouseCellMotion() }
			}
			return p, func() tea.Msg { return tea.DisableMouse() }
		case "tab":
			if hasCompletions {
				p.completionIndex = (p.completionIndex + 1) % len(filtered)
				return p, nil
			}
			st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
			if st != "running" && st != "thinking" {
				if p.runMode == tools.RunModePlan {
					p.runMode = tools.RunModeBuild
				} else {
					p.runMode = tools.RunModePlan
				}
			}
			return p, nil
		case "shift+tab":
			if hasCompletions {
				p.completionIndex--
				if p.completionIndex < 0 {
					p.completionIndex = len(filtered) - 1
				}
				return p, nil
			}
			return p, nil
		case "up":
			if p.historyIndex != -1 && p.historyUp() {
				return p, nil
			}
			if hasCompletions {
				p.completionIndex--
				if p.completionIndex < 0 {
					p.completionIndex = len(filtered) - 1
				}
				return p, nil
			}
			if p.historyUp() {
				return p, nil
			}
			return p, nil
		case "down":
			if p.historyIndex != -1 && p.historyDown() {
				return p, nil
			}
			if hasCompletions {
				p.completionIndex = (p.completionIndex + 1) % len(filtered)
				return p, nil
			}
			if p.historyDown() {
				return p, nil
			}
			return p, nil
		case "pgup":
			if !hasCompletions && p.scrollOffset > 0 {
				_, contentHeight := p.contentLineCount()
				p.scrollOffset -= contentHeight
				if p.scrollOffset < 0 {
					p.scrollOffset = 0
				}
			}
			return p, nil
		case "pgdown":
			if !hasCompletions {
				totalLines, contentHeight := p.contentLineCount()
				maxScroll := p.scrollMax(totalLines, contentHeight)
				p.scrollOffset += contentHeight
				if p.scrollOffset > maxScroll {
					p.scrollOffset = maxScroll
				}
			}
			return p, nil
		case "enter":
			p.resetHistoryBrowsing()
			if hasCompletions {
				sel := filtered[p.completionIndex]
				inputTrim := strings.TrimSpace(p.input)
				// If input matches selected completion, send; otherwise apply completion
				if inputTrim == sel.Name {
					p.addInputHistory(inputTrim)
					p.input = ""
					p.inputPos = 0
					p.completionIndex = 0
					p.messages = append(p.messages, chatMessage{role: "user", content: inputTrim, runMode: p.runMode})
					return p, p.handleSend(inputTrim)
				}
				// Partial input, apply completion
				p.input = sel.Name + " "
				p.inputPos = utf8.RuneCountInString(p.input)
				p.completionIndex = 0
				return p, nil
			}
			if p.input != "" {
				input := strings.TrimSpace(p.input)
				if input == "/plan on" {
					st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
					if st != "running" && st != "thinking" {
						p.runMode = tools.RunModePlan
						p.messages = append(p.messages, chatMessage{role: "assistant", content: "Switched to plan mode."})
					} else {
						p.messages = append(p.messages, chatMessage{role: "assistant", content: "Cannot switch mode while running."})
					}
					p.input = ""
					p.inputPos = 0
					p.completionIndex = 0
					return p, nil
				}
				if input == "/plan off" {
					st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
					if st != "running" && st != "thinking" {
						p.runMode = tools.RunModeBuild
						p.messages = append(p.messages, chatMessage{role: "assistant", content: "Switched to build mode."})
					} else {
						p.messages = append(p.messages, chatMessage{role: "assistant", content: "Cannot switch mode while running."})
					}
					p.input = ""
					p.inputPos = 0
					p.completionIndex = 0
					return p, nil
				}
				p.addInputHistory(input)
				p.input = ""
				p.inputPos = 0
				p.completionIndex = 0
				if input != "" {
					p.messages = append(p.messages, chatMessage{role: "user", content: input, runMode: p.runMode})
					return p, p.handleSend(input)
				}
			}
			return p, nil
		case "left":
			p.resetHistoryBrowsing()
			if p.inputPos > 0 {
				p.inputPos--
			}
			return p, nil
		case "right":
			p.resetHistoryBrowsing()
			if p.inputPos < utf8.RuneCountInString(p.input) {
				p.inputPos++
			}
			return p, nil
		case "home", "ctrl+a":
			p.resetHistoryBrowsing()
			p.inputPos = 0
			return p, nil
		case "end", "ctrl+e":
			p.resetHistoryBrowsing()
			p.inputPos = utf8.RuneCountInString(p.input)
			return p, nil
		case "backspace":
			p.resetHistoryBrowsing()
			if p.inputPos > 0 {
				bytePos := runePosToByteOffset(p.input, p.inputPos-1)
				_, size := utf8.DecodeRuneInString(p.input[bytePos:])
				p.input = p.input[:bytePos] + p.input[bytePos+size:]
				p.inputPos--
				filtered = p.filteredCompletions()
				if p.completionIndex >= len(filtered) && len(filtered) > 0 {
					p.completionIndex = len(filtered) - 1
				} else if len(filtered) == 0 {
					p.completionIndex = 0
				}
			}
			return p, nil
		case "ctrl+w":
			p.resetHistoryBrowsing()
			// Delete the word before the cursor
			if p.inputPos > 0 {
				runes := []rune(p.input)
				end := p.inputPos
				for end > 0 && (runes[end-1] == ' ' || runes[end-1] == '\t') {
					end--
				}
				for end > 0 && runes[end-1] != ' ' && runes[end-1] != '\t' {
					end--
				}
				p.input = string(runes[:end]) + string(runes[p.inputPos:])
				p.inputPos = end
			}
			return p, nil
		default:
			if len(m.Runes) > 0 {
				p.resetHistoryBrowsing()
				bytePos := runePosToByteOffset(p.input, p.inputPos)
				p.input = p.input[:bytePos] + string(m.Runes) + p.input[bytePos:]
				p.inputPos += utf8.RuneCountInString(string(m.Runes))
				filtered = p.filteredCompletions()
				p.completionIndex = 0
				if p.completionIndex >= len(filtered) && len(filtered) > 0 {
					p.completionIndex = 0
				}
			}
			return p, nil
		}
	case tea.WindowSizeMsg:
		p.width = m.Width
		p.height = m.Height
		return p, nil
	case outboundMsg:
		if m.Type == bus.MsgToolProgress {
			p.progressContent = mergeProgressStep(p.progressContent, m.Content)
		} else if m.Content != "" {
			// Spawn callback (including **Status:** and runId) always creates a new message to avoid being overwritten by agentDoneMsg after being merged with the parent agent's streaming content.
			isSpawnResult := strings.Contains(m.Content, "**Status:**") && strings.Contains(m.Content, "runId")
			st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
			isRunning := st == "running" || st == "thinking"
			appendToLast := !isSpawnResult && isRunning && len(p.messages) > 0 && p.messages[len(p.messages)-1].role == "assistant"
			if appendToLast {
				p.messages[len(p.messages)-1].content += m.Content
			} else {
				p.messages = append(p.messages, chatMessage{role: "assistant", content: m.Content})
			}
			totalLines, contentHeight := p.contentLineCount()
			p.scrollOffset = p.scrollMax(totalLines, contentHeight) // Scroll to bottom
		}
		return p, nil
	case agentStartMsg:
		p.status.SetStatus("running")
		p.progressContent = ""
		return p, nil
	case tickMsg:
		st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
		if st == "running" || st == "thinking" {
			return p, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
		}
		return p, nil
	case animTickMsg:
		st, _, _, _, _, _, _, _, _, _, _, _ := p.status.Get()
		if st == "running" || st == "thinking" {
			p.animFrame = (p.animFrame + 1) % 4
			return p, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return animTickMsg{} })
		}
		return p, nil
	case agentDoneMsg:
		p.status.SetStatus("idle")
		pendingSteps := p.progressContent
		p.progressContent = ""
		if m.err != nil {
			p.messages = append(p.messages, chatMessage{role: "assistant", content: "Error: " + m.err.Error()})
		} else if m.out != "" {
			content := m.out
			if pendingSteps != "" {
				content = pendingSteps + "\n\n" + content
			}
			// In streaming mode, the last assistant message is gradually built by outboundMsg.
			// Use the final content to replace to ensure complete message display.
			if m.streamed && len(p.messages) > 0 && p.messages[len(p.messages)-1].role == "assistant" {
				p.messages[len(p.messages)-1].content = content
			} else {
				p.messages = append(p.messages, chatMessage{role: "assistant", content: content})
			}
		}
		totalLines, contentHeight := p.contentLineCount()
		p.scrollOffset = p.scrollMax(totalLines, contentHeight) // Scroll to bottom
		return p, nil
	}
	return p, nil
}

func (p *tuiProgram) runAgentAsync(input string) tea.Cmd {
	return func() tea.Msg {
		channel, chatID := "tui", "main"
		if idx := strings.Index(p.session, ":"); idx >= 0 {
			channel = p.session[:idx]
			chatID = p.session[idx+1:]
		}
		ctx := context.Background()
		trimmed := strings.TrimSpace(input)
		if p.runMode == tools.RunModePlan {
			ctx = tools.WithRunMode(ctx, tools.RunModePlan)
			if trimmed != "" && !strings.HasPrefix(trimmed, "/") {
				input = "/plan " + trimmed
			}
		}
		out, streamed, err := p.loop.ProcessDirectWithContext(ctx, input, p.session, channel, chatID, nil)
		return agentDoneMsg{out: out, err: err, streamed: streamed}
	}
}

var loadingFrames = []string{"●○○", "○●○", "○○●", "○●○"}

// mergeProgressStep merges tool progress: if the new content is an update for the same
// step (same iteration), replace the last block; otherwise append. Ensures each step appears once.
func mergeProgressStep(existing, newContent string) string {
	newStep := parseProgressStepNum(newContent)
	if newStep <= 0 {
		return newContent
	}
	if existing == "" {
		return newContent
	}
	blocks := strings.Split(existing, "\n\n")
	lastIdx := len(blocks) - 1
	if lastIdx >= 0 {
		lastStep := parseProgressStepNum(blocks[lastIdx])
		if lastStep == newStep {
			blocks[lastIdx] = newContent
			return strings.Join(blocks, "\n\n")
		}
	}
	return existing + "\n\n" + newContent
}

func parseProgressStepNum(s string) int {
	const prefix = "🔧 Step "
	if idx := strings.Index(s, prefix); idx >= 0 {
		rest := s[idx+len(prefix):]
		var n int
		for _, r := range rest {
			if r >= '0' && r <= '9' {
				n = n*10 + int(r-'0')
			} else {
				break
			}
		}
		return n
	}
	return 0
}

func (p *tuiProgram) viewSessions() string {
	w := p.width
	if w < 20 {
		return ""
	}

	_, _, _, _, _, _, _, _, _, _, _, version := p.status.Get()
	topBarText := fmt.Sprintf("🍀 luckclaw v%s | Sessions", version)
	topBarStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("234")).
		Width(w)
	topBar := topBarStyle.Render(topBarText)

	var listLines []string
	contentHeight := p.height - 4
	for i := p.sessionScroll; i < len(p.sessionList) && i < p.sessionScroll+contentHeight; i++ {
		s := p.sessionList[i]
		cursor := "  "
		if i == p.sessionIdx {
			cursor = "→ "
		}

		summary := s.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		if i == p.renamingIdx {
			summary = p.renameInput + "▌"
		}

		key := s.Key
		displayKey := key
		if len(displayKey) > 20 {
			displayKey = displayKey[:17] + "..."
		}

		line := fmt.Sprintf("%s %-20s | %s", cursor, displayKey, summary)
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == p.sessionIdx {
			style = style.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("62")).Bold(true)
		} else {
			style = style.Foreground(lipgloss.Color("250"))
		}
		listLines = append(listLines, style.Render(line))

		if len(s.RecentMessages) > 0 && i == p.sessionIdx {
			for _, msg := range s.RecentMessages {
				msgStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245")).
					Padding(0, 1).
					PaddingLeft(4)
				listLines = append(listLines, msgStyle.Render(msg))
			}
		}
	}

	if len(listLines) == 0 {
		listLines = append(listLines, "  No sessions found.")
	}

	content := strings.Join(listLines, "\n")
	contentHeight = p.height - 4
	mainContent := lipgloss.NewStyle().
		Background(lipgloss.Color("0")).
		Width(w).
		Height(contentHeight).
		Render(content)

	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1).
		Render("↑/↓: navigate | enter: switch | d: delete | r: rename | esc/q: back | history shown for selected")

	return lipgloss.JoinVertical(lipgloss.Left, topBar, mainContent, footer)
}

func (p *tuiProgram) View() string {
	if p.showSessions {
		return p.viewSessions()
	}
	w := p.width
	if w < 20 {
		return ""
	}

	status, runningDur, _, model, terminal, _, _, _, _, ctxSources, ctxMode, version := p.status.Get()
	isRunning := status == "running" || status == "thinking"
	isStop := status == "stop"

	termPart := "local"
	if strings.TrimSpace(terminal) != "" {
		termPart = strings.TrimSpace(terminal)
	}
	topBarText := fmt.Sprintf("🍀 luckclaw v%s", version)
	topBarStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("234")). // Darker background to distinguish
		BorderForeground(lipgloss.Color("240")).
		Width(w)
	topBar := topBarStyle.Render(topBarText)

	// Header: # session (left) | tokens / ctx (right)
	// tokenStr := formatTokens(totalTok, ctxWindow)
	ctxPart := ""
	if ctxSources != "" {
		modeSuffix := ""
		if ctxMode != "" {
			modeSuffix = fmt.Sprintf(" (%s)", ctxMode)
		}
		ctxPart = fmt.Sprintf(" ctx: %s%s", ctxSources, modeSuffix)
	}

	headerLeft := fmt.Sprintf("# session: %s | term: %s", p.session, termPart)
	// headerRight := fmt.Sprintf("tokens: %s%s", tokenStr, ctxPart)
	headerRight := fmt.Sprintf("%s", ctxPart)

	// Calculate space between left and right
	leftWidth := lipgloss.Width(headerLeft)
	rightWidth := lipgloss.Width(headerRight)
	spaceWidth := w - leftWidth - rightWidth - 4 // 4 for padding/borders
	if spaceWidth < 1 {
		spaceWidth = 1
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("234")).
		Padding(0, 1).
		Border(lipgloss.Border{
			Left: "┃",
		}, false, false, false, true).
		BorderForeground(lipgloss.Color("240")).
		Width(w)

	headerContent := headerLeft + strings.Repeat(" ", spaceWidth) + headerRight
	header := headerStyle.Render(headerContent)

	// Status bar (at the bottom, above input)
	statusStr := status
	if isRunning {
		anim := loadingFrames[p.animFrame%len(loadingFrames)]
		statusStr = fmt.Sprintf("%s %s • %ds", status, anim, int(runningDur.Seconds()))
	}
	// Add mode and model name to status bar (Mode first)
	modePart := string(p.runMode)
	if modePart == "" {
		modePart = string(tools.RunModeBuild)
	}
	statusStr = fmt.Sprintf("%s | %s | %s", modePart, statusStr, model)

	// Accent colors: running: green; stop: amber; idle: blue(Build) or dark blue(Plan)
	accentColor := lipgloss.Color("62") // blue (idle Build)
	if p.runMode == tools.RunModePlan {
		accentColor = lipgloss.Color("24") // dark blue (idle Plan)
	}

	if isRunning {
		accentColor = lipgloss.Color("29")
	} else if isStop {
		accentColor = lipgloss.Color("166") // amber
	}

	// Remove padding to align exactly with the input border, but add a space at the start of the string for readability
	statusBarStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).
		Background(accentColor).
		Padding(0, 1).
		Width(w)
	statusBar := statusBarStyle.Width(w).Render(statusStr)

	// Input line: gray background with accent left border
	inputStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Padding(0, 1).
		Border(lipgloss.Border{
			Left: "┃",
		}, false, false, false, true).
		BorderForeground(accentColor).
		Width(w)

	innerWidth := w - inputStyle.GetHorizontalFrameSize()
	inputLine := inputStyle.Render(renderSingleLineInput(p.input, p.inputPos, innerWidth))

	// Footer: help hint below status bar
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1).Render("Type /help for commands. | Tab: toggle plan/build | Alt+M: toggle mouse capture")

	// Content area (between header and input) - scrollable
	content := p.buildContentString()
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}

	// Ensure scrollOffset is within bounds
	if p.scrollOffset >= len(lines) {
		p.scrollOffset = len(lines) - 1
	}
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
	start := p.scrollOffset

	// Recalculate contentAreaHeight considering header (1) and topBar (1)
	contentAreaHeight := p.height - 5 // inputLine(1) + statusBar(1) + footer(1) + header(1) + topBar(1)
	filtered := p.filteredCompletions()
	var completionBlock string
	if len(filtered) > 0 {
		maxShow := 6
		startC := p.completionIndex - maxShow/2
		if startC < 0 {
			startC = 0
		}
		endC := startC + maxShow
		if endC > len(filtered) {
			endC = len(filtered)
			startC = endC - maxShow
			if startC < 0 {
				startC = 0
			}
		}
		slice := filtered[startC:endC]
		var cLines []string
		for i, c := range slice {
			idx := startC + i
			arrow := "  "
			if idx == p.completionIndex {
				arrow = "→ "
			}
			cLines = append(cLines, fmt.Sprintf("%s%s  %s", arrow, c.Name, c.Desc))
		}
		completionBlock = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("0")).
			MaxWidth(w).
			Padding(0, 1).
			Render(strings.Join(cLines, "\n") + fmt.Sprintf("\n  (%d/%d)", p.completionIndex+1, len(filtered)))

		contentAreaHeight -= (strings.Count(completionBlock, "\n") + 1)
	}

	if contentAreaHeight < 3 {
		contentAreaHeight = 16
	}

	end := start + contentAreaHeight
	if end > len(lines) {
		end = len(lines)
	}
	visibleLines := lines[start:end]
	content = strings.Join(visibleLines, "\n")
	content += "\033[0m"

	mainContent := lipgloss.NewStyle().
		Width(w).
		MaxWidth(w).
		Height(contentAreaHeight).
		Render(content)

	bottom := lipgloss.JoinVertical(lipgloss.Left, inputLine, statusBar, footer)
	if completionBlock != "" {
		bottom = lipgloss.JoinVertical(lipgloss.Left, inputLine, statusBar, footer, completionBlock)
	}

	// Final view: TopBar -> Header -> Content -> Bottom
	return lipgloss.NewStyle().Render(lipgloss.JoinVertical(lipgloss.Left, topBar, header, mainContent, bottom))
}
