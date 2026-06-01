# Agent transcript — debugging affordances & session-header parity

> **Type:** plan
> **Status:** Done (2026-06-01) — all three phases shipped. **P1**
> (v1.0.770: lens + filter/jump pill + seq-anchored seek), **P2**
> (v1.0.771–773: shared `SessionHeader` + dense chip + `View ▾`, both
> surfaces converged, Pane/Journal extracted), **P3** (v1.0.774:
> `dense` flag → full-screen lens bar + right-edge minimap +
> `TranscriptScreen` expand route).
> **Audience:** contributors
> **Last verified vs code:** v1.0.777

**TL;DR.** From a testing pass: the agent transcript is hard to debug
(no way to filter to errors, no way to jump to a turn), and the same
feed is shown through several inconsistent surfaces with cramped,
hand-rolled headers. This plan adds **float-don't-stack** filter/jump
chrome to `AgentFeed`, unifies the project-agent and session-detail
surfaces behind one **shared `SessionHeader`** (with a `View ▾`
switcher so both reach Feed/Pane/Journal/Insights), and optimizes the
header's tight horizontal budget by slimming the `SessionInitChip`.
Builds on the `AgentFeed` extraction in
[agent-feed-split.md](agent-feed-split.md).

---

## 1. Why

Tester feedback, 2026-05-31:

1. **The transcript can't be debugged at scale.** A long run has no
   "show only errors" view and no jump-to-turn — you scroll a 5k-event
   feed by hand to find the failed tool call.
2. **The same feed wears four different faces.** General steward =
   floating overlay; project agent = a 4-tab sheet; a session from the
   Sessions tab = a feed-only full screen; archived = a 3-tab screen.
   The non-parity grew per-surface, not by design.
3. **The header is over-stuffed.** Once a surface needs name + session
   chip + a view switcher + an actions menu + close on one row, the
   `SessionInitChip` (the widest, most variable element) overflows.

## 2. Current state (grounded)

`AgentFeed` (`lib/widgets/agent_feed.dart`) build pipeline:
`_events → filtered` (drops hidden kinds via `isHiddenInFeed`,
agent_feed.dart:1236) `→ collapseStreamingPartials → visible`, rendered
in a `ListView.separated(_scroll)` inside an `Expanded > Stack`
(agent_feed.dart:1296). **Existing chrome floats over the Stack and
never adds a Column row** — `VerboseToggleChip` is `Positioned(top:6,
right:6)` (1392), `NewEventsPill` is `Positioned(bottom:12)` (1373).
The file is emphatic about not "eating a transcript line": the
mode/model picker was lifted to the parent AppBar, and `session.init`
is refused inline.

Two facts make the debug features cheap:

- **Errors are already a clean predicate**: `kind == 'error'`
  (`event_card.dart:142`) or a `tool_result` with
  `payload.is_error == true` (`event_card.dart:381`, `:877`). Failed
  tool cards already render in `DesignColors.error`.
- **Seq-anchored jumping already exists**: `_trySeekInitialSeq()` jumps
  to a specific event seq (used for attention→turn deep-links,
  agent_feed.dart:63). `turnCount` is already computed for the
  `TelemetryStrip`.

The two header surfaces that must reach parity:

| | Session detail (`SessionChatScreen`) | Project agent (`projects_screen.dart`) |
|---|---|---|
| Container | full-screen `Scaffold` | modal sheet |
| Header | `AppBar`: title + `SessionInitChip` (`sessions_screen.dart:429`) + `⋮` PopupMenu (:467) | custom `Row`: title + `_ActionsMenu` (:1933) + `×` (:1946); chip on its own row (:1959) |
| Body | `AgentFeed` only — **no Pane/Journal/Insights** (:541) | 4-tab `TabBar` Feed/Pane/Journal/Insights (:2002) |

`SessionInitChip` (`session_details_sheet.dart:155`) is a `Row` of up
to five `_Pill`s — `kind · model · permission_mode · {n}t · {n}mcp` —
plus a `▾`. It is a **tappable summary**: tapping opens a drawer with
the full payload (`:117`, `:200`), so the chip can be collapsed hard
without losing information.

## 3. Design decisions (ratified)

- **Float, don't stack.** New transcript chrome floats over the Stack
  corners/edges and only renders when relevant — matching
  `VerboseToggleChip` / `NewEventsPill`. No new Column rows.
- **One lens, single-select** (`All · Text · Tools · Errors`) rather
  than multiselect chips. `Verbose` stays orthogonal (depth, not lens).
- **Combined filter+jump pill.** When a lens is active, one pill
  (`⚠ Errors · 1/3 ▲▼ ✕`) both indicates the filter and steps through
  matches. Stepping drives a generalized `_seekToSeq`.
