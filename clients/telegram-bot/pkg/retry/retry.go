// Package retry provides a single generic backoff/retry helper, shared by
// every outbound call this bot makes that docs/client/client-architecture.md
// §9 designates as safely retryable (HTTP calls to user-service, and
// WebSocket connection attempts to game-service). It is deliberately generic
// over "what a retryable failure looks like" — see Do's doc comment.
package retry

import (
	"context"
	"time"
)

// Policy configures Do's retry/backoff behavior.
type Policy struct {
	// MaxAttempts is the total number of tries, including the first —
	// MaxAttempts: 3 means "try, then up to 2 retries."
	MaxAttempts int
	// BaseDelay is the delay before the second attempt; each subsequent
	// delay doubles (exponential backoff): BaseDelay, 2*BaseDelay,
	// 4*BaseDelay, ...
	BaseDelay time.Duration
	// MaxDelay caps the computed delay so backoff never grows unbounded.
	// Zero means uncapped.
	MaxDelay time.Duration
}

// Do calls fn up to p.MaxAttempts times, waiting an exponentially growing
// delay between attempts, until fn returns nil, attempts are exhausted, or
// ctx is cancelled. It returns the last error fn returned (or ctx.Err() if
// cancelled while waiting for the next attempt).
//
// Do retries on ANY non-nil error — it has no notion of "retryable vs.
// not." Callers whose failures include a non-retryable class (e.g. an HTTP
// 4xx, which must never be retried per client-architecture.md §9) must have
// fn itself absorb that case and return nil, stashing the real outcome in a
// closure variable — see internal/apiclient/http.Client.Do for the pattern.
func Do(ctx context.Context, p Policy, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if attempt > 1 {
			if err := wait(ctx, p, attempt); err != nil {
				return err
			}
		}

		if err := fn(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// wait blocks for the delay before the given attempt number (attempt >= 2),
// returning early with ctx.Err() if ctx is cancelled first.
func wait(ctx context.Context, p Policy, attempt int) error {
	delay := p.BaseDelay * time.Duration(1<<(attempt-2))
	if p.MaxDelay > 0 && delay > p.MaxDelay {
		delay = p.MaxDelay
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
