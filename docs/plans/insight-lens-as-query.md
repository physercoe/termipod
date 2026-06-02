# Insight lens as a server-side keyset query

> **Type:** plan
> **Status:** Proposed (2026-06-02) — implements
> [ADR-039](../decisions/039-insight-lens-as-server-query.md). Not started.
> Phases gate on the director's device-test (Flutter is untestable locally);
> the hub side is locally testable Go.
> **Audience:** contributors
> **Last verified vs code:** v1.0.789

**TL;DR.** Turn each filtered Insight lens (Errors / Turns / Text / Tools) into
its **own server-side keyset-paged list** instead of a client-side filter over
the live Feed's tail-anchored window. Fixes the empty-filtered-view dead-end
(Issue 1) and the scroll-up jump-to-end/top (Issue 2) by construction
([ADR-039](../decisions/039-insight-lens-as-server-query.md)). The live Feed
and the Insight **All** view are untouched (gate-for-no-regression). The hub
already exposes the needed surface for kind-based lenses; only the
full-fidelity Errors keyset is net-new and is deferred.

## Goal & non-goals

- **Goal.** A filtered Insight lens renders the lens itself — paged from the
  server by the lens predicate — so it is never empty-while-matches-exist,
  scrolls to load more of its own kind, and never jumps to the end/top.
- **Goal.** The funnel `N/M` + stepper read the lens list's own index
  (one list, one cursor).
- **Non-goal (separable follow-on).** Monotonic minimap tick *positioning* and
  drag-scrub (needs the dense per-session ordinal / time-scale —
  `transcript-paging` §§4–5). Minimap **tap-to-locate** is in scope (it routes
  into the lens list); minimap tick *position math* is not.
- **Non-goal.** Any change to the live Feed loader, SSE tail-follow, or the
  Insight All view.

## Current state (grounded)

- `lensed = _events.where(matchesLens)` — the rendered filtered list is a
  projection of the loaded window (`lib/widgets/agent_feed.dart:2232`).
- Empty-state at `:2629` ("scroll up to load older") is unreachable: an empty
  list has no scroll extent, and the load-older trigger is `pixels <= 120`
  (`_onScroll`, `:684`).
- Tail-follow re-enable (`:665–683`) and filtered-prepend anchoring (`:908`)
  are the two scroll-jump sources.
- Funnel/stepper already read the whole-run digest lists for turns/errors
  (`funnelUsesRunList`, `runTurnSeqs` / `runErrorSeqs`) — so the **counts** are
  already whole-run; only the **rendered list** lags.
- Hub: `GET …/events?kind=<set>` + `(ts,seq)` keyset
  (`handlers_agent_events.go:225–342`); `…/turns`; `…/digest` all exist.
- `RandomAccessLoader` (`lib/widgets/agent_feed/random_access_loader.dart`)
  already encapsulates the `(ts,seq)` keyset fetch shape — the natural seam to
  generalize per-lens.

## Phases

### P1 — kind-based lenses as their own paged lists (Text / Tools / Turns)

The lenses whose predicate is a `kind` set; no hub change.

- Generalize `RandomAccessLoader` (or add a sibling `LensQueryLoader`) to fetch
  a lens page via `kind=<set>` + `(ts,seq)` keyset — `fetchLensAround(anchor)`,
  `fetchLensOlder` / `fetchLensNewer`. Pure, unit-tested with a fake fetcher
  (mirrors the existing `agent_feed_random_access_loader_test.dart`).
- In `AgentFeed`, when `randomAccess && lens != All`, render from a **per-lens
  buffer** fed by that loader instead of `_events.where(...)`. The All view and
  the live Feed keep `_events`.
- Funnel `N/M` + the unified stepper read the lens buffer's index. Whole-run
  `M`: Turns / Errors / Tools use the exact digest total (already wired); **Text
  uses a loaded-lens-window `M`** for MVP (no digest text count — ADR-039 open
  question 2; exact Text `M` is a tracked follow-on). Lens entry lands on the
  newest match by default; load-older pages the lens, never the mixed window.
- Verify the kind sets against `agentEventMatchesLens`
  (`feed_reducer.dart`) so the server query ≡ the old client predicate
  (Text = `text`,`thought`; Tools = `tool_call`,`tool_result`; Turns =
  `turn.start` / the turn index).
- **Device-test gate:** Text/Tools/Turns lenses (a) never empty while the
  digest count > 0, (b) scroll-up loads more of that kind, (c) no jump to
  end/top, (d) funnel `N/M` + stepper agree and move together.

### P2 — Errors lens + minimap tap-to-locate

- **Errors list (MVP):** render the run's error list from the digest's
  whole-run error sample seqs (`runErrorSeqs`, already carried with ts via the
  v1.0.785 `runAnchorTs` work) — fetch those events by `(ts,seq)` and page the
  list. The count stays whole-run from the digest; the list is the samples.
- **Minimap tap → lens seek:** a landmark tap resolves to "match #k in the
  active lens list" and seeks there (load the lens page around k) — exact
  landing, no offset convergence. Reuses the P1 lens loader.
- **Device-test gate:** Errors lens lists the run's errors and pages them;
  minimap tap lands the exact landmark card in the viewport.

### P3 (follow-on) — full-fidelity Errors keyset + monotonic minimap

- **Server `error=true` keyset** on `GET …/events` — **SHIPPED (P3a).** Scans
  candidate-kind rows in (ts,seq)-keyset batches and filters each with the same
  `canonicalErrorClass` the digest fold uses (no SQL/Go divergence), so the
  endpoint lists **every** error at any depth. Go-tested
  (`handlers_agent_events_errors_test.go`). **Remaining (P3b):** rewire the
  mobile Errors lens to page this endpoint instead of the digest samples,
  lifting the 200-cap in the UI.
- **Monotonic minimap tick position + drag-scrub** via the dense per-session
  ordinal / time-scale (`transcript-paging` §§4–5). Separable; only if device
  testing shows the overview scrubber still wants exact positioning.

## Risks

- **Errors fidelity until P3** — the MVP Errors list is bounded by digest
  sample completeness (ADR-039 open question). Mitigation: count stays
  whole-run; P3 closes it.
- **Live agent on a lens** — a still-running agent's lens is a snapshot;
  decide the refresh affordance (manual "load newer" vs. snapshot-on-entry).
  Default: snapshot on entry, All view keeps tailing.
- **Loader seam** — generalizing `RandomAccessLoader` must not perturb the
  existing random-access window reset (anchor-near-top, v1.0.789+). Keep the
  lens loader a sibling path; unit-test both.

## Related

- [ADR-039](../decisions/039-insight-lens-as-server-query.md) — the decision.
- [ADR-038](../decisions/038-per-run-event-digest.md) — the digest/turn-index
  data model consumed here.
- [`discussions/transcript-paging-vs-forum-model.md`](../discussions/transcript-paging-vs-forum-model.md),
  [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md).
- [`plans/agent-run-analysis-mode.md`](agent-run-analysis-mode.md) — the
  Insight surface this navigates.
