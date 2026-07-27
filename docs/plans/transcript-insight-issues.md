# Transcript P5 wedge — session integrity issues + streaming-markdown efficiency

> **Type:** plan
> **Status:** Draft (2026-07-27) — for fleet implementation
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `1e9a2b46`
> **Parent:** [agent-transcript-redesign.md](agent-transcript-redesign.md) §P5
> (the recorded-not-scheduled bucket). This wedge schedules two of its items
> and leaves the rest recorded (§6 below). It does NOT touch the J3 **Inspect**
> tab ([debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md)) —
> "Issues" here are *session-transcript* integrity findings, not code/logs.

**TL;DR.** Two independent tracks, both born from reading kimi-code's
open-sourced debug tooling (`MoonshotAI/kimi-code`, `apps/vis` +
`apps/kimi-web`) against our transcript stack.

**Track A — Issues in the digest.** The digest fold (ADR-038,
`hub/internal/server/digest_fold.go`) already tallies *reported* errors —
events an engine chose to mark failed. It is blind to **structural**
failures: a `tool_call` whose result never arrives, a `tool_result` that
matches no call, a turn left open at termination, a truncated tool output,
a permission request nobody answered. Those are exactly the defects our own
mapper reviews keep finding by hand (the `callToolIdOf`/`tool_use_id` id-shape
bug shipped **three times**; each time the visible symptom was a card spinning
"running" forever — i.e. an orphan pair the system itself never noticed).
Track A ports the rule set of kimi vis's issues detector
(`apps/vis/web/src/lib/issues.ts` upstream) into the digest fold as a new
per-class **issues aggregation** (schema v7; sealed sessions pick it up via
the existing lazy refold), and renders it as an **Issues drawer**: a bottom
sheet on mobile Insight, a side drawer on desktop `RunReport` — every row
seek-navigable through the anchor machinery the Errors lens already uses.

**Track B — Streaming markdown stops re-parsing the world.** Kimi-web renders
streaming output with an incremental block parser (`markstream-vue`): the
stable prefix is cached; each chunk costs ~one tail block. Our mobile feed
does the opposite: every partial replaces the chain entry
(`collapseStreamingPartials`, `lib/widgets/transcript/feed_reducer.dart`)
and hands the **full accumulated text** back to `MarkdownBody` — full
re-parse, full re-highlight of every fenced block, full TeX layout, on the
UI isolate, per chunk: O(n²) over a long turn. Track B ports the *pattern*
(not the code): (B1) defer highlight/math while `partial:true`, (B2) split
the accumulated text at block boundaries and memoize completed blocks,
(B3) migrate `flutter_markdown` (discontinued upstream) to its drop-in
continuation `flutter_markdown_plus`.

Sequencing: **A1 (hub fold) → A2 (mobile drawer) ∥ A3 (desktop drawer)**;
**B1 → B2 → B3** independently. Nothing here blocks on anything outside
this doc.

---

## 0. Problem — concretely

### 0a. Structural failures are invisible until a human greps the transcript

Three shipped bugs, one shape: the P1 review found mobile group rows reading
only `id` while the log-tail mapper writes `tool_use_id` (cards spun
"running" forever); P2's review found the same class again; PR #375 found the
desktop stats strip double-counting subagent `turn.result`s. In every case
the *transcript data* contained the evidence — a call with no result, a
result with no call, counts that don't reconcile — and no surface surfaced
it. The digest's Errors lens (`digest_fold.go recordError`, line ~520) only
sees events with an explicit error mark; a **silently dangling pair is not
an error event**, so it never lands in `Errors`, never gets a funnel mark,
never reaches the RunReportCard stat.

Kimi's answer (vis Issues drawer) is a pure scan over the timeline with a
dozen structural rules. Ours can be *better* than a client-side scan: the
digest fold already walks every event exactly once, incrementally, inside
the `agent_events` POST transaction (`digest_store.go
foldEventIncremental`, line ~168), already resolves tool-call anchors
(`resolveToolAnchor`, line ~358), and already knows turn open/close. The
rules belong there — computed once, engine-neutral, served to both clients,
backfilled onto sealed sessions by the existing schema-bump refold
(`ensureAgentDigest`, `digest_store.go` line ~381: `SchemaVersion <
digestSchemaVersion` → full refold).

### 0b. Streaming re-render is O(n²) on the UI isolate

