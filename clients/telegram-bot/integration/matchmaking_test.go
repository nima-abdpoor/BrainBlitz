//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	apihttp "github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/http"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// fakeNotifier records matchmaking outcomes so the test can wait on them,
// mirroring internal/core/matchmaking's own test fakes.
type fakeNotifier struct {
	mu           sync.Mutex
	matchCreated map[int64]string
	queueFailed  map[int64]string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{matchCreated: map[int64]string{}, queueFailed: map[int64]string{}}
}

func (f *fakeNotifier) NotifyMatchCreated(chatID int64, gameID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.matchCreated[chatID] = gameID
}

func (f *fakeNotifier) NotifyQueueFailed(chatID int64, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueFailed[chatID] = reason
}

func (f *fakeNotifier) NotifyDisconnected(int64) {}

func (f *fakeNotifier) matchFor(chatID int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gameID, ok := f.matchCreated[chatID]
	return gameID, ok
}

func (f *fakeNotifier) failureFor(chatID int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reason, ok := f.queueFailed[chatID]
	return reason, ok
}

// registerAndLogin signs up a brand-new account for chatID and persists its
// session in sessions, so matchmaking.Manager.JoinQueue can dial with it.
func registerAndLogin(ctx context.Context, t *testing.T, sessions session.Store, chatID int64, label string) {
	t.Helper()
	userClient := apihttp.NewUserClient(apihttp.NewClient(userServiceURL(), 10*time.Second, discardLogger()))
	authSvc := auth.NewService(userClient, sessions)

	email := uniqueEmail(label)
	if _, err := authSvc.Register(ctx, chatID, email, integrationPassword); err != nil {
		t.Fatalf("Register(%q) = %v, want success", email, err)
	}
}

// TestIntegration_TwoPlayersMatch drives the same join-queue path
// internal/telegram/commands.PlayCallback does, for two independent chats
// against the real game-service, and asserts both land in the same match —
// the one behavior unit tests (which fake the Dialer) can't catch: a wire
// format or matching-logic mismatch with the actual backend.
func TestIntegration_TwoPlayersMatch(t *testing.T) {
	requireBackend(t, userServiceURL())

	sessions := session.NewMemoryStore()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const chatA, chatB = int64(101), int64(102)
	registerAndLogin(ctx, t, sessions, chatA, "match-a")
	registerAndLogin(ctx, t, sessions, chatB, "match-b")

	dialer := func(ctx context.Context, accessToken string) (matchmaking.Connection, error) {
		return ws.Dial(ctx, gameServiceWSURL(), accessToken)
	}
	notifier := newFakeNotifier()
	mgr := matchmaking.NewManager(dialer, notifier, sessions, nil)

	const category = "SPORT"
	if err := mgr.JoinQueue(ctx, chatA, category); err != nil {
		t.Fatalf("JoinQueue(chatA) = %v, want success", err)
	}
	if err := mgr.JoinQueue(ctx, chatB, category); err != nil {
		t.Fatalf("JoinQueue(chatB) = %v, want success", err)
	}

	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	var gameA, gameB string
	for gameA == "" || gameB == "" {
		select {
		case <-deadline:
			if reason, ok := notifier.failureFor(chatA); ok {
				t.Fatalf("chatA queue failed: %s", reason)
			}
			if reason, ok := notifier.failureFor(chatB); ok {
				t.Fatalf("chatB queue failed: %s", reason)
			}
			t.Fatalf("timed out waiting for both players to match (gameA=%q gameB=%q)", gameA, gameB)
		case <-tick.C:
			gameA, _ = notifier.matchFor(chatA)
			gameB, _ = notifier.matchFor(chatB)
		}
	}

	if gameA != gameB {
		t.Errorf("chatA matched into game %q, chatB into %q, want the same game", gameA, gameB)
	}
	if got := mgr.StateFor(chatA); got != matchmaking.Matched {
		t.Errorf("chatA StateFor = %v, want Matched", got)
	}
}
