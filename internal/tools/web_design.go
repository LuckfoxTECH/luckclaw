package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"luckclaw/internal/logging"
	"luckclaw/internal/service"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

type WebDesignTool struct {
	Workspace           string
	AllowedDir          string
	RestrictToWorkspace bool
	Logger              logging.Logger
}

var (
	globalWebDesignSessionsMu sync.Mutex
	globalWebDesignSessions   = make(map[string]*webDesignSession)
)

// GlobalWebDesignStart attempts to start a web_design session created by the tool.
func GlobalWebDesignStart(sessionID string) error {
	globalWebDesignSessionsMu.Lock()
	s, ok := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if err := s.start(); err != nil {
		return err
	}
	service.GlobalRegistry().Update(sessionID, func(info *service.ServiceInfo) {
		info.Running = true
		info.Port = s.port
	})
	return nil
}

// GlobalWebDesignStop attempts to stop a web_design session created by the tool.
func GlobalWebDesignStop(sessionID string) error {
	globalWebDesignSessionsMu.Lock()
	s, ok := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	_ = s.stop()
	service.GlobalRegistry().Update(sessionID, func(info *service.ServiceInfo) {
		info.Running = false
	})
	return nil
}

var webDesignUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const webDesignWriteTimeout = 2 * time.Second

type webDesignSession struct {
	id   string
	dir  string
	host string
	port int

	mu      sync.Mutex
	running bool
	srv     *http.Server
	cancel  context.CancelFunc

	clients map[*websocket.Conn]*webDesignClient
	logger  logging.Logger

	baseDir    string
	allowedDir string
	bindings   map[string]webDesignBinding

	nextSeq int64
	events  []webDesignEvent
}

type webDesignClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}
type webDesignEvent struct {
	Seq       int64           `json:"seq"`
	Timestamp string          `json:"timestamp"`
	Raw       string          `json:"raw"`
	JSON      json.RawMessage `json:"json,omitempty"`
}

type webDesignBinding struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delta   int    `json:"delta,omitempty"`

	Broker   string `json:"broker,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Topic    string `json:"topic,omitempty"`
	QOS      int    `json:"qos,omitempty"`
	Retained bool   `json:"retained,omitempty"`

	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	UnitID    int    `json:"unit_id,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	Address   int    `json:"address,omitempty"`
	Value     int    `json:"value,omitempty"`
}

func (t *WebDesignTool) Name() string { return "web_design" }

func (t *WebDesignTool) Description() string {
	return "Generate an index.html and serve it over HTTP with a WebSocket control channel. HTTP endpoints: GET / (static), GET /api/status. Control endpoint: WebSocket /ws. Note: There is no REST write API; bindings are triggered only by WebSocket messages like {\"type\":\"action\",\"id\":\"...\"}. For MQTT and Modbus integration, prefer using shell scripts invoking mosquitto_sub/mosquitto_pub and mbpoll first; use bindings only when necessary."
}

func (t *WebDesignTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []any{"create", "update", "start", "stop", "list", "pull_events", "broadcast", "help"},
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Session identifier (optional for create; required for other actions)",
			},
			"requirements": map[string]any{
				"type":        "string",
				"description": "Natural language requirements for the web UI (optional)",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Page title (optional)",
			},
			"ui": map[string]any{
				"type":        "object",
				"description": "Structured UI spec object embedded into index.html (optional)",
			},
			"bindings": map[string]any{
				"type":        "object",
				"description": "Optional backend bindings. Map action id -> binding object, triggered by WebSocket message {\"type\":\"action\",\"id\":\"<action_id>\",...}. kind supports write_file, append_file, write_script, read_int, add_int, set_int, mqtt_publish, modbus_write_single_register, modbus_write_single_coil, modbus_read_single_coil. When dealing with MQTT/Modbus, prefer shell + mosquitto_sub/mosquitto_pub and mbpoll first; use bindings only when necessary. IMPORTANT: For Modbus/MQTT bindings, never use only 'device' alias; you MUST explicitly provide connection parameters (host, port, unit_id, etc.) by querying device config first. If you encounter 'host is required' errors, query the config and retry.",
			},
			"files": map[string]any{
				"type":        "object",
				"description": "Optional extra files to write into the session dir. Map relative path -> content string, or {content,mode}.",
			},
			"shell": map[string]any{
				"type":        "string",
				"description": "Optional shell script body to write into shell_path under the session dir.",
			},
			"shell_path": map[string]any{
				"type":        "string",
				"description": "Optional shell script file path under the session dir (default run.sh). Must be relative.",
			},
			"html": map[string]any{
				"type":        "string",
				"description": "Full HTML content for index.html (optional; overrides ui/requirements when provided)",
			},
			"output_dir": map[string]any{
				"type":        "string",
				"description": "Directory to write index.html (optional; relative paths are resolved against workspace)",
			},
			"host": map[string]any{
				"type":        "string",
				"description": "Bind host for HTTP server (default 0.0.0.0)",
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "Bind port for HTTP server (0 = auto)",
				"minimum":     0,
				"maximum":     65535,
			},
			"auto_start": map[string]any{
				"type":        "boolean",
				"description": "Auto start server after create/update (default true for create)",
			},
			"after_seq": map[string]any{
				"type":        "integer",
				"description": "For pull_events: return events with seq > after_seq (default 0)",
				"minimum":     0,
			},
			"message": map[string]any{
				"type":        "string",
				"description": "For broadcast: message payload to send to all connected clients",
			},
		},
		"required": []any{"action"},
	}
}

