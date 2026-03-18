//go:build !nobrowser

package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// BrowserTool controls a remote browser for web automation.
// Reference: https://openclaw-docs.dx3n.cn/tutorials/tools/browser
// Session is persisted across tool calls so navigate→wait→screenshot share the same page.
type BrowserTool struct {
	RemoteURL   string            // wss://chrome.browserless.io?token=...
	Profile     string            // browser profile name for isolation
	SnapshotDir string            // directory to save screenshots (refs)
	DebugPort   int               // 0 = disabled; non-zero enables CDP debug
	refs        map[string]string // refId -> file path
	refMu       sync.RWMutex

	// Session persistence: reuse browser/page across Execute() calls
	sessionMu        sync.Mutex
	sessionBrowser   *rod.Browser
	sessionPage      *rod.Page
	sessionRemoteURL string
}

func (t *BrowserTool) Name() string { return "browser" }

func (t *BrowserTool) Description() string {
	return `Control a remote browser for web automation (Playwright-style). Actions: navigate, screenshot (returns ref), click, fill (clear+type), type, hover, check/uncheck, select_option, press, get_text, pdf, wait, snapshot, get_ref, status. For fill/type on search pages, prefer tryCommonSelectors=true or selectors: [".nav-search-input","#sb_form_q","input[name='q']"]. Shadow DOM: search=true. Supports Snapshot/Refs.`
}

func (t *BrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action: navigate, screenshot, click, fill, type, hover, check, uncheck, select_option, press, get_text, pdf, wait, snapshot, get_ref, status, close",
				"enum":        []any{"navigate", "screenshot", "click", "fill", "type", "hover", "check", "uncheck", "select_option", "press", "get_text", "pdf", "wait", "snapshot", "get_ref", "status", "close"},
			},
			"url": map[string]any{
				"type":        "string",
				"description": "URL to navigate to (for action=navigate)",
			},
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector for click/type/wait",
			},
			"selectors": map[string]any{
				"type":        "array",
				"description": "Try multiple selectors in order until one works. Use for fill/type when page structure is unknown. Example: [\".nav-search-input\", \"#sb_form_q\", \"input[name='q']\"]",
			},
			"search": map[string]any{
				"type":        "boolean",
				"description": "When true, search DOM including shadow DOM for selector (text, CSS, or XPath). Use for Bing Chat and other shadow-DOM inputs",
			},
			"tryCommonSelectors": map[string]any{
				"type":        "boolean",
				"description": "When true, after trying selector(s), also try common search box selectors (Bilibili .nav-search-input, Bing #sb_form_q, Google input[name='q'], etc.). Recommended for fill/type on search pages.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to type (for action=type)",
			},
			"waitStrategy": map[string]any{
				"type":        "string",
				"description": "Wait strategy: selector (wait for element), network_idle, or timeout (fixed ms)",
				"enum":        []any{"selector", "network_idle", "timeout"},
			},
			"waitTimeoutMs": map[string]any{
				"type":        "integer",
				"description": "Timeout in ms for wait (default 10000)",
			},
			"refId": map[string]any{
				"type":        "string",
				"description": "Ref ID from previous screenshot (for action=get_ref)",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Alias for refId",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Key for press: Enter, Tab, Backspace, Escape, etc.",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Option value for select_option; or attribute name for get_attribute",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Option label for select_option (alternative to value)",
			},
		},
		"required": []any{"action"},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if strings.TrimSpace(t.RemoteURL) == "" {
		return "", fmt.Errorf("browser not configured. Set tools.browser.remoteUrl (default wss://chrome.browserless.io) and tools.browser.token (or BROWSERLESS_TOKEN env)")
	}

	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	remoteURL := t.RemoteURL

	switch action {
	case "get_ref":
		return t.getRef(args)
	case "status":
		return fmt.Sprintf("Browser: remoteUrl=%s, snapshotDir=%s, debugPort=%d. For CDP debug with remote, use the service dashboard (e.g. Browserless).", t.RemoteURL, t.SnapshotDir, t.DebugPort), nil
	case "close":
		return t.closeSession(), nil
	case "navigate", "screenshot", "click", "fill", "type", "hover", "check", "uncheck", "select_option", "press", "get_text", "pdf", "wait", "snapshot":
		return t.withBrowser(ctx, remoteURL, args)
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func (t *BrowserTool) getRef(args map[string]any) (string, error) {
	refId, _ := args["refId"].(string)
	if refId == "" {
		refId, _ = args["ref"].(string)
	}
	refId = strings.TrimSpace(refId)
	if refId == "" {
		return "", fmt.Errorf("refId or ref is required for get_ref")
	}

	t.refMu.RLock()
	path := t.refs[refId]
	t.refMu.RUnlock()

	if path == "" {
		return "", fmt.Errorf("ref %q not found", refId)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read ref %q: %w", refId, err)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("Ref %q: base64 PNG (%d bytes). Use this for vision/analysis.", refId, len(data)) + "\n\n" + b64[:min(200, len(b64))] + "...", nil
}

func (t *BrowserTool) closeSession() string {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	if t.sessionBrowser != nil {
		t.sessionBrowser.MustClose()
		t.sessionBrowser = nil
		t.sessionPage = nil
		t.sessionRemoteURL = ""
		return "Browser session closed."
	}
	return "No active browser session."
}

// getOrCreateSession returns a page bound to ctx. Reuses existing session if remoteURL matches.
func (t *BrowserTool) getOrCreateSession(ctx context.Context, remoteURL string) (*rod.Page, error) {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()

	if t.sessionPage != nil && t.sessionRemoteURL == remoteURL {
		return t.sessionPage.Context(ctx), nil
	}

	if t.sessionBrowser != nil {
		t.sessionBrowser.MustClose()
		t.sessionBrowser = nil
		t.sessionPage = nil
	}

	browser := rod.New().ControlURL(remoteURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		browser.MustClose()
		return nil, fmt.Errorf("create page: %w", err)
	}
	t.sessionBrowser = browser
	t.sessionPage = page
	t.sessionRemoteURL = remoteURL
	return page.Context(ctx), nil
}

// browserActionTimeout limits each browser action to avoid hanging (e.g. element wait, Browserless timeout).
const browserActionTimeout = 60 * time.Second

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "closed network connection") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "use of closed network connection")
}

