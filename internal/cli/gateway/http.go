package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"luckclaw/internal/bus"
	"luckclaw/internal/channels"
	"luckclaw/internal/config"
	"luckclaw/internal/paths"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func serveGatewayHTTP(ctx context.Context, port int, hub *channels.WebUIHub, messageBus *bus.MessageBus, cfg *config.Config) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/chat/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[gateway] websocket upgrade: %v", err)
			return
		}
		defer conn.Close()

		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			sessionID = "webui_default"
		}
		hub.Register(sessionID, conn)
		defer hub.Unregister(sessionID, conn)

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var req struct {
				Type      string `json:"type"`
				Content   string `json:"content"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			if req.SessionID != "" {
				hub.Unregister(sessionID, conn)
				sessionID = req.SessionID
				hub.Register(sessionID, conn)
			}
			if req.Type == "message" && req.Content != "" {
				_ = messageBus.PublishInbound(ctx, bus.InboundMessage{
					Channel:  "webui",
					ChatID:   sessionID,
					Content:  req.Content,
					Metadata: map[string]any{"session_key_override": "webui:" + sessionID},
				})
			}
		}
	})

	workspace, _ := paths.ExpandUser(cfg.Agents.Defaults.Workspace)
	mux.HandleFunc("/api/workspace/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if workspace == "" {
			http.Error(w, "Workspace not configured", http.StatusInternalServerError)
			return
		}
		rel := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
		if rel == "" || strings.Contains(rel, "..") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}
		abs := filepath.Join(workspace, filepath.FromSlash(rel))
		abs = filepath.Clean(abs)
		wsClean := filepath.Clean(workspace)
		if relPath, err := filepath.Rel(wsClean, abs); err != nil || strings.HasPrefix(relPath, "..") {
			http.Error(w, "Path outside workspace", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeFile(w, r, abs)
	})

	addr := fmt.Sprintf(":%d", port)

	srv := &http.Server{Addr: addr, Handler: mux}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Printf("[gateway] HTTP listening on %s (WebUI: /api/chat/ws)", addr)
	err := srv.ListenAndServe()
	wg.Wait()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
