// Package metrics instruments this bot process for Prometheus. It is
// deliberately NOT a global registry with package-level collectors — every
// collector lives on a *Recorder built once in cmd/bot/main.go and injected
// into whatever needs it (telegram.Bot, http.Client,
// matchmaking.Manager), consistent with this project's "avoid global
// state" requirement (see docs/client/client-architecture.md's note on the
// logger). This is also the specific gap the backend itself has (the main
// repo wires Prometheus/Grafana in docker-compose but no backend service
// actually exposes application metrics — docs/context/07-known-issues.md
// #7) — this bot does not repeat it.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Recorder holds every collector this bot instruments.
type Recorder struct {
	registry *prometheus.Registry

	commandsTotal       *prometheus.CounterVec
	callbacksTotal      *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpRequestErrors   *prometheus.CounterVec
	activeWSConnections prometheus.Gauge
}

// New builds a Recorder with its own registry (not the global
// prometheus.DefaultRegisterer), so constructing more than one — e.g. in
// tests — never panics on a duplicate registration.
func New() *Recorder {
	r := &Recorder{registry: prometheus.NewRegistry()}

	r.commandsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_commands_total",
		Help: "Telegram commands handled, by command name.",
	}, []string{"command"})

	r.callbacksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_callbacks_total",
		Help: "Inline-keyboard callbacks handled, by registered callback data (or prefix, for per-question answer buttons).",
	}, []string{"callback"})

	r.httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "bot_http_request_duration_seconds",
		Help:    "Latency of outbound HTTP calls to the BrainBlitz backend, per attempt (including retries).",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	r.httpRequestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_http_request_errors_total",
		Help: "Outbound HTTP attempts that failed (network error or 5xx), per attempt (including retries).",
	}, []string{"method", "path"})

	r.activeWSConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bot_active_ws_connections",
		Help: "Currently open WebSocket connections to game-service (one per chat with an in-progress queue attempt or game).",
	})

	r.registry.MustRegister(r.commandsTotal, r.callbacksTotal, r.httpRequestDuration, r.httpRequestErrors, r.activeWSConnections)
	return r
}

// Handler returns the HTTP handler cmd/bot/main.go serves at /metrics.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

// CommandHandled records that a Telegram command was dispatched.
func (r *Recorder) CommandHandled(command string) {
	r.commandsTotal.WithLabelValues(command).Inc()
}

// CallbackHandled records that an inline-keyboard callback was dispatched.
func (r *Recorder) CallbackHandled(data string) {
	r.callbacksTotal.WithLabelValues(data).Inc()
}

// ObserveHTTPRequest records one outbound HTTP attempt's latency, and
// counts it as an error if err is non-nil.
func (r *Recorder) ObserveHTTPRequest(method, path string, duration time.Duration, err error) {
	r.httpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
	if err != nil {
		r.httpRequestErrors.WithLabelValues(method, path).Inc()
	}
}

// WSConnectionOpened increments the active-WS-connections gauge.
func (r *Recorder) WSConnectionOpened() {
	r.activeWSConnections.Inc()
}

// WSConnectionClosed decrements the active-WS-connections gauge.
func (r *Recorder) WSConnectionClosed() {
	r.activeWSConnections.Dec()
}
