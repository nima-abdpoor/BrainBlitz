package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
)

// UserClient implements auth.UserAPI against the BrainBlitz user-service
// gateway (see docs/client/backend-api-analysis.md §2). It holds no
// business rules of its own — only request/response marshalling and error
// normalization via internal/apiclient.
type UserClient struct {
	client *Client
}

// NewUserClient wraps an already-configured Client (base URL pointed at
// the user-service gateway, e.g. ".../user-service").
func NewUserClient(client *Client) *UserClient {
	return &UserClient{client: client}
}

type signUpRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signUpResponse struct {
	DisplayName string `json:"displayName"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	ID           string `json:"id"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func jsonHeaders() http.Header {
	return http.Header{"Content-Type": []string{"application/json"}}
}

// SignUp calls POST /public/api/v1/signup. It never logs or returns the
// password beyond this single request.
func (c *UserClient) SignUp(ctx context.Context, email, password string) (auth.SignUpResult, error) {
	body, err := json.Marshal(signUpRequest{Email: email, Password: password})
	if err != nil {
		return auth.SignUpResult{}, fmt.Errorf("encoding signup request: %w", err)
	}

	resp, err := c.client.Do(ctx, http.MethodPost, "/public/api/v1/signup", body, jsonHeaders())
	if err != nil {
		return auth.SignUpResult{}, apiclient.NewTransportError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return auth.SignUpResult{}, fmt.Errorf("reading signup response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return auth.SignUpResult{}, apiclient.NewBackendError(resp.StatusCode, respBody)
	}

	var out signUpResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return auth.SignUpResult{}, fmt.Errorf("decoding signup response: %w", err)
	}
	return auth.SignUpResult{DisplayName: out.DisplayName}, nil
}

// Login calls POST /public/api/v1/login. It never logs or returns the
// password beyond this single request.
func (c *UserClient) Login(ctx context.Context, email, password string) (auth.LoginResult, error) {
	body, err := json.Marshal(loginRequest{Email: email, Password: password})
	if err != nil {
		return auth.LoginResult{}, fmt.Errorf("encoding login request: %w", err)
	}

	resp, err := c.client.Do(ctx, http.MethodPost, "/public/api/v1/login", body, jsonHeaders())
	if err != nil {
		return auth.LoginResult{}, apiclient.NewTransportError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return auth.LoginResult{}, fmt.Errorf("reading login response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return auth.LoginResult{}, apiclient.NewBackendError(resp.StatusCode, respBody)
	}

	var out loginResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return auth.LoginResult{}, fmt.Errorf("decoding login response: %w", err)
	}
	return auth.LoginResult{UserID: out.ID, AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}, nil
}

type profileResponse struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	CreatedAt   int64  `json:"createdAt"`
}

// Profile calls GET /api/v1/profile with accessToken as a Bearer credential
// (see docs/client/backend-api-analysis.md §2). This route sits behind
// Traefik's ForwardAuth in production, which is what actually enforces the
// token there, but the bot sends the header itself regardless so this
// client works the same way against any deployment.
func (c *UserClient) Profile(ctx context.Context, accessToken string) (profile.Profile, error) {
	headers := http.Header{"Authorization": []string{"Bearer " + accessToken}}

	resp, err := c.client.Do(ctx, http.MethodGet, "/api/v1/profile", nil, headers)
	if err != nil {
		return profile.Profile{}, apiclient.NewTransportError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("reading profile response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return profile.Profile{}, apiclient.NewBackendError(resp.StatusCode, respBody)
	}

	var out profileResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return profile.Profile{}, fmt.Errorf("decoding profile response: %w", err)
	}
	return profile.Profile{
		Username:    out.Username,
		DisplayName: out.DisplayName,
		Role:        out.Role,
		CreatedAt:   time.UnixMilli(out.CreatedAt).UTC(),
	}, nil
}
