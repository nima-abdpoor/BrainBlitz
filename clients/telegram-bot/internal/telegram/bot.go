// Package telegram is the Telegram delivery adapter: the only part of this
// codebase that imports the Telegram Bot API SDK. It is responsible for
// dispatching incoming updates (commands, inline-keyboard callbacks, and
// plain text replies used by multi-step conversations) to registered
// handlers, and sending their replies. It intentionally contains no
// BrainBlitz business logic — that lives in internal/core — so a future
// non-Telegram client can be added without touching this package.
package telegram

import (
	"context"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Sender is the subset of *tgbotapi.BotAPI this package depends on. Depending
// on an interface rather than the concrete SDK type lets tests exercise Bot's
// dispatch logic with a fake, without making real Telegram API calls.
type Sender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Metrics is the subset of internal/metrics.Recorder this package depends
// on, defined here (the consumer) so tests don't need a real Prometheus
// registry — mirrors this codebase's established pattern (e.g.
// internal/core/auth.UserAPI). *metrics.Recorder satisfies this.
type Metrics interface {
	CommandHandled(command string)
	CallbackHandled(data string)
}

// noopMetrics is Bot's default Metrics — SetMetrics is optional, and every
// call site should be able to assume b.metrics is never nil rather than
// checking on every dispatch.
type noopMetrics struct{}

func (noopMetrics) CommandHandled(string)  {}
func (noopMetrics) CallbackHandled(string) {}

// CommandHandler builds a reply for a recognized command message. It
// receives the raw *tgbotapi.Message because, unlike internal/core, this
// package is explicitly allowed to know about Telegram's wire types — the
// architectural boundary is at internal/core and internal/apiclient, not
// inside the telegram package itself.
type CommandHandler func(ctx context.Context, msg *tgbotapi.Message) (tgbotapi.MessageConfig, error)

// CallbackHandler builds a reply for an inline-keyboard button press. Bot
// always answers the callback query itself (clearing Telegram's loading
// spinner on the button) before sending the handler's reply, so individual
// handlers don't need to remember to do that.
type CallbackHandler func(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error)

// CallbackPrefixHandler builds a reply for an inline-keyboard button whose
// data is generated dynamically at send-time (e.g. one per question ID —
// see internal/telegram/keyboards.Answer) rather than known ahead of
// registration. data is cb.Data with the registered prefix already
// trimmed.
type CallbackPrefixHandler func(ctx context.Context, cb *tgbotapi.CallbackQuery, data string) (tgbotapi.Chattable, error)

type callbackPrefixEntry struct {
	prefix  string
	handler CallbackPrefixHandler
}

// TextHandler processes a plain (non-command) text message. ok=false tells
// Bot no reply is needed — e.g. no conversation is currently in progress
// for this chat, so the message isn't relevant to anything registered.
type TextHandler func(ctx context.Context, msg *tgbotapi.Message) (reply tgbotapi.Chattable, ok bool, err error)

// Bot dispatches Telegram updates to registered handlers.
type Bot struct {
	api              Sender
	logger           *slog.Logger
	metrics          Metrics
	commands         map[string]CommandHandler
	callbacks        map[string]CallbackHandler
	callbackPrefixes []callbackPrefixEntry
	textHandler      TextHandler
}

// New builds a Bot. api and logger are required dependencies, injected by
// the caller (cmd/bot/main.go) rather than constructed internally, so Bot
// itself never reaches for global state. Metrics default to a no-op — call
// SetMetrics to record command/callback counts.
func New(api Sender, logger *slog.Logger) *Bot {
	return &Bot{
		api:       api,
		logger:    logger,
		metrics:   noopMetrics{},
		commands:  make(map[string]CommandHandler),
		callbacks: make(map[string]CallbackHandler),
	}
}

// SetMetrics installs m as this Bot's Metrics recorder, replacing the
// default no-op. Exposed as a setter (like matchmaking.Manager's
// SetGameHandler) so tests and simple callers can ignore metrics entirely.
func (b *Bot) SetMetrics(m Metrics) {
	b.metrics = m
}

// RegisterCommand associates a command name (without the leading "/") with
// its handler. Registering the same name twice replaces the handler.
func (b *Bot) RegisterCommand(name string, handler CommandHandler) {
	b.commands[name] = handler
}

// RegisterCallback associates exact inline-keyboard callback data with a
// handler. Registering the same data twice replaces the handler.
func (b *Bot) RegisterCallback(data string, handler CallbackHandler) {
	b.callbacks[data] = handler
}

// RegisterCallbackPrefix associates every callback whose Data starts with
// prefix with handler. It's checked only when no exact RegisterCallback
// match is found, in registration order (first match wins) — use this only
// for buttons whose data can't be enumerated ahead of time (e.g. one per
// question ID); prefer RegisterCallback for a small fixed set of values.
func (b *Bot) RegisterCallbackPrefix(prefix string, handler CallbackPrefixHandler) {
	b.callbackPrefixes = append(b.callbackPrefixes, callbackPrefixEntry{prefix: prefix, handler: handler})
}

// SetTextHandler installs the handler consulted for plain text messages.
// Only one is supported: today only the auth conversation flow needs
// free-text input, and it owns deciding (via its own conversation state)
// whether a given message is relevant to it. If a second, independent
// free-text consumer appears in a later phase, this can become a slice
// tried in order.
func (b *Bot) SetTextHandler(handler TextHandler) {
	b.textHandler = handler
}

// Run consumes updates until ctx is cancelled or the channel is closed,
// dispatching each update to its registered handler.
func (b *Bot) Run(ctx context.Context, updates tgbotapi.UpdatesChannel) {
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			b.handleUpdate(ctx, update)
		}
	}
}

