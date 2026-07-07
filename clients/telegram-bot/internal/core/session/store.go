package session

import (
	"context"
	"errors"
	"sync"
)

// ErrNotFound is returned by Store.Get when no session exists for a chat.
var ErrNotFound = errors.New("session: not found")

// Store persists one Session per Telegram chat ID. Implementations must be
// safe for concurrent use: a single bot process serves many chats at once,
// each potentially reading or writing its session at the same time.
type Store interface {
	Get(ctx context.Context, chatID int64) (Session, error)
	Save(ctx context.Context, s Session) error
	Delete(ctx context.Context, chatID int64) error
}

// MemoryStore is an in-process, concurrency-safe Store. It is the Phase 2
// implementation; a later phase can introduce a Redis-backed Store behind
// the same interface without changing any caller.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[int64]Session
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[int64]Session)}
}

// Get returns ErrNotFound if chatID has no stored session, rather than a
// zero-value Session with no error, so callers can't mistake "no session"
// for "session with empty fields."
func (m *MemoryStore) Get(_ context.Context, chatID int64) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[chatID]
	if !ok {
		return Session{}, ErrNotFound
	}
	return s, nil
}

// Save stores s, keyed by s.TelegramChatID, overwriting any existing entry.
func (m *MemoryStore) Save(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[s.TelegramChatID] = s
	return nil
}

// Delete removes chatID's session, if any. Deleting a chat with no session
// is not an error.
func (m *MemoryStore) Delete(_ context.Context, chatID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, chatID)
	return nil
}
