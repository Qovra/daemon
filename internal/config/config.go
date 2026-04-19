package config

import (
	"encoding/json"
	"fmt"
	"os"
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
	ProxyArgs []string `json:"proxy_args"`
}

// Load reads and parses the JSON configuration from the given path.
func Load(path string) (*DaemonConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read daemon config: %w", err)
	}

	var cfg DaemonConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse daemon config json: %w", err)
	}

	if cfg.APIListen == "" {
		cfg.APIListen = ":8080" // default
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("api_token must be configured for security")
	}
	if cfg.ProxyBinary == "" {
		return nil, fmt.Errorf("proxy_binary path must be provided")
	}

	return &cfg, nil
}