// handleUpdate routes a single update by kind. Exactly one of
// update.CallbackQuery or update.Message is set for the update types this
// bot cares about; anything else (e.g. a channel post) is silently ignored.
func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	switch {
	case update.CallbackQuery != nil:
		b.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil && update.Message.IsCommand():
		b.handleCommand(ctx, update.Message)
	case update.Message != nil:
		b.handleText(ctx, update.Message)
	}
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	command := msg.Command()
	handler, ok := b.commands[command]
	if !ok {
		b.logger.Debug("unrecognized command", "command", command, "chat_id", msg.Chat.ID)
		return
	}
	b.metrics.CommandHandled(command)

	reply, err := handler(ctx, msg)
	if err != nil {
		b.logger.Error("command handler failed", "command", command, "chat_id", msg.Chat.ID, "error", err)
		return
	}
	b.send(reply, "chat_id", msg.Chat.ID, "command", command)
}

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if _, err := b.api.Send(tgbotapi.NewCallback(cb.ID, "")); err != nil {
		b.logger.Warn("failed to acknowledge callback query", "callback_id", cb.ID, "error", err)
	}

	if handler, ok := b.callbacks[cb.Data]; ok {
		b.metrics.CallbackHandled(cb.Data)
		b.dispatchCallback(ctx, cb, handler)
		return
	}

	for _, p := range b.callbackPrefixes {
		if data, ok := strings.CutPrefix(cb.Data, p.prefix); ok {
			// Record the prefix, not the full data (e.g. "answer:", never
			// "answer:<questionID>|<index>") — the latter would mint a new,
			// never-reused Prometheus time series per question.
			b.metrics.CallbackHandled(p.prefix)
			prefixHandler := p.handler
			b.dispatchCallback(ctx, cb, func(ctx context.Context, cb *tgbotapi.CallbackQuery) (tgbotapi.Chattable, error) {
				return prefixHandler(ctx, cb, data)
			})
			return
		}
	}

	b.logger.Debug("unrecognized callback data", "data", cb.Data)
}

func (b *Bot) dispatchCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, handler CallbackHandler) {
	reply, err := handler(ctx, cb)
	if err != nil {
		b.logger.Error("callback handler failed", "data", cb.Data, "error", err)
		return
	}
	b.send(reply, "callback_data", cb.Data)
}

func (b *Bot) handleText(ctx context.Context, msg *tgbotapi.Message) {
	if b.textHandler == nil {
		return
	}

	reply, ok, err := b.textHandler(ctx, msg)
	if err != nil {
		b.logger.Error("text handler failed", "chat_id", msg.Chat.ID, "error", err)
		return
	}
	if !ok {
		return
	}
	b.send(reply, "chat_id", msg.Chat.ID)
}

// send delivers reply if non-nil, logging (rather than propagating) a send
// failure so one bad update never brings down the update loop.
func (b *Bot) send(reply tgbotapi.Chattable, logArgs ...any) {
	if reply == nil {
		return
	}
	if _, err := b.api.Send(reply); err != nil {
		b.logger.Error("failed to send reply", append(logArgs, "error", err)...)
	}
}
