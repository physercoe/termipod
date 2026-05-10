# Steward overlay — history backfill + snippet chips + user-input rendering

> **Type:** plan
> **Status:** Open (B1–B6 settled 2026-05-10; W1 in flight)
> **Audience:** contributors
> **Last verified vs code:** v1.0.473

**TL;DR.** Three QA gaps in the steward overlay (v1.0.464–471
prototype line) bundled into one wedge: cold-start panels are empty
until new SSE events arrive, the user's own prior prompts never
render even when other content does, and there's no quick-action
affordance for common requests. This plan closes all three together
because they share the same controller surface and SSE event-shape
work, and because the principal asked for them as one bundle.

## Goal

After this wedge:

1. Opening the puck panel on a freshly installed APK shows the last
   ~30 turns of the team-general steward's conversation, not an
   empty list.
2. Both *steward output* and *user input* render with their proper
   role (steward bubble / user bubble) — so the panel transcript
   matches what the user sees in the full Sessions chat.
3. Above the input box, 3-5 quick-action chips let the user fire
   common steward prompts in one tap (e.g. "Show insights", "Open
   activity", "What's blocked?") — pulled from the same snippets
   store the rest of the app uses, filtered by a `steward` tag.

## Non-goals

- **Per-project / per-host stewards.** This wedge stays scoped to
  the team-general steward (the singleton mounted via
  `MaterialApp.builder`). Multi-conversation routing belongs to the
  Pattern-B follow-up tracked in
  [`../discussions/agent-driven-mobile-ui.md` §13](../discussions/agent-driven-mobile-ui.md).
- **Snippet authoring UI.** New snippets are still authored
  through the existing snippets editor; the wedge only *consumes*
  them.
- **Full agent_feed parity.** The overlay chat stays compact —
  no thought cards, no expandable tool calls, no inline artifact
  rendering. That's `agent_feed.dart`'s job.
- **Hub-side change.** The hub already publishes both `text` and
  `input.text` kinds with the right `producer` field; the wedge is
  client-only.

## Why now

- v1.0.470 QA from the principal flagged all three issues
  back-to-back. Each in isolation is small; bundled they're a
  cohesive "the overlay panel is finally a usable chat" wedge.
- Pre-MVP demo arc needs the overlay to feel coherent across cold
  starts. A user who reinstalls and sees an empty chat (or only
  half the conversation) reads as "the steward forgot." Backfill
  fixes that perception in one move.
- Snippets shorten the demo path. "Show insights" via tap is faster
  + more reliable than typing it. Reduces voice-IME failure modes
  when filming.

## Decisions settled before kickoff (2026-05-10)

- **B1 — Snippet filter.** `Snippet` already has `category`
  ('general' / 'tmux' / 'cli-agent' / etc.); no generic tag
  system. Add `'steward'` as a recognized category and filter
  the chip strip on `category == 'steward'`. ~5 LOC of model
  acknowledgment, no shape change.
- **B2 — Backfill window.** `limit = 50` events. Covers ~8–15
  turns in practice; tune after demo.
- **B3 — Session filter.** Filter BOTH the backfill call AND
  the existing live `streamAgentEvents` to the resolved
  `sessionId` so the panel doesn't mix current and historical
  sessions. The existing call passes `sessionId: null`; tighten
  to `sessionId.isEmpty ? null : sessionId`.
- **B4 — Cache strategy.** Use `listAgentEventsCached` (mirror
  `agent_feed.dart`). Cache-first render, fresh refresh under
  the standard staleness pattern.
- **B5 — `mobile.intent` on replay.** Skip. Live navigation
  notes are transient logs, not durable transcript; re-rendering
  them as if the steward just navigated would be confusing. Live
  path keeps the existing snackbar + system message.
- **B6 — Loading state during backfill.** Existing
  `agentId == null → CircularProgressIndicator` covers it. Set
  `agentId` AFTER backfill populates `messages`, not before.

Settle-during-implementation (not blockers):
- Echo-dedup: ship Option A; swap to Option B in-wedge if QA
  flags latency.
- Snippet ordering: user-categorized first (insertion order),
  then 3 built-in defaults at the end.
- Default snippet bodies: "Show me the insights view",
  "What's blocked?", "Open my projects" (zh equivalents land
  when localisation pass happens).
- Tool calls on replay: skip (compact-chat axiom).
- Chip strip visibility: always visible, single-row horizontal
  scroll, regardless of input field state.

## Workband layout

### W1 — History backfill

**What.** On overlay bootstrap, after ensuring the steward agent
and resolving its session id, fetch the last 50 events via
`HubClient.listAgentEventsCached` and pre-populate
`state.messages`. Then start `streamAgentEvents` from that `seq`
cursor with the same session filter (using the existing
`sinceSeq` parameter, which `agent_feed.dart` already exercises).

**Files.**
- `lib/widgets/steward_overlay/steward_overlay_controller.dart` —
  add a `_backfill` step inside `_bootstrap` between session
  resolution and the SSE subscribe.
- (No new client methods. `listAgentEvents` already exists at
  `lib/services/hub/hub_client.dart:1519`.)

**Shape (post-B1–B6).**

```dart
final cached = await client.listAgentEventsCached(
  agentId,
  limit: 50,                                            // B2
  sessionId: sessionId.isEmpty ? null : sessionId,      // B3
);
final hydrated = _hydrateFromEvents(cached.value);      // demuxer skips mobile.intent (B5)
final lastSeq = hydrated.isEmpty ? null : _maxSeq(cached.value);
state = state.copyWith(
  agentId: agentId,                                     // B6: set AFTER hydration
  sessionId: sessionId,
  messages: hydrated,
  clearError: true,
);
_sub = client.streamAgentEvents(
  agentId,
  sinceSeq: lastSeq,
  sessionId: sessionId.isEmpty ? null : sessionId,      // B3: match backfill scope
).listen(_handleEvent, ...);
```

**Cost.** ~80 LOC. One new private method (`_hydrateFromEvents`)
that delegates to `_handleEvent` per item but skips the SSE-only
side effects (no snackbar replay).

### W2 — Render user input alongside steward output

**What.** `_handleEvent` currently treats `kind == 'text'` as
steward output and ignores `kind == 'input.text'`. The hub records
user input under `input.<kind>` with `producer = "user"` (see
`hub/internal/server/handlers_agent_input.go:378-407`). Render
those as `OverlayChatRole.user` bubbles. While we're there, also
render `input.approval` as a small system note ("you approved
…") so the transcript reads as a complete log instead of
half-redacted.

**Files.**
- `lib/widgets/steward_overlay/steward_overlay_controller.dart` —
  extend `_handleEvent` and `_extractText` to dispatch on
  `(kind, producer)` jointly.

**Shape.**

```dart
void _handleEvent(Map<String, dynamic> evt) {
  final kind = (evt['kind'] ?? '').toString();
  final producer = (evt['producer'] ?? '').toString();
  if (kind == 'mobile.intent') return _dispatchIntent(evt);

  if (kind == 'text') {
    final text = _extractText(evt);
    if (text != null && text.isNotEmpty) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.steward,
        text: text,
        ts: ...,
      ));
    }
    return;
  }

  if (kind == 'input.text' && producer == 'user') {
    final text = _extractInputText(evt);
    if (text != null && text.isNotEmpty) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.user,
        text: text,
        ts: ...,
      ));
    }
    return;
  }

  // input.approval, input.cancel, etc. → system notes (optional, W2.b)
}
```

Live note: `sendUserText` already locally appends a user-role
message before calling `postAgentInput`. After this change, the
SSE echo of our own `input.text` would arrive a moment later and
double-render. Two options:

- **Option A (preferred):** drop the local pre-echo in
  `sendUserText`; let the SSE round-trip drive the bubble. Adds
  ~100ms latency between tap and visible bubble — acceptable for
  a chat surface, and it means cold-start replay vs live-typing
  use the same code path.
- **Option B:** dedupe by `id` field — keep a small set of
  recently-sent input ids, skip them in `_handleEvent`. More
  complexity for marginal latency win.

Pick Option A. Document in the controller why the local echo is
gone.

**Cost.** ~50 LOC. One new helper (`_extractInputText`) plus the
producer-aware switch.

### W3 — Snippet chips above the input

**What.** A horizontally-scrolling row of `ActionChip`s above the
`_ChatInput` field. Each chip's `label` is the snippet's display
title; tap fires the snippet body through `_sendText`. The chip
list comes from the user's existing snippets store, filtered by a
`steward` tag (existing snippets feature, see
[`snippet_cmd_refactor`](../../memory) — the snippets data model
already supports tags).

**Source of presets.** Three layers:

1. **User snippets tagged `steward`** — top of the row, in
   user-defined order. Empty by default; users add via the
   snippets editor.
2. **Built-in defaults** — three hardcoded chips ship in the APK
   so the row is never empty on cold install: "Show insights",
   "What's blocked?", "Open my projects". Cosmetically distinct
   (slightly muted) so users know they can replace them.
3. **(Deferred — W4 candidate.)** Auto-suggested from sequence
   mining of recent prompts. Already discussed in
   `project_todo_usage_analytics.md`. Scope-cut from this wedge.

**Files.**
- `lib/widgets/steward_overlay/steward_overlay_chips.dart` (new)
  — the chip strip widget.
- `lib/widgets/steward_overlay/steward_overlay_chat.dart` — slot
  the strip above the `Divider` that separates messages from
  input.
- `lib/services/snippet_store.dart` (or wherever the snippet
  store lives) — add a `getByTag('steward')` accessor if missing.

**Cost.** ~120 LOC.

### W4 — Visual + accessibility polish (optional)

If the principal wants it after W1-W3 land:

- Distinct user/steward bubble colour ramp so the role contrast
  reads at a glance even with the panel translucent (current 50-
  100% opacity range).
- Semantics labels on chip strip ("Quick actions") + per-chip
  tooltip = full snippet body so long-press preview works.
- Empty-state copy in the chat surface that mentions the chip
  strip ("…or tap a quick action below").

Tag as W4 / scope-cut by default; only execute on principal
ack.

## Risks

- **W1 backfill window vs live-stream gap.** If `listAgentEvents`
  returns 30 events but new events arrive during the round-trip,
  the SSE subscribe with `sinceSeq` covers the gap. The hub's
  stream already replays from `sinceSeq` server-side
  (`streamAgentEvents` doc: "Subscribes before replaying backfill
  from sinceSeq so no live event is missed in the gap"). Low
  risk; mirror the agent_feed pattern.
- **W2 dedupe — Option A latency.** Dropping the local pre-echo
  costs one SSE round-trip (~100ms on LAN, ~300ms on cellular)
  before the user's own bubble appears. If QA flags this as
  laggy, fall back to Option B (dedupe by id).
- **W3 snippets data model assumption.** The plan assumes the
  snippets store supports a `tag` field. If it doesn't, the
  wedge needs a tiny store extension first — will discover during
  W3 grep. Worst case adds ~30 LOC.
- **No hub change.** All three workbands are mobile-only, so
  there's no cross-binary coordination risk.

## Out of scope (explicitly)

- Per-conversation routing inside the panel. The bundled wedge
  here is for the *team-general* steward only. Multi-conversation
  Pattern B is its own future wedge.
- Audio/voice transcript. Stays text-only per the existing
  v1.0.464 axiom (voice via system IME).
- Persisting messages to local storage. Backfill comes from the
  hub on every cold start; offline replay is a separate wedge if
  ever needed.

## Order

W1 → W2 in series (W2 needs W1's hydration helper).
W3 in parallel with W1/W2 — independent file.
W4 iff requested.

## Sequencing inside ADR-023

This wedge does NOT depend on ADR-023 ratifying. The fixes are
within the v1.0.464 prototype's already-locked scope (single
team-general steward, URI router, mobile.navigate). ADR-023
discusses *how far* the agent-driven model goes; this wedge is
about the existing model working correctly across cold starts.

## Sizing

| Workband | LOC (mobile) | Hub change | Risk |
|---|---|---|---|
| W1 backfill | ~80 | none | low |
| W2 user-input rendering | ~50 | none | low |
| W3 snippet chips | ~120 | none | low (model dep) |
| W4 polish | ~80 | none | low |
| **Total (W1-3)** | **~250** | **none** | **low** |

~1-2 days of work end-to-end. No new dependencies. APK delta < 5
KB.
