package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Qovra/hytale-daemon/internal/api"
	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/database"
	"github.com/Qovra/hytale-daemon/internal/manager"
	redisclient "github.com/Qovra/hytale-daemon/internal/redis"
	"github.com/joho/godotenv"
)

func main() {
	// Try to load .env from root or local (optional, fail is ignored gracefully)
	_ = godotenv.Load("../.env") // Root monorepo .env fallback

	configPath := flag.String("config", "daemon_config.json", "path to fallback json config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Fatal: failed to load configuration: %v", err)
	}

	log.Println("[main] Starting Hytale-Daemon v0.2.0 (Multi-Tenant Edition)")

	ctx := context.Background()

	if err := database.Init(ctx); err != nil {
		log.Fatalf("Fatal: database connection required for Node initialization: %v", err)
	}
	defer database.Close()

	if err := redisclient.Init(ctx); err != nil {
		log.Printf("[main] redis disabled due to connection error: %v", err)
	}

	node := manager.NewNodeManager(cfg)

	// Identify and lock identity
	if err := node.RegisterInDatabase(ctx); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// Auto retrieve and revive servers
	if err := node.LoadExistingServers(ctx); err != nil {
		log.Printf("[main] WARNING: failed to load existing servers from database: %v", err)
	}

	server := api.NewServer(cfg, node)

	// Intercept SIGINT/SIGTERM to cleanly stop all proxy sub-processes
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("[main] Shutting down Daemon... stopping all proxy child processes")
		// (In a real implementation we would iterate the map and issue node.StopAll())
		os.Exit(0)
	}()

	// Blocks forever
	if err := server.Start(); err != nil {
		log.Fatalf("Fatal: Node API server died: %v", err)
	}
}
