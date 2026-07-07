# Client Project Assessment — Telegram Bot for BrainBlitz

## Executive Summary

BrainBlitz's backend is **substantially ready** to support a Telegram bot client for its core value proposition: register, log in, join a quiz match, and play. The entire real-time gameplay loop (matchmaking → ready → questions → answers → leaderboard → completion) is implemented and functioning end-to-end. However, the backend was clearly built with an implicit "single persistent client" assumption (a always-connected WebSocket, no reconnection story) that is in tension with how a chat-bot backend actually operates — many users, connections that come and go, and a bot process that itself needs to survive restarts without losing every in-flight game. None of these gaps are fatal; they are all addressable with either client-side workarounds (documented in `client-architecture.md` and `client-business-flows.md`) or modest, well-scoped backend changes (`backend-improvements.md`).

## Backend Readiness

| Capability | Readiness |
|---|---|
| Registration & login | Ready |
| Session/token handling | Partially ready — works, but no refresh flow forces re-login every 24h |
| Profile | Ready |
| Matchmaking join | Ready |
| Matchmaking leave/cancel | Not ready — no backend support |
| Gameplay (ready/questions/answer/score) | Ready, with documented protocol quirks the client must handle defensively |
| Reconnection / resilience | Not ready — architecturally the weakest area for this specific client type |
| Question administration | Not ready (stub); not needed for MVP |
| Game history | Not ready; not needed for MVP |

## Client Feasibility

**A Telegram bot MVP is feasible today** covering: registration, login, category-based matchmaking, and the full quiz game loop with live scoring. The client architecture proposed (`client-architecture.md`) isolates all BrainBlitz-specific protocol knowledge into a `core`/`apiclient` layer separate from Telegram-specific code, so the same foundation can support Web, Discord, or CLI clients later with minimal duplicated effort.

The main feasibility risk is not "can it be built" but "how good is the experience when things go wrong" — disconnects, backgrounded chats, and an unresponsive opponent are all common in a chat-bot context and are exactly the scenarios the backend currently handles the least gracefully.

## Required Backend Work

Not required to start client development, but strongly recommended in this order once bot work is underway (full detail in `backend-improvements.md`):

1. Token refresh endpoint — avoids forcing daily re-login.
2. WebSocket reconnect state resync — the single highest-impact fix for chat-bot usability.
3. Ready-acknowledgment event + `ADD_TO_WAITING_LIST` fallthrough bug fix — both trivial, remove real client-side guesswork.
4. Structured error codes across HTTP and WebSocket — benefits this client and any future one.
5. Leave-queue command, abandonment timeout, question-service gateway exposure — sequence after MVP, based on real usage.

None of these block starting Phase 1-5 of the roadmap; they primarily affect Phase 6 (Resilience) quality.

## Recommended Implementation Order

1. **Build the bot MVP against the backend as-is** (roadmap Phases 1-5, ~3-4 weeks), documenting and working around the known protocol quirks rather than waiting on backend fixes.
2. **In parallel or immediately after**, request the trivial backend fixes (#3 above — ready ack, fallthrough bug) since they're low-effort/high-value and can land before Phase 6 without blocking earlier phases.
3. **Before or during Phase 6 (Resilience)**, prioritize the token refresh endpoint and reconnect resync — these materially change how much resilience logic the bot needs to fake client-side.
4. **Defer** question-service exposure, admin features, and game history indefinitely until there's a concrete feature request needing them.

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| No server-side reconnect support means real users will occasionally "lose" a game from the bot's perspective | Medium-High | Ship with clear in-chat messaging ("your game may have ended while you were away") rather than pretending recovery is seamless; prioritize G-14 backend fix post-MVP |
| Plain-text password entry in Telegram chat | Medium (UX/perception, low actual technical risk for a portfolio project) | Disclose clearly at registration; consider a web-based login deep link if this ever handles sensitive real-world data |
| Backend has zero rate limiting or WS message-size limits (per `docs/context/07-known-issues.md`) | Low for a bot under the bot's own control, but the bot itself should not amplify this by retrying aggressively | Bot-side backoff and sane retry caps, as specified in `client-architecture.md` §9 |
| One bot process holding many concurrent WebSocket connections is a new operational pattern not previously exercised against this backend | Medium | Load-test the WS layer with a handful of simulated concurrent bot sessions before wide rollout; the backend's own known issue of an in-memory, single-instance connection map (`docs/context/08-ai-context.md`) means the *backend* game-service also can't be horizontally scaled behind this bot — capacity planning should assume one game-service instance is the ceiling until that's addressed independently of the bot project |
| Backend's ambiguous error responses (500 for "user not found", bare-string errors) could mislead bot users | Low-Medium | Bot error-mapping layer absorbs this (see `client-architecture.md` §8); revisit once G-06/G-07 land |

## Estimated Project Size

- **Bot development**: ~5-6 weeks solo (see `client-roadmap.md` for phase breakdown), Phases 4-5 (matchmaking + gameplay) being the critical path.
- **Recommended backend improvements**: independently estimated at roughly 2-3 weeks of backend developer time if all of `backend-improvements.md` items are pursued; the top 3 priority items (#1, #3, #4) alone are under a week.
- These two workstreams can run in parallel with different developers, or sequentially by the same person — the roadmap does not require backend changes to complete Phases 1-5.

## Suggested Milestones

| Milestone | Content | Target |
|---|---|---|
| M1 — Bot skeleton live | Phase 1 complete, `/start` responds | End of week 1 |
| M2 — Users can log in | Phases 2-3 complete | End of week 2 |
| M3 — Users can find a match | Phase 4 complete | End of week 3 |
| M4 — Full game playable end-to-end | Phase 5 complete | End of week 4-5 |
| M5 — Production-hardened | Phases 6-7 complete | End of week 5-6 |
| M6 — Backend resilience improvements live | Token refresh + reconnect resync shipped | Parallel/fast-follow, target within 2-4 weeks of M4 |

## Next Steps

This planning pass is complete. Per the constraints of this task, **no implementation has been started**. Recommended next action: review this document set (`docs/client/*.md`) with stakeholders, confirm the language/framework assumption for the bot (`client-architecture.md` §Assumptions), and decide whether to greenlight Phase 1 of `client-roadmap.md`.
