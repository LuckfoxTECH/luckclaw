package tui

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SelectionMode represents selection mode
type SelectionMode int

const (
	SelectionNone SelectionMode = iota
	SelectionChar               // Character-level selection
	SelectionWord               // Word-level selection (double-click)
	SelectionLine               // Line-level selection (triple-click)
)

const doubleClickThreshold = 300 * time.Millisecond

// Selection manages text selection state
type Selection struct {
	active    bool
	startX    int
	startY    int
	endX      int
	endY      int
	mode      SelectionMode
	mouseDown bool
	lastClick time.Time
}

// NewSelection creates new selection instance
func NewSelection() *Selection {
	return &Selection{}
}

// HandleMouseDown handles mouse down event
func (s *Selection) HandleMouseDown(x, y int, content []string) {
	now := time.Now()

	// Detect double/triple click
	if now.Sub(s.lastClick) < doubleClickThreshold {
		if s.mode == SelectionWord {
			s.mode = SelectionLine
		} else {
			s.mode = SelectionWord
		}
	} else {
		s.mode = SelectionChar
	}
	s.lastClick = now

	s.mouseDown = true
	s.startX = x
	s.startY = y
	s.endX = x
	s.endY = y
	s.active = true

	// Boundary check: ensure y is within valid range
	if y >= 0 && y < len(content) {
		// Double-click selects word
		if s.mode == SelectionWord {
			s.expandToWord(x, y, content)
		}
		// Triple-click selects line
		if s.mode == SelectionLine {
			s.expandToLine(y, content)
		}
	}
}

// HandleMouseDrag handles mouse drag
func (s *Selection) HandleMouseDrag(x, y int) {
	if !s.mouseDown {
		return
	}
	s.endX = x
	s.endY = y
	s.active = true
}

// HandleMouseUp handles mouse up
func (s *Selection) HandleMouseUp(x, y int) bool {
	if !s.mouseDown {
		return false
	}
	s.mouseDown = false
	s.endX = x
	s.endY = y

	// Check if valid selection exists
	return s.HasSelection()
}

// HasSelection checks if there is a valid selection
func (s *Selection) HasSelection() bool {
	if !s.active {
		return false
	}
	return s.startX != s.endX || s.startY != s.endY
}

// IsMouseDown checks if mouse is pressed
func (s *Selection) IsMouseDown() bool {
	return s.mouseDown
}

// Clear clears selection
func (s *Selection) Clear() {
	s.active = false
	s.mouseDown = false
	s.mode = SelectionNone
}

// GetSelectedText gets selected text
func (s *Selection) GetSelectedText(content []string) string {
	if !s.HasSelection() || len(content) == 0 {
		return ""
	}

	// Ensure coordinates are ordered
	sy, ey := s.startY, s.endY
	sx, ex := s.startX, s.endX
	if sy > ey || (sy == ey && sx > ex) {
		sy, ey = ey, sy
		sx, ex = ex, sx
	}

	// Boundary check: ensure index is not negative
	if sy < 0 {
		sy = 0
	}
	if ey < 0 {
		return ""
	}
	if sy >= len(content) {
		return ""
	}
	if ey >= len(content) {
		ey = len(content) - 1
	}
	if sx < 0 {
		sx = 0
	}
	if ex < 0 {
		return ""
	}

	var result []string
	for y := sy; y <= ey; y++ {
		line := stripAnsi(content[y])
		if y == sy && y == ey {
			start := cellToByteStart(line, sx)
			end := cellToByteEnd(line, ex)
			if end > start {
				result = append(result, line[start:end])
			}
		} else if y == sy {
			start := cellToByteStart(line, sx)
			if start <= len(line) {
				result = append(result, line[start:])
			}
		} else if y == ey {
			end := cellToByteEnd(line, ex)
			if end > 0 {
				result = append(result, line[:end])
			}
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// IsSelected checks if a position is selected
func (s *Selection) IsSelected(x, y int) bool {
	if !s.HasSelection() {
		return false
	}

	sy, ey := s.startY, s.endY
	sx, ex := s.startX, s.endX
	if sy > ey || (sy == ey && sx > ex) {
		sy, ey = ey, sy
		sx, ex = ex, sx
	}

	if y < sy || y > ey {
		return false
	}
	if y == sy && y == ey {
		return x >= sx && x < ex
	}
	if y == sy {
		return x >= sx
	}
	if y == ey {
		return x < ex
	}
	return true
}

// expandToWord expands selection to word boundaries
func (s *Selection) expandToWord(x, y int, content []string) {
	if y < 0 || y >= len(content) {
		return
	}
	line := stripAnsi(content[y])
	if x < 0 || len(line) == 0 {
		return
	}
	runePos := cellToRuneIndex(line, x)
	runes := []rune(line)
	if runePos < 0 || runePos >= len(runes) {
		return
	}

	start := runePos
	for start > 0 && isWordChar(runes[start-1]) {
		start--
	}

	end := runePos
	for end < len(runes) && isWordChar(runes[end]) {
		end++
	}

	s.startX = runeIndexToCellPos(line, start)
	s.endX = runeIndexToCellPos(line, end)
}

// expandToLine expands selection to entire line
func (s *Selection) expandToLine(y int, content []string) {
	if y < 0 || y >= len(content) {
		return
	}
	line := stripAnsi(content[y])
	s.startX = 0
	s.endX = runeIndexToCellPos(line, utf8.RuneCountInString(line))
}

// isWordChar checks if character is a word character
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// ansiRegex matches ANSI escape sequences
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripAnsi removes ANSI escape sequences from string
func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func cellToByteStart(s string, cell int) int {
	if cell <= 0 {
		return 0
	}
	pos := 0
	for i := 0; i < len(s); {
		_, w, size := nextGrapheme(s[i:])
		if cell <= pos || cell < pos+w {
			return i
		}
		pos += w
		i += size
	}
	return len(s)
}

func cellToByteEnd(s string, cell int) int {
	if cell <= 0 {
		return 0
	}
	pos := 0
	for i := 0; i < len(s); {
		_, w, size := nextGrapheme(s[i:])
		nextPos := pos + w
		if cell <= pos {
			return i
		}
		if cell < nextPos {
			return i + size
		}
		pos = nextPos
		i += size
	}
	return len(s)
}

func cellToRuneIndex(s string, cell int) int {
	if cell <= 0 {
		return 0
	}
	pos := 0
	runeIndex := 0
	for i := 0; i < len(s); {
		cluster, w, size := nextGrapheme(s[i:])
		if cell <= pos || cell < pos+w {
			return runeIndex
		}
		pos += w
		i += size
		runeIndex += utf8.RuneCountInString(cluster)
	}
	return runeIndex
}

func runeIndexToCellPos(s string, targetRuneIndex int) int {
	if targetRuneIndex <= 0 {
		return 0
	}
	pos := 0
	currentRuneIndex := 0
	for i := 0; i < len(s); {
		if currentRuneIndex >= targetRuneIndex {
			break
		}
		cluster, w, size := nextGrapheme(s[i:])
		clusterRunes := utf8.RuneCountInString(cluster)

		if currentRuneIndex+clusterRunes > targetRuneIndex {
			break
		}

		pos += w
		currentRuneIndex += clusterRunes
		i += size
	}
	return pos
}
