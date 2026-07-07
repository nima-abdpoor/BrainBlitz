# Telegram Bot — Development Roadmap

Seven phases, each independently shippable/testable. Effort estimates assume one backend-capable developer working solo, familiar with Go, new to Telegram bot frameworks.

---

## Phase 1 — Project Setup ✅ Implemented

**Goals**: A running, empty bot that responds to `/start` and can be deployed.

**Deliverables**:
- Repo/directory scaffold per `client-architecture.md` §2
- Telegram framework selected and wired (e.g. `go-telegram-bot-api` or `telebot`) **[ASSUMPTION: exact library not chosen yet — evaluate both against webhook vs. long-polling needs before committing]**
- Config loading (`internal/config`) — bot token, backend base URLs
- Logging (`internal/logger`)
- HTTP API client skeleton (`internal/apiclient/http`) — no business calls yet, just base client with timeout/retry plumbing
- `/start` command that replies with a static greeting

**Dependencies**: None — can start immediately.

**Risks**: Choosing a Telegram library that doesn't support both polling and webhook modes could force a rewrite later; evaluate this decision explicitly.

**Estimated effort**: 2-3 days.

**Files/modules to create**: `cmd/bot/main.go`, `internal/config/config.go`, `internal/logger/logger.go`, `internal/telegram/bot.go`, `internal/telegram/commands/start.go`, `internal/apiclient/http/client.go`.

**Implementation notes (2026-07-01)**: Built at `clients/telegram-bot/` as a separate Go module (`github.com/nima-abdpoor/brain-blitz-telegram-bot`) inside this monorepo, resolving the earlier repo-placement assumption. Library choice: `go-telegram-bot-api/telegram-bot-api/v5` (rationale below). Deviated from the file list above only by adding test files (`*_test.go`) alongside each package and a `configs/development/config.yaml`, both implied but not explicitly listed. All other Phase 1 deliverables implemented as scoped; no Phase 2+ work included.

**Library choice — `go-telegram-bot-api/telegram-bot-api/v5`**: Chosen over `telebot.v3` and `go-telegram/bot` because it's the thinnest wrapper over the raw Bot API (a `Send(Chattable)` method plus an update channel), with no built-in router/middleware/context abstractions of its own. The architecture in this document already defines its own command dispatch (`internal/telegram/bot.go`), keyboards, and middleware packages, so a thicker framework would mean fighting or discarding a second layer of routing. Its `BotAPI` type is easy to hide behind a small `Sender` interface (`internal/telegram/bot.go`), which is what makes `Bot.handleUpdate` unit-testable without a live Telegram connection. It supports both long-polling (`GetUpdatesChan`, used in Phase 1) and webhook mode (`ListenForWebhook`), so switching later is a `cmd/bot/main.go` change, not a rewrite.

---

## Phase 2 — User Onboarding ✅ Implemented

**Goals**: A user can register and log in through the bot and have a persistent session.

**Deliverables**:
- `internal/core/auth` module (Login, Register)
- `internal/apiclient/http/user_client.go` (SignUp, Login)
- `internal/core/session` (Session type + in-memory Store)
- Telegram conversation flow: Register/Login buttons, email/password prompts
- Error message mapping per `feature-map.md` §1

**Dependencies**: Phase 1.

**Risks**: Plain-text password entry in a Telegram chat is a real UX/security wart (messages may be visible in chat history) — decide now whether to accept this for MVP or design around it (e.g. deep-linking to a one-time web form). Recommend accepting it for MVP given this is a portfolio/learning project, but flag prominently in the risk register if this ever handles real user data.

**Estimated effort**: 3-4 days.

