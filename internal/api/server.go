package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/manager"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // Allow all origins for the panel
}

// Server sets up and runs the Daemon HTTP REST API.
type Server struct {
	cfg  *config.DaemonConfig
	node *manager.NodeManager
}

func NewServer(cfg *config.DaemonConfig, node *manager.NodeManager) *Server {
	return &Server{
		cfg:  cfg,
		node: node,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Server-specific management
	mux.HandleFunc("/api/servers/create", s.withAuth(s.handleCreateServer))
	mux.HandleFunc("/api/servers/start", s.withAuth(s.handleServerAction("start")))
	mux.HandleFunc("/api/servers/stop", s.withAuth(s.handleServerAction("stop")))
	mux.HandleFunc("/api/servers/restart", s.withAuth(s.handleServerAction("restart")))
	mux.HandleFunc("/api/servers/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/servers/logs", s.withAuth(s.handleServerLogs))
	mux.HandleFunc("/api/servers/console", s.withAuth(s.handleServerConsole))
	mux.HandleFunc("/api/servers/delete", s.withAuth(s.handleDeleteServer))
	mux.HandleFunc("/api/servers/install", s.withAuth(s.handleInstallServer))

	// Node-level management
	mux.HandleFunc("/api/node/sync-routes", s.withAuth(s.handleSyncRoutes))
	mux.HandleFunc("/api/node/master/action", s.withAuth(s.handleMasterAction))
	mux.HandleFunc("/api/node/master/status", s.withAuth(s.handleMasterStatus))

	// Wrap mux with CORS middleware
	handler := s.withCORS(mux)

	log.Printf("[api] Daemon API listening securely on %s", s.cfg.APIListen)
	return http.ListenAndServe(s.cfg.APIListen, handler)
}

// Payload expected for CreateServer from the Panel
type CreateServerRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"server_type"` // "proxy" or "game"
	Port     int    `json:"port"`
	RAM      int    `json:"ram_mb"`
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Config   string `json:"config_json"` // Raw stringified internal JSON config for the Hytale-Proxy
}

func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Port <= 0 {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	workDir := filepath.Join("data", "servers", req.ID)
	_ = os.MkdirAll(workDir, 0755)

	// Since we are not strictly copying the binary right now (assuming it's in global proxy_binary path),
	// we just write the proxy configurations cleanly.
	configPath := filepath.Join(workDir, "config.json")
	if err := os.WriteFile(configPath, []byte(req.Config), 0644); err != nil {
		http.Error(w, "failed to write config", http.StatusInternalServerError)
		return
	}

	if req.Type == "" {
		req.Type = "proxy"
	}

	// Instantiate manager
	srv := manager.NewServerManager(s.node.Config(), req.ID, req.Name, req.Port, req.RAM, s.node.IP(), req.Version, req.Type, req.Hostname)
	s.node.AddServer(req.ID, srv)

	// Start it up via goroutine
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[api] Failed to start newly created server %s: %v", req.ID, err)
		}
	}()

	s.json(w, map[string]string{"message": "creation dispatched successfully"})
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	serverID := r.URL.Query().Get("id")
	if serverID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	srv, ok := s.node.GetServer(serverID)
	if ok {
		_ = srv.Stop()
		s.node.RemoveServer(serverID)
	}

	workDir := filepath.Join("data", "servers", serverID)
	_ = os.RemoveAll(workDir)

	s.json(w, map[string]string{"message": "server resources purged"})
}

func (s *Server) handleServerAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		serverID := r.URL.Query().Get("id")
		srv, ok := s.node.GetServer(serverID)
		if !ok {
			http.Error(w, "server not found", http.StatusNotFound)
			return
		}

		var err error
		switch action {
		case "start":
			err = srv.Start()
		case "stop":
			err = srv.Stop()
		case "restart":
			err = srv.Restart()
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.json(w, map[string]string{"message": "action successful"})
	}
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	serverID := r.URL.Query().Get("id")
	srv, ok := s.node.GetServer(serverID)
	if !ok {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	s.json(w, srv.Status())
}

func (s *Server) handleInstallServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	serverID := r.URL.Query().Get("id")
	if serverID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	if err := s.node.Installer.InstallGameServer(r.Context(), serverID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.json(w, map[string]string{"message": "installation started"})
}

func (s *Server) handleSyncRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.node.SyncMasterRoutes(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.json(w, map[string]string{"message": "master proxy routes synchronized"})
}

func (s *Server) handleMasterAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	action := r.URL.Query().Get("action")
	if s.node.MasterProxy == nil {
		http.Error(w, "master proxy not initialized", http.StatusInternalServerError)
		return
	}

	var err error
	switch action {
	case "start":
		err = s.node.MasterProxy.Start()
	case "stop":
		err = s.node.MasterProxy.Stop()
	case "restart":
		err = s.node.SyncMasterRoutes(r.Context())
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	if err != nil {
		// Handle idempotency: if already in desired state, don't 500.
		msg := err.Error()
		if msg == "server is already running" || msg == "server is not running" {
			s.json(w, map[string]string{"message": msg, "status": "no-op"})
			return
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	s.json(w, map[string]string{"message": "master proxy action successful"})
}

func (s *Server) handleMasterStatus(w http.ResponseWriter, r *http.Request) {
	if s.node.MasterProxy == nil {
		http.Error(w, "master proxy not initialized", http.StatusInternalServerError)
		return
	}
	s.json(w, s.node.MasterProxy.Status())
}

func (s *Server) handleServerLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	serverID := r.URL.Query().Get("id")
	srv, ok := s.node.GetServer(serverID)
	if !ok {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, srv.GetLogs())
}

func (s *Server) handleServerConsole(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get("id")
	srv, ok := s.node.GetServer(serverID)
	if !ok {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[api] WebSocket upgrade failed for %s: %v", serverID, err)
		return
	}
	defer conn.Close()

	log.Printf("[api] Console WebSocket connected for %s", serverID)

	// Stream existing logs first
	if err := conn.WriteMessage(websocket.TextMessage, []byte(srv.GetLogs())); err != nil {
		return
	}

	// Simple polling of the ring buffer for new content (real implementation would use a pub/sub or watcher)
	lastLogs := srv.GetLogs()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			currentLogs := srv.GetLogs()
			if len(currentLogs) > len(lastLogs) {
				newContent := currentLogs[len(lastLogs):]
				if err := conn.WriteMessage(websocket.TextMessage, []byte(newContent)); err != nil {
					return
				}
				lastLogs = currentLogs
			} else if len(currentLogs) < len(lastLogs) {
				// Buffer wrapped or cleared
				lastLogs = currentLogs
			}
		}
	}
}

// json is a small helper to serialize objects.
func (s *Server) json(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		token := ""
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				token = parts[1]
			}
		} else {
			// Fallback to query param for WebSockets
			token = r.URL.Query().Get("token")
		}

		if token == "" {
			http.Error(w, "unauthorized: missing token", http.StatusUnauthorized)
			return
		}

		if token != s.cfg.APIToken {
			http.Error(w, "unauthorized: invalid token", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
