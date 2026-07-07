package commands

import (
	"context"
	"errors"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// Profile replies to /profile with the chat's BrainBlitz account details.
// internal/telegram/middleware.RequireAuth gates this command (see
// cmd/bot/main.go), so by the time it runs chatID is expected to have a
// session — profileReply still handles the rare case where it doesn't.
func (h *Handlers) Profile(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
	return h.profileReply(ctx, msg.Chat.ID), nil
}

// ProfileCallback is the main-menu "Profile" button. It renders through the
// same profileReply as the /profile command so the two entry points can
// never drift out of sync with each other.
func (h *Handlers) ProfileCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	return h.profileReply(ctx, cb.Message.Chat.ID), nil
}

func (h *Handlers) profileReply(ctx context.Context, chatID int64) tgbotapi.MessageConfig {
	p, err := h.profile.Get(ctx, chatID)
	if err == nil {
		text := fmt.Sprintf("👤 %s\nUsername: %s\nRole: %s\nMember since: %s",
			p.DisplayName, p.Username, p.Role, p.CreatedAt.Format("2006-01-02"))
		reply := tgbotapi.NewMessage(chatID, text)
		reply.ReplyMarkup = keyboards.MainMenu()
		return reply
	}

	var profileErr *profile.Error
	if !errors.As(err, &profileErr) {
		// Covers profile.ErrNotLoggedIn (the session vanished between the
		// auth middleware's check and this call) and any other unclassified
		// error — neither has anything more specific to tell the user.
		return tgbotapi.NewMessage(chatID, genericErrorMessage)
	}

	switch profileErr.Kind {
	case profile.KindNotFound:
		// Per docs/client/feature-map.md §2: a 404 here means the account
		// behind this session's token no longer exists (e.g. deleted after
		// login) — force a local logout rather than leaving a session the
		// user can never successfully use again.
		_ = h.auth.Logout(ctx, chatID)
		reply := tgbotapi.NewMessage(chatID,
			"Your account couldn't be found, so you've been logged out. Send /start to register or log in again.")
		reply.ReplyMarkup = keyboards.Auth()
		return reply
	default: // KindTransport, KindUnknown
		return tgbotapi.NewMessage(chatID, "Couldn't load your profile, try again in a moment.")
	}
}
