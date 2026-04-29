# Frame profiles migration — retire hardcoded `translate()`

> **Type:** plan
> **Status:** Proposed (2026-04-29)
> **Audience:** contributors
> **Last verified vs code:** v1.0.328

**TL;DR.** Implements ADR-010 (frame profiles as data). Three phases:
**(1)** loader + evaluator + claude-code profile authored to match
today's `translate()` exactly, parity-tested against a recorded
corpus, behind a `frame_translator: legacy` default. **(2)** Flip
default to `both` (parallel run, profile output writes), then
`profile`. Legacy stays as emergency escape. **(3)** Author codex /
gemini-cli profiles; delete legacy translator. Total: ~3
wedges, ~2 weeks at current pace; bulk of effort is the parity
corpus, not the code.

---

## 1. Why this plan

ADR-010 commits to data-driven frame translation. Without a
phased migration:

- Authoring profiles blind risks subtle field-rename bugs that only
  surface on real device traffic — a rule that returns nil instead
  of a string silently empties a UI tile.
- A hard switch deletes the legacy code before we've confirmed
  parity. If a single rule is wrong, real `agent_events` rows are
  written incorrectly, and we can't retroactively re-translate the
  raw frames (we don't keep them).
- Multiple engines on day one means multiplying the unknowns. Land
  claude-code first because we already know its shape from the
  v1.0.326 / v1.0.328 fixes; codex / gemini-cli wait for a green
  baseline.

## 2. Decisions

**D1. Parity is corpus-driven, not unit-test-driven.**
The test fixture is a recorded SSE stream — actual frames captured
from a live claude-code session — replayed through both translators
with output diffed by `agent_events` row. Synthetic unit tests miss
the real shape drift; replay catches it.

**D2. Profile authoring lives in `hub/internal/hostrunner/profiles/<family>/`
during development, then moves to `agent_families.yaml` for ship.**
Keeping the development copy as a separate file gives a faster edit
loop than re-loading the embedded YAML each run. Pre-merge, the
profile gets folded into `agent_families.yaml`'s `frame_profile`
block.

**D3. Mobile-side decorative knowledge ships in Phase 4.**
Per ADR-010, the profile carries `display_labels` and similar mobile
hints. That migration is independent — Phase 1–3 only touch the
host-runner. Mobile keeps its current `_humanWindow` / model-name
shorteners until Phase 4.

---

## 3. Phase 1 — loader + evaluator + claude-code parity (Wedge 1)

**Goal:** A claude-code profile that produces byte-identical
`agent_events` rows compared to today's `translate()` over a recorded
frame corpus.

### 3.1 Schema additions (`hub/internal/agentfamilies`)

- Extend `Family` with `FrameProfile *FrameProfile` field.
- New types: `FrameProfile { ProfileVersion int; Rules []Rule }`,
  `Rule { Match map[string]any; ForEach string; Emit Emit }`,
  `Emit { Kind string; Producer string; Payload map[string]string }`.
- YAML round-trip test: round-trip an example profile through
  `yaml.Marshal/Unmarshal` and assert structural equality.

### 3.2 Expression evaluator (`hub/internal/hostrunner/profile_eval/`)

New package, ~300 LoC. Pure functions, no driver coupling:

```go
// Eval evaluates an expression against `frame` (top of stack) and
// `outer` (parent during for_each), returning the resolved value or
// nil. The expression vocabulary is fixed:
//   - $.a.b.c        path access (dotted)
//   - $.a[0]         indexed array access
//   - $$.x           outer-scope path
//   - "literal"      string literal
//   - a || b || "x"  coalesce (first non-nil), trailing literal allowed
func Eval(expr string, frame, outer map[string]any) any
```

Tests cover: nil propagation, missing keys, the `$$` outer scope
during `for_each`, coalesce with three or more terms, mixed path
+ literal coalesce, malformed expressions (return nil + log).

### 3.3 Profile-driven translator (`hub/internal/hostrunner/driver_profile.go`)

New file alongside `driver_stdio.go`. Same `translate()` signature
but reads rules from `Family.FrameProfile.Rules`:

```go
func (d *StdioDriver) translateViaProfile(ctx context.Context, frame map[string]any) {
  for _, rule := range d.Profile.Rules {
    if !matchesAll(rule.Match, frame) {
      continue
    }
    if rule.ForEach != "" {
      walk := profile_eval.Eval(rule.ForEach, frame, nil)
      // …iterate, emit per item with frame as outer scope
    } else {
      payload := buildPayload(rule.Emit.Payload, frame, nil)
      d.Poster.PostAgentEvent(ctx, d.AgentID, rule.Emit.Kind, rule.Emit.Producer, payload)
    }
    return // first match wins; subsequent rules don't fire
  }
  // No match → existing raw fallback (D5).
  d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", frame)
}
```

### 3.4 Author claude-code profile + agent-readability artifacts

Translate today's `translate()` into rules. Estimated ~12 rules
covering `system.init`, `system.subtype=rate_limit_event`,
`system.subtype=task_*`, `assistant.message.content[].type=text`,
`assistant.message.content[].type=tool_use`, the standalone `usage`
emit, `user.message.content[].type=tool_result`,
`rate_limit_event` (top-level, three shape variants),
`result` (`turn.result` + `completion`), `error`, raw fallback.

**Agent-native deliverables.** The system is primarily maintained by
AI agents (steward + worker engines per ADR-005), not humans —
authoring artifacts target agent-readability first. Concretely:

- **`description:` field at the FrameProfile root.** ~3 lines stating
  dispatch semantics + scope conventions inline so an agent editing
  rule 17 sees the model without grep'ing the implementation.
- **`docs/reference/frame-profiles.md`** (REFERENCE primitive). The
  agent-facing how-to-author-a-profile doc. Contents: grammar in
  BNF, dispatch semantics, scope rules (`$.` vs `$$.`), 3–5 worked
  input→output examples mirroring `translate()`'s real cases (rate
  limit shape variants, assistant multi-emit, system subtype
  hierarchy), and a "common pitfalls" section calling out
  divergences from JSONata (`$$.` for outer scope, `||` only
  short-circuits on `nil`).
