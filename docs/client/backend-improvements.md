# Recommended Backend Improvements for Client Integration

These are recommendations only — **not implemented in this planning pass**. Each maps back to a gap in `gap-analysis.md`. Ordered roughly by client-development priority, not necessarily backend implementation ease.

---

## 1. Token Refresh Endpoint (addresses G-12)

**New endpoint**: `POST /user-service/public/api/v1/refresh`

```json
// Request
{ "refreshToken": "..." }
// Response 200
{ "accessToken": "...", "expireTime": 86400000 }
// Response 401
{ "message": "invalid or expired refresh token", "error": "..." }
```

**Prerequisite backend change**: JWT claims need a `type: "access" | "refresh"` field so the validate-token logic (or a new dedicated check) can reject an access token presented as a refresh token and vice versa — today both are structurally identical JWTs distinguished only by which minting endpoint was called.

**Also fix while touching this code**: the `exp` claim bug noted in `backend-api-analysis.md` §1 — `svc.config.AccessTokenExpireTime` is used raw as the JWT `exp` claim value instead of `time.Now().Add(d).Unix()`. This should be fixed as part of any auth-service token work, since a refresh flow amplifies the correctness importance of expiry semantics.

---

## 2. Leave-Matchmaking-Queue Command (addresses G-13)

**New WS command** on game-service: `LEAVE_WAITING_LIST`
```json
{ "command": "LEAVE_WAITING_LIST" }
```
Server response:
```json
{ "success": true, "event": "REMOVED_FROM_WAITING_LIST" }
```

**Implementation sketch**: reuse `match_app.repository.RemoveWaitingMember` (already exists, currently only called by the scheduler after a match). Game-service would need either (a) a synchronous internal call path to match-service, or (b) a new Kafka topic (`GAME_V1_LEAVE_MATCH_QUEUE_REQUESTED`) mirroring the existing join pattern for consistency with the event-driven architecture already in place. Option (b) is more consistent with the existing design.

---

## 3. WebSocket Reconnect State Resync (addresses G-14)

**Behavior change**, not a new endpoint: on `GET /api/v1/process-game` upgrade, if the reconnecting user's cached `GameStatus` is `PENDING` or `STARTED` (not `UNKNOWN`/`INITIALIZED`), the server should push a synthetic snapshot event instead of doing nothing:

```json
{
  "event": "STATE_SYNC",
  "metaData": {
    "gameId": "...",
    "status": "STARTED",
    "questions": [ /* full question set, as originally sent */ ],
    "leaderBoard": { /* current leaderboard */ },
    "alreadyAnswered": ["questionId1", "questionId2"]
  }
}
```

This lets a reconnecting bot rebuild its local state machine to the correct position rather than guessing. The `alreadyAnswered` list is important so the client doesn't re-render already-answered questions as pending.

**Complexity note**: this touches the same Redis-cached `GameQuestions` structure already used for `QUESTIONS_PUBLISHED`, so most of the data is already available — the main work is the new event type and the "resend on reconnect" branch in the existing `switch` on `GameStatus` in `ProcessGame`.

---

## 4. Ready Acknowledgment Event (addresses G-08)

Every `READY` command should produce a response, not just the one that triggers game start:
```json
{ "success": true, "event": "READY_ACKNOWLEDGED", "metaData": { "playersReady": 1, "playersExpected": 2 } }
```
Trivial change to `services/game_app/service/websocket_handler.go`'s `READY` case — send this before/instead of the current silent branch, then still send `QUESTIONS_PUBLISHED` separately once everyone's ready.

---

## 5. Fix ADD_TO_WAITING_LIST Fallthrough Bug (addresses G-10)

Add `return` statements after the two error-response writes in the `ADD_TO_WAITING_LIST` case (invalid category, broker publish failure) so a client never receives a contradictory `ERROR` immediately followed by a false `ADDED_TO_WAITING_LIST`. Purely a correctness fix, already effectively described in `docs/refactoring/02-refactor-candidates.md`'s spirit even though it isn't listed there verbatim — recommend adding it as a new refactor candidate (R-16) since it wasn't caught in the earlier bug-fixing pass.

