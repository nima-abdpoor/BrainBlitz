package matchmaking

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// fakeConn is a test double for Connection. Its Listen loop dispatches
// exactly the events the test pushes onto it, in order, and only stops when
// the test explicitly calls endListen — decoupled from Close so tests can
// control event delivery deterministically, independent of when Cancel
// happens to call Close (see docs/client/phase4-matchmaking-plan.md §10).
type fakeConn struct {
	mu   sync.Mutex
	sent []ws.Command

	events     chan ws.Event
	closeCalls int
}

func newFakeConn() *fakeConn {
	return &fakeConn{events: make(chan ws.Event, 10)}
}

func (f *fakeConn) Send(cmd ws.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, cmd)
	return nil
}

func (f *fakeConn) Listen(handler func(ws.Event)) error {
	for e := range f.events {
		handler(e)
	}
	return errors.New("fake connection closed")
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func (f *fakeConn) sentCommands() []ws.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ws.Command(nil), f.sent...)
}

func (f *fakeConn) push(e ws.Event) { f.events <- e }
func (f *fakeConn) endListen()      { close(f.events) }

type matchCreatedCall struct {
	chatID int64
	gameID string
}

type queueFailedCall struct {
	chatID int64
	reason string
}

// fakeNotifier records Notifier calls on buffered channels so tests can
// assert on them without a sleep-based race.
type fakeNotifier struct {
	matchCreated chan matchCreatedCall
	queueFailed  chan queueFailedCall
	disconnected chan int64
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{
		matchCreated: make(chan matchCreatedCall, 10),
		queueFailed:  make(chan queueFailedCall, 10),
		disconnected: make(chan int64, 10),
	}
}

func (n *fakeNotifier) NotifyMatchCreated(chatID int64, gameID string) {
	n.matchCreated <- matchCreatedCall{chatID, gameID}
}

func (n *fakeNotifier) NotifyQueueFailed(chatID int64, reason string) {
	n.queueFailed <- queueFailedCall{chatID, reason}
}

func (n *fakeNotifier) NotifyDisconnected(chatID int64) {
	n.disconnected <- chatID
}

func newTestManager(t *testing.T, conn Connection, dialErr error, notifier *fakeNotifier) (*Manager, *session.MemoryStore) {
	t.Helper()
	sessions := session.NewMemoryStore()
	dial := func(_ context.Context, _ string) (Connection, error) {
		return conn, dialErr
	}
	return NewManager(dial, notifier, sessions, nil), sessions
}

const testChatID int64 = 42

func mustSaveSession(t *testing.T, sessions *session.MemoryStore, chatID int64) {
	t.Helper()
	sess := session.Session{TelegramChatID: chatID, AccessToken: "tok", TokenIssuedAt: time.Now()}
	if err := sessions.Save(context.Background(), sess); err != nil {
		t.Fatalf("saving session: %v", err)
	}
}

func TestJoinQueue_HappyPath(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, conn, nil, notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}
	if got := mgr.StateFor(testChatID); got != Queued {
		t.Fatalf("state after JoinQueue = %v, want Queued", got)
	}
	sent := conn.sentCommands()
	if len(sent) != 1 || sent[0].Command != ws.CommandAddToWaitingList || sent[0].Category != "SPORT" {
		t.Fatalf("sent commands = %+v, want one ADD_TO_WAITING_LIST{SPORT}", sent)
	}

	conn.push(ws.Event{Success: true, Event: ws.EventAddedToWaitingList, MetaData: []byte(`{}`)})
	conn.push(ws.Event{Success: true, Event: ws.EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})

	select {
	case call := <-notifier.matchCreated:
		if call.chatID != testChatID || call.gameID != "g1" {
			t.Errorf("NotifyMatchCreated call = %+v, want {%d g1}", call, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyMatchCreated")
	}

	if got := mgr.StateFor(testChatID); got != Matched {
		t.Errorf("state after MATCH_CREATED = %v, want Matched", got)
	}
}

func TestJoinQueue_AlreadyInProgress(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, conn, nil, notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("first JoinQueue returned error: %v", err)
	}

	err := mgr.JoinQueue(context.Background(), testChatID, "MUSIC")
	if !errors.Is(err, ErrAlreadyInProgress) {
		t.Errorf("second JoinQueue error = %v, want ErrAlreadyInProgress", err)
	}
	// The double-join guard must reject before a second dial/send happens.
	if got := len(conn.sentCommands()); got != 1 {
		t.Errorf("sent commands after rejected double-join = %d, want 1", got)
	}
}

