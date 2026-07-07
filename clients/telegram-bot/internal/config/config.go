// Package config loads and validates the bot's runtime configuration.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// envPrefix distinguishes this process's environment variables from the
// backend services', which use their own per-service prefixes (AUTH_, USER_, ...).
const envPrefix = "BOT_"

// Config is the bot's complete runtime configuration, populated from a YAML
// file with environment variable overrides layered on top.
type Config struct {
	Telegram     Telegram     `koanf:"telegram"`
	Backend      Backend      `koanf:"backend"`
	SessionStore SessionStore `koanf:"session_store"`
	RateLimit    RateLimit    `koanf:"rate_limit"`
	Logging      Logging      `koanf:"logging"`
}

// Telegram holds settings for the Telegram Bot API client.
type Telegram struct {
	BotToken string `koanf:"bot_token"`
}

// Backend holds the BrainBlitz gateway URLs and HTTP client tuning this
// process needs to reach the user, match, and game services.
type Backend struct {
	UserServiceURL   string        `koanf:"user_service_url"`
	MatchServiceURL  string        `koanf:"match_service_url"`
	GameServiceWSURL string        `koanf:"game_service_ws_url"`
	HTTPTimeout      time.Duration `koanf:"http_timeout"`
}

// SessionStore selects and configures where per-chat sessions are
// persisted. The zero value ("" for Type) behaves exactly like Phases 1-5
// (an in-process store, lost on restart) — opting into Redis (Phase 6,
// docs/client/client-roadmap.md) requires explicitly setting Type: "redis".
type SessionStore struct {
	// Type is "memory" (default) or "redis".
	Type      string        `koanf:"type"`
	RedisAddr string        `koanf:"redis_addr"`
	TTL       time.Duration `koanf:"ttl"`
}

// RateLimit bounds how fast this process makes outbound calls to the
// BrainBlitz backend (HTTP and WebSocket-connect), so a burst of
// client-side retries across many chats never hammers the gateway harder
// than a single sane baseline rate (docs/client/client-architecture.md §9).
// RequestsPerSecond <= 0 (the zero value) means "unlimited" — matching
// Phases 1-5's behavior exactly for any deployment that doesn't opt in.
type RateLimit struct {
	RequestsPerSecond float64 `koanf:"requests_per_second"`
	Burst             int     `koanf:"burst"`
}

// Logging holds structured-logging settings.
type Logging struct {
	Level string `koanf:"level"`
}

// Load reads yamlPath (if non-empty) and overlays BOT_-prefixed environment
// variables, where a double underscore separates nested keys, e.g.
// BOT_TELEGRAM__BOT_TOKEN overrides telegram.bot_token. It returns an error
// instead of exiting the process so callers (and tests) control failure
// handling.
func Load(yamlPath string) (*Config, error) {
	k := koanf.New(".")

	if yamlPath != "" {
		if err := k.Load(file.Provider(yamlPath), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading config file %q: %w", yamlPath, err)
		}
	}

	envToKey := func(key string) string {
		trimmed := strings.TrimPrefix(key, envPrefix)
		return strings.ReplaceAll(strings.ToLower(trimmed), "__", ".")
	}
	if err := k.Load(env.Provider(envPrefix, ".", envToKey), nil); err != nil {
		return nil, fmt.Errorf("loading environment variables: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks that fields required for the bot to start are present.
// It deliberately does not check optional/future fields (e.g. match or game
// service URLs are unused until later phases wire them up).
func (c Config) Validate() error {
	if c.Telegram.BotToken == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if c.Backend.UserServiceURL == "" {
		return fmt.Errorf("backend.user_service_url is required")
	}
	switch c.SessionStore.Type {
	case "", "memory":
	case "redis":
		if c.SessionStore.RedisAddr == "" {
			return fmt.Errorf(`session_store.redis_addr is required when session_store.type is "redis"`)
		}
	default:
		return fmt.Errorf(`session_store.type %q is invalid: want "memory" or "redis"`, c.SessionStore.Type)
	}
	return nil
}
