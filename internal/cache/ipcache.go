package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// IPCache caches source-address lookups (Section 3 Step 1). A negative
// result is cached too, briefly, so scanners do not hammer Postgres.
type IPCache struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewIPCache creates the cache.
func NewIPCache(rdb *redis.Client) *IPCache { return &IPCache{rdb: rdb, ttl: 5 * time.Minute} }

// Entry is the cached value.
type Entry struct {
	Found      bool   `json:"found"`
	CustomerID string `json:"customer_id,omitempty"`
}

func ipKey(ip string, port int, transport string) string {
	return "ip:" + ip + ":" + transport + ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Get returns the cached entry, or nil on a miss or Redis error.
func (c *IPCache) Get(ctx context.Context, ip string, port int, transport string) *Entry {
	raw, err := c.rdb.Get(ctx, ipKey(ip, port, transport)).Bytes()
	if err != nil {
		return nil
	}
	var e Entry
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	return &e
}

// Set stores an entry. Negative entries live 30 s.
func (c *IPCache) Set(ctx context.Context, ip string, port int, transport string, e Entry) {
	raw, _ := json.Marshal(e)
	ttl := c.ttl
	if !e.Found {
		ttl = 30 * time.Second
	}
	_ = c.rdb.Set(ctx, ipKey(ip, port, transport), raw, ttl).Err()
}

// Flush removes every cached address lookup (customer IP change).
func (c *IPCache) Flush(ctx context.Context) error {
	iter := c.rdb.Scan(ctx, 0, "ip:*", 500).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	if len(keys) > 0 {
		return c.rdb.Del(ctx, keys...).Err()
	}
	return nil
}
