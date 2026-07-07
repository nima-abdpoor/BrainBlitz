package game

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
)

// fakeConn is a minimal matchmaking.Connection double recording every
// command sent. Its Listen method is never exercised here — this package
// receives events via the onEvent func matchmaking.GameHandler.Attached
// returns, not by calling Listen itself — so it's a trivial no-op.
type fakeConn struct {
	mu   sync.Mutex
	sent []ws.Command
}

func newFakeConn() *fakeConn { return &fakeConn{} }

func (f *fakeConn) Send(cmd ws.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, cmd)
	return nil
}

func (f *fakeConn) Listen(func(ws.Event)) error { return errors.New("not used in this test") }
func (f *fakeConn) Close() error                { return nil }

func (f *fakeConn) sentCommands() []ws.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ws.Command(nil), f.sent...)
}

type questionCall struct {
	question       Question
	questionNumber int
	totalQuestions int
}

// fakeNotifier records Notifier calls on buffered channels so tests can
// assert on them without a sleep-based race.
type fakeNotifier struct {
	questions    chan questionCall
	leaderboards chan Leaderboard
	readyTimeout chan int64
	completed    chan Leaderboard
	disconnected chan int64
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{
		questions:    make(chan questionCall, 10),
		leaderboards: make(chan Leaderboard, 10),
		readyTimeout: make(chan int64, 10),
		completed:    make(chan Leaderboard, 10),
		disconnected: make(chan int64, 10),
	}
}

func (n *fakeNotifier) NotifyQuestion(_ int64, q Question, questionNumber, total int) {
	n.questions <- questionCall{q, questionNumber, total}
}
func (n *fakeNotifier) NotifyLeaderboard(_ int64, board Leaderboard) { n.leaderboards <- board }
func (n *fakeNotifier) NotifyReadyTimeout(chatID int64)              { n.readyTimeout <- chatID }
func (n *fakeNotifier) NotifyCompleted(_ int64, board Leaderboard)   { n.completed <- board }
func (n *fakeNotifier) NotifyDisconnected(chatID int64)              { n.disconnected <- chatID }

// fakeDetacher records Detach calls.
type fakeDetacher struct {
	calls chan int64
}

func newFakeDetacher() *fakeDetacher { return &fakeDetacher{calls: make(chan int64, 10)} }

func (d *fakeDetacher) Detach(chatID int64) { d.calls <- chatID }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const testChatID int64 = 42

func newTestManager(t *testing.T) (*Manager, *fakeNotifier, *fakeDetacher) {
	t.Helper()
	notifier := newFakeNotifier()
	detacher := newFakeDetacher()
	mgr := NewManager(notifier, detacher, testLogger())
	mgr.readyTimeout = 50 * time.Millisecond // real test package, so this is legal
	return mgr, notifier, detacher
}

func mustRecvQuestion(t *testing.T, ch chan questionCall) questionCall {
	t.Helper()
	select {
	case q := <-ch:
		return q
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyQuestion")
		return questionCall{}
	}
}

func TestAttached_SendsReadyAndTransitionsToReadyPending(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	conn := newFakeConn()

	onEvent := mgr.Attached(testChatID, "g1", conn)
	if onEvent == nil {
		t.Fatal("Attached returned a nil onEvent func")
	}

	sent := conn.sentCommands()
	if len(sent) != 1 || sent[0].Command != ws.CommandReady || sent[0].GameID != "g1" {
		t.Fatalf("sent commands = %+v, want one READY{gameId: g1}", sent)
	}
	if got := mgr.StateFor(testChatID); got != StateReadyPending {
		t.Errorf("state after Attached = %v, want ReadyPending", got)
	}
}

