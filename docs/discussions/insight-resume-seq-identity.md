---
name: Insight navigator lands on the wrong row after a resume — seq is agent-unique, not session-unique
description: The director reported that after resuming an agent and running several more turns, the Insight Navigator (Turns / Errors outline) jumps to the wrong position. This doc traces it to a load-bearing identity assumption — the Insight transcript is session-scoped but keys every event anchor on the bare per-agent `seq`, while a resumed session spans multiple agents whose seq ranges overlap (each restarts near 1). So a turn/error anchor at `seq=N` can land on a different agent's `seq=N`. Distinct from insight-navigation-fixed-pages.md (landing *precision*); this is event *identity*. Lays out the fix options — compound `(agent_id, seq)` anchor identity (mobile-mostly), the global event `id` (needs hub plumbing), or a server-supplied dense session ordinal — mapped to file:line, with a recommendation and the one hub gap (error samples carry no agent_id).
---

# Insight navigator lands on the wrong row after a resume — seq is agent-unique, not session-unique

> **Type:** discussion
> **Status:** Open (2026-06-04) — raised by the director: "when I resume an
> agent and view the Insight page after several turns, the Navigator (error,
> turn) seems to jump to the wrong position." Reproduced in the code as an
> identity defect, not a landing-precision one.
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** `agent_events.seq` is **per-agent** (`COALESCE(MAX(seq),0)+1 …
WHERE agent_id = ?`, unique key `(agent_id, seq)`). A **resumed session spans
multiple agents** — resume mints a *new* `agent_id` but keeps the *same*
`session_id` — so the two agents' seq ranges **overlap**, each restarting near
1. The Insight transcript, however, is **session-scoped** (it loads and
renders the whole session across agents) yet identifies every event anchor by
the **bare integer `seq`**: the Navigator's `runTurnSeqs` / `runErrorSeqs` /
`runAnchorTs` maps are keyed by `seq`, and `_seqIsLoaded` / `_jumpToContext`
match by `seq`. Once a session contains two agents, a turn/error anchor at
`seq=N` resolves to **whichever agent's `seq=N` is encountered first** — the
wrong row. The disambiguator (`agent_id`) already exists on every event and
every turn row; the fix is to make the **anchor identity compound**
(`(agent_id, seq)`), with one small hub gap on the errors side. This is the
event-*identity* sibling of
[`insight-navigation-fixed-pages.md`](insight-navigation-fixed-pages.md)
(which is about landing *precision*) and rests on the same substrate concern
as [`transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
(a dense session ordinal): both observe that **per-agent `seq` is not a sound
session-level coordinate**.

---

## 1. The symptom, reproduced in the code

After a resume, tapping a row in the right **Navigator** drawer (Turns or
Errors outline) lands the transcript on a *different* turn/error than the one
tapped. It does not happen on a fresh, never-resumed run. The path:

**Anchors are built keyed by bare `seq`.** The Navigator's whole-run outline
comes from the digest + turn index:

- Turn anchors — `runTurnSeqs.add(seq)` and `runAnchorTs[seq] = start_ts`,
  where `seq = r['start_seq']` for each turn row
  (`lib/widgets/session_analysis_view.dart:176-183`).
- Error anchors — `runErrorSeqs.add(seq)`, `runErrorClasses[seq]`,
  `runErrorLabels[seq]`, `runAnchorTs[seq]`, where `seq` is each error class's
  `sample_seqs[i]` (`lib/widgets/session_analysis_view.dart:145-171`).

Every one of these is a `Map<int, …>` or a `List<int>` **keyed on `seq`
alone**. `runAnchorTs` is the clearest tell: it is a `Map<int, String>`, so if
two agents in the session each have a turn whose `start_seq` is `5`, the second
**overwrites** the first's timestamp — there is no second agent's entry at all.

**Landing matches by bare `seq`.** A Navigator tap calls
`_jumpFromOutline(seq, ts)` → `_handleExternalSeek`
(`lib/widgets/insight_transcript.dart:1084`, `797`), which routes:

- `if (_seqIsLoaded(seq)) _landOnSeq(seq)` — and
  `_seqIsLoaded(seq) => _events.any((e) => e['seq'] == seq)`
  (`insight_transcript.dart:827-828`). This matches the **first** loaded event
  with that seq, which after a resume may be the *other* agent's row.
- else `_resetWindowAround(seq, ts)` — the `(ts, seq)` keyset fetch is fine for
  *fetching* the right window (ts disambiguates), but the subsequent
  `_jumpToContext(seq)` again resolves the landing **row** by bare seq.

So even when the fetch is correct, the *landing* can pick the wrong agent's
same-seq row that happens to be in the loaded window.

## 2. Why `seq` is not a session-level coordinate

**`seq` is per-agent.** Events are inserted with
`COALESCE(MAX(seq), 0) + 1 … FROM agent_events WHERE agent_id = ?`
(`hub/internal/server/handlers_agent_events.go:117-122`), and the table's
uniqueness is `(agent_id, seq)` — *not* `seq` alone. The first event of every
agent is `seq = 1`.

**A session spans multiple agents.** Resume respawns the agent: it mints a
**new `agent_id`** while preserving the **same `session_id`**
(`carryModeModelStateAcrossResume(priorAgentID, newAgentID)`,
`hub/internal/server/handlers_sessions.go:884`; the session is the primitive
that "survives respawn", per the glossary). So a resumed session contains
events from two (or more) agent_ids, whose seq ranges **both start at 1 and
overlap**.

**The hub already knows this.** The session-scoped event list explicitly orders
by `ts, agent_id, seq` and comments: *"Use ts because seq is per-agent and a
session can span multiple agents (resume)"*
(`hub/internal/server/handlers_agent_events.go:320-325`). The server is
careful; the **mobile client is the layer that collapses `(agent_id, seq)` to
`seq`**.

**The Insight surface is session-scoped.** The digest and turn providers are
keyed by `_sessionId` (`session_analysis_view.dart:117-118`,
`sessionDigestProvider(_sessionId)` / `sessionTurnsProvider(_sessionId)`), and
the turns endpoint aggregates **across all agents in the session**
(`SELECT DISTINCT … WHERE t.agent_id IN (SELECT DISTINCT agent_id FROM
agent_events WHERE session_id = ?) ORDER BY t.start_ts, t.agent_id, t.idx`,
`hub/internal/server/handlers_agent_turns.go:164-170`). So the outline
*intentionally* mixes agents — which is the right UX (one continuous
conversation) — but then anchors them by a coordinate that is only unique
*within* an agent.

## 3. The disambiguator already exists

The fix does not need new data on the turns side:

- Each turn row already carries `agent_id` (`AgentID string \`json:"agent_id"\``,
  `hub/internal/server/handlers_agent_turns.go:25`; `turnCols` selects it,
  `:144`).
