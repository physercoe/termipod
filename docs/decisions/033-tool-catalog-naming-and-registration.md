---
name: Tool catalog — one naming convention + single registration point
description: Lock the foundation under the MCP tool catalog. Five decisions. D-1 — one naming convention, resource-first and namespaced, in snake_case (Anthropic's own form, the only name format every MCP client accepts without filtering — load-bearing because termipod is multi-engine); a tool-use eval may revisit the delimiter post-MVP but does not gate the ADR. D-2 — migrate the non-conforming names via grandfathered aliases, no hard cutover. D-3 — one toolSpec declaration per tool; catalog, dispatch, tier, and role-eligibility are derived from it or CI-locked, retiring the four-place lockstep defect class structurally. D-4 — consolidate the three verified duplicate pairs (list_agents/agents.list, get_task/tasks.get, get_audit/audit.read). D-5 — the one-tool-one-REST-call relay rule holds as default; consolidation is an explicit ADR-justified exception. Must precede or fuse with ADR-031 W2, which rewrites every catalog entry anyway.
---

# 033. Tool catalog — one naming convention + single registration point

> **Type:** decision
> **Status:** Proposed (2026-05-18) — D-1 through D-5 from the [tool-catalog-structure discussion](../discussions/tool-catalog-structure.md), which audits the catalog and re-grounds the naming question against the MCP 2025-11-25 spec and Anthropic's tool-design guidance. Director review same day: D-3 (single registration point) and the W2-sequencing constraint ratified; D-1's delimiter resolved to `snake_case` (the eval is downgraded to a post-MVP option, not a gate — termipod has no tool-use eval harness and the client-safety case decides it). The contributor-facing counterpart to [ADR-031](031-agent-tool-ergonomics.md) (agent-facing).
> **Audience:** contributors
> **Last verified vs code:** v1.0.630-alpha (+ ADR-031 W1 `tools.get`)

**TL;DR.** The MCP tool catalog works but is grounded in accretion
history, not design: two unreconciled naming conventions, three
verified duplicate tools, and one tool's identity spread across four
files with no single registration point (the defect class that
burned v1.0.591 and v1.0.630). This ADR locks the foundation —
(D-1) one resource-first naming convention in `snake_case`; (D-2)
migrate the non-conforming names via grandfathered aliases; (D-3)
one `toolSpec` declaration per tool,
from which catalog / dispatch / tier / role-eligibility are derived
or CI-locked; (D-4) consolidate the three duplicate pairs; (D-5) the
relay rule holds as default, consolidation is an ADR-justified
exception. It must land **before or fused with ADR-031 W2**, which
rewrites every catalog entry regardless.

---

## 1. Context

ADR-031 made the catalog *discoverable to the agent* (`tools.get`,
two-tier descriptions, hint errors). It explicitly left tool
renaming and code structure out of scope. Implementing ADR-031 W1
forced a look at the catalog's topology, and the audit in the
[tool-catalog-structure discussion](../discussions/tool-catalog-structure.md)
found the catalog is not well-grounded for the *contributors* who
extend it:

- **Two naming conventions** — ~25 `snake_case` verbs
  (`get_task`, `list_agents`) vs ~50 `noun.verb`
  (`documents.get`, `agents.spawn`); no rule mandates either, and
  even the dotted half is internally inconsistent.
- **Three verified duplicate pairs** — a handler audit (discussion
  §2.2) confirmed `list_agents`/`agents.list`,
  `get_task`/`tasks.get`, `get_audit`/`audit.read` are accidental
  redundancy, and two have *drifted* so the twins return different
  fields — an agent picking the "wrong" twin silently gets less.
- **No single registration point** — a tool's identity is spread
  across a catalog def (one of four sources, two shapes), a
  dispatch case (one of two mechanisms), a `tiers.go` row, and a
  `roles.yaml` pattern. `documents.get` shipped in v1.0.630 missing
  its tier row; `request_project_steward` (v1.0.591) shipped a
  dispatcher case with no catalog entry. Same defect class, twice.

The discussion also re-grounds the naming question (its §3) against
current practice: the MCP spec rev. 2025-11-25 (SEP-986) makes both
`snake_case` and dotted names spec-legal, but clients still filter
dots inconsistently; Anthropic's tool-design guidance prescribes
choosing a namespacing scheme **by evaluation**, not by analogy, and
uses `snake_case` in its own examples.

## 2. Decisions

### D-1. One naming convention — `snake_case`, resource-first, namespaced

Every MCP tool name is **`snake_case`, resource-first, and
namespaced**: `resource[_subresource]_verb` — one scheme
catalog-wide, lint-enforced. Examples: `documents_get`,
`agents_spawn`, `tasks_update`, `project_channels_create`.

`snake_case` over the dotted `resource.verb` form for three
grounded reasons (discussion §3): it is the form Anthropic's own
tool-design guidance uses; it is the only name format every MCP
client and function-calling API accepts without filtering — which
is load-bearing because termipod is a multi-engine control plane
(claude-code, codex, gemini-cli, kimi-code) and a name is only as
safe as the least-tolerant client; and the dotted delimiter, though
spec-legal since MCP rev. 2025-11-25, is still filtered
inconsistently by real clients.

Anthropic's guidance notes the namespacing scheme has measurable,
model-dependent effects on tool-use evaluations. A tool-use eval
*could* therefore revisit the delimiter — but it does **not gate
this ADR**: termipod has no tool-use eval harness today, building
one is disproportionate to this single decision, and the
client-safety argument decides it on its own. Revisiting is a
post-MVP option, not a blocker.

Sub-rules: multi-word resources stay `snake_case`
(`project_channels`, not `projectChannels`); verbs are single
canonical tokens (`get`, `list`, `create`, `update`, `delete`);
three-level names are flattened unless the sub-resource is
load-bearing.

### D-2. Migrate via grandfathered aliases

Two sets of names change under D-1: the ~50 dotted tools
(`documents.get` → `documents_get` — a mechanical delimiter swap,
already resource-first) and the ~25 verb-first `snake_case` tools
(`get_task` → `tasks_get`, `list_agents` → `agents_list` — a
reorder). Effectively the whole catalog gets a new name.

Every old name keeps resolving as a **deprecated alias**: the
catalog `short` carries a `[DEPRECATED, use <new name>]` prefix, and
a `failure_modes[]` hint points at the canonical name. Aliases are
removed at a named version boundary. **No hard cutover** — a hard
rename would break agent templates that have not re-rendered.

Precedent: the existing `request_decision` → `request_select` and
`templates_propose` → `templates.propose` aliases; ADR-032's v1.1.0
cutoff shim.

### D-3. One registration point per tool

A tool is declared **once**, in a single `toolSpec` value carrying:
name, input schema, the D-1 operational metadata
(`concurrency_safe` / `side_effecting` / `permission_tier` per
ADR-031 D-1), the tier (`tiers.go`), role-eligibility, and the
dispatch target. From that one declaration:

- the `tools/list` catalog entry is **generated**;
- the dispatch route is **resolved** (retiring the two-mechanism
  `switch` + authority fall-through split);
- the `tiers.go` row and the `roles.yaml` cross-check are
  **derived or CI-locked** against it.

This makes the four-place lockstep state (discussion §2.4)
**unrepresentable** rather than test-caught — the
[validate-at-every-boundary](../discussions/validate-at-every-boundary.md)
principle. ADR-031 W5's catalog lint becomes a property of the type,
not a standalone reactive test.

The four catalog sources in two shapes (discussion §2 — `base` /
`extra` / `orchestration` map literals + `authority` typed structs)
collapse to one typed registry.

### D-4. Consolidate the three duplicate pairs

Per the verified audit (discussion §2.2):

- **`list_agents` → `agents.list`.** Port the one unique field
  (`pane_id`) onto `agents.list`, then `list_agents` becomes a
  deprecated alias (D-2).
- **`get_task` + `tasks.get` → one tool.** Merge into a single
  task-fetch tool returning the **field union** (`priority`,
  `plan_step_id`, `source`, `milestone_id`, `parent_id`,
  `assignee_id`, `created_by`). Decide one canonical input shape;
  the old name is a deprecated alias.
- **`get_audit` → `audit.read`.** Fold `get_audit`'s `action`
  filter into `audit.read`, reconcile the `limit` cap, then
  `get_audit` becomes a deprecated alias.

### D-5. The relay rule holds as default; consolidation is an explicit exception

`hub-mcp.md` §5's rule — "one MCP tool = one REST call; composition
is the agent's job" — **remains the default**. It keeps the audit
trail one-row-per-tool-call and the contributor model simple.

A tool that consolidates *multiple* REST calls for agent ergonomics
(Anthropic's "more tools don't always lead to better outcomes",
discussion §3) is permitted **only when named in an ADR with its
rationale** — not at a contributor's discretion. D-4's task-fetch
merge is still one REST call, so it needs no exception; it is
deduplication, not consolidation.

## 3. Consequences

### Positive
- A contributor adds a tool by writing one `toolSpec`; catalog,
  dispatch, tier, and role gate follow. The drift class is gone.
- One predictable naming scheme — agents stop guessing between
  twins, contributors get a precedent to copy.
- Three redundant tools removed; no silent field-loss from picking
  the wrong twin.
- The catalog becomes programmatically walkable from one registry —
  ADR-031's `tools.get` / two-tier work reads one source.

### Negative
- **Migration cost.** Effectively the whole catalog is renamed
  (D-2: ~50 dotted tools get a mechanical `.`→`_`, ~25 verb-first
  tools are reordered), plus the D-3 refactor touching every tool
  definition. Sizeable; staged in the companion plan.
- D-3 restructures `server/mcp.go`, `mcp_more.go`,
  `mcp_orchestrate.go`, and `hubmcpserver/tools.go` into one
  registry — a real refactor, not a wrapper.
- Aliases inflate the catalog during the deprecation window.

### Neutral / deferred
- **Code file layout by domain** (discussion O-C) — deferred to a
  follow-on; not a blocker for D-1–D-5.
- **A D-1 delimiter eval** — a post-MVP option to revisit
  `snake_case` vs dotted once a tool-use eval harness exists; it
  does not block this ADR or its rollout.

## 4. Alternatives considered

| Alternative | Why rejected |
|---|---|
| CI lint only, keep the four sources | The band-aid, not the cure — ADR-031 W5 is exactly that and stays reactive. D-3 makes the bad state unrepresentable instead. |
| Hard rename cutover (no aliases) | Breaks agent templates that have not re-rendered. D-2's soft path is the precedent. |
| Document "pick either convention" | That *is* the status quo — the audit's starting point. |
| Polymorphic dispatch accepting both names indefinitely | Permanent two-world catalog; defeats the predictability goal. |
| Fiat the `noun.verb` delimiter | Anthropic's guidance is explicit that the scheme is eval-sensitive; an earlier draft did this and it was the un-grounded call (discussion §3). |

## 5. Implementation

Sequencing is the load-bearing constraint: **ADR-031 W2 rewrites
every catalog entry to add the `short` field.** A naming migration
(D-1/D-2) also touches every entry. D-1/D-2 must therefore be
**resolved before ADR-031 W2 runs, or fused into it** — otherwise
all ~75 entries are edited twice (discussion §6, Q5).

Companion rollout plan: TBD. Sketch — (W1) introduce the `toolSpec`
type and migrate one domain end-to-end as proof; (W2) migrate the
remaining domains into the single registry + apply the D-1 naming;
(W3) D-4 deduplication; (W4, optional) the D-1 delimiter eval.

## 6. References

- [`../discussions/tool-catalog-structure.md`](../discussions/tool-catalog-structure.md)
  — the audit, the re-grounded naming analysis (§3), and the Q2
  duplicate-pair findings this ADR carries.
- [ADR-031](031-agent-tool-ergonomics.md) — agent-facing tool
  ergonomics; this ADR is its contributor-facing counterpart and
  W2-blocking dependency.
- [ADR-032](032-message-routing-envelope.md) — the v1.1.0 cutoff
  shim, precedent for D-2.
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md)
  — the make-bad-states-unrepresentable principle behind D-3.
- [`../reference/hub-mcp.md`](../reference/hub-mcp.md) — §3 domain
  grouping, §5 the relay rule weighed in D-5.
- [MCP specification — Tools (rev. 2025-11-25)](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
  and [Anthropic — Writing effective tools for AI agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
  — the practice D-1 is grounded in.
