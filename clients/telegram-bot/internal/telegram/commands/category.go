package commands

import (
	"context"
	"errors"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// CategoryCallback handles a category button from /play's keyboard (see
// keyboards.Category). internal/telegram/middleware.RequireAuthCallback
// gates this callback (cmd/bot/main.go), which is registered once per
// internal/core/matchmaking.Categories entry since Bot.RegisterCallback
// matches exact data, not a prefix.
func (h *Handlers) CategoryCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID
	category := strings.TrimPrefix(cb.Data, keyboards.CallbackCategoryPrefix)

	err := h.matchmaking.JoinQueue(ctx, chatID, category)
	switch {
	case err == nil:
		reply := tgbotapi.NewMessage(chatID, "Waiting for opponent…")
		reply.ReplyMarkup = keyboards.Waiting()
		return reply, nil
	case errors.Is(err, matchmaking.ErrAlreadyInProgress):
		return tgbotapi.NewMessage(chatID, "You're already looking for a match."), nil
	case errors.Is(err, matchmaking.ErrSessionExpired):
		reply := tgbotapi.NewMessage(chatID, "Your session has expired. Send /start to log in again.")
		reply.ReplyMarkup = keyboards.Auth()
		return reply, nil
	default:
		return tgbotapi.NewMessage(chatID, "Couldn't connect to the game right now. Please try /play again shortly."), nil
	}
}

// CancelCallback handles the Cancel button shown while waiting for an
// opponent (keyboards.Waiting). There is no server-side "leave queue"
// (docs/client/gap-analysis.md G-13): this only stops the bot from acting
// on a future MATCH_CREATED for this chat — the user's Redis waiting-list
// entry is untouched server-side until the 20-minute window lapses, so the
// reply below is deliberately not a bare "Cancelled ✓" (see
// docs/client/phase4-matchmaking-plan.md §6, an approved Phase 4 decision).
func (h *Handlers) CancelCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID

	if err := h.matchmaking.Cancel(chatID); err != nil {
		reply := tgbotapi.NewMessage(chatID, "You're not waiting for a match right now.")
		reply.ReplyMarkup = keyboards.MainMenu()
		return reply, nil
	}

	reply := tgbotapi.NewMessage(chatID,
		"You've stopped waiting. Note: if a match was already forming, the other player may briefly wait on you — this is a known backend limitation.")
	reply.ReplyMarkup = keyboards.MainMenu()
	return reply, nil
}
