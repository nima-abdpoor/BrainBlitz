package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// fakeUserAPI lets tests control SignUp/Login outcomes without any HTTP
// involved, and records the credentials each call received so tests can
// assert on what was (and wasn't) called.
type fakeUserAPI struct {
	signUpResult SignUpResult
	signUpErr    error
	loginResult  LoginResult
	loginErr     error

	signUpCalls int
	loginCalls  int
	lastEmail   string
	lastPass    string
}

func (f *fakeUserAPI) SignUp(_ context.Context, email, password string) (SignUpResult, error) {
	f.signUpCalls++
	f.lastEmail, f.lastPass = email, password
	return f.signUpResult, f.signUpErr
}

func (f *fakeUserAPI) Login(_ context.Context, email, password string) (LoginResult, error) {
	f.loginCalls++
	f.lastEmail, f.lastPass = email, password
	return f.loginResult, f.loginErr
}

func TestService_Login_Success(t *testing.T) {
	api := &fakeUserAPI{loginResult: LoginResult{UserID: "42", AccessToken: "at", RefreshToken: "rt"}}
	sessions := session.NewMemoryStore()
	svc := NewService(api, sessions)

	got, err := svc.Login(context.Background(), 100, "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if got.UserID != "42" || got.AccessToken != "at" || got.RefreshToken != "rt" {
		t.Errorf("Login result = %+v, want tokens from the fake API", got)
	}

	// Verify session persistence: a later, independent Get must return the
	// same session Login just saved.
	stored, err := sessions.Get(context.Background(), 100)
	if err != nil {
		t.Fatalf("session was not persisted: %v", err)
	}
	if stored != got {
		t.Errorf("persisted session = %+v, want %+v", stored, got)
	}
}

func TestService_Login_InvalidCredentials(t *testing.T) {
	api := &fakeUserAPI{loginErr: apiclient.NewBackendError(403, []byte(`{"message":"invalid username or password"}`))}
	sessions := session.NewMemoryStore()
	svc := NewService(api, sessions)

	_, err := svc.Login(context.Background(), 100, "alice@example.com", "wrong")

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("Login error = %v, want an *auth.Error", err)
	}
	if authErr.Kind != KindInvalidCredentials {
		t.Errorf("Kind = %v, want KindInvalidCredentials", authErr.Kind)
	}

	if _, err := sessions.Get(context.Background(), 100); err == nil {
		t.Error("a failed login must not persist a session")
	}
}

func TestService_Login_AmbiguousServerError(t *testing.T) {
	// Per gap-analysis.md G-06, a nonexistent email currently returns 500,
	// indistinguishable from a real server failure.
	api := &fakeUserAPI{loginErr: apiclient.NewBackendError(500, []byte(`{"message":"something went wrong"}`))}
	svc := NewService(api, session.NewMemoryStore())

	_, err := svc.Login(context.Background(), 100, "ghost@example.com", "whatever")

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("Login error = %v, want an *auth.Error", err)
	}
	if authErr.Kind != KindTransport {
		t.Errorf("Kind = %v, want KindTransport for the ambiguous 500 case", authErr.Kind)
	}
}

func TestService_Login_TransportFailure(t *testing.T) {
	api := &fakeUserAPI{loginErr: apiclient.NewTransportError(errors.New("connection refused"))}
	svc := NewService(api, session.NewMemoryStore())

	_, err := svc.Login(context.Background(), 100, "alice@example.com", "hunter2")

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("Login error = %v, want an *auth.Error", err)
	}
	if authErr.Kind != KindTransport {
		t.Errorf("Kind = %v, want KindTransport", authErr.Kind)
	}
}

func TestService_Register_Success(t *testing.T) {
	api := &fakeUserAPI{
		signUpResult: SignUpResult{DisplayName: "alice"},
		loginResult:  LoginResult{UserID: "42", AccessToken: "at", RefreshToken: "rt"},
	}
	sessions := session.NewMemoryStore()
	svc := NewService(api, sessions)

	got, err := svc.Register(context.Background(), 100, "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if got.DisplayName != "alice" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "alice")
	}
	if api.signUpCalls != 1 || api.loginCalls != 1 {
		t.Errorf("signUpCalls=%d loginCalls=%d, want 1 and 1 (register must auto-chain into login)", api.signUpCalls, api.loginCalls)
	}

	if _, err := sessions.Get(context.Background(), 100); err != nil {
		t.Errorf("Register success must persist a session: %v", err)
	}
}

func TestService_Register_DuplicateEmail(t *testing.T) {
	api := &fakeUserAPI{signUpErr: apiclient.NewBackendError(400, []byte(`{"message":"username already exists"}`))}
	sessions := session.NewMemoryStore()
	svc := NewService(api, sessions)

	_, err := svc.Register(context.Background(), 100, "alice@example.com", "hunter2")

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("Register error = %v, want an *auth.Error", err)
	}
	if authErr.Kind != KindDuplicateEmail {
		t.Errorf("Kind = %v, want KindDuplicateEmail", authErr.Kind)
	}
	if api.loginCalls != 0 {
		t.Error("Register must not attempt login when signup itself failed")
	}
}

func TestService_Register_InvalidInput(t *testing.T) {
	api := &fakeUserAPI{signUpErr: apiclient.NewBackendError(400, []byte(`{"message":"invalid input"}`))}
	svc := NewService(api, session.NewMemoryStore())

	_, err := svc.Register(context.Background(), 100, "not-an-email", "hunter2")

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("Register error = %v, want an *auth.Error", err)
	}
	if authErr.Kind != KindInvalidInput {
		t.Errorf("Kind = %v, want KindInvalidInput", authErr.Kind)
	}
}

func TestService_IsLoggedIn(t *testing.T) {
	sessions := session.NewMemoryStore()
	svc := NewService(&fakeUserAPI{}, sessions)
	ctx := context.Background()

	loggedIn, err := svc.IsLoggedIn(ctx, 100)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if loggedIn {
		t.Error("IsLoggedIn should be false before any Login")
	}

	_ = sessions.Save(ctx, sessionFor(100))

	loggedIn, err = svc.IsLoggedIn(ctx, 100)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if !loggedIn {
		t.Error("IsLoggedIn should be true after a session is saved")
	}
}

func TestService_IsLoggedIn_StaleSessionIsTreatedAsLoggedOutAndCleared(t *testing.T) {
	sessions := session.NewMemoryStore()
	svc := NewService(&fakeUserAPI{}, sessions)
	ctx := context.Background()
	stale := sessionFor(100)
	stale.TokenIssuedAt = time.Now().Add(-session.AccessTokenMaxAge - time.Hour)
	_ = sessions.Save(ctx, stale)

	loggedIn, err := svc.IsLoggedIn(ctx, 100)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if loggedIn {
		t.Error("IsLoggedIn should be false for a session past AccessTokenMaxAge")
	}

	if _, err := sessions.Get(ctx, 100); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("stale session should have been deleted, Get error = %v, want ErrNotFound", err)
	}
}

func TestService_Logout(t *testing.T) {
	sessions := session.NewMemoryStore()
	svc := NewService(&fakeUserAPI{}, sessions)
	ctx := context.Background()
	_ = sessions.Save(ctx, sessionFor(100))

	if err := svc.Logout(ctx, 100); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}

	loggedIn, err := svc.IsLoggedIn(ctx, 100)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if loggedIn {
		t.Error("IsLoggedIn should be false after Logout")
	}
}

func sessionFor(chatID int64) session.Session {
	return session.Session{TelegramChatID: chatID, UserID: "u", AccessToken: "a", TokenIssuedAt: time.Now()}
}
