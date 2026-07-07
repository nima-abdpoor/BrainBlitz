package commands

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/game"
)

// AnswerCallback handles a choice button from the current question's
// keyboard (see keyboards.Answer). Registered via
// telegram.Bot.RegisterCallbackPrefix on keyboards.CallbackAnswerPrefix
// (cmd/bot/main.go), since answer buttons are built per-question and can't
// be pre-registered as exact strings like Category's fixed set.
func (h *Handlers) AnswerCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, data string) (tgbotapi.Chattable, error) {
	chatID := cb.Message.Chat.ID

	questionID, index, ok := parseAnswerData(data)
	if !ok {
		return nil, nil
	}

	err := h.game.SubmitAnswer(chatID, questionID, index)
	switch {
	case err == nil:
		return tgbotapi.NewMessage(chatID, "Answer submitted! Waiting for the result…"), nil
	case errors.Is(err, game.ErrAlreadyAnswered):
		// A duplicate tap on the same question — nothing new to tell the
		// user, and re-sending the same confirmation would be confusing.
		return nil, nil
	case errors.Is(err, game.ErrNoActiveQuestion), errors.Is(err, game.ErrQuestionExpired):
		return tgbotapi.NewMessage(chatID, "That question is no longer active."), nil
	default:
		return tgbotapi.NewMessage(chatID, genericErrorMessage), nil
	}
}

// parseAnswerData splits keyboards.Answer's "<questionID>|<choiceIndex>"
// callback data (prefix already trimmed by Bot's dispatch).
func parseAnswerData(data string) (questionID string, index int, ok bool) {
	q, idxStr, found := strings.Cut(data, "|")
	if !found {
		return "", 0, false
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return "", 0, false
	}
	return q, idx, true
}
