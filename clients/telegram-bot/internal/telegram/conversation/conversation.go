// Package conversation tracks the in-progress, multi-step text prompts the
// bot uses to collect input Telegram has no native form for (email,
// password). It is deliberately separate from internal/core/session:
// Session is durable, authenticated identity; a conversation.State is
// short-lived UI flow state that exists only between "please enter your
// email" and the corresponding reply, and is gone once the flow finishes
// or is abandoned.
package conversation

import "sync"

// Step identifies which input the bot is waiting for next in a chat's
// registration or login flow.
type Step int

const (
	// StepNone means no registration/login flow is in progress for a chat.
	StepNone Step = iota
	StepAwaitingRegisterEmail
	StepAwaitingRegisterPassword
	StepAwaitingLoginEmail
	StepAwaitingLoginPassword
)

// State is the in-progress input for one chat.
//
// Email is buffered here only for the brief window between the email and
// password prompts. Password is never a field here, and must never become
// one: the handler that reads a password off an incoming message passes it
// directly to internal/core/auth and then discards it — see
// internal/telegram/commands.
type State struct {
	Step  Step
	Email string
}

// Store tracks one State per Telegram chat ID. Implementations must be
// concurrency-safe, since the bot serves many chats from a single process.
type Store interface {
	Get(chatID int64) (State, bool)
	Set(chatID int64, state State)
	Clear(chatID int64)
}

// MemoryStore is an in-process, concurrency-safe Store.
type MemoryStore struct {
	mu     sync.RWMutex
	states map[int64]State
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{states: make(map[int64]State)}
}

// Get reports the current State for chatID, and whether one exists at all
// (as opposed to a zero-value State, which is indistinguishable from
// StepNone but explicit here for clarity at call sites).
func (m *MemoryStore) Get(chatID int64) (State, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.states[chatID]
	return s, ok
}

// Set replaces chatID's State, starting or advancing its flow.
func (m *MemoryStore) Set(chatID int64, state State) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.states[chatID] = state
}

// Clear ends chatID's flow, if any.
func (m *MemoryStore) Clear(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.states, chatID)
}
