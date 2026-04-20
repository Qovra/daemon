package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/database"
)

// NodeManager is the core orchestrator representing this physical machine.
// It manages all server sub-processes running inside this Node.
type NodeManager struct {
	cfg     *config.DaemonConfig
	NodeID  string
	NodeIP  string
	
	mu      sync.RWMutex
	servers map[string]*ServerManager

	MasterProxy *ServerManager
	Installer   *Installer
}

// NewNodeManager instantiates the global daemon manager.
func NewNodeManager(cfg *config.DaemonConfig) *NodeManager {
	nm := &NodeManager{
		cfg:     cfg,
		servers: make(map[string]*ServerManager),
	}
	nm.Installer = NewInstaller(cfg.NodeHostname, cfg.BackendURL, cfg.APIToken, nm)
	return nm
}

func (nm *NodeManager) Config() *config.DaemonConfig { return nm.cfg }
func (nm *NodeManager) IP() string                   { return nm.NodeIP }

// RegisterInDatabase finds the Node by IP/Hostname in the database,
// marks it as 'online', retrieves its UUID, and begins the RAM syncing routine.
func (nm *NodeManager) RegisterInDatabase(ctx context.Context) error {
	hostname := nm.cfg.NodeHostname
	ip := nm.cfg.NodeIP

	log.Printf("[node] Booting up... Registering identity Hostname=%s IP=%s", hostname, ip)

	var id string
	query := `
		UPDATE nodes 
		SET status = 'online', ip = $2, updated_at = NOW() 
		WHERE hostname = $1 
		RETURNING id
	`
	err := database.Pool.QueryRow(ctx, query, hostname, ip).Scan(&id)
	if err != nil {
		return fmt.Errorf("failed to register node in database (ensure node exists in DB!): %w", err)
	}

	nm.NodeID = id
	nm.NodeIP = ip

	log.Printf("[node] Sucessfully assumed identity UUID: %s", id)

	// Ensure Master Proxy is ready on port 5520
	if err := nm.EnsureMasterProxy(ctx); err != nil {
		log.Printf("[node] WARNING: Master Proxy failed to start: %v", err)
	}

	go nm.ramSyncRoutine()
	return nil
}

// EnsureMasterProxy ensures the singleton proxy on port 5520 is active.
func (nm *NodeManager) EnsureMasterProxy(ctx context.Context) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if nm.MasterProxy != nil {
		return nil
	}

	log.Printf("[node] Initializing Master Proxy on port 5520...")
	
	// Use a deterministic UUID for the master proxy to avoid Postgres syntax errors
	masterID := "00000000-0000-0000-0000-000000005520"
	
	// We use 0 as RAM for master as it doesn't represent a game server
	nm.MasterProxy = NewServerManager(nm.cfg, masterID, "Master Proxy", 5520, 0, nm.NodeIP, "master", "proxy", "")
	
	// Sync initial routes before starting
	if err := nm.SyncMasterRoutes(ctx); err != nil {
		log.Printf("[node] Initial route sync failed: %v", err)
	}

	return nm.MasterProxy.Start()
}

