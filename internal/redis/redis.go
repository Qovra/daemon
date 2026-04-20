package redisclient

import (
	"context"
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

// Init optionally initializes the Redis client.
// If REDIS_URL is not set or empty, Redis integration is disabled gracefully.
func Init(ctx context.Context) error {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		log.Println("[redis] REDIS_URL not set, redis integration running in stub mode")
		return nil
	}

	opt, err := redis.ParseURL(url)
	if err != nil {
		return err
	}

	Client = redis.NewClient(opt)
	if err := Client.Ping(ctx).Err(); err != nil {
		Client = nil // disable on error
		return err
	}

	log.Println("[redis] successfully connected to redis")
	return nil
}

// SetServerStatus registers the server host/port info in redis.
func SetServerStatus(ctx context.Context, id, host string, port int, version string) {
	if Client == nil {
		return
	}
	
	key := "servers:" + id
	Client.HSet(ctx, key, map[string]interface{}{
		"host":    host,
		"port":    port,
		"version": version,
	})
}

// RemoveServer removes the server from redis when stopped.
func RemoveServer(ctx context.Context, id string) {
	if Client == nil {
		return
	}
	Client.Del(ctx, "servers:"+id)
}

// SetRoute registers a hostname targeting a specific IP:Port in the global Redis routing table.
func SetRoute(ctx context.Context, hostname, target string) {
	if Client == nil {
		return
	}
	// Use a global hash for SNI routing: hostname -> target (ip:port)
	Client.HSet(ctx, "proxy:routes", hostname, target)
}

// RemoveRoute unregisters a hostname from the routing table.
func RemoveRoute(ctx context.Context, hostname string) {
	if Client == nil {
		return
	}
	Client.HDel(ctx, "proxy:routes", hostname)
}

// GetRoutes fetches the entire routing table from Redis.
func GetRoutes(ctx context.Context) (map[string]string, error) {
	if Client == nil {
		return nil, nil
	}
	return Client.HGetAll(ctx, "proxy:routes").Result()
}
