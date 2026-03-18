package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/paths"
)

type Message struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Timestamp string         `json:"timestamp"`
	Extra     map[string]any `json:"-"`
}

type Session struct {
	Key              string
	Messages         []map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Metadata         map[string]any
	LastConsolidated int
	SavedCount       int
}

type Manager struct {
	Workspace string

	mu    sync.RWMutex
	cache map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		cache: make(map[string]*Session),
	}
}

func (m *Manager) sessionsDir() (string, error) {
	if m.Workspace != "" {
		dir := filepath.Join(m.Workspace, "sessions")
		return dir, nil
	}
	return paths.SessionsDir()
}

func (m *Manager) GetOrCreate(key string) (*Session, error) {
	// Check cache first
	m.mu.RLock()
	if s, ok := m.cache[key]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	s, err := m.load(key)
	if err != nil {
		return nil, err
	}
	if s != nil {
		m.mu.Lock()
		m.cache[key] = s
		m.mu.Unlock()
		return s, nil
	}
	now := time.Now()
	s = &Session{
		Key:       key,
		Messages:  []map[string]any{},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]any{},
	}
	m.mu.Lock()
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) AddMessage(s *Session, role string, content string, extra map[string]any) {
	if s == nil {
		return
	}
	msg := map[string]any{
		"role":      role,
		"content":   content,
		"timestamp": time.Now().Format(time.RFC3339Nano),
	}
	for k, v := range extra {
		msg[k] = v
	}
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

func (m *Manager) Save(s *Session) error {
	if s == nil {
		return nil
	}
	dir, err := m.sessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(s.Key, ":", "_"))+".jsonl")
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	}

	var f *os.File
	if !exists || s.SavedCount == 0 {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	} else {
		f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	if !exists || s.SavedCount == 0 {
		meta := map[string]any{
			"_type":             "metadata",
			"session_key":       s.Key,
			"created_at":        s.CreatedAt.Format(time.RFC3339Nano),
			"updated_at":        s.UpdatedAt.Format(time.RFC3339Nano),
			"last_consolidated": s.LastConsolidated,
			"metadata":          s.Metadata,
		}
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
		for _, msg := range s.Messages {
			b, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(b, '\n')); err != nil {
				return err
			}
		}
		s.SavedCount = len(s.Messages)
	} else {
		startIdx := s.SavedCount
		if startIdx > len(s.Messages) {
			startIdx = len(s.Messages)
		}
		for _, msg := range s.Messages[startIdx:] {
			b, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(b, '\n')); err != nil {
				return err
			}
		}
		s.SavedCount = len(s.Messages)
		meta := map[string]any{
			"_type":             "metadata",
			"session_key":       s.Key,
			"created_at":        s.CreatedAt.Format(time.RFC3339Nano),
			"updated_at":        s.UpdatedAt.Format(time.RFC3339Nano),
			"last_consolidated": s.LastConsolidated,
			"metadata":          s.Metadata,
		}
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (m *Manager) load(key string) (*Session, error) {
	dir, err := m.sessionsDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(key, ":", "_"))+".jsonl")

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m.loadLegacy(key)
		}
		return nil, err
	}
	defer f.Close()

	return m.parseJSONL(f, key)
}

func (m *Manager) loadLegacy(key string) (*Session, error) {
	legacyDir, err := paths.SessionsDir()
	if err != nil {
		return nil, nil
	}
	if m.Workspace != "" && legacyDir == filepath.Join(m.Workspace, "sessions") {
		return nil, nil
	}

	path := filepath.Join(legacyDir, safeFilename(strings.ReplaceAll(key, ":", "_"))+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	s, err := m.parseJSONL(f, key)
	if err != nil || s == nil {
		return nil, nil
	}

	// Migrate to workspace-scoped location
	if m.Workspace != "" {
		_ = m.Save(s)
	}
	return s, nil
}

func (m *Manager) parseJSONL(f *os.File, key string) (*Session, error) {
	var messages []map[string]any
	metadata := map[string]any{}
	var createdAt time.Time
	var updatedAt time.Time
	lastConsolidated := 0

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj["_type"] == "metadata" {
			if v, ok := obj["metadata"].(map[string]any); ok {
				metadata = v
			}
			if v, ok := obj["created_at"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
					createdAt = t
				}
			}
			if v, ok := obj["updated_at"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
					updatedAt = t
				}
			}
			if v, ok := obj["last_consolidated"].(float64); ok {
				lastConsolidated = int(v)
			}
			if v, ok := obj["session_key"].(string); ok && key == "" {
				key = v
			}
			continue
		}
		messages = append(messages, obj)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(messages) == 0 && createdAt.IsZero() {
		return nil, nil
	}

	now := time.Now()
	if createdAt.IsZero() {
		createdAt = now
	}
	if updatedAt.IsZero() {
		updatedAt = now
	}

	return &Session{
		Key:              key,
		Messages:         messages,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		Metadata:         metadata,
		LastConsolidated: lastConsolidated,
		SavedCount:       len(messages),
	}, nil
}