func (t *WebDesignTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "create":
		return t.handleCreate(args)
	case "update":
		return t.handleUpdate(args)
	case "start":
		return t.handleStart(args)
	case "stop":
		return t.handleStop(args)
	case "list":
		return t.handleList(args)
	case "pull_events":
		return t.handlePullEvents(args)
	case "broadcast":
		return t.handleBroadcast(args)
	case "help":
		return t.handleHelp(), nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *WebDesignTool) handleHelp() string {
	return strings.TrimSpace(`
web_design quick usage

Endpoints:
- HTTP: GET / (static index.html), GET /api/status
- WebSocket: /ws (control channel)

Important:
- No REST write APIs like /api/save_value.
- To trigger bindings, the page must send a WebSocket JSON message:
  {"type":"action","id":"<action_id>", ...}
- For MQTT/Modbus integrations, prefer using shell scripts with mosquitto_sub/mosquitto_pub and mbpoll first; use bindings only when necessary.
- For Modbus/MQTT bindings, never use only 'device' alias; you MUST explicitly provide connection parameters (host, port, unit_id, etc.) by querying device config first.
- For create/update, you MUST ALWAYS provide ui.controls (in ui object) or html. Do NOT just provide bindings when updating. You must resend the complete UI specification during updates.

Typical flow:
1) web_design create -> get "url" and "ws_url"
2) Open url in browser; page connects to ws_url automatically
3) Click buttons / submit input -> page sends action messages
4) Server executes matching bindings and broadcasts:
   {"type":"action_result","id":"<action_id>","ok":true,"result":"...","value":123}

Minimal create example (two buttons + input):
{
  "action": "create",
  "title": "Control Panel",
  "requirements": "A simple control panel with two large buttons and one input field.",
  "bindings": {
    "primary": { "kind": "write_file", "path": "state.txt", "content": "on\n" },
    "secondary": { "kind": "write_file", "path": "state.txt", "content": "off\n" },
    "input": { "kind": "write_file", "path": "input.txt", "content": "placeholder\n" }
  },
  "auto_start": true,
  "host": "0.0.0.0",
  "port": 0
}

Counter example:
{
  "action": "create",
  "title": "Counter",
  "requirements": "Show current integer in value file, with +1/-1 buttons and an input to set the value.",
  "ui": {
    "counter": { "path": "value", "readActionId": "read" },
    "controls": [
      { "type": "button", "id": "inc", "label": "+1", "size": "lg", "payload": { "type": "action", "id": "inc" } },
      { "type": "button", "id": "dec", "label": "-1", "size": "lg", "variant": "secondary", "payload": { "type": "action", "id": "dec" } },
      { "type": "text", "id": "set", "label": "Set value", "placeholder": "Enter number...", "payload": { "type": "action", "id": "set", "value": "$value" } }
    ]
  },
  "bindings": {
    "read": { "kind": "read_int", "path": "value" },
    "inc": { "kind": "add_int", "path": "value", "delta": 1 },
    "dec": { "kind": "add_int", "path": "value", "delta": -1 },
    "set": { "kind": "set_int", "path": "value" }
  },
  "auto_start": true
}

Modbus Coil Example (Must replace 192.168.1.10 with actual host):
{
  "action": "create",
  "title": "Modbus Panel",
  "ui": {
    "controls": [
      { "type": "button", "id": "coil_on", "label": "Coil ON", "payload": { "type": "action", "id": "coil_on" } },
      { "type": "button", "id": "coil_off", "label": "Coil OFF", "payload": { "type": "action", "id": "coil_off" } }
    ]
  },
  "bindings": {
    "coil_on":  { "kind": "modbus_write_single_coil", "host": "192.168.1.10", "port": 502, "unit_id": 1, "timeout_ms": 2000, "address": 10, "value": true },
    "coil_off": { "kind": "modbus_write_single_coil", "host": "192.168.1.10", "port": 502, "unit_id": 1, "timeout_ms": 2000, "address": 10, "value": false }
  },
  "auto_start": true,
  "host": "0.0.0.0",
  "port": 0
}
`)
}

func (t *WebDesignTool) handleCreate(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		sessionID = "wd_" + GenerateRunID()
	}
	start := time.Now()
	if t.Logger != nil {
		t.Logger.Info(fmt.Sprintf("[web_design] create start session=%s", sessionID))
		defer t.Logger.Info(fmt.Sprintf("[web_design] create done session=%s dur=%s", sessionID, time.Since(start)))
	}

	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Web Design"
	}

	outputDir, _ := args["output_dir"].(string)
	dir, err := t.resolveOutputDir(sessionID, outputDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	if err := writeWebDesignExtraFiles(dir, args["files"]); err != nil {
		return "", err
	}
	if err := writeWebDesignShell(dir, args["shell"], args["shell_path"]); err != nil {
		return "", err
	}

	html, _ := args["html"].(string)
	html = strings.TrimSpace(html)

	if html == "" {
		requirements, _ := args["requirements"].(string)
		var ui map[string]any
		if v, ok := args["ui"].(map[string]any); ok {
			ui = v
		}
		if ui == nil || ui["controls"] == nil {
			return "", fmt.Errorf("ui.controls or html is required")
		}
		built, err := buildWebDesignHTML(sessionID, title, requirements, ui)
		if err != nil {
			return "", err
		}
		html = built
	}

	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		return "", err
	}

	host := "0.0.0.0"
	if v, ok := args["host"].(string); ok && strings.TrimSpace(v) != "" {
		host = strings.TrimSpace(v)
	}
	port := 0
	if v, ok := args["port"].(float64); ok {
		port = int(v)
	}

	bindings, err := parseWebDesignBindings(args["bindings"])
	if err != nil {
		return "", err
	}

	s := &webDesignSession{
		id:      sessionID,
		dir:     dir,
		host:    host,
		port:    port,
		clients: make(map[*websocket.Conn]*webDesignClient),
		logger:  t.Logger,
		baseDir: t.Workspace,
		allowedDir: func() string {
			if t.RestrictToWorkspace {
				return t.AllowedDir
			}
			return ""
		}(),
		bindings: bindings,
	}

	globalWebDesignSessionsMu.Lock()
	if globalWebDesignSessions == nil {
		globalWebDesignSessions = make(map[string]*webDesignSession)
	}
	if old, ok := globalWebDesignSessions[sessionID]; ok {
		globalWebDesignSessionsMu.Unlock()
		_ = old.stop()
		globalWebDesignSessionsMu.Lock()
	}
	globalWebDesignSessions[sessionID] = s
	globalWebDesignSessionsMu.Unlock()

	autoStart := true
	if v, ok := args["auto_start"].(bool); ok {
		autoStart = v
	}
	if autoStart {
		if err := s.start(); err != nil {
			return "", err
		}
	}

	service.GlobalRegistry().Register(&service.ServiceInfo{
		ID:        s.id,
		Type:      service.TypeWebDesign,
		Name:      title,
		Dir:       dir,
		Host:      s.host,
		Port:      s.port,
		Running:   s.running,
		AutoStart: autoStart,
		Metadata: map[string]any{
			"session_id": s.id,
		},
	})

	return s.statusJSON(), nil
}

