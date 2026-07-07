package commands

import (
	"context"
	"errors"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

const (
	promptLoginEmail    = "Let's log you in. What's your email address?"
	promptLoginPassword = "Thanks. Now send your password."
)

// LoginCallback starts the login conversation by prompting for an email;
// the reply is collected by HandleText via the buffered conversation.State.
func (h *Handlers) LoginCallback(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID
	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingLoginEmail})
	return tgbotapi.NewMessage(chatID, promptLoginEmail), nil
}

// handleLoginEmail buffers the email the user just typed and prompts for a
// password next.
func (h *Handlers) handleLoginEmail(chatID int64, email string) tgbotapi.Chattable {
	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingLoginPassword, Email: email})
	return tgbotapi.NewMessage(chatID, promptLoginPassword)
}

// handleLoginPassword completes the login flow: it calls
// internal/core/auth.Login with the buffered email and this password, then
// clears the conversation state regardless of outcome. password is held
// only in this call's stack frame and is never written to the conversation
// or session store.
func (h *Handlers) handleLoginPassword(ctx context.Context, chatID int64, email, password string) tgbotapi.Chattable {
	h.conversations.Clear(chatID)

	_, err := h.auth.Login(ctx, chatID, email, password)
	if err == nil {
		reply := tgbotapi.NewMessage(chatID, "✅ You're logged in! Here's your menu:")
		reply.ReplyMarkup = keyboards.MainMenu()
		return reply
	}

	var authErr *auth.Error
	if !errors.As(err, &authErr) {
		return tgbotapi.NewMessage(chatID, genericErrorMessage)
	}

	// Message text per docs/client/feature-map.md §1 (Callback: login).
	// Per gap-analysis.md G-06, a nonexistent email currently surfaces as
	// KindTransport (backend returns an ambiguous 500), so it necessarily
	// gets the same generic wording as a real server error here — there is
	// no way to tell the two apart from this response alone.
	switch authErr.Kind {
	case auth.KindInvalidCredentials, auth.KindInvalidInput:
		return tgbotapi.NewMessage(chatID, "Invalid email or password. Send /start to try again.")
	default: // KindTransport, KindUnknown
		return tgbotapi.NewMessage(chatID, "Couldn't log you in — check your details or try again shortly.")
	}
}
