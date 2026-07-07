// Package auth implements the bot's authentication business rules: what
// "register" and "login" mean in terms of BrainBlitz API calls and the
// resulting session state. It has no knowledge of Telegram — see
// internal/telegram/commands for the delivery-layer conversation flow that
// calls this package.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// UserAPI is the subset of the BrainBlitz user-service Service depends on.
// Defining it here (the consumer) rather than in internal/apiclient/http
// (the implementer) is what lets tests substitute a fake without any HTTP
// involved, and is what keeps this package ignorant of HTTP status codes
// and JSON shapes — internal/apiclient/http.UserClient implements this
// interface and does that translation.
type UserAPI interface {
	SignUp(ctx context.Context, email, password string) (SignUpResult, error)
	Login(ctx context.Context, email, password string) (LoginResult, error)
}

// SignUpResult is what a successful signup call returns.
type SignUpResult struct {
	DisplayName string
}

// LoginResult is what a successful login call returns.
type LoginResult struct {
	UserID       string
	AccessToken  string
	RefreshToken string
}

// RegisterResult is what the delivery layer needs to render a successful
// registration.
type RegisterResult struct {
	DisplayName string
}

// Service implements registration, login, and session lifecycle. A
// password is only ever held in a local variable for the duration of a
// Register or Login call — it is never written to sessions or logged.
type Service struct {
	api      UserAPI
	sessions session.Store
}

// NewService builds a Service. Both dependencies are interfaces, injected
// by the caller, so Service never reaches for a concrete HTTP client or
// storage backend directly.
func NewService(api UserAPI, sessions session.Store) *Service {
	return &Service{api: api, sessions: sessions}
}

// Register signs up a new account and, on success, immediately logs in
// with the same credentials and persists the resulting session — signup
// alone returns no tokens (see docs/client/backend-api-analysis.md §2), so
// a bare "registered" state with no session would strand the user needing
// to separately log in with a password they just typed.
func (s *Service) Register(ctx context.Context, chatID int64, email, password string) (RegisterResult, error) {
	signUp, err := s.api.SignUp(ctx, email, password)
	if err != nil {
		return RegisterResult{}, classifySignUpError(err)
	}

	if _, err := s.Login(ctx, chatID, email, password); err != nil {
		return RegisterResult{}, err
	}

	return RegisterResult{DisplayName: signUp.DisplayName}, nil
}

// Login authenticates and persists a Session for chatID. It returns a
// *Error (via errors.As) on any failure so callers can branch on Kind
// without inspecting HTTP details.
func (s *Service) Login(ctx context.Context, chatID int64, email, password string) (session.Session, error) {
	result, err := s.api.Login(ctx, email, password)
	if err != nil {
		return session.Session{}, classifyLoginError(err)
	}

	sess := session.Session{
		TelegramChatID: chatID,
		UserID:         result.UserID,
		AccessToken:    result.AccessToken,
		RefreshToken:   result.RefreshToken,
		TokenIssuedAt:  time.Now().UTC(),
	}
	if err := s.sessions.Save(ctx, sess); err != nil {
		return session.Session{}, fmt.Errorf("saving session: %w", err)
	}
	return sess, nil
}

// Logout removes chatID's stored session. There is no server-side
// revocation endpoint — JWTs are stateless (see
// docs/client/feature-map.md §2) — so this is purely a local
// forget-the-token action.
func (s *Service) Logout(ctx context.Context, chatID int64) error {
	return s.sessions.Delete(ctx, chatID)
}

// IsLoggedIn reports whether chatID currently has a stored, still-fresh
// session. A session whose access token is old enough to certainly have
// expired server-side (session.Session.Stale) is treated the same as no
// session at all, and is proactively deleted — there is no token-refresh
// endpoint (docs/client/gap-analysis.md G-12), so continuing to report it
// as "logged in" would only defer a confusing failure to the next API call
// instead of a clear "please /login again."
func (s *Service) IsLoggedIn(ctx context.Context, chatID int64) (bool, error) {
	sess, err := s.sessions.Get(ctx, chatID)
	if errors.Is(err, session.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if sess.Stale() {
		_ = s.sessions.Delete(ctx, chatID)
		return false, nil
	}
	return true, nil
}

// Kind classifies why a Register or Login call failed, in terms the
// delivery layer can render a specific message for without knowing
// anything about the backend's wire format.
type Kind int

const (
	KindUnknown Kind = iota
	// KindTransport covers network failures and exhausted-retry server
	// errors — nothing user-specific to say, just "try again."
	KindTransport
	// KindInvalidInput covers a rejected email format or empty password.
	KindInvalidInput
	// KindDuplicateEmail covers signing up with an already-registered email.
	KindDuplicateEmail
	// KindInvalidCredentials covers a login rejected for bad credentials —
	// which, per the documented backend quirk in
	// docs/client/gap-analysis.md (G-06), is also what a nonexistent email
	// would ideally map to, but today only genuinely wrong credentials
	// reach this Kind; a nonexistent email currently surfaces as
	// KindTransport (see classifyLoginError).
	KindInvalidCredentials
)

// Error is the typed error Register and Login return. Message is the raw
// backend message, kept for logging — it is not guaranteed to be
// user-appropriate text (e.g. the terse "invalid input"), so callers
// should render their own copy based on Kind rather than displaying
// Message verbatim.
type Error struct {
	Kind    Kind
	Message string
	cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

func classifySignUpError(err error) *Error {
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		return &Error{Kind: KindUnknown, Message: err.Error(), cause: err}
	}
	if apiErr.Kind == apiclient.KindTransport {
		return &Error{Kind: KindTransport, Message: apiErr.Message, cause: err}
	}

	// See docs/client/backend-api-analysis.md §2: signup has no structured
	// error codes, only these specific message strings to match on.
	switch apiErr.Message {
	case "username already exists":
		return &Error{Kind: KindDuplicateEmail, Message: apiErr.Message, cause: err}
	case "invalid input":
		return &Error{Kind: KindInvalidInput, Message: apiErr.Message, cause: err}
	case "invalid username or password":
		// Signup reuses this login-flavored message for an empty
		// password; there is no way to further disambiguate it from the
		// backend's response alone (documented backend quirk).
		return &Error{Kind: KindInvalidInput, Message: apiErr.Message, cause: err}
	default:
		return &Error{Kind: KindTransport, Message: apiErr.Message, cause: err}
	}
}

func classifyLoginError(err error) *Error {
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		return &Error{Kind: KindUnknown, Message: err.Error(), cause: err}
	}
	if apiErr.Kind == apiclient.KindTransport {
		return &Error{Kind: KindTransport, Message: apiErr.Message, cause: err}
	}

	const httpForbidden = 403 // login's "wrong credentials" status; see backend-api-analysis.md §2
	if apiErr.StatusCode == httpForbidden {
		return &Error{Kind: KindInvalidCredentials, Message: apiErr.Message, cause: err}
	}

	// Login returns 500 for a nonexistent email (documented backend bug,
	// gap-analysis.md G-06), indistinguishable here from a genuine server
	// error. Both get the same generic, retry-suggesting treatment.
	return &Error{Kind: KindTransport, Message: apiErr.Message, cause: err}
}
