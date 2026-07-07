package session

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_GetMissing(t *testing.T) {
	store := NewMemoryStore()

	_, err := store.Get(context.Background(), 42)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on empty store returned %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_SaveThenGet(t *testing.T) {
	store := NewMemoryStore()
	want := Session{
		TelegramChatID: 42,
		UserID:         "123",
		AccessToken:    "access-token",
		RefreshToken:   "refresh-token",
		TokenIssuedAt:  time.Now().UTC(),
	}

	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := store.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// TestMemoryStore_SessionPersistsAcrossCalls verifies that a session saved
// in one call is still retrievable, unchanged, in a later call — the
// persistence guarantee the bot relies on between separate Telegram
// updates (each update is handled independently; there is no in-memory
// "current session" beyond this store).
func TestMemoryStore_SessionPersistsAcrossCalls(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	original := Session{TelegramChatID: 7, UserID: "u7", AccessToken: "tok"}

	if err := store.Save(ctx, original); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := store.Get(ctx, 7)
		if err != nil {
			t.Fatalf("Get call %d returned error: %v", i, err)
		}
		if got != original {
			t.Fatalf("Get call %d = %+v, want %+v", i, got, original)
		}
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	_ = store.Save(ctx, Session{TelegramChatID: 1})

	if err := store.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if _, err := store.Get(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_DeleteMissingIsNotError(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Delete(context.Background(), 999); err != nil {
		t.Errorf("Delete on a chat with no session returned an error: %v", err)
	}
}

// TestMemoryStore_ConcurrentAccess exercises many chat IDs from many
// goroutines simultaneously — the "support multiple concurrent Telegram
// users" requirement translated into a race-detector-checked test. Run
// with -race.
func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	const chats = 50
	var wg sync.WaitGroup
	for i := 0; i < chats; i++ {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			s := Session{TelegramChatID: chatID, UserID: "user"}
			if err := store.Save(ctx, s); err != nil {
				t.Errorf("Save(%d) returned error: %v", chatID, err)
			}
			if _, err := store.Get(ctx, chatID); err != nil {
				t.Errorf("Get(%d) returned error: %v", chatID, err)
			}
			if err := store.Delete(ctx, chatID); err != nil {
				t.Errorf("Delete(%d) returned error: %v", chatID, err)
			}
		}(int64(i))
	}
	wg.Wait()
}

// TestSession_NeverHasAPasswordField is a regression guard for the "never
// store passwords after authentication" requirement: it fails at test time
// if a future change adds any field to Session whose name suggests it
// might hold a password, rather than relying solely on code review to
// catch that.
func TestSession_NeverHasAPasswordField(t *testing.T) {
	typ := reflect.TypeOf(Session{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "password") || strings.Contains(name, "passwd") {
			t.Errorf("Session must never store a password, but found field %q", typ.Field(i).Name)
		}
	}
}
