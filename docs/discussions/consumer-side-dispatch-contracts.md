---
name: Consumer-side dispatch contracts
description: Four mobile-side bugs in 48h all reduced to the same shape — producer emits a new event kind; consumer's per-kind dispatch (busy-inference skip-set, chip-strip reducer, etc.) doesn't see it; default behaviour is wrong. The producer-side sweep memory (feedback_engine_dispatch_sweep) catches instances *once they fire on device*. This doc reframes the recurrence as a structural design gap and proposes three candidate fixes — allowlist-over-denylist for busy-inference, contract-tests for the chip dispatch, and a cross-cutting kind registry — with a recommendation on what to ship first.
---

# Consumer-side dispatch contracts

> **Type:** discussion
> **Status:** Open (2026-05-26) — captures the pattern across v1.0.667 / v1.0.699 / v1.0.717 / v1.0.720, sketches three structural fixes, and identifies the smallest one as worth shipping. No code in this commit; the recommendation lands in a follow-up plan if accepted.
> **Audience:** contributors
> **Last verified vs code:** v1.0.720 (mobile `lib/widgets/agent_feed.dart` — three per-kind dispatch sites currently live: `kAgentBusyInferenceSkipKinds` constant at `:96`, `kAgentFeedAlwaysHiddenKinds` at `:74`, the chip-strip reducer at `:1600`). Cross-references the four bug commits + their changelog entries (v1.0.667 / v1.0.699 / v1.0.717 / v1.0.720).

## 1. The pattern

Four bugs in 48 hours all reduced to the same shape:

| Ship | Producer | Consumer | Default for unknown kind |
|---|---|---|---|
| v1.0.667 | codex emits `usage` per-message | mobile `_isAgentBusy` | "busy" (cancel button pinned) |
| v1.0.699 | claude-code emits `status_line` on cold open | mobile `_isAgentBusy` | "busy" |
| v1.0.717 | codex emits `raw` post-resume (forward-compat catch-all from profile, see `agent_families.yaml:782-787`) | mobile `_isAgentBusy` | "busy" |
| v1.0.720 | antigravity emits `status_line` (NO `usage` events; transcript carries no token counts) | mobile chip-strip reducer | "ignore" → chips stay blank |

In each case:

1. The producer adds a new event kind (or carries existing telemetry on a new path).
2. The consumer's per-kind dispatch — a switch, an if-else chain, or a set membership test — doesn't enumerate the new kind.
3. **The default branch of the dispatch is wrong for this kind** (busy when the kind is telemetry-only; ignore when the kind carries data the consumer needs).
4. The bug is invisible at code-review time because the producer's tests pass and the consumer's tests pass — they're scoped to the kinds each side already knows about.
5. End-to-end smoke is the only signal. The fix is local: append the missing kind to the dispatch. The class recurs the next time *either side* adds a kind.

We have a producer-side discipline written down after v1.0.714's
300-tile leak + v1.0.716's 3-stage resume break: *when you add a new
engine to a dispatch, sweep all sites the data traverses.* That
discipline was already in place when v1.0.720 landed and didn't
prevent it. **The v1.0.720 dispatch site wasn't on any
engine-grep because nothing in `agent_feed.dart` literally names an
engine** — the chip-strip reducer's miss was about a *kind*
(`status_line`), not an engine. The producer-side discipline catches
engine-keyed dispatches; this doc covers the second class.

## 2. The three dispatch shapes currently live

`lib/widgets/agent_feed.dart` is the mobile event-consumer entry
point. Three dispatch sites operate on event kinds; their defaults
differ:

### 2.1 `kAgentBusyInferenceSkipKinds` (set, default = busy)

```dart
const kAgentBusyInferenceSkipKinds = <String>{
  'usage', 'rate_limit', 'status_line', 'raw',
};
// _isAgentBusy walks events newest-first, skips these, returns true
// for any other agent-produced kind → "busy".
```

Default is **busy** for unknown kinds. Every producer that emits a
new pre-turn-active kind has to be added here. The four bugs above
are the four times this didn't happen. The set is currently 4 entries
deep; each came from a separate on-device incident.

### 2.2 `kAgentFeedAlwaysHiddenKinds` (set, default = render)

```dart
const kAgentFeedAlwaysHiddenKinds = <String>{
  'session.init', 'usage', 'rate_limit', 'status_line',
};
// Anything not in this set renders as a transcript bubble.
```

Default is **render** for unknown kinds. The risk here is the
opposite: a new pure-telemetry kind that we don't hide will render as
a noisy JSON card in the feed. (v1.0.699's cold-open `status_line`
was *also* an instance of this — we added `status_line` to both sets
in the same commit.)

### 2.3 Chip-strip reducer (if-else chain, default = ignore)

```dart
for (final e in _events) {
  final kind = (e['kind'] ?? '').toString();
  if (kind == 'turn.result') { ... }
  else if (kind == 'rate_limit') { ... }
  else if (kind == 'usage' && _isCumulativeUsage(p)) { ... }    // codex
  else if (kind == 'usage') { ... }                              // claude-code
  else if (kind == 'status_line') { ... }                        // v1.0.720 — antigravity
}
```

