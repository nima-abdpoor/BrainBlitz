package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestServer starts an httptest server that upgrades every request to a
// WebSocket connection, handing the server-side *websocket.Conn to onConn
// (called on its own goroutine per connection) and recording the
// Authorization header the client dialed with.
func newTestServer(t *testing.T, onConn func(*websocket.Conn)) (*httptest.Server, *string) {
	t.Helper()

	var upgrader websocket.Upgrader
	var lastAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuthHeader = r.Header.Get("Authorization")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		if onConn != nil {
			go onConn(conn)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &lastAuthHeader
}

func wsURL(httpURL string) string {
	return "ws" + httpURL[len("http"):]
}

func TestDial_SendsAuthorizationHeader(t *testing.T) {
	srv, gotHeader := newTestServer(t, nil)

	client, err := Dial(context.Background(), wsURL(srv.URL), "test-token")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.Close()

	if want := "Bearer test-token"; *gotHeader != want {
		t.Errorf("Authorization header = %q, want %q", *gotHeader, want)
	}
}

func TestClient_SendAndListen_RoundTrip(t *testing.T) {
	srv, _ := newTestServer(t, func(conn *websocket.Conn) {
		var cmd Command
		if err := conn.ReadJSON(&cmd); err != nil {
			return
		}
		_ = conn.WriteJSON(Event{
			Success:  true,
			Event:    EventAddedToWaitingList,
			MetaData: []byte(`{}`),
		})
	})

	client, err := Dial(context.Background(), wsURL(srv.URL), "tok")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.Close()

	if err := client.Send(Command{Command: CommandAddToWaitingList, Category: "SPORT"}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	events := make(chan Event, 1)
	go func() {
		_ = client.Listen(func(e Event) { events <- e })
	}()

	select {
	case e := <-events:
		if e.Event != EventAddedToWaitingList {
			t.Errorf("event = %q, want %q", e.Event, EventAddedToWaitingList)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestClient_Listen_SkipsNonEnvelopedPush(t *testing.T) {
	srv, _ := newTestServer(t, func(conn *websocket.Conn) {
		// The non-enveloped first-connect push: no "event" field at all.
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"categories":["SPORT"],"numberOfPlayers":[2]}`))
		_ = conn.WriteJSON(Event{Success: true, Event: EventMatchCreated, MetaData: []byte(`{"gameId":"g1"}`)})
	})

	client, err := Dial(context.Background(), wsURL(srv.URL), "tok")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.Close()

	events := make(chan Event, 2)
	go func() {
		_ = client.Listen(func(e Event) { events <- e })
	}()

	select {
	case e := <-events:
		if e.Event != EventMatchCreated {
			t.Errorf("first dispatched event = %q, want %q (the categories push should have been skipped)", e.Event, EventMatchCreated)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestClient_Close_UnblocksListen(t *testing.T) {
	srv, _ := newTestServer(t, func(conn *websocket.Conn) {
		// Keep the server side open; the test only closes the client side.
		_, _, _ = conn.ReadMessage()
	})

	client, err := Dial(context.Background(), wsURL(srv.URL), "tok")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Listen(func(Event) {})
	}()

	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("Listen returned nil error after Close; want the read error that ended the loop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return after Close — read loop leaked")
	}

	// Close must be safe to call twice (cancel-then-natural-close race).
	if err := client.Close(); err != nil {
		t.Errorf("second Close returned error: %v", err)
	}
}

func TestClient_Send_IsConcurrencySafe(t *testing.T) {
	received := make(chan Command, 10)
	srv, _ := newTestServer(t, func(conn *websocket.Conn) {
		for {
			var cmd Command
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			received <- cmd
		}
	})

	client, err := Dial(context.Background(), wsURL(srv.URL), "tok")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.Close()

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.Send(Command{Command: CommandReady})
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatalf("only received %d of %d commands", i, n)
		}
	}
}
