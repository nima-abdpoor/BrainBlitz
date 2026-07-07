// Package matchmaking implements the bot's join-queue business rules: what
// "join a category queue" and "cancel" mean in terms of game-service's
// WebSocket protocol, and the per-chat state machine that guards against
// sending commands out of order (there is no server-side state machine —
// see docs/client/backend-api-analysis.md §4 point 9). It has no knowledge
// of Telegram — see internal/telegram/commands for the delivery-layer
// command and callbacks that call this package.
package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// State is where a chat's matchmaking attempt currently is. This is the
// Phase 4 slice of the full client-side game state machine described in
// docs/client/client-architecture.md §5; Matched is a hand-off point, not a
// dead end — Phase 5 continues using the same open connection from there.
type State int

const (
	Idle State = iota
	Connecting
	Queued
	Cancelling
	Matched
)

// Categories are the only valid values for JoinQueue's category parameter.
// Hardcoded client-side rather than fetched from game-service's first-connect
// push — an approved Phase 4 decision, since GET_CATEGORIES is a documented
// no-op (docs/client/gap-analysis.md G-09) and the push only fires on a
// genuinely first-ever connection anyway (backend-api-analysis.md §4).
var Categories = []string{"SPORT", "MUSIC", "TECH"}

var (
	// ErrAlreadyInProgress is returned by JoinQueue when chatID already has
	// a non-Idle matchmaking attempt underway.
	ErrAlreadyInProgress = errors.New("matchmaking: already in progress")
	// ErrNotInQueue is returned by Cancel when chatID has nothing to cancel.
	ErrNotInQueue = errors.New("matchmaking: not in queue")
	// ErrSessionExpired is returned by JoinQueue when chatID's stored
	// session is too old to be worth dialing with (session.Session.Stale) —
	// there is no token-refresh endpoint (docs/client/gap-analysis.md
	// G-12), so failing fast here gives a clear "please /login again"
	// instead of an opaque WebSocket upgrade rejection.
	ErrSessionExpired = errors.New("matchmaking: session expired")
)

// Connection is the subset of ws.Client this package depends on, defined
// here (the consumer) so tests can substitute a fake with no real socket —
// mirrors internal/core/auth.UserAPI's pattern. *ws.Client satisfies this.
type Connection interface {
	Send(cmd ws.Command) error
	Listen(handler func(ws.Event)) error
	Close() error
}

// Dialer opens a new Connection authenticated as accessToken. A function
// type rather than an interface because there is exactly one operation and
// no state to share between calls; cmd/bot/main.go wires this to ws.Dial
// bound to the configured game-service URL.
type Dialer func(ctx context.Context, accessToken string) (Connection, error)

// Notifier is how Manager reports asynchronous outcomes — a match found, a
// failed join, or an unexpected disconnect — to the delivery layer. It
// fires from the Connection's read-loop goroutine, which is not the same
// goroutine that called JoinQueue or Cancel, so implementations must be
// safe for concurrent use with themselves.
type Notifier interface {
	NotifyMatchCreated(chatID int64, gameID string)
	NotifyQueueFailed(chatID int64, reason string)
	NotifyDisconnected(chatID int64)
}

// GameHandler receives control of a chat's Connection once its match is
// created — internal/core/game implements this. This package keeps running
// the read loop (and disconnect detection) for the connection's whole
// lifetime, including the game that follows a match, per
// docs/client/phase4-matchmaking-plan.md ("Matched is a hand-off point, not
// a dead end"); GameHandler is how control of individual events is handed
// over without a second goroutine trying to read the same socket.
//
// If unset (nil), a matched chat's subsequent events (QUESTIONS_PUBLISHED,
// ANSWER_ACCEPTED, COMPLETED) are simply dropped — this was this package's
// entire behavior before internal/core/game existed.
type GameHandler interface {
	// Attached is called once, synchronously (from the same goroutine
	// processing this chat's events), when MATCH_CREATED transitions
	// chatID to Matched. It returns the function this package should
	// forward every subsequent event for chatID to, until Detach(chatID)
	// is called.
	Attached(chatID int64, gameID string, conn Connection) (onEvent func(ws.Event))
	// Disconnected is called if the connection drops while the game layer
	// still owns chatID (i.e. Detach hasn't been called yet) — instead of
	// Notifier.NotifyDisconnected, so the user gets game-appropriate
	// messaging rather than matchmaking's "lost connection while waiting"
	// text.
	Disconnected(chatID int64)
}

// Metrics is the subset of internal/metrics.Recorder this package depends
// on, defined here (the consumer) so tests don't need a real Prometheus
// registry.
type Metrics interface {
	WSConnectionOpened()
	WSConnectionClosed()
}

// noopMetrics is Manager's default Metrics — SetMetrics is optional.
type noopMetrics struct{}

func (noopMetrics) WSConnectionOpened() {}
func (noopMetrics) WSConnectionClosed() {}