func (t *BrowserTool) withBrowser(ctx context.Context, remoteURL string, args map[string]any) (string, error) {
	timeout := browserActionTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < timeout {
			timeout = d
		}
	}

	action, _ := args["action"].(string)
	action = strings.ToLower(action)

	tryOnce := func(actxCtx context.Context) (string, error) {
		page, err := t.getOrCreateSession(actxCtx, remoteURL)
		if err != nil {
			return "", fmt.Errorf("get session: %w", err)
		}
		return t.runAction(page.Context(actxCtx), action, args)
	}

	actxCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := tryOnce(actxCtx)
	if err != nil && isConnectionError(err) {
		// Connection dead (e.g. Browserless timeout). Clear session and retry with fresh connection and fresh context.
		t.sessionMu.Lock()
		if t.sessionBrowser != nil {
			t.sessionBrowser.MustClose()
			t.sessionBrowser = nil
			t.sessionPage = nil
			t.sessionRemoteURL = ""
		}
		t.sessionMu.Unlock()
		cancel() // release first context before creating new one
		actxCtx2, cancel2 := context.WithTimeout(ctx, timeout)
		defer cancel2()
		result, err = tryOnce(actxCtx2)
	}
	if err != nil {
		t.sessionMu.Lock()
		if t.sessionBrowser != nil {
			t.sessionBrowser.MustClose()
			t.sessionBrowser = nil
			t.sessionPage = nil
			t.sessionRemoteURL = ""
		}
		t.sessionMu.Unlock()
		return "", err
	}
	return result, nil
}

func (t *BrowserTool) runAction(page *rod.Page, action string, args map[string]any) (string, error) {
	switch action {
	case "navigate":
		return t.doNavigate(page, args)
	case "screenshot":
		return t.doScreenshot(page, args)
	case "click":
		return t.doClick(page, args)
	case "fill":
		return t.doFill(page, args)
	case "type":
		return t.doType(page, args)
	case "hover":
		return t.doHover(page, args)
	case "check":
		return t.doCheck(page, args, true)
	case "uncheck":
		return t.doCheck(page, args, false)
	case "select_option":
		return t.doSelectOption(page, args)
	case "press":
		return t.doPress(page, args)
	case "get_text":
		return t.doGetText(page, args)
	case "pdf":
		return t.doPDF(page, args)
	case "wait":
		return t.doWait(page, args)
	case "snapshot":
		return t.doSnapshot(page, args)
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func (t *BrowserTool) doNavigate(page *rod.Page, args map[string]any) (string, error) {
	url, _ := args["url"].(string)
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("url is required for navigate")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("navigate failed: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return "", fmt.Errorf("wait load failed: %w", err)
	}
	return fmt.Sprintf("Navigated to %s", url), nil
}

func (t *BrowserTool) doScreenshot(page *rod.Page, args map[string]any) (string, error) {
	dir := t.SnapshotDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "luckclaw-screenshots")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create snapshot dir: %w", err)
	}

	refId := fmt.Sprintf("snap-%d", time.Now().UnixNano())
	path := filepath.Join(dir, refId+".png")

	data, err := page.Screenshot(true, nil)
	if err != nil {
		return "", fmt.Errorf("screenshot failed: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save screenshot: %w", err)
	}

	t.refMu.Lock()
	if t.refs == nil {
		t.refs = make(map[string]string)
	}
	t.refs[refId] = path
	t.refMu.Unlock()

	return fmt.Sprintf("Screenshot saved. refId: %q (path: %s). Use send_file with path %q to deliver to user, or get_ref to retrieve.", refId, path, path), nil
}

