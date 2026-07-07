//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	apihttp "github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/http"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
)

// TestIntegration_RegisterLoginProfile exercises the full chain a real
// /register conversation drives: core/auth.Service.Register (signup, then
// login) against the real user-service, followed by core/profile.Service.Get
// reading the session that Register persisted. This is the one path unit
// tests (which fake UserAPI) can't catch: a wire-format mismatch between
// this client and the actual backend.
func TestIntegration_RegisterLoginProfile(t *testing.T) {
	requireBackend(t, userServiceURL())

	userClient := apihttp.NewUserClient(apihttp.NewClient(userServiceURL(), 10*time.Second, discardLogger()))
	sessions := session.NewMemoryStore()
	authSvc := auth.NewService(userClient, sessions)
	profileSvc := profile.NewService(userClient, sessions)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const chatID = int64(1)
	email := uniqueEmail("register")

	if _, err := authSvc.Register(ctx, chatID, email, integrationPassword); err != nil {
		t.Fatalf("Register(%q) = %v, want success", email, err)
	}

	if loggedIn, err := authSvc.IsLoggedIn(ctx, chatID); err != nil || !loggedIn {
		t.Fatalf("IsLoggedIn = (%v, %v) after a successful Register, want (true, nil)", loggedIn, err)
	}

	prof, err := profileSvc.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("profileSvc.Get after Register = %v, want success", err)
	}
	if prof.Username == "" {
		t.Error("Profile.Username is empty")
	}
	if prof.DisplayName == "" {
		t.Error("Profile.DisplayName is empty")
	}
	if prof.CreatedAt.IsZero() {
		t.Error("Profile.CreatedAt is zero")
	}
}

// TestIntegration_LoginWrongPassword confirms the real backend's
// invalid-credentials response classifies the same way this bot's fakes do
// (auth.KindInvalidCredentials), since that classification drives which
// reply text the user sees.
func TestIntegration_LoginWrongPassword(t *testing.T) {
	requireBackend(t, userServiceURL())

	userClient := apihttp.NewUserClient(apihttp.NewClient(userServiceURL(), 10*time.Second, discardLogger()))
	sessions := session.NewMemoryStore()
	authSvc := auth.NewService(userClient, sessions)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const chatID = int64(2)
	email := uniqueEmail("badlogin")

	if _, err := authSvc.Register(ctx, chatID, email, integrationPassword); err != nil {
		t.Fatalf("Register(%q) = %v, want success", email, err)
	}

	// A fresh chat, never logged in, attempting login with the wrong password.
	const otherChatID = int64(3)
	if _, err := authSvc.Login(ctx, otherChatID, email, "definitely-wrong-password"); err == nil {
		t.Fatal("Login with wrong password succeeded, want an error")
	} else if authErr, ok := err.(*auth.Error); !ok || authErr.Kind != auth.KindInvalidCredentials {
		t.Errorf("Login error = %v, want *auth.Error{Kind: KindInvalidCredentials}", err)
	}
}