func TestReadyTimeout_FiresWhenNoQuestionsArrive(t *testing.T) {
	mgr, notifier, detacher := newTestManager(t)
	conn := newFakeConn()
	mgr.Attached(testChatID, "g1", conn)

	select {
	case chatID := <-notifier.readyTimeout:
		if chatID != testChatID {
			t.Errorf("NotifyReadyTimeout chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyReadyTimeout")
	}
	select {
	case chatID := <-detacher.calls:
		if chatID != testChatID {
			t.Errorf("Detach chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Detach after ready-timeout")
	}
	if got := mgr.StateFor(testChatID); got != StateIdle {
		t.Errorf("state after ready-timeout = %v, want Idle", got)
	}
}

func TestReadyTimeout_DoesNotFireIfQuestionsArriveFirst(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	mgr.readyTimeout = 5 * time.Second // long enough that QUESTIONS_PUBLISHED wins the race deterministically
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)

	onEvent(ws.Event{
		Event:    ws.EventQuestionsPublished,
		MetaData: []byte(`{"gameId":"g1","questions":[{"id":"q1","content":"2+2?","choices":["3","4"],"ttl":"` + time.Now().Add(time.Hour).Format(time.RFC3339Nano) + `"}]}`),
	})

	mustRecvQuestion(t, notifier.questions)

	select {
	case <-notifier.readyTimeout:
		t.Fatal("NotifyReadyTimeout fired after QUESTIONS_PUBLISHED already arrived")
	case <-time.After(200 * time.Millisecond):
	}
	if got := mgr.StateFor(testChatID); got != StateAnswering {
		t.Errorf("state after QUESTIONS_PUBLISHED = %v, want Answering", got)
	}
}

// questionsPublishedEvent builds a QUESTIONS_PUBLISHED ws.Event with two
// questions, in reverse deadline order in the wire payload — proving
// Manager presents them sorted by deadline, not wire order.
func questionsPublishedEvent(q1Deadline, q2Deadline time.Time) ws.Event {
	meta := `{"gameId":"g1","questions":[` +
		`{"id":"q2","content":"second","choices":["a","b"],"ttl":"` + q2Deadline.Format(time.RFC3339Nano) + `"},` +
		`{"id":"q1","content":"first","choices":["a","b"],"ttl":"` + q1Deadline.Format(time.RFC3339Nano) + `"}` +
		`]}`
	return ws.Event{Event: ws.EventQuestionsPublished, MetaData: []byte(meta)}
}

func TestQuestionsPublished_PresentsFirstQuestionSortedByDeadline(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)

	q1Deadline := time.Now().Add(time.Hour)
	q2Deadline := time.Now().Add(2 * time.Hour)
	onEvent(questionsPublishedEvent(q1Deadline, q2Deadline))

	got := mustRecvQuestion(t, notifier.questions)
	if got.question.ID != "q1" || got.questionNumber != 1 || got.totalQuestions != 2 {
		t.Errorf("first presented question = %+v, want q1 (1/2) — questions must be sorted by deadline", got)
	}
}

func TestQuestionTimeout_AdvancesToNextQuestion(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)

	q1Deadline := time.Now().Add(50 * time.Millisecond)
	q2Deadline := time.Now().Add(5 * time.Second)
	onEvent(questionsPublishedEvent(q1Deadline, q2Deadline))

	first := mustRecvQuestion(t, notifier.questions)
	if first.question.ID != "q1" {
		t.Fatalf("first question = %s, want q1", first.question.ID)
	}

	// Let q1's deadline pass without answering — the countdown timer must
	// advance to q2 on its own, with no server push telling it to.
	second := mustRecvQuestion(t, notifier.questions)
	if second.question.ID != "q2" || second.questionNumber != 2 {
		t.Errorf("second presented question = %+v, want q2 (2/2)", second)
	}
}

func TestSubmitAnswer_SendsChoiceAndAwaitsAccept(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	if err := mgr.SubmitAnswer(testChatID, "q1", 1); err != nil {
		t.Fatalf("SubmitAnswer returned error: %v", err)
	}

	sent := conn.sentCommands()
	last := sent[len(sent)-1]
	if last.Command != ws.CommandAnswer || last.Answer == nil || last.Answer.QuestionID != "q1" || last.Answer.Choice != "b" {
		t.Errorf("last sent command = %+v, want ANSWER{q1, choice \"b\"}", last)
	}
}

func TestSubmitAnswer_DuplicateIsRejected(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	if err := mgr.SubmitAnswer(testChatID, "q1", 0); err != nil {
		t.Fatalf("first SubmitAnswer returned error: %v", err)
	}
	if err := mgr.SubmitAnswer(testChatID, "q1", 1); !errors.Is(err, ErrAlreadyAnswered) {
		t.Errorf("second SubmitAnswer for the same question error = %v, want ErrAlreadyAnswered", err)
	}
	if got := len(conn.sentCommands()); got != 2 { // READY + one ANSWER
		t.Errorf("sent commands after rejected duplicate = %d, want 2", got)
	}
}

func TestSubmitAnswer_StaleQuestionIsRejected(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	conn := newFakeConn()
	mgr.Attached(testChatID, "g1", conn)
	// No QUESTIONS_PUBLISHED processed yet — still ReadyPending.

	if err := mgr.SubmitAnswer(testChatID, "q1", 0); !errors.Is(err, ErrNoActiveQuestion) {
		t.Errorf("SubmitAnswer with no active question error = %v, want ErrNoActiveQuestion", err)
	}
}

func TestSubmitAnswer_WrongQuestionIDIsRejected(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	if err := mgr.SubmitAnswer(testChatID, "some-other-question", 0); !errors.Is(err, ErrQuestionExpired) {
		t.Errorf("SubmitAnswer for a non-active question error = %v, want ErrQuestionExpired", err)
	}
}

