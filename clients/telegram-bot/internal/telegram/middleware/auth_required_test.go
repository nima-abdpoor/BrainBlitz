package middleware

import (
	"context"
	"errors"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type fakeChecker struct {
	loggedIn bool
	err      error
}

func (f fakeChecker) IsLoggedIn(context.Context, int64) (bool, error) {
	return f.loggedIn, f.err
}

func TestRequireAuth_LoggedIn_CallsNext(t *testing.T) {
	var called bool
	next := func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		called = true
		return tgbotapi.NewMessage(msg.Chat.ID, "ok"), nil
	}

	handler := RequireAuth(fakeChecker{loggedIn: true}, next)
	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}}
	reply, err := handler(context.Background(), msg)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !called {
		t.Error("next was not called for a logged-in chat")
	}
	if reply.Text != "ok" {
		t.Errorf("reply text = %q, want %q", reply.Text, "ok")
	}
}

func TestRequireAuth_NotLoggedIn_BlocksNext(t *testing.T) {
	var called bool
	next := func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		called = true
		return tgbotapi.MessageConfig{}, nil
	}

	handler := RequireAuth(fakeChecker{loggedIn: false}, next)
	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}}
	reply, err := handler(context.Background(), msg)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if called {
		t.Error("next must not be called for a logged-out chat")
	}
	if reply.Text != notLoggedInMessage {
		t.Errorf("reply text = %q, want %q", reply.Text, notLoggedInMessage)
	}
}

func TestRequireAuth_CheckerError_BlocksNextGracefully(t *testing.T) {
	var called bool
	next := func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		called = true
		return tgbotapi.MessageConfig{}, nil
	}

	handler := RequireAuth(fakeChecker{err: errors.New("store down")}, next)
	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}}
	reply, err := handler(context.Background(), msg)
	if err != nil {
		t.Fatalf("handler returned error: %v, want a graceful reply instead", err)
	}
	if called {
		t.Error("next must not be called when the session check itself fails")
	}
	if reply.Text != checkFailedMessage {
		t.Errorf("reply text = %q, want %q", reply.Text, checkFailedMessage)
	}
}

func TestRequireAuthCallback_LoggedIn_CallsNext(t *testing.T) {
	var called bool
	next := func(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
		called = true
		return tgbotapi.NewMessage(cb.Message.Chat.ID, "ok"), nil
	}

	handler := RequireAuthCallback(fakeChecker{loggedIn: true}, next)
	cb := &tgbotapi.CallbackQuery{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}}}
	if _, err := handler(context.Background(), cb); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !called {
		t.Error("next was not called for a logged-in chat")
	}
}

func TestRequireAuthCallback_NotLoggedIn_BlocksNext(t *testing.T) {
	var called bool
	next := func(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
		called = true
		return nil, nil
	}

	handler := RequireAuthCallback(fakeChecker{loggedIn: false}, next)
	cb := &tgbotapi.CallbackQuery{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}}}
	reply, err := handler(context.Background(), cb)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if called {
		t.Error("next must not be called for a logged-out chat")
	}
	msg, ok := reply.(tgbotapi.MessageConfig)
	if !ok || msg.Text != notLoggedInMessage {
		t.Errorf("reply = %+v, want a MessageConfig with text %q", reply, notLoggedInMessage)
	}
}
