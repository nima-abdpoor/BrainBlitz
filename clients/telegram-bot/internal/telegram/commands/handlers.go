// Package commands is the Telegram delivery layer for the bot's commands,
// inline-keyboard callbacks, and multi-step text conversations. It parses
// Telegram update types, calls into internal/core (auth, and later
// profile/matchmaking/game), and builds Telegram-shaped replies. It holds
// no BrainBlitz business rules of its own — see internal/core/auth for
// what "register" and "login" actually mean.
package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/game"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// genericErrorMessage is shown whenever a failure gives the user no
// actionable detail (transport errors, an unclassified backend response, or
// an unexpected local error) — see docs/client/client-business-flows.md §2.
const genericErrorMessage = "Something went wrong on our end. Please try again in a moment."

// Handlers holds the dependencies shared by every command, callback, and
// text handler in this package. All fields are injected by the caller
// (cmd/bot/main.go) rather than reached for as globals, so a bot process
// can be wired up explicitly and tested with fakes.
type Handlers struct {
	auth          *auth.Service
	profile       *profile.Service
	matchmaking   *matchmaking.Manager
	game          *game.Manager
	conversations conversation.Store
}

// NewHandlers builds a Handlers. All dependencies are required.
func NewHandlers(authSvc *auth.Service, profileSvc *profile.Service, matchmakingMgr *matchmaking.Manager, gameMgr *game.Manager, conversations conversation.Store) *Handlers {
	return &Handlers{auth: authSvc, profile: profileSvc, matchmaking: matchmakingMgr, game: gameMgr, conversations: conversations}
}

// navigationKeyboard picks the keyboard that matches chatID's current auth
// state, so handlers with no other opinion on the matter (Help today) offer
// consistent next steps whether or not the chat is logged in. A
// session-store failure is treated the same as "not logged in" — the
// safer of the two guesses, since the auth keyboard's actions (Register,
// Login) are always valid, while the main menu's (Profile, Logout) require
// a session that may not actually be there.
func (h *Handlers) navigationKeyboard(ctx context.Context, chatID int64) tgbotapi.InlineKeyboardMarkup {
	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil || !loggedIn {
		return keyboards.Auth()
	}
	return keyboards.MainMenu()
}
