package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"luckclaw/internal/paths"
	"luckclaw/internal/tools"
)

type Entry struct {
	Type         string        `json:"type"`
	SSH          tools.SSHConn `json:"ssh"`
	RemoteBins   []string      `json:"remote_bins,omitempty"`
	RemoteHome   string        `json:"remote_home,omitempty"`
	SyncedSkills []string      `json:"synced_skills,omitempty"`
	UpdatedAt    string        `json:"updated_at,omitempty"`
}

type Store struct {
	Default   string           `json:"default,omitempty"`
	Terminals map[string]Entry `json:"terminals"`
	UpdatedAt string           `json:"updated_at,omitempty"`
}

func Path() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "terminals.json"), nil
}

func Load() (Store, error) {
	p, err := Path()
	if err != nil {
		return Store{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Store{Terminals: map[string]Entry{}}, nil
		}
		return Store{}, err
	}
	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		return Store{}, err
	}
	if s.Terminals == nil {
		s.Terminals = map[string]Entry{}
	}
	return s, nil
}

func Save(s Store) error {
	p, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if s.Terminals == nil {
		s.Terminals = map[string]Entry{}
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func Names(s Store) []string {
	var out []string
	for k := range s.Terminals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Get(s Store, name string) (Entry, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, false
	}
	it, ok := s.Terminals[name]
	return it, ok
}

func Set(s Store, name string, e Entry) (Store, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return s, fmt.Errorf("name is required")
	}
	if s.Terminals == nil {
		s.Terminals = map[string]Entry{}
	}
	e.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	s.Terminals[name] = e
	return s, nil
}

func Remove(s Store, name string) (Store, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return s, fmt.Errorf("name is required")
	}
	if s.Terminals == nil {
		return s, nil
	}
	delete(s.Terminals, name)
	if s.Default == name {
		s.Default = ""
	}
	return s, nil
}
