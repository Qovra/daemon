package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/manager"
)

// Server sets up and runs the Daemon HTTP REST API.
type Server struct {
	cfg     *config.DaemonConfig
	manager *manager.Manager
}

func NewServer(cfg *config.DaemonConfig, mgr *manager.Manager) *Server {
	return &Server{
		cfg:     cfg,
		manager: mgr,
	}
}

// Start opens the HTTP port securely exposing the Manager API.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Protected routes
	mux.HandleFunc("/api/start", s.withAuth(s.handleStart))
	mux.HandleFunc("/api/stop", s.withAuth(s.handleStop))
	mux.HandleFunc("/api/restart", s.withAuth(s.handleRestart))
	mux.HandleFunc("/api/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("/api/logs", s.withAuth(s.handleLogs))

	// Wrap mux with CORS middleware
	handler := s.withCORS(mux)

	log.Printf("[api] Daemon API listening securely on %s", s.cfg.APIListen)
	return http.ListenAndServe(s.cfg.APIListen, handler)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.manager.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.json(w, map[string]string{"message": "proxy started successfully"})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.manager.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.json(w, map[string]string{"message": "proxy stopped successfully"})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.manager.Restart(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.json(w, map[string]string{"message": "proxy restarted successfully"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	status := s.manager.Status()
	s.json(w, status)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	logs := s.manager.GetLogs()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(logs))
}

// json is a small helper to serialize objects.
func (s *Server) json(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

// withAuth is a middleware enforcing Bearer token validation.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// handle preflight
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "unauthorized: invalid auth format", http.StatusUnauthorized)
			return
		}

		if parts[1] != s.cfg.APIToken {
			http.Error(w, "unauthorized: invalid token", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

// withCORS is a middleware appending broadly-accepting CORS headers so React panels can hit the daemon from browsers.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We allow origin `*` natively for testing.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Authorization")

		// Let browser preflight checks pass quickly
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
