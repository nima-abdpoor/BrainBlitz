// Package session defines what the bot remembers about each Telegram
// chat's authenticated BrainBlitz identity, and where that is stored.
package session

import "time"

// Session is one Telegram chat's authenticated state: which BrainBlitz
// user they are, and their current tokens.
//
// There is deliberately no password field, and none should ever be added:
// internal/core/auth only ever holds a password in a local variable for
// the duration of a single SignUp/Login call, never persisting it here or
// anywhere else.
type Session struct {
	TelegramChatID int64
	UserID         string
	AccessToken    string
	RefreshToken   string
	TokenIssuedAt  time.Time
}

// AccessTokenMaxAge is how long an access token is trusted to still be
// valid without waiting for the backend to reject it — matching the
// documented 24h dev-config expiry
// (docs/client/backend-api-analysis.md §1). There is no token-refresh
// endpoint (docs/client/gap-analysis.md G-12), so once a session is this
// old the only correct move is asking the user to /login again; treating
// it as still valid would fail later anyway, just more confusingly (an
// opaque HTTP 401 or a rejected WebSocket upgrade instead of a clear
// message).
const AccessTokenMaxAge = 24 * time.Hour

// Stale reports whether s's access token is old enough that it should be
// treated as expired without waiting for an API call to confirm it. A
// zero-value TokenIssuedAt (never actually produced by
// internal/core/auth.Service.Login, which always sets it) is treated as
// not stale rather than maximally stale, since it more likely indicates an
// incompletely-constructed Session than a genuinely ancient one.
func (s Session) Stale() bool {
	return !s.TokenIssuedAt.IsZero() && time.Since(s.TokenIssuedAt) > AccessTokenMaxAge
}