func TestAnswerAccepted_UpdatesLeaderboardAndAdvances(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	if err := mgr.SubmitAnswer(testChatID, "q1", 0); err != nil {
		t.Fatalf("SubmitAnswer returned error: %v", err)
	}

	onEvent(ws.Event{
		Event:    ws.EventAnswerAccepted,
		MetaData: []byte(`{"leaderBoard":{"gameId":"g1","playerPoint":[{"playerId":"u1","point":10,"answers":[]}]}}`),
	})

	select {
	case board := <-notifier.leaderboards:
		if len(board.Scores) != 1 || board.Scores[0].PlayerID != "u1" || board.Scores[0].Points != 10 {
			t.Errorf("leaderboard = %+v, want one entry {u1, 10}", board)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyLeaderboard")
	}

	next := mustRecvQuestion(t, notifier.questions)
	if next.question.ID != "q2" {
		t.Errorf("question after ANSWER_ACCEPTED = %s, want q2 (advance to next question)", next.question.ID)
	}
}

func TestAnswerAccepted_LateEventUpdatesLeaderboardWithoutDoubleAdvancing(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	// q1's deadline is already in the past, so presentCurrentQuestion's
	// timer fires almost immediately, advancing to q2 without an answer.
	onEvent(questionsPublishedEvent(time.Now().Add(-time.Second), time.Now().Add(2*time.Hour)))
	first := mustRecvQuestion(t, notifier.questions)
	if first.question.ID != "q1" {
		t.Fatalf("first question = %s, want q1", first.question.ID)
	}
	second := mustRecvQuestion(t, notifier.questions) // the timeout-driven advance to q2
	if second.question.ID != "q2" {
		t.Fatalf("question after timeout = %s, want q2", second.question.ID)
	}

	// A late ANSWER_ACCEPTED for the never-submitted q1 arrives after the
	// client already moved on — leaderboard still updates, but must not
	// present a third question out of a two-question game.
	onEvent(ws.Event{
		Event:    ws.EventAnswerAccepted,
		MetaData: []byte(`{"leaderBoard":{"gameId":"g1","playerPoint":[{"playerId":"u1","point":0,"answers":[]}]}}`),
	})

	select {
	case <-notifier.leaderboards:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the late ANSWER_ACCEPTED's leaderboard update")
	}
	select {
	case q := <-notifier.questions:
		t.Errorf("a third NotifyQuestion fired after a late ANSWER_ACCEPTED: %+v", q)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCompleted_NotifiesFinalResultsAndDetaches(t *testing.T) {
	mgr, notifier, detacher := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	onEvent(ws.Event{
		Event:    ws.EventCompleted,
		MetaData: []byte(`{"leaderBoard":{"gameId":"g1","playerPoint":[{"playerId":"u1","point":20,"answers":[]}]}}`),
	})

	select {
	case board := <-notifier.completed:
		if len(board.Scores) != 1 || board.Scores[0].Points != 20 {
			t.Errorf("final leaderboard = %+v, want one entry with 20 points", board)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyCompleted")
	}
	select {
	case chatID := <-detacher.calls:
		if chatID != testChatID {
			t.Errorf("Detach chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Detach after COMPLETED")
	}
	if got := mgr.StateFor(testChatID); got != StateIdle {
		t.Errorf("state after COMPLETED+Detach = %v, want Idle (Detach removes tracking)", got)
	}
}

func TestCompleted_DuplicateIsIgnored(t *testing.T) {
	mgr, notifier, detacher := newTestManager(t)
	conn := newFakeConn()
	onEvent := mgr.Attached(testChatID, "g1", conn)
	onEvent(questionsPublishedEvent(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)))
	mustRecvQuestion(t, notifier.questions)

	completedEvent := ws.Event{
		Event:    ws.EventCompleted,
		MetaData: []byte(`{"leaderBoard":{"gameId":"g1","playerPoint":[{"playerId":"u1","point":20,"answers":[]}]}}`),
	}
	onEvent(completedEvent)
	<-notifier.completed
	<-detacher.calls

	// A duplicate COMPLETED for a chat this package no longer tracks must
	// be dropped silently, not panic or re-notify.
	onEvent(completedEvent)
	select {
	case board := <-notifier.completed:
		t.Errorf("NotifyCompleted fired again for a duplicate COMPLETED: %+v", board)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDisconnected_CleansUpAndNotifies(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)
	conn := newFakeConn()
	mgr.Attached(testChatID, "g1", conn)

	mgr.Disconnected(testChatID)

	select {
	case chatID := <-notifier.disconnected:
		if chatID != testChatID {
			t.Errorf("NotifyDisconnected chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyDisconnected")
	}
	if got := mgr.StateFor(testChatID); got != StateIdle {
		t.Errorf("state after Disconnected = %v, want Idle", got)
	}
}

func TestDisconnected_UnknownChatIsANoOp(t *testing.T) {
	mgr, notifier, _ := newTestManager(t)

	mgr.Disconnected(testChatID) // never attached

	select {
	case chatID := <-notifier.disconnected:
		t.Errorf("NotifyDisconnected fired for a chat that was never attached: %d", chatID)
	default:
	}
}

var _ matchmaking.Connection = (*fakeConn)(nil)