func (t *WebDesignTool) handleUpdate(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	start := time.Now()
	if t.Logger != nil {
		t.Logger.Info(fmt.Sprintf("[web_design] update start session=%s", sessionID))
		defer t.Logger.Info(fmt.Sprintf("[web_design] update done session=%s dur=%s", sessionID, time.Since(start)))
	}

	globalWebDesignSessionsMu.Lock()
	s := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()

	if s == nil {
		return "", fmt.Errorf("unknown session_id: %s", sessionID)
	}

	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Web Design"
	}

	html, _ := args["html"].(string)
	html = strings.TrimSpace(html)
	if html == "" {
		requirements, _ := args["requirements"].(string)
		var ui map[string]any
		if v, ok := args["ui"].(map[string]any); ok {
			ui = v
		}
		if ui == nil || ui["controls"] == nil {
			return "", fmt.Errorf("ui.controls or html is required")
		}
		built, err := buildWebDesignHTML(sessionID, title, requirements, ui)
		if err != nil {
			return "", err
		}
		html = built
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	if err := writeWebDesignExtraFiles(s.dir, args["files"]); err != nil {
		return "", err
	}
	if err := writeWebDesignShell(s.dir, args["shell"], args["shell_path"]); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(s.dir, "index.html"), []byte(html), 0o644); err != nil {
		return "", err
	}

	bindings, err := parseWebDesignBindings(args["bindings"])
	if err != nil {
		return "", err
	}
	if bindings != nil {
		s.mu.Lock()
		s.bindings = bindings
		if strings.TrimSpace(s.baseDir) == "" {
			s.baseDir = t.Workspace
		}
		if t.RestrictToWorkspace && strings.TrimSpace(s.allowedDir) == "" {
			s.allowedDir = t.AllowedDir
		}
		s.mu.Unlock()
	}

	s.broadcastJSON(map[string]any{"type": "reload"})

	autoStart := false
	if v, ok := args["auto_start"].(bool); ok {
		autoStart = v
	}
	if autoStart {
		if err := s.start(); err != nil {
			return "", err
		}
	}

	return s.statusJSON(), nil
}

func (t *WebDesignTool) handleStart(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	start := time.Now()
	if t.Logger != nil {
		t.Logger.Info(fmt.Sprintf("[web_design] start start session=%s", sessionID))
		defer t.Logger.Info(fmt.Sprintf("[web_design] start done session=%s dur=%s", sessionID, time.Since(start)))
	}

	globalWebDesignSessionsMu.Lock()
	s := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()

	if s == nil {
		return "", fmt.Errorf("unknown session_id: %s", sessionID)
	}
	if err := s.start(); err != nil {
		return "", err
	}

	service.GlobalRegistry().Update(sessionID, func(info *service.ServiceInfo) {
		info.Running = true
		info.Port = s.port
	})

	return s.statusJSON(), nil
}

func (t *WebDesignTool) handleStop(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	start := time.Now()
	if t.Logger != nil {
		t.Logger.Info(fmt.Sprintf("[web_design] stop start session=%s", sessionID))
		defer t.Logger.Info(fmt.Sprintf("[web_design] stop done session=%s dur=%s", sessionID, time.Since(start)))
	}

	globalWebDesignSessionsMu.Lock()
	s := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()

	if s == nil {
		return "", fmt.Errorf("unknown session_id: %s", sessionID)
	}
	_ = s.stop()

	service.GlobalRegistry().Update(sessionID, func(info *service.ServiceInfo) {
		info.Running = false
	})

	return s.statusJSON(), nil
}

