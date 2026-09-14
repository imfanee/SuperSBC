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
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}
