# Gap Analysis — Backend Readiness for a Telegram Client

Categories: **Already supported**, **Needs small backend changes**, **Needs major backend changes**, **Missing entirely**.

---

## Already Supported

### G-01: Registration
- **Backend files**: `services/user_app/delivery/http/handler.go`, `service/service.go` (`SignUp`)
- **Why it works**: `POST /user-service/public/api/v1/signup` is complete and public.
- **Effort to use from bot**: None — call as-is.

### G-02: Login + token issuance
- **Backend files**: `services/user_app/service/service.go` (`Login`), `adapter/auth/client.go`
- **Why it works**: Returns access + refresh tokens; the R-01 bug (refresh token bug) is fixed per repo history.
- **Effort**: None — call as-is, but see G-08 below for the token-refresh gap that limits its usefulness.

### G-03: Profile retrieval
- **Backend files**: `services/user_app/delivery/http/handler.go` (`Profile`)
- **Why it works**: Works correctly behind ForwardAuth.
- **Effort**: None.

### G-04: Matchmaking join + pairing
- **Backend files**: `services/game_app/service/websocket_handler.go`, `services/match_app/service/scheduler.go`
- **Why it works**: The WS `ADD_TO_WAITING_LIST` → Kafka → Redis → scheduler → `MATCH_CREATED` pipeline is fully implemented and working end-to-end.
- **Effort**: None — bot just needs a correct WS client.

### G-05: Full gameplay loop (ready, questions, answer, scoring, leaderboard, completion)
- **Backend files**: `services/game_app/service/{websocket_handler,answer_processor,game_lifecycle,game_completion}.go`, `services/game_app/repository/game.go`
- **Why it works**: All confirmed implemented and functioning (ready-check off-by-one fixed R-04; scoring, anti-cheat window, leaderboard aggregation all work).
- **Effort**: None — bot implements the client side of an already-correct protocol.

---

## Needs Small Backend Changes

### G-06: Ambiguous login failure (500 for "user not found")
- **Why it exists**: `services/user_app/repository/user.go` `GetUser` wraps a "no rows" DB error as `errApp.ErrInternal` instead of a distinct not-found/invalid-credentials error.
- **Backend files involved**: `services/user_app/repository/user.go`, `services/user_app/service/service.go` (`Login`)
- **Suggested solution**: Wrap "no rows" as `errApp.ErrInvalidLOGIN` (403) so it's indistinguishable from "wrong password" (which is arguably the correct security posture — don't reveal whether an email exists — but currently it's indistinguishable from a *real server failure*, which is the actual bug). Map DB "no rows" → 403, keep genuine DB connectivity errors → 500.
- **Estimated effort**: Trivial (< 1 hour).
- **Priority**: Medium — improves bot UX (can show "wrong email/password" confidently) without which the bot must keep messaging vague.

### G-07: Inconsistent error response shapes
- **Why it exists**: Ad-hoc error paths (`errmsg.MessageMissingXUserId`, `errmsg.InvalidCategory`) return bare JSON strings; most other paths return `{"message","error"}` objects.
- **Backend files involved**: `pkg/err_msg/err_msg.go`, handlers in `user_app`, `match_app` that use these constants directly instead of `errApp`.
- **Suggested solution**: Route all error responses through `errApp.New(...)` / `ToHTTPJson` so every error is `{"message","error","code"}` (adding a `code` field per G-11 below while at it).
- **Estimated effort**: Small (few hours — touches ~4-5 call sites).
- **Priority**: Medium — reduces client-side branching complexity.

### G-08: `READY` produces no acknowledgment when not all players are ready
- **Why it exists**: `service/websocket_handler.go`'s `READY` case only writes a response in the `isGameReady == true` branch.
- **Backend files involved**: `services/game_app/service/websocket_handler.go`
- **Suggested solution**: Add a lightweight ack event, e.g. `{"event":"READY_ACKNOWLEDGED","metaData":{"playersReady":1,"playersExpected":2}}`, sent regardless of whether the game is fully ready.
- **Estimated effort**: Small (a few hours, plus a test).
- **Priority**: Medium-High — directly removes one of the bot's biggest UX workarounds (blind client-side timeout).

### G-09: `GET_CATEGORIES` command is a no-op
- **Why it exists**: `service/websocket_handler.go` handles the case with only a `fmt.Println`.
- **Backend files involved**: `services/game_app/service/websocket_handler.go`
- **Suggested solution**: Make it call the same `getCategories()` helper used on fresh-connect and send the response, so a bot reconnecting mid-session (status != UNKNOWN) can still fetch the category list on demand instead of hardcoding it client-side.
- **Estimated effort**: Trivial (< 1 hour).
- **Priority**: Low — bot can hardcode categories as a workaround since they're static (`SPORT/MUSIC/TECH`) today.

