package game

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
)

// readyTimeout is how long this bot waits for QUESTIONS_PUBLISHED after
// sending READY before giving up on the match. The backend enforces no
// timeout of its own — READY before all players are ready produces no
// response at all (docs/client/backend-api-analysis.md §4 point 2) — so
// without this, a non-responsive opponent would leave the chat waiting
// forever. 90s is the upper end of the 60-90s window recommended in
// docs/client/client-business-flows.md §5.
const readyTimeout = 90 * time.Second

var (
	// ErrNoActiveQuestion is returned by SubmitAnswer when chatID has no
	// game in the Answering state.
	ErrNoActiveQuestion = errors.New("game: no active question")
	// ErrQuestionExpired is returned by SubmitAnswer when questionID isn't
	// the currently active question — the countdown already moved on.
	ErrQuestionExpired = errors.New("game: question no longer active")
	// ErrAlreadyAnswered is returned by SubmitAnswer for a duplicate
	// submission (e.g. a repeated button tap) for the same question.
	ErrAlreadyAnswered = errors.New("game: already answered this question")
)

// Question is this package's domain view of one question — translated from
// ws.Question (the wire format) so the delivery layer never needs to know
// about game-service's JSON shape.
type Question struct {
	ID       string
	Content  string
	Choices  []string
	Deadline time.Time
}

// PlayerScore is one player's standing on the leaderboard.
type PlayerScore struct {
	PlayerID string
	Points   int
}

// Leaderboard is this package's domain view of a leaderboard push.
type Leaderboard struct {
	Scores []PlayerScore
}

// Notifier is how Manager reports gameplay events to the delivery layer.
// All methods fire from matchmaking's read-loop goroutine for the affected
// chat (see matchmaking.GameHandler) — never the goroutine that called
// SubmitAnswer — so implementations must be safe for concurrent use with
// themselves.
type Notifier interface {
	// NotifyQuestion presents question (questionNumber of totalQuestions,
	// both 1-indexed) with its answer deadline.
	NotifyQuestion(chatID int64, question Question, questionNumber, totalQuestions int)
	// NotifyLeaderboard reports an updated leaderboard after an accepted
	// answer, with the game still in progress.
	NotifyLeaderboard(chatID int64, board Leaderboard)
	// NotifyReadyTimeout reports that the opponent never became ready.
	NotifyReadyTimeout(chatID int64)
	// NotifyCompleted reports the final leaderboard; the game is over.
	NotifyCompleted(chatID int64, board Leaderboard)
	// NotifyDisconnected reports the connection dropped mid-game.
	NotifyDisconnected(chatID int64)
}

// Detacher lets Manager tell matchmaking.Manager it's done with a chat
// (after COMPLETED or a ready-timeout) so the connection's natural close
// isn't mistaken for an unexpected disconnect. *matchmaking.Manager
// satisfies this.
type Detacher interface {
	Detach(chatID int64)
}

// chatGame is one chat's in-progress (post-match) game.
type chatGame struct {
	state  State
	gameID string
	conn   matchmaking.Connection

	questions    []Question
	currentIndex int
	// answeredQuestionID is the ID of the question this chat has already
	// submitted an answer for — guards against a duplicate button tap
	// re-submitting for the same question (see Manager.SubmitAnswer).
	answeredQuestionID string
	// awaitingAccept is true from the moment an ANSWER is sent until its
	// ANSWER_ACCEPTED is processed. It is what lets Manager tell a
	// legitimate accept apart from a late/duplicate one arriving after the
	// question already advanced on timeout (see handleAnswerAccepted).
	awaitingAccept bool

	readyTimer    *time.Timer
	questionTimer *time.Timer
}

func (cg *chatGame) stopTimers() {
	if cg.readyTimer != nil {
		cg.readyTimer.Stop()
	}
	if cg.questionTimer != nil {
		cg.questionTimer.Stop()
	}
}

