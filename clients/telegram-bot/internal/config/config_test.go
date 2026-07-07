package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoad_FromYAML(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "yaml-token"
backend:
  user_service_url: "http://localhost/user-service"
  http_timeout: 10s
logging:
  level: "debug"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Telegram.BotToken != "yaml-token" {
		t.Errorf("BotToken = %q, want %q", cfg.Telegram.BotToken, "yaml-token")
	}
	if cfg.Backend.UserServiceURL != "http://localhost/user-service" {
		t.Errorf("UserServiceURL = %q, want %q", cfg.Backend.UserServiceURL, "http://localhost/user-service")
	}
	if cfg.Backend.HTTPTimeout != 10*time.Second {
		t.Errorf("HTTPTimeout = %v, want %v", cfg.Backend.HTTPTimeout, 10*time.Second)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "yaml-token"
backend:
  user_service_url: "http://localhost/user-service"
`)

	t.Setenv("BOT_TELEGRAM__BOT_TOKEN", "env-token")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Telegram.BotToken != "env-token" {
		t.Errorf("BotToken = %q, want env override %q", cfg.Telegram.BotToken, "env-token")
	}
}

func TestLoad_MissingRequiredField(t *testing.T) {
	path := writeTempConfig(t, `
backend:
  user_service_url: "http://localhost/user-service"
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load should fail when telegram.bot_token is missing")
	}
}

func TestLoad_SessionStoreAndRateLimit(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "yaml-token"
backend:
  user_service_url: "http://localhost/user-service"
session_store:
  type: "redis"
  redis_addr: "localhost:6379"
  ttl: 720h
rate_limit:
  requests_per_second: 10
  burst: 5
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.SessionStore.Type != "redis" {
		t.Errorf("SessionStore.Type = %q, want %q", cfg.SessionStore.Type, "redis")
	}
	if cfg.SessionStore.RedisAddr != "localhost:6379" {
		t.Errorf("SessionStore.RedisAddr = %q, want %q", cfg.SessionStore.RedisAddr, "localhost:6379")
	}
	if cfg.SessionStore.TTL != 720*time.Hour {
		t.Errorf("SessionStore.TTL = %v, want %v", cfg.SessionStore.TTL, 720*time.Hour)
	}
	if cfg.RateLimit.RequestsPerSecond != 10 {
		t.Errorf("RateLimit.RequestsPerSecond = %v, want 10", cfg.RateLimit.RequestsPerSecond)
	}
	if cfg.RateLimit.Burst != 5 {
		t.Errorf("RateLimit.Burst = %d, want 5", cfg.RateLimit.Burst)
	}
}

func TestLoad_Metrics(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "yaml-token"
backend:
  user_service_url: "http://localhost/user-service"
metrics:
  enabled: true
  listen_addr: ":9091"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = false, want true")
	}
	if cfg.Metrics.ListenAddr != ":9091" {
		t.Errorf("Metrics.ListenAddr = %q, want %q", cfg.Metrics.ListenAddr, ":9091")
	}
}

func TestLoad_MetricsDefaultsToDisabled(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "yaml-token"
backend:
  user_service_url: "http://localhost/user-service"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = true, want false when metrics section is omitted")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid",
			cfg: Config{
				Telegram: Telegram{BotToken: "token"},
				Backend:  Backend{UserServiceURL: "http://localhost"},
			},
			wantErr: false,
		},
		{
			name:    "missing bot token",
			cfg:     Config{Backend: Backend{UserServiceURL: "http://localhost"}},
			wantErr: true,
		},
		{
			name:    "missing user service url",
			cfg:     Config{Telegram: Telegram{BotToken: "token"}},
			wantErr: true,
		},
		{
			name: "session store type memory is valid",
			cfg: Config{
				Telegram:     Telegram{BotToken: "token"},
				Backend:      Backend{UserServiceURL: "http://localhost"},
				SessionStore: SessionStore{Type: "memory"},
			},
			wantErr: false,
		},
		{
			name: "session store type redis without addr is invalid",
			cfg: Config{
				Telegram:     Telegram{BotToken: "token"},
				Backend:      Backend{UserServiceURL: "http://localhost"},
				SessionStore: SessionStore{Type: "redis"},
			},
			wantErr: true,
		},
		{
			name: "session store type redis with addr is valid",
			cfg: Config{
				Telegram:     Telegram{BotToken: "token"},
				Backend:      Backend{UserServiceURL: "http://localhost"},
				SessionStore: SessionStore{Type: "redis", RedisAddr: "localhost:6379"},
			},
			wantErr: false,
		},
		{
			name: "unknown session store type is invalid",
			cfg: Config{
				Telegram:     Telegram{BotToken: "token"},
				Backend:      Backend{UserServiceURL: "http://localhost"},
				SessionStore: SessionStore{Type: "postgres"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
