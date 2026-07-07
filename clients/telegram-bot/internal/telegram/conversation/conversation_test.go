package conversation

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestMemoryStore_GetMissing(t *testing.T) {
	store := NewMemoryStore()

	_, ok := store.Get(1)
	if ok {
		t.Error("Get on an empty store should report ok=false")
	}
}

func TestMemoryStore_SetThenGet(t *testing.T) {
	store := NewMemoryStore()
	want := State{Step: StepAwaitingRegisterPassword, Email: "alice@example.com"}

	store.Set(1, want)

	got, ok := store.Get(1)
	if !ok {
		t.Fatal("Get should report ok=true after Set")
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestMemoryStore_SetOverwrites(t *testing.T) {
	store := NewMemoryStore()
	store.Set(1, State{Step: StepAwaitingRegisterEmail})
	store.Set(1, State{Step: StepAwaitingLoginEmail})

	got, _ := store.Get(1)
	if got.Step != StepAwaitingLoginEmail {
		t.Errorf("Step = %v, want the most recently Set value", got.Step)
	}
}

func TestMemoryStore_Clear(t *testing.T) {
	store := NewMemoryStore()
	store.Set(1, State{Step: StepAwaitingRegisterEmail})

	store.Clear(1)

	if _, ok := store.Get(1); ok {
		t.Error("Get after Clear should report ok=false")
	}
}

// TestMemoryStore_IsolatedPerChat is the direct test of the "keep
// conversation state isolated" requirement: two chats progressing through
// different steps must never see each other's state.
func TestMemoryStore_IsolatedPerChat(t *testing.T) {
	store := NewMemoryStore()
	store.Set(1, State{Step: StepAwaitingRegisterEmail, Email: "chat1@example.com"})
	store.Set(2, State{Step: StepAwaitingLoginPassword, Email: "chat2@example.com"})

	got1, _ := store.Get(1)
	got2, _ := store.Get(2)

	if got1.Step != StepAwaitingRegisterEmail || got1.Email != "chat1@example.com" {
		t.Errorf("chat 1 state = %+v, want untouched by chat 2's Set", got1)
	}
	if got2.Step != StepAwaitingLoginPassword || got2.Email != "chat2@example.com" {
		t.Errorf("chat 2 state = %+v, want untouched by chat 1's Set", got2)
	}

	store.Clear(1)
	if _, ok := store.Get(2); !ok {
		t.Error("clearing chat 1 must not clear chat 2")
	}
}

// TestMemoryStore_ConcurrentAccess is the "support multiple concurrent
// Telegram users" requirement as a race-detector-checked test.
func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()

	const chats = 50
	var wg sync.WaitGroup
	for i := 0; i < chats; i++ {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			store.Set(chatID, State{Step: StepAwaitingRegisterEmail})
			store.Get(chatID)
			store.Clear(chatID)
		}(int64(i))
	}
	wg.Wait()
}

// TestState_NeverHasAPasswordField guards the same requirement as
// session.Session's equivalent test: conversation.State must never grow a
// field that could hold a password.
func TestState_NeverHasAPasswordField(t *testing.T) {
	typ := reflect.TypeOf(State{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "password") || strings.Contains(name, "passwd") {
			t.Errorf("State must never store a password, but found field %q", typ.Field(i).Name)
		}
	}
}