**Files/modules to create**: `internal/core/auth/auth.go`, `internal/apiclient/http/user_client.go`, `internal/core/session/session.go`, `internal/core/session/store.go` (in-memory impl), `internal/telegram/commands/{start,login,register}.go` (extends Phase 1's `start.go`).

**Implementation notes (2026-07-05)**: Built as scoped, plus `internal/telegram/commands/text.go` (`Handlers.HandleText`) — implied by the roadmap's `TextHandler` hook in `bot.go` and by in-code comments in `login.go`/`register.go` referencing it, but not called out as a separate file above. `start.go`'s `Start` became a `*Handlers` method (was a free function in Phase 1) so it can check `auth.IsLoggedIn` and clear any in-progress conversation on `/start`. `cmd/bot/main.go` now wires `session.MemoryStore` → `auth.Service` → `commands.Handlers` and registers the `register`/`login` callbacks and the text handler.

Backend API verification (per `backend-api-analysis.md` §2, cross-checked against `services/user_app/delivery/http/server.go`): `POST /public/api/v1/signup` and `POST /public/api/v1/login` both exist and are public (no gateway/auth gap). No API was invented. Two pre-existing backend quirks are handled rather than worked around: a nonexistent-email login returns an ambiguous 500 (gap-analysis.md G-06) — surfaced to the user as a generic retry message, not misattributed to bad credentials; and signup's empty-password case reuses login's error string ("invalid username or password") — mapped to `KindInvalidInput`, not `KindInvalidCredentials`, since it's a signup-time rejection.

Session store is in-memory (`session.MemoryStore`), matching Phase 2's scope — Phase 6 tracks migrating it to Redis for multi-instance readiness. Passwords are never persisted: `conversation.State` and `session.Session` both have a reflection-based test (`TestState_NeverHasAPasswordField`, `TestSession_NeverHasAPasswordField`) guarding against a future field reintroducing one; passwords live only in a handler's local variables for the duration of a single SignUp/Login call.

---

## Phase 3 — Main Menu, Profile, Settings ✅ Implemented

**Goals**: A logged-in user can navigate a menu and see their profile.

**Deliverables**:
- Main menu keyboard (`internal/telegram/keyboards`)
- `/profile` command wired to `GET /user-service/api/v1/profile`
- `/help` static command
- `Logout` callback (local session clear)
- `internal/telegram/middleware/auth_required.go` — gate commands behind a valid session

**Dependencies**: Phase 2.

**Risks**: Low — this phase is straightforward CRUD-style integration.

**Estimated effort**: 2 days.

**Files/modules to create**: `internal/core/profile/profile.go`, `internal/telegram/commands/{profile,help}.go`, `internal/telegram/callbacks/logout.go`, `internal/telegram/middleware/auth_required.go`, `internal/telegram/keyboards/keyboards.go`.

**Implementation notes (2026-07-07)**: Built as scoped, with one placement deviation: `Logout` is `commands.Handlers.LogoutCallback` rather than a separate `internal/telegram/callbacks` package — this codebase never introduced a `callbacks` package, so all callbacks (register/login/profile/help/logout) live alongside commands in `internal/telegram/commands`. `keyboards.MainMenu()` omits a "Play Game" button on purpose (see comment in `keyboards.go`) since Phase 4 has no handler to wire it to yet — adding it now would be a dead-end button. `middleware.RequireAuth`/`RequireAuthCallback` are wired in `cmd/bot/main.go` around `profile` (command + callback) and `logout`; `/help` and its callback are deliberately left ungated, matching the roadmap's "Logout callback (local session clear)" note that these are the only two flows needing new auth wiring beyond Phase 2. `profile.Service.Get` also handles the profile-endpoint 404 case (account deleted after token issuance) by forcing a local logout — not explicitly scoped above but implied by `feature-map.md` §2.

---

## Phase 4 — Matchmaking ✅ Implemented

**Goals**: A user can join a category queue and get notified when matched.

**Deliverables**:
- WebSocket client (`internal/apiclient/ws`) — connect, send, receive-dispatch
- `internal/core/matchmaking` module wrapping `ADD_TO_WAITING_LIST`
- Category selection UI
- "Waiting for opponent" message + best-effort Cancel button (documented as UI-only per gap G-13)
- Handling of the `MATCH_CREATED` inbound event

**Dependencies**: Phase 2 (needs a valid token to connect WS). Does not depend on Phase 3.

**Risks**:
- This is the first phase touching the WebSocket protocol's documented quirks (contradictory ERROR+success frames, no ack patterns) — budget extra time for defensive handling per `backend-api-analysis.md` §4.
- One bot process must manage many concurrent WS connections (one per active session) — validate the chosen WS library's concurrency model early.

**Estimated effort**: 4-5 days.

**Files/modules to create**: `internal/apiclient/ws/{client,protocol,dispatcher}.go`, `internal/core/matchmaking/matchmaking.go`, `internal/telegram/callbacks/category.go`, `internal/telegram/commands/play.go`.

**Implementation notes (2026-07-07)**: Built per `docs/client/phase4-matchmaking-plan.md`, with all three of that doc's open questions resolved: `gorilla/websocket` (over `coder/websocket`), hardcoded categories client-side (per gap G-09), and the honest Cancel-button copy in §6. One placement deviation, consistent with Phase 3's precedent: `CategoryCallback`/`CancelCallback` live in `internal/telegram/commands` (as `category.go`), not a separate `internal/telegram/callbacks` package — this codebase has never introduced that package.

`internal/apiclient/ws.Client` wraps one `gorilla/websocket` connection (`Dial`, `Send`, `Listen`, `Close`), serializing writes and silently skipping the non-enveloped first-connect `{categories, numberOfPlayers}` push (unneeded since categories are hardcoded). `internal/core/matchmaking.Manager` owns one `Connection` per chat behind a mutex-guarded map, implementing the Phase 4 state machine (`Idle → Connecting → Queued → {Cancelling, Matched, Idle}`) and both documented race-condition mitigations from the plan: the double-join guard (§8.1) and the Cancel-vs-`MATCH_CREATED` guard (§8.2, via deleting the chat's session from the map before closing the connection). The ERROR-then-false-success quirk (`backend-api-analysis.md` §4 point 1) is latched and handled as a failure, not a success. `MainMenu` now includes a Play button (the Phase 3 regression test guarding against this was updated, not just deleted, to assert the new expected button set).

Not yet built (explicitly out of scope per the plan, deferred to later phases): `READY`/`ANSWER` handling (Phase 5), any hard timeout while waiting for `MATCH_CREATED` (Phase 6), and reconnect-after-bot-restart (Phase 6, gap G-14). Tests: `ws.Client` against a real `httptest`+`gorilla/websocket` server (auth header, send/listen round-trip, concurrent-write safety, `Close` unblocking a pending read); `matchmaking.Manager` against a fake `Connection`/`Dialer` covering the happy path, double-join, the ERROR-then-success latch, the cancel-vs-match race, unexpected disconnect, and dial failure — all pass under `go test -race`. Manual end-to-end testing against a running backend (`docker-compose up`) has not been done yet.

---

## Phase 5 — Gameplay ✅ Implemented

**Goals**: Full game loop — ready up, receive questions, answer, see live leaderboard, see final results.

**Deliverables**:
- `internal/core/game` module: client-side state machine (`client-architecture.md` §5), Ready/Answer logic
- Question pacing logic (bot must locally gate reveal/answer windows since the server sends all questions up front)
- Countdown timer UI per question
- Live leaderboard rendering after each `ANSWER_ACCEPTED`
- Final leaderboard + "Play Again" on `COMPLETED`

**Dependencies**: Phase 4.

**Risks**:
- Highest-complexity phase. The client-side state machine and pacing logic is the crux of making a server with no request/response guarantees (per `backend-api-analysis.md` §4 quirks) feel responsive.
- No backend guardrails against out-of-order commands — thorough manual testing against a locally running backend (`docker-compose up`) is essential before considering this phase done.

**Estimated effort**: 6-8 days.

**Files/modules to create**: `internal/core/game/{state_machine,game}.go` (+ tests), `internal/telegram/callbacks/{ready,answer}.go`, `internal/telegram/presenter/presenter.go`.

**Implementation notes (2026-07-07)**: Built as `internal/core/game/{state_machine,game}.go`, with the same placement deviation as Phase 3/4 — `AnswerCallback` lives in `internal/telegram/commands/answer.go`, not a separate `callbacks` package (never introduced in this codebase); there's no separate `presenter` package either — message formatting lives directly in `telegram.GameNotifier` (`internal/telegram/notifier.go`), mirroring how `MatchmakingNotifier` already worked. No Ready button: the bot auto-sends `READY` the instant `MATCH_CREATED` hands off to this package (`Manager.Attached`), since `client-business-flows.md`'s own end-to-end flow shows no user action between match-found and READY.

**State machine** (`state_machine.go`): `Matched → ReadyPending → Answering → Completed → Idle`, a pure `Transition(state, event) (next, ok)` function — deterministic by construction (no clock reads, no hidden state), with every unlisted `(state, event)` pair rejected (`ok=false`, state unchanged). This single mechanism is what makes duplicate events (a second `READY`, a repeat `QUESTIONS_PUBLISHED`), late events (an `ANSWER_ACCEPTED` after `COMPLETED`), and any other out-of-order input safe to ignore — `TestTransition_ExhaustiveTable` checks all 35 `(state, event)` combinations against an explicit valid-transition table.

**Hand-off from Phase 4**: `matchmaking.Manager` gained a `GameHandler` interface (`Attached`/`Disconnected`) and a `Detach` method — once `MATCH_CREATED` fires, matchmaking keeps running the same read loop and connection (per `phase4-matchmaking-plan.md`'s "Matched is a hand-off point, not a dead end") but forwards every further event for that chat straight to `game.Manager` instead of its own switch. `game.Manager.finish` calls `Detach` after `COMPLETED` or a ready-timeout so the server's own post-`COMPLETED` connection close (`backend-api-analysis.md` §4) isn't misreported as a mid-game disconnect.

**Countdown / pacing**: questions arrive all at once (`QUESTIONS_PUBLISHED`) with individual absolute deadlines (wire type confirmed against `services/game_app/service/param.go` — `TTL time.Time`, not a duration); `Manager` sorts by deadline and presents one at a time, arming a `time.AfterFunc` per question that auto-advances if the deadline passes unanswered (there's no server "time's up" push — this is the client-side pacing `client-business-flows.md` §6 calls for). `ANSWER`'s `choice` field is the literal choice text (confirmed against `services/game_app/repository/game.go`), not a letter/index — the delivery layer's answer buttons encode `<questionID>|<index>` and `Manager.SubmitAnswer` resolves the index against the currently-active question's choices.

**New generic capability**: `telegram.Bot` gained `RegisterCallbackPrefix` (checked after exact `RegisterCallback` matches) — needed because answer buttons are generated per-question at send time and can't be pre-registered as fixed strings the way Category's static set could in Phase 4.

**Duplicate/late-event handling beyond the state machine**: a duplicate answer-button tap for an already-answered question is rejected (`ErrAlreadyAnswered`) without a second WS send; a late `ANSWER_ACCEPTED` arriving after the client already timeout-advanced past that question still refreshes the leaderboard but does not re-advance (`awaitingAccept` flag). Unrecognized/unexpected events are logged (`slog` `Debug`/`Warn`), never treated as errors.

**Explicitly not done (by design, per this phase's scope)**: no resilience work — no reconnect after a disconnect, no retry/backoff on `READY`/`ANSWER` sends, no persistence of in-flight game state across a bot restart. A disconnect or ready-timeout mid-game simply resets to `Idle` with a message telling the user to `/play` again; that hardening is Phase 6. Also not done: manual end-to-end testing against a running backend (`docker-compose up`).

**Tests**: exhaustive state-machine table (`state_machine_test.go`, all 35 `(state, event)` pairs) plus determinism/regression tests; `game.Manager` tests (fake `Connection`/`Notifier`/`Detacher`, shrunk `readyTimeout` for the timeout tests) covering auto-ready, ready-timeout, deadline-sorted question presentation, timeout-driven auto-advance, answer submission (happy path, duplicate, stale-question), leaderboard update + advance, late-accept handling, completion + detach, duplicate completion, and disconnect cleanup; `telegram.Bot`'s new prefix-callback dispatch (exact-match priority, trimmed data, unrecognized data ignored); `matchmaking.Manager`'s hand-off wiring (`Attached` invoked with the right args, events forwarded post-match, `Detach` suppresses disconnect notification, disconnect after hand-off routes to the game layer not the matchmaking notifier). All pass under `go test -race`.

---

## Phase 6 — Resilience ✅ Implemented

**Goals**: The bot behaves gracefully under real-world network conditions.

**Deliverables**:
- WS reconnect logic with clear "context may be stale" messaging (per `client-business-flows.md` §8 — no server resync exists, so this is fundamentally best-effort)
- Client-side timeouts for silent WS commands (`READY` with no response, `GET_CATEGORIES`)
- Retry/backoff policy implementation per `client-architecture.md` §9
- Rate-limiting the bot's own outbound calls to avoid hammering the backend during retries
- Session store migration path to Redis (if not already done) for multi-instance readiness

**Dependencies**: Phases 4-5 (needs the WS/game flows to exist before hardening them).

**Risks**: Some resilience gaps (e.g. true reconnection with state resync) **cannot** be fully solved client-side — this phase should explicitly scope what's achievable now vs. deferred to backend changes tracked in `gap-analysis.md` (G-12, G-14, G-15).

**Estimated effort**: 4-5 days.

**Files/modules to create**: `pkg/retry/retry.go`, updates to `internal/apiclient/ws/client.go` (reconnect), `internal/core/session/store.go` (Redis impl).

**Implementation notes (2026-07-07)**: Before writing any code, this phase's scope was deliberately narrowed based on which failures are actually client-recoverable — see the reasoning below, since it drove several design decisions.

**What's client-recoverable vs. backend-only** (this is the "Risks" row above, made concrete): HTTP failures and WS *connect* failures are safely retryable (backoff, no ambiguity about server-side state). Bot-restart loss of the *auth* session is recoverable via persistence (Redis). A stale-but-present session (token likely expired, no way to check without an API call) is recoverable in the sense that the bot can *recognize* it and ask for `/login` again instead of confidently pretending it's still valid. **Not recoverable client-side**: token refresh (G-12, no endpoint exists), and — critically — mid-session state resync (G-14). A WS disconnect while queued or mid-game cannot be safely resumed: reconnecting the socket doesn't tell the bot whether it's still queued or was silently matched-and-abandoned while offline, and blindly re-sending `ADD_TO_WAITING_LIST` risks double-enqueueing (no idempotency key). **Decision: a disconnect always ends the current queue attempt/game locally** (unchanged from Phases 4-5's behavior) rather than attempting a "reconnect and resume," which would require guessing server-side truth the bot cannot know — this is the concrete application of "never fake state after reconnect." Consequently, "reconnection" in this phase means robust *connection establishment* (retried dialing with backoff), not *session resumption*.

**`pkg/retry`**: a generic `Do(ctx, Policy, func() error) error` backoff helper (`MaxAttempts`, `BaseDelay`, exponential, optional `MaxDelay` cap). `internal/apiclient/http.Client` was refactored to delegate to it internally (exact same retry counts/timing as before — all its existing tests pass unchanged); the WS-connect dialer wired in `cmd/bot/main.go` uses it with the `client-architecture.md` §9 policy (5 attempts, 1s/2s/4s/8s backoff, well under the documented ~30s ceiling).

**`pkg/ratelimit`**: a thin wrapper around `golang.org/x/time/rate` (`New(ratePerSecond, burst)`, `Wait(ctx)`), shared by `http.Client` (via a new `WithRateLimiter` option) and the WS dialer, so retries across many chats can't collectively hammer the gateway. Opt-in: `rate_limit.requests_per_second <= 0` (the default, i.e. the config section omitted) means unlimited — unchanged behavior for any existing deployment.

**Session recovery / Redis**: `session.RedisStore` (new `internal/core/session/redis_store.go` + `redis_client.go`) implements the existing `session.Store` interface against a minimal `RedisCmdable` interface (Get/Set/Del), so its tests run against an in-memory fake with no real Redis server; `NewGoRedisClient` adapts a real `*redis.Client` (`github.com/redis/go-redis/v9`) to that interface for production use. Keys are namespaced `telegram-bot:session:<chatID>`, separate from `game_app`'s own Redis usage. Selected via new config (`session_store.type: "memory"|"redis"`, `redis_addr`, `ttl`) — `"memory"` (including the section being entirely absent) preserves Phases 1-5's behavior exactly. **Only the auth session (tokens, user ID) is ever persisted** — no in-flight matchmaking/game state is written to Redis, deliberately: there's no backend API to resync a game after a restart, so persisting "you were on question 3" just to redisplay it later would mean fabricating what happened in the meantime. A bot restart cleanly loses any in-flight game; only login state survives.

**Session staleness**: `session.Session.Stale()` + `AccessTokenMaxAge` (24h, matching the documented dev-config token expiry) — `auth.Service.IsLoggedIn` and `profile.Service.Get` now treat a stale session the same as no session (and delete it), and `matchmaking.Manager.JoinQueue` fails fast with a new `ErrSessionExpired` before even dialing, rather than surfacing an opaque WS-upgrade rejection.

**Already satisfied from earlier phases**: the `READY`-timeout deliverable was implemented in Phase 5 (`game.Manager`'s ready-timeout). `GET_CATEGORIES` needs no timeout since this bot never sends it (categories are hardcoded per Phase 4's G-09 decision).

**Tests**: `pkg/retry` (success/retry/exhaustion, backoff growth, `MaxDelay` cap, context cancellation) and `pkg/ratelimit` (burst, throttling, cancellation) from scratch; `http.Client`'s existing retry tests pass unchanged post-refactor, plus a new rate-limiter test; `session.RedisStore` against a fake `RedisCmdable`; staleness tests in `session`, `auth`, `profile`, and `matchmaking` (fails fast without dialing). All pass under `go test -race`.

**Not done (out of scope, per the reasoning above)**: no reconnect-and-resume for a dropped queue/game — see "what's client-recoverable" above. No multi-instance coordination beyond what a shared Redis session store enables (no distributed locking, no sticky-session routing) — "multi-instance readiness" here means sessions survive *a* restart, not that multiple bot processes can safely share one game's WS connections. Manual end-to-end testing against a running backend + real Redis has not been done.

---

## Phase 7 — Polish ❌ Not Started

**Goals**: Production-readiness.

**Deliverables**:
- Metrics (bot-side: commands handled, API call latency/errors, active WS connections) — consistent with the backend's own metrics gap (`docs/context/07-known-issues.md` #7), so this is a good opportunity to not repeat that omission
- Structured logging review (no tokens/secrets logged)
- Test coverage: unit tests for `core/` (state machine, auth logic — no network needed), integration tests against a locally running backend
- Documentation: bot README, deployment guide
- Deployment: Dockerfile + manifest, mirroring `infra/deploy/*` conventions from the main repo

**Dependencies**: All prior phases.

**Risks**: Low — this is cleanup/hardening, not new integration risk.

**Estimated effort**: 3-4 days.

**Files/modules to create**: `deploy/Dockerfile`, `deploy/*.yaml`, test files across `internal/core/*`, `README.md`.

---

## Summary Timeline

| Phase | Effort | Cumulative | Status |
|---|---|---|---|
| 1. Project setup | 2-3 days | 3 days | ✅ Implemented |
| 2. Onboarding | 3-4 days | 7 days | ✅ Implemented |
| 3. Menu/Profile | 2 days | 9 days | ✅ Implemented |
| 4. Matchmaking | 4-5 days | 14 days | ✅ Implemented |
| 5. Gameplay | 6-8 days | 22 days | ✅ Implemented |
| 6. Resilience | 4-5 days | 27 days | ✅ Implemented |
| 7. Polish | 3-4 days | 31 days | ❌ Not started |

**Total: ~5-6 weeks solo**, excluding time for the backend improvements in `backend-improvements.md` that are recommended (not required) alongside this timeline. Phases 4-5 are the critical path and highest-risk; consider a spike/prototype of the WebSocket client against the real backend before committing to the full Phase 4 estimate.
