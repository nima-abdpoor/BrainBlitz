package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Client is one WebSocket connection to game-service's process-game
// endpoint. It serializes writes (gorilla/websocket forbids concurrent
// writes on a single connection) and exposes a blocking read loop via
// Listen; internal/core/matchmaking runs Listen on its own goroutine per
// chat.
type Client struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

// Dial connects to url (game-service's WebSocket upgrade endpoint),
// attaching accessToken the same way the gateway's ForwardAuth expects it
// on any other authenticated call (docs/client/backend-api-analysis.md §4).
// A failed upgrade (e.g. an expired/invalid token) is not retried here —
// see internal/core/matchmaking for the retry policy wrapping Dial.
func Dial(ctx context.Context, url, accessToken string) (*Client, error) {
	header := http.Header{"Authorization": []string{"Bearer " + accessToken}}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, fmt.Errorf("dialing game service websocket: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Send writes cmd as the standard command envelope. Safe for concurrent use
// with itself and with Listen's read loop, since reads and writes are
// independent halves of the underlying connection.
func (c *Client) Send(cmd Command) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(cmd)
}

// Listen runs the read loop on the calling goroutine, invoking handler for
// every frame that decodes as the standard event envelope, until the
// connection closes or a read fails — at which point it returns the error
// that ended the loop (ReadMessage always eventually errors on close, so
// callers should expect a non-nil error even after a clean shutdown
// triggered by Close).
//
// A frame that doesn't decode as an event (Event.Event == "") is the
// non-enveloped {categories, numberOfPlayers} push game-service sends on a
// genuinely first-ever connection (backend-api-analysis.md §4). This bot
// hardcodes categories client-side instead (approved Phase 4 decision), so
// such frames are silently skipped rather than treated as malformed input.
func (c *Client) Listen(handler func(Event)) error {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return err
		}

		var event Event
		if err := json.Unmarshal(data, &event); err != nil || event.Event == "" {
			continue
		}
		handler(event)
	}
}

// Close closes the underlying connection, unblocking any in-flight
// ReadMessage in Listen's loop. Safe to call more than once — a cancel and
// a natural disconnect can race to close the same Client.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}
