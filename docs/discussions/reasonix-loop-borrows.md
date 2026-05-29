# Loop-design borrows from Reasonix

> **Type:** discussion
> **Status:** Open (2026-05-28) — engineering ideas surfaced by a Reasonix code-read; none committed
> **Audience:** contributors
> **Last verified vs code:** v1.0.723
> **Freshness:** snapshot (refresh when Reasonix lands a significant loop-design change or when the borrows themselves ship)

**TL;DR.** [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)
(~12k stars, MIT, TypeScript) is a terminal AI coding agent
**engineered around DeepSeek's prefix-cache invariant**. Its README
claim — *"Cache stability isn't a feature you turn on; it's an
invariant the loop is designed around"* — produces a real engineering
result: 99.82% cache hit on a documented multi-hour session, ~5× cost
reduction ($12 vs $61 without caching). The integration question
("could Reasonix become a 5th TermiPod engine?") is answered in
[integrating-open-source-agents.md §4](integrating-open-source-agents.md)
— M2 driving mode, new profile, days of work. This doc captures the
*separable* question: regardless of whether we ever integrate Reasonix,
**which engineering ideas from its loop apply to TermiPod's existing
four engines?** Four borrows surface. Tier A (build soon): (A1)
surface prompt-cache hit % alongside cents in ADR-036 telemetry
across all caching-capable engines, and (A2) make tool-call repair a
first-class pattern with structured retry. Tier B (borrow with
adaptation): (B1) explicit SEARCH/REPLACE → `/apply` review gate
extending ADR-030, and (B2) bridge between engine-side planning
(`/todo`) and hub-side ADR-029 tasks. None are committed; this doc is
the catalogue before the wedges.

---

## 1. Background — the prefix-cache pillar

Reasonix has three claimed design pillars: (1) **cache-first loop**,
(2) **tool-call repair**, (3) **cost control**. The first is the
load-bearing one and shapes everything else.

DeepSeek's API charges roughly 10× less for cache-hit input tokens
than for cache-miss tokens. Reasonix engineers *every* loop turn —
message construction, tool-result placement, system-prompt position —
to keep the byte-stable prefix valid across turns. The README is
explicit about the discipline: *"Cache stability isn't a feature you
turn on; it's an invariant the loop is designed around."* The
benchmark in `benchmarks/real-world-cache/README.md` claims **435M
input tokens / 99.82% cache hit / ~$12** on a real single-day session,
vs ~$61 without the discipline.

