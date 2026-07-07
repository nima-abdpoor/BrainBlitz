package session

import (
	"testing"
	"time"
)

func TestSession_Stale_FreshTokenIsNotStale(t *testing.T) {
	s := Session{TokenIssuedAt: time.Now()}
	if s.Stale() {
		t.Error("a just-issued token must not be stale")
	}
}

func TestSession_Stale_OldTokenIsStale(t *testing.T) {
	s := Session{TokenIssuedAt: time.Now().Add(-AccessTokenMaxAge - time.Minute)}
	if !s.Stale() {
		t.Error("a token older than AccessTokenMaxAge must be stale")
	}
}

func TestSession_Stale_ExactlyAtMaxAgeIsNotStale(t *testing.T) {
	s := Session{TokenIssuedAt: time.Now().Add(-AccessTokenMaxAge + time.Second)}
	if s.Stale() {
		t.Error("a token just under AccessTokenMaxAge must not be stale")
	}
}

func TestSession_Stale_ZeroValueIsNotStale(t *testing.T) {
	// A zero-value TokenIssuedAt shouldn't happen in production
	// (auth.Service.Login always sets it), but must not be treated as
	// maximally stale — see Stale's doc comment.
	var s Session
	if s.Stale() {
		t.Error("a zero-value TokenIssuedAt must not be treated as stale")
	}
}