- **Jump on seq, never list index.** The feed pages older events lazily
  (`_loadOlder` / `_atHead`), so an index→pixel jump is unreliable for
  not-yet-loaded targets. Generalize `_trySeekInitialSeq` into
  `_seekToSeq(seq)` that loads older pages until the target is present,
  then `ensureVisible`.
- **One shared `SessionHeader`.** Both surfaces render the same widget
  (title + chip + `View ▾` + `⋮` + `×`) so parity holds structurally.
- **Dedicated `View ▾` switcher** (not folded into the `⋮` actions
  menu): discoverable, keeps navigation separate from actions. Feed is
  the default; an `IndexedStack` behind it preserves each view's scroll
  and state (as `TabBarView` does today). `SessionChatScreen` thereby
  gains Pane/Journal/Insights; the project sheet drops the tab row.
- **Slim the chip.** `kind` → engine glyph; `model` → short;
  `permission_mode` → a colored shield glyph (not the long word);
  `{n}t` / `{n}mcp` counts → drawer only. Add a `dense` variant.
- **Header yield priority** under width pressure: the fixed controls
  (`View ▾`, `⋮`, `×`) never shrink; the title `Flexible`-ellipsizes
  first; the chip collapses second.
- **Hybrid chip placement.** A `LayoutBuilder` keeps the slim chip
  inline when it fits and drops it to its own slim row 2 only when the
  row is full — never truncating the chip to unreadable.
- **Responsive disclosure by container.** `AgentFeed` takes a `dense`
  flag: constrained hosts show only the funnel/pill; a full-screen host
  unlocks a lens *bar* and a right-edge minimap.

## 4. Mocks

Constrained host (overlay / sheet):

```
REST                                  LENS ACTIVE
┌────────────────────────┐            ┌────────────────────────┐
│ (≡)            (v·12)   │            │ ( ⚠ Errors 1/3 ▲▼ ✕ )  │
│  assistant text…        │            │  ✗ result · error       │
│  ▸ tool_call bash       │            │  ✗ result · error       │
│              (▼ 3)      │            │  …                      │
├────────────────────────┤            ├────────────────────────┤
│ > compose…              │            │ > compose…              │
└────────────────────────┘            └────────────────────────┘
(≡)=filter funnel  (v·12)=verbose chip (existing)
```

Full-screen host (chrome unlocks a lens bar + minimap):

```
┌──────────────────────────────────────────────┐
│ ‹  refactor-x   [chip]   (v·12)   ⋮            │  AppBar
│ [ All · Text · Tools · Errors 3 ]        (≡)   │  lens bar (full-screen only)
├──────────────────────────────────────────────┤▕
│  ▸ tool_call bash                            │▕  ▕ = turn tick
│  ✗ result · error                            │▌  ▌ = error tick (red), tap to jump
│  …                                           │▕
├──────────────────────────────────────────────┤
│ > compose…                                   │
└──────────────────────────────────────────────┘
```

Shared header — both surfaces converge (chip hybrid placement):

```
WIDE (chip inline)
┌ refactor-x  [⟁ opus-4.8 🛡 ▾]  View:Feed ▾  ⋮  × ┐
│  AgentFeed…                                       │
└───────────────────────────────────────────────────┘

NARROW (chip drops to row 2; title ellipsizes; controls fixed)
┌ refactor-the-att…        View:Feed ▾   ⋮   × ┐
│ [⟁ opus-4.8 🛡 ▾]                            │
│  AgentFeed…                                  │
└──────────────────────────────────────────────┘
```

## 5. Phases

- **P1 — debug affordances (self-contained, highest value). ✅ Shipped
  v1.0.770-alpha.** Generalized `_trySeekInitialSeq` → `_seekToSeq(seq)`
  (cold-open deep-link + lens stepper share it); added `FeedLens` +
  `agentEventMatchesLens` to the reducer; added the funnel + combined
  filter/jump pill (`FeedFilterControl`) and an empty-lens hint. All four
  lenses (All/Text/Tools/Errors) wired. No header or surface changes;
  rides the existing float-over-Stack pattern. The full-screen lens *bar*
  + minimap deferred to P3 (this ships the constrained-host chrome only).
- **P2 — header parity. ✅ Shipped v1.0.771–773.** Extracted
  `SessionHeader` (title + subtitle + slim chip + `View ▾` + `⋮` +
  `×`/back) with hybrid `LayoutBuilder` chip placement; added the
  `dense` `SessionInitChip` variant (perm-mode → shield glyph, counts →
  drawer); replaced the project sheet's `TabBar` with a `View ▾`-driven
  `IndexedStack` (771); extracted `AgentPaneView` / `AgentJournalView`
  so the views are shareable, no fork (772); adopted `SessionHeader` +
  the four views in `SessionChatScreen` (773), so the session-detail
  surface gained Pane/Journal/Insights and the two surfaces are now
  structurally identical headers.
