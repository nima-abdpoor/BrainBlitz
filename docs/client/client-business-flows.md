# Client Business Flows

This maps the full player journey as a client (Telegram bot or otherwise) would experience it, based on the verified API surface in `backend-api-analysis.md`. It supersedes `docs/business/02-business-flows.md` for client-facing purposes by adding failure/retry/timeout detail that document didn't need to cover.

## 1. End-to-End User Journey

```mermaid
flowchart TD
    A[User opens chat / app] --> B{Has stored session?}
    B -- No --> C[Register or Login]
    B -- Yes, token valid --> F[Main Menu]
    B -- Yes, token expired --> C
    C --> D[POST /user-service/public/api/v1/signup or /login]
    D -- success --> E[Store accessToken + refreshToken + userId]
    E --> F[Main Menu]
    F --> G[View Profile]
    F --> H[Play Game]
    H --> I[Open WebSocket to game-service]
    I --> J[Choose category]
    J --> K[Send ADD_TO_WAITING_LIST]
    K --> L[Waiting for opponent]
    L -- MATCH_CREATED --> M[Send READY]
    M --> N{All players ready?}
    N -- No response yet --> L2[Show waiting UI, poll nothing, just wait]
    N -- QUESTIONS_PUBLISHED --> O[Answer loop]
    O --> P[Send ANSWER per question]
    P -- ANSWER_ACCEPTED --> O
    O -- all deadlines pass --> Q[COMPLETED: final leaderboard]
    Q --> R[Connection closed by server]
    R --> F
```

## 2. Registration / Login

```mermaid
sequenceDiagram
    participant U as Telegram User
    participant B as Bot Backend
    participant US as User Service

    U->>B: /start
    B->>U: Register or Login?
    U->>B: provides email + password
    B->>US: POST /user-service/public/api/v1/signup
    alt success
        US-->>B: 200 {displayName}
        B->>US: POST /user-service/public/api/v1/login
        US-->>B: 200 {id, accessToken, refreshToken}
        B->>B: store session keyed by Telegram chat/user ID
        B->>U: Welcome, main menu
    else duplicate email (400)
        US-->>B: 400 "username already exists"
        B->>U: "Already registered — try /login"
    else invalid email/password (400/403)
        US-->>B: 400/403 error
        B->>U: "Invalid email or password, try again"
    else 500
        US-->>B: 500 "something went wrong"
        B->>U: "Something went wrong, please retry"
    end
```

**Failure/retry notes**:
- No idempotency concern: signup either creates the user or fails with a duplicate error the bot can catch and redirect to login.
- Login "user not found" incorrectly returns `500`, indistinguishable from a real server error. The bot **cannot reliably tell "wrong email" apart from "server is down"** from this response alone — it should present a generic "check your credentials or try again shortly" message rather than a confident "user not found."
- No retry-after / backoff signal from the backend on `500` — bot should apply its own exponential backoff (e.g. 3 attempts, 1s/2s/4s) for transient failures, but not for `400`/`403` (those are certain user errors).

## 3. Token Lifecycle

```mermaid
stateDiagram-v2
    [*] --> NoSession
    NoSession --> ActiveSession: login success
    ActiveSession --> ActiveSession: API calls with valid accessToken
    ActiveSession --> Expired: accessToken > 24h old
    Expired --> NoSession: no refresh endpoint exists today
    NoSession --> ActiveSession: user logs in again
```

**Critical gap**: because there is no token-refresh HTTP endpoint (see `backend-api-analysis.md` §7), the bot cannot silently renew a session. Once the 24h access token expires, the stored `refreshToken` is currently useless from the client's perspective — the bot must force the user through `/login` again. This must be designed around explicitly (see `gap-analysis.md` — categorized as "Needs small backend changes").

## 4. Matchmaking — Join, Wait, Match Found