// Manager owns one chatGame per Telegram chat from the moment
// matchmaking.Manager hands off a MATCH_CREATED (see Attached) through
// COMPLETED, a ready-timeout, or a disconnect. It implements
// matchmaking.GameHandler.
type Manager struct {
	notifier Notifier
	detacher Detacher
	logger   *slog.Logger

	// readyTimeout is a field (not just the readyTimeout const) so tests in
	// this package can shrink it instead of waiting 90 real seconds.
	readyTimeout time.Duration

	mu    sync.Mutex
	chats map[int64]*chatGame
}

// NewManager builds a Manager. All dependencies are required, injected by
// the caller.
func NewManager(notifier Notifier, detacher Detacher, logger *slog.Logger) *Manager {
	return &Manager{
		notifier:     notifier,
		detacher:     detacher,
		logger:       logger,
		readyTimeout: readyTimeout,
		chats:        make(map[int64]*chatGame),
	}
}

// StateFor reports chatID's current game state (StateIdle if it has no
// active game).
func (m *Manager) StateFor(chatID int64) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	cg, ok := m.chats[chatID]
	if !ok {
		return StateIdle
	}
	return cg.state
}

// Attached implements matchmaking.GameHandler: called once when chatID's
// MATCH_CREATED arrives. It immediately sends READY — this bot auto-readies
// on behalf of the user rather than waiting for a separate confirmation
// step, matching the flow in docs/client/client-business-flows.md §1 (no
// user action is shown between MATCH_CREATED and READY) — and starts the
// ready-timeout timer.
func (m *Manager) Attached(chatID int64, gameID string, conn matchmaking.Connection) func(ws.Event) {
	cg := &chatGame{state: StateMatched, gameID: gameID, conn: conn}

	m.mu.Lock()
	m.chats[chatID] = cg
	m.mu.Unlock()

	if err := conn.Send(ws.Command{Command: ws.CommandReady, GameID: gameID}); err != nil {
		m.logger.Error("failed to send READY", "chat_id", chatID, "game_id", gameID, "error", err)
		m.finish(chatID, cg)
		m.notifier.NotifyDisconnected(chatID)
		return func(ws.Event) {}
	}

	m.apply(chatID, cg, EventReadySent)

	m.mu.Lock()
	cg.readyTimer = time.AfterFunc(m.readyTimeout, func() { m.handleReadyTimeout(chatID, cg) })
	m.mu.Unlock()

	return func(event ws.Event) { m.handleEvent(chatID, event) }
}

// Disconnected implements matchmaking.GameHandler: called if the
// connection drops while this package still owns chatID (i.e. before
// finish/Detach ran).
func (m *Manager) Disconnected(chatID int64) {
	m.mu.Lock()
	cg, ok := m.chats[chatID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.chats, chatID)
	m.mu.Unlock()

	cg.stopTimers()
	m.notifier.NotifyDisconnected(chatID)
}

// apply calls Transition and, if valid, updates cg.state; it returns
// whether the transition was applied. Invalid transitions — duplicate,
// late, or otherwise out-of-order events — are logged and ignored rather
// than treated as an error, so unexpected input can never crash or corrupt
// a game in progress.
func (m *Manager) apply(chatID int64, cg *chatGame, event Event) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, ok := Transition(cg.state, event)
	if !ok {
		m.logger.Debug("ignoring invalid game state transition", "chat_id", chatID, "state", cg.state.String(), "event", event.String())
		return false
	}
	cg.state = next
	return true
}

func (m *Manager) handleReadyTimeout(chatID int64, cg *chatGame) {
	if !m.apply(chatID, cg, EventReadyTimeout) {
		return // already moved on (e.g. QUESTIONS_PUBLISHED won the race)
	}
	m.finish(chatID, cg)
	m.notifier.NotifyReadyTimeout(chatID)
}

