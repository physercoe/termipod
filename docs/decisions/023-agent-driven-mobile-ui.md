# 023. Agent-driven mobile UI — overlay + URI intents + compact-mode framing

> **Type:** decision
> **Status:** Accepted (2026-05-10; prototype shipped v1.0.464–v1.0.478; W1–W3 of overlay-history-and-snippets done; agent-events-shared-provider P1 infrastructure shipped v1.0.478, overlay+AgentFeed consumer migrations bundled into P2 post-MVP)
> **Audience:** contributors
> **Last verified vs code:** v1.0.476

**TL;DR.** The principal's directive is "the steward operates the
mobile app FOR the user" — not "the user navigates to features
the steward suggests." This ADR locks the surface, the wire
format, the dismissal/coexistence semantics, the
agent-conjured-surface tier strategy, and the compactness
contract that distinguishes the overlay from the Sessions screen.
Eleven decisions (D1–D11), each grounded in a shipped feature or
a 2026 SOTA reference. Eight open questions from the discussion
doc are now closed; one (Q15 — capacity model) added during
review and resolved here.

## Context

### The directive

Through 2026-04 the user repeatedly framed the mobile experience
in director/principal terms: they direct agents, they don't
operate the system. ADR-005 first locked this for the steward
("CEO-class operator"). The agent-driven mobile UI work is the
direct execution: when the user says "show me the insights
view" — by chat, voice, or snippet tap — the *agent* takes them
there. The user reviews, approves, redirects.

This is consistent with the 2024–2026 SOTA shift documented in
the discussion doc §10.2: "operate-for-me" copilots are
displacing "suggest-to-me" assistants across enterprise software.
The Smashing Magazine 2026 IAA framework (Intent → Action →
Audit) is the design vocabulary the field has converged on for
this class of agentic UX.

### What was missing before this work

Mobile already had:

- A general-steward agent reachable through the Sessions screen
  (full chat surface).
- A URI grammar (`termipod://...`, legacy `muxpod://`) and a
  centralized router (`lib/services/deep_link/uri_router.dart`).
- A snippet store and a custom keyboard with action-bar slots.

Mobile lacked:

- A persistent surface for the steward to surface, ask, or act
  *without forcing the user out of whatever screen they were on*.
- A wire format the steward could emit to "drive" the app —
  navigate, surface artifacts, or (eventually) create / edit /
  write.
- A coherent dismissal / coexistence story (modal sheets that
  block the page were the only existing pattern).
- A compact view for the directive history that wouldn't
  duplicate the Sessions chat.

### The discussion doc

`docs/discussions/agent-driven-mobile-ui.md` (Open) carries the
research + axioms + open-questions register. This ADR
consolidates and locks the load-bearing decisions. The
discussion doc remains as the design rationale; the ADR is the
contract.

## Decision

### D1. Persistent floating overlay (puck + panel) is the agent's home surface.

Mounted **once** at the app root via `MaterialApp.builder` in
`lib/main.dart`. Three states:

