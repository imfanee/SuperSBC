// Package cache wraps Redis: hot caches, admission counters, reservation TTL
// keys and pub/sub (Section 1, Redis responsibilities).
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Connect opens a Redis client from a redis:// URL and pings it.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	c := redis.NewClient(opt)
	return ping(ctx, c)
}

// ConnectSentinel connects through Redis Sentinel (D-68): the client asks the
// sentinels for the current master of masterName and follows failovers.
// password and db apply to the master; sentinelPassword to the sentinels.
func ConnectSentinel(ctx context.Context, masterName string, sentinels []string, password, sentinelPassword string, db int) (*redis.Client, error) {
	if masterName == "" || len(sentinels) == 0 {
		return nil, fmt.Errorf("sentinel: master name and at least one sentinel address are required")
	}
	c := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName: masterName, SentinelAddrs: sentinels, Password: password, SentinelPassword: sentinelPassword, DB: db,
	})
	return ping(ctx, c)
}

func ping(ctx context.Context, c *redis.Client) (*redis.Client, error) {
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}
