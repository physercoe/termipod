---
name: Agent feed split
description: Executable wedge-by-wedge plan to split lib/widgets/agent_feed.dart (6,196 LOC, 37 classes — the largest file in the codebase) into a layered set of libraries. Import-based layering (not part/part-of): Layer 0 a pure tested reducer, Layer 1 shared render primitives (dissolving the existing _ToolKvLine and double-_payloadOf duplication), Layer 2 the six card clusters, Layer 3 the slim container. 7 wedges, ~no behavior change, CI + on-device smoke per wedge. Realises R2A of docs/discussions/monolith-refactor.md.
---

# Agent feed split — phased

> **Type:** plan
> **Status:** Proposed (not started)
> **Audience:** contributors
> **Last verified vs code:** v1.0.728

**TL;DR.** `lib/widgets/agent_feed.dart` is 6,196 LOC / 37 classes — the
largest file in the repo and a recurring regression site (the event-kind
dispatch logic, v1.0.667/699/717/720/721). This plan splits it **by its
latent dependency layers**, using ordinary library imports (not
`part`/`part of`). The split is value-neutral on behavior and dissolves
two pieces of existing duplication along the way. Seven wedges, each a
single green PR. Realises **R2A** of
`docs/discussions/monolith-refactor.md`.

This plan supersedes the *ordering* sketched in that discussion (which
put the reducer last as "highest risk"). Reading the code inverts that:
the reducer logic is **already** top-level pure functions with **10
dedicated test files**, so extracting it is the lowest-risk, highest-value
move and goes **first**.

---

## What the code actually looks like (verified at v1.0.728)

37 classes + ~20 top-level helpers in one library. Line refs below are at
v1.0.728 and each wedge must re-confirm them (they drift as wedges land).

**The hidden layering.** Sorted by line the file looks tangled; sorted by
*dependency* it is clean:

```
Layer 0  feed_reducer        pure fns: classification, dedupe, formatters   (no Flutter deps beyond types)
   ▲
Layer 1  feed_render         shared widgets/helpers everything draws with
   ▲
Layer 2  six card clusters   event_card, telemetry, interaction, approval, tool_renderers, misc
   ▲
Layer 3  agent_feed (container)  _AgentFeedState: subscribe/ingest/scroll/build
```

**Coupling evidence that fixes the mechanism choice (import, not `part`):**
- `_jsonPretty` is a `static` on `AgentEventCard`, already called
  cross-class as `AgentEventCard._jsonPretty(...)` (lines 5202, 6126).
- `_ToolKvLine` (line 5225) exists **only** to duplicate `_kv` — its own
  doc says it "mirrors the parent card's `_kv` formatting without
  depending on its private instance method." The privacy wall was already
  hit and worked around by copying.
- The two `_payloadOf` statics (lines 2777, 3178) are **byte-identical**.
- `_CollapsibleMono` (line 5042) is called from **all five** card
  clusters (2969, 3888, 4079, 4696, 4904, 5045, 5202, 5206, 6186).

`part`/`part of` would preserve all this private cross-talk untouched
(zero churn) — but it has **no precedent** anywhere in `lib/` (122k LOC),
keeps the coupling rather than removing it, and yields no testable units.
Import-based layering matches the team's demonstrated instinct (the
`_ToolKvLine` decoupling; three existing `export … show` re-exports) and
produces the architectural win. The cost is promoting ~8 private symbols
to public and updating their call sites — fully compiler-checked.

### Symbol inventory per target library

**Layer 0 — `lib/widgets/agent_feed/feed_reducer.dart`** (~500 LOC)
- Already top-level (lines 1–714, most `@visibleForTesting`):
  `agentEventIsReplay`, the content-stable dedupe-key fn,
  `kAgentFeedAlwaysHiddenKinds`, `kAgentTurnActiveKinds`,
  `rateLimitsFromEvents`, `latestStatusLinePayload`,
  `formatRateLimitResetsAt(+Absolute)`, `buildSessionCostTooltipFromDetail`,
  `modeModelStateFromEvents`, `envelopeRoleLabel`, `envelopeSenderLabel`,
  `renderAttentionReplyText`, privates `_fmtAbsoluteShort`,
  `_shortModelNameForTooltip`.
