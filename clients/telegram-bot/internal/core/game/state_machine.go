// Package game implements the bot's client-side gameplay rules from the
// moment a match is found through its final result: sending READY,
// pacing questions against their individual deadlines, submitting answers,
// and tracking the leaderboard. Like internal/core/matchmaking, it has no
// knowledge of Telegram — see internal/telegram/commands for the
// delivery-layer callbacks that call this package.
package game

// State is the Phase 5 slice of the client-side game state machine
// (docs/client/client-architecture.md §5), picking up where
// internal/core/matchmaking hands off at "a match was found" through to a
// completed (or abandoned) game.
type State int

const (
	// StateMatched is the entry state: a match was found and this package
	// has just been handed the connection, but READY hasn't been sent yet.
	StateMatched State = iota
	// StateReadyPending: READY sent, waiting for QUESTIONS_PUBLISHED. The
	// backend gives no acknowledgment until *all* players are ready
	// (docs/client/backend-api-analysis.md §4 point 2), so this state can
	// only end via QUESTIONS_PUBLISHED or a client-side timeout.
	StateReadyPending
	// StateAnswering: questions received; the answering loop is active.
	StateAnswering
	// StateCompleted: the final leaderboard was received; the game is over.
	StateCompleted
	// StateIdle: no active game for this chat — post-completion, timed
	// out, or disconnected. A new game re-enters via
	// internal/core/matchmaking, not this package.
	StateIdle
)

func (s State) String() string {
	switch s {
	case StateMatched:
		return "Matched"
	case StateReadyPending:
		return "ReadyPending"
	case StateAnswering:
		return "Answering"
	case StateCompleted:
		return "Completed"
	case StateIdle:
		return "Idle"
	default:
		return "Unknown"
	}
}

// Event is an input to Transition — either a server push translated by
// this package's caller, or a purely local occurrence (a timeout, a
// disconnect, or the user acknowledging a finished game).
type Event int

const (
	EventReadySent Event = iota
	EventQuestionsPublished
	EventAnswerAccepted
	EventCompleted
	EventReadyTimeout
	EventDisconnected
	EventAcknowledged
)

func (e Event) String() string {
	switch e {
	case EventReadySent:
		return "ReadySent"
	case EventQuestionsPublished:
		return "QuestionsPublished"
	case EventAnswerAccepted:
		return "AnswerAccepted"
	case EventCompleted:
		return "Completed"
	case EventReadyTimeout:
		return "ReadyTimeout"
	case EventDisconnected:
		return "Disconnected"
	case EventAcknowledged:
		return "Acknowledged"
	default:
		return "Unknown"
	}
}

// Transition computes the next state for (current, event). It is a pure
// function — the same inputs always produce the same output, with no
// hidden state, clock reads, or I/O — so it is exhaustively unit-testable
// with no Telegram or network dependency, per
// docs/client/client-architecture.md §5.
//
// ok is false for any (state, event) pair not explicitly listed below. This
// is the single mechanism that makes the machine reject duplicate events
// (e.g. a second QUESTIONS_PUBLISHED while already Answering), late events
// (e.g. an AnswerAccepted arriving after Completed), and any other
// out-of-order input: callers must treat ok==false as "ignore this event
// and log it," never as an error to propagate, and must leave the session's
// state untouched (next == current is returned for convenience, but callers
// should not assign it — see internal/core/game.Manager.apply).
func Transition(current State, event Event) (next State, ok bool) {
	switch current {
	case StateMatched:
		switch event {
		case EventReadySent:
			return StateReadyPending, true
		case EventDisconnected:
			return StateIdle, true
		}

	case StateReadyPending:
		switch event {
		case EventQuestionsPublished:
			return StateAnswering, true
		case EventReadyTimeout, EventDisconnected:
			return StateIdle, true
		}

	case StateAnswering:
		switch event {
		case EventAnswerAccepted:
			// Stays in Answering — more questions may remain; Manager
			// decides whether to advance based on its own per-question
			// bookkeeping, not this transition alone.
			return StateAnswering, true
		case EventCompleted:
			return StateCompleted, true
		case EventDisconnected:
			return StateIdle, true
		}

	case StateCompleted:
		switch event {
		case EventAcknowledged:
			return StateIdle, true
		}

	case StateIdle:
		// No valid transitions out of Idle in this package — a new game
		// starts by re-entering through internal/core/matchmaking.
	}

	return current, false
}