func (m *Manager) handleEvent(chatID int64, event ws.Event) {
	m.mu.Lock()
	cg, ok := m.chats[chatID]
	m.mu.Unlock()
	if !ok {
		m.logger.Debug("game event for untracked chat, ignoring", "chat_id", chatID, "event", event.Event)
		return
	}

	switch event.Event {
	case ws.EventQuestionsPublished:
		m.handleQuestionsPublished(chatID, cg, event)
	case ws.EventAnswerAccepted:
		m.handleAnswerAccepted(chatID, cg, event)
	case ws.EventCompleted:
		m.handleCompleted(chatID, cg, event)
	case ws.EventError:
		m.logger.Warn("game received an ERROR event", "chat_id", chatID, "message", event.Message)
	default:
		m.logger.Debug("unexpected event for in-progress game, ignoring", "chat_id", chatID, "event", event.Event)
	}
}

func (m *Manager) handleQuestionsPublished(chatID int64, cg *chatGame, event ws.Event) {
	if !m.apply(chatID, cg, EventQuestionsPublished) {
		return
	}

	m.mu.Lock()
	if cg.readyTimer != nil {
		cg.readyTimer.Stop()
	}
	m.mu.Unlock()

	meta, err := event.QuestionsPublished()
	if err != nil {
		m.logger.Error("failed to decode QUESTIONS_PUBLISHED", "chat_id", chatID, "error", err)
		return
	}

	questions := make([]Question, len(meta.Questions))
	for i, q := range meta.Questions {
		questions[i] = Question{ID: q.ID, Content: q.Content, Choices: q.Choices, Deadline: q.TTL}
	}
	// Questions arrive all at once with individual deadlines
	// (docs/client/client-business-flows.md §6); this package paces the UI
	// by presenting them in deadline order, one at a time.
	sort.Slice(questions, func(i, j int) bool { return questions[i].Deadline.Before(questions[j].Deadline) })

	m.mu.Lock()
	cg.questions = questions
	cg.currentIndex = 0
	m.mu.Unlock()

	m.presentCurrentQuestion(chatID, cg)
}

// presentCurrentQuestion shows cg.questions[cg.currentIndex] (if any remain)
// and arms a timer for its deadline, so an unanswered question is skipped
// automatically instead of stalling the game client-side.
func (m *Manager) presentCurrentQuestion(chatID int64, cg *chatGame) {
	m.mu.Lock()
	if cg.currentIndex >= len(cg.questions) {
		m.mu.Unlock()
		// Every question's window has passed with no COMPLETED yet — just
		// wait; the server sends COMPLETED once its own total-TTL timer
		// elapses (docs/client/client-business-flows.md §7).
		return
	}
	q := cg.questions[cg.currentIndex]
	idx := cg.currentIndex
	total := len(cg.questions)
	cg.answeredQuestionID = ""
	cg.awaitingAccept = false
	m.mu.Unlock()

	timer := time.AfterFunc(time.Until(q.Deadline), func() { m.handleQuestionTimeout(chatID, cg, q.ID) })
	m.mu.Lock()
	cg.questionTimer = timer
	m.mu.Unlock()

	m.notifier.NotifyQuestion(chatID, q, idx+1, total)
}

// handleQuestionTimeout advances past questionID's window if the chat is
// still on that exact question — i.e. it neither answered in time nor
// already advanced via a just-processed ANSWER_ACCEPTED. This is the
// client-side countdown enforcement: game-service sends no "time's up"
// push (docs/client/client-business-flows.md §6 — the bot is responsible
// for pacing the UI itself).
func (m *Manager) handleQuestionTimeout(chatID int64, cg *chatGame, questionID string) {
	m.mu.Lock()
	current := cg.currentIndex < len(cg.questions) && cg.questions[cg.currentIndex].ID == questionID
	if cg.state != StateAnswering || !current {
		m.mu.Unlock()
		return // already advanced past this question
	}
	cg.currentIndex++
	m.mu.Unlock()

	m.presentCurrentQuestion(chatID, cg)
}