This is a *very* specific optimization that doesn't translate
verbatim — TermiPod is explicitly multi-engine (claude-code, codex,
gemini-cli, kimi-code via [driving modes](../reference/glossary.md#driving-mode)
M1/M2/M4), and each backend's caching contract differs. But the
*principle* — design the loop around what makes the engine cheap to
run — is engine-agnostic and we don't yet apply it.

Three of our existing engines ship some form of prompt caching today:
**claude-code** emits `cache_read_input_tokens` /
`cache_creation_input_tokens` in its `statusLine` payload (the source
[ADR-036 telemetry](../decisions/036-claude-code-statusline-telemetry.md)
reads); **codex** exposes cache statistics via the app-server protocol
in `tokenUsage.last.cached_input_tokens` (used in v1.0.712 for the
context-fill fix); **kimi-code** documents Moonshot Cache discounts.
We capture the cents post-hoc; we don't yet make the cache picture
visible or treat its decay as a recovery surface.

---

## 2. A1 — Prompt-cache hit % alongside cost in ADR-036 telemetry

**What Reasonix does.** Surfaces cache-hit rate as a first-class
metric in its embedded web dashboard alongside token count and
dollar cost. Treats a falling cache-hit rate as a *signal* — long
sessions degrade if the prefix is invalidated by, e.g., tool-result
re-ordering or system-prompt edits.

**What TermiPod does today.** [ADR-036](../decisions/036-claude-code-statusline-telemetry.md)'s
statusLine pipeline captures `cache_read_input_tokens` from
claude-code; codex emits `cached_input_tokens` via `tokenUsage.last`.
The fields are present in `agent_events.usage` payloads. The mobile
cost chip (v1.0.706 three-in-one tile) renders cents only — no cache
component. Long-press tooltip composes process / session / turn-summed
USD, not cache hit %.

**The borrow.** Extend the cost chip (or add a sibling chip) to
surface cache-hit % alongside cents. Render either as:

```
[ $0.043 · 87% cache ]   ← combined chip
```

or as a second tile beside the cost one:

```
[ $0.043 ]  [ 87% cache ]   ← pair
```

The pair is consistent with [consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)
allowlist-over-denylist discipline (kind classification stays
explicit per engine) and with the v1.0.706 chip-pair pattern.

**Cross-engine handling.** Per-engine source-of-truth differs:

| Engine | Field path | Notes |
|---|---|---|
| claude-code (M4) | `payload.usage.cache_read_input_tokens` / `.cache_creation_input_tokens` | Anthropic prompt cache; 5-min TTL |
| codex (M2) | `payload.usage.tokenUsage.last.cached_input_tokens` | OpenAI prompt cache |
| gemini-cli (M1) | not documented; ACP frame TBD | likely absent |
| kimi-code (M2/M4) | Moonshot Cache fields TBD | needs verification |

When a field is missing, the chip degrades to "—" rather than "0%"
(the *blank > wrong* discipline — same family as
[ADR-036 D9](../decisions/036-claude-code-statusline-telemetry.md)
and the allowlist-over-denylist contract in
[consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)).

**Sites.**
- `hub/internal/server/handlers_events.go` — confirm cache fields
  survive normalisation
- `lib/widgets/agent_feed/` — chip composer + tooltip
- `hub/internal/agentfamilies/agent_families.yaml` — profile rules
  for each engine's cache-field name

**Sizing.** Small — single-digit hours per engine; ~200 LOC mobile +
~50 LOC per profile YAML rule.

**Why it pays off.** Today we tell the director *what* they spent
post-hoc. With cache visibility we can tell them *whether the loop
is degrading in real time* — i.e. whether a long session has lost its
cache and is now burning 5× per turn. That signal is actionable
(respawn, compact, restart the loop with a tighter system prompt)
before the bill arrives. Same value Reasonix's cost-vs-cache split
gives DeepSeek users.

---

## 3. A2 — Tool-call repair as a first-class pattern

**What Reasonix does.** When a tool call fails (wrong arguments,
schema violation, file not found), the loop has a **structured
repair phase**: the failure is fed back as a typed event, the agent
gets a chance to repair the call, and only after a configurable
retry budget does the failure escalate. Treated as one of three
load-bearing pillars, not a peripheral concern.

**What TermiPod does today.** Failed MCP tool calls surface as orphan
cards in the mobile agent feed (v1.0.706 default-folded) or as
attention items (ADR-030 governed actions). The repair *intent* is
implicit — the agent sees the error frame in its transcript and may
or may not try again; no structured retry budget, no separation
between "transient repair retry" and "actual failure that needs
director attention."

This is exactly the recovery-layer gap that two recent fixes pointed
at:

- **v1.0.711** — `a2aPosterTap` was masking `*Client`'s
  `AttentionPoster` cast for every codex MCP tool call. Auto-declined
  as "user rejected" with no card raised. The fix was correct but
  the gap was that the failure was invisible until smoke caught it —
  no repair-attempt event, no escalation budget.
- **v1.0.722** — recursive disconnect via stream listener firing
  inside its own teardown await. Same family: a tool/event flow where
  the recovery path was the next listener firing rather than an
  explicit phase.

The recovery-layer underweight is also flagged in
[multi-agent-harness-landscape.md §7.2 B1](multi-agent-harness-landscape.md)
(hook taxonomy framing).

**The borrow.** Promote tool-call repair to a typed phase in the loop.
A failed MCP call emits:

```
event: tool_call.repair_attempt
  cause: schema_violation | not_found | permission_denied | transient
  attempt: 1
  budget: 3
```

The agent gets the typed error back; on attempt-exhaustion, the flow
escalates to an attention card with the full retry trace, not a
single masked decline. This composes with the existing ADR-030
governed-actions framing: governed tools have explicit policy; their
*repair* is a separate phase from their *initial call*.

**Sites.**
- `hub/internal/hostrunner/a2a_dispatcher.go` — wire the typed repair
  event past the dispatch
- `hub/internal/server/handlers_attention.go` — escalation card on
  budget-exhaustion
- `hub/internal/hubmcpserver/` — new event kind in `tools/list`
  catalogue (ADR-031 ergonomics)
- New ADR before code — this is a design pattern, not a bug fix

**Sizing.** Medium — an ADR plus probably a wedge of ~300 LOC across
hub + host-runner + mobile. The shape matters more than the size;
worth pacing.

**Why it pays off.** Today the recovery layer is invisible — failures
masquerade as silence. A structured repair phase makes the failure
*and* the retry budget legible to the director. That's the same
ergonomic gain a structured task primitive (ADR-029) provided over
ad-hoc todo lists: making implicit state explicit. Reasonix's three-
pillar framing is the giveaway: when something gets named as a
pillar, the rest of the loop earns clarity by contrast.

---

## 4. B1 — SEARCH/REPLACE → `/apply` explicit review gate

**What Reasonix does.** In `code` mode, the agent proposes diffs as
SEARCH/REPLACE blocks. The user explicitly types `/apply` to commit.
The diff is *shown*, the user *acks*, the bytes hit disk. No silent
edits.

**What TermiPod does today.** [ADR-030 governed actions](../decisions/030-governed-actions-and-propose-verb.md)
gives us approve / decline for *propose-verb* actions, and tool-call
elicitations raise cards. But there's no "preview this exact diff,
swipe to apply" surface specifically for file edits. The mobile feed
renders tool_result with the diff inline (default-folded since
v1.0.706), but applying happens before the director sees it — the
edit is already on disk by the time the orphan card shows up.

**The borrow.** Add a propose-edit verb that:

1. Renders the diff as a swipeable card in the mobile feed (extends
   ADR-030 with a new propose action — `propose.edit`).
2. The agent's edit blocks land in a `pending_edits` table until the
   director swipes apply (or auto-applies if the director has flipped
   the per-project "auto-apply" preference, parallel to permission
   mode).
3. On apply: bytes hit disk + attention card updates to "applied at
   <ts>." On decline: bytes never written, agent gets a typed
   "rejected" frame back (composes with A2 repair budget).

**Sites.**
- `hub/internal/server/handlers_propose.go` — new propose-verb +
  pending_edits table
- `hub/migrations/` — new migration for `pending_edits`
- `lib/screens/projects/` — mobile diff renderer (~400 LOC)
- ADR-030 amendment naming the new propose verb

**Sizing.** Medium — wedge-sized; a new ADR amendment + database
migration + mobile UX. Defer until A1 + A2 land so the recovery
layer is warm before adding more typed flows.

**Why it pays off.** The mobile-cockpit position lives or dies on
the director's confidence that the agent isn't silently changing
their codebase between glances. A swipe-to-apply diff card is the
exact ergonomic that Cursor's chat-edit mode demonstrates is
valuable; Reasonix demonstrates the *same* ergonomic works in a
terminal-first agent loop. We have neither today.

---

## 5. B2 — Plan mode `/todo` overlaying ADR-029 tasks

**What Reasonix does.** Engine-side `/todo` command for plan-mode
chain-of-thought scaffolding. The agent writes a plan; the user can
inspect/edit; execution then walks the plan. File-based, per-workspace.

**What TermiPod does today.** [ADR-029 tasks](../decisions/029-tasks-as-first-class-primitive.md)
are hub-side, first-class primitives with status
(todo / in_progress / blocked / done / cancelled), routed to projects,
visible across the fleet. The engine has no direct way to *create*
ADR-029 tasks — it can write a todo block in its transcript, but
that doesn't materialise as an attention/project surface.

**The borrow.** Bridge engine-side planning to hub-side tasks via a
new MCP tool:

```
tools/list entry:
  name: project_task_create
  description: Create a hub-side task in the current project.
  inputSchema: { title, description, assignee?, parent_task_id? }
```

When the agent calls `/todo create "Refactor auth"` (its own slash
command), the engine's planner runs `project_task_create` under the
hood; the task appears in the mobile project view; updates from
either side stay in sync via the existing event stream. This composes
with the per-engine driving-mode profile (ADR-010) — the slash
command and the MCP call don't have to be aware of each other.

**Sites.**
- `hub/internal/hubmcpserver/toolspec.go` — new ToolSpec entry
  (authority registry)
- `hub/internal/server/native_tools.go` — paired native tool if the
  call is also useful for skills (per CLAUDE.md "Easy to get wrong"
  §, verify the producer → wire → consumer → render chain before
  claiming symmetry — half-implemented primitives look identical to
  fully-implemented ones at the schema layer)
- `hub/internal/agentfamilies/` — per-engine wiring of the local
  slash command to the MCP call (claude-code skill / codex command /
  etc.)
- `tool_registry_test.go` + `native_tools_meta_test.go` —
  catalogue sweep (per CLAUDE.md "Easy to get wrong" §)

**Sizing.** Small per engine; the heavy work is the cross-engine
wiring (which slash commands → same MCP call). Probably 150-200 LOC
total + new ADR-029 amendment.

**Why it pays off.** Today the engine's plan and the hub's task list
are two separate worlds the director has to mentally reconcile.
Reasonix's `/todo` is just a markdown file in `~/.reasonix/`; ours
is a SQLite row with state and lineage. The bridge gets the engine's
planning ergonomic *and* the hub's authority + audit + cross-host
visibility. The tighter the bridge, the less the director has to
hold two models of "what work is in flight."

---

## 6. What NOT to borrow

Three Reasonix design choices are *explicitly* the wrong direction
for us:

- **Single-backend coupling.** Reasonix says it plainly: *"Multi-
  provider flexibility. DeepSeek-only on purpose. Coupling to one
  backend is the feature, not a limitation."* We are explicitly
  multi-engine across four backends with M1/M2/M4 driving modes;
  coupling to one engine would invert our core trade-off. Their
  cache-invariant gain comes precisely *from* that coupling; we get
  similar gains per-engine (A1) without giving up the multi-engine
  premise.
- **Tauri desktop bundle.** Different deployment model; we're
  mobile-first by [blueprint](../spine/blueprint.md) and
  [information-architecture](../spine/information-architecture.md)
  axioms. Not even a tension — just irrelevant.
- **QQ-messenger bridge.** Same family as omo's OpenClaw (Discord /
  Telegram / WhatsApp), agent-deck's Telegram/Slack, PilotDeck's
  Web/CLI/IM. See [integrating-open-source-agents.md §5
  Group B](integrating-open-source-agents.md). Our mobile app is
  the cockpit; a messenger bridge inverts the principal/director
  archetype.