Default is **ignore** for unknown kinds. v1.0.720 was this class —
antigravity's `status_line` payloads existed but never reached the
chip state. Adding the new branch was the fix.

## 3. Why local fixes haven't held

Each of the four fixes added one entry / one branch and shipped.
None addressed the structural question: **what is the contract
between event kinds and the consumer's default behaviour?**

The pattern's expense is real:

- 4 untagged sub-commits across the day (v1.0.667 / 699 / 717 / 720) all
  fit the "post-mortem add-one-line" shape.
- Three of them shipped a Cancel button that didn't cancel (UX
  smell — user taps, nothing happens). v1.0.720 was the inverse
  (chips silently missing).
- Each was diagnosed *only* by inspecting the live DB. Unit tests
  didn't catch any of them.

Counter-bias to watch: **"we just need one more entry"** thinking
masks the fact that we don't have a contract. The next bug will
shape-shift again — a new engine, a new event kind, a new dispatch
site — and we'll add one more line and ship. Five lines from now we
still won't have the contract.

## 4. Three candidate structural fixes

Ordered by scope, smallest first.

### Fix A — Allowlist over denylist for `_isAgentBusy` (~30 LOC + ~10 tests)

**The shape change.** Replace `kAgentBusyInferenceSkipKinds` with
`kAgentTurnActiveKinds` — the set of kinds that *signal turn-
active*. Inverts the default: unknown kinds → idle. New telemetry
kinds (the historical recurrence) cost zero because they're not in
the active set.

```dart
@visibleForTesting
const kAgentTurnActiveKinds = <String>{
  'text', 'tool_call', 'thought', 'plan',
  // Streaming kinds, partials, etc.
};

bool _isAgentBusy() {
  for (final e in _events.reversed) {
    if ((e['producer'] ?? '').toString() == 'user') continue;
    final kind = (e['kind'] ?? '').toString();
    // Terminal kinds short-circuit to idle (preserved from current
    // shape).
    if (kind == 'turn.result' || kind == 'session.init' ||
        kind == 'completion') return false;
    if (kind == 'lifecycle') {
      final phase = ((e['payload'] as Map?)?['phase'] ?? '').toString();
      if (phase == 'exited' || phase == 'stopped') return false;
      continue;
    }
    if (kAgentTurnActiveKinds.contains(kind)) return true;
    // Anything else — known telemetry, unknown future kinds, raw
    // catch-all — does not signal busy. Continue scanning.
  }
  return false;
}
```

**Trade-off.** A *real* turn-active kind missing from the allowlist
makes a busy agent look idle. But "appears idle while busy" is less
bad than "stuck cancel button" — the user just sends another prompt
or the next text/tool_call event pushes inference back to busy
within a tick.

**What it costs.** ~30 LOC + a contract test that enumerates every
kind the codebase emits (grep `PostAgentEvent.*"<kind>"`) and
asserts each is either in `kAgentTurnActiveKinds`, in the terminal-
kinds switch, or explicitly ignored (with a rationale comment).
Failing test = a producer added a kind the consumer hasn't
classified.

**Class-coverage.** Catches v1.0.667 / 699 / 717 directly (all three
denylist additions become unnecessary). v1.0.720 is a different
dispatch site — Fix A doesn't help there.

### Fix B — Contract test for the chip-strip reducer (~50 LOC + ~5 tests)

**The shape change.** No production code change. Add a test in
`test/widgets/` that:

1. Enumerates every `kind` that any engine in `agent_families.yaml`
   profile rules emits, plus every hardcoded `PostAgentEvent` call
   in `hub/` (grep + parse).
2. For each kind, asserts either:
   a. The chip-strip reducer has an explicit branch handling it, or
   b. The kind appears in `kAgentFeedAlwaysHiddenKinds`
      (chip-only, no chip-strip data), or
   c. An allowlist `kKindsIntentionallyIgnoredByChipStrip` carries
      it with a written rationale.

**What it costs.** ~50 LOC of test scaffolding + per-kind
classification one-time. Maintenance: when a producer adds a kind
they must classify it; CI fails the next push otherwise.

**Class-coverage.** Catches v1.0.720 directly. Doesn't help the
busy-inference cases (those need Fix A).

### Fix C — Single source-of-truth kind registry (~200 LOC + bigger)

**The shape change.** A new `lib/event_kinds.dart` (or
`hub/internal/eventkinds/`) holds the canonical kind registry:

```dart
enum EventKindCategory {
  turnActive,      // text, tool_call, thought, plan
  telemetry,       // usage, rate_limit, status_line, raw
  terminal,        // turn.result, completion
  lifecycle,       // lifecycle{phase:started/stopped/exited}
  systemMarker,    // system events
  userInput,       // input.text, input.cancel, etc.
}

const kKindRegistry = <String, EventKindCategory>{
  'text': EventKindCategory.turnActive,
  'tool_call': EventKindCategory.turnActive,
  'usage': EventKindCategory.telemetry,
  'status_line': EventKindCategory.telemetry,
  // ...
};
```