// perSelectorTimeout limits each selector attempt so we can try multiple within the 60s action timeout.
const perSelectorTimeout = 6 * time.Second

// commonSearchSelectors are tried in order when selectors/selector fail (for fill/type only).
var commonSearchSelectors = []string{
	".nav-search-input", // Bilibili
	"#sb_form_q",        // Bing
	"input[name=\"q\"]", // Google, DuckDuckGo
	"input[type=\"search\"]",
	"input[placeholder*=\"搜索\"]",
	"input[placeholder*=\"search\"]",
	".search-input",
	".search-input-el",
}

func (t *BrowserTool) selectorList(args map[string]any, withCommonFallback bool) []string {
	var list []string
	if s, ok := args["selector"].(string); ok && strings.TrimSpace(s) != "" {
		list = append(list, strings.TrimSpace(s))
	}
	if arr, ok := args["selectors"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				list = append(list, strings.TrimSpace(s))
			}
		}
	}
	if withCommonFallback && len(list) > 0 {
		for _, s := range commonSearchSelectors {
			dup := false
			for _, x := range list {
				if x == s {
					dup = true
					break
				}
			}
			if !dup {
				list = append(list, s)
			}
		}
	}
	return list
}

func (t *BrowserTool) getElement(page *rod.Page, args map[string]any) (*rod.Element, string, error) {
	useSearch := false
	if b, ok := args["search"].(bool); ok && b {
		useSearch = true
	}
	if v, ok := args["search"].(float64); ok && v != 0 {
		useSearch = true
	}
	withCommon := false
	if b, ok := args["tryCommonSelectors"].(bool); ok && b {
		withCommon = true
	}
	if v, ok := args["tryCommonSelectors"].(float64); ok && v != 0 {
		withCommon = true
	}
	// Default tryCommonSelectors for fill/type when not explicitly set
	if args["tryCommonSelectors"] == nil {
		act, _ := args["action"].(string)
		if act == "fill" || act == "type" {
			withCommon = true
		}
	}

	list := t.selectorList(args, withCommon)
	if len(list) == 0 {
		return nil, "", fmt.Errorf("selector or selectors is required")
	}

	var lastErr error
	for _, sel := range list {
		el, err := t.tryGetElement(page, sel, useSearch)
		if err == nil {
			return el, sel, nil
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("tried %d selector(s), last: %w", len(list), lastErr)
}

func (t *BrowserTool) tryGetElement(page *rod.Page, sel string, useSearch bool) (*rod.Element, error) {
	if useSearch {
		sr, err := page.Timeout(perSelectorTimeout).Search(sel)
		if err != nil {
			return nil, fmt.Errorf("search %q: %w", sel, err)
		}
		defer sr.Release()
		if sr.First == nil {
			return nil, fmt.Errorf("no element for search %q", sel)
		}
		return sr.First, nil
	}
	el, err := page.Timeout(perSelectorTimeout).Element(sel)
	if err != nil {
		return nil, fmt.Errorf("element %q: %w", sel, err)
	}
	return el, nil
}

func (t *BrowserTool) doClick(page *rod.Page, args map[string]any) (string, error) {
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}
	return fmt.Sprintf("Clicked %q", sel), nil
}

func (t *BrowserTool) doFill(page *rod.Page, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	_ = el.Click(proto.InputMouseButtonLeft, 1)
	_ = page.Keyboard.Press(input.ControlLeft)
	_ = page.Keyboard.Type(input.KeyA)
	_ = page.Keyboard.Release(input.KeyA)
	_ = page.Keyboard.Release(input.ControlLeft)
	if err := el.Input(text); err != nil {
		// Fallback for contenteditable: click to focus, then insert text
		_ = el.Click(proto.InputMouseButtonLeft, 1)
		if err := page.InsertText(text); err != nil {
			return "", fmt.Errorf("fill failed: %w", err)
		}
	}
	return fmt.Sprintf("Filled %q with %q", sel, text), nil
}

func (t *BrowserTool) doType(page *rod.Page, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return "", fmt.Errorf("text is required for type")
	}
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	if err := el.Input(text); err != nil {
		// Fallback for contenteditable: click to focus, then insert text
		_ = el.Click(proto.InputMouseButtonLeft, 1)
		if err := page.InsertText(text); err != nil {
			return "", fmt.Errorf("type failed: %w", err)
		}
	}
	return fmt.Sprintf("Typed into %q", sel), nil
}

