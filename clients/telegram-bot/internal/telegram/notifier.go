package telegram

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/game"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
)

// MatchmakingNotifier turns internal/core/matchmaking's asynchronous
// outcomes (match found, failed join, unexpected disconnect) into Telegram
// messages. It implements matchmaking.Notifier structurally — this package
// deliberately does not import internal/core/matchmaking, since Notifier's
// method signatures need nothing from that package (see
// docs/client/client-architecture.md §1: core must not know about
// Telegram, so the dependency only points the other way, wired together in
// cmd/bot/main.go).
//
// These methods fire from a matchmaking.Manager read-loop goroutine, not
// from Bot's own update-driven dispatch, so unlike Bot.send this type talks
// to the Sender directly rather than through the update-handling path.
type MatchmakingNotifier struct {
	api    Sender
	logger *slog.Logger
}

// NewMatchmakingNotifier builds a MatchmakingNotifier. Both dependencies are
// required, injected by the caller.
func NewMatchmakingNotifier(api Sender, logger *slog.Logger) *MatchmakingNotifier {
	return &MatchmakingNotifier{api: api, logger: logger}
}

// NotifyMatchCreated tells chatID an opponent was found. internal/core/game
// takes over immediately after this (see matchmaking.GameHandler), sending
// READY on the chat's behalf — the next message the user sees is either the
// first question (GameNotifier.NotifyQuestion) or, if the opponent never
// readies up, GameNotifier.NotifyReadyTimeout.
func (n *MatchmakingNotifier) NotifyMatchCreated(chatID int64, gameID string) {
	n.send(chatID, tgbotapi.NewMessage(chatID, "🎮 Opponent found! Getting ready…"))
}

// NotifyQueueFailed tells chatID its join attempt was rejected (e.g. the
// documented ERROR-then-false-success quirk for an invalid category).
func (n *MatchmakingNotifier) NotifyQueueFailed(chatID int64, reason string) {
	reply := tgbotapi.NewMessage(chatID, "Couldn't join the queue, try /play again.")
	reply.ReplyMarkup = keyboards.MainMenu()
	n.send(chatID, reply)
}

// NotifyDisconnected tells chatID its connection dropped while waiting.
func (n *MatchmakingNotifier) NotifyDisconnected(chatID int64) {
	reply := tgbotapi.NewMessage(chatID, "Lost connection while waiting for an opponent. Send /play to try again.")
	reply.ReplyMarkup = keyboards.MainMenu()
	n.send(chatID, reply)
}

func (n *MatchmakingNotifier) send(chatID int64, msg tgbotapi.MessageConfig) {
	if _, err := n.api.Send(msg); err != nil {
		n.logger.Error("failed to send matchmaking notification", "chat_id", chatID, "error", err)
	}
}

// GameNotifier turns internal/core/game's gameplay events (questions,
// leaderboard updates, timeouts, final results) into Telegram messages. It
// implements game.Notifier — unlike MatchmakingNotifier, this package does
// import internal/core/game here, since Notifier's methods carry that
// package's domain types (Question, Leaderboard); that's still a
// delivery-layer-depends-on-core dependency, the direction
// docs/client/client-architecture.md §1 expects.
//
// Like MatchmakingNotifier, these methods fire from a
// matchmaking-Connection-owned read-loop goroutine, not from Bot's own
// update-driven dispatch.
type GameNotifier struct {
	api    Sender
	logger *slog.Logger
}

// NewGameNotifier builds a GameNotifier. Both dependencies are required.
func NewGameNotifier(api Sender, logger *slog.Logger) *GameNotifier {
	return &GameNotifier{api: api, logger: logger}
}

// NotifyQuestion presents question with a countdown to its deadline and one
// button per choice.
func (n *GameNotifier) NotifyQuestion(chatID int64, q game.Question, questionNumber, totalQuestions int) {
	seconds := int(time.Until(q.Deadline).Round(time.Second) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	text := fmt.Sprintf("❓ Question %d/%d (%ds to answer):\n\n%s", questionNumber, totalQuestions, seconds, q.Content)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboards.Answer(q.ID, q.Choices)
	n.send(chatID, msg)
}

// NotifyLeaderboard reports an updated leaderboard after an accepted
// answer, with the game still in progress.
func (n *GameNotifier) NotifyLeaderboard(chatID int64, board game.Leaderboard) {
	n.send(chatID, tgbotapi.NewMessage(chatID, formatLeaderboard("📊 Leaderboard", board)))
}

// NotifyReadyTimeout tells chatID its opponent never became ready.
func (n *GameNotifier) NotifyReadyTimeout(chatID int64) {
	reply := tgbotapi.NewMessage(chatID, "Your opponent never became ready. Send /play to look for a new match.")
	reply.ReplyMarkup = keyboards.MainMenu()
	n.send(chatID, reply)
}

// NotifyCompleted reports the final leaderboard; the game is over.
func (n *GameNotifier) NotifyCompleted(chatID int64, board game.Leaderboard) {
	reply := tgbotapi.NewMessage(chatID, formatLeaderboard("🏆 Game over! Final results", board))
	reply.ReplyMarkup = keyboards.MainMenu()
	n.send(chatID, reply)
}

// NotifyDisconnected tells chatID its connection dropped mid-game.
func (n *GameNotifier) NotifyDisconnected(chatID int64) {
	reply := tgbotapi.NewMessage(chatID, "Lost connection during the game. Send /play to start a new match.")
	reply.ReplyMarkup = keyboards.MainMenu()
	n.send(chatID, reply)
}

// formatLeaderboard renders board's entries by player ID — game-service
// never sends a username/displayName alongside a leaderboard entry (see
// docs/client/backend-api-analysis.md), so "Player <id>" is the most this
// can say without a separate profile lookup per opponent, which is out of
// scope for Phase 5.
func formatLeaderboard(title string, board game.Leaderboard) string {
	var b strings.Builder
	b.WriteString(title + ":\n")
	for _, s := range board.Scores {
		fmt.Fprintf(&b, "Player %s: %d pts\n", s.PlayerID, s.Points)
	}
	return b.String()
}

func (n *GameNotifier) send(chatID int64, msg tgbotapi.MessageConfig) {
	if _, err := n.api.Send(msg); err != nil {
		n.logger.Error("failed to send game notification", "chat_id", chatID, "error", err)
	}
}