```mermaid
sequenceDiagram
    participant U as User
    participant B as Bot (WS client)
    participant GS as Game Service
    participant K as Kafka
    participant MS as Match Service (scheduler, every 15s)

    U->>B: /play
    B->>GS: WS connect (X-User-ID via gateway auth)
    GS-->>B: {categories, numberOfPlayers} (only if first connect)
    B->>U: Choose category
    U->>B: SPORT
    B->>GS: {command: ADD_TO_WAITING_LIST, category: SPORT}
    GS-->>B: ADDED_TO_WAITING_LIST
    GS->>K: publish GAME_V1_JOIN_MATCH_QUEUE_REQUESTED
    K->>MS: consumed, user added to Redis waiting list
    Note over MS: up to ~15s scheduler tick, plus needs >=2 same-category waiters within 20 min window
    MS->>K: publish matchMaking_v1_matchUsers (once paired)
    K->>GS: consumed, game created in MongoDB
    GS-->>B: MATCH_CREATED {gameId}
    B->>U: Opponent found! Get ready...
```

**Timeout/retry behavior**:
- **No timeout exists server-side** for a lone waiter — if no second player joins the same category within the 20-minute window, the user simply waits forever with no expiry notification. The bot must implement its own client-side "give up after N minutes" UX (e.g. offer a Cancel button) since there is no "leave queue" backend command either (see gap analysis) — cancelling is a UI-only illusion unless the backend adds a leave-queue endpoint.
- Typical match latency: 0–15s scheduler tick + Kafka propagation (sub-second in practice) — the bot should show a "matching..." indicator rather than expect near-instant response.
- If the WS connection drops while waiting, the user's Redis waiting-list entry is untouched (still queued) but they will not receive the eventual `MATCH_CREATED` push unless they reconnect with the same `X-User-ID` before the match is dispatched.

## 5. Game Start — Ready Synchronization

```mermaid
sequenceDiagram
    participant B1 as Bot (Player 1)
    participant B2 as Bot (Player 2)
    participant GS as Game Service

    B1->>GS: {command: READY, gameId}
    Note over GS: 1 of 2 ready — NO response sent
    B2->>GS: {command: READY, gameId}
    Note over GS: 2 of 2 ready — starts game
    GS-->>B1: QUESTIONS_PUBLISHED {questions[]}
    GS-->>B2: QUESTIONS_PUBLISHED {questions[]}
```

**Failure scenario**: if Player 1 sends `READY` but Player 2 never does (disconnected, closed the app, ignored the prompt), Player 1 receives **no error, no timeout notification, nothing** — they wait indefinitely. There is no server-side abandonment detection. A production-quality bot should apply a client-side wait timeout (e.g. 60–90s) and, on expiry, tell the user the opponent didn't respond — but cannot actually cancel or requeue the match server-side today (no such endpoint exists).

## 6. Answering Questions

```mermaid
sequenceDiagram
    participant U as User
    participant B as Bot
    participant GS as Game Service

    GS-->>B: QUESTIONS_PUBLISHED {questions: [{id, content, choices, ttl}, ...]}
    B->>U: Show question 1 with countdown to its ttl
    U->>B: picks choice "A"
    B->>GS: {command: ANSWER, answer: {gameId, questionId, choice: "A"}}
    alt answered within window
        GS-->>B: ANSWER_ACCEPTED {leaderBoard}
        B->>U: Show updated leaderboard, next question
    else answered too early (before anti-cheat window opens)
        GS-->>B: ERROR "answered to question quickly"
        B->>U: Retry — this should not normally happen if bot paces UI correctly
    else duplicate answer
        GS-->>B: ERROR "player already answered this question"
        B->>U: Ignore — bot should prevent double-submit client-side
    else answered after deadline
        GS-->>B: ANSWER_ACCEPTED {leaderBoard} (0 points, not an error)
        B->>U: Show 0 points for that question
    end
```