func (t *BrowserTool) doHover(page *rod.Page, args map[string]any) (string, error) {
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	if err := el.Hover(); err != nil {
		return "", fmt.Errorf("hover failed: %w", err)
	}
	return fmt.Sprintf("Hovered over %q", sel), nil
}

func (t *BrowserTool) doCheck(page *rod.Page, args map[string]any, checked bool) (string, error) {
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	cur, _ := el.Attribute("checked")
	isChecked := cur != nil && *cur != "" && *cur != "false"
	if isChecked != checked {
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return "", fmt.Errorf("check/uncheck failed: %w", err)
		}
	}
	action := "checked"
	if !checked {
		action = "unchecked"
	}
	return fmt.Sprintf("%s %q", action, sel), nil
}

func (t *BrowserTool) doSelectOption(page *rod.Page, args map[string]any) (string, error) {
	value, _ := args["value"].(string)
	value = strings.TrimSpace(value)
	label, _ := args["label"].(string)
	label = strings.TrimSpace(label)
	if value == "" && label == "" {
		return "", fmt.Errorf("value or label is required for select_option")
	}
	el, sel, err := t.getElement(page, args)
	if err != nil {
		return "", err
	}
	if value != "" {
		cssSel := fmt.Sprintf(`[value="%s"]`, strings.ReplaceAll(value, `"`, `\"`))
		if err := el.Select([]string{cssSel}, true, rod.SelectorTypeCSSSector); err != nil {
			return "", fmt.Errorf("select_option by value failed: %w", err)
		}
		return fmt.Sprintf("Selected option value=%q in %q", value, sel), nil
	}
	if err := el.Select([]string{label}, true, rod.SelectorTypeText); err != nil {
		return "", fmt.Errorf("select_option by label failed: %w", err)
	}
	return fmt.Sprintf("Selected option label=%q in %q", label, sel), nil
}

func (t *BrowserTool) doPress(page *rod.Page, args map[string]any) (string, error) {
	key, _ := args["key"].(string)
	if key == "" {
		key, _ = args["text"].(string)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("key is required for press")
	}
	sel, _ := args["selector"].(string)
	sel = strings.TrimSpace(sel)
	keyInput := parseKey(key)
	if keyInput == nil {
		return "", fmt.Errorf("unknown key %q (use Enter, Tab, Backspace, Escape, etc.)", key)
	}
	if sel != "" {
		el, err := page.Element(sel)
		if err != nil {
			return "", fmt.Errorf("element not found for selector %q: %w", sel, err)
		}
		if err := el.Type(*keyInput); err != nil {
			return "", fmt.Errorf("press %q failed: %w", key, err)
		}
		return fmt.Sprintf("Pressed %q on %q", key, sel), nil
	}
	if err := page.Keyboard.Type(*keyInput); err != nil {
		return "", fmt.Errorf("press %q failed: %w", key, err)
	}
	return fmt.Sprintf("Pressed %q", key), nil
}

func parseKey(s string) *input.Key {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "enter", "return":
		k := input.Enter
		return &k
	case "tab":
		k := input.Tab
		return &k
	case "backspace":
		k := input.Backspace
		return &k
	case "escape", "esc":
		k := input.Escape
		return &k
	case "space":
		k := input.Space
		return &k
	case "arrowup", "up":
		k := input.ArrowUp
		return &k
	case "arrowdown", "down":
		k := input.ArrowDown
		return &k
	case "arrowleft", "left":
		k := input.ArrowLeft
		return &k
	case "arrowright", "right":
		k := input.ArrowRight
		return &k
	default:
		if len(s) == 1 {
			k := input.Key(s[0])
			return &k
		}
		return nil
	}
}