`collapseStreamingPartials` (`feed_reducer.dart` line ~1239) is correct:
partials carry full accumulated text, the chain entry is replaced in place,
one card per message. But the render side treats every replacement as a
fresh document: `EventCard._markdownBody` (`event_card.dart` line ~704)
calls `MarkdownBody` (line ~726) with the whole string —
`markdown` re-parses it, `HighlightedCodeBuilder`
(`markdown_builders.dart`) re-tokenizes **every completed fenced block**
through highlight.js, `MathBuilder` re-lays-out every formula, all
synchronously on the UI isolate, on **every chunk**. A long assistant turn
with two code fences re-does all of it dozens of times. Desktop has the
same shape in milder form (`react-markdown` re-renders the full tree per
update; React reconciliation absorbs some of it, `rehype-highlight` work
recurs).

Separately: `flutter_markdown` was **discontinued by the Flutter team**
(pub.dev marked 2025-05-30); we pin its final line (`pubspec.yaml` line
~80, `^0.7.4`). The maintained continuation `flutter_markdown_plus`
(Foresight Mobile) keeps the exact API surface we extend
(`MarkdownBody`, `MarkdownElementBuilder`, `builders` map, styleSheet) on
`markdown ^7.3.x` — a drop-in that keeps `HighlightedCodeBuilder` /
`MathBuilder` / `normalizeMultilineMath` untouched.

## 1. Substrate — what ships today that this plan reuses

