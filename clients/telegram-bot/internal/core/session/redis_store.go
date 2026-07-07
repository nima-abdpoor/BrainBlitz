package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// redisKeyPrefix namespaces this bot's session keys within a Redis instance
// that may be shared with the main backend — a separate logical prefix, not
// sharing keys with game_app's own Redis usage (see
// docs/client/client-architecture.md §4).
const redisKeyPrefix = "telegram-bot:session:"

// RedisStore is a Redis-backed Store, letting session data survive a bot
// process restart — the upgrade path from MemoryStore recommended in
// docs/client/client-architecture.md §4. It deliberately stores ONLY auth
// session data (tokens, user ID); no in-flight matchmaking/game state is
// ever persisted here — see docs/client/client-roadmap.md's Phase 6 notes
// for why: without a backend resync API (gap-analysis.md G-14), a
// "recovered" game would have to be faked, which this bot never does.
type RedisStore struct {
	client RedisCmdable
	ttl    time.Duration
}

// RedisCmdable is the actual subset of *redis.Client's method set this
// package calls — named to match go-redis's own "Cmdable" terminology and
// kept minimal so a fake (see redis_store_test.go) needs no real Redis
// server and no extra test dependency.
type RedisCmdable interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// ErrRedisNil is returned by a RedisCmdable.Get implementation when key
// doesn't exist — mirrors go-redis's redis.Nil sentinel so RedisStore.Get
// can translate it to session.ErrNotFound without importing the go-redis
// package's error type directly into this narrow interface.
var ErrRedisNil = errors.New("session: redis key not found")

// NewRedisStore builds a RedisStore. ttl bounds how long a session survives
// in Redis with no activity (refreshed on every Save) — this defends
// against unbounded growth from chats that log in once and never return,
// not against token expiry, which session.Session.Stale already handles at
// the application layer regardless of how long the raw data sits in Redis.
func NewRedisStore(client RedisCmdable, ttl time.Duration) *RedisStore {
	return &RedisStore{client: client, ttl: ttl}
}

func redisKey(chatID int64) string {
	return fmt.Sprintf("%s%d", redisKeyPrefix, chatID)
}

// Get returns ErrNotFound if chatID has no stored session (translating both
// ErrRedisNil and a bare redis.Nil-wrapping error the same way), matching
// MemoryStore's contract exactly so callers never need to know which Store
// implementation they're using.
func (s *RedisStore) Get(ctx context.Context, chatID int64) (Session, error) {
	data, err := s.client.Get(ctx, redisKey(chatID))
	if errors.Is(err, ErrRedisNil) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("redis get: %w", err)
	}

	var sess Session
	if err := json.Unmarshal([]byte(data), &sess); err != nil {
		return Session{}, fmt.Errorf("decoding session: %w", err)
	}
	return sess, nil
}

// Save stores sess as JSON, keyed by sess.TelegramChatID, refreshing the
// key's TTL.
func (s *RedisStore) Save(ctx context.Context, sess Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("encoding session: %w", err)
	}
	if err := s.client.Set(ctx, redisKey(sess.TelegramChatID), string(data), s.ttl); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

// Delete removes chatID's session, if any. Deleting a chat with no session
// is not an error (go-redis's Del reports 0 keys removed, not an error, so
// this falls out naturally).
func (s *RedisStore) Delete(ctx context.Context, chatID int64) error {
	if err := s.client.Del(ctx, redisKey(chatID)); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

var _ Store = (*RedisStore)(nil)
