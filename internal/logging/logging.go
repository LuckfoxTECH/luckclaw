package logging

import (
	"log"
	"sync"
	"time"
)

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type Logger interface {
	Info(msg string)
	Error(msg string)
	Debug(msg string)
}

type MemoryLogger struct {
	mu      sync.Mutex
	entries []Entry
	maxSize int
}

func NewMemoryLogger(maxSize int) *MemoryLogger {
	return &MemoryLogger{
		entries: make([]Entry, 0, maxSize),
		maxSize: maxSize,
	}
}

func (l *MemoryLogger) Log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := Entry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	}

	if len(l.entries) >= l.maxSize {
		// Drop oldest
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
}

func (l *MemoryLogger) Info(msg string)  { l.Log("INFO", msg) }
func (l *MemoryLogger) Error(msg string) { l.Log("ERROR", msg) }
func (l *MemoryLogger) Debug(msg string) { l.Log("DEBUG", msg) }

func (l *MemoryLogger) GetEntries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Return copy
	cpy := make([]Entry, len(l.entries))
	copy(cpy, l.entries)
	return cpy
}

// StdLogger writes to standard log (log.Printf). Use for gateway/CLI when
// MemoryLogger is not needed.
type StdLogger struct {
	Prefix string // e.g. "[agent]"
}

func (l *StdLogger) Info(msg string)  { log.Printf("%s %s", l.Prefix, msg) }
func (l *StdLogger) Error(msg string) { log.Printf("%s ERROR: %s", l.Prefix, msg) }
func (l *StdLogger) Debug(msg string) { log.Printf("%s DEBUG: %s", l.Prefix, msg) }