- **Digest fold** (ADR-038/039/042/045): incremental fold in the POST tx +
  lazy backfill; `digestSchemaVersion = 6` (`digest_fold.go` line ~51);
  per-class error aggregation `Errors map[string]*errorClassAgg` with
  aligned sample slices `(SampleSeqs, SampleOrdinals, SampleTSs,
  SampleLabels)` capped at `maxDigestErrorSeqs` (`addSampleTS`,
  `digest_fold.go` line ~727); anchor relocation to the triggering event
  (`errorAnchor`, line ~501, issue #64 precedent).
- **Seek machinery**: `TranscriptSeekController` shared by
  `RunReportCard` (requester — Errors stat `onTap` seeks to
  `firstErrorSeq`, `run_report_card.dart` line ~217) and
  `InsightTranscript` (responder: window reset around `(ts, seq)` anchor +
  highlight). Issues rows navigate through this path unchanged.
- **Insight surface** (ADR-040): `session_analysis_view.dart` (digest
  dashboard over the sealed transcript), lens bar / errors funnel /
  minimap / N-of-M stepper — all whole-run, digest-driven.
- **Desktop parity surface**: `desktop/src/ui/RunReport.tsx` rendered from
  the same `GET /digest` in `AgentTranscript.tsx` (line ~1304).
- **Streaming chain**: hub stamps `message_id` + `partial:true` with full
  accumulated text (driver_acp.go text/thought/plan arms);
  `collapseStreamingPartials` folds text/thought/plan chains.
- **Upstream reference** (pattern source, MIT): `MoonshotAI/kimi-code`
  `apps/vis/web/src/lib/issues.ts` (rule set + severity model),
  `markstream-vue` (stable-prefix streaming markdown, worker-offloaded
  KaTeX/mermaid).

## 2. Track A1 — hub: issues aggregation in the digest

### Rules (engine-neutral, normalized-event layer)

Ported from vis and adjusted to our event kinds. Severity: `error` |
`warning` | `info`.

| class | severity | rule (over normalized `agent_events`) |
|---|---|---|
| `missing_tool_result` | error | `tool_call` whose id never receives a terminal `tool_result`/`tool_call_update` by **turn close** (sweep at `closeTurn`) or by agent terminal state (sweep at `finalizeDigestOutcome`) |
| `orphan_tool_result` | warning | `tool_result` whose id matches no prior `tool_call` (`resolveToolAnchor` miss — *both* id shapes checked, see anchors §5) |
| `truncated_output` | warning | tool result payload carrying a truncation marker (engines vary; detect the flags we actually map — kimi wire `truncated`, claude-code `is_truncated`-style fields — extend per-mapper as found) |
| `incomplete_turn` | warning | turn still open when the agent reaches a terminal state (`finalizeDigestOutcome`, `digest_store.go` line ~278) |
| `unanswered_permission` | warning | permission/approval request with no recorded decision by turn close |
| `rejected_permission` | info | approval decision = rejected (visible in feed, but the drawer aggregates) |
| `abnormal_stop` | warning | turn/stop reason `max_tokens`, `filtered`, `refusal` where the mapper carries one |
| `mixed_id_shape` | info | the same session carries tool ids under different key shapes across events (the `callToolIdOf` class made *self-reporting*) |

Notes:
- **Reported errors stay in `Errors`** — no overlap. Issues are structural
  findings only; the drawer UI merges both lists visually (§3) but the
  digest keeps them separate so the Errors lens/funnel semantics don't
  change.
- Pending-call state for `missing_tool_result` lives in the fold state the
  turn already scopes; incremental == brute-force must hold (ADR-038
  discipline — same-input refold produces identical issues; add the
  equivalence test alongside the existing fold tests).
- Live sessions: a call is only *missing* at turn close, never mid-turn —
  no flicker while a tool legitimately runs.

### Schema & wire

- `agentDigest` gains `Issues map[string]*issueClassAgg` (same shape as
  `errorClassAgg`: count + aligned sample slices + per-sample label),
  persisted as `issues_json` beside `errors_json`; bump
  `digestSchemaVersion` → **7**. Sealed sessions refold lazily on next
  read (`ensureAgentDigest` — no migration step needed); the column is
  additive so old clients ignore it.
- Digest GET body: `issues` object `{class → {count, severity, sample_seqs,
  sample_ordinals, sample_tss, sample_labels}}` + rolled-up
  `issue_count` / `issue_worst_severity` for the chip.
- Turn rows: per-turn `issue_count` beside `ErrorCount` (cheap, same
  `recordError`-style tally) so the funnel can mark issue-bearing turns
  later (deferred, §6).

## 3. Track A2/A3 — clients: the Issues drawer

### A2 — mobile (the layout)

**Entry points (two, both existing patterns):**
1. **RunReportCard stat chip** — an `Issues` stat beside `Errors`
   (`run_report_card.dart` stat row): count + worst-severity tint (error
   red > warning amber > info neutral). Hidden at 0 (a "0 issues" chip is
   noise; the drawer's empty state is reachable from the overflow menu for
   the "prove it's clean" use).
2. **Insight lens bar badge** — a small severity-tinted dot+count pill at
   the trailing edge of the lens bar (`insight_transcript.dart`), so the
   drawer is reachable while deep in the transcript without scrolling back
   to the dashboard.

**The drawer itself — modal bottom sheet** (decision §7.5 of the parent
plan: mobile state surfaces are bottom sheets, not docks/rails):

```
╭──────────────────────────────────────────╮
│  ── grab handle ──                       │
│  Issues (7)              [All|Err|Warn]  │  ← header: count + severity
│                                          │    segmented filter (chips)
│  ▌ERROR   missing tool result            │  ← severity bar + class label
│  │        Bash · call_9f2 never resolved │    one-line summary (sample
│  │        turn 14 · 12:41:07        seq→ │    label) + anchor meta;
│  ├──────────────────────────────────────┤    trailing seek affordance
│  ▌WARN    truncated output               │
│  │        Read · 2 of 4 samples    (×4)  │  ← class rows aggregate: count
│  ├──────────────────────────────────────┤    badge, expand to samples
│  ▌INFO    permission rejected            │
│  ╰  …                                    │
│                                          │
│  8 checks · engine kimi-code-ts · v7     │  ← footer: provenance line
╰──────────────────────────────────────────╯
```

- `DraggableScrollableSheet`, initial ~55% height, drag to full; list is
  grouped **severity-first, then class** (vis's sort), each class row
  showing `count` and expanding in place to its capped samples
  (`maxDigestErrorSeqs`-style cap; footer notes "showing first N" when
  capped — the no-silent-caps anchor).
- **Row tap = seek**: dismiss the sheet, then
  `TranscriptSeekController.jump` to the sample's `(ts, seq)` — the exact
  Errors-stat path; the transcript window-resets and highlights the row.
  The reported-`Errors` classes render in the same list (severity `error`,
  tagged `reported`) so the drawer is the one "what went wrong" surface,
  while the lens/funnel keep their existing errors-only semantics
  untouched (state-not-filtering anchor).
- **Empty state**: "No issues · N checks ran" + the provenance footer —
  the affirmative-clean signal is the point of the surface.
- Live runs: the sheet reads the digest like the dashboard does — "as of
  `<ts>` · live" affordance carried over; no SSE tail in Insight (ADR-040
  §E snapshot semantics unchanged).

### A3 — desktop parity

`RunReport.tsx` gains the same stat + a right-side drawer (existing drawer
idiom in the transcript surface) rendering the identical grouping; row
click drives the desktop seek path. **Parity is a review anchor, not an
afterthought** — this family has shipped 4 mobile↔desktop misses
(`callToolIdOf` ×3, stats strip #375); both clients must consume the same
digest fields with the same severity ordering and the same
hidden-at-zero rule. Extract shared fold/format logic where the desktop
side already has the pattern (`state/transcriptStats.ts` precedent — pure
module + node tests).

## 4. Track B — streaming-markdown efficiency (mobile-first)

### B1 — defer heavy treatment while `partial:true` (smallest, ship first)

While the chain entry carries `partial:true`, `_markdownBody` renders
fenced blocks as plain mono (skip `HighlightedCodeBuilder`) and skips
`MathBuilder` layout (raw `$…$` text) — the full builders run once, when
the final non-partial event replaces the chain. One conditional per
builder + the plumbing to thread `isPartial` into the card. Kills the
worst term (highlight.js × chunks) with zero new dependencies and zero
visual change on completed messages. Desktop equivalent: gate
`rehype-highlight`/`rehype-katex` on the partial flag the same way.

### B2 — stable-prefix block cache (the markstream idea, in Dart)

Split the accumulated text at **completed block boundaries** (blank-line +
fence-aware scanner — a fence opened and not yet closed pins the split
point before it): completed blocks each render as their own `MarkdownBody`
in a `Column`, keyed by content hash and cached (`const`-stable widget per
key — parse/highlight/math run once per block, ever); only the open tail
block re-parses per chunk. The scanner is pure Dart → unit-testable
(fences inside lists, unterminated fences, `$$` blocks spanning the split,
CRLF). Behavior guard: block-split rendering of the *final* text must be
visually identical to single-body rendering — snapshot-test the seams
(tight list spacing across split points is the known risk; splitting only
at top-level blank lines avoids most of it).

### B3 — migrate to `flutter_markdown_plus`

Dependency swap + import sweep; `MarkdownBody`, `MarkdownElementBuilder`,
`builders`, styleSheet are API-identical (v1.0.x, `markdown ^7.3.1` —
our direct `markdown ^7.2.2` pin moves with it). Do B3 *after* B1/B2 so
the perf work is bisectable from the dep swap. Verify the two behaviors we
depend on: builder-append semantics for the `a` element (the
double-render workaround documented in `_markdownBody`) and
inline-`<code>` fallthrough (no `class` attr → styleSheet path).

## 5. Sequencing & review anchors

**Order:** A1 → (A2 ∥ A3); B1 → B2 → B3. Tracks A and B are independent.

Review anchors (named for the recurring bug classes):
- **Both id shapes** (`callToolIdOf` class): every Track-A rule that pairs
  calls with results must resolve ids through the shared helpers
  (`fold_maps.dart callToolIdOf` on clients, `eventToolID` /
  `resolveToolAnchor` in the fold) — never a raw `payload['id']` read.
  The `mixed_id_shape` info rule is the canary.
- **Incremental == brute** (ADR-038): issues folded incrementally must
  equal a full refold — extend the existing equivalence tests; no
  `Date`-dependent rule.
- **Mobile↔desktop parity** (4 shipped misses): same digest fields, same
  severity order, same zero-hiding; shared pure modules + tests on both
  sides.
- **State, not filtering**: the drawer must not touch lens/funnel/busy
  semantics; `session.init`-class synthesized kinds are out of scope here
  (no new event kinds at all — Track A is fold + read only).
- **No silent caps**: capped sample lists say so in the UI footer.
- **B2 visual identity**: final-text rendering block-split vs single-body
  snapshot-equal; negative-test the scanner (an unclosed fence must pin
  the split).
- **B3 bisectability**: dep swap is its own commit; no behavior change
  mixed in.

## 6. Recorded, not scheduled (unchanged from parent §P5)

- **Raw-wire record/replay/diff** (kimi-inspect audit-trail concept) as an
  Insight mode over the P4 wire store — needs its own design (storage,
  retention, diff UI); this wedge's drawer neither blocks nor prejudges it.
- **Context projector** (vis `context-projector.ts` concept): model's-eye
  context reconstruction with compaction/undo ribbons — hub-side, digest
  family, separate wedge.
- **Session zip export/import** (vis `zip-import.ts` concept) for
  shareable debug bundles.
- **Funnel issue marks** (per-turn `issue_count` is folded in A1; the
  funnel/minimap glyphs wait until the drawer proves the taxonomy).
- Mermaid rendering on mobile (webview-per-diagram — desktop's kimi-web
  panel covers the need today).

## 7. Open questions

1. `truncated_output` detection is per-mapper opportunistic — is a
   mapper-side normalized `truncated: true` payload field worth
   standardizing first (one-line per mapper), so the fold rule is single?
2. Should `rejected_permission` be in the drawer at all, or is it noise
   once `unanswered_permission` exists? (vis keeps it as info; start there,
   demote on feedback.)
3. B2 on desktop: worth porting the block cache to `react-markdown`
   (memoized block components), or is React reconciliation + B1's gating
   enough there? Measure first.

## Related

- [agent-transcript-redesign.md](agent-transcript-redesign.md) — parent
  plan; P0–P4 shipped, §P5 bucket.
- [debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md) — J3
  Inspect tab (name collision guard).
- Upstream: `MoonshotAI/kimi-code` — `apps/vis` (issues rule set),
  `apps/kimi-inspect` (audit trail), `apps/kimi-web` + `markstream-vue`
  (streaming markdown), all MIT.