### G-10: `ADD_TO_WAITING_LIST` sends contradictory ERROR+success frames on invalid category
- **Why it exists**: Missing `return` statements after the error-write in `readMessage`'s `ADD_TO_WAITING_LIST` case (`services/game_app/service/websocket_handler.go`).
- **Suggested solution**: Add `return` after each `writeMessage(id, errorResponse)` call in that branch so an invalid category or publish failure stops processing instead of falling through to send a false "success."
- **Estimated effort**: Trivial (< 1 hour, 2-3 line fix + test).
- **Priority**: Medium — currently the bot must defensively ignore the following success event, which is fragile.

### G-11: No machine-readable error codes over WebSocket
- **Why it exists**: The WS envelope (`ProcessGameMessageResponse`) has no `code` field, only free-text `message`.
- **Backend files involved**: `services/game_app/service/param.go` (envelope struct), all `writeMessage` call sites
- **Suggested solution**: Add a `code` string field (e.g. `"INVALID_CATEGORY"`, `"ANSWERED_TOO_EARLY"`, `"DUPLICATE_ANSWER"`, `"INVALID_COMMAND"`) alongside `message`, sourced from the same constants the backend already uses internally for HTTP errors (`pkg/err_app`).
- **Estimated effort**: Small (half a day — requires enumerating all WS error sites and assigning codes).
- **Priority**: Medium — meaningfully de-risks the bot's error handling (string-matching is brittle across backend releases).

---

## Needs Major Backend Changes

### G-12: No token refresh endpoint
- **Why it exists**: `auth_app` can mint access/refresh tokens but nothing exchanges a refresh token for a new access token. This was already known (`docs/business/03-feature-map.md`, `docs/business/04-project-roadmap.md` Phase 2).
- **Backend files involved**: `services/auth_app/delivery/http/*`, `services/auth_app/service/service.go`, `services/user_app` (likely the right place to expose it publicly, proxying to auth-service, matching the existing login pattern)
- **Suggested solution**: Add `POST /user-service/public/api/v1/refresh` accepting `{refreshToken}`, validating it via auth-service, and minting a fresh access token. Requires distinguishing access vs. refresh tokens by a claim (e.g. `type: "access"|"refresh"`) since today's JWTs don't self-identify their type.
- **Estimated effort**: Medium (2-3 days — new endpoint, new gRPC call or reuse existing validate-token with a type check, tests).
- **Priority**: High — without this, every BrainBlitz user (bot or otherwise) is forced to fully re-login every 24 hours, which is a poor experience for a chat bot where re-typing a password is especially annoying.

### G-13: No "leave matchmaking queue" capability
- **Why it exists**: Only `AddToWaitingList` exists in `match_app`'s repository/service; no `RemoveFromWaitingList` is exposed as a command or endpoint (internally `RemoveWaitingMember` exists but is only called by the scheduler after a successful match, not on user request).
- **Backend files involved**: `services/match_app/repository/match.go` (has `RemoveWaitingMember` — reusable), `services/match_app/service/service.go`, `services/game_app/service/websocket_handler.go` (new command needed)
- **Suggested solution**: Add a `LEAVE_WAITING_LIST` WS command (game-service) that publishes a new Kafka event or calls match-service directly to remove the user's Redis sorted-set entry.
- **Estimated effort**: Medium (2-3 days — new command, new Kafka topic or direct call path, tests).
- **Priority**: Medium-High — a bot's "Cancel" button is currently cosmetic; real cancellation meaningfully improves trust in the UI.

### G-14: No mid-game reconnection / state resync
- **Why it exists**: WebSocket connections are purely in-memory (`svc.connections map[uint64]net.Conn`); no event log or "catch-up" mechanism exists. This is a known architectural limitation (`docs/context/08-ai-context.md` — "WebSocket connections are in-memory").
- **Backend files involved**: `services/game_app/service/websocket_handler.go`, `services/game_app/repository/game.go` (would need a way to answer "what's the current state/last-sent-event for gameId X, playerId Y")
- **Suggested solution**: On reconnect, if the user's Redis `GameStatus` is `PENDING`/`STARTED`, look up the cached `GameQuestions` and current leaderboard and replay a synthetic `QUESTIONS_PUBLISHED`/state snapshot to the newly (re)connected socket, instead of doing nothing (current behavior for all non-`UNKNOWN` statuses).
- **Estimated effort**: Medium-Large (3-5 days — needs careful handling of already-answered questions per player, partial TTL remaining, tests for every status branch).
- **Priority**: High for a chat-bot client specifically — Telegram users routinely background the app; treating every backgrounding as "you may have lost your game" is a much worse experience for a bot than for a persistent web/mobile client.