// SubmitAnswer sends choiceIndex (an index into the currently active
// question's Choices) as chatID's answer, if that question is still
// active. It returns a sentinel error — never a state mutation — for a
// chat with no active question, a stale question ID (the countdown already
// moved on), or a duplicate submission for the same question: these are
// the user-input equivalents of the duplicate/late-event handling this
// package applies to server events.
func (m *Manager) SubmitAnswer(chatID int64, questionID string, choiceIndex int) error {
	m.mu.Lock()
	cg, ok := m.chats[chatID]
	if !ok || cg.state != StateAnswering || cg.currentIndex >= len(cg.questions) {
		m.mu.Unlock()
		return ErrNoActiveQuestion
	}
	current := cg.questions[cg.currentIndex]
	if current.ID != questionID {
		m.mu.Unlock()
		return ErrQuestionExpired
	}
	if cg.answeredQuestionID == questionID {
		m.mu.Unlock()
		return ErrAlreadyAnswered
	}
	if choiceIndex < 0 || choiceIndex >= len(current.Choices) {
		m.mu.Unlock()
		return fmt.Errorf("game: choice index %d out of range for %d choices", choiceIndex, len(current.Choices))
	}
	choice := current.Choices[choiceIndex]
	cg.answeredQuestionID = questionID
	cg.awaitingAccept = true
	conn := cg.conn
	gameID := cg.gameID
	m.mu.Unlock()

	if err := conn.Send(ws.Command{Command: ws.CommandAnswer, Answer: &ws.Answer{GameID: gameID, QuestionID: questionID, Choice: choice}}); err != nil {
		return fmt.Errorf("sending answer: %w", err)
	}
	return nil
}

func (m *Manager) handleAnswerAccepted(chatID int64, cg *chatGame, event ws.Event) {
	if !m.apply(chatID, cg, EventAnswerAccepted) {
		return
	}

	meta, err := event.Leaderboard()
	if err != nil {
		m.logger.Error("failed to decode ANSWER_ACCEPTED", "chat_id", chatID, "error", err)
		return
	}
	board := toLeaderboard(meta)

	m.mu.Lock()
	wasPending := cg.awaitingAccept
	if wasPending {
		cg.awaitingAccept = false
		if cg.questionTimer != nil {
			cg.questionTimer.Stop()
		}
		cg.currentIndex++
	}
	m.mu.Unlock()

	m.notifier.NotifyLeaderboard(chatID, board)

	if !wasPending {
		// A late or duplicate accept (e.g. the question already timed out
		// and this package moved on before the response arrived) — the
		// leaderboard is still refreshed above, but must not re-trigger
		// advancing to a question that's already been presented.
		m.logger.Debug("received ANSWER_ACCEPTED with no pending answer (late or duplicate)", "chat_id", chatID)
		return
	}
	m.presentCurrentQuestion(chatID, cg)
}

func (m *Manager) handleCompleted(chatID int64, cg *chatGame, event ws.Event) {
	if !m.apply(chatID, cg, EventCompleted) {
		return
	}

	m.finish(chatID, cg)

	meta, err := event.Leaderboard()
	if err != nil {
		m.logger.Error("failed to decode COMPLETED", "chat_id", chatID, "error", err)
		m.notifier.NotifyCompleted(chatID, Leaderboard{})
		return
	}
	m.notifier.NotifyCompleted(chatID, toLeaderboard(meta))
}

// finish removes chatID from this package's tracking and tells matchmaking
// it's done with the connection, so the server's own post-COMPLETED close
// (docs/client/backend-api-analysis.md §4) isn't mistaken for an
// unexpected disconnect.
func (m *Manager) finish(chatID int64, cg *chatGame) {
	m.mu.Lock()
	if current, ok := m.chats[chatID]; ok && current == cg {
		delete(m.chats, chatID)
	}
	m.mu.Unlock()

	cg.stopTimers()
	m.detacher.Detach(chatID)
}

func toLeaderboard(meta ws.LeaderboardMetaData) Leaderboard {
	scores := make([]PlayerScore, len(meta.PlayerPoint))
	for i, p := range meta.PlayerPoint {
		scores[i] = PlayerScore{PlayerID: p.PlayerID, Points: p.Point}
	}
	return Leaderboard{Scores: scores}
}
