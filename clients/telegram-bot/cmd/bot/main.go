// Command bot is the entry point for the BrainBlitz Telegram bot. It loads
// configuration, wires dependencies (logger, Telegram API client, command
// handlers), and runs the update loop until interrupted.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	goredis "github.com/redis/go-redis/v9"

	apihttp "github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/http"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/apiclient/ws"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/config"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/auth"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/game"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/matchmaking"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/profile"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/core/session"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/logger"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/metrics"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/commands"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/conversation"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/keyboards"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/internal/telegram/middleware"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/pkg/ratelimit"
	"github.com/nima-abdpoor/brain-blitz-telegram-bot/pkg/retry"
)

// updatePollTimeoutSeconds is the long-polling timeout passed to Telegram's
// getUpdates call. 30s is the commonly recommended value: long enough to
// avoid hammering the API with empty polls, short enough to notice a dead
// connection promptly.
const updatePollTimeoutSeconds = 30

// wsConnectRetryPolicy is the WS-connect retry policy from
// docs/client/client-architecture.md §9: 5 attempts (1s, 2s, 4s, 8s
// backoff), capped well under the documented ~30s ceiling.
var wsConnectRetryPolicy = retry.Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 30 * time.Second}

// defaultSessionTTL bounds how long an unused Redis-backed session survives
// when session_store.ttl isn't set — long enough to not surprise an
// infrequent user, short enough to eventually reclaim abandoned chats.
const defaultSessionTTL = 30 * 24 * time.Hour

// defaultMetricsListenAddr is used when metrics.enabled is true but
// metrics.listen_addr is left empty in config.
const defaultMetricsListenAddr = ":9090"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := os.Getenv("BOT_CONFIG_FILE")
	if configPath == "" {
		configPath = "configs/development/config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log := logger.New(cfg.Logging.Level)

	tgAPI, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		return fmt.Errorf("initializing telegram bot api: %w", err)
	}
	log.Info("authenticated with telegram", "username", tgAPI.Self.UserName)

	// recorder is always built (cheap: its own registry, no I/O) so every
	// call site below can assume it's non-nil; whether it's actually served
	// is gated separately by cfg.Metrics.Enabled in runMetricsServer.
	recorder := metrics.New()

	// A nil limiter (the default: rate_limit.requests_per_second <= 0) means
	// every call site below skips rate limiting entirely — Phases 1-5's
	// behavior, unchanged unless a deployment opts in.
	var limiter *ratelimit.Limiter
	httpOpts := []apihttp.Option{apihttp.WithMetrics(recorder)}
	if cfg.RateLimit.RequestsPerSecond > 0 {
		limiter = ratelimit.New(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst)
		httpOpts = append(httpOpts, apihttp.WithRateLimiter(limiter))
	}

	sessionStore, err := newSessionStore(cfg.SessionStore, log)
	if err != nil {
		return fmt.Errorf("building session store: %w", err)
	}

	userClient := apihttp.NewUserClient(apihttp.NewClient(cfg.Backend.UserServiceURL, cfg.Backend.HTTPTimeout, log, httpOpts...))
	authSvc := auth.NewService(userClient, sessionStore)
	profileSvc := profile.NewService(userClient, sessionStore)

	matchmakingNotifier := telegram.NewMatchmakingNotifier(tgAPI, log)
	// dialer retries a failed connection attempt with backoff
	// (wsConnectRetryPolicy) and, if configured, waits on the same rate
	// limiter as HTTP calls — see docs/client/client-architecture.md §9.
	// This is the full extent of "reconnection" this bot attempts: robust
	// *connection establishment*, never resuming a queue/game a dropped
	// connection already lost (see this phase's design notes in
	// docs/client/client-roadmap.md for why resuming isn't safe to fake).
	dialer := func(ctx context.Context, accessToken string) (matchmaking.Connection, error) {
		var conn *ws.Client
		err := retry.Do(ctx, wsConnectRetryPolicy, func() error {
			if limiter != nil {
				if err := limiter.Wait(ctx); err != nil {
					return err
				}
			}
			c, dialErr := ws.Dial(ctx, cfg.Backend.GameServiceWSURL, accessToken)
			if dialErr != nil {
				return dialErr
			}
			conn = c
			return nil
		})
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	// gameHandler starts nil and is filled in below — game.NewManager needs
	// matchmakingMgr as its Detacher, which would otherwise be a
	// construction-order cycle (see matchmakingMgr.SetGameHandler's doc
	// comment).
	matchmakingMgr := matchmaking.NewManager(dialer, matchmakingNotifier, sessionStore, nil)
	matchmakingMgr.SetMetrics(recorder)

	gameNotifier := telegram.NewGameNotifier(tgAPI, log)
	gameMgr := game.NewManager(gameNotifier, matchmakingMgr, log)
	matchmakingMgr.SetGameHandler(gameMgr)

	handlers := commands.NewHandlers(authSvc, profileSvc, matchmakingMgr, gameMgr, conversation.NewMemoryStore())

	bot := telegram.New(tgAPI, log)
	bot.SetMetrics(recorder)
	bot.RegisterCommand("start", handlers.Start)
	bot.RegisterCommand("help", handlers.Help)
	bot.RegisterCommand("profile", middleware.RequireAuth(authSvc, handlers.Profile))
	bot.RegisterCommand("play", middleware.RequireAuth(authSvc, handlers.Play))
	bot.RegisterCallback("register", handlers.RegisterCallback)
	bot.RegisterCallback("login", handlers.LoginCallback)
	bot.RegisterCallback("help", handlers.HelpCallback)
	bot.RegisterCallback("profile", middleware.RequireAuthCallback(authSvc, handlers.ProfileCallback))
	bot.RegisterCallback("logout", middleware.RequireAuthCallback(authSvc, handlers.LogoutCallback))
	bot.RegisterCallback(keyboards.CallbackPlay, middleware.RequireAuthCallback(authSvc, handlers.PlayCallback))
	bot.RegisterCallback(keyboards.CallbackCancel, middleware.RequireAuthCallback(authSvc, handlers.CancelCallback))
	for _, category := range matchmaking.Categories {
		bot.RegisterCallback(keyboards.CallbackCategoryPrefix+category, middleware.RequireAuthCallback(authSvc, handlers.CategoryCallback))
	}
	bot.RegisterCallbackPrefix(keyboards.CallbackAnswerPrefix, handlers.AnswerCallback)
	bot.SetTextHandler(handlers.HandleText)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stopMetrics := runMetricsServer(cfg.Metrics, recorder, log)
	defer stopMetrics()

	runBot(ctx, tgAPI, bot, log)
	return nil
}

