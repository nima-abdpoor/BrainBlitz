package commands

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/game"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// noopNotifier is a matchmaking.Notifier that does nothing — matchmaking
// itself isn't exercised by the tests in this file, which only need a
// Handlers with a valid (if unused) matchmaking.Manager.
type noopNotifier struct{}

func (noopNotifier) NotifyMatchCreated(int64, string) {}
func (noopNotifier) NotifyQueueFailed(int64, string)  {}
func (noopNotifier) NotifyDisconnected(int64)         {}

func newTestMatchmakingManager(sessions *session.MemoryStore) *matchmaking.Manager {
	dial := func(context.Context, string) (matchmaking.Connection, error) {
		return nil, errors.New("not used in this test")
	}
	return matchmaking.NewManager(dial, noopNotifier{}, sessions, nil)
}

// noopGameNotifier is a game.Notifier that does nothing — game logic isn't
// exercised by the tests in this file, which only need a Handlers with a
// valid (if unused) game.Manager.
type noopGameNotifier struct{}

func (noopGameNotifier) NotifyQuestion(int64, game.Question, int, int) {}
func (noopGameNotifier) NotifyLeaderboard(int64, game.Leaderboard)     {}
func (noopGameNotifier) NotifyReadyTimeout(int64)                      {}
func (noopGameNotifier) NotifyCompleted(int64, game.Leaderboard)       {}
func (noopGameNotifier) NotifyDisconnected(int64)                      {}

// noopDetacher is a game.Detacher that does nothing.
type noopDetacher struct{}

func (noopDetacher) Detach(int64) {}

func newTestGameManager() *game.Manager {
	return game.NewManager(noopGameNotifier{}, noopDetacher{}, testLogger())
}

// fakeUserAPI is a minimal auth.UserAPI double: SignUp always fails (not
// exercised by the tests in this file), Login succeeds iff loginOK is set.
type fakeUserAPI struct {
	loginOK bool
}

func (fakeUserAPI) SignUp(context.Context, string, string) (auth.SignUpResult, error) {
	return auth.SignUpResult{}, errors.New("not used in this test")
}

func (f fakeUserAPI) Login(context.Context, string, string) (auth.LoginResult, error) {
	if !f.loginOK {
		return auth.LoginResult{}, errors.New("not used in this test")
	}
	return auth.LoginResult{UserID: "u", AccessToken: "at", RefreshToken: "rt"}, nil
}

// fakeProfileAPI is a minimal profile.UserAPI double; unused by the tests in
// this file, which only exercise Start.
type fakeProfileAPI struct{}

func (fakeProfileAPI) Profile(context.Context, string) (profile.Profile, error) {
	return profile.Profile{}, errors.New("not used in this test")
}

func newTestHandlers(loginOK bool) *Handlers {
	sessions := session.NewMemoryStore()
	authSvc := auth.NewService(fakeUserAPI{loginOK: loginOK}, sessions)
	profileSvc := profile.NewService(fakeProfileAPI{}, sessions)
	return NewHandlers(authSvc, profileSvc, newTestMatchmakingManager(sessions), newTestGameManager(), conversation.NewMemoryStore())
}

func TestStart_NotLoggedIn(t *testing.T) {
	h := newTestHandlers(false)
	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 99}}

	reply, err := h.Start(context.Background(), msg)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if reply.ChatID != 99 {
		t.Errorf("ChatID = %d, want %d", reply.ChatID, 99)
	}
	if reply.Text == "" {
		t.Error("Text is empty, want a greeting")
	}
	if reply.ReplyMarkup == nil {
		t.Error("ReplyMarkup is nil, want Register/Login buttons for a logged-out chat")
	}
}

func TestStart_AlreadyLoggedIn(t *testing.T) {
	h := newTestHandlers(true)
	ctx := context.Background()
	chatID := int64(100)
	if _, err := h.auth.Login(ctx, chatID, "alice@example.com", "hunter2"); err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}}
	reply, err := h.Start(ctx, msg)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if reply.ChatID != chatID {
		t.Errorf("ChatID = %d, want %d", reply.ChatID, chatID)
	}
	if reply.ReplyMarkup == nil {
		t.Error("ReplyMarkup is nil, want the main menu for an already-logged-in chat")
	}
}

func TestStart_ClearsInProgressConversation(t *testing.T) {
	h := newTestHandlers(false)
	chatID := int64(101)
	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingRegisterPassword, Email: "alice@example.com"})

	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}}
	if _, err := h.Start(context.Background(), msg); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if _, ok := h.conversations.Get(chatID); ok {
		t.Error("Start must clear any in-progress registration/login conversation")
	}
}