func TestJoinQueue_ErrorThenFalseSuccess_TreatedAsFailure(t *testing.T) {
	// Regression test for the documented backend quirk
	// (backend-api-analysis.md §4 point 1): an invalid category produces
	// BOTH an ERROR and a subsequent ADDED_TO_WAITING_LIST. The manager must
	// not treat the trailing "success" event as proof the error didn't
	// happen.
	conn := newFakeConn()
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, conn, nil, notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "BOGUS"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}

	conn.push(ws.Event{Success: false, Event: ws.EventError, Message: "invalid category"})
	conn.push(ws.Event{Success: true, Event: ws.EventAddedToWaitingList, MetaData: []byte(`{}`)})

	select {
	case call := <-notifier.queueFailed:
		if call.chatID != testChatID || call.reason != "invalid category" {
			t.Errorf("NotifyQueueFailed call = %+v, want {%d invalid category}", call, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyQueueFailed")
	}

	if got := mgr.StateFor(testChatID); got != Idle {
		t.Errorf("state after latched error = %v, want Idle", got)
	}
	select {
	case call := <-notifier.matchCreated:
		t.Errorf("NotifyMatchCreated unexpectedly called: %+v", call)
	default:
	}
}

func TestCancel_DropsRacingMatchCreated(t *testing.T) {
	// Regression test for race #2 in
	// docs/client/phase4-matchmaking-plan.md §8: a Cancel that lands the
	// instant before MATCH_CREATED arrives must not let the match through.
	conn := newFakeConn()
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, conn, nil, notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}

	if err := mgr.Cancel(testChatID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if got := mgr.StateFor(testChatID); got != Idle {
		t.Fatalf("state after Cancel = %v, want Idle", got)
	}
	if conn.closeCalls != 1 {
		t.Errorf("Close calls after Cancel = %d, want 1", conn.closeCalls)
	}

	// Simulate the race: MATCH_CREATED was already in flight and arrives
	// after Cancel has already removed this chat's session.
	conn.push(ws.Event{Success: true, Event: ws.EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})

	select {
	case call := <-notifier.matchCreated:
		t.Errorf("NotifyMatchCreated fired for a cancelled chat: %+v", call)
	case <-time.After(200 * time.Millisecond):
		// Expected: nothing arrives. A short bounded wait is the only way
		// to assert a negative outcome here — there is no further
		// observable side effect of a dropped event to synchronize on.
	}
}

func TestCancel_NotInQueue(t *testing.T) {
	mgr, _ := newTestManager(t, newFakeConn(), nil, newFakeNotifier())

	if err := mgr.Cancel(testChatID); !errors.Is(err, ErrNotInQueue) {
		t.Errorf("Cancel on idle chat error = %v, want ErrNotInQueue", err)
	}
}

func TestUnexpectedDisconnect_NotifiesAndCleansUp(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, conn, nil, notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}

	conn.endListen() // simulate the connection dropping unexpectedly

	select {
	case chatID := <-notifier.disconnected:
		if chatID != testChatID {
			t.Errorf("NotifyDisconnected chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyDisconnected")
	}
	if got := mgr.StateFor(testChatID); got != Idle {
		t.Errorf("state after disconnect = %v, want Idle", got)
	}
}

func TestJoinQueue_DialFailure_CleansUpAndAllowsRetry(t *testing.T) {
	notifier := newFakeNotifier()
	mgr, sessions := newTestManager(t, nil, errors.New("connection refused"), notifier)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err == nil {
		t.Fatal("JoinQueue returned nil error, want the dial failure")
	}
	if got := mgr.StateFor(testChatID); got != Idle {
		t.Errorf("state after failed dial = %v, want Idle (so /play can be retried)", got)
	}
}

func TestJoinQueue_StaleSessionFailsFastWithoutDialing(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	dialCalls := 0
	sessions := session.NewMemoryStore()
	dial := func(context.Context, string) (Connection, error) {
		dialCalls++
		return conn, nil
	}
	mgr := NewManager(dial, notifier, sessions, nil)
	stale := session.Session{TelegramChatID: testChatID, AccessToken: "tok", TokenIssuedAt: time.Now().Add(-session.AccessTokenMaxAge - time.Hour)}
	if err := sessions.Save(context.Background(), stale); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	err := mgr.JoinQueue(context.Background(), testChatID, "SPORT")
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("JoinQueue error = %v, want ErrSessionExpired", err)
	}
	if dialCalls != 0 {
		t.Errorf("dial calls = %d, want 0 (stale session must fail before dialing)", dialCalls)
	}
	if got := mgr.StateFor(testChatID); got != Idle {
		t.Errorf("state after stale-session rejection = %v, want Idle", got)
	}
}

// fakeGameHandler is a test double for GameHandler, recording Attached and
// Disconnected calls. Attached forwards every subsequent event it's given
// onto a channel the test can drain, proving matchmaking really does route
// post-match events to the game layer instead of dropping them.
type fakeGameHandler struct {
	mu           sync.Mutex
	attachedCall struct {
		chatID int64
		gameID string
		conn   Connection
	}
	attachedCalls   int
	events          chan ws.Event
	disconnectCalls chan int64
}

