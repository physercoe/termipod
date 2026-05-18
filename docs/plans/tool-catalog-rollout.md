---
name: Tool catalog rollout
description: Phased rollout of ADR-033 — collapse the MCP tool catalog's four sources in two shapes into one typed ToolSpec registry, migrate every tool to the snake_case resource-first naming convention behind grandfathered aliases, consolidate the three verified duplicate pairs, and CI-lock dispatch so the four-place lockstep defect class becomes unrepresentable. Six wedges in three phases (foundation+proof / domain migration / consolidate+teardown). Because each migration wedge authors the ADR-031 D-1 structured payload (short/long/failure_modes) as it converts a domain, ADR-031 W2 is subsumed — it does not run as a separate pass. Companion to ADR-033 and the tool-catalog-structure discussion.
---

# Tool catalog rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18) — seven wedges, three phases (W4 split). **W1–W4 + W4n shipped** — every MVP tool is now in the unified `ToolSpec` registry: 48 authority-backed tools (`hubmcpserver`'s registry) + 28 native switch-dispatched tools (the `server`-side native registry, W4n). All carry `snake_case` names, dotted names kept as deprecated aliases; catalog, tier, and worker role-eligibility derive from the spec; CI-lock tests guard both registries. W3 closed the security gotcha — `dispatchTool` resolves the canonical name before the `agents_spawn` / `a2a_invoke` literal-name gates. W4n built the native-dispatch path: a `server`-side `nativeHandlers` map keyed by canonical name, CI-locked mutually exhaustive with the native `ToolSpec` list; the `dispatchTool` switch shrank to the protocol cases + `tools.get`. **Remaining: W5** (D-4 dedup of the three duplicate pairs) and **W6** (delete the legacy four-source assembly). Implements [ADR-033](../decisions/033-tool-catalog-naming-and-registration.md); the decision rationale and the catalog audit are there and in the [tool-catalog-structure discussion](../discussions/tool-catalog-structure.md).
> **Audience:** contributors · QA
> **Last verified vs code:** v1.0.630-alpha (+ ADR-031 W1 `tools.get`)

**TL;DR.** ADR-033 locks the foundation under the MCP tool catalog.
This plan executes it in six wedges:

- **Phase 1 — Foundation + proof (W1).** Define the `ToolSpec` type
  and the single registry; derive the catalog, tiers, and the
  roles cross-check from it; CI-lock dispatch. Migrate the
  `documents` domain end-to-end as the proof.
- **Phase 2 — Domain migration (W2–W4).** Convert the remaining
  ~70 tools into the registry, domain group by domain group, each
  with its `snake_case` name, its alias, and its full ADR-031 D-1
  payload.
- **Phase 3 — Consolidate + tear down (W5–W6).** Resolve the three
  duplicate pairs (D-4); delete the legacy four-source assembly and
  the dispatch `switch`.

**ADR-031 W2 is subsumed.** A `ToolSpec` carries `short` / `long` /
`failure_modes` (the ADR-031 D-1 structured payload) by type — so
converting a domain to `ToolSpec` *is* doing W2 for that domain.
W2 never runs as a separate pass. See §1.5.

---

## 0. Phase / wedge summary

| Phase | # | Wedge | Approx | Depends on |
|---|---|---|---|---|
| 1 | W1 | `ToolSpec` type + registry + derivation + dispatch CI-lock; `documents` domain migrated as proof | ~400 LOC | — |
| 2 | W2 | Migrate `projects` / `plans` / `runs` / `artifacts` | ~250 LOC (high-churn) | W1 |
| 2 | W3 | Migrate `agents` / `hosts` / `reviews` / `channels` / `a2a` | ~250 LOC (high-churn) | W1 |
| 2 | W4 | Migrate the authority-backed `tasks` / `schedules` / misc tools (`audit.read`, `policy.read`, `mobile.navigate`, channel-creation) | ~200 LOC | W1 |
| 2 | W4n | **Native-dispatch path** + migrate the ~28 switch-dispatched native tools (messaging, lifecycle, attention, `templates.propose`, `get_task`, `list_agents`, `agents.fanout/gather`, …) | ~350 LOC (structural) | W1 |
| 3 | W5 | D-4 — consolidate the three duplicate pairs | ~200 LOC | W2, W3, W4n |
| 3 | W6 | Delete the legacy four-source assembly + dispatch `switch`; settle the standalone daemon | ~250 LOC (mostly deletion) | W2–W5 |

Order: **W1 → W2/W3/W4/W4n (parallelisable, each a disjoint tool
set) → W5 → W6**. The whole catalog keeps working throughout — old
names resolve as aliases (D-2), and the legacy assembly is removed
only in W6, after every tool has a `ToolSpec`.