### G-15: No abandonment/timeout detection for a non-responsive opponent
- **Why it exists**: `UpsertReadyPlayer`'s ready-check has no timeout; a game can be permanently stuck waiting for a `READY` that never comes.
- **Backend files involved**: `services/game_app/repository/game.go`, `services/game_app/service/websocket_handler.go`
- **Suggested solution**: Add a scheduled/deferred check (Asynq task, similar to `game:completed`) fired shortly after `MATCH_CREATED` that, if not all players have readied up within N seconds, notifies remaining players and/or returns them to matchmaking.
- **Estimated effort**: Medium (2-3 days).
- **Priority**: Medium — currently mitigated client-side by a bot timeout + apology message, but the *game itself* (and the other player, if using a different client) stays stuck forever server-side, which is a real defect beyond just bot UX.

---

## Missing Entirely

### G-16: Question service has no external gateway route
- **Why it exists**: No `docker-compose.yml` entry, no Traefik `IngressRoute`, no Kubernetes deployment manifest exists for `question_app` (confirmed: `infra/kubernetes/deployment/` lacks a question manifest; known issue #14 in `docs/context/07-known-issues.md`).
- **Backend files involved**: `infra/kubernetes/deployment/` (needs new manifest), `infra/kubernetes/ingress/ingress-route.yaml` (needs new route), `docker-compose.yml` (needs new service block)
- **Suggested solution**: Add deployment + ingress route following the existing pattern for match/game services.
- **Estimated effort**: Small (< 1 day, purely infra/config).
- **Priority**: Low for the bot's MVP (no bot feature needs it yet) — becomes High if/when an admin "add question" bot feature is desired, which also requires G-17.

### G-17: Admin question management
- **Why it exists**: `AddQuestion` is an empty stub end-to-end (`services/question_app/service/service.go`); no listing/search/edit APIs exist at all.
- **Backend files involved**: `services/question_app/service/service.go`, `services/question_app/repository/questions.go`, `services/question_app/delivery/http/handler.go`
- **Suggested solution**: Implement `InsertQuestion` in the repository, wire the handler, add role-gated auth (no role-checking middleware exists anywhere in the codebase today — that's an additional prerequisite).
- **Estimated effort**: Large (1 week+ — includes building the first-ever role-based authorization check in the system).
- **Priority**: Low — out of scope for a player-facing bot MVP; relevant only if an "admin bot" feature is later desired.

### G-18: Game history / past results retrieval
- **Why it exists**: No API ever queries `player_answers`/`game` collections for a user's history; game documents aren't even marked `FINISHED` on completion (known issue #15).
- **Backend files involved**: `services/game_app/repository/game.go` (needs new query methods), `services/game_app/delivery/http/*` (needs new HTTP route — game-service currently has zero REST endpoints beyond health-check)
- **Suggested solution**: First fix game-status-on-completion (small, already tracked as R-15 in refactoring docs), then add `GET /game-service/api/v1/history` returning a paginated list of past games/scores for the caller.
- **Estimated effort**: Medium (2-3 days once status-tracking is fixed).
- **Priority**: Low-Medium — a natural "nice to have" bot feature (`/history` command) but not required for MVP gameplay.

### G-19: Profile editing (display name, password change)
- **Why it exists**: Never built — `docs/business/03-feature-map.md` already lists "Password change" as Missing.
- **Backend files involved**: `services/user_app/service/service.go`, `services/user_app/repository/user.go`, new HTTP route
- **Suggested solution**: Add `PATCH /user-service/api/v1/profile` for display name; separate password-change flow with current-password verification.
- **Estimated effort**: Medium (2 days).
- **Priority**: Low — not needed for MVP.

### G-20: Queue depth / matchmaking visibility
- **Why it exists**: No endpoint exposes how many users are waiting per category.
- **Backend files involved**: `services/match_app/repository/match.go` (has the Redis sorted sets already — `ZCard` would suffice), new HTTP route
- **Suggested solution**: Add `GET /match-service/api/v1/queue-status?category=SPORT` returning a count.
- **Estimated effort**: Trivial (few hours).
- **Priority**: Low — nice-to-have "X players waiting" UX polish, not functionally required.

---

## Priority Summary for Bot Launch

| Priority | Items | Blocks MVP? |
|---|---|---|
| Must-fix before/soon-after launch | G-12 (token refresh), G-14 (reconnect/resync) | No — bot can launch with documented workarounds (force re-login, "game may be lost on disconnect" messaging), but both should be fast follows |
| Should-fix early | G-06, G-08, G-09, G-10, G-11, G-13, G-15 | No — all have viable client-side workarounds |
| Nice-to-have | G-16 through G-20 | No — genuinely out of scope for MVP |

See `client-roadmap.md` for how these map into implementation phases, and `backend-improvements.md` for the full technical proposal per item.
