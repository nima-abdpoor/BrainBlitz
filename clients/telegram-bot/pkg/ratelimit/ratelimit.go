// Package ratelimit throttles this bot's own outbound calls to the
// BrainBlitz backend, so a burst of client-side retries — e.g. many chats
// retrying at once during a backend blip — never hammers the gateway any
// harder than a single sane baseline rate (docs/client/client-architecture.md
// §9). It wraps golang.org/x/time/rate, the standard token-bucket
// implementation, rather than reimplementing one.
package ratelimit

import (
	"context"

	"golang.org/x/time/rate"
)

// Limiter is a token-bucket rate limiter shared across every outbound call
// site that opts in (internal/apiclient/http.Client, the WS dialer wired in
// cmd/bot/main.go) — one process talking to one gateway, so a single
// process-wide budget is what actually protects the backend.
type Limiter struct {
	l *rate.Limiter
}

// New builds a Limiter allowing ratePerSecond sustained requests/sec, with
// up to burst requests allowed instantaneously before throttling kicks in.
func New(ratePerSecond float64, burst int) *Limiter {
	return &Limiter{l: rate.NewLimiter(rate.Limit(ratePerSecond), burst)}
}

// Wait blocks until a slot is available or ctx is cancelled first.
func (lim *Limiter) Wait(ctx context.Context) error {
	return lim.l.Wait(ctx)
}
