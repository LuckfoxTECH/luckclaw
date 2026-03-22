package tui

import "github.com/charmbracelet/lipgloss"

// Theme represents a TUI color theme (Catppuccin-based)
type Theme struct {
	Name string

	// Base colors (background)
	Base     lipgloss.Color
	Mantle   lipgloss.Color
	Crust    lipgloss.Color
	Surface0 lipgloss.Color
	Surface1 lipgloss.Color
	Surface2 lipgloss.Color
	Overlay0 lipgloss.Color
	Overlay1 lipgloss.Color

	// Text colors
	Text     lipgloss.Color
	Subtext0 lipgloss.Color
	Subtext1 lipgloss.Color

	// Accent colors
	Blue      lipgloss.Color
	Lavender  lipgloss.Color
	Sapphire  lipgloss.Color
	Sky       lipgloss.Color
	Teal      lipgloss.Color
	Green     lipgloss.Color
	Yellow    lipgloss.Color
	Peach     lipgloss.Color
	Maroon    lipgloss.Color
	Red       lipgloss.Color
	Mauve     lipgloss.Color
	Pink      lipgloss.Color
	Flamingo  lipgloss.Color
	Rosewater lipgloss.Color

	// Status colors (derived from accents)
	StatusIdle    lipgloss.Color
	StatusRunning lipgloss.Color
	StatusStop    lipgloss.Color
	StatusPlan    lipgloss.Color
}

// Catppuccin Latte (Light)
var ThemeLatte = Theme{
	Name: "latte",

	Base:     lipgloss.Color("#eff1f5"),
	Mantle:   lipgloss.Color("#e6e9ef"),
	Crust:    lipgloss.Color("#dce0e8"),
	Surface0: lipgloss.Color("#ccd0da"),
	Surface1: lipgloss.Color("#bcc0cc"),
	Surface2: lipgloss.Color("#acb0be"),
	Overlay0: lipgloss.Color("#9ca0b0"),
	Overlay1: lipgloss.Color("#8c8fa1"),

	Text:     lipgloss.Color("#4c4f69"),
	Subtext0: lipgloss.Color("#5c5f77"),
	Subtext1: lipgloss.Color("#6c6f85"),

	Blue:      lipgloss.Color("#1e66f5"),
	Lavender:  lipgloss.Color("#7287fd"),
	Sapphire:  lipgloss.Color("#209fb5"),
	Sky:       lipgloss.Color("#04a5e5"),
	Teal:      lipgloss.Color("#179299"),
	Green:     lipgloss.Color("#40a02b"),
	Yellow:    lipgloss.Color("#df8e1d"),
	Peach:     lipgloss.Color("#fe640b"),
	Maroon:    lipgloss.Color("#e64553"),
	Red:       lipgloss.Color("#d20f39"),
	Mauve:     lipgloss.Color("#8839ef"),
	Pink:      lipgloss.Color("#ea76cb"),
	Flamingo:  lipgloss.Color("#dd7878"),
	Rosewater: lipgloss.Color("#dc8a78"),

	StatusIdle:    lipgloss.Color("#1e66f5"),
	StatusRunning: lipgloss.Color("#40a02b"),
	StatusStop:    lipgloss.Color("#fe640b"),
	StatusPlan:    lipgloss.Color("#8839ef"),
}

// Catppuccin Frappe
var ThemeFrappe = Theme{
	Name: "frappe",

	Base:     lipgloss.Color("#303446"),
	Mantle:   lipgloss.Color("#292c3c"),
	Crust:    lipgloss.Color("#232634"),
	Surface0: lipgloss.Color("#414559"),
	Surface1: lipgloss.Color("#51576d"),
	Surface2: lipgloss.Color("#626880"),
	Overlay0: lipgloss.Color("#737994"),
	Overlay1: lipgloss.Color("#838ba7"),

	Text:     lipgloss.Color("#c6d0f5"),
	Subtext0: lipgloss.Color("#b5bfe2"),
	Subtext1: lipgloss.Color("#a5adce"),

	Blue:      lipgloss.Color("#8caaee"),
	Lavender:  lipgloss.Color("#babbf1"),
	Sapphire:  lipgloss.Color("#85c1dc"),
	Sky:       lipgloss.Color("#99d1db"),
	Teal:      lipgloss.Color("#81c8be"),
	Green:     lipgloss.Color("#a6d189"),
	Yellow:    lipgloss.Color("#e5c890"),
	Peach:     lipgloss.Color("#ef9f76"),
	Maroon:    lipgloss.Color("#ea999c"),
	Red:       lipgloss.Color("#e78284"),
	Mauve:     lipgloss.Color("#ca9ee6"),
	Pink:      lipgloss.Color("#f4b8e4"),
	Flamingo:  lipgloss.Color("#eebebe"),
	Rosewater: lipgloss.Color("#f2d5cf"),

	StatusIdle:    lipgloss.Color("#8caaee"),
	StatusRunning: lipgloss.Color("#a6d189"),
	StatusStop:    lipgloss.Color("#ef9f76"),
	StatusPlan:    lipgloss.Color("#ca9ee6"),
}

