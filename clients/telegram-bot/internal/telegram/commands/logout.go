package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// LogoutCallback is the main-menu "Logout" button. internal/telegram/middleware.RequireAuthCallback
// gates this callback (see cmd/bot/main.go), so by the time it runs the
// chat is expected to have a session to remove. There is no server-side
// revocation endpoint — JWTs are stateless (see
// docs/client/feature-map.md §2) — so auth.Service.Logout only forgets the
// locally stored token.
func (h *Handlers) LogoutCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID
	if err := h.auth.Logout(ctx, chatID); err != nil {
		return tgbotapi.NewMessage(chatID, genericErrorMessage), nil
	}

	reply := tgbotapi.NewMessage(chatID, "You've been logged out. Send /start to log back in.")
	reply.ReplyMarkup = keyboards.Auth()
	return reply, nil
}
