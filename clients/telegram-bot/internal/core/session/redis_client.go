package session

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// goRedisAdapter adapts *redis.Client (github.com/redis/go-redis/v9) to the
// minimal RedisCmdable interface RedisStore actually depends on, translating
// go-redis's redis.Nil sentinel to this package's own ErrRedisNil so
// RedisStore never needs to import go-redis's error type directly.
type goRedisAdapter struct {
	client *redis.Client
}

// NewGoRedisClient wraps client for use as RedisStore's RedisCmdable,
// e.g.: session.NewRedisStore(session.NewGoRedisClient(redisClient), ttl).
func NewGoRedisClient(client *redis.Client) RedisCmdable {
	return &goRedisAdapter{client: client}
}

func (a *goRedisAdapter) Get(ctx context.Context, key string) (string, error) {
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrRedisNil
	}
	return val, err
}

func (a *goRedisAdapter) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return a.client.Set(ctx, key, value, ttl).Err()
}

func (a *goRedisAdapter) Del(ctx context.Context, key string) error {
	return a.client.Del(ctx, key).Err()
}
