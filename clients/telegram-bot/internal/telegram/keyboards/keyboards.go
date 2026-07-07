// Package keyboards builds the InlineKeyboardMarkup layouts shared across
// the bot's commands and callbacks, so every entry point that needs, say,
// the main menu renders the exact same buttons rather than each handler
// building its own slightly-different copy.
package keyboards

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Callback data for every button built by this package. Handlers register
// against these same constants (see internal/telegram/commands and
// cmd/bot/main.go), so a renamed button here can't silently drift out of
// sync with what's registered.
const (
	CallbackRegister = "register"
	CallbackLogin    = "login"
	CallbackProfile  = "profile"
	CallbackHelp     = "help"
	CallbackLogout   = "logout"
	CallbackPlay     = "play"
	CallbackCancel   = "cancel_queue"
	// CallbackCategoryPrefix + a category name (e.g. "category:SPORT") is
	// registered once per internal/core/matchmaking.Categories entry in
	// cmd/bot/main.go, since Bot.RegisterCallback matches exact data, not a
	// prefix.
	CallbackCategoryPrefix = "category:"
	// CallbackAnswerPrefix + "<questionID>|<choiceIndex>" is registered via
	// telegram.Bot.RegisterCallbackPrefix (cmd/bot/main.go) — Answer's
	// buttons are generated per question at send-time, so unlike Category's
	// fixed set they can't be pre-registered as exact strings.
	CallbackAnswerPrefix = "answer:"
)

// Auth offers both entry points into the onboarding flow, for a chat with
// no session yet.
func Auth() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Register", CallbackRegister),
			tgbotapi.NewInlineKeyboardButtonData("Login", CallbackLogin),
		),
	)
}

// Login offers only the login entry point, for messages that already know
// the user has an account (e.g. a duplicate-email rejection during
// registration).
func Login() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Login", CallbackLogin)),
	)
}

// MainMenu is the primary navigation surface for a logged-in chat.
func MainMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Play", CallbackPlay),
			tgbotapi.NewInlineKeyboardButtonData("Profile", CallbackProfile),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Help", CallbackHelp),
			tgbotapi.NewInlineKeyboardButtonData("Logout", CallbackLogout),
		),
	)
}

// Category offers the three matchmaking categories, shown after /play.
// Categories are hardcoded client-side rather than fetched from
// game-service's first-connect push — an approved Phase 4 decision, since
// GET_CATEGORIES is a documented no-op server-side
// (docs/client/gap-analysis.md G-09). Button labels are Title-cased for
// display; callback data carries the exact wire value
// internal/core/matchmaking.Categories expects.
func Category() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Sport", CallbackCategoryPrefix+"SPORT"),
			tgbotapi.NewInlineKeyboardButtonData("Music", CallbackCategoryPrefix+"MUSIC"),
			tgbotapi.NewInlineKeyboardButtonData("Tech", CallbackCategoryPrefix+"TECH"),
		),
	)
}

// Waiting offers only a Cancel button, shown while queued for a match.
// There is no server-side "leave queue" (docs/client/gap-analysis.md
// G-13) — Cancel only stops the bot from acting on this chat's queue
// attempt locally; see internal/core/matchmaking.Manager.Cancel.
func Waiting() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Cancel", CallbackCancel)),
	)
}

// Answer offers one button per choice for the currently active question.
// Callback data encodes the question ID alongside the choice's index (e.g.
// "answer:3f2c...|1") so a stale button from an already-passed question
// (its message isn't retracted, just superseded by a newer one) is rejected
// by internal/core/game.Manager.SubmitAnswer instead of being silently
// misapplied to whatever question is active now.
func Answer(questionID string, choices []string) tgbotapi.InlineKeyboardMarkup {
	row := make([]tgbotapi.InlineKeyboardButton, len(choices))
	for i, choice := range choices {
		data := fmt.Sprintf("%s%s|%d", CallbackAnswerPrefix, questionID, i)
		row[i] = tgbotapi.NewInlineKeyboardButtonData(choice, data)
	}
	return tgbotapi.NewInlineKeyboardMarkup(row)
}
