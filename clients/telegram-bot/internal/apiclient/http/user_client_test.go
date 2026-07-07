package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
)

func jsonServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func newUserClient(baseURL string) *UserClient {
	return NewUserClient(NewClient(baseURL, 2*time.Second, testLogger(), WithMaxRetries(0)))
}

func TestUserClient_SignUp_Success(t *testing.T) {
	server := jsonServer(t, http.StatusOK, `{"displayName":"alice"}`)
	defer server.Close()

	result, err := newUserClient(server.URL).SignUp(context.Background(), "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}
	if result.DisplayName != "alice" {
		t.Errorf("DisplayName = %q, want %q", result.DisplayName, "alice")
	}
}

func TestUserClient_SignUp_DuplicateEmail(t *testing.T) {
	server := jsonServer(t, http.StatusBadRequest, `{"message":"username already exists"}`)
	defer server.Close()

	_, err := newUserClient(server.URL).SignUp(context.Background(), "alice@example.com", "hunter2")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("SignUp error = %v, want an *apiclient.Error", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if apiErr.Message != "username already exists" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "username already exists")
	}
}

func TestUserClient_SignUp_RequestBodyIsWellFormed(t *testing.T) {
	var received signUpRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"displayName":"alice"}`))
	}))
	defer server.Close()

	_, err := newUserClient(server.URL).SignUp(context.Background(), "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}
	if received.Email != "alice@example.com" || received.Password != "hunter2" {
		t.Errorf("server received %+v, want the email/password passed to SignUp", received)
	}
}

func TestUserClient_Login_Success(t *testing.T) {
	server := jsonServer(t, http.StatusOK, `{"id":"42","accessToken":"at","refreshToken":"rt"}`)
	defer server.Close()

	result, err := newUserClient(server.URL).Login(context.Background(), "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if result.UserID != "42" || result.AccessToken != "at" || result.RefreshToken != "rt" {
		t.Errorf("Login result = %+v, want the server's tokens", result)
	}
}

func TestUserClient_Login_InvalidCredentials(t *testing.T) {
	server := jsonServer(t, http.StatusForbidden, `{"message":"invalid username or password"}`)
	defer server.Close()

	_, err := newUserClient(server.URL).Login(context.Background(), "alice@example.com", "wrong")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Login error = %v, want an *apiclient.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

func TestUserClient_Login_AmbiguousServerError(t *testing.T) {
	// Per backend-api-analysis.md §2, a nonexistent email returns 500 with
	// the generic message, same as a real server failure.
	server := jsonServer(t, http.StatusInternalServerError, `{"message":"something went wrong"}`)
	defer server.Close()

	_, err := newUserClient(server.URL).Login(context.Background(), "ghost@example.com", "whatever")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Login error = %v, want an *apiclient.Error", err)
	}
	if apiErr.Kind != apiclient.KindTransport {
		t.Errorf("Kind = %v, want KindTransport (Client.Do exhausts retries on 5xx before returning)", apiErr.Kind)
	}
}

func TestUserClient_Login_TransportFailure(t *testing.T) {
	server := jsonServer(t, http.StatusOK, `{}`)
	server.Close() // simulate the backend being unreachable

	_, err := newUserClient(server.URL).Login(context.Background(), "alice@example.com", "hunter2")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Login error = %v, want an *apiclient.Error", err)
	}
	if apiErr.Kind != apiclient.KindTransport {
		t.Errorf("Kind = %v, want KindTransport", apiErr.Kind)
	}
}

func TestUserClient_Profile_Success(t *testing.T) {
	server := jsonServer(t, http.StatusOK, `{"id":"123","username":"alice@example.com","displayName":"alice","role":"user","createdAt":1719999999000,"updatedAt":1719999999000}`)
	defer server.Close()

	result, err := newUserClient(server.URL).Profile(context.Background(), "at")
	if err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if result.Username != "alice@example.com" || result.DisplayName != "alice" || result.Role != "user" {
		t.Errorf("Profile result = %+v, want the server's fields", result)
	}
	wantCreatedAt := time.UnixMilli(1719999999000).UTC()
	if !result.CreatedAt.Equal(wantCreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", result.CreatedAt, wantCreatedAt)
	}
}

func TestUserClient_Profile_SendsBearerToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"username":"alice@example.com","displayName":"alice","role":"user","createdAt":0}`))
	}))
	defer server.Close()

	if _, err := newUserClient(server.URL).Profile(context.Background(), "my-token"); err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer my-token")
	}
}

func TestUserClient_Profile_NotFound(t *testing.T) {
	server := jsonServer(t, http.StatusNotFound, `{"message":"user not found"}`)
	defer server.Close()

	_, err := newUserClient(server.URL).Profile(context.Background(), "at")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Profile error = %v, want an *apiclient.Error", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestUserClient_Profile_TransportFailure(t *testing.T) {
	server := jsonServer(t, http.StatusOK, `{}`)
	server.Close() // simulate the backend being unreachable

	_, err := newUserClient(server.URL).Profile(context.Background(), "at")

	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Profile error = %v, want an *apiclient.Error", err)
	}
	if apiErr.Kind != apiclient.KindTransport {
		t.Errorf("Kind = %v, want KindTransport", apiErr.Kind)
	}
}