- Lift out of `_AgentFeedState` and make pure (pass `events`/`verbose`
  as params instead of reading fields): `_isIdleDropSignature` (static,
  1417), `_isAgentBusy` (2126), `_latestSessionInitPayload` (2158),
  `_latestModeModelData` (2196), `_modeModelSig` (2218), `_stringList`
  (2343), `_isHiddenInFeed` (2368), `_isGatedToolName` (2487),
  `_isVerboseOnly` (2502), `_isCumulativeUsage` (2521),
  `_collapseStreamingPartials` (static, 2544).

**Layer 1 — `lib/widgets/agent_feed/feed_render.dart`** (~700 LOC)
- Promote to public top-level fns (rename, update call sites):
  `_mono`→`feedMono` (4272), `_kv`→`feedKv` (4241), `_textBody`→`feedTextBody`
  (4058), `_jsonPretty`→`feedJsonPretty` (4286).
- Move shared widgets: `_CollapsibleMono` (5042), `_DiffView`/`_DiffLine`/
  `_DiffKind` (5274/5262/5260), `_StatusPill` (5374), `_ModelTokens`
  (5418), `_humanWindow` (6058), `_fmtTokens` (5893, today a static on
  `_TelemetryStrip`).
- **Dedup removals:** delete `_ToolKvLine` (5225) and route its callers to
  a shared `feedKvLine`; collapse the two identical `_payloadOf` (2777,
  3178) into one `feedPayloadOf`.

**Layer 2 — six cluster libraries under `lib/widgets/agent_feed/`**
- `event_card.dart` (~1,140): `AgentEventCard` (3436), `_AgentEventCardState`
  (4325), `_CardHeader` (4427).
- `interaction_cards.dart` (~700): `_PendingPermissionPrompts` (2740),
  `_PermissionPromptCard`(+State) (2790/2803), `_PlanApprovalBody` (3050),
  `_CompactionBody` (3102), `_PendingSelections` (3144),
  `_SelectionCard`(+State) (3191/3200).
- `approval_cards.dart` (~470): `_ApprovalCard`(+State) (4571/4587),
  `_ApprovalOption` (4761), `_DecisionChip` (4767),
  `_AskUserQuestionCard`(+State) (4816/4834), `_AskOption` (5033).
- `tool_renderers.dart` (~350): `_FoldableToolCall`(+State) (5110/5133),
  `_ToolResultInline`(+State) (6092/6101).
- `telemetry_strip.dart` (~520): `_TelemetryStrip` (5472), `_TelemetryTile`
  (5987).
- `feed_misc.dart` (~150): `_OfflineBanner` (2601), `_VerboseToggleChip`
  (2653), `_NewEventsPill` (3372).

**Layer 3 — `lib/widgets/agent_feed.dart` (container, residue ~1,000 LOC)**
- `AgentFeed` (715) + `_AgentFeedState` (773) minus the lifted reducer
  methods: `initState`/`dispose`/`didUpdateWidget`, `_bootstrap`,
  `_subscribe`, `_scheduleReconnect`, `_ingestSnapshot`, `_maybeLoadOlder`,
  `_maybeBackfillSessionInit`, the scroll set (`_onScroll`, `_jumpToLatest`,
  `_scrollToTail`, `_trySeekInitialSeq`), the session-cost poll
  (`_startSessionCostPolling`, `_fetchSessionCost`), the
  `_maybeFire*`/`_onSetMode`/`_onSetModel` side-effects, and `build()`.
- Re-exports the public reducer/render API
  (`export 'agent_feed/feed_reducer.dart';`) so the 10 existing test
  imports of `agent_feed.dart` keep resolving unchanged.

---

## Constraints (inherited from monolith-refactor.md)

- **No behavior change.** Each wedge is a pure rearrangement. Verified by
  CI (`flutter analyze` + `flutter test` + Android/iOS release builds)
  plus an on-device smoke of the feed — **there is no local Flutter SDK,
  so CI is the only automated gate.**
- **No new dependencies.**
- **One wedge per PR.** Each PR compiles and is green on its own.
- **Doc-only?** No — this is code; each wedge bumps the app version.

---

## Test surface (the behavior contract)

Ten test files already pin the reducer/formatters and must stay green
every wedge:
`agent_feed_kind_classification_test`, `_rate_limits_test`,
`_cost_chips_test`, `_status_line_test`, `_status_line_reducer_test`,
`_replay_dedupe_test`, `_w6_test`, `attention_reply_render_test`,
`envelope_sender_label_test`, `mode_model_picker_test`.

