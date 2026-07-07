package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// Play replies to /play with a category-selection keyboard.
// internal/telegram/middleware.RequireAuth gates this command (see
// cmd/bot/main.go).
func (h *Handlers) Play(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
	return h.playReply(msg.Chat.ID), nil
}

// PlayCallback is the main-menu "Play" button; see Play.
func (h *Handlers) PlayCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	return h.playReply(cb.Message.Chat.ID), nil
}

func (h *Handlers) playReply(chatID int64) tgbotapi.MessageConfig {
	reply := tgbotapi.NewMessage(chatID, "Choose a category:")
	reply.ReplyMarkup = keyboards.Category()
	return reply
}
