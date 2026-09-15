package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/cache"
	"github.com/opensbc/opensbc/internal/config"
)

func connectRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	if len(cfg.RedisSentinelAddrs) > 0 {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
		return cache.ConnectSentinel(ctx, cfg.RedisSentinelMaster, cfg.RedisSentinelAddrs, opt.Password, opt.Password, opt.DB)
	}
	return cache.Connect(ctx, cfg.RedisURL)
}

// publishAll tells running API nodes that everything may have changed.
func publishAll(ctx context.Context, rdb *redis.Client) {
	cache.Publish(ctx, rdb, cache.ChanRateDeckChanged, uuid.Nil.String())
	cache.Publish(ctx, rdb, cache.ChanRoutesChanged, uuid.Nil.String())
	cache.Publish(ctx, rdb, cache.ChanCarriersChanged, uuid.Nil.String())
	cache.Publish(ctx, rdb, cache.ChanCustomerIPs, "")
}
