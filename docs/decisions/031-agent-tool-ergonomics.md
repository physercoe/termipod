---
name: Agent tool ergonomics — two-tier descriptions, tools.get, structured hints, no polymorphism
description: Lock in the four design picks from the agent-tool-ergonomics discussion. Tools ship two-tier descriptions (short in catalog, long via `tools.get`); the meta-tool is named `tools.get` for surface-area consistency; error hints are a structured `{hint_text, see_tool, see_doc}` triple; no new input polymorphism (legacy aliases grandfathered with deprecation hint only). MVP enables the steward to read back a doc by ULID in one tool call without guessing across 5 wrong tools.
---

# 031. Agent tool ergonomics — two-tier descriptions, `tools.get`, structured hints, no polymorphism

> **Type:** decision
> **Status:** Proposed (2026-05-18) — D-1 through D-4 locked in the 2026-05-18 design conversation following the [agent-tool-ergonomics discussion](../discussions/agent-tool-ergonomics.md); D-1 enriched the same day with Claude Code chapter-3 prior art (self-documenting schema + fail-closed operational metadata + deterministic ordering). Companion rollout plan at [`../plans/agent-tool-ergonomics-rollout.md`](../plans/agent-tool-ergonomics-rollout.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Close the discovery + documentation-depth + error-recovery gap revealed by the 2026-05-18 steward incident (6 turns guessing the right tool to read a doc by ULID) by locking in four design picks: (D-1) every MCP tool ships a **two-tier description** with a structured payload (short + long + schema + examples + failure_modes + see_also), (D-2) the meta-lookup tool is named **`tools.get`** for consistency with `agents.get` / `documents.get` / etc, (D-3) error responses include a **structured `hint` envelope** `{hint_text, see_tool?, see_doc?}` on every 4xx path, and (D-4) **no new input polymorphism** — each tool keeps one canonical input shape; the two existing legacy aliases (`request_decision` → `request_select`, `templates_propose` → `templates.propose`) are grandfathered with a deprecation hint, and no new aliases are added. MVP enables a steward to read a doc by ULID via `documents.get` in one tool call without guessing across `get_project_doc` / `documents_get` / `search` / `journal_read` / etc.

---

## 1. Context

A project steward on 2026-05-18 took six turns to read back a memo by ULID after a worker reported `tasks.complete(summary="doc_id=01KRV538…")`. The sequence: `get_project_doc(path=<ULID>)` → 404; `documents_get` → no such tool; `search(q=...)` → SQL error; `list_channels` → unrelated; `journal_read` → wrong agent's notes; gave up. The actual tool (`documents.get`) didn't exist as an MCP entry until v1.0.630 — same defect class as v1.0.591 (MCP catalog × dispatcher × handler discipline).

The discussion at [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md) names three orthogonal failure modes the incident collapsed into one symptom:
- **Discovery** — agent has an intent, no tool name maps cleanly; agent guesses.
- **Documentation depth** — descriptions live in the always-loaded MCP catalog (~30KB across ~50 tools); pre-v1.0.621 hygiene rule allowed bloat, post-rule still verbose. Tradeoff: inline-every-call vs lazy-load.
- **Error recovery** — errors describe what happened, almost never what to try instead. The agent decides between retry / different tool / escalate with no signal.

Plus a fourth temptation: **input polymorphism** (one tool accepts multiple equivalent forms, e.g. `documents.get(id_or_path)`). UNIX, REST, GraphQL, and well-tested MCP servers converge on single-canonical-input — the fix for "agent picked wrong" lives in discovery + descriptions + errors, not in tool-input branching.

## 2. Decisions

### D-1. Two-tier descriptions, structured payload

Every tool's catalog entry carries **two description fields**:

- **`short`** — one-sentence present-tense contract + required params + canonical input type. Always returned by `tools/list`. Hard cap 200 chars. Example: `"Fetch a document by id. Required: document_id (ULID)."`.
- **`description`** (the existing long field) — full body returned only by `tools.get` (D-2). May include shape, worked examples, failure modes, see-also. Subject to the v1.0.621 hygiene rule (no version markers / no doc references / no rationale prose).

`tools.get` returns a **structured payload**, not just the long blob:

```json
{
  "name": "documents.get",
  "short": "Fetch a document by id. Required: document_id (ULID).",
  "long": "...the full description body...",
  "input_schema": {...},          // per-parameter descriptions live IN the schema
  "examples": [{"description": "...", "args": {...}}],
  "failure_modes": [{"code": "not_found", "when": "...", "hint": "..."}],
  "see_also": ["documents.list", "get_project_doc"],
  "concurrency_safe": true,        // read-only — safe to batch
  "side_effecting": false,
  "permission_tier": "worker"      // ties the catalog to roles.yaml / ADR-030
}
```

Rationale: structure constrains description authoring (every tool's failure modes get enumerated explicitly, not buried in prose); makes the catalog programmatically diff-able (CI lint over `failure_modes[].hint.see_tool` references); allows future agentic clients to act on structure (auto-retry with `see_tool`).

Cost: every tool authoring grows from "write a description string" to "fill out the named fields." Worth it: descriptions become a contract surface with a schema, not free prose.

**Three refinements borrowed from Claude Code's tool system** (《御舆 — 解码 Agent Harness》 ch. 3 "工具系统"; full analysis in [discussion §7](../discussions/agent-tool-ergonomics.md)):

- **The schema self-documents.** Per-parameter descriptions live *inside* `input_schema` — the way Zod schemas carry the constraint and the doc together — not restated in prose. This shrinks the authoring surface and makes the description ↔ schema lint (plan W5) trivial: there is no parallel prose to drift.
- **Operational metadata, fail-closed.** The payload carries `concurrency_safe`, `side_effecting`, and `permission_tier`. The safety flags default *closed* — an entry that omits them is read as `concurrency_safe:false, side_effecting:true`. This makes authority explicit in the catalog and gives [ADR-030](030-governed-actions-and-propose-verb.md) (governed actions) and roles.yaml a single declared source to check against, instead of leaving authority implicit in handler code.
- **Deterministic ordering.** `tools/list` returns the catalog in a stable (alphabetical) order so the prompt prefix stays cache-stable; a non-deterministic catalog silently kills prompt-cache hits on every dispatch.

The two-tier split (D-1) + `tools.get` (D-2) is the same **`ToolSearchTool` / deferred-capability** pattern the Claude Code harness already ships — independent re-derivation from the same prompt-token pressure, which is strong external validation of the direction.

### D-2. Meta-lookup tool named `tools.get`

The meta-lookup tool is named **`tools.get(name)`** — not `tools.describe` or `tools.help`. Surface-area consistency with the rest of the termipod catalog (`agents.get`, `documents.get`, `projects.get`, `tasks.get`, ...) wins over UNIX-style `help`.

The protocol-level `tools/list` continues to return the catalog (short only); the `tools.get` MCP tool returns the structured payload from D-1. Behavior parallels `documents.list` + `documents.get`.

Rationale: agents learn one verb shape and apply it across the catalog. The MCP spec already has `tools/list` and `tools/call` as protocol-level verbs; `tools.get` is a termipod-specific MCP tool that doesn't conflict.

### D-3. Structured hint envelope on every 4xx error

Every 4xx error path returns a **structured `hint` envelope** alongside the existing message:

```json
{
  "error": "<the existing message — names what happened with the offending value>",
  "hint": {
    "hint_text": "<what to do instead, in present tense>",
    "see_tool": "<optional: name of the correct tool to call>",
    "see_doc": "<optional: doc path for human contributors reading the source>"
  }
}
```

Rationale over free-string hints: structure lets a smart MCP client implement "did-you-mean" automatically (read `see_tool` from the error, surface as a UI hint); CI lint (the plan's W5) can verify `see_tool` references always name a real tool in the catalog; future agentic clients can do auto-retry without parsing prose. `hint_text` is required when `hint` is present; `see_tool` and `see_doc` are optional but at least one of the three should carry actionable signal.

Specific canonical hint authors should write (non-exhaustive):
- `get_project_doc` 404 + ULID-shaped path → `{hint_text: "this tool reads filesystem files under docs_root; for document ULIDs use documents.get", see_tool: "documents.get"}`.
- `documents.get` 404 → `{hint_text: "this tool reads docs created via documents.create; for filesystem files use get_project_doc", see_tool: "get_project_doc"}`.
- Permission denied (403) on a worker-called tool → `{hint_text: "your role lacks this capability; escalate via request_help to your parent steward", see_tool: "request_help"}`.

### D-4. No new input polymorphism — pragmatic grandfathering

**No tool may accept two semantically distinct input shapes for the same operation.** A tool's `input_schema` defines exactly one canonical shape per parameter.

**Pragmatic grandfathering for the two existing aliases:**
- `request_decision` (alias for `request_select`, renamed in v1.0.295).
- `templates_propose` (alias for `templates.propose`).

These continue to resolve, but their `short` description carries a `[DEPRECATED, use <new name>]` prefix, and the `failure_modes[]` includes a hint pointing at the canonical name. No new aliases are added.

Specifically REJECTED designs:
- `documents.get(document_id | path)` polymorphic input — collapsing filesystem-tier (`docs_root` markdown files) and DB-tier (documents-table rows) hides a real semantic distinction.
- "Smart routing" tools that delegate based on argument shape — increases authorization complexity and ambiguates errors.

Rationale: UNIX (`ls` vs `find`), REST (one resource shape per endpoint), GraphQL (each resolver has one input shape), and well-tested MCP servers all converge on single-canonical-input. Convenience wins of polymorphism are real but small; the costs (semantic ambiguity, authorization branching, contributor confusion, error-message drift) are medium and recurring. The fix for "agent picked wrong tool" lives in D-1 / D-2 / D-3, not in tool 4.

## 3. Consequences

### Positive
- A steward asking "I need to read a doc by ULID" finds `documents.get` via per-persona index or `tools.get('documents.get')`; one-tool-call recovery.
- `tools/list` payload drops from ~30KB to ~5KB (every dispatch saves tokens).
- 4xx errors become self-recovering for the agent: every hint names the right next action.
- CI lint can walk the catalog (every tool has short + long + schema; every `see_tool` reference exists).
- New contributors authoring a tool fill out 5 named fields, not free prose — the schema is the spec.

### Negative
- Migration cost: every existing tool (~50) needs a `short` field plus the three operational fields (`concurrency_safe` / `side_effecting` / `permission_tier`). The `short` split is mechanical; the operational flags need a per-tool judgement call.
- Description authoring overhead: structured `failure_modes[]` is more work than a sentence in the long prose. Acceptable cost — the structure is what makes hints checkable.
- Backward-incompat risk: clients reading only the `description` field from `tools/list` get nothing (`short` is the new field). Mitigation: ship both (`description` keeps the long body during deprecation window) until clients update.

### Neutral / deferred
- **Hint aggressiveness** (manual hint strings vs automatic inference from argument shape): start manual; automatic inference is a future extension if manual proves insufficient.
- **Per-persona index location** (inline in persona prompts vs separate `context_files` fragment): plan picks inline; not load-bearing for this ADR.

## 4. Alternatives considered

| Alternative | Why rejected |
|---|---|
| Inline-everything (status quo) | Pays ~30KB / dispatch cost forever; doesn't fix discovery. |
| `tools.describe` (verb naming) | Mismatches the `<noun>.get` convention used everywhere else in the catalog. |
| `tools.help` | UNIX-y but inconsistent with termipod's noun.verb pattern. |
| Free-string hints | Can't be CI-linted; future clients can't programmatically act on them. |
| Input polymorphism (`documents.get(id_or_path)`) | Hides semantic distinction; ambiguates errors; complicates authz. |
| External docs links in descriptions (`see docs/...`) | Agents can't fetch local repo files; the link is for humans, not the LLM consumer. |

Full alternatives analysis in [discussion §3, §5](../discussions/agent-tool-ergonomics.md).

## 5. Implementation

See [`../plans/agent-tool-ergonomics-rollout.md`](../plans/agent-tool-ergonomics-rollout.md):
- **Phase 1 (MVP, 3 wedges):** `tools.get` meta-tool, two-tier split across the catalog, hint-bearing errors on top 5 paths.
- **Phase 2 (MVP, 1 wedge):** per-persona intent → tool index in 10 main prompts.
- **Phase 3 (post-MVP, 1 wedge):** hint pass for remaining 4xx paths + CI catalog lint.

Status flips to `Accepted` when Phase 1 + Phase 2 ship.

## 6. References

- [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md) — full framing + alternatives analysis.
- [`../plans/agent-tool-ergonomics-rollout.md`](../plans/agent-tool-ergonomics-rollout.md) — execution.
- [`../reference/hub-mcp.md`](../reference/hub-mcp.md) — current MCP surface; this ADR's two-tier split applies to every entry there.
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md) §3 Layer 4 — the description ↔ schema audit lint lands as the plan's W5.
