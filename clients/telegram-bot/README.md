# BrainBlitz Telegram Bot

A Telegram bot client for [BrainBlitz](../../README.md), the real-time multiplayer quiz game backend. Players register, log in, join a category queue, and play a live timed quiz — entirely through Telegram chat — while this process holds the WebSocket connection to the game backend on their behalf.

This is its own Go module (`github.com/nima-abdpoor/brain-blitz-telegram-bot`), independently versioned and deployed from the main BrainBlitz backend, though it lives inside the same monorepo (`clients/telegram-bot/`).

## Features

- **Onboarding**: `/start`, inline register/login conversations
- **Profile & menu**: `/profile`, `/help`, logout
- **Matchmaking**: join a category queue (`SPORT` / `MUSIC` / `TECH`), cancel, match-found notification
- **Gameplay**: timed questions delivered as they're published, inline-keyboard answers, live scoring, final leaderboard
- **Resilience**: retrying HTTP client with backoff, WebSocket-connect retry, optional rate limiting, optional Redis-backed session persistence across restarts
- **Observability**: Prometheus metrics, structured JSON logging (see [Metrics](#metrics) below)

See `docs/client/` in the repo root for the full design history (architecture, business flows, phased roadmap) behind this implementation.

## Architecture

```
internal/core/{auth,session,profile,matchmaking,game}   Telegram-agnostic business logic
        │
internal/apiclient/{http,ws}                            Typed BrainBlitz API clients
        │
internal/telegram/{commands,conversation,keyboards,middleware}   Delivery layer (owns Telegram SDK types)
```

`internal/core` and `internal/apiclient` know nothing about Telegram — a future Web/Discord/CLI client would reuse them and add its own delivery adapter. `internal/telegram` is the only layer allowed to import the Telegram Bot API SDK.

A single bot process serves every Telegram chat; it multiplexes many per-chat WebSocket connections to game-service, guarded by a per-chat state machine (`internal/core/matchmaking`, `internal/core/game`) since there is no server-side session/reconnect API.

## Requirements

- Go 1.23 (the module pins `go 1.23.0` deliberately — see `go.mod`)
- A running BrainBlitz backend (`docker-compose up` from the repo root) reachable via its Traefik gateway
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- Redis, only if you opt into `session_store.type: redis` (see [Configuration](#configuration))

## Getting Started

```sh
cd clients/telegram-bot
go build ./...

export BOT_TELEGRAM__BOT_TOKEN="<token from @BotFather>"
go run ./cmd/bot
```

By default the bot loads `configs/development/config.yaml`, pointed at `http://localhost/user-service` etc. (the docker-compose gateway). Override the config file path with `BOT_CONFIG_FILE`, or any individual key with a `BOT_`-prefixed environment variable (`__` separates nesting, e.g. `BOT_BACKEND__HTTP_TIMEOUT`).

## Configuration

All settings live in `configs/development/config.yaml`; every key can be overridden by an environment variable. Sections:

| Section | Purpose | Default |
|---|---|---|
| `telegram.bot_token` | Bot API token | required, no default |
| `backend.*` | Gateway URLs + HTTP timeout | `http://localhost/...` (docker-compose) |
| `session_store` | `memory` (lost on restart) or `redis` (persists auth sessions only) | `memory` |
| `rate_limit` | Caps outbound HTTP/WS-connect rate | unlimited |
| `metrics` | Prometheus `/metrics` endpoint | disabled |
| `logging.level` | `debug`/`info`/`warn`/`error` | `info` |

See the comments in `configs/development/config.yaml` for the full, current set of keys and their defaults.

## Metrics

Set `metrics.enabled: true` (or `BOT_METRICS__ENABLED=true`) to serve Prometheus metrics on `metrics.listen_addr` (default `:9090`) at `/metrics`:

- `bot_commands_total{command}` — Telegram commands handled
- `bot_callbacks_total{callback}` — inline-keyboard callbacks handled (per-question answer buttons are recorded by prefix, not full callback data, to avoid an unbounded label cardinality)
- `bot_http_request_duration_seconds{method,path}` — outbound HTTP latency per attempt, including retries
- `bot_http_request_errors_total{method,path}` — failed outbound HTTP attempts
- `bot_active_ws_connections` — currently open game-service WebSocket connections

This closes a gap in the main BrainBlitz backend itself, which wires Prometheus/Grafana in `docker-compose.yml` but has no service that actually exposes application metrics (`docs/context/07-known-issues.md` #7) — this bot does not repeat that omission.

## Structured Logging

The bot logs structured JSON to stdout (`internal/logger`) at a configurable level. Logging has been audited end to end: no call site logs a password, access token, or refresh token — auth flows only ever pass credentials through local variables for the duration of a single request. Container runtimes are expected to capture stdout; this process does not manage its own log files or rotation.

## Testing

```sh
go test ./...                       # unit tests (fakes only, no network) — always safe to run
go test ./... -race                 # same, with the race detector

docker-compose up -d                # from the repo root — brings up the real backend
go test -tags=integration ./integration/...   # exercises real signup/login/profile and a live 2-player match
```

Integration tests each perform their own reachability check and call `t.Skip` (not fail) if the backend they need isn't up, so `-tags=integration` is safe to run even without `docker-compose up`.

## Deployment

See [deploy/README.md](deploy/README.md) for the Docker image and Kubernetes manifests, and the constraints specific to a long-polling Telegram bot (in particular: **do not scale replicas past 1** without first switching to webhook mode).

## Known Limitations

See `docs/client/client-roadmap.md` and `docs/client/gap-analysis.md` in the repo root for the full list; the most consequential for day-to-day operation:

- No backend token-refresh endpoint exists — a session simply expires and the user is asked to `/login` again (`session.Session.Stale`, 24h).
- A dropped WebSocket connection (queued or mid-game) is never auto-resumed; the bot has no way to know server-side truth after reconnecting, so it always ends the attempt locally rather than risk double-enqueuing or missing an already-fired match.
- Categories are hardcoded client-side (`SPORT`/`MUSIC`/`TECH`) since the backend's `GET_CATEGORIES` command is a documented no-op.
