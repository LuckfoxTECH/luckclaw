package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	"luckclaw/internal/paths"

	"github.com/gorilla/websocket"
)

const TypeWebDesign = "web_design"

// Ensure WebDesignSession implements Service interface.
var _ Service = (*WebDesignSession)(nil)

var webDesignUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const webDesignWriteTimeout = 2 * time.Second

type WebDesignSession struct {
	ID        string `json:"session_id"`
	Title     string `json:"title"`
	Dir       string `json:"dir"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	AutoStart bool   `json:"auto_start"`

	mu      sync.Mutex
	running bool
	srv     *http.Server
	cancel  context.CancelFunc

	clients  map[*websocket.Conn]*webDesignClient
	bindings map[string]WebDesignBinding
	nextSeq  int64
	events   []WebDesignEvent
}

type webDesignClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type WebDesignBinding struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delta   int    `json:"delta,omitempty"`
}

type WebDesignEvent struct {
	Seq       int64           `json:"seq"`
	Timestamp string          `json:"timestamp"`
	Raw       string          `json:"raw"`
	JSON      json.RawMessage `json:"json,omitempty"`
}

func NewWebDesignSession(id, title, dir, host string, port int, autoStart bool) *WebDesignSession {
	if id == "" {
		id = "wd_" + generateServiceID()
	}
	if title == "" {
		title = "Web Design"
	}
	if host == "" {
		host = "0.0.0.0"
	}
	return &WebDesignSession{
		ID:        id,
		Title:     title,
		Dir:       dir,
		Host:      host,
		Port:      port,
		AutoStart: autoStart,
		clients:   make(map[*websocket.Conn]*webDesignClient),
		bindings:  make(map[string]WebDesignBinding),
	}
}

func (s *WebDesignSession) ServiceType() string {
	return TypeWebDesign
}

func (s *WebDesignSession) ServiceID() string {
	return s.ID
}

func (s *WebDesignSession) ServiceInfo() *ServiceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ServiceInfo{
		ID:        s.ID,
		Type:      TypeWebDesign,
		Name:      s.Title,
		Dir:       s.Dir,
		Host:      s.Host,
		Port:      s.Port,
		Running:   s.running,
		AutoStart: s.AutoStart,
		Metadata: map[string]any{
			"session_id": s.ID,
		},
	}
}

func (s *WebDesignSession) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	host := strings.TrimSpace(s.Host)
	if host == "" {
		host = "0.0.0.0"
		s.Host = host
	}
	port := s.Port
	s.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_, actualPortStr, _ := net.SplitHostPort(ln.Addr().String())
	actualPort := port
	if actualPortStr != "" {
		if p, err := parseServicePort(actualPortStr); err == nil {
			actualPort = p
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(s.Dir)))
	mux.HandleFunc("/ws", s.wsHandler)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.statusJSON()))
	})

	server := &http.Server{Handler: mux}
	// Use Background context so service survives after Start() returns
	runCtx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.Port = actualPort
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

func (s *WebDesignSession) Stop() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.closeAllClients()
	return nil
}

func (s *WebDesignSession) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *WebDesignSession) SetBindings(bindings map[string]WebDesignBinding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings = bindings
}

func (s *WebDesignSession) wsHandler(w http.ResponseWriter, r *http.Request) {
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

	_ = s.writeJSON(client, map[string]any{"type": "hello", "session_id": s.ID})

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

func (s *WebDesignSession) executeBinding(actionID string, msg map[string]any) (string, any, error) {
	s.mu.Lock()
	b := s.bindings[actionID]
	s.mu.Unlock()

	if strings.TrimSpace(b.Kind) == "" {
		return "", nil, nil
	}
	kind := strings.TrimSpace(b.Kind)
	path := strings.TrimSpace(b.Path)
	content := b.Content

	if path == "" {
		return "", nil, fmt.Errorf("binding %s: path is required", actionID)
	}

	resolved := path
	if !filepath.IsAbs(path) {
		resolved = filepath.Join(s.Dir, path)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", nil, err
	}

	switch kind {
	case "write_file":
		if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Wrote %s", path), nil, nil
	case "append_file":
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
		v, err := readServiceIntFile(resolved)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Read %s", path), v, nil
	case "add_int":
		v, err := readServiceIntFile(resolved)
		if err != nil {
			return "", nil, err
		}
		v = v + b.Delta
		if err := writeServiceIntFile(resolved, v); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Updated %s", path), v, nil
	case "set_int":
		vAny := msg["value"]
		if vAny == nil {
			vAny = msg["content"]
		}
		v, err := parseServiceIntAny(vAny)
		if err != nil {
			return "", nil, fmt.Errorf("binding %s: invalid value", actionID)
		}
		if err := writeServiceIntFile(resolved, v); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Set %s", path), v, nil
	default:
		return "", nil, fmt.Errorf("binding %s: unsupported kind %s", actionID, kind)
	}
}

func readServiceIntFile(path string) (int, error) {
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

func writeServiceIntFile(path string, v int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(v)+"\n"), 0o644)
}

func parseServiceIntAny(v any) (int, error) {
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

func (s *WebDesignSession) appendEvent(raw string, js json.RawMessage) WebDesignEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	ev := WebDesignEvent{
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

func (s *WebDesignSession) broadcastJSON(v any) {
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

func (s *WebDesignSession) writeJSON(c *webDesignClient, v any) error {
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

func (s *WebDesignSession) closeAllClients() {
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

func (s *WebDesignSession) statusJSON() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"session_id": s.ID,
		"type":       TypeWebDesign,
		"title":      s.Title,
		"dir":        s.Dir,
		"host":       s.Host,
		"port":       s.Port,
		"running":    s.running,
	}
	if s.running && s.Port > 0 {
		out["url"] = fmt.Sprintf("http://%s:%d/", s.Host, s.Port)
		out["ws_url"] = fmt.Sprintf("ws://%s:%d/ws", s.Host, s.Port)
		out["status_url"] = fmt.Sprintf("http://%s:%d/api/status", s.Host, s.Port)

		hosts := webDesignAccessHosts(s.Host)
		if len(hosts) > 0 {
			out["access_hosts"] = hosts
			urls := make([]string, 0, len(hosts))
			wsURLs := make([]string, 0, len(hosts))
			for _, h := range hosts {
				urls = append(urls, fmt.Sprintf("http://%s:%d/", h, s.Port))
				wsURLs = append(wsURLs, fmt.Sprintf("ws://%s:%d/ws", h, s.Port))
			}
			out["access_urls"] = urls
			out["access_ws_urls"] = wsURLs
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

func parseServicePort(s string) (int, error) {
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

func normalizeServiceID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9_\-]`)
	id = re.ReplaceAllString(id, "_")
	return strings.TrimSpace(id)
}

func generateServiceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type WebDesignManager struct {
	mu       sync.RWMutex
	sessions map[string]*WebDesignSession
}

var webDesignManager *WebDesignManager
var webDesignManagerOnce sync.Once

func WebDesignManagerGlobal() *WebDesignManager {
	webDesignManagerOnce.Do(func() {
		webDesignManager = &WebDesignManager{
			sessions: make(map[string]*WebDesignSession),
		}
	})
	return webDesignManager
}

func (m *WebDesignManager) Create(id, title, dir, host string, port int, autoStart bool, bindings map[string]WebDesignBinding) (*WebDesignSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := normalizeServiceID(id)
	if sessionID == "" {
		sessionID = "wd_" + generateServiceID()
	}

	if dir == "" {
		ws, _ := paths.WorkspaceDir()
		if ws != "" {
			dir = filepath.Join(ws, ".web_design", sessionID)
		} else {
			dir = filepath.Join(os.TempDir(), "luckclaw_web_design", sessionID)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	session := NewWebDesignSession(sessionID, title, dir, host, port, autoStart)
	if bindings != nil {
		session.SetBindings(bindings)
	}

	if old, ok := m.sessions[sessionID]; ok {
		_ = old.Stop()
	}
	m.sessions[sessionID] = session

	GlobalRegistry().Register(session.ServiceInfo())

	if autoStart {
		if err := session.Start(context.Background()); err != nil {
			return nil, err
		}
	}

	return session, nil
}

func (m *WebDesignManager) Get(id string) (*WebDesignSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *WebDesignManager) List() []*WebDesignSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*WebDesignSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

func (m *WebDesignManager) Start(id string) error {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := session.Start(context.Background()); err != nil {
		return err
	}
	GlobalRegistry().Update(id, func(info *ServiceInfo) {
		info.Running = true
		info.Port = session.Port
	})
	return nil
}

func (m *WebDesignManager) Stop(id string) error {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := session.Stop(); err != nil {
		return err
	}
	GlobalRegistry().Update(id, func(info *ServiceInfo) {
		info.Running = false
	})
	return nil
}

func (m *WebDesignManager) Delete(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		_ = session.Stop()
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	GlobalRegistry().Unregister(id)
	return nil
}

func (m *WebDesignManager) UpdateBindings(id string, bindings map[string]WebDesignBinding) error {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	session.SetBindings(bindings)
	session.broadcastJSON(map[string]any{"type": "reload"})
	return nil
}

// WebDesignFactory creates a WebDesignSession from ServiceInfo.
func WebDesignFactory(info ServiceInfo) (Service, error) {
	session := &WebDesignSession{
		ID:        info.ID,
		Title:     info.Name,
		Dir:       info.Dir,
		Host:      info.Host,
		Port:      info.Port,
		AutoStart: info.AutoStart,
		clients:   make(map[*websocket.Conn]*webDesignClient),
		bindings:  make(map[string]WebDesignBinding),
	}
	if info.Metadata != nil {
		if bRaw, ok := info.Metadata["bindings"]; ok {
			bBytes, err := json.Marshal(bRaw)
			if err == nil {
				var bindings map[string]WebDesignBinding
				if err := json.Unmarshal(bBytes, &bindings); err == nil {
					session.bindings = bindings
				}
			}
		}
	}
	WebDesignManagerGlobal().RegisterSession(session)
	return session, nil
}

// RegisterSession adds a session to the manager (used by factory).
func (m *WebDesignManager) RegisterSession(session *WebDesignSession) {
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
}

func init() {
	GlobalTypeRegistry().Register(TypeWebDesign, WebDesignFactory)
}
