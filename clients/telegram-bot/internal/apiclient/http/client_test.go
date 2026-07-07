package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger())

	resp, err := c.Do(context.Background(), http.MethodGet, "/ping", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server called %d times, want 1", got)
	}
}

func TestDo_RetriesOnServerErrorThenSucceeds(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(3), WithBackoff(time.Millisecond))

	resp, err := c.Do(context.Background(), http.MethodGet, "/flaky", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("server called %d times, want 3", got)
	}
}

func TestDo_DoesNotRetryOnClientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(3), WithBackoff(time.Millisecond))

	resp, err := c.Do(context.Background(), http.MethodPost, "/bad", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error for a 4xx response, want the response returned as-is: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server called %d times, want 1 (no retries on 4xx)", got)
	}
}

func TestDo_ExhaustsRetriesAndReturnsError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(2), WithBackoff(time.Millisecond))

	_, err := c.Do(context.Background(), http.MethodGet, "/always-down", nil, nil)
	if err == nil {
		t.Fatal("Do should return an error once retries are exhausted")
	}
	if got := atomic.LoadInt32(&calls); got != 3 { // initial attempt + 2 retries
		t.Errorf("server called %d times, want 3", got)
	}
}

func TestDo_SendsBodyAndHeaders(t *testing.T) {
	var receivedBody []byte
	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger())
	headers := http.Header{"X-Custom": []string{"value"}}

	resp, err := c.Do(context.Background(), http.MethodPost, "/echo", []byte(`{"hello":"world"}`), headers)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()

	if string(receivedBody) != `{"hello":"world"}` {
		t.Errorf("body = %q, want %q", receivedBody, `{"hello":"world"}`)
	}
	if receivedHeader != "value" {
		t.Errorf("X-Custom header = %q, want %q", receivedHeader, "value")
	}
}

// fakeLimiter is a RateLimiter double recording every Wait call so tests
// can assert the Client actually consults it before each attempt.
type fakeLimiter struct {
	calls int32
}

func (f *fakeLimiter) Wait(context.Context) error {
	atomic.AddInt32(&f.calls, 1)
	return nil
}

func TestDo_WaitsOnRateLimiterBeforeEveryAttempt(t *testing.T) {
	var serverCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&serverCalls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	lim := &fakeLimiter{}
	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(3), WithBackoff(time.Millisecond), WithRateLimiter(lim))

	resp, err := c.Do(context.Background(), http.MethodGet, "/limited", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()

	if got := atomic.LoadInt32(&lim.calls); got != 2 {
		t.Errorf("limiter Wait calls = %d, want 2 (once per attempt)", got)
	}
}

// fakeMetrics is a Metrics double recording every observed call.
type fakeMetrics struct {
	mu    sync.Mutex
	calls []struct {
		method, path string
		err          error
	}
}

func (f *fakeMetrics) ObserveHTTPRequest(method, path string, _ time.Duration, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		method, path string
		err          error
	}{method, path, err})
}

func TestDo_RecordsMetricsPerAttempt(t *testing.T) {
	var serverCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&serverCalls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := &fakeMetrics{}
	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(3), WithBackoff(time.Millisecond), WithMetrics(m))

	resp, err := c.Do(context.Background(), http.MethodGet, "/metered", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()

	if len(m.calls) != 2 {
		t.Fatalf("recorded %d attempts, want 2 (one failed, one succeeded)", len(m.calls))
	}
	if m.calls[0].err == nil {
		t.Error("first attempt should have recorded a non-nil error")
	}
	if m.calls[1].err != nil {
		t.Errorf("second attempt should have recorded nil error, got %v", m.calls[1].err)
	}
	for _, c := range m.calls {
		if c.method != http.MethodGet || c.path != "/metered" {
			t.Errorf("recorded call = %+v, want method=%s path=/metered", c, http.MethodGet)
		}
	}
}

func TestDo_ContextCancellationStopsRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewClient(server.URL, 2*time.Second, testLogger(),
		WithMaxRetries(5), WithBackoff(50*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	_, err := c.Do(ctx, http.MethodGet, "/slow-fail", nil, nil)
	if err == nil {
		t.Fatal("Do should return an error when the context is cancelled mid-retry")
	}
}
