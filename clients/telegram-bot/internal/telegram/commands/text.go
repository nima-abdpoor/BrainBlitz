package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
)

// HandleText advances a chat's in-progress registration/login conversation
// with the plain text message it was waiting for. It reports ok=false when
// no such conversation exists, telling Bot the message isn't relevant to
// anything registered.
func (h *Handlers) HandleText(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.Chattable, bool, error) {
	chatID := msg.Chat.ID
	state, ok := h.conversations.Get(chatID)
	if !ok {
		return nil, false, nil
	}

	switch state.Step {
	case conversation.StepAwaitingRegisterEmail:
		return h.handleRegisterEmail(chatID, msg.Text), true, nil
	case conversation.StepAwaitingRegisterPassword:
		return h.handleRegisterPassword(ctx, chatID, state.Email, msg.Text), true, nil
	case conversation.StepAwaitingLoginEmail:
		return h.handleLoginEmail(chatID, msg.Text), true, nil
	case conversation.StepAwaitingLoginPassword:
		return h.handleLoginPassword(ctx, chatID, state.Email, msg.Text), true, nil
	default:
		return nil, false, nil
	}
}
