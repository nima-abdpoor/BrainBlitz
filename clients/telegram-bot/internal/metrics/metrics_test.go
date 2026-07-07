package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecorder_CommandHandled(t *testing.T) {
	r := New()
	r.CommandHandled("start")
	r.CommandHandled("start")
	r.CommandHandled("help")

	if got := testutil.ToFloat64(r.commandsTotal.WithLabelValues("start")); got != 2 {
		t.Errorf("bot_commands_total{command=start} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.commandsTotal.WithLabelValues("help")); got != 1 {
		t.Errorf("bot_commands_total{command=help} = %v, want 1", got)
	}
}

func TestRecorder_CallbackHandled(t *testing.T) {
	r := New()
	r.CallbackHandled("play")

	if got := testutil.ToFloat64(r.callbacksTotal.WithLabelValues("play")); got != 1 {
		t.Errorf("bot_callbacks_total{callback=play} = %v, want 1", got)
	}
}

func TestRecorder_ObserveHTTPRequest_CountsErrorsSeparately(t *testing.T) {
	r := New()
	r.ObserveHTTPRequest("GET", "/profile", 10*time.Millisecond, nil)
	r.ObserveHTTPRequest("GET", "/profile", 20*time.Millisecond, errAny)

	if got := testutil.ToFloat64(r.httpRequestErrors.WithLabelValues("GET", "/profile")); got != 1 {
		t.Errorf("bot_http_request_errors_total = %v, want 1 (only the failed attempt)", got)
	}
	if got := testutil.CollectAndCount(r.httpRequestDuration); got != 1 {
		t.Errorf("distinct httpRequestDuration series = %d, want 1 (same method+path)", got)
	}
}

var errAny = &testError{}

type testError struct{}

func (*testError) Error() string { return "boom" }

func TestRecorder_WSConnectionsGauge(t *testing.T) {
	r := New()
	r.WSConnectionOpened()
	r.WSConnectionOpened()
	r.WSConnectionClosed()

	if got := testutil.ToFloat64(r.activeWSConnections); got != 1 {
		t.Errorf("bot_active_ws_connections = %v, want 1", got)
	}
}

func TestRecorder_Handler_ServesPrometheusFormat(t *testing.T) {
	r := New()
	r.CommandHandled("start")

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bot_commands_total") {
		t.Error("response body does not contain bot_commands_total")
	}
}

func TestNew_MultipleRecordersDoNotPanic(t *testing.T) {
	// Each Recorder owns its own registry (not prometheus.DefaultRegisterer),
	// so constructing several — as parallel tests in this package do — must
	// never panic on a duplicate metric registration.
	_ = New()
	_ = New()
}