func safeFilename(name string) string {
	unsafe := `<>:"/\\|?*`
	for _, r := range unsafe {
		name = strings.ReplaceAll(name, string(r), "_")
	}
	return strings.TrimSpace(name)
}

func (m *Manager) Delete(key string) error {
	m.mu.Lock()
	delete(m.cache, key)
	m.mu.Unlock()

	dir, err := m.sessionsDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(key, ":", "_"))+".jsonl")
	if _, err := os.Stat(path); err == nil {
		return os.Remove(path)
	}
	return nil
}

func (m *Manager) SetSummary(key string, summary string) error {
	s, err := m.GetOrCreate(key)
	if err != nil {
		return err
	}
	if s.Metadata == nil {
		s.Metadata = make(map[string]any)
	}
	s.Metadata["summary"] = summary
	return m.Save(s)
}

// SessionInfo holds metadata for a session (for list/CLI display).
type SessionInfo struct {
	Key       string
	Summary   string
	CreatedAt string
	UpdatedAt string
	Path      string
}

func (m *Manager) ListSessions() ([]string, error) {
	infos, err := m.ListSessionInfos()
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(infos))
	for i, info := range infos {
		keys[i] = info.Key
	}
	return keys, nil
}

// ListSessionInfos returns session metadata for CLI display (key, created_at, updated_at, path).
func (m *Manager) ListSessionInfos() ([]SessionInfo, error) {
	dir, err := m.sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var infos []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".jsonl")
		key := strings.Replace(stem, "_", ":", 1)
		path := filepath.Join(dir, e.Name())
		info := SessionInfo{Key: key, Path: path}
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			if scanner.Scan() {
				var meta map[string]any
				if json.Unmarshal(scanner.Bytes(), &meta) == nil && meta["_type"] == "metadata" {
					info.CreatedAt, _ = meta["created_at"].(string)
					info.UpdatedAt, _ = meta["updated_at"].(string)
					if m, ok := meta["metadata"].(map[string]any); ok {
						info.Summary, _ = m["summary"].(string)
					}
				}
			}
			f.Close()
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		a, b := infos[i].UpdatedAt, infos[j].UpdatedAt
		if a == "" {
			a = infos[i].CreatedAt
		}
		if b == "" {
			b = infos[j].CreatedAt
		}
		return a > b
	})
	return infos, nil
}

func (m *Manager) ClearSession(s *Session) {
	if s == nil {
		return
	}
	s.Messages = []map[string]any{}
	s.LastConsolidated = 0
	s.SavedCount = 0
	s.UpdatedAt = time.Now()
}

// Invalidate removes a session from the in-memory cache so the next GetOrCreate
// loads fresh from disk. Call after /new to ensure a clean session state.
func (m *Manager) Invalidate(key string) {
	m.mu.Lock()
	delete(m.cache, key)
	m.mu.Unlock()
}

func (m *Manager) UnconsolidatedCount(s *Session) int {
	if s == nil {
		return 0
	}
	return len(s.Messages) - s.LastConsolidated
}

func (m *Manager) GetHistory(s *Session) []map[string]any {
	if s == nil {
		return nil
	}
	return s.Messages
}

// GetHistoryAligned returns unconsolidated messages aligned to a user turn.
// Drops leading non-user messages to avoid orphaned tool_result blocks.
// Limits to maxMessages (0 = no limit).
func (m *Manager) GetHistoryAligned(s *Session, maxMessages int) []map[string]any {
	if s == nil {
		return nil
	}
	unconsolidated := s.Messages[s.LastConsolidated:]
	var sliced []map[string]any
	if maxMessages <= 0 || len(unconsolidated) <= maxMessages {
		sliced = unconsolidated
	} else {
		sliced = unconsolidated[len(unconsolidated)-maxMessages:]
	}
	// Drop leading non-user messages to avoid orphaned tool_result blocks
	for i, msg := range sliced {
		if role, _ := msg["role"].(string); role == "user" {
			return sliced[i:]
		}
	}
	// If no user message found (shouldn't happen normally, but can occur after truncation),
	// return at least the last few messages to preserve some context
	// This prevents complete loss of history when truncation removes all user messages
	if len(sliced) > 0 {
		// Keep at least the last 3 messages (usually includes current user message + recent context)
		keepCount := 3
		if len(sliced) < keepCount {
			keepCount = len(sliced)
		}
		return sliced[len(sliced)-keepCount:]
	}
	return nil
}
