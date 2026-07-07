package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// helpText is static, per docs/client/feature-map.md §5 — it only describes
// what the bot can actually do today; matchmaking/gameplay commands aren't
// listed since they don't exist yet (see docs/client/client-roadmap.md,
// Phases 4-5).
const helpText = "🧠 BrainBlitz Help\n\n" +
	"Available commands:\n" +
	"/start — greet the bot, then register or log in\n" +
	"/profile — view your BrainBlitz account details\n" +
	"/help — show this message\n\n" +
	"Matchmaking and gameplay are coming in a future update."

// Help replies to /help. Unlike Profile, this has no auth requirement — the
// command works the same whether or not the chat is logged in.
func (h *Handlers) Help(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error) {
	return h.helpReply(ctx, msg.Chat.ID), nil
}

// HelpCallback is the main-menu "Help" button; see Help.
func (h *Handlers) HelpCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
	return h.helpReply(ctx, cb.Message.Chat.ID), nil
}

func (h *Handlers) helpReply(ctx context.Context, chatID int64) tgbotapi.MessageConfig {
	reply := tgbotapi.NewMessage(chatID, helpText)
	reply.ReplyMarkup = h.navigationKeyboard(ctx, chatID)
	return reply
}