Every consumer dispatches against the category, not the literal
name. Adding a new kind requires updating the registry; consumers
inherit the classification automatically.

**Trade-off.** Heaviest fix; touches every existing dispatch site
(hub-side too — server router, attention dispatcher, audit emitter).
Forces a single classification per kind that may not fit every
consumer's needs (e.g. the chip-strip reducer wants per-engine
sub-classifications inside `telemetry`).

**What it costs.** ~200 LOC of refactor + cross-package coordination
+ a migration window where both old and new dispatch paths coexist.
Realistically a multi-wedge effort.

**Class-coverage.** Catches all four bugs *plus* future hub-side
recurrences (engine_dispatch_sweep memory's class). Strongest
prevention. Highest cost.

## 5. Recommendation

**Ship Fix A + Fix B, defer Fix C.** Rationale:

- **Fix A** addresses 3 of 4 historical bugs at the smallest cost. The
  allowlist inversion is principled (the producer set of turn-active
  kinds is small + stable; the telemetry set grows). The contract test
  added as part of A surfaces a kind being added without
  classification immediately.
- **Fix B** addresses the 4th. The chip-strip reducer's branch-
  dispatch is a different shape (per-kind code, not per-kind set
  membership), so allowlist inversion doesn't apply. A contract test
  enumerating "every kind that emits chip-relevant data must have a
  reducer branch or an explicit ignore" is the right shape.
- **Fix C** is the right *end* state but the wrong *next* step. The
  category enum + registry would force hub-side refactors we don't
  need for the next class of bug we'll see. Revisit if Fix A+B prove
  insufficient over the next 30 days.

## 6. Acceptance criteria for the follow-up wedge

If Fix A+B ships:

1. `kAgentBusyInferenceSkipKinds` is gone. `kAgentTurnActiveKinds`
   replaces it. The four entries in the skip-set become "not in the
   active set" by default.
2. New test `test/widgets/agent_feed_kind_classification_test.dart`
   parses every `PostAgentEvent.*"<kind>"` call in `hub/` + every
   profile-rule `emit.kind` in `agent_families.yaml` and asserts
   each kind is classified in *one* of: turn-active /
   terminal-kinds switch / always-hidden / chip-strip branch /
   explicit-ignore allowlist.
3. CI runs this test on every push. A producer adding a new kind
   without classifying it fails before merge.
4. The four historical fixes are documented in the test fixture as
   regression evidence (verbatim links to v1.0.667 / 699 / 717 / 720
   changelog entries).

## 7. Open questions

1. **Cross-cutting hub-side classification.** Fix A/B are mobile-
   only. The producer-side (hub) doesn't enforce that engine
   profiles classify kinds — `agent_families.yaml` can emit any
   string as `kind`. Should the families schema include a
   `category` field next to each `emit.kind`, so the consumer-side
   classification has an authoritative source? Bigger refactor;
   could be Fix C's first step.

2. **What about hub-side dispatch?** Mobile is one consumer; the hub
   has others (attention router at
   `hub/internal/server/handlers_attention.go`, audit_events at
   `hub/internal/server/handlers_audits.go`, etc.). v1.0.714 and
   v1.0.716 were hub-side instances; Fix A+B don't address those.
   `feedback_engine_dispatch_sweep` already covers producer-side
   sweeps; the symmetric structural fix on the hub-consumer side is
   out of scope for this doc.

3. **Test sustainability.** A grep-the-codebase test for "every
   PostAgentEvent kind literal" depends on contributors using the
   literal form `PostAgentEvent(..., "<kind>", ...)` rather than
   a variable. If a hub-side helper centralises the constants
   (`const kindStatusLine = "status_line"`), the test will need to
   trace through the helper. Defer until the first false-positive.

## 8. References

- Producer-side discipline (the engine-dispatch sweep) lives in
  the project's private memory; this doc is the consumer-side
  complement. Together they cover both halves of the
  "new kind / new engine added; some site misses it" failure class.
- Changelog entries for the four instances:
  - [v1.0.667](../changelog.md#v10667-alpha--2026-05-23) — `usage`
  - [v1.0.699](../changelog.md#v10699-alpha--2026-05-25) — `status_line` cold-open
  - [v1.0.717](../changelog.md#v10717-alpha--2026-05-26) — `raw` on codex resume
  - [v1.0.720](../changelog.md#v10720-alpha--2026-05-26) — `status_line` chip-strip miss
- Code: `lib/widgets/agent_feed.dart:74` (`kAgentFeedAlwaysHiddenKinds`),
  `:96` (`kAgentBusyInferenceSkipKinds`),
  `:1600-1688` (chip-strip reducer),
  `:2002-2047` (`_isAgentBusy`).
- Related: ADR-036 (status_line contract), ADR-010 (frame profiles
  as data; the producer-side discipline).