// chatSession is one chat's in-progress matchmaking attempt (or, once
// onEvent is set, an in-progress game handed off to a GameHandler).
type chatSession struct {
	state  State
	conn   Connection
	gameID string
	// pendingJoinError latches an ERROR event received before the
	// ADD_TO_WAITING_LIST response, so the documented contradictory
	// ERROR-then-false-success quirk (backend-api-analysis.md §4 point 1)
	// is caught instead of trusting whichever event is read last.
	pendingJoinError string
	// onEvent is set once by GameHandler.Attached, after which every event
	// this chat's Connection receives is forwarded here instead of being
	// interpreted by this package's own handleEvent switch (see
	// handleEvent's first check).
	onEvent func(ws.Event)
}

// Manager owns one Connection per Telegram chat with an active matchmaking
// attempt (or, after Matched, an in-progress game), and drives the
// join-queue state machine documented in
// docs/client/phase4-matchmaking-plan.md.
type Manager struct {
	dial        Dialer
	notifier    Notifier
	sessions    session.Store
	gameHandler GameHandler
	metrics     Metrics

	mu    sync.Mutex
	chats map[int64]*chatSession
}

// NewManager builds a Manager. dial, notifier, and sessions are required;
// gameHandler may be nil (see GameHandler's doc comment). All are
// interfaces/functions injected by the caller, so Manager never reaches for
// a concrete WS client or storage backend directly. Metrics default to a
// no-op — call SetMetrics to record the active-connections gauge.
func NewManager(dial Dialer, notifier Notifier, sessions session.Store, gameHandler GameHandler) *Manager {
	return &Manager{
		dial:        dial,
		notifier:    notifier,
		sessions:    sessions,
		gameHandler: gameHandler,
		metrics:     noopMetrics{},
		chats:       make(map[int64]*chatSession),
	}
}

// SetMetrics installs m as this Manager's Metrics recorder, replacing the
// default no-op.
func (m *Manager) SetMetrics(metrics Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics = metrics
}

// SetGameHandler installs h as this Manager's GameHandler. Exposed as a
// setter, not just a NewManager parameter, because internal/core/game's own
// constructor needs a Detacher — typically this same *Manager — which
// would otherwise be a construction-order cycle: build Manager (with a nil
// GameHandler), build the game.Manager with this Manager as its Detacher,
// then call SetGameHandler (see cmd/bot/main.go).
func (m *Manager) SetGameHandler(h GameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gameHandler = h
}

// StateFor reports chatID's current matchmaking state (Idle if it has no
// in-progress attempt).
func (m *Manager) StateFor(chatID int64) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	cs, ok := m.chats[chatID]
	if !ok {
		return Idle
	}
	return cs.state
}

// JoinQueue dials game-service and sends ADD_TO_WAITING_LIST for category on
// behalf of chatID's stored session. It returns once the command has been
// sent (Queued) or the attempt failed to even get that far; MATCH_CREATED
// (or a latched join error) arrives later via Notifier, from the read loop
// started here on its own goroutine.
func (m *Manager) JoinQueue(ctx context.Context, chatID int64, category string) error {
	m.mu.Lock()
	if cs, ok := m.chats[chatID]; ok && cs.state != Idle {
		m.mu.Unlock()
		return ErrAlreadyInProgress
	}
	cs := &chatSession{state: Connecting}
	m.chats[chatID] = cs
	m.mu.Unlock()

	sess, err := m.sessions.Get(ctx, chatID)
	if err != nil {
		m.abandon(chatID, cs)
		return fmt.Errorf("loading session: %w", err)
	}
	if sess.Stale() {
		m.abandon(chatID, cs)
		return ErrSessionExpired
	}

	conn, err := m.dial(ctx, sess.AccessToken)
	if err != nil {
		m.abandon(chatID, cs)
		return fmt.Errorf("connecting to game service: %w", err)
	}
	m.getMetrics().WSConnectionOpened()

	if err := conn.Send(ws.Command{Command: ws.CommandAddToWaitingList, Category: category}); err != nil {
		_ = conn.Close()
		m.getMetrics().WSConnectionClosed()
		m.abandon(chatID, cs)
		return fmt.Errorf("joining queue: %w", err)
	}

	m.mu.Lock()
	cs.conn = conn
	cs.state = Queued
	m.mu.Unlock()

	go m.listen(chatID, conn)
	return nil
}

// abandon removes chatID's session if it's still cs (i.e. nothing else —
// like a concurrent Cancel — has already taken it over), used when JoinQueue
// fails before reaching Queued.
func (m *Manager) abandon(chatID int64, cs *chatSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.chats[chatID]; ok && current == cs {
		delete(m.chats, chatID)
	}
}