**W4 was split.** W1–W4 migrated only *authority-backed* tools —
those with a `buildTools()` REST adapter, dispatched via
`dispatchAuthorityToolRaw`. The remaining native tools are
switch-dispatched `(*Server)` methods; the registry has no native
handler path (`ToolSpec.Backend=""` cannot dispatch). Building that
path — the `map[string]handler` W1 described but deferred — is its
own structural wedge, **W4n**, on which W5 now depends.

---

## 1. Wedges in detail

### Phase 1 — Foundation + proof

#### W1 — `ToolSpec` type, registry, derivation, dispatch CI-lock

**The type.** One value per tool, carrying everything except the
handler body:

```go
type ToolSpec struct {
    Name        string          // snake_case, resource-first (D-1)
    Aliases     []string        // deprecated old names (D-2)
    Short       string          // ADR-031 D-1 — catalog short
    Description string          // ADR-031 D-1 — long body
    InputSchema json.RawMessage
    Examples    []ToolExample
    FailureModes []FailureMode  // {code, when, hint}
    SeeAlso     []string
    Tier        string          // replaces tiers.go toolTiers
    Concurrency Safety           // concurrency_safe / side_effecting / permission_tier
    WorkerEligible bool          // default role eligibility (D-3)
    Dispatch    DispatchKind     // Native | Authority
}
```

**Registry location.** The spec list must be readable by both the
in-process `server` catalog and the standalone `hubmcpserver`
daemon. Given the existing import direction (`server` imports
`hubmcpserver`, not the reverse), the `ToolSpec` type and the spec
list live in `hubmcpserver` (or a new leaf package both import).
The first task of W1 is to settle this — it is the one open
structural choice; everything else follows.

**Derivation — what the one declaration produces:**
- **Catalog.** `mcpToolDefs()` is generated by mapping the spec
  list → the `tools/list` shape. The four-source concatenation
  (`base` + `extra` + `orchestration` + `authority`) is replaced;
  removal of the old funcs is W6.
- **Tiers.** `tierFor()` reads `ToolSpec.Tier`. The `toolTiers`
  map in `tiers.go` is deleted; `TestEveryCatalogEntryHasTier`
  becomes structurally true (every spec has a `Tier` field) and
  is removed.
- **Roles.** `ToolSpec.WorkerEligible` declares the default. A CI
  test asserts the bundled `roles.yaml` worker allow-set agrees
  with the specs. Per-deployment `roles.yaml` overrides
  (`hub-mcp.md` §4, hot-reloadable) stay authoritative at runtime —
  the spec declares the default, the manifest can still override.

**Dispatch — CI-locked, not generated.** A native tool's handler is
a `(*Server)` method and cannot live in a spec value in the
`hubmcpserver` package (import cycle). So dispatch stays a
`map[string]handler` built at `server` init: native specs wire to
their `(*Server)` method, `Authority` specs wire to one shared
handler that forwards via `dispatchAuthorityToolRaw`. The
explicit `switch` in `dispatchTool` is retired in W6. Two CI tests
lock it: `TestEverySpecHasHandler` and `TestEveryHandlerHasSpec`.
This is the structural retirement of the four-place lockstep class
(ADR-033 D-3) — and it subsumes ADR-031 W5's catalog lint.

**Proof — the `documents` domain.** Migrate `documents.list` /
`documents.get` / `documents.create` to `ToolSpec`: new
`snake_case` names (`documents_list`, `documents_get`,
`documents_create`), old dotted names as aliases, full ADR-031 D-1
payload authored. `documents` is chosen because it is small (3
tools) and is the domain of the 2026-05-18 incident that started
this whole thread.

**Acceptance:** `documents_*` tools resolve under their new names;
the dotted names resolve as aliases with a `[DEPRECATED]` short;
`tools/list`, `tierFor`, and the roles check for those three derive
from the specs; both CI-lock tests pass.

#### Phase 2 — Domain migration

W2 / W3 / W4 each take a disjoint set of domains and migrate every
tool in them to `ToolSpec`, identically to the W1 `documents`
proof: author the spec, apply the D-1 `snake_case` resource-first
name, register the old name as a D-2 alias, write the full ADR-031
D-1 payload (`short` / `long` / `examples` / `failure_modes` /
`see_also` / safety flags / tier).

The split is by domain so the three wedges touch disjoint files and
can land in parallel:

