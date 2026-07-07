// Package profile implements the bot's view of a BrainBlitz user's account
// details: what "profile" means in terms of an API call plus the session's
// stored access token. It has no knowledge of Telegram — see
// internal/telegram/commands for the delivery-layer command and callback
// that call this package.
package profile

import (
	"context"
	"errors"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// UserAPI is the subset of the BrainBlitz user-service Service depends on.
// Defined here (the consumer), mirroring internal/core/auth.UserAPI, so
// tests can substitute a fake with no HTTP involved — internal/apiclient/http.UserClient
// implements this interface.
type UserAPI interface {
	Profile(ctx context.Context, accessToken string) (Profile, error)
}

// Profile is a BrainBlitz user's account details, as returned by
// GET /user-service/api/v1/profile (see docs/client/backend-api-analysis.md
// §2). There is deliberately no field for anything editable — the backend
// has no profile-edit endpoint (docs/client/gap-analysis.md G-19), so this
// is read-only by construction, not just by convention.
type Profile struct {
	Username    string
	DisplayName string
	Role        string
	CreatedAt   time.Time
}

// ErrNotLoggedIn is returned by Get when chatID has no stored session to
// fetch a profile with.
var ErrNotLoggedIn = errors.New("profile: not logged in")

// Service fetches a chat's BrainBlitz profile using its stored session.
type Service struct {
	api      UserAPI
	sessions session.Store
}

// NewService builds a Service. Both dependencies are interfaces, injected by
// the caller, matching internal/core/auth.NewService.
func NewService(api UserAPI, sessions session.Store) *Service {
	return &Service{api: api, sessions: sessions}
}

// Get fetches chatID's BrainBlitz profile using its stored access token. It
// returns ErrNotLoggedIn if chatID has no session — or has one whose token
// is old enough to certainly have expired server-side
// (session.Session.Stale; that session is also deleted, same as
// internal/core/auth.Service.IsLoggedIn) — or a *Error (via errors.As) for
// any API failure.
func (s *Service) Get(ctx context.Context, chatID int64) (Profile, error) {
	sess, err := s.sessions.Get(ctx, chatID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return Profile{}, ErrNotLoggedIn
		}
		return Profile{}, err
	}
	if sess.Stale() {
		_ = s.sessions.Delete(ctx, chatID)
		return Profile{}, ErrNotLoggedIn
	}

	p, err := s.api.Profile(ctx, sess.AccessToken)
	if err != nil {
		return Profile{}, classifyError(err)
	}
	return p, nil
}

// Kind classifies why Get failed, in terms the delivery layer can render a
// specific message for without knowing anything about the backend's wire
// format.
type Kind int

const (
	KindUnknown Kind = iota
	// KindTransport covers network failures and server errors (including
	// the malformed-request 400 documented in backend-api-analysis.md §2,
	// which shouldn't occur through the gateway) — nothing user-specific to
	// say, just "try again."
	KindTransport
	// KindNotFound covers a 404: the account behind this session's token no
	// longer exists (e.g. deleted after the token was issued). Per
	// docs/client/feature-map.md §2, this should force a local logout.
	KindNotFound
)

// Error is the typed error Get returns for any API failure.
type Error struct {
	Kind  Kind
	cause error
}

func (e *Error) Error() string { return e.cause.Error() }
func (e *Error) Unwrap() error { return e.cause }

// httpNotFound is the profile endpoint's "account no longer exists" status;
// see backend-api-analysis.md §2.
const httpNotFound = 404

func classifyError(err error) *Error {
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		return &Error{Kind: KindUnknown, cause: err}
	}
	if apiErr.StatusCode == httpNotFound {
		return &Error{Kind: KindNotFound, cause: err}
	}
	return &Error{Kind: KindTransport, cause: err}
}