- Each event map already carries `agent_id` (the list `cols` include it,
  `handlers_agent_events.go:295`), so `_events` rows can be matched on
  `(agent_id, seq)`.

The **one gap** is the errors side: the digest's per-class error samples carry
`sample_seqs` + `sample_ts` + `sample_labels` but **no `sample_agent_ids`**
(`hub/internal/server/digest_fold.go:80-99`). So error anchors cannot be made
compound without a small hub addition (see §4, option A's footnote).

## 4. Options

### A. Compound anchor identity `(agent_id, seq)` — recommended

Thread `agent_id` alongside `seq` everywhere an anchor is built or matched:

- `session_analysis_view.dart` — change the anchor collections from
  `seq`-keyed to `(agent_id, seq)`-keyed (e.g. a small `AnchorKey` value type,
  or a `String` key `"$agentId:$seq"`). Turn rows already supply `agent_id`.
- `insight_transcript.dart` — `_seqIsLoaded` / `_jumpToContext` /
  `_landOnSeq` / `runAnchorTs` lookups match on `(agent_id, seq)`.

**Scope.** Turns: mobile-only, fully fixable today. Errors: needs the hub to
emit `sample_agent_ids` (1:1 with `sample_seqs`, exactly as `sample_ts` was
added — `digest_fold.go` `addSampleTS` is the template) plus a digest schema
bump + refold. Until then the Errors navigator can fall back to ts-keyed
landing (already mostly correct because post-resume errors have later ts).

**Pros.** Minimal, surgical, matches the hub's own `(agent_id, seq)`
uniqueness; no new global identifiers. **Cons.** Touches several call sites;
the errors half needs a hub change to be exact.

### B. Anchor on the global event `id`

Every event has a globally-unique `id` (ULID). Carry `id` as the anchor
identity instead of `seq`. **Cons.** Turn rows expose `start_seq`, not the
event id, and error samples expose `sample_seqs`, not ids — so this needs hub
plumbing on *both* sides (more than A), and the RA keyset is `(ts, seq)`, not
id, so fetching still needs seq/ts anyway. Strictly more work than A for the
same correctness.

### C. Server-supplied dense **session** ordinal

Have the hub assign a per-session monotonic ordinal (dense across agents) and
key the Navigator on that. This is the keystone
[`transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
and [`insight-navigation-fixed-pages.md`](insight-navigation-fixed-pages.md)
§10 already argue for, and it would *also* fix landing precision and the "N of
M" pill. **Cons.** Much larger (a new server-side coordinate, backfill,
every anchor/keyset migrated). Right long-term direction, wrong size for *this*
bug. A and C are not mutually exclusive — A is the correctness floor; C is the
eventual model that subsumes it.

## 5. Recommendation

Do **A**, in two steps: (1) mobile-only compound `(agent_id, seq)` for the
**Turns** navigator (closes the reported bug for the common case), then (2) the
hub `sample_agent_ids` addition for the **Errors** navigator. Treat **C** as
the tracked long-term convergence (it subsumes both this identity fix and the
landing-precision work), not a prerequisite. The mobile changes are CI-only
verifiable (no local Flutter), so they should land gated/narrow and lean on the
existing Insight device-test loop.

## 6. Open questions

1. Key shape for A — a typed `AnchorKey(agentId, seq)` vs a `"$agentId:$seq"`
   string. The string is cheaper to thread through the existing `Map<int,…>`
   sites; the type is safer. Lean string for step 1, revisit if it spreads.
2. Should the Navigator outline **visually group by agent** across a resume
   boundary (a divider / "resumed" marker), or stay a single flat list? Out of
   scope for the bug fix, but the resume boundary is now a real seam the UI
   could surface.
3. Does the same bare-`seq` assumption leak into the **live** feed
   (`live_feed.dart`)? Live tail is single-agent per window in practice, but
   the `_jumpToContext` / context-jump path shares the pattern — worth an audit
   when A lands.