// Catppuccin Macchiato
var ThemeMacchiato = Theme{
	Name: "macchiato",

	Base:     lipgloss.Color("#24273a"),
	Mantle:   lipgloss.Color("#1e2030"),
	Crust:    lipgloss.Color("#181926"),
	Surface0: lipgloss.Color("#363a4f"),
	Surface1: lipgloss.Color("#494d64"),
	Surface2: lipgloss.Color("#5b6078"),
	Overlay0: lipgloss.Color("#6e738d"),
	Overlay1: lipgloss.Color("#8087a2"),

	Text:     lipgloss.Color("#cad3f5"),
	Subtext0: lipgloss.Color("#b8c0e0"),
	Subtext1: lipgloss.Color("#a5adcb"),

	Blue:      lipgloss.Color("#8aadf4"),
	Lavender:  lipgloss.Color("#b7bdf8"),
	Sapphire:  lipgloss.Color("#7dc4e4"),
	Sky:       lipgloss.Color("#91d7e3"),
	Teal:      lipgloss.Color("#8bd5ca"),
	Green:     lipgloss.Color("#a6da95"),
	Yellow:    lipgloss.Color("#eed49f"),
	Peach:     lipgloss.Color("#f5a97f"),
	Maroon:    lipgloss.Color("#ee99a0"),
	Red:       lipgloss.Color("#ed8796"),
	Mauve:     lipgloss.Color("#c6a0f6"),
	Pink:      lipgloss.Color("#f5bde6"),
	Flamingo:  lipgloss.Color("#f0c6c6"),
	Rosewater: lipgloss.Color("#f4dbd6"),

	StatusIdle:    lipgloss.Color("#8aadf4"),
	StatusRunning: lipgloss.Color("#a6da95"),
	StatusStop:    lipgloss.Color("#f5a97f"),
	StatusPlan:    lipgloss.Color("#c6a0f6"),
}

// Catppuccin Mocha (Dark) - Default
var ThemeMocha = Theme{
	Name: "mocha",

	Base:     lipgloss.Color("#1e1e2e"),
	Mantle:   lipgloss.Color("#181825"),
	Crust:    lipgloss.Color("#11111b"),
	Surface0: lipgloss.Color("#313244"),
	Surface1: lipgloss.Color("#45475a"),
	Surface2: lipgloss.Color("#585b70"),
	Overlay0: lipgloss.Color("#6c7086"),
	Overlay1: lipgloss.Color("#7f849c"),

	Text:     lipgloss.Color("#cdd6f4"),
	Subtext0: lipgloss.Color("#bac2de"),
	Subtext1: lipgloss.Color("#a6adc8"),

	Blue:      lipgloss.Color("#89b4fa"),
	Lavender:  lipgloss.Color("#b4befe"),
	Sapphire:  lipgloss.Color("#74c7ec"),
	Sky:       lipgloss.Color("#89dceb"),
	Teal:      lipgloss.Color("#94e2d5"),
	Green:     lipgloss.Color("#a6e3a1"),
	Yellow:    lipgloss.Color("#f9e2af"),
	Peach:     lipgloss.Color("#fab387"),
	Maroon:    lipgloss.Color("#eba0ac"),
	Red:       lipgloss.Color("#f38ba8"),
	Mauve:     lipgloss.Color("#cba6f7"),
	Pink:      lipgloss.Color("#f5c2e7"),
	Flamingo:  lipgloss.Color("#f2cdcd"),
	Rosewater: lipgloss.Color("#f5e0dc"),

	StatusIdle:    lipgloss.Color("#89b4fa"),
	StatusRunning: lipgloss.Color("#a6e3a1"),
	StatusStop:    lipgloss.Color("#fab387"),
	StatusPlan:    lipgloss.Color("#cba6f7"),
}

// AllThemes returns all available themes
func AllThemes() []Theme {
	return []Theme{ThemeMocha, ThemeMacchiato, ThemeFrappe, ThemeLatte}
}

// ThemeByName returns a theme by name, defaults to Mocha
func ThemeByName(name string) Theme {
	for _, t := range AllThemes() {
		if t.Name == name {
			return t
		}
	}
	return ThemeMocha
}

// ThemeNames returns all theme names
func ThemeNames() []string {
	return []string{"mocha", "macchiato", "frappe", "latte"}
}

// CurrentTheme is the active theme (default: mocha)
var CurrentTheme = ThemeMocha

// SetTheme sets the current theme by name
func SetTheme(name string) {
	CurrentTheme = ThemeByName(name)
}