func (t *WebDesignTool) handleList(args map[string]any) (string, error) {
	type item struct {
		SessionID string `json:"session_id"`
		Dir       string `json:"dir"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Running   bool   `json:"running"`
		URL       string `json:"url,omitempty"`
		WSURL     string `json:"ws_url,omitempty"`
	}

	globalWebDesignSessionsMu.Lock()
	defer globalWebDesignSessionsMu.Unlock()
	out := make([]item, 0, len(globalWebDesignSessions))
	for _, s := range globalWebDesignSessions {
		s.mu.Lock()
		running := s.running
		host := s.host
		port := s.port
		dir := s.dir
		s.mu.Unlock()
		it := item{
			SessionID: s.id,
			Dir:       dir,
			Host:      host,
			Port:      port,
			Running:   running,
		}
		if running && port > 0 {
			it.URL = fmt.Sprintf("http://%s:%d/", host, port)
			it.WSURL = fmt.Sprintf("ws://%s:%d/ws", host, port)
		}
		out = append(out, it)
	}
	b, _ := json.MarshalIndent(map[string]any{"sessions": out}, "", "  ")
	return string(b), nil
}

func (t *WebDesignTool) handlePullEvents(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}

	afterSeq := int64(0)
	if v, ok := args["after_seq"].(float64); ok && v > 0 {
		afterSeq = int64(v)
	}

	globalWebDesignSessionsMu.Lock()
	s := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()

	if s == nil {
		return "", fmt.Errorf("unknown session_id: %s", sessionID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var evs []webDesignEvent
	latest := afterSeq
	for _, e := range s.events {
		if e.Seq > afterSeq {
			evs = append(evs, e)
			latest = e.Seq
		}
	}
	b, _ := json.MarshalIndent(map[string]any{
		"session_id": sessionID,
		"after_seq":  afterSeq,
		"latest_seq": latest,
		"events":     evs,
	}, "", "  ")
	return string(b), nil
}

func (t *WebDesignTool) handleBroadcast(args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	sessionID = normalizeWebDesignID(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}

	msg, _ := args["message"].(string)
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", fmt.Errorf("message is required")
	}

	globalWebDesignSessionsMu.Lock()
	s := globalWebDesignSessions[sessionID]
	globalWebDesignSessionsMu.Unlock()

	if s == nil {
		return "", fmt.Errorf("unknown session_id: %s", sessionID)
	}

	if t.Logger != nil {
		t.Logger.Info(fmt.Sprintf("[web_design] broadcast session=%s message_len=%d", sessionID, len(msg)))
	}
	s.broadcastJSON(map[string]any{"type": "broadcast", "message": msg})
	return s.statusJSON(), nil
}

func (t *WebDesignTool) resolveOutputDir(sessionID, outputDir string) (string, error) {
	outputDir = strings.TrimSpace(outputDir)
	ws := strings.TrimSpace(t.Workspace)

	var dir string
	if outputDir == "" {
		if ws != "" {
			dir = filepath.Join(ws, ".web_design", sessionID)
		} else {
			dir = filepath.Join(os.TempDir(), "luckclaw_web_design", sessionID)
		}
	} else if filepath.IsAbs(outputDir) {
		dir = outputDir
	} else if ws != "" {
		dir = filepath.Join(ws, outputDir)
	} else {
		dir = filepath.Join(os.TempDir(), outputDir)
	}

	dir = filepath.Clean(dir)

	if t.RestrictToWorkspace && strings.TrimSpace(t.AllowedDir) != "" {
		base := filepath.Clean(t.AllowedDir)
		rel, err := filepath.Rel(base, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("output_dir must be inside workspace")
		}
	}
	return dir, nil
}

func (s *webDesignSession) start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	host := strings.TrimSpace(s.host)
	if host == "" {
		host = "0.0.0.0"
		s.host = host
	}
	port := s.port
	s.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_, actualPortStr, _ := net.SplitHostPort(ln.Addr().String())
	actualPort := port
	if actualPortStr != "" {
		if p, err := parsePort(actualPortStr); err == nil {
			actualPort = p
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(s.dir)))
	mux.HandleFunc("/ws", s.wsHandler)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.statusJSON()))
	})

	server := &http.Server{Handler: mux}
	runCtx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.port = actualPort
	s.srv = server
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	go func() {
		<-runCtx.Done()
		_ = server.Shutdown(context.Background())
	}()
	go func() {
		_ = server.Serve(ln)
		s.mu.Lock()
		s.running = false
		s.srv = nil
		s.cancel = nil
		s.mu.Unlock()
	}()

	return nil
}

func (s *webDesignSession) stop() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.closeAllClients()
	return nil
}

func (s *webDesignSession) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := webDesignUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client := &webDesignClient{conn: conn}
	s.mu.Lock()
	s.clients[conn] = client
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	if s.logger != nil {
		s.logger.Info(fmt.Sprintf("[web_design] ws connect session=%s remote=%s", s.id, r.RemoteAddr))
	}
	defer func() {
		if s.logger != nil {
			s.logger.Info(fmt.Sprintf("[web_design] ws disconnect session=%s remote=%s", s.id, r.RemoteAddr))
		}
	}()

	_ = s.writeJSON(client, map[string]any{"type": "hello", "session_id": s.id})

	for {
		mt, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
			continue
		}
		payload := strings.TrimSpace(string(raw))
		if payload == "" {
			continue
		}

		if s.logger != nil {
			s.logger.Debug(fmt.Sprintf("[web_design] ws recv session=%s bytes=%d", s.id, len(raw)))
		}
		var js json.RawMessage
		if json.Valid(raw) {
			js = append([]byte(nil), raw...)
		}
		ev := s.appendEvent(payload, js)
		s.broadcastJSON(map[string]any{"type": "event", "event": ev})

		if len(js) > 0 {
			var msg map[string]any
			if err := json.Unmarshal(js, &msg); err == nil {
				if typ, _ := msg["type"].(string); strings.TrimSpace(typ) == "action" {
					actionID, _ := msg["id"].(string)
					actionID = strings.TrimSpace(actionID)
					if actionID != "" {
						result, value, execErr := s.executeBinding(actionID, msg)
						resp := map[string]any{
							"type": "action_result",
							"id":   actionID,
							"ok":   execErr == nil,
						}
						if strings.TrimSpace(result) != "" {
							resp["result"] = result
						}
						if value != nil {
							resp["value"] = value
						}
						if execErr != nil {
							resp["error"] = execErr.Error()
						}
						s.broadcastJSON(resp)
					}
				}
			}
		}
	}
}

func (s *webDesignSession) executeBinding(actionID string, msg map[string]any) (string, any, error) {
	s.mu.Lock()
	b := s.bindings[actionID]
	baseDir := strings.TrimSpace(s.baseDir)
	allowedDir := strings.TrimSpace(s.allowedDir)
	sessionID := s.id
	s.mu.Unlock()

	if strings.TrimSpace(b.Kind) == "" {
		return "", nil, nil
	}
	kind := strings.TrimSpace(b.Kind)
	path := strings.TrimSpace(b.Path)
	content := b.Content

	switch kind {
	case "write_file":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Wrote %s", path), nil, nil
	case "append_file":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", nil, err
		}
		f, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", nil, err
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil {
			return "", nil, werr
		}
		if cerr != nil {
			return "", nil, cerr
		}
		return fmt.Sprintf("Appended %s", path), nil, nil
	case "write_script":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", nil, err
		}
		script := strings.TrimSpace(content)
		if script == "" {
			return "", nil, fmt.Errorf("binding %s: content is required for write_script", actionID)
		}
		if !strings.HasPrefix(script, "#!") {
			script = "#!/usr/bin/env bash\nset -e\n" + script + "\n"
		}
		if err := os.WriteFile(resolved, []byte(script), 0o755); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Wrote script %s", path), nil, nil
	case "read_int":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		v, err := readIntFile(resolved)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Read %s", path), v, nil
	case "add_int":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		v, err := readIntFile(resolved)
		if err != nil {
			return "", nil, err
		}
		v = v + b.Delta
		if err := writeIntFile(resolved, v); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Updated %s", path), v, nil
	case "set_int":
		if path == "" {
			return "", nil, fmt.Errorf("binding %s: path is required", actionID)
		}
		if baseDir == "" {
			return "", nil, fmt.Errorf("binding %s: workspace is not configured", actionID)
		}
		resolved, err := resolvePath(path, baseDir, allowedDir)
		if err != nil {
			return "", nil, err
		}
		vAny := msg["value"]
		if vAny == nil {
			vAny = msg["content"]
		}
		v, err := parseIntAny(vAny)
		if err != nil {
			return "", nil, fmt.Errorf("binding %s: invalid value", actionID)
		}
		if err := writeIntFile(resolved, v); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Set %s", path), v, nil
	case "mqtt_publish":
		broker := strings.TrimSpace(b.Broker)
		topic := strings.TrimSpace(b.Topic)
		if broker == "" || topic == "" {
			return "", nil, fmt.Errorf("binding %s: broker and topic are required for mqtt_publish", actionID)
		}
		clientID := strings.TrimSpace(b.ClientID)
		if clientID == "" {
			clientID = normalizeWebDesignID(fmt.Sprintf("wd_%s_%s", sessionID, actionID))
			if len(clientID) > 64 {
				clientID = clientID[:64]
			}
		}
		qos := b.QOS
		if qos < 0 {
			qos = 0
		}
		if qos > 2 {
			qos = 2
		}

		opts := mqtt.NewClientOptions().
			SetClientID(clientID).
			AddBroker(broker).
			SetCleanSession(true).
			SetAutoReconnect(false).
			SetConnectRetry(false)
		if strings.TrimSpace(b.Username) != "" {
			opts.SetUsername(strings.TrimSpace(b.Username))
		}
		if strings.TrimSpace(b.Password) != "" {
			opts.SetPassword(b.Password)
		}

		client := mqtt.NewClient(opts)
		tok := client.Connect()
		if !tok.WaitTimeout(5 * time.Second) {
			return "", nil, fmt.Errorf("mqtt connect timeout")
		}
		if tok.Error() != nil {
			return "", nil, fmt.Errorf("mqtt connect failed: %w", tok.Error())
		}
		defer client.Disconnect(250)

		pub := client.Publish(topic, byte(qos), b.Retained, fmt.Sprintf("%v", content))
		if !pub.WaitTimeout(5 * time.Second) {
			return "", nil, fmt.Errorf("mqtt publish timeout")
		}
		if pub.Error() != nil {
			return "", nil, fmt.Errorf("mqtt publish failed: %w", pub.Error())
		}
		return fmt.Sprintf("Published to %s", topic), nil, nil
	case "modbus_write_single_register":
		host := strings.TrimSpace(b.Host)
		if host == "" {
			return "", nil, fmt.Errorf("binding %s: host is required for modbus_write_single_register", actionID)
		}
		port := b.Port
		if port == 0 {
			port = 502
		}
		unitID := b.UnitID
		if unitID == 0 {
			unitID = 1
		}
		timeoutMS := b.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 2000
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
		defer cancel()

		client := ModbusTCPClient{
			Host:    host,
			Port:    port,
			UnitID:  byte(unitID),
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		}
		out, err := ModbusWriteSingleRegister(ctx, client, b.Address, b.Value)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Wrote register %d", b.Address), int(out), nil
	case "modbus_write_single_coil", "modbus_wirte_single_coil":
		host := strings.TrimSpace(b.Host)
		if host == "" {
			return "", nil, fmt.Errorf("binding %s: host is required for modbus_write_single_coil", actionID)
		}
		port := b.Port
		if port == 0 {
			port = 502
		}
		unitID := b.UnitID
		if unitID == 0 {
			unitID = 1
		}
		timeoutMS := b.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 2000
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
		defer cancel()

		client := ModbusTCPClient{
			Host:    host,
			Port:    port,
			UnitID:  byte(unitID),
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		}
		on, err := ModbusWriteSingleCoil(ctx, client, b.Address, b.Value != 0)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Wrote coil %d", b.Address), on, nil
	case "modbus_read_single_coil":
		host := strings.TrimSpace(b.Host)
		if host == "" {
			return "", nil, fmt.Errorf("binding %s: host is required for modbus_read_single_coil", actionID)
		}
		port := b.Port
		if port == 0 {
			port = 502
		}
		unitID := b.UnitID
		if unitID == 0 {
			unitID = 1
		}
		timeoutMS := b.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 2000
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
		defer cancel()

		client := ModbusTCPClient{
			Host:    host,
			Port:    port,
			UnitID:  byte(unitID),
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		}
		out, err := ModbusReadCoils(ctx, client, b.Address, 1)
		if err != nil {
			return "", nil, err
		}
		if len(out) == 0 {
			return "", nil, fmt.Errorf("modbus read coils: empty response")
		}
		return fmt.Sprintf("Read coil %d", b.Address), out[0], nil
	default:
		return "", nil, fmt.Errorf("binding %s: unsupported kind %s", actionID, kind)
	}
}

func readIntFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

func writeIntFile(path string, v int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(v)+"\n"), 0o644)
}

func parseIntAny(v any) (int, error) {
	switch x := v.(type) {
	case nil:
		return 0, fmt.Errorf("missing")
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, fmt.Errorf("empty")
		}
		return strconv.Atoi(s)
	default:
		return 0, fmt.Errorf("unsupported")
	}
}

func parseBoolAny(v any) (bool, error) {
	switch x := v.(type) {
	case nil:
		return false, fmt.Errorf("missing")
	case bool:
		return x, nil
	case float64:
		return x != 0, nil
	case int:
		return x != 0, nil
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		switch s {
		case "1", "true", "on", "yes", "y":
			return true, nil
		case "0", "false", "off", "no", "n":
			return false, nil
		default:
			return false, fmt.Errorf("invalid")
		}
	default:
		return false, fmt.Errorf("unsupported")
	}
}

func (s *webDesignSession) appendEvent(raw string, js json.RawMessage) webDesignEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	ev := webDesignEvent{
		Seq:       s.nextSeq,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Raw:       raw,
		JSON:      js,
	}
	if len(s.events) >= 2000 {
		s.events = append(s.events[:0], s.events[len(s.events)-1000:]...)
	}
	s.events = append(s.events, ev)
	return ev
}

func (s *webDesignSession) broadcastJSON(v any) {
	s.mu.Lock()
	clients := make([]*webDesignClient, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	var bad []*webDesignClient
	for _, c := range clients {
		if err := s.writeJSON(c, v); err != nil {
			bad = append(bad, c)
			if s.logger != nil {
				s.logger.Error(fmt.Sprintf("[web_design] ws write failed session=%s err=%v", s.id, err))
			}
		}
	}
	if len(bad) == 0 {
		return
	}

	s.mu.Lock()
	for _, c := range bad {
		delete(s.clients, c.conn)
	}
	s.mu.Unlock()

	for _, c := range bad {
		_ = c.conn.Close()
	}
}

func (s *webDesignSession) writeJSON(c *webDesignClient, v any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("nil websocket client")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(webDesignWriteTimeout))
	err := c.conn.WriteJSON(v)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (s *webDesignSession) closeAllClients() {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.clients))
	for c := range s.clients {
		conns = append(conns, c)
	}
	s.clients = make(map[*websocket.Conn]*webDesignClient)
	s.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *webDesignSession) statusJSON() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"session_id": s.id,
		"dir":        s.dir,
		"host":       s.host,
		"port":       s.port,
		"running":    s.running,
	}
	if s.running && s.port > 0 {
		out["url"] = fmt.Sprintf("http://%s:%d/", s.host, s.port)
		out["ws_url"] = fmt.Sprintf("ws://%s:%d/ws", s.host, s.port)
		out["status_url"] = fmt.Sprintf("http://%s:%d/api/status", s.host, s.port)

		hosts := webDesignAccessHosts(s.host)
		if len(hosts) > 0 {
			out["access_hosts"] = hosts
			urls := make([]string, 0, len(hosts))
			wsURLs := make([]string, 0, len(hosts))
			statusURLs := make([]string, 0, len(hosts))
			for _, h := range hosts {
				urls = append(urls, fmt.Sprintf("http://%s:%d/", h, s.port))
				wsURLs = append(wsURLs, fmt.Sprintf("ws://%s:%d/ws", h, s.port))
				statusURLs = append(statusURLs, fmt.Sprintf("http://%s:%d/api/status", h, s.port))
			}
			out["access_urls"] = urls
			out["access_ws_urls"] = wsURLs
			out["access_status_urls"] = statusURLs
		}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

func webDesignAccessHosts(bindHost string) []string {
	h := strings.TrimSpace(strings.ToLower(bindHost))
	if h == "" {
		return webDesignLANHosts()
	}
	if h == "127.0.0.1" || h == "localhost" {
		return webDesignLoopbackHosts()
	}
	if h == "0.0.0.0" || h == "::" || h == "[::]" {
		return webDesignLANHosts()
	}
	return []string{bindHost}
}

func webDesignLoopbackHosts() []string {
	return []string{"127.0.0.1", "localhost"}
}

func webDesignLANHosts() []string {
	out := webDesignLoopbackHosts()
	out = append(out, webDesignNonLoopbackIPv4()...)
	return dedupeStrings(out)
}

func webDesignNonLoopbackIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}
	sort.Strings(ips)
	return dedupeStrings(ips)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func writeWebDesignExtraFiles(sessionDir string, v any) error {
	if v == nil {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("files must be an object")
	}
	for k, vv := range raw {
		rel := strings.TrimSpace(k)
		if rel == "" {
			continue
		}
		if filepath.IsAbs(rel) {
			return fmt.Errorf("files.%s must be a relative path", rel)
		}
		rel = filepath.Clean(filepath.FromSlash(rel))
		if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return fmt.Errorf("files.%s is invalid", k)
		}
		abs := filepath.Join(sessionDir, rel)

		content := ""
		mode := os.FileMode(0o644)
		switch x := vv.(type) {
		case string:
			content = x
		case map[string]any:
			if s, ok := x["content"].(string); ok {
				content = s
			}
			if mv, ok := x["mode"].(float64); ok && mv > 0 {
				mode = os.FileMode(int(mv)) & 0o777
			}
		default:
			return fmt.Errorf("files.%s must be a string or an object", k)
		}

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(content), mode); err != nil {
			return err
		}
	}
	return nil
}

func writeWebDesignShell(sessionDir string, shellAny any, shellPathAny any) error {
	shell, _ := shellAny.(string)
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return nil
	}
	shellPath, _ := shellPathAny.(string)
	shellPath = strings.TrimSpace(shellPath)
	if shellPath == "" {
		shellPath = "run.sh"
	}
	if filepath.IsAbs(shellPath) {
		return fmt.Errorf("shell_path must be a relative path")
	}
	shellPath = filepath.Clean(filepath.FromSlash(shellPath))
	if shellPath == "." || shellPath == ".." || strings.HasPrefix(shellPath, ".."+string(filepath.Separator)) {
		return fmt.Errorf("shell_path is invalid")
	}
	abs := filepath.Join(sessionDir, shellPath)
	script := shell
	if !strings.HasPrefix(script, "#!") {
		script = "#!/usr/bin/env bash\nset -e\n" + script + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(script), 0o755)
}

func parseWebDesignBindings(v any) (map[string]webDesignBinding, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("bindings must be an object")
	}
	if len(raw) == 0 {
		return map[string]webDesignBinding{}, nil
	}
	out := make(map[string]webDesignBinding, len(raw))
	for k, vv := range raw {
		actionID := strings.TrimSpace(k)
		if actionID == "" {
			continue
		}
		m, ok := vv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("bindings.%s must be an object", actionID)
		}
		kind, _ := m["kind"].(string)
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		delta := 0
		if dv, ok := m["delta"].(float64); ok {
			delta = int(dv)
		}
		kind = strings.TrimSpace(kind)
		path = strings.TrimSpace(path)
		if kind == "" {
			return nil, fmt.Errorf("bindings.%s.kind is required", actionID)
		}

		b := webDesignBinding{Kind: kind, Path: path, Content: content, Delta: delta}
		switch kind {
		case "write_file", "append_file", "read_int", "set_int":
			if b.Path == "" {
				return nil, fmt.Errorf("bindings.%s.path is required", actionID)
			}
		case "write_script":
			if b.Path == "" {
				return nil, fmt.Errorf("bindings.%s.path is required", actionID)
			}
			if strings.TrimSpace(b.Content) == "" {
				return nil, fmt.Errorf("bindings.%s.content is required for write_script", actionID)
			}
		case "add_int":
			if b.Path == "" {
				return nil, fmt.Errorf("bindings.%s.path is required", actionID)
			}
			if b.Delta == 0 {
				return nil, fmt.Errorf("bindings.%s.delta is required for add_int", actionID)
			}
		case "mqtt_publish":
			b.Broker, _ = m["broker"].(string)
			b.ClientID, _ = m["client_id"].(string)
			b.Username, _ = m["username"].(string)
			b.Password, _ = m["password"].(string)
			b.Topic, _ = m["topic"].(string)
			if p, ok := m["payload"].(string); ok && strings.TrimSpace(b.Content) == "" {
				b.Content = p
			}
			if qv, ok := m["qos"].(float64); ok {
				b.QOS = int(qv)
			}
			if rv, ok := m["retained"].(bool); ok {
				b.Retained = rv
			}
			b.Broker = strings.TrimSpace(b.Broker)
			b.ClientID = strings.TrimSpace(b.ClientID)
			b.Username = strings.TrimSpace(b.Username)
			b.Topic = strings.TrimSpace(b.Topic)
			if b.Broker == "" {
				return nil, fmt.Errorf("bindings.%s.broker is required for mqtt_publish", actionID)
			}
			if b.Topic == "" {
				return nil, fmt.Errorf("bindings.%s.topic is required for mqtt_publish", actionID)
			}
		case "modbus_write_single_register":
			b.Host, _ = m["host"].(string)
			b.Host = strings.TrimSpace(b.Host)
			if b.Host == "" {
				return nil, fmt.Errorf("bindings.%s.host is required for modbus_write_single_register", actionID)
			}
			if pv, ok := m["port"].(float64); ok && pv > 0 {
				b.Port = int(pv)
			}
			if uv, ok := m["unit_id"].(float64); ok && uv >= 0 {
				b.UnitID = int(uv)
			}
			if tv, ok := m["timeout_ms"].(float64); ok && tv > 0 {
				b.TimeoutMS = int(tv)
			}
			if av, ok := m["address"].(float64); ok {
				b.Address = int(av)
			} else {
				return nil, fmt.Errorf("bindings.%s.address is required for modbus_write_single_register", actionID)
			}
			if vv, ok := m["value"].(float64); ok {
				b.Value = int(vv)
			} else {
				return nil, fmt.Errorf("bindings.%s.value is required for modbus_write_single_register", actionID)
			}
		case "modbus_write_single_coil":
			b.Host, _ = m["host"].(string)
			b.Host = strings.TrimSpace(b.Host)
			if b.Host == "" {
				return nil, fmt.Errorf("bindings.%s.host is required for modbus_write_single_coil", actionID)
			}
			if pv, ok := m["port"].(float64); ok && pv > 0 {
				b.Port = int(pv)
			}
			if uv, ok := m["unit_id"].(float64); ok && uv >= 0 {
				b.UnitID = int(uv)
			}
			if tv, ok := m["timeout_ms"].(float64); ok && tv > 0 {
				b.TimeoutMS = int(tv)
			}
			if av, ok := m["address"].(float64); ok {
				b.Address = int(av)
			} else {
				return nil, fmt.Errorf("bindings.%s.address is required for modbus_write_single_coil", actionID)
			}
			on, err := parseBoolAny(m["value"])
			if err != nil {
				return nil, fmt.Errorf("bindings.%s.value is required for modbus_write_single_coil", actionID)
			}
			if on {
				b.Value = 1
			} else {
				b.Value = 0
			}
		case "modbus_wirte_single_coil":
			b.Kind = "modbus_write_single_coil"
			b.Host, _ = m["host"].(string)
			b.Host = strings.TrimSpace(b.Host)
			if b.Host == "" {
				return nil, fmt.Errorf("bindings.%s.host is required for modbus_write_single_coil", actionID)
			}
			if pv, ok := m["port"].(float64); ok && pv > 0 {
				b.Port = int(pv)
			}
			if uv, ok := m["unit_id"].(float64); ok && uv >= 0 {
				b.UnitID = int(uv)
			}
			if tv, ok := m["timeout_ms"].(float64); ok && tv > 0 {
				b.TimeoutMS = int(tv)
			}
			if av, ok := m["address"].(float64); ok {
				b.Address = int(av)
			} else {
				return nil, fmt.Errorf("bindings.%s.address is required for modbus_write_single_coil", actionID)
			}
			on, err := parseBoolAny(m["value"])
			if err != nil {
				return nil, fmt.Errorf("bindings.%s.value is required for modbus_write_single_coil", actionID)
			}
			if on {
				b.Value = 1
			} else {
				b.Value = 0
			}
		case "modbus_read_single_coil":
			b.Host, _ = m["host"].(string)
			b.Host = strings.TrimSpace(b.Host)
			if b.Host == "" {
				return nil, fmt.Errorf("bindings.%s.host is required for modbus_read_single_coil", actionID)
			}
			if pv, ok := m["port"].(float64); ok && pv > 0 {
				b.Port = int(pv)
			}
			if uv, ok := m["unit_id"].(float64); ok && uv >= 0 {
				b.UnitID = int(uv)
			}
			if tv, ok := m["timeout_ms"].(float64); ok && tv > 0 {
				b.TimeoutMS = int(tv)
			}
			if av, ok := m["address"].(float64); ok {
				b.Address = int(av)
			} else {
				return nil, fmt.Errorf("bindings.%s.address is required for modbus_read_single_coil", actionID)
			}
		default:
			return nil, fmt.Errorf("bindings.%s.kind must be one of write_file, append_file, write_script, read_int, set_int, add_int, mqtt_publish, modbus_write_single_register, modbus_write_single_coil, modbus_read_single_coil", actionID)
		}
		out[actionID] = b
	}
	return out, nil
}

func normalizeWebDesignID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9_\-]`)
	id = re.ReplaceAllString(id, "_")
	return strings.TrimSpace(id)
}

