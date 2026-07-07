# Feature Map — Backend Capability → Telegram Bot Feature

For every backend capability (from `backend-api-analysis.md`), this maps the Telegram command/callback, the conversation flow, the API calls involved, expected responses, and any missing backend support.

## Overview Flow

```
/start
  ↓
Register or Login
  ↓
Receive JWT (accessToken + refreshToken)
  ↓
Store session (keyed by Telegram chat ID)
  ↓
Main Menu
  ↓
Play Game
  ↓
Choose Category
  ↓
Join Matchmaking (WS ADD_TO_WAITING_LIST)
  ↓
Wait for Match (WS MATCH_CREATED)
  ↓
Ready Up (WS READY)
  ↓
Answer Questions (WS QUESTIONS_PUBLISHED → ANSWER loop)
  ↓
Game Completion (WS COMPLETED)
  ↓
Back to Main Menu
```

---

## 1. Onboarding

### `/start`
- **Conversation flow**: If no session exists for this chat, greet the user and present inline buttons `[Register] [Login]`. If a session exists with a valid token, go straight to Main Menu.
- **API calls**: none yet (just checks local session store).
- **Expected response**: Welcome message + menu.
- **Missing backend support**: none.

### Callback: `register`
- **Conversation flow**: Prompt for email (text message), then prompt for password (text message — Telegram can't mask input in a normal chat; recommend warning the user, or requiring a Bot with a WebApp/inline form for masked entry as a future enhancement).
- **API calls**: `POST /user-service/public/api/v1/signup`
- **Expected responses**:
  - Success → "Registered as {displayName}! Now let's log you in." → auto-chain into login flow with the same credentials.
  - Duplicate email → "Already registered — try Login instead" with a `[Login]` button.
  - Invalid email/password → re-prompt with the specific validation message.
- **Missing backend support**: None functionally, but note the email validation is non-standard (§`backend-api-analysis.md` — local-part must be `>4` chars) — the bot should surface the *actual* rejection reason rather than a generic "invalid email," which requires the bot to pattern-match the returned message (no structured error code exists).

### Callback: `login`
- **Conversation flow**: Prompt email, then password.
- **API calls**: `POST /user-service/public/api/v1/login`
- **Expected responses**:
  - Success → store `id`, `accessToken`, `refreshToken` → Main Menu.
  - Wrong credentials (403) → "Invalid email or password."
  - 500 (ambiguous — could mean "user not found" OR real server error, see api-analysis) → "Couldn't log you in — check your details or try again shortly."
- **Missing backend support**: Cannot distinguish "no such user" from "server error" (both surface as 500) — bot messaging must stay generic here until the backend fixes this (see `gap-analysis.md`).

---

## 2. Main Menu & Profile

### `/menu` (or persistent keyboard after login)
- **Conversation flow**: Show buttons `[Play Game] [Profile] [Help] [Logout]`.
- **API calls**: none.

### `/profile`
- **Conversation flow**: Immediately calls the API and renders the result; no multi-step conversation needed.
- **API calls**: `GET /user-service/api/v1/profile` (Bearer token)
- **Expected response**: Render `{username, displayName, role, createdAt}` as a formatted message.
- **Error handling**: 404 "user not found" → force logout (stale/deleted account); 500 → "Couldn't load your profile, try again."
- **Missing backend support**: No way to *edit* profile (change display name, password) — feature does not exist server-side at all. If desired later, this is a "missing entirely" gap.

### Callback: `logout`
- **Conversation flow**: Clear local session tokens; confirm "You've been logged out."
- **API calls**: none — there is no server-side logout/revocation endpoint (JWT is stateless). This is purely a client-side forget-the-token action.
- **Missing backend support**: No revocation means a "logged out" token is still technically valid until it expires if somehow replayed — acceptable for a game bot's threat model, but worth documenting as a known limitation.

---

## 3. Matchmaking

### `/play` (or `Play Game` button)
- **Conversation flow**:
  1. Check session has a valid token; if not, redirect to login.
  2. If no open WS connection for this session, open one to game-service.
  3. On connect, if the server pushes the initial `{categories, numberOfPlayers}` payload (only happens on a truly fresh connection), present inline buttons for each category (`[SPORT] [MUSIC] [TECH]`). If it doesn't (reconnect case), fall back to a bot-side hardcoded category list (see gap below) since the bot cannot re-request categories (`GET_CATEGORIES` is a no-op).
- **API calls**: WS connect to `game-service`.
- **Missing backend support**: `GET_CATEGORIES` command exists in the protocol but the server never responds to it (confirmed no-op). The bot must hardcode `SPORT/MUSIC/TECH` as a fallback rather than relying on this command, and treat the initial-connect payload as a "nice to have, not guaranteed" source of truth.

### Callback: `category:SPORT` (or MUSIC/TECH)
- **Conversation flow**: Send WS command, show "Looking for an opponent..." with a `[Cancel]` button (UI-only cancel — see below).
- **API calls**: WS send `{command: "ADD_TO_WAITING_LIST", category: "SPORT"}`
- **Expected responses**: `ADDED_TO_WAITING_LIST` (proceed to waiting state) — possibly preceded by a spurious `ERROR` if the bot ever sends a malformed category (shouldn't happen since the bot controls the button values).
- **Missing backend support**: **No "leave queue" command exists.** The `[Cancel]` button can only stop the bot from acting on a future `MATCH_CREATED` for this chat's UI purposes — the user remains enqueued server-side until matched or the 20-minute window lapses. This should be disclosed to the user ("Cancel just stops waiting here; matching may still happen in the background") or the feature deprioritized until the backend adds real dequeue support.

### (Automatic) `MATCH_CREATED` event
- **Conversation flow**: Bot receives this asynchronously (not from a user action) — updates the chat with "Opponent found!" and a `[Ready]` button.
- **API calls**: none (inbound WS event).

---

## 4. Gameplay

### Callback: `ready`
- **Conversation flow**: Send `READY`, show "Waiting for opponent to be ready..." Disable the button after one tap (idempotency safeguard — resending `READY` is silently ignored server-side but wastes a round trip and confuses UI state).
- **API calls**: WS send `{command: "READY", gameId}`
- **Expected responses**: Either **nothing** (opponent not ready yet — client-side timeout applies, see business-flows §10) or `QUESTIONS_PUBLISHED` (game starts).
- **Missing backend support**: No ack for "your READY was received" — bot UI must optimistically show "waiting" immediately after sending, since there's no confirmation event either way.

### (Automatic) `QUESTIONS_PUBLISHED` event
- **Conversation flow**: Bot receives the full question array with per-question deadlines up front. It must pace delivery: show question 1 with an inline keyboard of choices and a countdown; only reveal question 2 once question 1's window logically completes (client-paced, not server-signaled).
- **API calls**: none (inbound).

### Callback: `answer:<questionId>:<choice>`
- **Conversation flow**: Send the answer, disable further taps for that question, show a brief "Submitted!" then update with the result once `ANSWER_ACCEPTED` arrives.
- **API calls**: WS send `{command: "ANSWER", answer: {gameId, questionId, choice}}`
- **Expected responses**: `ANSWER_ACCEPTED` with the full leaderboard (render as "You: X pts | Opponent: Y pts").
- **Error handling**:
  - `"answered to question quickly"` → indicates the bot's own pacing logic let the user answer before the window opened — treat as a bot bug to fix, not a user-facing error to show verbatim; if it happens, a generic "please wait a moment" is friendlier.
  - `"already answered this question"` → bot should have disabled the button; if seen, just ignore (no user-facing message needed).
- **Missing backend support**: No structured error codes (must string-match); no per-answer correctness flag independent of points (the `isCorrect` field on the backend is derived from `Point > 0`, so a "correct but too late" answer reports as incorrect — bot should present it as "no points earned" rather than "wrong answer" to avoid misleading the user).

### (Automatic) `COMPLETED` event
- **Conversation flow**: Show final leaderboard, congratulate the winner, offer `[Play Again]` which restarts the `/play` flow (a **new** WS connection, since the server closes the old one).
- **API calls**: none (inbound); server actively closes the WS connection right after.
- **Missing backend support**: No persisted game history to show "past games" later — if that feature is wanted, it's missing entirely server-side.

---

## 5. Help / Misc

### `/help`
- **Conversation flow**: Static text explaining commands and how the game works (categories, scoring, anti-cheat window). No API calls.

---

## 6. Feature Summary Table

| Backend feature | Telegram surface | API calls | Fully supported? |
|---|---|---|---|
| Register | `/start` → Register button | signup | Yes |
| Login | `/start` → Login button | login | Yes (with ambiguous-error caveat) |
| Token refresh | — | — | **No backend endpoint** — bot forces re-login on expiry |
| Logout | Logout button | none (local only) | Yes (client-only) |
| Profile view | `/profile` | profile | Yes |
| Profile edit | — | — | **Missing entirely** |
| Join matchmaking | Category buttons | WS ADD_TO_WAITING_LIST | Yes |
| Leave matchmaking | Cancel button | — | **UI-only; no real dequeue** |
| Get categories | (fallback hardcoded) | WS GET_CATEGORIES | **No-op on backend — do not rely on it** |
| Ready up | Ready button | WS READY | Yes (no ack though) |
| Answer question | Choice buttons | WS ANSWER | Yes |
| Leaderboard (live) | Shown after each answer | (part of ANSWER_ACCEPTED) | Yes |
| Game completion | Automatic message | (part of COMPLETED) | Yes |
| Game history | — | — | **Missing entirely** |
| Reconnect mid-game | Best-effort | WS reconnect | **Partial — no server resync, bot must fake it from local state** |
