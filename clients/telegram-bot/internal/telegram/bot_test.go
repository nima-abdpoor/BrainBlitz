package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// fakeSender records every Chattable passed to Send so tests can assert on
// what the Bot tried to send without making a real Telegram API call.
type fakeSender struct {
	sent    []tgbotapi.Chattable
	sendErr error
}

func (f *fakeSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if f.sendErr != nil {
		return tgbotapi.Message{}, f.sendErr
	}
	f.sent = append(f.sent, c)
	return tgbotapi.Message{}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func commandMessage(chatID int64, command string) *tgbotapi.Message {
	text := "/" + command
	return &tgbotapi.Message{
		Text: text,
		Chat: &tgbotapi.Chat{ID: chatID},
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: len(text)},
		},
	}
}

func TestBot_DispatchesRegisteredCommand(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())

	var received *tgbotapi.Message
	bot.RegisterCommand("start", func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		received = msg
		return tgbotapi.NewMessage(msg.Chat.ID, "hello"), nil
	})

	msg := commandMessage(42, "start")
	bot.handleUpdate(context.Background(), tgbotapi.Update{Message: msg})

	if received != msg {
		t.Fatal("handler was not invoked with the update's message")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	got, ok := sender.sent[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("sent message has type %T, want tgbotapi.MessageConfig", sender.sent[0])
	}
	if got.Text != "hello" {
		t.Errorf("sent text = %q, want %q", got.Text, "hello")
	}
}

func TestBot_IgnoresUnrecognizedCommand(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())

	bot.handleUpdate(context.Background(), tgbotapi.Update{Message: commandMessage(1, "unknown")})

	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages for an unregistered command, want 0", len(sender.sent))
	}
}

func TestBot_IgnoresNonCommandMessages(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	bot.RegisterCommand("start", func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		t.Fatal("handler should not be called for a non-command message")
		return tgbotapi.MessageConfig{}, nil
	})

	plain := &tgbotapi.Message{Text: "just chatting", Chat: &tgbotapi.Chat{ID: 1}}
	bot.handleUpdate(context.Background(), tgbotapi.Update{Message: plain})

	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages for a non-command message, want 0", len(sender.sent))
	}
}

func TestBot_HandlerErrorDoesNotSend(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	bot.RegisterCommand("start", func(_ context.Context, _ *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		return tgbotapi.MessageConfig{}, errors.New("boom")
	})

	bot.handleUpdate(context.Background(), tgbotapi.Update{Message: commandMessage(1, "start")})

	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages after a handler error, want 0", len(sender.sent))
	}
}

func TestBot_SendFailureDoesNotPanic(t *testing.T) {
	sender := &fakeSender{sendErr: errors.New("network down")}
	bot := New(sender, testLogger())
	bot.RegisterCommand("start", func(_ context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		return tgbotapi.NewMessage(msg.Chat.ID, "hi"), nil
	})

	bot.handleUpdate(context.Background(), tgbotapi.Update{Message: commandMessage(1, "start")})
}

func callbackUpdate(chatID int64, data string) tgbotapi.Update {
	return tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		ID:      "cb1",
		Data:    data,
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}},
	}}
}

func TestBot_DispatchesExactCallback(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	bot.RegisterCallback("play", func(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
		return tgbotapi.NewMessage(cb.Message.Chat.ID, "playing"), nil
	})

	bot.handleUpdate(context.Background(), callbackUpdate(1, "play"))

	if len(sender.sent) != 2 { // the callback-query ack, then the reply
		t.Fatalf("sent %d messages, want 2 (ack + reply)", len(sender.sent))
	}
	got, ok := sender.sent[1].(tgbotapi.MessageConfig)
	if !ok || got.Text != "playing" {
		t.Errorf("reply = %+v, want text %q", sender.sent[1], "playing")
	}
}

func TestBot_DispatchesPrefixCallback_WithTrimmedData(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	var gotData string
	bot.RegisterCallbackPrefix("answer:", func(_ context.Context, cb *tgbotapi.CallbackQuery, data string) (tgbotapi.Chattable, error) {
		gotData = data
		return tgbotapi.NewMessage(cb.Message.Chat.ID, "answered"), nil
	})

	bot.handleUpdate(context.Background(), callbackUpdate(1, "answer:q1|2"))

	if gotData != "q1|2" {
		t.Errorf("prefix handler data = %q, want %q (prefix trimmed)", gotData, "q1|2")
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent %d messages, want 2 (ack + reply)", len(sender.sent))
	}
}

func TestBot_ExactCallbackTakesPriorityOverPrefix(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	bot.RegisterCallback("answer:all", func(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
		return tgbotapi.NewMessage(cb.Message.Chat.ID, "exact"), nil
	})
	bot.RegisterCallbackPrefix("answer:", func(_ context.Context, cb *tgbotapi.CallbackQuery, data string) (tgbotapi.Chattable, error) {
		t.Fatal("prefix handler should not run when an exact match exists")
		return nil, nil
	})

	bot.handleUpdate(context.Background(), callbackUpdate(1, "answer:all"))

	got, ok := sender.sent[len(sender.sent)-1].(tgbotapi.MessageConfig)
	if !ok || got.Text != "exact" {
		t.Errorf("reply = %+v, want the exact-match handler's reply", sender.sent[len(sender.sent)-1])
	}
}

func TestBot_IgnoresUnrecognizedCallbackData(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())
	bot.RegisterCallbackPrefix("answer:", func(context.Context, *tgbotapi.CallbackQuery, string) (tgbotapi.Chattable, error) {
		t.Fatal("handler should not run for non-matching data")
		return nil, nil
	})

	bot.handleUpdate(context.Background(), callbackUpdate(1, "unrelated"))

	if len(sender.sent) != 1 { // only the callback-query ack
		t.Errorf("sent %d messages for unrecognized callback data, want 1 (ack only)", len(sender.sent))
	}
}

func TestBot_RunStopsOnContextCancellation(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())

	updates := make(chan tgbotapi.Update)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		bot.Run(ctx, updates)
		close(done)
	}()

	<-done // Run must return promptly once ctx is already cancelled.
}

func TestBot_RunStopsOnClosedChannel(t *testing.T) {
	sender := &fakeSender{}
	bot := New(sender, testLogger())

	updates := make(chan tgbotapi.Update)
	close(updates)

	done := make(chan struct{})
	go func() {
		bot.Run(context.Background(), updates)
		close(done)
	}()

	<-done // Run must return once the updates channel is closed.
}
