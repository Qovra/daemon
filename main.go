package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Qovra/hytale-daemon/internal/api"
	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/manager"
)

func main() {
	configPath := flag.String("config", "daemon_config.json", "path to daemon.json config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Fatal: failed to load configuration: %v", err)
	}

	log.Println("[main] Starting Hytale-Daemon v0.1.0")
	log.Printf("[main] Target Proxy Binary: %s", cfg.ProxyBinary)

	mgr := manager.New(cfg)
	server := api.NewServer(cfg, mgr)

	// Intercept SIGINT/SIGTERM to cleanly stop the proxy before the daemon dies
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("[main] Shutting down Daemon... stopping proxy child process")
		_ = mgr.Stop()
		os.Exit(0)
	}()

	// Optionally start the proxy immediately, or wait for API /start call.
	// Normally a daemon launches the payload at startup:
	log.Println("[main] Auto-starting proxy process...")
	if err := mgr.Start(); err != nil {
		log.Printf("[main] Warning: initial proxy start failed: %v", err)
	}

	// Blocks forever
	if err := server.Start(); err != nil {
		log.Fatalf("Fatal: API server died: %v", err)
	}
}
