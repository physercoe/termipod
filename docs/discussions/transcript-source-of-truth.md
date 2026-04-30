# Transcript source of truth: hub-mediated vs direct engine-record

> **Type:** discussion
> **Status:** Resolved (2026-04-30) — hub-mediated is the locked
> design; direct-read forensic view kept as a post-MVP follow-up.
> No ADR — the choice is implicit in the multi-engine positioning
> (ADR-010, ADR-012, ADR-013) and the operation-log framing pinned
> in ADR-014. This doc exists so the comparison and its tradeoffs
> are grep-able when someone re-litigates "why don't we just read
> the JSONL?"
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** There are two coherent designs for "what is a session
transcript?" — (A) hub-mediated, where `agent_events` is the source
of truth and drivers translate vendor stream-json into a typed
event vocabulary, and (B) direct engine-record read, where the
client renders claude's `~/.claude/projects/<cwd>/<sid>.jsonl` (and
each other engine's native record format) without translation.
Termipod chose (A). The choice is paid for in translators, an
FTS index, a snapshot cache, and write amplification, but it's
load-bearing for multi-engine + multi-host + multi-tenant +
operation-log semantics — exactly the axes single-engine clients
(claudecode-remote, Happy) don't carry. (B)'s strengths around
engine-fidelity and forensics are real; the right way to get them
without giving up (A)'s benefits is a post-MVP "raw engine record"
forensic view served through host-runner. This doc records the
comparison, the decision, and where (B) still earns its keep.

---

## 1. The two designs in one paragraph each

**Design A — hub-mediated transcript (current).** The host-runner's
driver reads the engine's stdout (claude/gemini stream-json, codex
JSON-RPC), translates each frame into a typed `agent_event` row
(`text`, `tool_call`, `tool_result`, `usage`, `session.init`,
`context.compacted`, …), and POSTs to the hub. The hub is the
single store; `agent_events.session_id` stamps each row with the
hub session it belongs to; mobile renders entirely off
`agent_events`. ADR-010's frame profiles absorb vendor schema
churn declaratively. Pre-engine events (mobile input, approvals,
A2A, lifecycle, hub-emitted markers) live in the same table.

**Design B — direct engine-record read.** The mobile (or hub)
reads the engine's own conversation file directly. For claude
that's `~/.claude/projects/<cwd>/<sid>.jsonl`; gemini stores under
`<projdir>/.gemini/sessions/<uuid>/`; codex keeps thread state in
its CLI store. The renderer parses each engine's native format,
no translation layer. Search, persistence, and multi-host stitching
are filesystem problems.

---

## 2. Comparison axes

### 2.1 Where direct-read genuinely wins

| Axis | Why (B) beats (A) |
|---|---|
| Authority for engine view | The engine's record IS the engine's truth. (A)'s translation can drop fields, lag behind SDK changes, or diverge silently. |
| Storage cost | One copy on disk vs duplicate write to `agent_events`. ~2× disk + bandwidth on the hot path. |
| Engine-fidelity replay | Native format retains system prompts, tool schemas, internal markers. Useful for fine-tuning, vendor analytics, replay. |
| Implementation cost (single-engine, single-host) | Roughly 10× less code. claudecode-remote / Happy live in this space because they don't carry the multi axes. |
| Independence from hub uptime | If the engine kept writing while the hub was down, (B) sees those turns; (A) has a gap. |
| Native tooling | `claude --resume`, JSONL viewers, vendor analytics work out of the box. |

### 2.2 Where hub-mediated wins

| Axis | Why (A) beats (B) |
|---|---|
| Multi-engine unification | Three engines, three schemas. Mobile would need three renderers — or a normalising layer, which *is* `agent_events`. |
| Multi-host | Engine records live on the host where the engine ran. Stitching one steward's transcript across hosts is a filesystem-coordination problem (A) sidesteps. |
| Multi-tenant / per-team auth | DB row-level isolation + per-request bearer is straightforward. Filesystem ACLs across hosts/teams are fragile. |
| Hub-only events | Lifecycle (started/stopped/paused/resumed), approval decisions, attention items, A2A peer messages, audit events, context-mutation markers, cancelled-or-rejected user input. All hub-only by construction. The engine doesn't know about most of these. |
| Search & cross-session queries | FTS5 + indexes on `agent_events` (migration 0031). (B)'s answer would be grep across scattered JSONL — cross-session search would need an indexer, which is (A) in another shape. |
| SDK churn isolation | claude reshapes stream-json regularly. ADR-010 frame profiles absorb that in YAML. (B) breaks at every SDK upgrade. |
| Real-time streaming to mobile | SSE bus already wired off the event-insert path. (B) needs per-host file watchers + a transport. |
| Cache-first UX (ADR-006) | Mobile SQLite cache renders cached transcripts instantly. (B) would need to ship JSONL or build a per-engine offline serialiser. |
| Retention through engine mutations | `/compact`, `/clear`, `/rewind` may rewrite or rotate the engine's record. `agent_events` is append-only — mutations are markers (ADR-014 OQ-4), not data loss. The operation-log framing only works because hub events are immutable. |
| Privacy / redaction surface | Engine records can leak CWD, file paths, secret tool inputs. (A) can normalise/redact at insert; (B) exposes whatever the engine wrote. |