// SyncMasterRoutes regenerates the Master Proxy config with all active SNI routes and triggers SIGHUP.
func (nm *NodeManager) SyncMasterRoutes(ctx context.Context) error {
	if nm.MasterProxy == nil {
		return nil
	}

	log.Printf("[node] Synchronizing Master Proxy routing table (PostgreSQL)...")

	// Query all valid hostnames for this node
	query := `SELECT hostname, port FROM servers WHERE node_id = $1 AND status = 'running'`
	rows, err := database.Pool.Query(ctx, query, nm.NodeID)
	if err != nil {
		return err
	}
	defer rows.Close()

	routes := make(map[string][]string)
	for rows.Next() {
		var hostname string
		var port int
		if err := rows.Scan(&hostname, &port); err == nil && hostname != "" && !strings.Contains(hostname, "unknown") {
			routes[hostname] = []string{fmt.Sprintf("127.0.0.1:%d", port)}
		}
	}

	// Build Config based on available routes
	var handlers []any
	if len(routes) > 0 {
		handlers = append(handlers, map[string]any{
			"type": "sni-router",
			"config": map[string]any{
				"routes": routes,
			},
		})
	}
	
	// Always add forwarder at the end or as primary if no routes (default behavior)
	handlers = append(handlers, map[string]any{
		"type": "forwarder",
	})

	cfg := map[string]any{
		"listen":   ":5520",
		"handlers": handlers,
	}

	configJSON, _ := json.MarshalIndent(cfg, "", "  ")
	workDir := filepath.Join("data", "servers", nm.MasterProxy.ID)
	_ = os.MkdirAll(workDir, 0755)
	
	err = os.WriteFile(filepath.Join(workDir, "config.json"), configJSON, 0644)
	if err != nil {
		return err
	}

	// Hot reload if already running
	if nm.MasterProxy.actualState == StateRunning && nm.MasterProxy.cmd != nil && nm.MasterProxy.cmd.Process != nil {
		log.Printf("[node] Sending SIGHUP to Master Proxy (PID %d) for hot-reload", nm.MasterProxy.cmd.Process.Pid)
		return nm.MasterProxy.cmd.Process.Signal(syscall.SIGHUP)
	}

	return nil
}

// ramSyncRoutine polls the underlying running servers to calculate used RAM
// and updates the database record every 30 seconds.
func (nm *NodeManager) ramSyncRoutine() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		nm.mu.RLock()
		var used int
		for _, srv := range nm.servers {
			if srv.Status().ActualState == "RUNNING" {
				used += srv.allocatedRAM
			}
		}
		nm.mu.RUnlock()

		if database.Pool == nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := database.Pool.Exec(ctx, "UPDATE nodes SET updated_at = NOW() WHERE id = $1", nm.NodeID) // RAM stat is kept in servers actively, but we can bump last seen
		cancel()
		
		if err != nil {
			log.Printf("[node] Heartbeat update failed: %v", err)
		}
	}
}

// GetServer safely retrieves a supervised server instance.
func (nm *NodeManager) GetServer(serverID string) (*ServerManager, bool) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	srv, ok := nm.servers[serverID]
	return srv, ok
}

// AddServer binds a new ServerManager into orchestrator memory.
func (nm *NodeManager) AddServer(serverID string, manager *ServerManager) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.servers[serverID] = manager
}

// RemoveServer detaches a server instance from memory completely.
func (nm *NodeManager) RemoveServer(serverID string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	delete(nm.servers, serverID)
}

// LoadExistingServers queries the database for ALL servers assigned to this node,
// maps them into memory, and only auto-starts the ones that were 'running'.
func (nm *NodeManager) LoadExistingServers(ctx context.Context) error {
	query := `SELECT id, name, port, ram_mb, version, status, server_type, hostname FROM servers WHERE node_id = $1`
	
	rows, err := database.Pool.Query(ctx, query, nm.NodeID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, version, status, sType, hostname string
		var port, ram int
		
		if err := rows.Scan(&id, &name, &port, &ram, &version, &status, &sType, &hostname); err != nil {
			continue
		}

		srv := NewServerManager(nm.cfg, id, name, port, ram, nm.NodeIP, version, sType, hostname)
		nm.AddServer(id, srv)
		
		if status == "running" {
			log.Printf("[node] Auto-resurrecting server %s at port %d", id, port)
			// Fire and forget start command
			go func(s *ServerManager) {
				if err := s.Start(); err != nil {
					log.Printf("[node] Failed to auto-resurrect %s: %v", s.ID, err)
				}
			}(srv)
		} else {
			log.Printf("[node] Mapped offline server %s into memory natively", id)
		}
	}

	return nil
}
