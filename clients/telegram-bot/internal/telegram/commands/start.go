package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

const (
	welcomeMessage = "Welcome to BrainBlitz! I'm your quiz game companion.\n\n" +
		"Register a new account or log in to an existing one to get started."
	welcomeBackMessage = "Welcome back! Here's your menu:"
)

// Start replies to /start. A fresh /start abandons any registration/login
// conversation already in progress for this chat, since it's the user's
// explicit signal to restart. A chat with a stored session is greeted
// directly; otherwise the bot offers Register/Login buttons.
func (h *Handlers) Start(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
	chatID := msg.Chat.ID
	h.conversations.Clear(chatID)

	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil {
		// A session-store failure isn't the user's fault and isn't
		// actionable by them; still greet them rather than going silent.
		return tgbotapi.NewMessage(chatID, genericErrorMessage), nil
	}
	if loggedIn {
		reply := tgbotapi.NewMessage(chatID, welcomeBackMessage)
		reply.ReplyMarkup = keyboards.MainMenu()
		return reply, nil
	}

	reply := tgbotapi.NewMessage(chatID, welcomeMessage)
	reply.ReplyMarkup = keyboards.Auth()
	return reply, nil
}
