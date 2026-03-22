package tui

import "github.com/charmbracelet/lipgloss"

// Rounded border definition
var RoundedBorder = lipgloss.Border{
	Top:         "─",
	Bottom:      "─",
	Left:        "│",
	Right:       "│",
	TopLeft:     "╭",
	TopRight:    "╮",
	BottomLeft:  "╰",
	BottomRight: "╯",
}

// Double border definition
var DoubleBorder = lipgloss.Border{
	Top:         "═",
	Bottom:      "═",
	Left:        "║",
	Right:       "║",
	TopLeft:     "╔",
	TopRight:    "╗",
	BottomLeft:  "╚",
	BottomRight: "╝",
}

// Block border definition
var BlockBorder = lipgloss.Border{
	Top:         "▄",
	Bottom:      "▀",
	Left:        "█",
	Right:       "█",
	TopLeft:     "█",
	TopRight:    "█",
	BottomLeft:  "█",
	BottomRight: "█",
}

// Styles provides common style builders
type Styles struct {
	theme Theme

	// Base styles
	Base      lipgloss.Style
	Secondary lipgloss.Style

	// Text styles
	Text    lipgloss.Style
	Subtext lipgloss.Style
	Bold    lipgloss.Style
	Italic  lipgloss.Style
	Dim     lipgloss.Style

	// Border styles
	Border lipgloss.Style

	// Status styles
	StatusIdle    lipgloss.Style
	StatusRunning lipgloss.Style
	StatusStop    lipgloss.Style
	StatusPlan    lipgloss.Style

	// User message styles
	UserMessage lipgloss.Style

	// Assistant message styles
	AssistantMessage lipgloss.Style

	// Input styles
	Input lipgloss.Style

	// Footer styles
	Footer lipgloss.Style
}

// NewStyles creates style builders
func NewStyles(theme Theme) Styles {
	s := Styles{theme: theme}

	// Base styles
	s.Base = lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.Base)

	s.Secondary = lipgloss.NewStyle().
		Foreground(theme.Subtext0).
		Background(theme.Mantle)

	// Text styles
	s.Text = lipgloss.NewStyle().
		Foreground(theme.Text)

	s.Subtext = lipgloss.NewStyle().
		Foreground(theme.Subtext0)

	s.Bold = lipgloss.NewStyle().
		Bold(true)

	s.Italic = lipgloss.NewStyle().
		Italic(true)

	s.Dim = lipgloss.NewStyle().
		Faint(true)

	// Border styles
	s.Border = lipgloss.NewStyle().
		Border(RoundedBorder).
		BorderForeground(theme.Surface2)

	// Status styles
	s.StatusIdle = lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.StatusIdle).
		Padding(0, 1)

	s.StatusRunning = lipgloss.NewStyle().
		Foreground(theme.Crust).
		Background(theme.StatusRunning).
		Padding(0, 1)

	s.StatusStop = lipgloss.NewStyle().
		Foreground(theme.Crust).
		Background(theme.StatusStop).
		Padding(0, 1)

	s.StatusPlan = lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.StatusPlan).
		Padding(0, 1)

	// User message styles
	s.UserMessage = lipgloss.NewStyle().
		Foreground(theme.Text).
		Padding(0, 1).
		Border(lipgloss.Border{
			Left: "┃",
		}, false, false, false, true).
		BorderForeground(theme.Blue)

	// Assistant message styles
	s.AssistantMessage = lipgloss.NewStyle().
		Foreground(theme.Text).
		Padding(0, 0)

	// Input styles
	s.Input = lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.Surface0).
		Padding(0, 1).
		Border(RoundedBorder).
		BorderForeground(theme.Surface2)

	// Footer styles
	s.Footer = lipgloss.NewStyle().
		Foreground(theme.Overlay0).
		Padding(0, 1)

	return s
}

// UserMessageStyle creates user message style
func (s *Styles) UserMessageStyle(mode string) lipgloss.Style {
	color := s.theme.Blue
	if mode == "plan" {
		color = s.theme.Mauve
	}
	return lipgloss.NewStyle().
		Foreground(s.theme.Text).
		Background(s.theme.Crust).
		Padding(0, 1).
		Border(lipgloss.Border{
			Left: "┃",
		}, false, false, false, true).
		BorderForeground(color)
}

// StatusBarStyle creates status bar style
func (s *Styles) StatusBarStyle(isRunning bool, isStop bool, isPlan bool) lipgloss.Style {
	bg := s.theme.StatusIdle
	if isRunning {
		bg = s.theme.StatusRunning
	} else if isStop {
		bg = s.theme.StatusStop
	} else if isPlan {
		bg = s.theme.StatusPlan
	}

	fg := lipgloss.Color("#FFFFFF")

	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Padding(0, 1)
}

// TopBarStyle creates top bar style
func (s *Styles) TopBarStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(s.theme.Blue).
		Bold(true).
		Width(width).
		Height(1)
}

// HeaderStyle creates header style
func (s *Styles) HeaderStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Subtext0).
		Background(s.theme.Surface0).
		Padding(0, 1).
		BorderForeground(s.theme.Sapphire).
		Width(width).
		Height(1)
}

// InputStyle creates input box style
func (s *Styles) InputStyle(width int, accentColor lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Text).
		Background(s.theme.Surface0).
		Padding(0, 1).
		Border(RoundedBorder).
		BorderForeground(accentColor).
		Width(width)
}

// CompletionStyle creates completion menu style
func (s *Styles) CompletionStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Subtext0).
		Background(s.theme.Crust).
		MaxWidth(width).
		Padding(0, 1)
}

// SelectedCompletionStyle creates selected completion item style
func (s *Styles) SelectedCompletionStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Text).
		Bold(true)
}

// SessionItemStyle creates session item style
func (s *Styles) SessionItemStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().
			Foreground(s.theme.Crust).
			Background(s.theme.Blue).
			Bold(true)
	}
	return lipgloss.NewStyle().
		Foreground(s.theme.Text)
}

// ThinkingStyle creates thinking text style
func (s *Styles) ThinkingStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Teal).
		Italic(true)
}

// ProgressStyle creates progress text style
func (s *Styles) ProgressStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Teal)
}

// MQTTMessageStyle creates MQTT message style
func (s *Styles) MQTTMessageStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(s.theme.Teal).
		Padding(0, 1).
		Border(lipgloss.Border{
			Left: "┃",
		}, false, false, false, true).
		BorderForeground(s.theme.Teal).
		Width(width)
}