func (t *BrowserTool) doGetText(page *rod.Page, args map[string]any) (string, error) {
	sel, _ := args["selector"].(string)
	sel = strings.TrimSpace(sel)
	if sel == "" {
		text, err := page.Element("body")
		if err != nil {
			return "", err
		}
		txt, err := text.Text()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(txt), nil
	}
	el, err := page.Element(sel)
	if err != nil {
		return "", fmt.Errorf("element not found for selector %q: %w", sel, err)
	}
	txt, err := el.Text()
	if err != nil {
		return "", fmt.Errorf("get_text failed: %w", err)
	}
	return strings.TrimSpace(txt), nil
}

func (t *BrowserTool) doPDF(page *rod.Page, args map[string]any) (string, error) {
	dir := t.SnapshotDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "luckclaw-screenshots")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create dir: %w", err)
	}
	refId := fmt.Sprintf("pdf-%d", time.Now().UnixNano())
	path := filepath.Join(dir, refId+".pdf")
	sr, err := page.PDF(&proto.PagePrintToPDF{})
	if err != nil {
		return "", fmt.Errorf("pdf failed: %w", err)
	}
	defer sr.Close()
	data, err := io.ReadAll(sr)
	if err != nil {
		return "", fmt.Errorf("failed to read pdf: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save pdf: %w", err)
	}
	t.refMu.Lock()
	if t.refs == nil {
		t.refs = make(map[string]string)
	}
	t.refs[refId] = path
	t.refMu.Unlock()
	return fmt.Sprintf("PDF saved. refId: %q (path: %s). Use get_ref to retrieve.", refId, path), nil
}

func (t *BrowserTool) doWait(page *rod.Page, args map[string]any) (string, error) {
	timeout := 10000
	if v, ok := args["waitTimeoutMs"].(float64); ok && v > 0 {
		timeout = int(v)
	}
	if v, ok := args["waitTimeoutMs"].(int); ok && v > 0 {
		timeout = v
	}
	dur := time.Duration(timeout) * time.Millisecond

	strategy, _ := args["waitStrategy"].(string)
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	selector, _ := args["selector"].(string)
	selector = strings.TrimSpace(selector)

	ctx, cancel := context.WithTimeout(page.GetContext(), dur)
	defer cancel()
	page = page.Context(ctx)

	switch strategy {
	case "selector", "element":
		if selector == "" {
			return "", fmt.Errorf("selector is required for wait strategy %q", "selector")
		}
		_, err := page.Timeout(dur).Element(selector)
		if err != nil {
			return "", fmt.Errorf("wait for selector %q failed: %w", selector, err)
		}
		return fmt.Sprintf("Waited for selector %q", selector), nil
	case "network_idle":
		if err := page.WaitLoad(); err != nil {
			return "", fmt.Errorf("wait network_idle failed: %w", err)
		}
		// rod WaitLoad waits for load event; network idle needs extra
		time.Sleep(500 * time.Millisecond)
		return "Waited for network idle", nil
	case "timeout", "time":
		time.Sleep(dur)
		return fmt.Sprintf("Waited %d ms", timeout), nil
	default:
		// Default: wait for load
		if err := page.WaitLoad(); err != nil {
			return "", fmt.Errorf("wait failed: %w", err)
		}
		return "Page loaded", nil
	}
}

func (t *BrowserTool) doSnapshot(page *rod.Page, args map[string]any) (string, error) {
	// Use Accessibility API to get form controls (works across shadow DOM)
	res, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return "", fmt.Errorf("snapshot failed: %w", err)
	}
	var lines []string
	editableRoles := map[string]bool{"textbox": true, "searchbox": true, "combobox": true}
	for _, n := range res.Nodes {
		if n.Ignored || n.Role == nil {
			continue
		}
		role := axValueStr(n.Role)
		if !editableRoles[role] {
			continue
		}
		name := axValueStr(n.Name)
		val := axValueStr(n.Value)
		lines = append(lines, fmt.Sprintf("- role=%s name=%q value=%q (use search=true with name/placeholder for shadow DOM)", role, name, val))
	}
	if len(lines) == 0 {
		return "Snapshot: no textbox/searchbox/combobox found. Try main-DOM selectors: input, textarea, #sb_form_q (Bing), input[name=\"q\"] (Google).", nil
	}
	return "Snapshot (form controls):\n" + strings.Join(lines, "\n"), nil
}

func axValueStr(v *proto.AccessibilityAXValue) string {
	if v == nil {
		return ""
	}
	if val := v.Value.Val(); val != nil {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
