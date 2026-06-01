# Agent transcript — debugging affordances & session-header parity

> **Type:** plan
> **Status:** Proposed (2026-06-01) — design ratified across review;
> no code yet. Three phases; P1 is self-contained.
> **Audience:** contributors
> **Last verified vs code:** v1.0.767

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

- **P1 — debug affordances (self-contained, highest value).**
  Generalize `_trySeekInitialSeq` → `_seekToSeq(seq)`; add the lens
  predicate to the `filtered` pass; add the funnel + combined
  filter/jump pill; wire Errors first, then Text/Tools. No header or
  surface changes. Ships behind the existing float-over-Stack pattern.
- **P2 — header parity.** Extract `SessionHeader` (title + slim chip +
  `View ▾` + `⋮` + `×`); add the `dense` `SessionInitChip` variant and
  hybrid `LayoutBuilder` placement; replace the project sheet's
  `TabBar` with an `IndexedStack` driven by `View ▾`; adopt
  `SessionHeader` in `SessionChatScreen` so it gains the other views.
- **P3 — full-screen transcript route.** An "expand" affordance pushes
  a dedicated full-screen `AgentFeed` (also the missing full-screen
  surface); unlock the lens *bar* and the right-edge minimap with turn
  and error ticks via the `dense=false` path.

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