- **JSON Schema sidecar** at `hub/internal/agentfamilies/agent_families.schema.json`.
  Generated from or hand-aligned with the Go struct tags. Editor
  LSPs (and AI editors) get autocomplete + inline validation. Add a
  smoke test that the embedded YAML validates against its own
  schema.
- **Inline `# ` comments per rule** documenting which SDK shape it
  was authored against (`# v1.0.328 — handles nested rate_limit_info
  shape; see docs/changelog.md`). The git-blame-of-the-upstream so
  agents extending later have context.
- **Profile validator subcommand** (`hub-server profile validate
  <file>`). Round-trip + dry-run-against-included-corpus. Errors
  reference rule index + line for actionable agent feedback. Phase 3
  could elevate this to a dedicated `termipod-hub` CLI; Phase 1
  ships it as a hub-server subcommand to keep the surface small.

The 5 deliverables collectively answer "would a fresh AI agent
get profile authoring right from a 30-second read?" Without them
the dispatch model is invisible (rule 17 doesn't see rule 5);
with them, the agent's edit-validate-fix loop is tight.

### 3.5 Parity test corpus

- Capture a recorded SSE stream from a live claude-code session
  exercising: session start, text streaming, tool_use + tool_result
  pairing, rate-limit emission (all three shapes), task subagent
  spawn, `result` end-of-turn, error frame.
- Store as a JSONL fixture under `hub/internal/hostrunner/testdata/profiles/claude-code/`.
- New test `TestProfile_ClaudeCodeMatchesLegacy` runs both
  translators against every frame in the corpus, diffs the resulting
  `agent_events` rows by `(kind, producer, payload)`, fails on any
  divergence.

### 3.6 Wiring

- `agent_families.yaml` claude-code entry gets the `frame_profile`
  block. Embedded fixture matches the corpus output.
- `frame_translator: legacy` is the default for v1; nobody is
  affected yet.
- Operators can flip to `both` per family for opt-in canary testing.

**Wedge ships when:** parity test green on the corpus, legacy
default unchanged on the user's device.

---

## 4. Phase 2 — flip default to profile (Wedge 2)

**Goal:** Profile-driven translation is the default for claude-code;
legacy stays as emergency escape.

### 4.1 Add the divergence metric

- New audit-row kind `frame_translator_diff` recording (frame_type,
  divergence_summary, raw_frame_bytes). Bounded ring (last 100 per
  agent) to avoid bloat.
- Mobile gets a debug-overlay surface that shows the count + last few
  divergences. Operator-only.

### 4.2 Stage rollout

- Release N+1: default `frame_translator: both` for claude-code.
  Profile output writes to DB; legacy runs in shadow mode and logs
  divergences. Mobile pill turns red if the divergence count is
  non-zero on this device.
- Release N+2: default `frame_translator: profile`. Legacy stays
  available via flag.
- Release N+3: delete legacy translator code. `agent_families.yaml`
  drops the `frame_translator` toggle (it had only one valid value
  by then).

**Wedge ships when:** divergence rate at zero for 7 days on the
operator's device fleet AND no field complaints during the canary.

---

## 5. Phase 3 — multi-engine (Wedge 3)

**Goal:** codex and gemini-cli both spawn through the
profile-driven path with no code changes. Aider was on the roadmap
as a fourth target but retired 2026-04-29 (project decision: only
cover dominant-vendor products).

### 5.1 Per-engine profile authoring

For each engine:
- Capture a recorded SSE corpus the same way as Phase 1.
- Author the profile against the corpus.
- Add a `TestProfile_<engine>Corpus` test that asserts the corpus
  produces the expected `agent_events` shapes (no legacy comparison
  — there's no legacy translator for these).

Sequencing: codex → gemini-cli, in order of likely user demand.

### 5.2 Engine-specific quirks expected

- **codex** likely uses different `usage` field naming. Profile
  handles via coalesce.
- **gemini-cli** may emit thoughts as separate frames. Profile rule
  emits `kind=thought` instead of `kind=text` for those.
**Wedge ships when:** both engines have green corpus tests and
the demo passes against at least one non-claude-code engine.

---

## 6. Phase 4 — mobile decorative knowledge (Wedge 4, post-MVP)

**Goal:** Move `_humanWindow`, model-name shorteners, tool icons
out of Dart and into the profile, served to mobile via the
`/agent_families` API.

Independent of Phases 1–3; ships when those land green and there's
appetite. Per ADR-010 §5, interactive widgets (AskUserQuestion etc.)
stay in Dart but key off a `tool_widgets:` registry in the profile.

---

## 7. Risks + open questions

- **Corpus completeness.** A frame shape that's rare but real (a
  particular error subtype, a specific cache-miss reason) might not
  appear in our recorded corpus. Mitigation: keep the corpus
  growing — every device-walkthrough finding that touches
  translation gets a recorded fixture added.
- **Hot-reload race.** `Invalidate()` re-reads on overlay change,
  but an in-flight agent's driver holds a snapshot. Mitigation:
  resolved by re-reading on next-frame translate, not by
  interrupting the in-flight one. Still: document explicitly.
- **Subset DSL escape hatch.** If a Phase 3 engine needs an
  expression we can't express in the subset, the answer is "extend
  the subset minimally, or fall back to legacy translator for that
  engine while we revisit ADR." We do **not** sneak Go-side translator
  helpers back in.
- **Profile authoring tooling.** v1 ships YAML-by-hand; a profile
  validator + a "test against captured frame" CLI are nice-to-haves
  for Phase 3 if profile authoring becomes routine.

---

## 8. References

- ADR: `../decisions/010-frame-profiles-as-data.md`
- Discussion that fed this plan:
  `../discussions/multi-engine-frame-parsing.md`
- Existing overlay infrastructure:
  `hub/internal/agentfamilies/families.go`
- Current coupling we're retiring:
  `hub/internal/hostrunner/driver_stdio.go::translate()`
- Recent SDK churn this avoids in future:
  - v1.0.326 — `system.subtype=rate_limit_event` branch
  - v1.0.328 — nested `rate_limit_info`
