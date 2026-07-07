package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeRedis is an in-memory RedisCmdable double — this package's tests
// never talk to a real Redis server, matching this codebase's established
// style of exercising business logic against fakes rather than real
// infrastructure.
type fakeRedis struct {
	mu   sync.Mutex
	data map[string]string
	ttls map[string]time.Duration
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{data: make(map[string]string), ttls: make(map[string]time.Duration)}
}

func (f *fakeRedis) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return "", ErrRedisNil
	}
	return v, nil
}

func (f *fakeRedis) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = value
	f.ttls[key] = ttl
	return nil
}

func (f *fakeRedis) Del(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	delete(f.ttls, key)
	return nil
}

func TestRedisStore_GetMissing(t *testing.T) {
	store := NewRedisStore(newFakeRedis(), time.Hour)

	_, err := store.Get(context.Background(), 42)
	if err != ErrNotFound {
		t.Errorf("Get on empty store returned %v, want ErrNotFound", err)
	}
}

func TestRedisStore_SaveThenGet(t *testing.T) {
	store := NewRedisStore(newFakeRedis(), time.Hour)
	want := Session{
		TelegramChatID: 42,
		UserID:         "123",
		AccessToken:    "access-token",
		RefreshToken:   "refresh-token",
		TokenIssuedAt:  time.Now().UTC().Truncate(time.Second), // JSON round-trips to second precision in practice
	}

	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := store.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !got.TokenIssuedAt.Equal(want.TokenIssuedAt) || got.TelegramChatID != want.TelegramChatID ||
		got.UserID != want.UserID || got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestRedisStore_Delete(t *testing.T) {
	store := NewRedisStore(newFakeRedis(), time.Hour)
	ctx := context.Background()
	_ = store.Save(ctx, Session{TelegramChatID: 1})

	if err := store.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := store.Get(ctx, 1); err != ErrNotFound {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestRedisStore_DeleteMissingIsNotError(t *testing.T) {
	store := NewRedisStore(newFakeRedis(), time.Hour)
	if err := store.Delete(context.Background(), 999); err != nil {
		t.Errorf("Delete on a chat with no session returned an error: %v", err)
	}
}

func TestRedisStore_SaveRefreshesTTL(t *testing.T) {
	redis := newFakeRedis()
	store := NewRedisStore(redis, 24*time.Hour)

	if err := store.Save(context.Background(), Session{TelegramChatID: 1}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	redis.mu.Lock()
	ttl := redis.ttls[redisKey(1)]
	redis.mu.Unlock()
	if ttl != 24*time.Hour {
		t.Errorf("stored TTL = %v, want %v", ttl, 24*time.Hour)
	}
}

func TestRedisStore_KeysAreNamespaced(t *testing.T) {
	redis := newFakeRedis()
	store := NewRedisStore(redis, time.Hour)
	_ = store.Save(context.Background(), Session{TelegramChatID: 7})

	redis.mu.Lock()
	_, ok := redis.data["telegram-bot:session:7"]
	redis.mu.Unlock()
	if !ok {
		t.Error("RedisStore must namespace keys under telegram-bot:session: to avoid colliding with game_app's own Redis usage")
	}
}