// listen runs on its own goroutine for the lifetime of one chat's
// Connection, dispatching each event and, when the read loop ends, cleaning
// up and notifying of the disconnect — unless the chat already moved on
// (Cancel, or a fresher JoinQueue replaced this session, though the latter
// can't happen while this one is Queued or Matched; see JoinQueue's guard).
func (m *Manager) listen(chatID int64, conn Connection) {
	err := conn.Listen(func(event ws.Event) {
		m.handleEvent(chatID, event)
	})

	m.mu.Lock()
	cs, ok := m.chats[chatID]
	stillOurs := ok && cs.conn == conn
	var hadGame bool
	if stillOurs {
		hadGame = cs.onEvent != nil
		delete(m.chats, chatID)
	}
	m.mu.Unlock()

	if !stillOurs || err == nil {
		return
	}
	m.getMetrics().WSConnectionClosed()
	// Once the game layer has taken over (hadGame), it gives
	// game-appropriate messaging via GameHandler.Disconnected instead of
	// this package's generic "lost connection while waiting" text.
	if gh := m.getGameHandler(); hadGame && gh != nil {
		gh.Disconnected(chatID)
		return
	}
	m.notifier.NotifyDisconnected(chatID)
}

// getGameHandler returns the currently installed GameHandler, safe for
// concurrent use with SetGameHandler.
func (m *Manager) getGameHandler() GameHandler {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gameHandler
}

// getMetrics returns the currently installed Metrics, safe for concurrent
// use with SetMetrics.
func (m *Manager) getMetrics() Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.metrics
}

func (m *Manager) handleEvent(chatID int64, event ws.Event) {
	m.mu.Lock()
	cs, ok := m.chats[chatID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if cs.onEvent != nil {
		onEvent := cs.onEvent
		m.mu.Unlock()
		onEvent(event)
		return
	}

	switch event.Event {
	case ws.EventError:
		cs.pendingJoinError = event.Message
		m.mu.Unlock()

	case ws.EventAddedToWaitingList:
		joinErr := cs.pendingJoinError
		m.mu.Unlock()
		if joinErr != "" {
			m.failQueue(chatID, cs, joinErr)
		}

	case ws.EventMatchCreated:
		if cs.state != Queued {
			// Cancelling (or already Matched) — drop it. This is the guard
			// that makes Cancel racing MATCH_CREATED safe: see Cancel.
			m.mu.Unlock()
			return
		}
		meta, decodeErr := event.MatchCreated()
		cs.state = Matched
		cs.gameID = meta.GameID
		conn := cs.conn
		m.mu.Unlock()
		if decodeErr != nil {
			return
		}
		m.notifier.NotifyMatchCreated(chatID, meta.GameID)

		if gh := m.getGameHandler(); gh != nil {
			onEvent := gh.Attached(chatID, meta.GameID, conn)
			m.mu.Lock()
			if current, ok := m.chats[chatID]; ok && current == cs {
				cs.onEvent = onEvent
			}
			m.mu.Unlock()
		}

	default:
		m.mu.Unlock()
	}
}

// Detach removes chatID from this package's tracking and closes its
// connection, without treating it as an unexpected disconnect (neither
// Notifier.NotifyDisconnected nor GameHandler.Disconnected fires). The game
// layer calls this once it's done with a chat (e.g. after COMPLETED, whose
// connection game-service closes server-side anyway — see
// docs/client/backend-api-analysis.md §4) so that inevitable close isn't
// mistaken for a mid-game drop.
func (m *Manager) Detach(chatID int64) {
	m.mu.Lock()
	cs, ok := m.chats[chatID]
	if ok {
		delete(m.chats, chatID)
	}
	m.mu.Unlock()

	if ok {
		_ = cs.conn.Close()
		m.getMetrics().WSConnectionClosed()
	}
}

// failQueue tears down a queue attempt rejected by the backend (the
// ERROR-then-false-success quirk) and notifies the delivery layer.
func (m *Manager) failQueue(chatID int64, cs *chatSession, reason string) {
	m.mu.Lock()
	if current, ok := m.chats[chatID]; ok && current == cs {
		delete(m.chats, chatID)
	}
	m.mu.Unlock()

	_ = cs.conn.Close()
	m.getMetrics().WSConnectionClosed()
	m.notifier.NotifyQueueFailed(chatID, reason)
}

// Cancel stops chatID's matchmaking attempt from the bot's perspective.
// There is no server-side "leave queue" (docs/client/gap-analysis.md
// G-13): the user's Redis waiting-list entry is untouched until the
// 20-minute window lapses, so a MATCH_CREATED already in flight the instant
// Cancel is called is only dropped locally (see the Queued-state check in
// handleEvent) — it is not prevented server-side. Callers must convey that
// limitation to the user rather than implying a clean cancellation.
func (m *Manager) Cancel(chatID int64) error {
	m.mu.Lock()
	cs, ok := m.chats[chatID]
	if !ok || cs.state != Queued {
		m.mu.Unlock()
		return ErrNotInQueue
	}
	cs.state = Cancelling
	delete(m.chats, chatID)
	m.mu.Unlock()

	err := cs.conn.Close()
	m.getMetrics().WSConnectionClosed()
	return err
}
