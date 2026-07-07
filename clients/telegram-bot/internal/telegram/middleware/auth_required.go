// Package middleware provides cross-cutting checks that wrap Telegram
// command and callback handlers before they run — currently just the "must
// be logged in" gate. It sits between internal/telegram (which defines the
// handler function types) and cmd/bot/main.go (which wires concrete
// handlers from internal/telegram/commands through it), so an individual
// handler's body never needs to check auth itself.
package middleware

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram"
)

// SessionChecker is the subset of internal/core/auth.Service that
// RequireAuth and RequireAuthCallback depend on — just enough to ask "does
// this chat have a valid session?" without pulling in the whole auth
// package. *auth.Service satisfies this.
type SessionChecker interface {
	IsLoggedIn(ctx context.Context, chatID int64) (bool, error)
}

const (
	// notLoggedInMessage is shown when a gated command/callback is used by
	// a chat with no session.
	notLoggedInMessage = "You need to be logged in for that. Send /start to register or log in."
	// checkFailedMessage is shown when the session check itself fails
	// (e.g. the session store errored) — not the user's fault, so it isn't
	// told "log in," just to retry.
	checkFailedMessage = "Something went wrong on our end. Please try again in a moment."
)

// RequireAuth wraps a CommandHandler so next only runs for a chat with a
// valid session; otherwise it replies with notLoggedInMessage (or
// checkFailedMessage, if the check itself errored) instead of invoking next.
func RequireAuth(checker SessionChecker, next telegram.CommandHandler) telegram.CommandHandler {
	return func(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
		chatID := msg.Chat.ID
		loggedIn, err := checker.IsLoggedIn(ctx, chatID)
		if err != nil {
			return tgbotapi.NewMessage(chatID, checkFailedMessage), nil
		}
		if !loggedIn {
			return tgbotapi.NewMessage(chatID, notLoggedInMessage), nil
		}
		return next(ctx, msg)
	}
}

// RequireAuthCallback is RequireAuth for a CallbackHandler — see RequireAuth.
func RequireAuthCallback(checker SessionChecker, next telegram.CallbackHandler) telegram.CallbackHandler {
	return func(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
		chatID := cb.Message.Chat.ID
		loggedIn, err := checker.IsLoggedIn(ctx, chatID)
		if err != nil {
			return tgbotapi.NewMessage(chatID, checkFailedMessage), nil
		}
		if !loggedIn {
			return tgbotapi.NewMessage(chatID, notLoggedInMessage), nil
		}
		return next(ctx, cb)
	}
}
