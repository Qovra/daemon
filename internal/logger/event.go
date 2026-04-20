package logger

import (
	"context"
	"encoding/json"
	"log"

	"github.com/Qovra/hytale-daemon/internal/database"
)

// LogEvent inserts an audit/status log entry securely into PostgreSQL using context timeouts.
func LogEvent(ctx context.Context, level, target, targetID, action, message string, metadata map[string]any) {
	if database.Pool == nil {
		return // Ignore safely if DB not active
	}

	var metaBytes []byte = []byte("{}")
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			metaBytes = b
		}
	}

	var parsedTargetID *string
	if targetID != "" {
		parsedTargetID = &targetID
	}

	query := `
		INSERT INTO event_logs (level, target, target_id, action, message, metadata) 
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := database.Pool.Exec(ctx, query, level, target, parsedTargetID, action, message, metaBytes)
	if err != nil {
		log.Printf("[logger] Failed to insert event_log (action=%s): %v", action, err)
	}
}
