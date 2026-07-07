package apiclient

import (
	"errors"
	"testing"
)

func TestNewBackendError_ObjectBody(t *testing.T) {
	err := NewBackendError(400, []byte(`{"message":"invalid input","error":"invalid input"}`))

	if err.Kind != KindBackend {
		t.Errorf("Kind = %v, want KindBackend", err.Kind)
	}
	if err.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", err.StatusCode)
	}
	if err.Message != "invalid input" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid input")
	}
	if err.Retryable {
		t.Error("backend (4xx) errors should not be marked retryable")
	}
}

func TestNewBackendError_BareStringBody(t *testing.T) {
	// user-service's missing X-User-ID case and match-service's category
	// error both return a bare JSON string rather than an object.
	err := NewBackendError(400, []byte(`"Invalid user id"`))

	if err.Message != "Invalid user id" {
		t.Errorf("Message = %q, want %q", err.Message, "Invalid user id")
	}
}

func TestNewBackendError_UnparsableBody(t *testing.T) {
	err := NewBackendError(500, []byte("not json at all"))

	if err.Message != "not json at all" {
		t.Errorf("Message = %q, want the raw body as a fallback", err.Message)
	}
}

func TestNewTransportError(t *testing.T) {
	cause := errors.New("connection refused")
	err := NewTransportError(cause)

	if err.Kind != KindTransport {
		t.Errorf("Kind = %v, want KindTransport", err.Kind)
	}
	if !err.Retryable {
		t.Error("transport errors should be marked retryable")
	}
	if err.Message != cause.Error() {
		t.Errorf("Message = %q, want %q", err.Message, cause.Error())
	}
}

func TestError_ErrorString(t *testing.T) {
	err := &Error{Message: "boom"}
	if err.Error() != "boom" {
		t.Errorf("Error() = %q, want %q", err.Error(), "boom")
	}

	empty := &Error{}
	if empty.Error() == "" {
		t.Error("Error() should never return an empty string")
	}
}