---

## 7. Sequencing

If all four borrows ever ship:

1. **A1 first.** Smallest, most measurable, immediately useful.
   Surfaces a number that motivates the others.
2. **A2 second.** Builds on the typed-event vocabulary A1 introduces
   (cache field handling per engine). Pays for itself the next time
   the recovery layer fails silently.
3. **B1 only after A2.** Diff-apply gates *are* a kind of governed
   tool call; A2's repair phase is the natural place to land their
   decline path.
4. **B2 in parallel with B1.** Different surface (tasks vs edits);
   no contention.

None are blockers for current MVP work. The catalogue is here so
that when one of them gets reached for, the framing is already
written.

---

## 8. Open questions

- **OQ-1.** What's the right unit for the cache-hit metric — last
  turn, rolling 10 turns, session-cumulative? Reasonix shows
  cumulative; for a recovery signal, rolling-10 is probably sharper.
- **OQ-2.** Does A2's repair phase belong inside ADR-030's propose-
  verb framework, or as a new typed phase parallel to it?
  Argument for inside: tool calls are already governed actions in
  ADR-030's sense; repair is just an attempt-counter on the same
  action. Argument for parallel: repair has a budget and a decay,
  which propose-verbs don't.
- **OQ-3.** For B2, what happens when an engine's local plan
  diverges from the hub task it created — does the engine's plan
  re-sync from the hub, or do they drift? Reasonix's `/todo` is
  one-way (engine writes); ours probably wants bidirectional.
