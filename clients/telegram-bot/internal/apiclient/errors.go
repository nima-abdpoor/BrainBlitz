// Package apiclient holds types shared by every BrainBlitz backend service
// client (currently internal/apiclient/http, later internal/apiclient/ws).
// It defines the single normalized error shape that absorbs the backend's
// documented inconsistencies (see docs/client/backend-api-analysis.md §7)
// so neither internal/core nor internal/telegram ever parses raw response
// bodies themselves.
package apiclient

import (
	"encoding/json"
	"strings"
)

// Kind classifies why a backend call failed, coarsely enough for callers to
// decide "is this worth telling the user something specific, or just
// generic transient-failure messaging?" without inspecting HTTP internals.
type Kind int

const (
	// KindUnknown covers errors this package didn't produce (e.g. a bug in
	// response decoding) — callers should treat it like KindTransport.
	KindUnknown Kind = iota
	// KindTransport covers network failures and cases where the underlying
	// http.Client already exhausted its retries against repeated 5xx
	// responses (see internal/apiclient/http.Client.Do): by the time this
	// package sees it, retrying again is not useful.
	KindTransport
	// KindBackend covers a 4xx response the backend returned deliberately —
	// a business-rule rejection, not a transient failure.
	KindBackend
)

// Error is what every BrainBlitz API failure becomes before internal/core
// sees it. The backend returns errors as either a bare JSON string (e.g.
// "invalid category") or an object like {"message": "...", "error": "..."}
// depending on which code path produced them; Message is already resolved
// to plain text either way.
type Error struct {
	Kind       Kind
	StatusCode int // 0 for network errors (KindTransport with no response)
	Message    string
	Retryable  bool
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "backend request failed"
}

// NewTransportError wraps a network failure, or a failure the underlying
// HTTP client already retried and gave up on. Both cases are equally
// "nothing more this layer can do about it right now."
func NewTransportError(cause error) *Error {
	return &Error{Kind: KindTransport, Message: cause.Error(), Retryable: true}
}

// NewBackendError builds an Error from a non-2xx response body, extracting
// a human-readable message regardless of which of the backend's two
// documented error-body shapes was used.
func NewBackendError(statusCode int, body []byte) *Error {
	return &Error{
		Kind:       KindBackend,
		StatusCode: statusCode,
		Message:    parseErrorBody(body),
		Retryable:  false, // Client.Do only ever hands 4xx bodies to this constructor; see doc comment above.
	}
}

// parseErrorBody extracts a message from a BrainBlitz error response body.
// Most endpoints return {"message": "...", "error": "..."}, but a handful
// (documented in backend-api-analysis.md §7) return a bare JSON string like
// "invalid category" instead of an object.
func parseErrorBody(body []byte) string {
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}

	var bare string
	if err := json.Unmarshal(body, &bare); err == nil && bare != "" {
		return bare
	}

	return strings.TrimSpace(string(body))
}