- **Hidden** — controller stopped (Settings → Experimental → "Voice
  input"-style toggle off, or no hub configured). No SSE
  subscription, no UI footprint.
- **Puck** — collapsed circular avatar, draggable around the
  viewport, position persisted via `settings_provider`.
- **Panel** — expanded chat surface. Drag the header to move,
  bottom-right corner to resize; both persist. Opacity
  configurable (50–100% via Settings).

Three-state dismissal matches the SOTA pattern (§10.6, lock #2).

**Why a single root mount:** the overlay must outlive route
transitions. The user issues a directive on the Projects tab;
the steward navigates to Activity; the panel must still be there.
A per-route mount couldn't survive route pops.

**Shipped:** v1.0.464 (initial spike) through v1.0.476 (compact
rework). Layout persistence, opacity slider, and the
experimental toggle in v1.0.466. Non-modal coexistence locked in
v1.0.469 (D4 below).

### D2. URI is the public API for `mobile.intent` events.

The wire format the steward emits to drive the mobile app is a
URI string, not a typed enum or RPC call. Schema:
`termipod://<host>/<segments...>?<params>` (legacy `muxpod://`
also accepted). The hub publishes a `mobile.intent` event with
`{kind: 'mobile.intent', uri: '<uri-string>', agent_id, team_id,
ts}` on the agent bus.

The mobile-side router (`uri_router.dart` `navigateToUri`) is
typed internally — but the agent-facing contract is the URI
string. Reasons:

- LLM training data fits URI patterns natively (better
  generation accuracy than typed structures).
- Survives serialization through agent_events / SSE / logs
  without schema versioning concerns.
- Forward-compatible: new destinations are new URI hosts/paths,
  not protocol additions.
- Audit-trail-friendly: the URI is the durable record of what
  the agent *meant* to do, even if the destination later
  changed.

Captured in `feedback_uri_as_public_api` memory.

### D3. Voice via system IME; chat surface stays text-only.

When voice input lands (planned, see
`docs/plans/voice-input-overlay-v1.md`), it routes through the
system IME's microphone gesture, NOT a custom in-overlay
record button. v1 of voice is push-to-talk via long-press on a
dedicated mic button that captures audio for SenseVoice-Small —
but the keyboard / IME flow stays the canonical text-entry
path. The chat does not own audio capture; SenseVoice writes a
transcript into the input field, the user reviews + sends.

**Why:** voice features compete on system-level integration
(iOS dictation, Gboard voice button, accessibility tools). An
in-app voice surface that doesn't compose with these is a
worse experience than just leaning on the IME.

### D4. Panel is non-modal — coexists with underlying page.

The expanded panel is a `Positioned` sibling of the underlying
page, NOT a modal sheet. No `Positioned.fill` barrier with
`onTap: collapse`, no translucent scrim. Industry SOTA confirms:
Slack floating chat, Discord PiP, iOS PiP, Apple Intelligence's
floating prompt — none use a barrier.

**Why this is a load-bearing decision (not just polish):** the
"steward sits above the app and you read the page in parallel"
framing is how the directive UX works. A barrier breaks every
coexistence axiom — the user can't tap a bottom-nav tab while
reading a steward reply, can't scroll the underlying list while
the panel covers the bottom half. v1.0.464–468 had a
barrier+scrim "for affordance" and broke this; v1.0.469 removed
both.

Captured in `feedback_floating_chat_non_modal` memory.

### D5. Single shell, multi-conversation list inside — NOT N pucks.

(Resolved Q15 from the discussion doc §13.)

When floating subjects multiply beyond the team-general steward
(per-project stewards, per-host stewards, attention threads,
multi-steward conversations), they land as **rows inside the
existing single panel's conversation list**, not as N
independent pucks on screen.

Three patterns were considered:
- **A. N-pucks** (Messenger Chat Heads style): rejected.
  Multiplies SSE bandwidth cost, multiplies drag/resize state,
  multiplies attention claim. No SOTA app does this for >2
  conversations.
- **B. Single shell, list inside** (Slack mobile DMs): chosen.
  One puck → one panel → list of subjects → tap-to-switch.
- **C. Edge-dock + single panel** (Discord PiP): deferred. A
  vertical icon-rail glued to one screen edge, tap to swap which
  conversation is active. Cosmetic upgrade if the list outgrows
  scroll readability.

The router already segments by entity (`termipod://stewards/<id>`,
`termipod://projects/<id>/chat`, `termipod://attentions/<id>`),
so each list row is just a URI; the panel body is "render
transcript for whatever URI the user picked."

**Future-proofing:** the steward can `mobile.navigate(uri)` to
set the panel's current conversation, the same way it navigates
the page underneath. No new MCP tool needed.

### D6. IAA framework — Intent → Action → Audit, all three pillars, none implicit.

(Resolved §10.6 lock #1.)

The Smashing Magazine 2026 IAA skeleton is the canonical shape.
Termipod implements all three:

- **Intent Preview.** Before the steward acts, surface the
  intended action so the user can redirect. Today: streaming
  text replies in the panel before the URI dispatches.
- **Autonomous Action.** The steward acts on the system the user
  is operating. Today: `mobile.navigate(uri)` MCP tool fires the
  router and the page changes.
- **Audit + Undo.** Past actions appear as past-tense pills in
  the panel ("Steward → Insights · 14:32"). Tapping a pill
  re-fires the URI (the closest thing to "Undo" for navigation;
  destructive future actions need explicit undo affordances).

**Why all three:** Audit-only is the trap that Rabbit R1 / Humane
Pin fell into — agent acts opaquely, user sees only the after.
Without Intent Preview, users don't trust the agent enough to
let it act. Without Audit, users can't recover from mistakes.

### D7. Three-state dismissal (Hidden / Puck / Panel) — locked.

(Resolved §10.6 lock #2.)

The overlay has exactly three visible states. No "minimized"
fourth state, no system tray-style icon-only mode, no slide-in
swipe-from-edge gesture. Reasons:

- Mobile users have ~5 mental slots for app surface states; a
  fourth makes the model harder to learn.
- Hidden vs Puck is the user's "I don't want the steward
  watching me right now" lever — Settings toggle.
- Puck vs Panel is the user's "I want a quick chat / I'm done"
  lever — tap.
- Drag-cancel on the puck doesn't toggle (debounce via
  `_draggedThisGesture`).

### D8. Tier 1 first for agent-conjured surfaces.

(Resolved Q14 from §12.)

When the steward needs to render *something* beyond URI
navigation (a chart, a structured artifact, an inline
visualisation), the mobile client supports three tiers:

- **Tier 1 — markdown code-fence renderers** (` ```svg `, ` ```html `).
  Agent emits a fenced block; mobile renders it as a widget
  via the existing `flutter_markdown` builder pipeline. ~750
  LOC mobile, +250 KB APK, no protocol change. Plan:
  `docs/plans/agent-artifact-rendering-tier-1.md`.
- **Tier 2 — sandboxed WebView artifacts.** Agent emits a
  `ui_html` artifact via the existing artifacts primitive
  (versioning + sharing + retention reused, not a parallel
  store). Mobile renders in `webview_flutter`. ~+250 KB to
  ~2 MB APK depending on JS engine choice.
- **Tier 3 — Server-Driven UI (SDUI).** JSON describes a
  widget tree; mobile materialises native widgets. Heaviest
  lift, only justified if Tier 2's web-in-app feel breaks the
  demo arc.

**Locked progression:** ship Tier 1 first. Tier 2 only if Tier
1 hits expressiveness limits. Tier 3 only if Tier 2's UX
breaks. Most teams never need Tier 3.

**Three sub-locks within Tier 1:**

- Renderer registry shape: language-string ↔ renderer mapping,
  lazy-loaded, fail-closed (unknown language renders as code).
- HTML allowlist: explicit tag whitelist; no `<script>`,
  `<iframe>`, `<object>`, `<embed>`, `<style>`, no
  `javascript:` URLs.
- Artifact storage contract: Tier 2's `ui_html` kind reuses
  `artifacts_wedge` primitive — versioning, sharing, retention
  reuse. NOT a parallel store.

### D9. Compact mode — overlay shows recent directive context, NOT a parallel transcript.

The Sessions screen owns the full chat transcript. The overlay
is the **recent directive context** — what the user said
recently, what the steward did/said back, with a "open full
session" pivot to the Sessions screen for everything else.

Concretely:

- Rolling cap: 20 messages.
- Steward replies > 240 chars truncate with "open full session
  for the rest" affordance.
- `mobile.intent` events render on cold-open replay (not just
  live) as past-tense pills — they ARE the most informative
  directive signal.
- Tool calls, thoughts, plans, diffs, completion frames stay
  hidden in the overlay (they belong in Sessions chat).
- Header has "Open full session" icon → pushes
  `SessionChatScreen(steward.sessionId, steward.agentId)`,
  collapses overlay.
- Header has pending-attention badge when steward has raised
  `status=='open'` attention items; tap → Me tab.

**Why this matters:** v1.0.474–475 implementation accidentally
reproduced the Sessions chat with smaller bubbles. The principal
flagged it during review: *"the overlay should be a concise /
compact / condensed version, not duplicating."* v1.0.476 reframes
it correctly.

### D10. Action-aware intent rendering — `verb` field for future create / edit / write.

(Future-proofing decision baked into the data model now.)

`OverlayIntentAction { verb, target, uri }` — a structured
representation of what the steward DID via `mobile.intent`. v1
ships verb='→' (navigate). Future intents (create, edit, write
artifacts) will carry an `action` field on the event payload,
which the message folder switches on to set the right verb.

The hub's `mobile.intent` event shape can add an `action: 'navigate'
| 'create' | 'edit' | 'write'` field without breaking older
clients (defaults to 'navigate' for backward compat).

**Why now:** the principal explicitly asked during compact-mode
review that the rendering be ready for future actions, not just
navigation. Building the verb field now costs nothing; building
it later means migrating the model.

### D11. Eager-load + experimental gating.

The overlay is gated by Settings → Experimental → "Voice input"
**Steward overlay** toggle. When off, no controller is started,
no SSE subscription, no UI footprint. Default ON for v1 because
the prototype is shipped enabled — but the toggle exists so
users who don't want it can opt out.

**Eager controller start:** `_StewardOverlayHostState` watches
the toggle. When ON + hub configured, calls `ensureStarted()`
post-frame. The first time the user interacts with the puck,
backfill is already in flight (or done) — first interaction
feels instant.

The agent-events shared provider's infrastructure layer landed
in v1.0.478 (`lib/providers/agent_events_provider.dart`) but the
overlay does NOT yet consume it — the overlay-migration attempt
in v1.0.477 hit a Riverpod 3.x lifecycle limitation (`Ref` does
not expose `listenManual`) that requires a split-provider
refactor (separate `FutureProvider` for async-resolved
`(agentId, sessionId)` + family-keyed Notifier for the events
listener). That refactor is bundled with the AgentFeed migration
into a single post-MVP wedge (P2 in the plan); both consumers
need the same shape, and bundling cuts the test pass in half.
Until P2 lands, the overlay continues to own its own SSE
subscription; the eager-load contract is unchanged from v1.0.476.

## Consequences

### Positive

- **Demo-critical "operate for me" feel achievable.** Steward
  navigates the user through the demo arc instead of
  command-line-ing them through a list of features.
- **Single contract for any future "drive the mobile app"
  action.** URI-as-API is robust to future write capabilities;
  tier framework handles future visual artifacts; verb field
  handles future intent kinds.
- **No protocol pressure** to ship MCP additions per UI
  capability — every new destination is a URI host, every new
  artifact type is a markdown code-fence language.
- **Coexistence axiom prevents UX regressions.** The non-modal
  + three-state-dismissal locks make the panel safe to ship
  always-on without competing with the underlying page.
- **Compact-mode framing prevents Sessions-chat duplication
  drift.** Future contributors who try to add tool-call cards
  / thought rendering / full-fidelity expansion to the overlay
  hit this ADR first.

### Negative / costs

- **Two SSE subscriptions to the steward agent** when both
  Sessions chat and overlay are open simultaneously. Resolved
  by `agent-events-shared-provider` plan. P1's infrastructure
  layer shipped in v1.0.478; the consumer migrations
  (overlay + AgentFeed) are bundled into P2 post-MVP.
- **Empty backfill flash** before the cache-only first paint
  lands — unavoidable on absolute first install. Resolved by
  P1 of the same plan.
- **Tier 1 renderer registry is a new code surface** to maintain
  alongside the markdown library. Acceptable; future
  contributors learn one extension point.
- **Action-aware intent verb requires hub-side payload extension**
  whenever new intent kinds land. Acceptable; the field is
  optional and back-compatible.

### Reversibility

| Decision | Reversible? | Cost to reverse |
|---|---|---|
| D1 overlay surface | Yes | Remove overlay subtree; ~600 LOC mobile + Settings cleanup |
| D2 URI as API | Hard | Migrate every steward template + every mobile router case + every `mobile.navigate` MCP call. Don't reverse without a new ADR |
| D3 voice via system IME | Yes | Add custom record button; doesn't break anything else |
| D4 non-modal panel | Yes | Re-add barrier; would re-open coexistence bugs |
| D5 single-shell pattern | Yes if zero N-pucks shipped | Hard once N-pucks exist (would need to converge) |
| D6 IAA all three | Soft | Could ship Audit-only, but would re-introduce Rabbit R1 trust failure |
| D7 three-state dismissal | Yes | Add a fourth state; small UX learning cost |
| D8 Tier 1 first | Yes | Skip to Tier 2 first; ~3× more APK + sandbox work |
| D9 compact mode | Hard once contributors add full-fidelity rendering | Each addition makes the boundary fuzzier |
| D10 verb field | Easy to extend | Hard to remove once present |
| D11 toggle gating | Yes | Move toggle out of Experimental once demoably stable |

## References

### Internal

- `docs/discussions/agent-driven-mobile-ui.md` — design rationale
  (Open). Sections 1–13 cover the research, axioms, SOTA, and
  open questions register.
- `docs/plans/agent-artifact-rendering-tier-1.md` — Tier 1
  implementation plan (Open).
- `docs/plans/overlay-history-and-snippets.md` — W1–W3 of the
  history + snippet wedge (Done as of v1.0.476).
- `docs/plans/agent-events-shared-provider.md` — P1 provider
  infrastructure shipped v1.0.478; P2 (overlay + AgentFeed
  consumer migrations bundled, split-provider shape) deferred
  post-MVP.
- `docs/plans/voice-input-overlay-v1.md` — push-to-talk Android
  voice plan (Open).
- ADR-005 (UX principal/director).
- ADR-017 (layered stewards) — provides the general-vs-domain
  split the overlay targets.
- ADR-020 (director action surface) — companion ADR for
  director-level actions.

### External (feeds the §10 SOTA framing)

- Smashing Magazine 2026 — "Designing for the Agentic Era"
  (IAA framework: Intent → Action → Audit).
- Apple HIG 2025 — Apple Intelligence Floating Prompt design
  guidance (single-instance, non-modal, system-IME-integrated).
- Slack mobile floating chat pattern (single-shell-multi-row).
- Discord PiP (edge-dock variant; deferred per D5).
- Rabbit R1 / Humane Pin failure analyses (Audit-only trap;
  motivates D6).

### Open questions resolved by this ADR

| # | Question | Resolution |
|---|---|---|
| Q1–Q12 | Twelve open questions from §5 of the discussion doc | All addressed in D1–D11 (see discussion doc cross-reference once D1–D11 are mapped per question; mapping pass scheduled with v1.0.477 doc work) |
| Q14 | Tier (1/2/3) for agent-conjured surfaces | D8 — Tier 1 first |
| Q15 | Floating-surface capacity model | D5 — single shell, multi-conversation inside (Pattern B) |

### Future questions deferred (NOT resolved here)

- **Voice output (TTS).** Steward replying in voice. Out of
  scope for this ADR — its own future ADR if voice-out demand
  surfaces.
- **Inline approval/decide affordances** in the overlay. Today
  the attention badge jumps to Me tab. Could move to inline if
  the demo arc shows attention-decision frequency from the
  overlay. Future ADR if so.
- **Multi-steward concurrent conversations.** D5 allows the
  shape; the actual implementation (per-team / per-member /
  per-project steward conversations rendering in the same
  panel list) is a future wedge.
