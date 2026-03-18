package clawhub

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lockfile format (compatible with clawhub CLI).
type Lockfile struct {
	Version string               `json:"version"`
	Skills  map[string]LockEntry `json:"skills"`
}

type LockEntry struct {
	Version     string `json:"version"`
	InstalledAt int64  `json:"installedAt"`
}

func lockfilePath(workdir string) string {
	return filepath.Join(workdir, ".clawhub", "lock.json")
}

func readLockfile(workdir string) (*Lockfile, error) {
	path := lockfilePath(workdir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Lockfile{Version: "1", Skills: map[string]LockEntry{}}, nil
		}
		return nil, err
	}
	var l Lockfile
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Skills == nil {
		l.Skills = map[string]LockEntry{}
	}
	return &l, nil
}

func writeLockfile(workdir string, l *Lockfile) error {
	dir := filepath.Dir(lockfilePath(workdir))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lockfilePath(workdir), data, 0644)
}

// Install installs a skill to workdir/skills/<slug>.
// If resourceConstrained is true, returns installation instructions instead of downloading.
func (c *Client) Install(workdir, slug, version string, force bool, resourceConstrained bool) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("slug required")
	}
	skillsDir := filepath.Join(workdir, "skills")
	target := filepath.Join(skillsDir, slug)

	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("already installed: %s (use --force to overwrite)", target)
	}

	meta, err := c.GetSkill(slug)
	if err != nil {
		return err
	}
	if meta.Moderation != nil && meta.Moderation.IsMalwareBlocked {
		return fmt.Errorf("blocked: %s is flagged as malicious", slug)
	}

	resolvedVersion := version
	if resolvedVersion == "" && meta.LatestVersion != nil {
		resolvedVersion = meta.LatestVersion.Version
	}
	if resolvedVersion == "" {
		return fmt.Errorf("could not resolve version for %s", slug)
	}

	// In resource-constrained mode, return installation instructions instead of downloading
	if resourceConstrained {
		installCmd := fmt.Sprintf("luckclaw clawhub install %s", slug)
		if resolvedVersion != "" && resolvedVersion != "latest" {
			installCmd = fmt.Sprintf("luckclaw clawhub install %s@%s", slug, resolvedVersion)
		}
		return fmt.Errorf("resource-constrained mode: auto-download disabled. Please run manually:\n  %s\n\nOr download from: %s/api/v1/download?slug=%s&version=%s",
			installCmd, c.Registry, slug, resolvedVersion)
	}

	zipData, err := c.DownloadZip(slug, resolvedVersion)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}
	if force {
		_ = os.RemoveAll(target)
	}
	if err := extractZip(zipData, target); err != nil {
		return err
	}

	lock, err := readLockfile(workdir)
	if err != nil {
		return err
	}
	if lock.Skills == nil {
		lock.Skills = map[string]LockEntry{}
	}
	lock.Skills[slug] = LockEntry{Version: resolvedVersion, InstalledAt: nowMillis()}
	return writeLockfile(workdir, lock)
}

// List returns installed skills from lockfile.
func List(workdir string) (map[string]LockEntry, error) {
	lock, err := readLockfile(workdir)
	if err != nil {
		return nil, err
	}
	return lock.Skills, nil
}

// Remove uninstalls a skill: deletes from lockfile and removes the skill directory.
func Remove(workdir, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("slug required")
	}
	if !isSafeSlug(slug) {
		return fmt.Errorf("invalid slug: %s", slug)
	}
	skillsDir := filepath.Join(workdir, "skills")
	target := filepath.Join(skillsDir, slug)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s: %w", target, err)
	}
	lock, err := readLockfile(workdir)
	if err != nil {
		return err
	}
	if lock.Skills != nil {
		delete(lock.Skills, slug)
		return writeLockfile(workdir, lock)
	}
	return nil
}

// Update updates one or all installed skills.
// resourceConstrained: if true, only provide suggestions, don't download
func (c *Client) Update(workdir, slug string, all, force bool, resourceConstrained bool) error {
	lock, err := readLockfile(workdir)
	if err != nil {
		return err
	}
	slugs := []string{}
	if slug != "" {
		if !isSafeSlug(slug) {
			return fmt.Errorf("invalid slug: %s", slug)
		}
		slugs = []string{slug}
	} else if all {
		for s := range lock.Skills {
			if isSafeSlug(s) {
				slugs = append(slugs, s)
			}
		}
	} else {
		return fmt.Errorf("provide <slug> or --all")
	}
	if len(slugs) == 0 {
		return nil // no skills to update
	}

	for _, s := range slugs {
		if err := c.Install(workdir, s, "", force, resourceConstrained); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func isSafeSlug(s string) bool {
	return s != "" && !strings.Contains(s, "/") && !strings.Contains(s, "\\") && !strings.Contains(s, "..")
}

func extractZip(data []byte, targetDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	targetClean := filepath.Clean(targetDir)
	for _, f := range zr.File {
		name := filepath.Join(targetDir, f.Name)
		abs, err := filepath.Abs(name)
		if err != nil {
			return err
		}
		targetAbs, _ := filepath.Abs(targetClean)
		if !strings.HasPrefix(abs, targetAbs+string(filepath.Separator)) && abs != targetAbs {
			return fmt.Errorf("zip path traversal: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(name, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(name)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(dst, rc)
		dst.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
