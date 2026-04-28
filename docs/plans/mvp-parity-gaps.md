# MVP parity gaps — push notifications, session search

> **Type:** plan
> **Status:** Proposed (2026-04-28; pending owner approval)
> **Audience:** contributors
> **Last verified vs code:** v1.0.322

**TL;DR.** Phase 1+2 of `agent-state-and-identity.md` closed the
state/identity/scope gaps with single-engine remote-control prior
art. Two parity gaps remain: **push notifications** (the "go to
dinner, get pinged when the agent is done" pitch in
`positioning.md` §5 doesn't actually wake the device today) and
**search across past sessions**. By the no-short-board commitment
(`feedback_no_short_board.md`), both are MVP-scope, not polish.
This plan sizes them as a single Phase 1.5 wedge with two
deliverables that can land independently.

---

## 1. Why this plan

A v1.0.322 parity audit against claudecode-remote / Codex remote /
Happy showed termipod at parity on 14 of the 16 main axes those
products cover. Two gaps:

1. **Push notifications.** The app uses `flutter_foreground_task`
   to keep its process alive on Android, but ships no
   `flutter_local_notifications` or FCM/APNs integration. There is
   no path for "agent asked for approval at 3 AM" to surface
   anywhere except the next time the user opens the app. Happy and
   Claude Code Channels both ping the phone; users coming from
   those products will notice immediately.
2. **Session search.** Sessions list groups by scope (Phase 2)
   but offers no way to find a past conversation by content —
   "where did I discuss the AdamW config last week?" requires
   scrolling. SQLite FTS5 is already a dependency and `agent_events`
   carries the transcript text.

Both are load-bearing for the MVP pitch:
- The "directing while away" story (positioning.md §5) requires
  push.
- The "20+ archived sessions" reality (post-Phase-2 fork-friendly
  workflow) requires search.

---

## 2. Decisions

**D1. Self-hostable push first; cloud-FCM optional.** Termipod's
self-hosted ethos rules out making FCM/APNs mandatory. The
canonical path is **ntfy.sh-style HTTP push** — the hub posts to a
user-configured ntfy URL on relevant events; the phone subscribes
via the ntfy client (or a built-in subscriber inside termipod).
FCM/APNs can ship later as a convenience for users who don't want
to run ntfy.

**D2. Killed-state push needs an OS-level channel.** A foreground
task + local notifications cover foreground + background but
**not** killed/swiped-away state. Without OS-level push (ntfy,
FCM, APNs), the agent-finishes-while-app-is-killed case can't be
addressed. This plan ships local notifications for foreground/
background as Phase 1.5a, ntfy integration as Phase 1.5b.

**D3. Search surfaces transcript text + tool calls.** SQLite FTS5
virtual table over `agent_events.payload_json` (filtered to text
and tool-call kinds). Result rows resolve to (session, seq) so
tapping a result deep-links into the session at that event.
Per-team scoping comes for free since `agent_events.team_id`
filters every query.

**D4. Search is a separate screen, not an inline filter.** Adding
search to the sessions-list filter chip would conflate
**finding-by-name** (which the existing list already does) with
**finding-by-content**. A dedicated search screen with a single
input + result list is cleaner and matches the muscle memory of
ChatGPT / Claude.ai / Cursor.

---

## 3. Phases

### Phase 1.5a — Local notifications + foreground SSE wake

Goal: when the app is foregrounded or recently-backgrounded, agent
events that *should* notify (turn end, attention item raised,
session paused) surface as system notifications.

1. Add `flutter_local_notifications` dependency.
2. In the existing foreground task, watch the hub SSE stream for:
   - `turn.result` with `terminal_reason` (agent finished a turn)
   - `attention_item.raised` (decision/approval request)
   - `session.paused` (host went offline)
3. Emit a system notification with title (event kind), body
   (summary or first 80 chars of relevant text), and tap action
   that opens the relevant screen (chat / Me / sessions).
4. Setting toggle in `settings_screen.dart` to disable per kind.
5. Verification: test on Android device — receive a notification
   when a steward finishes a turn while the app is backgrounded.

**Limit (documented in-app):** does not fire when the app is
killed/swiped. Phase 1.5b removes this limit.

### Phase 1.5b — ntfy integration (killed-state push)

Goal: agent-finishes-while-app-is-killed event reaches the user.

1. Hub config: optional `notify_url` per team (the user's ntfy
   topic URL).
2. Hub posts to that URL on the same event set as 1.5a, plus an
   "agent action requires user" critical-priority event.
3. App: deep-link handler for ntfy notifications that opens the
   referenced session/attention.
4. Settings UI: paste-in field for ntfy URL, a Test button, and
   a recommended-self-host blurb (link to ntfy.sh self-host
   docs).
5. Verification: kill the app, post a test event from the hub,
   receive the ntfy notification on phone, tap → app opens at
   the right surface.

### Phase 1.5c — Session search screen

Goal: find a past conversation by content in <5 seconds.

1. Hub schema: add an FTS5 virtual table mirroring text content
   from `agent_events` (text payloads, tool-call names + args).
   Index is built on insert; back-fill migration walks existing
   rows.
2. Hub API: `GET /v1/teams/:team/search?q=<query>&limit=…`
   returns `[{session_id, scope_kind, scope_id, event_seq,
   snippet, ts}]`.
3. App: new `lib/screens/sessions/search_screen.dart` accessible
   from the sessions screen AppBar (search icon). Single text
   field, debounced search, list of results. Each result shows
   the session title, scope chip, snippet with the match
   highlighted, timestamp.
4. Tap a result → `SessionChatScreen` opened at the matching
   `event_seq` (scrolls to context).
5. Verification: open 5+ archived sessions, search for a unique
   string from one of them, tap result → land at the right
   place.

---

## 4. Sequencing

Phases 1.5a and 1.5c are independent and can land in either order.
Phase 1.5b depends on 1.5a (uses the same notification-handling
plumbing).

Suggested order: **1.5a → 1.5c → 1.5b**.

- 1.5a is self-contained and gives the user immediate value
  (background notifications today).
- 1.5c is mostly server-side (FTS5 + endpoint) plus one screen;
  doesn't touch the notification path.
- 1.5b extends 1.5a. Optional for users who don't want ntfy.

---

## 5. Verification (cross-phase)

- [ ] Walkthrough: agent finishes a turn while app is in
  background → notification appears (1.5a).
- [ ] Walkthrough: app killed, hub fires test event, ntfy
  notification arrives, tap opens correct surface (1.5b).
- [ ] Walkthrough: search for a string from an archived
  conversation, find the result, tap → land at the right event
  (1.5c).
- [ ] Settings allow disabling each notification kind
  independently (1.5a).
- [ ] No regression in existing foreground task behavior.

---

## 6. Open questions

- Should the search screen also surface artifact bodies
  (briefings, plans), or just session transcripts? **Lean: just
  transcripts for MVP**, add artifacts as a Phase 2 of the search
  feature if users ask.
- iOS path for 1.5b: does ntfy's iOS app cover us, or do we need
  to ship an APNs adapter? **Lean: ship Android first, document
  iOS limitation, revisit when an iOS user appears.**
- Should we re-export user-curated push channels (Slack, Telegram)
  via the same notify_url field? **Lean: not in MVP — adds
  ambiguity about which provider the URL is for.** Punt to a
  per-channel section if/when domain packs land.

---

## 7. Related

- `./agent-state-and-identity.md` — Phase 1+2; this is Phase 1.5
- `../discussions/positioning.md` §5 — the "go to dinner" pitch
  that requires push
- `feedback_no_short_board.md` (memory) — MVP parity standard
- `../discussions/post-mvp-domain-packs.md` — possible future home
  for per-channel push providers
