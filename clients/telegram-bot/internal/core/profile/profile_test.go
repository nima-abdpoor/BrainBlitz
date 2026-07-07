package profile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

type fakeUserAPI struct {
	result Profile
	err    error

	calls     int
	lastToken string
}

func (f *fakeUserAPI) Profile(_ context.Context, accessToken string) (Profile, error) {
	f.calls++
	f.lastToken = accessToken
	return f.result, f.err
}

func TestService_Get_Success(t *testing.T) {
	want := Profile{Username: "alice@example.com", DisplayName: "alice", Role: "user", CreatedAt: time.Unix(1719999999, 0)}
	api := &fakeUserAPI{result: want}
	sessions := session.NewMemoryStore()
	_ = sessions.Save(context.Background(), session.Session{TelegramChatID: 100, AccessToken: "at"})
	svc := NewService(api, sessions)

	got, err := svc.Get(context.Background(), 100)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
	if api.lastToken != "at" {
		t.Errorf("Profile called with token %q, want the session's access token %q", api.lastToken, "at")
	}
}

func TestService_Get_NotLoggedIn(t *testing.T) {
	svc := NewService(&fakeUserAPI{}, session.NewMemoryStore())

	_, err := svc.Get(context.Background(), 999)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Get error = %v, want ErrNotLoggedIn", err)
	}
}

func TestService_Get_StaleSessionIsTreatedAsNotLoggedInAndCleared(t *testing.T) {
	api := &fakeUserAPI{result: Profile{Username: "alice@example.com"}}
	sessions := session.NewMemoryStore()
	stale := session.Session{TelegramChatID: 100, AccessToken: "at", TokenIssuedAt: time.Now().Add(-session.AccessTokenMaxAge - time.Hour)}
	_ = sessions.Save(context.Background(), stale)
	svc := NewService(api, sessions)

	_, err := svc.Get(context.Background(), 100)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Get error = %v, want ErrNotLoggedIn", err)
	}
	if api.calls != 0 {
		t.Error("Get must not call the API with a known-stale token")
	}
	if _, err := sessions.Get(context.Background(), 100); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("stale session should have been deleted, Get error = %v, want ErrNotFound", err)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	api := &fakeUserAPI{err: apiclient.NewBackendError(404, []byte(`{"message":"user not found"}`))}
	sessions := session.NewMemoryStore()
	_ = sessions.Save(context.Background(), session.Session{TelegramChatID: 100, AccessToken: "at"})
	svc := NewService(api, sessions)

	_, err := svc.Get(context.Background(), 100)

	var profileErr *Error
	if !errors.As(err, &profileErr) {
		t.Fatalf("Get error = %v, want a *profile.Error", err)
	}
	if profileErr.Kind != KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", profileErr.Kind)
	}
}

func TestService_Get_ServerError(t *testing.T) {
	api := &fakeUserAPI{err: apiclient.NewBackendError(500, []byte(`{"message":"something went wrong"}`))}
	sessions := session.NewMemoryStore()
	_ = sessions.Save(context.Background(), session.Session{TelegramChatID: 100, AccessToken: "at"})
	svc := NewService(api, sessions)

	_, err := svc.Get(context.Background(), 100)

	var profileErr *Error
	if !errors.As(err, &profileErr) {
		t.Fatalf("Get error = %v, want a *profile.Error", err)
	}
	if profileErr.Kind != KindTransport {
		t.Errorf("Kind = %v, want KindTransport", profileErr.Kind)
	}
}

func TestService_Get_TransportFailure(t *testing.T) {
	api := &fakeUserAPI{err: apiclient.NewTransportError(errors.New("connection refused"))}
	sessions := session.NewMemoryStore()
	_ = sessions.Save(context.Background(), session.Session{TelegramChatID: 100, AccessToken: "at"})
	svc := NewService(api, sessions)

	_, err := svc.Get(context.Background(), 100)

	var profileErr *Error
	if !errors.As(err, &profileErr) {
		t.Fatalf("Get error = %v, want a *profile.Error", err)
	}
	if profileErr.Kind != KindTransport {
		t.Errorf("Kind = %v, want KindTransport", profileErr.Kind)
	}
}
