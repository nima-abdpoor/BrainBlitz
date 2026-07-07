//go:build integration

// Package integration holds tests that exercise this bot's core+apiclient
// layers against a real, locally running BrainBlitz backend (the
// docker-compose stack in the repo root) instead of fakes — a deliberate
// complement to the fake-backed unit tests everywhere else in this module,
// not a replacement for them.
//
// These tests are gated behind the "integration" build tag so `go test
// ./...` (CI, and every other developer command in this repo) never
// requires a running backend. Run them explicitly with:
//
//	docker-compose up -d   # from the repo root
//	go test -tags=integration ./integration/...
//
// Each test also does its own reachability check and calls t.Skip if the
// backend isn't up, so an accidental `-tags=integration` run elsewhere
// fails soft instead of red.
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// defaultUserServiceURL and defaultGameServiceWSURL match
// configs/development/config.yaml — this module's own documented default
// for a docker-compose-backed local backend.
const (
	defaultUserServiceURL   = "http://localhost/user-service"
	defaultGameServiceWSURL = "ws://localhost/game-service/api/v1/process-game"
)

// userServiceURL returns BOT_INTEGRATION_USER_SERVICE_URL, or the default.
func userServiceURL() string {
	if v := os.Getenv("BOT_INTEGRATION_USER_SERVICE_URL"); v != "" {
		return v
	}
	return defaultUserServiceURL
}

// gameServiceWSURL returns BOT_INTEGRATION_GAME_SERVICE_WS_URL, or the default.
func gameServiceWSURL() string {
	if v := os.Getenv("BOT_INTEGRATION_GAME_SERVICE_WS_URL"); v != "" {
		return v
	}
	return defaultGameServiceWSURL
}

// discardLogger is what every integration test passes to apiclient/http's
// Client — these tests assert on behavior, not log output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// tHelper is the subset of *testing.T requireBackend needs, so it can be
// called from any test function without importing "testing" twice in this
// file's doc comment.
type tHelper interface {
	Helper()
	Skipf(format string, args ...any)
}

// requireBackend skips the calling test if url isn't reachable, with a
// message pointing at how to start the backend this test needs.
func requireBackend(t tHelper, url string) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Skipf("building reachability request for %s: %v", url, err)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("backend not reachable at %s — start it with `docker-compose up -d` from the repo root: %v", url, err)
		return
	}
	_ = resp.Body.Close()
}

// uniqueEmail returns a signup email that won't collide with a previous
// run against the same backend.
func uniqueEmail(label string) string {
	return fmt.Sprintf("bot-integration-%s-%d@example.com", label, time.Now().UnixNano())
}

const integrationPassword = "IntegrationTest123!"