func parsePort(s string) (int, error) {
	var p int
	_, err := fmt.Sscanf(s, "%d", &p)
	if err != nil {
		return 0, err
	}
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return p, nil
}

func buildWebDesignHTML(sessionID, title, requirements string, ui any) (string, error) {
	type embedded struct {
		SessionID     string `json:"session_id"`
		Title         string `json:"title"`
		Requirements  string `json:"requirements,omitempty"`
		Controls      any    `json:"controls,omitempty"`
		Counter       any    `json:"counter,omitempty"`
		Debug         bool   `json:"debug,omitempty"`
		RawUISpec     any    `json:"ui,omitempty"`
		GeneratedHint string `json:"generated_hint,omitempty"`
	}

	cfg := embedded{
		SessionID:    sessionID,
		Title:        title,
		Requirements: strings.TrimSpace(requirements),
		RawUISpec:    ui,
	}

	if m, ok := ui.(map[string]any); ok {
		if v, ok := m["controls"]; ok {
			cfg.Controls = v
		}
		if v, ok := m["counter"]; ok {
			cfg.Counter = v
		}
		if v, ok := m["debug"].(bool); ok {
			cfg.Debug = v
		}
	}
	if cfg.Controls == nil {
		cfg.GeneratedHint = "No controls were generated. Provide ui.controls or html."
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	escapedTitle := htmlEscape(title)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>%s</title>
  <style>
    body{font-family:system-ui,-apple-system,Segoe UI,Roboto,Arial,sans-serif;margin:16px;max-width:980px}
    .row{display:flex;gap:12px;flex-wrap:wrap;align-items:flex-end}
    .card{border:1px solid #e5e7eb;border-radius:12px;padding:12px}
    label{display:block;font-size:12px;color:#374151;margin-bottom:6px}
    input[type="text"]{width:320px;max-width:100%%;padding:8px;border:1px solid #d1d5db;border-radius:10px}
    button{padding:8px 12px;border-radius:10px;border:1px solid #d1d5db;background:#111827;color:#fff;cursor:pointer}
    button.secondary{background:#fff;color:#111827}
    button.lg{padding:18px 22px;font-size:18px;border-radius:14px;min-width:220px}
    .value{font-size:30px;font-weight:700;letter-spacing:0.5px}
    pre{background:#0b1020;color:#d1e7ff;padding:12px;border-radius:12px;overflow:auto;min-height:180px}
    .muted{color:#6b7280;font-size:12px}
  </style>
</head>
<body>
  <h1>%s</h1>
  <div class="muted" id="status">Connecting...</div>
  <div class="row">
    <div class="card" style="flex:1;min-width:320px">
      <div id="counterCard" class="card" style="margin-bottom:12px;display:none">
        <label>Current</label>
        <div id="currentValue" class="value">-</div>
        <div id="currentMeta" class="muted" style="margin-top:8px"></div>
      </div>
      <div id="controls" class="row"></div>
      <div class="muted" id="hint" style="margin-top:10px"></div>
    </div>
    <div class="card" id="logCard" style="flex:1;min-width:320px;display:none">
      <label>Log</label>
      <pre id="log"></pre>
    </div>
  </div>

  <script>
  const cfg = %s;
  const logEl = document.getElementById('log');
  const statusEl = document.getElementById('status');
  const hintEl = document.getElementById('hint');
  const logCardEl = document.getElementById('logCard');
  const counterCardEl = document.getElementById('counterCard');
  const currentValueEl = document.getElementById('currentValue');
  const currentMetaEl = document.getElementById('currentMeta');
  if (cfg.generated_hint) hintEl.textContent = cfg.generated_hint;
  if (cfg.debug && logCardEl) logCardEl.style.display = 'block';

  function normalizeLogText(s) {
    if (s === null || s === undefined) return '';
    const str = String(s);
    return str.replaceAll('\\r\\n', '\n').replaceAll('\\n', '\n');
  }

  function appendLog(line) {
    logEl.textContent += normalizeLogText(line) + "\\n";
    logEl.scrollTop = logEl.scrollHeight;
  }

  function setCurrentValue(v) {
    if (!currentValueEl) return;
    currentValueEl.textContent = String(v);
  }

  function interpolatePayload(payload, value) {
    if (!payload) return null;
    const cloned = JSON.parse(JSON.stringify(payload));
    const walk = (obj) => {
      if (obj === null || obj === undefined) return obj;
      if (typeof obj === 'string') return obj === '$value' ? String(value ?? '') : obj;
      if (Array.isArray(obj)) return obj.map(walk);
      if (typeof obj === 'object') {
        for (const k of Object.keys(obj)) obj[k] = walk(obj[k]);
        return obj;
      }
      return obj;
    };
    return walk(cloned);
  }

  function send(ws, payload) {
    try {
      ws.send(JSON.stringify(payload));
    } catch (e) {
      appendLog('send error: ' + e);
    }
  }

  function renderControls(ws) {
    const root = document.getElementById('controls');
    root.innerHTML = '';
    const controls = cfg.controls || [];
    for (const c of controls) {
      const wrap = document.createElement('div');
      wrap.style.display = 'flex';
      wrap.style.flexDirection = 'column';
      wrap.style.gap = '6px';

      const lab = document.createElement('label');
      lab.textContent = c.label || c.id || c.type || 'control';
      wrap.appendChild(lab);

      if (c.type === 'button') {
        const btn = document.createElement('button');
        btn.textContent = c.label || c.id || 'Button';
        const classes = [];
        if (c.variant === 'secondary') classes.push('secondary');
        if (c.size === 'lg' || c.large === true) classes.push('lg');
        if (classes.length) btn.className = classes.join(' ');
        btn.onclick = () => {
          if (c.client_action === 'toggle_log') {
            if (logCardEl) {
              logCardEl.style.display = logCardEl.style.display === 'none' ? 'block' : 'none';
            }
            return;
          }
          if (c.client_action === 'clear_log') {
            if (logEl) logEl.textContent = '';
            return;
          }
          const payload = c.payload || {type:'action', id:c.id || 'button'};
          send(ws, interpolatePayload(payload, true));
        };
        wrap.appendChild(btn);
      } else if (c.type === 'text') {
        const input = document.createElement('input');
        input.type = 'text';
        input.placeholder = c.placeholder || '';
        const btn = document.createElement('button');
        btn.textContent = 'Send';
        btn.className = 'secondary';
        const doSend = () => {
          if (c.send_raw === true) {
            try { ws.send(String(input.value ?? '')); } catch (e) { appendLog('send error: ' + e); }
            return;
          }
          const payload = c.payload || {type:'action', id:c.id || 'input', value:'$value'};
          send(ws, interpolatePayload(payload, input.value));
        };
        btn.onclick = doSend;
        input.addEventListener('keydown', (ev) => {
          if (ev.key === 'Enter') {
            ev.preventDefault();
            doSend();
          }
        });
        const row = document.createElement('div');
        row.className = 'row';
        row.appendChild(input);
        row.appendChild(btn);
        wrap.appendChild(row);
      } else {
        const btn = document.createElement('button');
        btn.textContent = (c.label || c.id || 'Send') + ' (generic)';
        btn.className = 'secondary';
        btn.onclick = () => {
          const payload = c.payload || {type:'action', id:c.id || 'generic', value:true};
          send(ws, interpolatePayload(payload, true));
        };
        wrap.appendChild(btn);
      }

      root.appendChild(wrap);
    }
  }

  const wsProto = location.protocol === 'https:' ? 'wss://' : 'ws://';
  const ws = new WebSocket(wsProto + location.host + '/ws');
  ws.onopen = () => {
    statusEl.textContent = 'Connected. session_id=' + (cfg.session_id || '');
    renderControls(ws);
    if (cfg.counter && cfg.counter.readActionId) {
      if (counterCardEl) counterCardEl.style.display = 'block';
      if (currentMetaEl && cfg.counter.path) currentMetaEl.textContent = String(cfg.counter.path);
      send(ws, {type:'action', id: String(cfg.counter.readActionId)});
    }
  };
  ws.onclose = () => statusEl.textContent = 'Disconnected';
  ws.onerror = () => statusEl.textContent = 'WebSocket error';
  ws.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data);
      if (msg.type === 'event') {
        appendLog('event #' + msg.event.seq + ': ' + msg.event.raw);
      } else if (msg.type === 'hello') {
        appendLog('hello: ' + JSON.stringify(msg));
      } else if (msg.type === 'reload') {
        appendLog('reload requested');
        setTimeout(() => location.reload(), 150);
      } else if (msg.type === 'broadcast') {
        appendLog('broadcast: ' + msg.message);
      } else if (msg.type === 'action_result') {
        if (msg.ok) {
          appendLog('action ' + msg.id + ': ok' + (msg.result ? (': ' + msg.result) : ''));
        } else {
          appendLog('action ' + msg.id + ': error' + (msg.error ? (': ' + msg.error) : ''));
        }
        if (msg.value !== undefined) setCurrentValue(msg.value);
      } else {
        appendLog('message: ' + e.data);
      }
    } catch {
      appendLog(String(e.data));
    }
  };
  </script>
</body>
</html>`, escapedTitle, escapedTitle, string(b)), nil
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