- **OQ-4.** Where does prompt-cache decay live in the engine's
  responsibility vs the hub's? Reasonix bakes it into the loop;
  for us, the engine ships its own cache logic and the hub *observes*
  via telemetry. The borrow is observability, not loop control —
  but is that enough?

---

## 9. Sources

- esengine/DeepSeek-Reasonix — https://github.com/esengine/DeepSeek-Reasonix
- Reasonix landing page — https://esengine.github.io/DeepSeek-Reasonix
- Reasonix benchmark write-up (cache hit on real session) —
  github.com/esengine/DeepSeek-Reasonix/blob/main/benchmarks/real-world-cache/README.md
- DeepSeek API prefix-cache pricing — https://api-docs.deepseek.com/quick_start/pricing
- Anthropic prompt-cache reference —
  https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching

---

## 10. Related

- [integrating-open-source-agents.md](integrating-open-source-agents.md)
  — the integration question for Reasonix-as-engine (§4 Group A).
  This doc complements it by extracting the *design ideas* from the
  same engine independently of whether we ever spawn it.
- [ADR-030 — Governed actions](../decisions/030-governed-actions-and-propose-verb.md)
  — the surface B1 (`/apply` gate) extends.
- [ADR-029 — Tasks as first-class primitive](../decisions/029-tasks-as-first-class-primitive.md)
  — the surface B2 (`/todo` bridge) extends.
- [ADR-036 — Statusline / cost telemetry](../decisions/036-claude-code-statusline-telemetry.md)
  — the surface A1 (cache-hit chip) extends.
- [consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)
  — allowlist-over-denylist discipline A1's missing-field handling
  should follow.
- [multi-agent-harness-landscape.md §7.2 B4](multi-agent-harness-landscape.md)
  — stuck-session heartbeat nudge from agent-deck; pairs with A2
  recovery-layer work.
