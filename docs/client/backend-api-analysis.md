# Backend API Analysis (Client Integration Perspective)

This document is ground truth extracted directly from source code (not from prior business docs) for anyone building a new client — Telegram bot or otherwise. Every claim below cites the file it came from. Where a prior doc (`docs/business/`, `docs/context/`) is more optimistic than the code, the code wins.

Assumptions are marked **[ASSUMPTION]**.

---

## 0. Topology Recap

| Service | HTTP | gRPC | Client-reachable via gateway? |
|---|---|---|---|
| `auth` | `:5000` | `:6000` | **No** — no Traefik/IngressRoute exists for auth-service. It is internal-only, called by (a) Traefik's ForwardAuth middleware directly, (b) `user` service via gRPC. |
| `user` | `:5001` | `:6001` (unused stub) | Yes — `/user-service/...` |
| `match` | `:5002` | none (skeleton only, never wired) | Yes — `/match-service/...` |
| `game` | `:5003` (HTTP + WS on same port) | n/a | Yes — `/game-service/...` |
| `question` | `:5004` | none | **No** — no Traefik route/docker-compose entry exists in this repo. Internal-only today. |

A Telegram bot (or any external client) can only reach `user`, `match`, and `game` through the gateway as currently deployed. **Question service has no external route** — this is a gap the client design must account for (see `gap-analysis.md`).

Source: `infra/kubernetes/ingress/ingress-route.yaml`, `docker-compose.yml`.

---

## 1. AUTH SERVICE — internal only

Not directly callable by a Telegram bot in the current deployment (no gateway route). Documented for completeness since a future client architecture may need direct access, or the gateway may be extended.

