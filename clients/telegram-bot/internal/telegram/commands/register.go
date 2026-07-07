package commands

import (
	"context"
	"errors"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

const (
	promptRegisterEmail    = "Let's get you registered. What's your email address?"
	promptRegisterPassword = "Thanks. Now send your password.\n\n" +
		"⚠️ Telegram doesn't mask typed text or hide it from chat history, " +
		"so avoid reusing a password you rely on elsewhere."
)

// RegisterCallback starts the registration conversation by prompting for an
// email; the reply is collected by HandleText via the buffered
// conversation.State.
func (h *Handlers) RegisterCallback(_ context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID
	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingRegisterEmail})
	return tgbotapi.NewMessage(chatID, promptRegisterEmail), nil
}

// handleRegisterEmail buffers the email the user just typed and prompts
// for a password next.
func (h *Handlers) handleRegisterEmail(chatID int64, email string) tgbotapi.Chattable {
	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingRegisterPassword, Email: email})
	return tgbotapi.NewMessage(chatID, promptRegisterPassword)
}

// handleRegisterPassword completes the registration flow: it calls
// internal/core/auth.Register with the buffered email and this password,
// then clears the conversation state regardless of outcome. password is
// held only in this call's stack frame and is never written to the
// conversation or session store.
func (h *Handlers) handleRegisterPassword(ctx context.Context, chatID int64, email, password string) tgbotapi.Chattable {
	h.conversations.Clear(chatID)

	result, err := h.auth.Register(ctx, chatID, email, password)
	if err == nil {
		reply := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"🎉 Registered and logged in as %s! Here's your menu:", result.DisplayName))
		reply.ReplyMarkup = keyboards.MainMenu()
		return reply
	}

	var authErr *auth.Error
	if !errors.As(err, &authErr) {
		return tgbotapi.NewMessage(chatID, genericErrorMessage)
	}

	// Message text per docs/client/feature-map.md §1 (Callback: register).
	switch authErr.Kind {
	case auth.KindDuplicateEmail:
		reply := tgbotapi.NewMessage(chatID, "Already registered — try Login instead.")
		reply.ReplyMarkup = keyboards.Login()
		return reply
	case auth.KindInvalidInput:
		return tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"That wasn't accepted: %s. Send /start to try registering again.", authErr.Message))
	default: // KindTransport, KindUnknown
		return tgbotapi.NewMessage(chatID, genericErrorMessage)
	}
}