- **W2 — `projects` / `plans` / `runs` / `artifacts`.**
- **W3 — `agents` / `hosts` / `reviews` / `channels` / `a2a`.**
- **W4 — authority-backed `tasks` / `schedules` + misc
  (`audit.read`, `policy.read`, `mobile.navigate`,
  `project_channels.create`, `team_channels.create`).**
- **W4n — the native switch-dispatched tools:** messaging
  (`post_message`, `post_excerpt`), lifecycle (`pause_self`,
  `shutdown_self`), attention (`request_approval`, `request_select`,
  `request_help`, `get_attention`), `templates.*`, `get_task`,
  `update_own_task_status`, `get_feed`, `search`, `journal_*`,
  `delegate`, `attach`, `get_event`, `get_parent_thread`,
  `permission_prompt`, `reports.post`, and the W3-leftover natives
  `list_agents` / `agents.fanout` / `agents.gather` /
  `list_channels`.

**W4n is structural, not just churn — SHIPPED.** A native tool's
handler is a `(*Server)` method; the `ToolSpec` lives in
`hubmcpserver`, which `server` imports (not the reverse), so the
handler cannot live in the spec. W4n added `server/native_tools.go`:
a `nativeHandlers` `map[string]nativeHandler` keyed by canonical
name (the data form of the old switch), the `nativeToolRegistry()`
spec list (with `Description` + `InputSchema` pulled from the
existing `mcpToolDefsBase/Extra/orchestration` maps so no schema
drift), and `lookupToolSpec` — the combined lookup over both
registries that `tierFor` / `authorizeMCPCall` / the catalog now
use. `dispatchTool`'s `default` resolves a native spec and calls
its handler; the switch shrank to the protocol cases + `tools.get`.
CI-lock: `TestNativeRegistry_EverySpecHasHandler` /
`_EveryHandlerHasSpec` make the map and the spec list mutually
exhaustive.

**Naming deferral.** W4n kept each native tool's current name as
canonical and only flattened the three dotted names
(`agents.fanout`→`agents_fanout`, `agents.gather`→`agents_gather`,
`reports.post`→`reports_post`). A resource-first pass over the
verb-first names (`get_feed`→`feed_get`, …) is deferred until after
W5: flipping `get_task`→`tasks_get`, `list_agents`→`agents_list`,
`get_audit`→`audit_read` now would collide with the W4 authority
tools of those exact names — W5's D-4 dedup resolves the collision
first.

**Bundled-template sweep is batched, not per-wedge.** W1–W3 left
the templates under `hub/templates/` and
`hub/internal/agentfamilies/` on the dotted names — the deprecated
aliases keep every template working. Updating templates one wedge
at a time would leave the corpus in a half-renamed mix; instead a
single sweep renames every reference once the catalog migration is
complete (W6, or a dedicated sweep before it).

**Acceptance (each wedge):** every tool in the domain set has a
`ToolSpec`; old names resolve as aliases; the CI-lock tests still
pass; `go test ./...` green.

#### Phase 3 — Consolidate + tear down

#### W5 — D-4: consolidate the three duplicate pairs

Per the verified audit (discussion §2.2):
- **`agents_list`** absorbs the one field `list_agents` had
  uniquely (`pane_id`); `list_agents` becomes a deprecated alias
  of `agents_list`.
- **`tasks_get`** is merged to return the field *union* of the old
  `get_task` and `tasks.get` (`priority`, `plan_step_id`,
  `source`, `milestone_id`, `parent_id`, `assignee_id`,
  `created_by`); pick one canonical input shape; `get_task` and the
  old `tasks.get` both alias to it.
- **`audit_read`** gains `get_audit`'s `action` filter and a
  reconciled `limit` cap; `get_audit` becomes a deprecated alias.

**Acceptance:** the three operations each resolve to one tool;
no field is lost relative to either old twin; old names alias.

#### W6 — Delete the legacy assembly

Remove `mcpToolDefsBase()`, `mcpToolDefsExtra()`,
`orchestrationToolDefs()`, the `authorityToolDefs()` composition,
and the explicit `switch` in `dispatchTool` — all now dead, every
tool served and dispatched through the registry. Settle the
standalone `hubmcpserver` daemon: either point its `tools/list` at
the shared registry or scope it down explicitly (it is the
dev/debug path — discussion §0.1).

**Acceptance:** one registry is the only source of catalog,
dispatch, tier, and role-eligibility; `grep` finds no second tool
list; `go test ./...` green.

### 1.5 Relationship to ADR-031 — W2 is subsumed

ADR-031's rollout plan has W2 = "add a `short` field to every
catalog entry." The `ToolSpec` type **carries `Short` (and `long`,
`examples`, `failure_modes`, `see_also`, the safety flags) by
design**. Converting a domain to `ToolSpec` in W1–W4 of *this* plan
authors that payload. Therefore:

