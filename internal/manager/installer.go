package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"
)

// Installer handles the asynchronous download and preparation of server files.
type Installer struct {
	nodeID     string
	backendURL string
	token      string
	nm         *NodeManager
}

func NewInstaller(nodeID, backendURL, token string, nm *NodeManager) *Installer {
	return &Installer{
		nodeID:     nodeID,
		backendURL: backendURL,
		token:      token,
		nm:         nm,
	}
}

// InstallGameServer simulates downloading a Hytale server binary.
func (i *Installer) InstallGameServer(ctx context.Context, serverID string) error {
	log.Printf("[installer] Starting installation for server %s...", serverID)

	// Update Backend: Mark as installing
	i.reportProgress(serverID, 0, true, "installing")

	// Simulate progress
	go func() {
		steps := []string{
			"Downloading Hytale Server Core...",
			"Verifying checksums...",
			"Extracting assets...",
			"Preparing Java runtime environment...",
			"Finalizing installation...",
		}

		for idx, step := range steps {
			progress := (idx + 1) * 20
			log.Printf("[installer-%s] %s (%d%%)", serverID, step, progress)
			
			i.reportProgress(serverID, progress, true, "installing")
			
			time.Sleep(time.Duration(rand.Intn(2)+1) * time.Second)
		}

		// Finish
		log.Printf("[installer-%s] Installation completed successfully. Auto-starting...", serverID)
		
		srv, ok := i.nm.GetServer(serverID)
		if ok {
			if err := srv.Start(); err != nil {
				log.Printf("[installer-%s] Auto-start failed: %v", serverID, err)
				i.reportProgress(serverID, 100, false, "crashed")
				return
			}
			
			// Hot Reload Proxy
			i.nm.SyncMasterRoutes(context.Background())
			
			// Success
			i.reportProgress(serverID, 100, false, "running")
		} else {
			i.reportProgress(serverID, 100, false, "stopped")
		}
	}()

	return nil
}

func (i *Installer) reportProgress(serverID string, progress int, installing bool, status string) {
	url := fmt.Sprintf("%s/api/internal/servers/%s/progress", i.backendURL, serverID)
	
	payload, _ := json.Marshal(map[string]any{
		"progress":   progress,
		"installing": installing,
		"status":     status,
	})

	req, _ := http.NewRequest("PATCH", url, bytes.NewBuffer(payload))
	req.Header.Set("Authorization", "Bearer "+i.token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[installer-%s] Failed to report progress to backend: %v", serverID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[installer-%s] Backend rejected progress update: %s", serverID, resp.Status)
	}
}