func newFakeGameHandler() *fakeGameHandler {
	return &fakeGameHandler{events: make(chan ws.Event, 10), disconnectCalls: make(chan int64, 10)}
}

func (h *fakeGameHandler) Attached(chatID int64, gameID string, conn Connection) func(ws.Event) {
	h.mu.Lock()
	h.attachedCalls++
	h.attachedCall.chatID = chatID
	h.attachedCall.gameID = gameID
	h.attachedCall.conn = conn
	h.mu.Unlock()

	return func(e ws.Event) { h.events <- e }
}

func (h *fakeGameHandler) Disconnected(chatID int64) {
	h.disconnectCalls <- chatID
}

func TestMatchCreated_AttachesGameHandlerAndForwardsSubsequentEvents(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	gameHandler := newFakeGameHandler()
	sessions := session.NewMemoryStore()
	dial := func(context.Context, string) (Connection, error) { return conn, nil }
	mgr := NewManager(dial, notifier, sessions, gameHandler)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}
	conn.push(ws.Event{Success: true, Event: ws.EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})

	select {
	case <-notifier.matchCreated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyMatchCreated")
	}

	gameHandler.mu.Lock()
	calls := gameHandler.attachedCalls
	got := gameHandler.attachedCall
	gameHandler.mu.Unlock()
	if calls != 1 {
		t.Fatalf("GameHandler.Attached calls = %d, want 1", calls)
	}
	if got.chatID != testChatID || got.gameID != "g1" || got.conn != Connection(conn) {
		t.Errorf("Attached called with (%d, %q, conn), want (%d, g1, the dialed conn)", got.chatID, got.gameID, testChatID)
	}

	// A subsequent event (QUESTIONS_PUBLISHED, meaningless to matchmaking
	// itself) must now reach the game handler, not be dropped.
	conn.push(ws.Event{Success: true, Event: ws.EventQuestionsPublished})
	select {
	case e := <-gameHandler.events:
		if e.Event != ws.EventQuestionsPublished {
			t.Errorf("forwarded event = %q, want %q", e.Event, ws.EventQuestionsPublished)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the event to be forwarded to GameHandler")
	}
}

func TestDetach_ClosesConnectionWithoutNotifyingDisconnect(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	gameHandler := newFakeGameHandler()
	sessions := session.NewMemoryStore()
	dial := func(context.Context, string) (Connection, error) { return conn, nil }
	mgr := NewManager(dial, notifier, sessions, gameHandler)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}
	conn.push(ws.Event{Success: true, Event: ws.EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})
	select {
	case <-notifier.matchCreated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyMatchCreated")
	}

	mgr.Detach(testChatID)

	if got := mgr.StateFor(testChatID); got != Idle {
		t.Errorf("state after Detach = %v, want Idle", got)
	}
	if conn.closeCalls != 1 {
		t.Errorf("Close calls after Detach = %d, want 1", conn.closeCalls)
	}

	// The connection's read loop ending after Detach must not be reported
	// as an unexpected disconnect through either path.
	conn.endListen()
	select {
	case <-notifier.disconnected:
		t.Error("NotifyDisconnected fired after a clean Detach")
	case <-gameHandler.disconnectCalls:
		t.Error("GameHandler.Disconnected fired after a clean Detach")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDisconnect_AfterAttach_NotifiesGameHandlerNotMatchmakingNotifier(t *testing.T) {
	conn := newFakeConn()
	notifier := newFakeNotifier()
	gameHandler := newFakeGameHandler()
	sessions := session.NewMemoryStore()
	dial := func(context.Context, string) (Connection, error) { return conn, nil }
	mgr := NewManager(dial, notifier, sessions, gameHandler)
	mustSaveSession(t, sessions, testChatID)

	if err := mgr.JoinQueue(context.Background(), testChatID, "SPORT"); err != nil {
		t.Fatalf("JoinQueue returned error: %v", err)
	}
	conn.push(ws.Event{Success: true, Event: ws.EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})
	select {
	case <-notifier.matchCreated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NotifyMatchCreated")
	}

	conn.endListen() // unexpected disconnect, mid-game — Detach was never called

	select {
	case chatID := <-gameHandler.disconnectCalls:
		if chatID != testChatID {
			t.Errorf("Disconnected chatID = %d, want %d", chatID, testChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for GameHandler.Disconnected")
	}

	select {
	case call := <-notifier.disconnected:
		t.Errorf("matchmaking.Notifier.NotifyDisconnected fired for a chat GameHandler had already taken over: %v", call)
	default:
	}
}