- **ADR-031 W2 does not run as a separate pass** — it is done,
  domain by domain, by this plan's W1–W4.
- **ADR-031 W5** (catalog lint) is subsumed by W1's
  `TestEverySpecHasHandler` / `TestEveryHandlerHasSpec` plus the
  type itself (every spec has `short` + `long` + schema by
  construction).
- **ADR-031 W1** (`tools.get`) already shipped and keeps working —
  it reads `mcpToolDefs()`, which becomes registry-derived.
- **ADR-031 W3** (hint-bearing 4xx errors) and **W4** (per-persona
  intent index) are independent of the catalog's structure and
  proceed on their own schedule.

So after this plan lands, ADR-031's open scope is just W3 + W4.
Its rollout plan should be updated to record the subsumption when
W1 of this plan starts.

---

## 2. Out of scope

- **Domain-based code file layout** (discussion O-C) — the registry
  is one list; splitting it into per-domain files is cosmetic and
  deferred.
- **The D-1 delimiter eval** — `snake_case` is decided (ADR-033
  D-1); an eval is a post-MVP option, not part of this plan.
- **Tool consolidation beyond D-4** — merging non-duplicate tools
  into workflow-shaped tools (ADR-033 D-5's exception path) is a
  per-case ADR decision, not a blanket wedge here.
- **ACP / engine-side catalogs** — this plan covers the hub MCP
  catalog. Engine-native tool surfaces (claude-code's `Bash` etc.)
  are unaffected.

## 3. Risks

- **Alias completeness.** Every old name must resolve, or an agent
  mid-task (or an unrendered template) breaks. Mitigation: W1's CI
  adds `TestEveryRetiredNameAliases` — the rename diff is the
  source, every removed name must appear in some `ToolSpec.Aliases`.
- **`roles.yaml` divergence.** The spec's `WorkerEligible` and the
  bundled manifest can drift. Mitigation: the W1 CI test; the
  manifest stays authoritative for runtime overrides by design.
- **Big high-churn migration.** W2–W4 touch ~70 tools. Mitigation:
  disjoint domain sets, each wedge independently green; aliases
  mean a half-migrated catalog still works.
- **Standalone daemon.** `hubmcpserver`'s `buildTools()` is a
  second catalog today. Until W6 settles it, W1 must not let the
  two diverge — W1 either routes the daemon through the registry
  immediately or freezes `buildTools()` and flags it.
- **Sequencing vs ADR-031 W2.** If anyone runs ADR-031 W2 as a
  standalone pass before this plan starts, the work is thrown away.
  The ADR-031 plan already carries the gating note (commit
  `74c68be`).

## 4. Acceptance for the bundle

- One `ToolSpec` registry is the sole source of the catalog,
  dispatch routing, tiers, and default role-eligibility.
- Every tool name is `snake_case`, resource-first; every old name
  resolves as a `[DEPRECATED]` alias.
- The three duplicate pairs are one tool each; no field lost.
- `TestEverySpecHasHandler`, `TestEveryHandlerHasSpec`,
  `TestEveryRetiredNameAliases`, and the roles cross-check pass;
  `TestEveryCatalogEntryHasTier` is gone (structurally true).
- The legacy four-source assembly and the dispatch `switch` are
  deleted.
- `go build ./... && go test ./...` green.

Verification on-device: re-run the 2026-05-18 smoke task — ask a
steward to read back a doc by ULID. It should reach `documents_get`
first try, and calling the old `documents.get` should still work
and return a `[DEPRECATED]` signal.

## 5. References

- [ADR-033](../decisions/033-tool-catalog-naming-and-registration.md)
  — the five decisions this plan executes.
- [tool-catalog-structure discussion](../discussions/tool-catalog-structure.md)
  — the catalog audit (§2), current-practice grounding (§3), and the
  Q2 duplicate-pair findings W5 acts on.
- [ADR-031](../decisions/031-agent-tool-ergonomics.md) +
  [its rollout plan](agent-tool-ergonomics-rollout.md) — W2 is
  subsumed by this plan (§1.5); W3 + W4 remain independent.
- [`../reference/hub-mcp.md`](../reference/hub-mcp.md) — the current
  MCP surface; §3 domain grouping informs the W2–W4 split, §4 the
  `roles.yaml` override model, §5 the relay rule (ADR-033 D-5).
- [validate-at-every-boundary discussion](../discussions/validate-at-every-boundary.md)
  — the make-bad-states-unrepresentable principle behind W1's
  dispatch CI-lock.