Because Layer-0 functions are re-exported from `agent_feed.dart`, **none
of these imports change** — they are the proof that W0 preserved behavior.

---

## Wedge sequence

Bottom-up by dependency layer, so every PR compiles. The order differs
from the discussion doc on purpose (reducer first — see TL;DR).

| Wedge | Library created | ~LOC out | Risk | Guard |
|---|---|---:|---|---|
| **W0** | `feed_reducer.dart` (Layer 0) | ~500 | low | the 10 reducer tests (unchanged via re-export) |
| **W1** | `feed_render.dart` (Layer 1) + dedup | ~700 | low-med | compiler (every renamed call site) + analyze |
| **W2** | `feed_misc.dart` | ~150 | trivial | analyze + smoke |
| **W3** | `telemetry_strip.dart` | ~520 | low | `_cost_chips`/`_rate_limits`/`_status_line` tests + smoke |
| **W4** | `tool_renderers.dart` | ~350 | low | smoke: tool-call fold, tool_result, diff |
| **W5** | `approval_cards.dart` + `interaction_cards.dart` | ~1,170 | med | smoke: permission/AskUser/plan/selection/compaction |
| **W6** | `event_card.dart` | ~1,140 | med | full feed smoke; container becomes residue |

### W0 — feed_reducer (do first)
Move the top-level reducer fns, then lift the 11 `_AgentFeedState`
classifier methods into pure functions (parameterise the field reads).
Re-export from `agent_feed.dart`. **Acceptance:** the 10 reducer tests
pass with no import edits; container shrinks ~500 LOC; `analyze` clean.

### W1 — feed_render (the enabler)
Promote `feedMono/feedKv/feedTextBody/feedJsonPretty`; move the shared
widgets; **remove** `_ToolKvLine` and the duplicate `_payloadOf`. Update
every call site (compiler-enforced). **Acceptance:** no `_ToolKvLine`, one
`feedPayloadOf`; `analyze` zero new warnings; all card tests green. This
wedge touches the most call sites but each is a mechanical rename the
compiler catches.

### W2–W6 — cluster extractions
Each cluster moves to its own library importing Layers 0/1. Order is
ascending by how many other clusters reference it, so the residue
(`event_card`, which instantiates most others) goes last. After W6,
`agent_feed.dart` is ~1,000 LOC of container.

**Per-cluster manual smoke (no local Flutter — runs on device):**
live turn streams + busy pill clears · tool-call fold / tool_result
default-folded · diff insert/delete/context · permission allow/deny ·
AskUserQuestion submit · plan-approval + compaction bodies · telemetry
cost/context-fill/rate-limit chips · new-events pill jump · offline
banner · verbose toggle.

---

## Acceptance (whole plan)

- `agent_feed.dart` ≤ 1,100 LOC; no sibling library > 1,200 LOC.
- `_ToolKvLine` gone; single `feedPayloadOf`; reducer is a standalone
  tested library.
- All 10 reducer tests + existing widget tests green at every wedge.
- `flutter analyze` zero new warnings; Android/iOS release builds green.
- On-device feed smoke clean after W2–W6.
- The R0 CI LOC ceiling (monolith-refactor.md) ratchets down as wedges
  land — wire `agent_feed/*` siblings into it.

---

## Risks & mitigations

- **Lifting instance classifiers to pure fns (W0)** changes field reads to
  params. Mitigation: the kind-classification + replay-dedupe tests pin
  exactly this behavior; do W0 against them red-green.
- **Broad rename blast radius (W1).** Mitigation: renames are
  compiler-checked; analyze fails on any miss. No semantic change.
- **No widget-level test for the container build().** Mitigation: W6 is
  the only wedge that meaningfully reshapes `build()` glue; lean on the
  on-device smoke and keep the diff to *moves*, not edits.
- **Line refs drift between wedges.** Mitigation: each wedge re-greps the
  class/symbol list before moving; never trust this doc's line numbers
  after the prior wedge merges.

---

## Open questions

1. **W5 as one PR or two?** ~1,170 LOC across two cluster files. Default
   to two PRs (approval, then interaction) to honor the ≤3-day / one-thing
   rule; merge only if they prove trivially mechanical.
2. **Reducer as free functions or a `FeedReducer` class?** Free functions
   match today's top-level shape and the existing tests. Defer any
   class-wrapping to a later, separate step if a stateful reducer is ever
   wanted (it isn't today).
