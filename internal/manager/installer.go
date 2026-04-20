package manager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
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

// InstallGameServer runs the real hytale-downloader CLI.
func (i *Installer) InstallGameServer(ctx context.Context, serverID string) error {
	log.Printf("[installer] Starting installation for server %s...", serverID)

	srv, ok := i.nm.GetServer(serverID)
	if !ok {
		return fmt.Errorf("server %s not found in memory", serverID)
	}

	workDir := srv.WorkDir()
	_ = os.MkdirAll(workDir, 0755)

	// Update Backend: Mark as installing
	i.reportProgress(serverID, 0, true, "installing", "", "")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[installer-%s] CRITICAL PANIC during install: %v", serverID, r)
			}
		}()

		// Command: /usr/local/bin/hytale-downloader
		// We execute it inside the workDir. 
		// According to manual, it downloads 'Server/' and 'Assets.zip'
		cmd := exec.Command("/usr/local/bin/hytale-downloader")
		cmd.Dir = workDir
		
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		
		if err := cmd.Start(); err != nil {
			log.Printf("[installer-%s] Failed to start downloader: %v", serverID, err)
			i.reportProgress(serverID, 0, false, "crashed", "", "")
			return
		}

		// Regex for OAuth2 detection
		reURL := regexp.MustCompile(`https?://[a-zA-Z0-9./?=_-]+`)
		reCode := regexp.MustCompile(`[A-Z0-9]{4}-[A-Z0-9]{4}`)

		scanner := bufio.NewScanner(stdout)
		go func() {
			errScanner := bufio.NewScanner(stderr)
			for errScanner.Scan() {
				line := errScanner.Text()
				log.Printf("[installer-%s][stderr] %s", serverID, line)
			}
		}()

		authURL, authCode := "", ""
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[installer-%s] %s", serverID, line)

			// Detect Auth URL
			if foundURL := reURL.FindString(line); foundURL != "" && authURL == "" {
				authURL = foundURL
				log.Printf("[installer-%s] Detected Auth URL: %s", serverID, authURL)
				i.reportProgress(serverID, 10, true, "installing", authURL, authCode)
			}

			// Detect Auth Code
			if foundCode := reCode.FindString(line); foundCode != "" && authCode == "" {
				authCode = foundCode
				log.Printf("[installer-%s] Detected Auth Code: %s", serverID, authCode)
				i.reportProgress(serverID, 15, true, "installing", authURL, authCode)
			}

			// Simple progress guessing based on output keywords
			if strings.Contains(line, "Downloading") {
				i.reportProgress(serverID, 30, true, "installing", authURL, authCode)
			} else if strings.Contains(line, "Extracting") {
				i.reportProgress(serverID, 70, true, "installing", authURL, authCode)
			} else if strings.Contains(line, "Success") || strings.Contains(line, "completed") {
				i.reportProgress(serverID, 95, true, "installing", authURL, authCode)
			}
		}

		if err := cmd.Wait(); err != nil {
			log.Printf("[installer-%s] Downloader exited with error: %v", serverID, err)
			i.reportProgress(serverID, 0, false, "crashed", "", "")
			return
		}

		// Success
		log.Printf("[installer-%s] Installation completed. Auto-starting...", serverID)
		if err := srv.Start(); err != nil {
			log.Printf("[installer-%s] Auto-start failed: %v", serverID, err)
			i.reportProgress(serverID, 100, false, "crashed", "", "")
		} else {
			i.nm.SyncMasterRoutes(context.Background())
			i.reportProgress(serverID, 100, false, "running", "", "")
		}
	}()

	return nil
}

func (i *Installer) reportProgress(serverID string, progress int, installing bool, status, authURL, authCode string) {
	url := fmt.Sprintf("%s/api/internal/servers/%s/progress", i.backendURL, serverID)
	
	payload, _ := json.Marshal(map[string]any{
		"progress":   progress,
		"installing": installing,
		"status":     status,
		"auth_url":   authURL,
		"auth_code":  authCode,
	})

	req, _ := http.NewRequest("PATCH", url, bytes.NewBuffer(payload))
	req.Header.Set("Authorization", "Bearer "+i.token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[installer-%s] Failed to report progress: %v", serverID, err)
		return
	}
	defer resp.Body.Close()
}