### 2.3 Where they fundamentally diverge: post-mutation

After `/compact`, the two designs answer *different questions*:

- **(B) post-/compact:** The JSONL is rewritten (claude as of late
  2025; behaviour subject to vendor change). The reader sees the
  compacted state. Pre-compact history is gone from the engine
  view.
- **(A) post-/compact:** `agent_events` retains full pre-compact
  history (append-only) plus a `context.compacted` marker. Mobile
  shows the operator everything they ever saw, plus a marker for
  where the engine truncated.

(B) says "show what the model can see now." (A) says "show what
happened, including what the model can no longer see." Neither is
wrong; they answer different questions. ADR-014's operation-log
framing commits termipod to the second.

---

## 3. The structural cost each design imposes

(B) pushes complexity *outward* — N renderers, host-side file
shipping, multi-host coordination, vendor-SDK upgrade tax, cross-
engine schema reconciliation in the client.

(A) pushes complexity *inward* — translators, an event schema,
migrations, an FTS index, a snapshot cache. The hub becomes load-
bearing and can't be skipped.

The deciding factor isn't "which is simpler at small scale." It's
that **multi-engine + multi-host + per-team auth + operation-log
semantics** are termipod's positioning differentiators
([memory: project_positioning_vs_competitors](../../project_positioning_vs_competitors.md)).
The moment those are required, (B) fragments into N renderers
and M host shippers; (A) converges into one schema.

---

## 4. What we lose by picking hub-mediated

Worth being honest about. Picking (A) means:

- **Translator drift risk.** If the StdioDriver's translator drops
  a field claude added in a recent SDK release, mobile silently
  loses that data. ADR-010 frame profiles + parity tests
  (`profile_parity_test.go`) mitigate but don't eliminate.
- **Write amplification.** Every event written twice — once to
  the engine's record, once to `agent_events`. Network + storage
  cost. Materially fine at MVP scale; would matter for high-
  throughput tool-call traffic.
- **No native tool compatibility.** A user who wants to point
  vendor analytics at the transcript has to extract from
  `agent_events`, not feed it the JSONL.
- **Hub uptime is now load-bearing.** If the hub is down, the
  engine keeps running but its events fall on the floor. (B) would
  catch up on next read.

Some of these are addressable later (better translator coverage,
event compression, optional engine-record export). None of them
flip the analysis at termipod's scale and positioning.

---

## 5. The hybrid we're not building yet

Coherent post-MVP follow-up: **(A) as primary + (B) as a
forensic-view escape hatch**. Concretely:

- `GET /v1/teams/{team}/agents/{agent}/engine-record` proxied
  through host-runner, returning the engine's native record file
  for the agent's current spawn.
- Mobile UI: per-session "Show raw engine record" power-user
  affordance, gated behind a debug flag, only enabled when the
  agent's `kind` ships in a (B)-compatible engine.
- Use cases: translator drift debugging, vendor support tickets
  ("send us your conversation"), forensics on agents whose
  behaviour the operator wants to audit at full fidelity.
- Cost: one new endpoint, a small per-engine path resolver
  (claude → `~/.claude/projects/<cwd>/<sid>.jsonl`,
  gemini → `<projdir>/.gemini/sessions/<uuid>/`,
  codex → TBD), a small mobile pane.
- Why post-MVP: zero current device-test pressure pulling for it,
  and the engineering would distract from the multi-engine surface
  parity work that's actually load-bearing for the demo.

Triggers that would promote it from later:

- A user files a bug whose root cause is translator drift, and the
  raw engine record is the only way to confirm it.
- A vendor (Anthropic / Google / OpenAI) asks for a transcript in
  their own format for support purposes.
- We start shipping fine-tuning workflows that consume native
  conversation records.

Until then: keep the framing, don't build the endpoint.

---

## 6. Bottom line

If termipod were re-scoped to claude-only, single-host, single-
tenant, (B) would be the right call — less code, less infra, native
tool compatibility, no translator drift. The hub-mediated design is
paying its weight in the multi-engine, multi-host, multi-tenant,
operation-log axes the positioning rests on. ADR-014's framing is
what makes the cost coherent: termipod isn't a transcript renderer
mimicking the engine, it's a different artifact — an operation log.

If anyone re-litigates this in the future, the question to ask is
**"have any of those positioning axes changed?"** If yes, revisit.
If no, the answer is unchanged.

---

## 7. Cross-links

- ADR-010 — frame profiles as data (the substrate that makes
  hub-mediated cheap to extend across engines).
- ADR-014 — claude-code resume cursor + operation-log framing +
  context-mutation markers (the design statement that hub
  transcript ≠ engine transcript).
- ADR-006 — cache-first cold-start (depends on hub-mediated).
- `discussions/multi-engine-frame-parsing.md` — the design
  conversation that produced ADR-010; complementary lens.
- `discussions/fork-and-engine-context-mutations.md` — adjacent;
  same hub/engine boundary, different axis.
- Memory: `project_positioning_vs_competitors` — the
  positioning differentiators this doc rests on.