- **P3 — full-screen transcript route. ✅ Shipped v1.0.774.** Added the
  `dense` flag to `AgentFeed` (`true` = P1 funnel/pill; `false` = full-
  screen). The `dense=false` path unfolds `FeedLensBar` (every lens +
  live count) and a right-edge `FeedMinimap` (faint tool-call ticks + red
  error ticks, tap-to-jump via `_seekToSeq`, prefers nearest error). New
  `TranscriptScreen` hosts `AgentFeed(dense:false)`; `SessionChatScreen`'s
  Feed runs `dense:false` inline, the project-agent sheet gets an
  `ExpandFeedButton` that pushes `TranscriptScreen`. The archived-agent
  surface (still a 3-tab) can wire `onExpand` opportunistically later.

## 6. Constraints & risks

- A filter must not break tail-follow: live events that don't match the
  active lens must not yank the viewport — keep the `NewEventsPill`
  "N new below" semantics.
- Filter state is **ephemeral** (resets when the surface closes) so a
  stale filter never surprises a returning user.
- `_seekToSeq` must bound its load-older walk (a target seq below the
  first page) so a far jump can't spin indefinitely.
- The `dense` flag must thread through every `AgentFeed` host
  (`StewardOverlay`, the session sheet, `SessionChatScreen`, the
  project view) — confirm before P3.

## 7. Out of scope

- The general-steward `StewardOverlay` stays a floating panel by design
  (ambient concierge); this plan does not make it full-screen.
- Archived-agent surface (3-tab) is left as-is for now; it can adopt
  `SessionHeader` opportunistically once P2 lands.
- In-feed text search (there is already a separate `search_screen.dart`)
  is a complement, not part of this plan.

## 8. Follow-up — long-log navigation (v1.0.775–776)

Post-ship testing surfaced two issues and one design question.

**Fixes (v1.0.775).** The right-edge minimap was effectively un-tappable
(14px strip flush to the edge, fighting the device edge-swipe) and a tap
on a far tick silently no-op'd because the seq-anchored `ensureVisible`
needs an already-built `ListView` row. Widened + pulled off the edge, and
added `_seekToFrac` (proportional pre-scroll, then fine-tune) so any tick
jumps. Also added the `Turns` lens (inbound prompts + a2a + system).

**Design question — "why not numbered pages?"** A tester proposed prev/
next *pages* with a page number + direct jump. Rejected as the primitive,
kept as the *intent*:

- The transcript is an **append-only live tail** — page indices defined
  against a fixed boundary drift on every SSE event.
- There is **no total count** (the server runs `LIMIT`, never `COUNT`),
  and a long agent is 10k–100k+ rows; counting on every open is costly and
  immediately stale.
- A resumed **session spans multiple agents**, and `seq` is per-agent —
  there's no dense global index to map to a page; only `ts` totally
  orders, and you can't derive "page 7" from a `ts` without an `OFFSET`
  scan.
- **`OFFSET` paging is O(offset)** in SQLite — it would regress exactly
  the deep/long logs the feature is meant to serve, versus the current
  O(log n) keyset cursor (`before` / `before_ts`).

**Shipped instead (v1.0.776, revised v1.0.777).** The *turn* is the stable
unit: a stepper walks inbound-prompt anchors, `⤒` jumps to top-of-loaded
(paging older on demand, never breaking the contiguous tail-anchored
window), and the minimap is a tick-overview + tap-jump + drag-scrubber.
Keyset infinite-scroll stays underneath.

**v1.0.777 corrections (testing).** The first cut had real flaws:

- The full-width footer **`TranscriptNavBar` ate vertical space**, and its
  `turn N/M` **ordinal disagreed with the cost/turn chip** (it counted
  prompts; the chip counts agent `turn.result`s) and was derived from
  scroll-percent under variable row heights, so prev/next **mis-stepped /
  appeared to wrap**. Replaced with a compact floating **`TurnStepperPill`**
  (`⤒ ‹ ›`): relative, explicitly clamped (disables at the ends), no
  ordinal to mismatch.
- A **position indicator over lazily-loaded content with no total is
  inherently non-monotonic** — loading an older page above your row
  re-scales any normalized %/thumb. So the jump-pill **percent and the
  minimap thumb were removed** (they jittered on every load). The minimap
  keeps ticks + tap-jump + drag-scrub; position sense is qualitative.
- Turn anchors now **exclude system-injected prompts** (`isTurnAnchorEvent`
  skips `producer==system` / `from.role==system`), the minimap renders in
  **every** full-screen view (ticks include turn anchors), and the expand +
  verbose chips share one row so they can't collide.