---

## 6. Structured Error Codes (addresses G-07, G-11)

Standardize on one error shape everywhere, HTTP and WebSocket alike:
```json
{ "message": "human readable", "error": "human readable", "code": "MACHINE_READABLE_CODE" }
```
For WebSocket, add `code` to `ProcessGameMessageResponse`. Suggested codes based on existing message strings:

| Current message | Suggested code |
|---|---|
| "invalid category" | `INVALID_CATEGORY` |
| "internal server error" | `INTERNAL_ERROR` |
| "answered to question quickly" | `ANSWERED_TOO_EARLY` |
| "player already answered this question" | `DUPLICATE_ANSWER` |
| "invalid command" | `INVALID_COMMAND` |

For HTTP, route the bare-string error responses (`user-service` profile's missing-header case, `match-service`'s category/user-id errors) through the same `errApp` machinery everything else already uses, so every HTTP error is a `{message,error,code}` object.

---

## 7. Distinguish "User Not Found" from Server Errors on Login (addresses G-06)

In `services/user_app/repository/user.go`, map a "no rows returned" condition to a distinct sentinel the service layer can map to `errApp.ErrInvalidLOGIN` (403) rather than letting it fall into the generic `ErrInternal` (500) path. This is a one-function change with no API contract change (the HTTP status the client sees actually improves from 500→403, which is a client-visible but backward-compatible improvement — no client relying on "500 means retry" breaks, since 403 is still an error the bot should surface, not retry).

---

## 8. Matchmaking Abandonment Timeout (addresses G-15)

Add a deferred check (Asynq, mirroring the existing `game:completed` task pattern) scheduled at `MATCH_CREATED` time with a delay of e.g. 60-90 seconds. If not all players have sent `READY` by then, broadcast an event to whoever *did* ready up:
```json
{ "event": "OPPONENT_TIMEOUT", "message": "opponent did not respond in time" }
```
and clean up the stuck Redis ready-tracking state so the game doesn't remain permanently half-ready.

---

## 9. Expose Question Service Through the Gateway (addresses G-16)

Add the missing Traefik `IngressRoute` + Kubernetes deployment manifest for `question_app`, following the exact pattern already used for `match_app`/`game_app` in `infra/kubernetes/ingress/ingress-route.yaml`. Low effort, unblocks any future client feature needing direct question-bank access (browsing, admin management).

---

## 10. Performance/Event Improvements (lower priority, forward-looking)

- **Incremental leaderboard deltas**: `ANSWER_ACCEPTED` currently sends the full recomputed leaderboard on every answer (`repository.GetLeaderBoard` re-aggregates all answers each time — already flagged as a performance concern in `docs/project-assessment.md`). For a chat bot rendering compact messages, a delta (`{playerId, pointsGained, newTotal}`) would be both cheaper server-side and simpler to render client-side, though the full leaderboard isn't harmful, just slightly wasteful at scale.
- **WebSocket keepalive/ping contract**: no documented ping/pong behavior exists. Publishing an expected keepalive interval (or implementing one) would let clients (bot included) detect dead connections proactively instead of only on a failed write.
- **Correlation IDs on WS commands**: adding an optional client-supplied `requestId` echoed back in the corresponding response would let a client definitively match a command to its response instead of inferring from event type and payload contents — useful once multiple commands can be in flight (e.g. future multi-question submission UX).

---

## Summary — What the Bot Team Should Ask the Backend Team For, In Order

1. Token refresh endpoint (#1) — highest client-experience impact.
2. WS reconnect state resync (#3) — second highest impact, but larger effort; can be deferred to a fast-follow after bot MVP ships with documented limitations.
3. Ready acknowledgment (#4) and ADD_TO_WAITING_LIST fallthrough fix (#5) — both trivial, high value-per-effort.
4. Structured error codes (#6) — do this once, benefits every future client, not just the bot.
5. Everything else (#2, #7, #8, #9, #10) — genuinely nice-to-have, sequence after the bot MVP is live and real usage patterns are observed.