### `POST /api/v1/access-token`
- **Auth**: none (this endpoint mints tokens; it doesn't consume them)
- **Request**:
  ```json
  { "data": [ {"key": "id", "value": "123"}, {"key": "role", "value": "user"} ] }
  ```
- **Response 200**:
  ```json
  { "access_token": "string", "expire_time": 86400000 }
  ```
- **Errors**: `400` invalid body / missing `data`; `500` signing failure.
- **Purpose**: Mint a JWT access token from arbitrary claims.
- **Client feature**: None directly — only `user` service calls this today.
- **Known defect**: the JWT `exp` claim is set to the raw `time.Duration` value (nanoseconds), not `time.Now().Add(d).Unix()`. Token expiry may not behave as the `expire_time` field implies. Flag for backend fix before relying on expiry client-side.

### `POST /api/v1/refresh-token`
Same shape as above, mints a longer-lived token (`120h` dev config). Same defect applies.

### `POST /api/v1/validate-token` and `GET /api/v1/validate-token`
- **Auth**: token passed via `Authorization` header, not body.
- **Response 200**: `{ "valid": true, "data": [{"key":"id","value":"123"}, {"key":"role","value":"user"}] }`
- **Response 401**: `{ "valid": false }` (missing header, bad body, or invalid/expired JWT)
- **Purpose**: This is what Traefik's `forwardAuth` middleware calls on every protected request; it injects `X-User-ID`, `X-User-Role`, `X-Auth-Data` headers into the downstream request.
- **Client feature**: Indirect — every authenticated Telegram bot call rides on this.

**gRPC** (`auth.TokenService`, port `6000`): `GetAccessToken`, `GetRefreshToken` — same payloads as above, called only by `user` service today.

---

## 2. USER SERVICE — `/user-service/...`

### `GET /user-service/public/api/v1/health-check`
Public. Returns `{ "message": "everything is good!" }`. No dependency checks performed (doesn't ping Postgres).

### `POST /user-service/public/api/v1/signup`
- **Auth**: none (public group)
- **Request**:
  ```json
  { "email": "alice@example.com", "password": "hunter2" }
  ```
- **Validation**: email must contain exactly one `@`; local-part length `> 4`; domain length `> 2` and `< 50` (`pkg/email/emailValidation.go` — a manual check, not RFC-compliant regex). Password just needs `len > 0` — **no minimum length, complexity, or format requirement**.
- **Response 200**: `{ "displayName": "alice" }` (derived from the email local-part; the user can't choose their own display name)
- **Errors**:
  | Status | Body | Cause |
  |---|---|---|
  | 400 | `{"message":"invalid username or password"}` | malformed JSON |
  | 400 | `{"message":"invalid input","error":"invalid input"}` | invalid email format |
  | 403 | `{"message":"invalid username or password"}` | empty password (note: mapped to the *login* error message even though this is signup) |
  | 400 | `{"message":"username already exists"}` | duplicate email |
  | 500 | `{"message":"something went wrong"}` | bcrypt or DB failure |
- **Purpose**: Register a new player. Role is always `"user"` — no way to create an admin via this API.
- **Client feature**: `/register` or first-run onboarding flow.

### `POST /user-service/public/api/v1/login`
- **Auth**: none
- **Request**: `{ "email": "...", "password": "..." }` (both required)
- **Response 200**: `{ "id": "123", "accessToken": "...", "refreshToken": "..." }`
- **Errors**:
  | Status | Body | Cause |
  |---|---|---|
  | 400 | `{"message":"invalid username or password"}` | malformed JSON |
  | 403 | `{"message":"invalid username or password"}` | invalid email format, empty password, OR wrong password |
  | 500 | `{"message":"something went wrong"}` | **user not found** (bug: a nonexistent email returns 500, not 401/403/404 — the repository error for "no such user" is wrapped as `ErrInternal`) |
  | 500 | `{"message":"something went wrong"}` | Auth Service gRPC unreachable |
- **Client-critical defect**: `refreshToken` returned here is a bug in `adapter/auth/client.go` — confirmed **fixed** per repo history (R-01), so as of the current `develop` branch this should be a real refresh token. A client should still verify by decoding the JWT and checking its expiry claim differs from the access token's, given the separate `exp`-encoding defect noted above.
- **Purpose**: Authenticate and receive tokens.
- **Client feature**: `/login`, session bootstrap.

### `GET /user-service/api/v1/profile` (protected — ForwardAuth)
- **Auth**: `Authorization: Bearer <accessToken>`; Traefik injects `X-User-ID` before this handler runs.
- **Request**: no body.
- **Response 200**:
  ```json
  { "id": "123", "username": "alice@example.com", "displayName": "alice", "role": "user", "createdAt": 1719999999000, "updatedAt": 1719999999000 }
  ```
- **Errors**:
  | Status | Body | Cause |
  |---|---|---|
  | 400 | `"Invalid user id"` (bare JSON string, not an object — inconsistent with every other error shape in the system) | missing `X-User-ID` header (shouldn't happen through the gateway, but possible if called directly) |
  | 404 | `{"message":"user not found"}` | user deleted after token issued |
  | 500 | `{"message":"something went wrong"}` | DB failure |
- **Purpose**: Fetch the caller's own profile.
- **Client feature**: `/profile` command, main menu display.

**gRPC** (port `6001`): registered but **no service is actually bound to the server** — `GetCustomer` exists as a bare Go method, not wired to any `.proto` contract. Treat as non-functional; do not build against it.

---

## 3. MATCH SERVICE — `/match-service/api/v1/...`

Entire router (including health-check) sits behind Traefik's `auth-middleware` in the current deployment — there is no public sub-router for match, unlike user-service.

### `GET /match-service/api/v1/health-check`
Protected (per current deployment config, unlike its name might suggest). Returns `{ "message": "everything is good!" }`.

### `POST /match-service/api/v1/addToWaitingList`
- **Auth**: `Authorization: Bearer <token>` (ForwardAuth) + handler also independently requires `X-User-ID`.
- **Request**: `{ "category": "SPORT" }` — must be exactly `SPORT`, `MUSIC`, or `TECH` (case-sensitive).
- **Response 200**: `{ "timeout": 1200000000000 }` — this is a raw Go `time.Duration` serialized as **nanoseconds**, not a human string or seconds. A client must divide by `1e9` to get seconds (20 minutes in dev config).
- **Errors**:
  | Status | Body | Cause |
  |---|---|---|
  | 400 | `"Invalid user id"` (bare string) | missing `X-User-ID` |
  | 400 | `"invalid category"` (bare string) | malformed JSON body |
  | 400 | `{"message":"invalid input"}` | category not in `{SPORT,MUSIC,TECH}` |
  | 500 | `{"message":"something went wrong"}` | Redis failure |
- **Purpose**: HTTP alternative to the WebSocket `ADD_TO_WAITING_LIST` command. **Important**: joining via this endpoint alone does not let the player receive `MATCH_CREATED` — that only arrives over the game-service WebSocket. A client must have a WS connection open regardless of which path it used to join the queue.
- **Client feature**: Not needed if the bot always uses the WebSocket path (recommended — see `client-architecture.md`).

No gRPC server is actually running for match-service despite config/proto scaffolding existing.

---

## 4. GAME SERVICE — `/game-service/api/v1/...` (HTTP + WebSocket, same port)

### `GET /game-service/api/v1/health-check`
Public in effect (reads but doesn't validate auth headers). `{ "message": "everything is good!" }`.

### `GET /game-service/api/v1/process-game` — **WebSocket upgrade endpoint**

This is the single most important endpoint for the client and the only way to actually play a game.

- **Auth**: `Authorization: Bearer <token>` for ForwardAuth (gateway-level) — **but the game-service handler itself only checks for a raw `X-User-ID` header**, with no token/signature validation of its own. In production this header only exists because Traefik injected it after ForwardAuth succeeded, but if a client could reach the game-service directly (bypassing the gateway) it could impersonate any user ID. This is an internal trust boundary a client developer should be aware of but cannot fix — connect through the gateway URL.
- **Handshake**: standard WebSocket upgrade (`ws://` or `wss://` scheme), no subprotocol negotiation used by the app.
- **Immediate server push after connect** (only if this is the user's first-ever connection, or their cached status expired to `UNKNOWN`): a **non-enveloped** raw JSON object (not wrapped in the standard event envelope used everywhere else):
  ```json
  { "categories": ["SPORT", "MUSIC", "TECH"], "numberOfPlayers": [2] }
  ```
  If the user reconnects mid-flow (status is `INITIALIZED`/`PENDING`/`CREATED`/`STARTED`/`FINISHED`), **nothing is sent automatically** — the client must already know its `gameId`/state from before the disconnect, since there is no state resync.

#### Client → Server commands
Standard envelope:
```json
{ "command": "READY", "gameId": "...", "matchId": "...", "category": "...", "players": 2, "answer": { "gameId": "...", "questionId": "...", "choice": "A" } }
```
(Only the fields relevant to a given command need to be populated; unused fields are ignored.)

| Command | Required fields | Purpose |
|---|---|---|
| `ADD_TO_WAITING_LIST` | `category` | Join matchmaking queue for a category |
| `READY` | `gameId` | Signal readiness to start after a match is formed |
| `ANSWER` | `answer.gameId`, `answer.questionId`, `answer.choice` | Submit an answer |
| `GET_CATEGORIES` | none | **No-op today — sends no response.** Do not rely on this; categories only arrive automatically on first connect. |

#### Server → Client events
Envelope: `{ "success": bool, "event": "...", "message": "...", "metaData": {...} }`

| Event | When | metaData contents |
|---|---|---|
| `ADDED_TO_WAITING_LIST` | Reply to `ADD_TO_WAITING_LIST` | empty |
| `MATCH_CREATED` | Broadcast when matchmaking pairs this player | `gameId` |
| `QUESTIONS_PUBLISHED` | Broadcast once **all** expected players send `READY` | `gameId`, `questions[]` (full set, each with its own `ttl` deadline, delivered all at once) |
| `ANSWER_ACCEPTED` | Reply to a valid `ANSWER` | `leaderBoard` (full recomputed leaderboard, not a delta) |
| `COMPLETED` | Server-scheduled task fires after all question deadlines pass | `leaderBoard` (final) — **connection is closed by the server immediately after sending this** |
| `ERROR` | Various failure conditions (see below) | usually empty |

#### Documented WebSocket quirks a client MUST handle defensively (verified in code, not hypothetical)
1. **Invalid category on `ADD_TO_WAITING_LIST` sends BOTH an `ERROR` and a subsequent `ADDED_TO_WAITING_LIST`.** A client must not treat receipt of a success event as proof the preceding error didn't happen — check the error first.
2. **`READY` sent before all players are ready produces NO response at all.** A client cannot distinguish "still waiting for others" from "my READY was silently dropped" except by absence of `QUESTIONS_PUBLISHED`. Recommend a client-side timeout/UX indicator, not a request-response assumption.
3. **`READY` sent twice by the same player is silently ignored** (error only logged server-side).
4. **`GET_CATEGORIES` never responds.**
5. **`ANSWER` with an unrecognized `questionId` is accepted silently with 0 points** — no error surfaces.
6. **Answers submitted after the deadline are accepted (not rejected), scored 0.** Only answers submitted *too early* (before the anti-cheat window opens) are rejected with `ERROR: "answered to question quickly"`.
7. **No reconnection/session-resume protocol.** A dropped connection loses all in-flight event delivery; reconnecting with the same `X-User-ID` silently replaces the old connection entry (old socket isn't proactively closed) but the server does **not** replay missed events (`MATCH_CREATED`, `QUESTIONS_PUBLISHED` while disconnected are lost). The client must persist its own `gameId`/`matchId`/last-known-state locally (or the bot backend must) to have any hope of resuming a session — see `gap-analysis.md`.
8. **No message-size limits or rate limiting** on the WS layer — not a client concern to work around, but don't assume the server protects itself; the bot's own client library should apply sane limits.
9. **Commands can be sent in any order** with no server-side state machine gate — e.g. sending `ANSWER` before a match even exists just falls through to a generic `{"event":"ERROR","message":"something went wrong"}`. A client-side (bot) state machine should prevent sending commands out of the expected sequence rather than relying on the server to reject them meaningfully.
10. **`ERROR.message` is frequently the generic string `"something went wrong"`** with no machine-readable code — a client cannot branch logic on error type beyond a few specific strings (`"invalid category"`, `"answered to question quickly"`, `"player <id> already answered this question <qid>"`, `"invalid command"`).

No other REST endpoints exist on game-service — there is no "get current game state," "get leaderboard," or "get game history" HTTP API. Everything is WebSocket-driven and ephemeral.

---

## 5. QUESTION SERVICE — internal only, not reachable via gateway

### `GET /api/v1/question/health-check` — public, no dependency checks.

### `POST /api/v1/question/add` — **stub, non-functional**
Both request and response are empty structs (`{}`); the handler performs no validation, no persistence, and always returns `200 {}`. There is no working way to add questions to the bank via any API today — questions must be seeded directly into PostgreSQL.

**Client feature**: None — and even if this service were exposed via the gateway, this endpoint could not support an "admin adds a question" bot feature without backend work.

---

## 6. Kafka Events (not directly client-facing, but shape what the WebSocket eventually delivers)

| Topic | Producer → Consumer | Client relevance |
|---|---|---|
| `GAME_V1_JOIN_MATCH_QUEUE_REQUESTED` | game → match | Indirect: this is how a WS `ADD_TO_WAITING_LIST` command actually lands in the matchmaking queue. |
| `matchMaking_v1_matchUsers` | match → question, game | Indirect: gates when a match/game is created and when questions get generated. |
| `question_v1_questions` | question → game | Indirect: last hop before `QUESTIONS_PUBLISHED` can be sent over WS. |

A client never touches Kafka directly; it's documented here only so a client developer understands the ~15-second scheduler latency and multi-hop pipeline between "join queue" and "match found" (see `client-business-flows.md` for timing).

---

## 7. Missing / Duplicate / Problematic Endpoints for Client Integration

### Missing entirely
- **Token refresh exchange endpoint** (`refresh token → new access token`). The auth-service can *mint* a refresh token, but nothing consumes one to issue a fresh access token. A client has no way to avoid forcing re-login after 24h without this.
- **Logout / token revocation.** JWTs are stateless with no blacklist; a bot "logout" command can only forget the token locally.
- **Get current game/match state** (HTTP, for a bot that reconnects after a backend restart or user restarts the chat). Nothing lets a client ask "what game am I in right now?" outside of the ambiguous WS reconnect behavior in §4.
- **Get game history / past results.** No retrieval API — `docs/business/03-feature-map.md` confirms this was never built.
- **Leave matchmaking queue.** Only joining is supported; no "cancel" command exists.
- **Queue/queue-depth visibility.** No way to show a user "N players waiting in SPORT."
- **Admin question management** — see §5, stub only.
- **Question service gateway route** — service exists and works internally but is unreachable externally; blocks any bot feature that would need to browse/manage questions.

### Duplicate / redundant
- **Joining matchmaking exists twice**: WS `ADD_TO_WAITING_LIST` command and HTTP `POST /match-service/api/v1/addToWaitingList` do the same Redis write, but only the WS path lets the client subsequently receive `MATCH_CREATED`. The HTTP path is effectively dead for a Telegram bot client and should be ignored in the architecture (use WS exclusively).

### Difficult for client integration (design smells to work around, not fix)
- Inconsistent error envelopes: most endpoints return `{"message":"...","error":"..."}`, but `user-service` profile and `match-service` X-User-ID/category errors return **bare JSON strings**, not objects. A client's HTTP layer needs to defensively parse both shapes.
- `match-service`'s `timeout` field is nanoseconds as a raw number, inconsistent with human-readable durations elsewhere.
- WebSocket has no correlation ID between a command and its response — a client sending multiple `ANSWER`s in flight cannot definitively match a given `ANSWER_ACCEPTED` back to the triggering command except by `questionId` inference from the leaderboard payload (which doesn't even echo `questionId` back at top level).
- No structured/machine-readable error codes over WebSocket (see §4, point 10) — client must pattern-match on message strings, which is brittle.

---

## 8. Recommended Improvements (summary — see `backend-improvements.md` for full detail)

1. Add a token refresh endpoint (`user-service` or `auth-service`).
2. Add a "get my current game state" HTTP endpoint on game-service for reconnect support.
3. Add a "leave waiting list" command/endpoint.
4. Standardize all error response bodies to the `{"message","error","code"}` shape.
5. Add machine-readable error `code` fields to WebSocket `ERROR` events.
6. Expose question-service through the gateway (or accept it stays internal and the bot never needs it).
7. Fix the two non-response WS cases (`READY` before ready, `GET_CATEGORIES`) to always emit *some* acknowledgment.
