package commands

import (
	"context"
	"errors"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
)

// scriptedUserAPI is a fuller auth.UserAPI double than fakeUserAPI in
// start_test.go: it lets each test script exactly what SignUp/Login should
// return, so the full register/login conversation can be driven end to end
// through Handlers without any real HTTP call.
type scriptedUserAPI struct {
	signUpResult auth.SignUpResult
	signUpErr    error
	loginResult  auth.LoginResult
	loginErr     error
}

func (s scriptedUserAPI) SignUp(context.Context, string, string) (auth.SignUpResult, error) {
	return s.signUpResult, s.signUpErr
}

func (s scriptedUserAPI) Login(context.Context, string, string) (auth.LoginResult, error) {
	return s.loginResult, s.loginErr
}

func newScriptedHandlers(api scriptedUserAPI) *Handlers {
	sessions := session.NewMemoryStore()
	authSvc := auth.NewService(api, sessions)
	profileSvc := profile.NewService(fakeProfileAPI{}, sessions)
	return NewHandlers(authSvc, profileSvc, newTestMatchmakingManager(sessions), newTestGameManager(), conversation.NewMemoryStore())
}

func textMessage(chatID int64, text string) *tgbotapi.Message {
	return &tgbotapi.Message{Text: text, Chat: &tgbotapi.Chat{ID: chatID}}
}

func messageText(t *testing.T, c tgbotapi.Chattable) string {
	t.Helper()
	msg, ok := c.(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("reply has type %T, want tgbotapi.MessageConfig", c)
	}
	return msg.Text
}

func TestHandleText_NoConversationInProgress(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{})

	reply, ok, err := h.HandleText(context.Background(), textMessage(1, "just chatting"))
	if err != nil {
		t.Fatalf("HandleText returned error: %v", err)
	}
	if ok {
		t.Error("ok = true, want false when no conversation is in progress")
	}
	if reply != nil {
		t.Error("reply is non-nil, want nil when no conversation is in progress")
	}
}

func TestRegisterFlow_Success(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{
		signUpResult: auth.SignUpResult{DisplayName: "alice"},
		loginResult:  auth.LoginResult{UserID: "42", AccessToken: "at", RefreshToken: "rt"},
	})
	ctx := context.Background()
	chatID := int64(100)

	if _, err := h.RegisterCallback(ctx, &tgbotapi.CallbackQuery{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}}}); err != nil {
		t.Fatalf("RegisterCallback returned error: %v", err)
	}

	reply, ok, err := h.HandleText(ctx, textMessage(chatID, "alice@example.com"))
	if err != nil || !ok {
		t.Fatalf("HandleText(email) = (ok=%v, err=%v)", ok, err)
	}
	if state, _ := h.conversations.Get(chatID); state.Step != conversation.StepAwaitingRegisterPassword || state.Email != "alice@example.com" {
		t.Fatalf("conversation state = %+v, want awaiting password with buffered email", state)
	}
	_ = reply

	reply, ok, err = h.HandleText(ctx, textMessage(chatID, "hunter2"))
	if err != nil || !ok {
		t.Fatalf("HandleText(password) = (ok=%v, err=%v)", ok, err)
	}
	if got := messageText(t, reply); got == "" {
		t.Error("success reply text is empty")
	}

	if _, exists := h.conversations.Get(chatID); exists {
		t.Error("conversation state must be cleared once registration completes")
	}
	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if !loggedIn {
		t.Error("a successful register must leave the chat logged in (session persisted)")
	}
}

func TestRegisterFlow_DuplicateEmail(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{
		signUpErr: apiclient.NewBackendError(400, []byte(`{"message":"username already exists"}`)),
	})
	ctx := context.Background()
	chatID := int64(101)

	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingRegisterPassword, Email: "alice@example.com"})
	reply, ok, err := h.HandleText(ctx, textMessage(chatID, "hunter2"))
	if err != nil || !ok {
		t.Fatalf("HandleText(password) = (ok=%v, err=%v)", ok, err)
	}

	msg, isMsg := reply.(tgbotapi.MessageConfig)
	if !isMsg {
		t.Fatalf("reply has type %T, want tgbotapi.MessageConfig", reply)
	}
	if msg.ReplyMarkup == nil {
		t.Error("duplicate-email reply should offer a Login button")
	}

	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if loggedIn {
		t.Error("a failed registration must not leave the chat logged in")
	}
}

func TestLoginFlow_Success(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{
		loginResult: auth.LoginResult{UserID: "42", AccessToken: "at", RefreshToken: "rt"},
	})
	ctx := context.Background()
	chatID := int64(200)

	if _, err := h.LoginCallback(ctx, &tgbotapi.CallbackQuery{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}}}); err != nil {
		t.Fatalf("LoginCallback returned error: %v", err)
	}
	if _, ok, err := h.HandleText(ctx, textMessage(chatID, "alice@example.com")); err != nil || !ok {
		t.Fatalf("HandleText(email) = (ok=%v, err=%v)", ok, err)
	}

	reply, ok, err := h.HandleText(ctx, textMessage(chatID, "hunter2"))
	if err != nil || !ok {
		t.Fatalf("HandleText(password) = (ok=%v, err=%v)", ok, err)
	}
	if got := messageText(t, reply); got == "" {
		t.Error("success reply text is empty")
	}

	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if !loggedIn {
		t.Error("a successful login must leave the chat logged in (session persisted)")
	}
}

func TestLoginFlow_InvalidCredentials(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{
		loginErr: apiclient.NewBackendError(403, []byte(`{"message":"invalid username or password"}`)),
	})
	ctx := context.Background()
	chatID := int64(201)

	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingLoginPassword, Email: "alice@example.com"})
	reply, ok, err := h.HandleText(ctx, textMessage(chatID, "wrong"))
	if err != nil || !ok {
		t.Fatalf("HandleText(password) = (ok=%v, err=%v)", ok, err)
	}
	if got := messageText(t, reply); got == "" {
		t.Error("invalid-credentials reply text is empty")
	}

	loggedIn, err := h.auth.IsLoggedIn(ctx, chatID)
	if err != nil {
		t.Fatalf("IsLoggedIn returned error: %v", err)
	}
	if loggedIn {
		t.Error("a failed login must not leave the chat logged in")
	}
	if _, exists := h.conversations.Get(chatID); exists {
		t.Error("conversation state must be cleared once the login attempt completes, win or lose")
	}
}

func TestLoginFlow_TransportFailure(t *testing.T) {
	h := newScriptedHandlers(scriptedUserAPI{
		loginErr: apiclient.NewTransportError(errors.New("connection refused")),
	})
	ctx := context.Background()
	chatID := int64(202)

	h.conversations.Set(chatID, conversation.State{Step: conversation.StepAwaitingLoginPassword, Email: "alice@example.com"})
	reply, ok, err := h.HandleText(ctx, textMessage(chatID, "hunter2"))
	if err != nil || !ok {
		t.Fatalf("HandleText(password) = (ok=%v, err=%v)", ok, err)
	}
	if got := messageText(t, reply); got == "" {
		t.Error("transport-failure reply text is empty")
	}
}
