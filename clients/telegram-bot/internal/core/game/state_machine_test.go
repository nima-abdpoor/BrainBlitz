package game

import "testing"

func allStates() []State {
	return []State{StateMatched, StateReadyPending, StateAnswering, StateCompleted, StateIdle}
}

func allEvents() []Event {
	return []Event{
		EventReadySent, EventQuestionsPublished, EventAnswerAccepted,
		EventCompleted, EventReadyTimeout, EventDisconnected, EventAcknowledged,
	}
}

type transitionKey struct {
	state State
	event Event
}

// validTransitions is the complete, authoritative set of (state, event)
// pairs Transition accepts. TestTransition_ExhaustiveTable checks every
// possible combination of allStates() x allEvents() against this map — any
// pair not listed here MUST be rejected (ok == false, state unchanged) by
// Transition, which is what gives the state machine its
// ignore-duplicate/ignore-late/ignore-out-of-order behavior "for free."
var validTransitions = map[transitionKey]State{
	{StateMatched, EventReadySent}:               StateReadyPending,
	{StateMatched, EventDisconnected}:            StateIdle,
	{StateReadyPending, EventQuestionsPublished}: StateAnswering,
	{StateReadyPending, EventReadyTimeout}:       StateIdle,
	{StateReadyPending, EventDisconnected}:       StateIdle,
	{StateAnswering, EventAnswerAccepted}:        StateAnswering,
	{StateAnswering, EventCompleted}:             StateCompleted,
	{StateAnswering, EventDisconnected}:          StateIdle,
	{StateCompleted, EventAcknowledged}:          StateIdle,
}

func TestTransition_ExhaustiveTable(t *testing.T) {
	for _, s := range allStates() {
		for _, e := range allEvents() {
			key := transitionKey{s, e}
			want, isValid := validTransitions[key]

			next, ok := Transition(s, e)

			if isValid {
				if !ok {
					t.Errorf("Transition(%v, %v) ok = false, want true", s, e)
					continue
				}
				if next != want {
					t.Errorf("Transition(%v, %v) = %v, want %v", s, e, next, want)
				}
				continue
			}

			if ok {
				t.Errorf("Transition(%v, %v) ok = true, want false (not a listed valid transition)", s, e)
			}
			if next != s {
				t.Errorf("Transition(%v, %v) returned next = %v for an invalid transition, want unchanged %v", s, e, next, s)
			}
		}
	}
}

func TestTransition_IsDeterministic(t *testing.T) {
	// Same inputs must always produce the same outputs — no hidden state,
	// no clock reads, no randomness.
	for _, s := range allStates() {
		for _, e := range allEvents() {
			next1, ok1 := Transition(s, e)
			next2, ok2 := Transition(s, e)
			if next1 != next2 || ok1 != ok2 {
				t.Errorf("Transition(%v, %v) is not deterministic: got (%v,%v) then (%v,%v)", s, e, next1, ok1, next2, ok2)
			}
		}
	}
}

func TestTransition_IgnoresDuplicateReady(t *testing.T) {
	if _, ok := Transition(StateReadyPending, EventReadySent); ok {
		t.Error("a second READY while already ReadyPending must be ignored")
	}
}

func TestTransition_IgnoresDuplicateQuestionsPublished(t *testing.T) {
	if _, ok := Transition(StateAnswering, EventQuestionsPublished); ok {
		t.Error("a duplicate QUESTIONS_PUBLISHED while already Answering must be ignored")
	}
}

func TestTransition_IgnoresLateAnswerAcceptedAfterCompletion(t *testing.T) {
	// Regression: an ANSWER_ACCEPTED that arrives after COMPLETED (e.g. a
	// slow response to the very last answer) must not resurrect the game.
	if _, ok := Transition(StateCompleted, EventAnswerAccepted); ok {
		t.Error("a late ANSWER_ACCEPTED arriving after COMPLETED must be ignored")
	}
}

func TestTransition_IgnoresDuplicateCompleted(t *testing.T) {
	if _, ok := Transition(StateCompleted, EventCompleted); ok {
		t.Error("a duplicate COMPLETED must be ignored")
	}
}

func TestTransition_IdleAcceptsNothing(t *testing.T) {
	for _, e := range allEvents() {
		if _, ok := Transition(StateIdle, e); ok {
			t.Errorf("Idle must not accept event %v — a new game starts via matchmaking, not this package", e)
		}
	}
}

func TestTransition_ReadyTimeoutOnlyValidWhilePending(t *testing.T) {
	for _, s := range allStates() {
		if s == StateReadyPending {
			continue
		}
		if _, ok := Transition(s, EventReadyTimeout); ok {
			t.Errorf("Transition(%v, EventReadyTimeout) ok = true, want false — timeout only applies while ReadyPending", s)
		}
	}
}
