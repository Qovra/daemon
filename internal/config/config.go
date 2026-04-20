package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DaemonConfig holds the settings for the Daemon itself.
type DaemonConfig struct {
	// APIListen is the address the dashboard will bind to to receive commands (e.g. ":8080")
	APIListen string `json:"api_listen"`
	// APIToken is the mandatory Bearer token required for all API endpoints.
	APIToken string `json:"api_token"`
	// ProxyBinary is the absolute or relative path to the compiled Hytale-Proxy executable.
	ProxyBinary string `json:"proxy_binary"`
	// ProxyArgs are the arguments to pass to the binary, normally including "-config" and the path.
	NodeHostname       string `json:"node_hostname"`
	NodeIP             string `json:"node_ip"`
	ProxyTemplatesPath string `json:"proxy_templates_path"`
	BackendURL         string `json:"backend_url"`
}

// Load reads and parses the JSON configuration or loads them fundamentally from ENV vars now.
// For the new .env implementation, we prioritize Env vars over the JSON file.
func Load(path string) (*DaemonConfig, error) {
	// First load JSON fallback if provided
	var cfg DaemonConfig
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	// Environment variables override for the Multi-Node panel
	if port := os.Getenv("DAEMON_PORT"); port != "" {
		cfg.APIListen = ":" + port
	} else if cfg.APIListen == "" {
		cfg.APIListen = ":8080"
	}

	if tkn := os.Getenv("DAEMON_API_TOKEN"); tkn != "" {
		cfg.APIToken = tkn
	}
	if host := os.Getenv("NODE_HOSTNAME"); host != "" {
		cfg.NodeHostname = host
	}
	if ip := os.Getenv("NODE_IP"); ip != "" {
		cfg.NodeIP = ip
	}
	if tpls := os.Getenv("PROXY_TEMPLATES_PATH"); tpls != "" {
		cfg.ProxyTemplatesPath = tpls
	}
	if bURL := os.Getenv("BACKEND_URL"); bURL != "" {
		cfg.BackendURL = bURL
	} else if cfg.BackendURL == "" {
		cfg.BackendURL = "http://localhost:3000"
	}
	
	// Legacy daemon_config.json support for the execution layer
	if cfg.ProxyBinary == "" {
		cfg.ProxyBinary = "../Hytale-Proxy/proxy"
	}

	// NEW: Resolve absolute path to avoid breakages when changing Dir in exec.Cmd
	if abs, err := filepath.Abs(cfg.ProxyBinary); err == nil {
		cfg.ProxyBinary = abs
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("api_token must be configured (DAEMON_API_TOKEN)")
	}

	return &cfg, nil
}
