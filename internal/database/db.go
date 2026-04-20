package database

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

// Init initializes the PostgreSQL connection pool using pgx.
// It retrieves the URL from the PG_URL environment variable.
func Init(ctx context.Context) error {
	dsn := os.Getenv("PG_URL")
	if dsn == "" {
		return fmt.Errorf("PG_URL env var is not set")
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid PG_URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	Pool = pool
	log.Println("[database] successfully connected to postgresql")

	// Auto-migration: Ensure hostname support for SNI Routing
	migrationQuery := `
		DO $$ 
		BEGIN 
			IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='servers' AND column_name='hostname') THEN
				ALTER TABLE servers ADD COLUMN hostname VARCHAR(255) NOT NULL DEFAULT 'unknown.local';
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'unique_hostname_per_node') THEN
				ALTER TABLE servers ADD CONSTRAINT unique_hostname_per_node UNIQUE (node_id, hostname);
			END IF;
		END $$;
	`
	_, migrationErr := Pool.Exec(ctx, migrationQuery)
	if migrationErr != nil {
		log.Printf("[database] WARNING: auto-migration failed: %v", migrationErr)
	}

	return nil
}

// Close gracefully closes the connection pool.
func Close() {
	if Pool != nil {
		Pool.Close()
	}
}
