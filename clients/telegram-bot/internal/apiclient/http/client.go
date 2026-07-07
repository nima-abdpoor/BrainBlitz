// Package http provides the shared HTTP client used by every BrainBlitz
// backend service client (user, match). It owns only cross-cutting
// concerns — base URL resolution, timeouts, and retrying transient
// failures — never service-specific request/response shapes. Those are
// added as small typed methods (e.g. SignUp, Login) on top of this Client
// in later phases.
package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/pkg/retry"
)

// Default retry tuning, overridable via Option for tests or callers with
// different tolerance for latency.
const (
	defaultMaxRetries = 3
	defaultBackoff    = time.Second
)

// RateLimiter is the subset of pkg/ratelimit.Limiter this package depends
// on, defined here (the consumer) so this package doesn't need to import
// pkg/ratelimit directly — mirrors this codebase's established
// consumer-defined-interface pattern (e.g. internal/core/auth.UserAPI).
type RateLimiter interface {
	Wait(ctx context.Context) error
}

// Client is a minimal, retrying HTTP client bound to a single backend
// service's base URL (e.g. the user-service gateway path).
type Client struct {
	httpClient *http.Client
	baseURL    string
	logger     *slog.Logger
	maxRetries int
	backoff    time.Duration
	limiter    RateLimiter
}

// Option customizes a Client constructed by NewClient.
type Option func(*Client)

// WithMaxRetries overrides the default number of retry attempts for
// requests that fail with a network error or a 5xx response.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = n }
}

// WithBackoff overrides the base delay used between retry attempts. Actual
// delay grows exponentially: backoff * 2^(attempt-1).
func WithBackoff(d time.Duration) Option {
	return func(c *Client) { c.backoff = d }
}

// WithRateLimiter makes every attempt (including retries) wait for lim
// before the request goes out, so a burst of retries across many chats
// can't hammer the backend faster than lim allows
// (docs/client/client-architecture.md §9). Optional — a Client with no
// limiter configured (the default) never throttles itself.
func WithRateLimiter(lim RateLimiter) Option {
	return func(c *Client) { c.limiter = lim }
}

// NewClient builds a Client for a single backend base URL. logger must not
// be nil; callers should pass a no-op logger (e.g. slog.New(slog.DiscardHandler))
// in contexts where logging is unwanted, rather than passing nil.
func NewClient(baseURL string, timeout time.Duration, logger *slog.Logger, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		logger:     logger,
		maxRetries: defaultMaxRetries,
		backoff:    defaultBackoff,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Do sends an HTTP request built from method, path (joined to the client's
// base URL), and an optional body, retrying on network errors or 5xx
// responses with exponential backoff (via pkg/retry). A 4xx response is
// returned immediately without retrying, since retrying a client error
// cannot change the outcome.
//
// body is accepted as a byte slice (rather than io.Reader) specifically so
// it can be safely replayed across retry attempts.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, headers http.Header) (*http.Response, error) {
	url := c.baseURL + path

	var resp *http.Response
	policy := retry.Policy{MaxAttempts: c.maxRetries + 1, BaseDelay: c.backoff}
	err := retry.Do(ctx, policy, func() error {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return err
			}
		}

		r, err := c.attempt(ctx, method, url, body, headers)
		if err != nil {
			c.logger.Warn("http request attempt failed", "method", method, "path", path, "error", err)
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("request %s %s failed after %d attempts: %w", method, path, policy.MaxAttempts, err)
	}
	return resp, nil
}

// attempt performs a single try and classifies the result: nil error means
// the response should be returned to the caller as-is (including non-5xx
// error statuses, which are the caller's concern to interpret); a non-nil
// error means the attempt is retryable.
func (c *Client) attempt(ctx context.Context, method, url string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	for key, values := range headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error: status %d, body %q", resp.StatusCode, respBody)
	}

	return resp, nil
}
