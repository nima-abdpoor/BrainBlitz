# Deploying the BrainBlitz Telegram Bot

This directory holds the Docker image build and Kubernetes manifests for running the bot, mirroring the conventions used by the main repo's backend services (`infra/deploy/*/development/Dockerfile`, `infra/kubernetes/`).

## Docker image

Unlike the backend services (all one Go module, built from the repo root), this bot is its own Go module — the build context is `clients/telegram-bot/` itself, not the repo root:

```sh
cd clients/telegram-bot
docker build \
  -f deploy/Dockerfile \
  --build-arg GO_IMAGE_NAME=golang \
  --build-arg GO_IMAGE_VERSION=1.23 \
  -t ghcr.io/nima-abdpoor/brain-blitz/telegram-bot:latest \
  .
```

The image is a multi-stage build (`golang:1.23` → `debian:bookworm-slim`), ships the compiled `telegram-bot` binary plus `configs/development/config.yaml` (kept as the shipped default, same as the main repo's own services — every value in it is meant to be overridden by `BOT_`-prefixed environment variables at deploy time, never edited in the image itself).

Verified locally: `docker build` succeeds and the resulting container starts, loads the shipped config, and fails config validation cleanly (`telegram.bot_token is required`) when no token is supplied — confirming the entrypoint and config loading both work end to end.

## Kubernetes manifests

| File | Contents |
|---|---|
| `deployment.yaml` | `Deployment` (1 replica — see below) + `Service` exposing `:9090/metrics` |
| `configmap.yaml` | Non-secret config: backend gateway URLs, session store, metrics, log level |
| `secret.yaml` | Template for `telegram.bot_token` — **the shipped value is a placeholder, not a real token** |

Apply in order:

```sh
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/secret.yaml   # after replacing the placeholder — see below
kubectl apply -f deploy/deployment.yaml
```

### Setting the real bot token

`secret.yaml`'s `bot-token` value is the base64 encoding of the literal string `REPLACE_ME`. Generate the real secret out of band and apply it instead of editing the committed file:

```sh
kubectl create secret generic telegram-bot-secret \
  --from-literal=bot-token="<token from @BotFather>" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Why replicas must stay at 1

This bot uses Telegram's long-polling `getUpdates` API (`cmd/bot/main.go`'s `runBot`), which has no fan-out between multiple consumers of the same bot token. Running more than one replica means both pollers race for the same updates and can double-handle a command or callback. `deployment.yaml` is pinned to `replicas: 1` and `strategy: Recreate` (not `RollingUpdate`) for the same reason — a rolling update would briefly run two pollers side by side. Scaling this service means moving to Telegram's webhook mode instead (out of scope for this phase — see `docs/client/client-roadmap.md` in the repo root).

### Metrics and the liveness probe

`configmap.yaml` ships with `metrics-enabled: "true"` deliberately: `deployment.yaml`'s `livenessProbe` hits `GET /metrics` on port `9090`, so metrics must stay enabled for the Deployment to consider the pod healthy. If you disable metrics, remove or replace the liveness probe first.

### Session persistence across restarts

`configmap.yaml` defaults `session-store-type` to `redis`, pointed at `redis-service:6379` — matching the main repo's own Redis deployment (`infra/kubernetes/deployment/redis.yaml`). This persists only auth sessions (tokens, user ID) across a bot restart; in-flight matchmaking/game state is never persisted (there is no backend API to resync it — see `docs/client/client-roadmap.md`'s Phase 6 notes). Switch to `session-store-type: memory` if you don't want that dependency, at the cost of every chat needing to `/login` again after any restart.
