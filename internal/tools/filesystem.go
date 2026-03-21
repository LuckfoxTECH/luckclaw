package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

type ReadFileTool struct {
	AllowedDir string // when set, restricts path to this dir
	BaseDir    string // when set, relative paths resolve relative to this (e.g. workspace)
}

func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Read the contents of a file at the given path. Supports text files, PDF (extracts text, up to 20MB), and images (returns base64 data URI for vision models)."
}
func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to read",
			},
		},
		"required": []any{"path"},
	}
}

const (
	maxReadFileSize  = 128 * 1024       // 128KB for text files
	maxPDFFileSize   = 20 * 1024 * 1024 // 20MB for PDF
	maxExtractedText = 200 * 1024       // 200KB max extracted text (e.g. from PDF)
	maxImageFileSize = 10 * 1024 * 1024 // 10MB for images
)

var imageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	p, _ := args["path"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := resolvePath(p, t.BaseDir, t.AllowedDir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(resolved))
	if ext == ".pdf" {
		return t.readPDF(resolved, info.Size())
	}

	if mimeType, ok := imageExtensions[ext]; ok {
		return t.readImage(resolved, info.Size(), mimeType)
	}

	if info.Size() > maxReadFileSize {
		return "", fmt.Errorf("file is too large (%d bytes, limit is %d bytes). Use exec with head/tail to read portions", info.Size(), maxReadFileSize)
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}
	return string(b), nil
}

func (t *ReadFileTool) readPDF(path string, size int64) (string, error) {
	if size > maxPDFFileSize {
		return "", fmt.Errorf("PDF is too large (%d bytes, limit is %d bytes)", size, maxPDFFileSize)
	}
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading PDF: %w", err)
	}
	defer f.Close()

	plainReader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extracting PDF text: %w", err)
	}
	if c, ok := plainReader.(io.Closer); ok {
		defer c.Close()
	}
	buf := &bytes.Buffer{}
	if _, err := io.CopyN(buf, plainReader, maxExtractedText+1); err != nil && err != io.EOF {
		return "", fmt.Errorf("reading PDF content: %w", err)
	}
	content := buf.String()
	if len(content) > maxExtractedText {
		content = content[:maxExtractedText] + "\n\n[... truncated, PDF text exceeds " + fmt.Sprintf("%d", maxExtractedText) + " chars ...]"
	}
	if strings.TrimSpace(content) == "" {
		return "[PDF: no extractable text (may be scanned/image-based)]", nil
	}
	return content, nil
}

func (t *ReadFileTool) readImage(path string, size int64, mimeType string) (string, error) {
	if size > maxImageFileSize {
		return "", fmt.Errorf("image is too large (%d bytes, limit is %d bytes)", size, maxImageFileSize)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading image: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(b)
	return fmt.Sprintf("[image: %s;base64,%s]", mimeType, encoded), nil
}

type WriteFileTool struct {
	AllowedDir string
	BaseDir    string
}

func (t *WriteFileTool) Name() string { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write content to a file (creates parent directories if needed)."
}
func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write",
			},
		},
		"required": []any{"path", "content"},
	}
}
func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	p, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := resolvePath(p, t.BaseDir, t.AllowedDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}
	return fmt.Sprintf("Successfully wrote %s", p), nil
}

type EditFileTool struct {
	AllowedDir string
	BaseDir    string
}

func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Edit a file by replacing specific text."
}
func (t *EditFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to replace",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The replacement text",
			},
		},
		"required": []any{"path", "old_text", "new_text"},
	}
}
func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	p, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := resolvePath(p, t.BaseDir, t.AllowedDir)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("editing file: %w", err)
	}
	content := string(b)
	if !strings.Contains(content, oldText) {
		return "", fmt.Errorf("%s", editFileNotFoundHint(content, oldText, p))
	}
	if strings.Count(content, oldText) > 1 {
		return fmt.Sprintf("Warning: old_text appears %d times. Please provide more context to make it unique.", strings.Count(content, oldText)), nil
	}
	updated := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("editing file: %w", err)
	}
	return fmt.Sprintf("Successfully edited %s", p), nil
}

type ListDirTool struct {
	AllowedDir string
	BaseDir    string
}

func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string {
	return "List contents of a directory."
}
func (t *ListDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The directory path to list",
			},
		},
		"required": []any{"path"},
	}
}
func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	p, _ := args["path"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := resolvePath(p, t.BaseDir, t.AllowedDir)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("listing directory: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Sprintf("Directory %s is empty", p), nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name = "📁 " + name
		} else {
			name = "📄 " + name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

// resolvePath resolves path to an absolute path. baseDir is used for relative paths when set.
// allowedDir, when set, restricts the result to that directory.
func resolvePath(path string, baseDir string, allowedDir string) (string, error) {
	expanded := strings.TrimSpace(path)
	if expanded == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if expanded == "~" {
			expanded = home
		} else if strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~/"))
		}
	} else if baseDir != "" && !filepath.IsAbs(expanded) {
		// Relative path: resolve relative to baseDir (e.g. workspace)
		expanded = filepath.Join(baseDir, expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			real = abs
		} else {
			return "", err
		}
	}
	if strings.TrimSpace(allowedDir) == "" {
		return real, nil
	}
	allowedAbs, err := filepath.Abs(allowedDir)
	if err != nil {
		return "", err
	}
	allowedReal, err := filepath.EvalSymlinks(allowedAbs)
	if err != nil {
		if os.IsNotExist(err) {
			allowedReal = allowedAbs
		} else {
			return "", err
		}
	}
	allowedReal = filepath.Clean(allowedReal)
	real = filepath.Clean(real)
	if real == allowedReal {
		return real, nil
	}
	
	// Add trailing separator to allowedReal for safe prefix checking, 
	// but handle the case where allowedReal is already "/" (root directory)
	allowedPrefix := allowedReal
	if !strings.HasSuffix(allowedPrefix, string(filepath.Separator)) {
		allowedPrefix += string(filepath.Separator)
	}

	if !strings.HasPrefix(real, allowedPrefix) {
		return "", fmt.Errorf("path %s is outside allowed directory %s", path, allowedReal)
	}
	return real, nil
}

// editFileNotFoundHint returns a helpful error when old_text is not found,
// including a best-match similarity hint (like Python's difflib.SequenceMatcher).
func editFileNotFoundHint(content, oldText, path string) string {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 || len(contentLines) == 0 {
		return fmt.Sprintf("old_text not found in %s. No similar text found. Verify the file content.", path)
	}
	window := len(oldLines)
	if window > len(contentLines) {
		window = len(contentLines)
	}
	bestRatio, bestStart := 0.0, 0
	for i := 0; i <= len(contentLines)-window; i++ {
		matches := 0
		for j := 0; j < window; j++ {
			if contentLines[i+j] == oldLines[j] {
				matches++
			}
		}
		ratio := float64(matches) / float64(len(oldLines))
		if ratio > bestRatio {
			bestRatio, bestStart = ratio, i
		}
	}
	if bestRatio > 0.5 {
		bestLines := contentLines[bestStart:]
		if len(bestLines) > window {
			bestLines = bestLines[:window]
		}
		snippet := strings.Join(bestLines, "\n")
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return fmt.Sprintf("old_text not found in %s.\nBest match (%.0f%% similar) at line %d:\n--- actual content ---\n%s",
			path, bestRatio*100, bestStart+1, snippet)
	}
	return fmt.Sprintf("old_text not found in %s. No similar text found. Verify the file content.", path)
}