**Design implication for the bot**: because all questions are delivered up front (`QUESTIONS_PUBLISHED` sends the whole array with individual per-question `ttl` deadlines, not one at a time), the bot is responsible for pacing the UI — showing question N only when its answer window is open, and locally enforcing "don't let the user submit before this question's window starts" to avoid the confusing `"answered to question quickly"` error. This is entirely a client-side state machine concern; the backend gives no "next question please" signal.

## 7. Game Completion

```mermaid
sequenceDiagram
    participant GS as Game Service (Asynq timer)
    participant B1 as Bot (Player 1)
    participant B2 as Bot (Player 2)

    Note over GS: total TTL elapses (sum of all question deadlines)
    GS-->>B1: COMPLETED {leaderBoard}
    GS-->>B2: COMPLETED {leaderBoard}
    GS->>GS: closes both WebSocket connections
    B1->>B1: connection closed — must reconnect for next game
    B2->>B2: connection closed — must reconnect for next game
```

**Failure scenario**: if the leaderboard fetch fails server-side, players get `COMPLETED` with `success:false` and an empty leaderboard instead — the bot should treat this as "game ended, results unavailable" rather than crash on missing fields.

## 8. Reconnection (Current State — No Real Support)

```mermaid
flowchart TD
    A[WS connection drops] --> B{What was player doing?}
    B -- Was waiting in queue --> C[Reconnect: still queued server-side,\nbut misses MATCH_CREATED if it fired while offline]
    B -- Was mid-game, hadn't sent READY --> D[Reconnect: game may be stuck forever\nif this was the last READY needed]
    B -- Was mid-game, questions already published --> E[Reconnect: no resend of QUESTIONS_PUBLISHED;\nclient has lost the question set unless it cached it locally]
    B -- Game already completed --> F[Reconnect: connection was already closed by server;\nfresh connect just gets a new session]
```

**Implication**: the bot backend (not just the Telegram client) MUST persist per-user in-flight game state (last known `gameId`, `matchId`, received questions, current leaderboard) in its own storage, because the backend provides no replay/resync mechanism. This is a core architectural requirement, not an optional resilience nicety — see `client-architecture.md` §Session Management and `gap-analysis.md`.

## 9. Error Handling Summary Table

| Failure class | Backend behavior | Bot must... |
|---|---|---|
| HTTP 5xx from user/match service | Generic message, no retry hint | Apply its own backoff/retry (idempotent GETs safe to retry; POSTs like signup/login safe to retry since they're naturally idempotent-ish by error type) |
| WS command silently ignored (READY, GET_CATEGORIES) | No response frame at all | Apply client-side timeouts, never block waiting for an ack that may never come |
| WS command produces contradictory frames (bad category) | ERROR then success | Process events in order; don't assume one cancels the other |
| WS connection drop | No cleanup, no resync | Persist state locally; treat reconnect as "new socket, old context" |
| Kafka/scheduler pipeline delay | Up to ~15s + processing | Show a "matching..." spinner, not a spinner that times out prematurely |

## 10. Retry & Timeout Policy Recommendations for the Bot (client-side only — no backend changes assumed)

| Scenario | Recommended client policy |
|---|---|
| HTTP call to user-service fails with network error / 5xx | Retry 3x with exponential backoff (1s, 2s, 4s), then show error |
| HTTP call fails with 4xx | Do not retry; surface the specific message to the user |
| WS connect fails | Retry with backoff, cap at e.g. 5 attempts, then tell user to try `/play` again later |
| Waiting for `MATCH_CREATED` | No hard timeout enforced by backend; offer the user a manual "Cancel" that just stops showing the waiting UI (cannot actually dequeue) |
| Waiting for `QUESTIONS_PUBLISHED` after sending `READY` | Client-side timeout ~60-90s; if exceeded, inform user opponent may have disconnected |
| Waiting for `ANSWER_ACCEPTED` | Short timeout (~5-10s more than the answer window); if no response, assume network issue and let user know |
| WS unexpectedly closes mid-game | Attempt one reconnect; if game state can't be recovered (no resync from server), inform the user the game may have ended and direct them to start a new one |