// runMetricsServer starts the Prometheus /metrics endpoint in the
// background when cfg.Enabled, and returns a func that shuts it down. When
// disabled (the default), it does nothing and returns a no-op — Phases 1-6
// deployments that never set metrics.enabled see zero behavior change.
func runMetricsServer(cfg config.Metrics, recorder *metrics.Recorder, log *slog.Logger) func() {
	if !cfg.Enabled {
		return func() {}
	}

	addr := cfg.ListenAddr
	if addr == "" {
		addr = defaultMetricsListenAddr
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", recorder.Handler())
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "addr", addr, "error", err)
		}
	}()
	log.Info("serving metrics", "addr", addr, "path", "/metrics")

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Warn("metrics server shutdown error", "error", err)
		}
	}
}

// runBot starts long-polling and blocks until ctx is cancelled, then stops
// polling cleanly. Split out from run() so it doesn't need to be duplicated
// across tests.
func runBot(ctx context.Context, tgAPI *tgbotapi.BotAPI, bot *telegram.Bot, log *slog.Logger) {
	updateCfg := tgbotapi.NewUpdate(0)
	updateCfg.Timeout = updatePollTimeoutSeconds
	updates := tgAPI.GetUpdatesChan(updateCfg)

	log.Info("bot started, listening for updates")
	bot.Run(ctx, updates)

	tgAPI.StopReceivingUpdates()
	log.Info("bot stopped")
}

// newSessionStore builds cfg's configured session.Store — "memory" (the
// default, including when cfg.Type is unset) preserves Phases 1-5's
// behavior exactly; "redis" persists sessions across a bot restart (Phase
// 6, docs/client/client-roadmap.md). Only ever the auth session (tokens,
// user ID) is persisted this way — never in-flight matchmaking/game state,
// which this bot has no safe way to resume after a restart regardless of
// storage (see the Phase 6 design notes for why).
func newSessionStore(cfg config.SessionStore, log *slog.Logger) (session.Store, error) {
	if cfg.Type != "redis" {
		return session.NewMemoryStore(), nil
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	client := goredis.NewClient(&goredis.Options{Addr: cfg.RedisAddr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("connecting to redis at %q: %w", cfg.RedisAddr, err)
	}
	log.Info("using redis session store", "addr", cfg.RedisAddr, "ttl", ttl)
	return session.NewRedisStore(session.NewGoRedisClient(client), ttl), nil
}
