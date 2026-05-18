---
name: Agent tool ergonomics rollout
description: Phased rollout of the agent-tool-ergonomics design — two-tier descriptions (catalog short + tools.get long), per-persona intent → tool index in each persona prompt, hint-bearing errors on every 4xx, and a CI lint that asserts catalog × dispatcher × describe stay in lockstep. Five wedges across hub server + hubmcpserver + bundled prompts + CI; ~600 LOC + ~400 lines prose. Three-phase plan (foundation → index → polish); MVP is phases 1 + 2 (foundation + index). Companion discussion at agent-tool-ergonomics.md.
---

# Agent tool ergonomics rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18) — three phases, eight wedges total (W1, W2.a, W2.b, W3, W4, W5, W6.a, W6.b). **W1 + W2.a + W3 + W4 + W6.a shipped — MVP (phases 1 + 2) is complete bar W2.b.** W1 — `tools.get` meta-tool added server-side; the pre-existing `documents.get` missing-tier gap was closed alongside. W2.a — `tools/list` now serves the one-line `short`; the long body is fetched per-tool via `tools_get`. W3 — `Hint` envelope + `writeErrHint`; four discovery-confusable 4xx paths now carry a structured recovery hint (`search` no-results deferred — its `200`-array success shape can't carry a hint without a breaking change). W4 — all 14 persona prompts gained a `## Tools at a glance` index (10 main: full table; 4 per-engine stewards: one-line `tools_get` pointer). **Reconciled against post-[ADR-033](../decisions/033-tool-catalog-naming-and-registration.md) code** (the W6-teardown landing): ADR-033 is complete — the catalog is now the two `ToolSpec` registries, not the old four `mcpToolDefs*` sources — so §0.1, W1, W2 and W5 are rewritten to that topology. **W2's data model is done**: every `ToolSpec` carries a populated `Short` field (it rode in with the ADR-033 migration, which ADR-033's plan flagged as subsuming ADR-031 W2's per-tool work). Remaining W2 is W2.b — D-1's structured-payload fields. **W6** (sweep the dangling non-tool refs from the persona prompt bodies) was added to Phase 3 from a finding W4 surfaced; **W6.a** (the clean renames) shipped, **W6.b** (the genuine gaps + drift-lock lint) is post-MVP. Companion discussion at [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md); the failure-mode taxonomy + recommendation are there.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** W1 + W2.a + W3 + W4 shipped; ADR-033 W6 teardown complete

**TL;DR.** Close the discovery / depth / error-recovery gap
revealed by the 2026-05-18 steward incident (6 turns guessing
the right tool to read a doc by ULID). Three phases:

- **Phase 1 — Foundation (MVP, 3 wedges):** add `tools.get`
  meta-tool, split every tool's description into short + long,
  rewrite the worst 5 error paths to carry `hint` fields. ~350
  LOC + ~150 lines prose.
- **Phase 2 — Index (MVP, 1 wedge):** add "intent → tool" table
  to each of the 10 main persona prompts. ~250 lines prose, no
  code.
- **Phase 3 — Polish (post-MVP):** hint-pass on the remaining
  ~25 4xx error paths, CI lint asserting every registered tool
  has both short + long descriptions and matching schema (W5);
  plus a sweep of the dangling non-tool references in the
  persona prompts — clean renames done (W6.a), genuine gaps +
  drift-lock lint remaining (W6.b). ~200 LOC + tests + prose.

The MVP phases (1 + 2) are the minimum for the agent's
discovery experience to feel solid. Phase 3 closes the long
tail.

---

## 0. Phase / wedge summary

| Phase | # | Wedge | Approx | Depends on |
|---|---|---|---|---|
| 1 | W1 | `tools.get` meta-tool | ~80 LOC | — — **✓ shipped** |
| 1 | W2.a | `tools/list` serves `short` | ~60 LOC | W1 — **✓ shipped** |
| 1 | W2.b | D-1 structured payload | ~150 LOC | W1, W4 |
| 1 | W3 | Hint-bearing errors — top paths | ~90 LOC | — — **✓ shipped** |
| 2 | W4 | Per-persona intent → tool index | ~250 prose | W2.a — **✓ shipped** |
| 3 | W5 | Hint pass + CI lint | ~150 LOC | W1, W2, W3 |
| 3 | W6.a | Sweep dangling non-tool refs — clean renames | ~50 prose | W4 — **✓ shipped** |
| 3 | W6.b | Dangling-ref genuine gaps + drift-lock lint | ~80 LOC | W6.a |

Implementation order is **W1 ✓ → W2.a ✓ → W3 ✓ → W4 ✓ → W6.a ✓ →
W2.b (after W4) → W5 ‖ W6.b (post-MVP polish)**. MVP (phases 1 + 2)
is now complete bar W2.b; W5 and W6.b are the post-MVP polish and are
independent of each other.

---

## 0.1 Catalog topology — read before W2 / W5

The agent-facing MCP catalog is **composed**, not single.
`mcpToolDefs()` in `hub/internal/server/mcp.go` concatenates the two
ADR-033 `ToolSpec` registries:

| Source | Tools | Where |
|---|---|---|
| `RegistryCatalogDefs()` — authority registry | 66 | `hubmcpserver/toolspec.go` |
| `nativeRegistryCatalogDefs()` — native registry | 26 | `server/native_tools.go` |

Each tool is one `ToolSpec` (`Name, Aliases, Short, Description,
InputSchema, Tier, WorkerEligible, Backend`); the catalog functions
project it to the `{name, description, inputSchema}` map shape — one
entry per canonical name plus one `[DEPRECATED]` entry per alias. The
legacy four `mcpToolDefsBase/Extra/orchestrationToolDefs` +
`authorityToolDefs()` sources this plan was first written against
were deleted by the ADR-033 W6 teardown.

Consequences for the wedges:

- **W1 (shipped)** — `tools.get` resolves a name across the composed
  `mcpToolDefs()`. ADR-033 W6.2 then folded it into the native
  registry as `tools_get` (canonical) with `tools.get` a deprecated
  alias; the handler `mcpToolsGet` (`server/mcp.go`) still reads the
  composed catalog.
- **W2.a (shipped)** — `RegistryCatalogDefs()` /
  `nativeRegistryCatalogDefs()` now emit a `short` key alongside the
  long `description`; the new `mcpToolListDefs()` projection serves
  `short` (in both the `short` and MCP-standard `description` keys)
  over `tools/list`, while `mcpToolsGet` keeps reading the full
  `mcpToolDefs()` so `tools_get` returns the long body. **W2.b** —
  D-1's structured-payload fields — see W2 below.
- **W5** — the catalog lint walks the composed `mcpToolDefs()`, now
  exactly the two registries. ADR-033's CI-locks (`TestToolRegistry_*`,
  `TestNativeRegistry_*`) already enforce catalog × dispatch lockstep,
  so W5 layers the description/schema rules on top rather than
  re-proving lockstep.
- The standalone `hubmcpserver` daemon (`run.go` `tools/list`) now
  serves `RegistryCatalogDefs()` too (ADR-033 W6.5) — authority-only
  by construction but no longer name-divergent. Still the dev/debug
  path; agents reach the full surface via the in-process catalog.

---

## 1. Wedges in detail

### Phase 1 — Foundation

#### W1 — `tools.get` meta-tool — ✓ shipped

> **Shipped** `dc38f37`; ADR-033 W6.2 (`e89fb6d`) later folded it into
> the native registry as `tools_get` (canonical) with `tools.get` a
> deprecated alias. Handler `mcpToolsGet` enumerates the composed
> `mcpToolDefs()`. Tests `TestMCP_ToolsGet_*` in `mcp_tools_get_test.go`.
> The section below is the original design, kept for context.

A new MCP tool: given a name, return the full description body.
Mirrors `tools/list` in shape; the difference is that
`tools/list` returns short descriptions (~1 line each) per W2,
while `tools.get` returns the full body (shape, examples,
failure modes, see-also).

**Surface (MCP tool definition):**

- Name: `tools.get`
- Input schema: `{tool_name: string}` (required)
- Returns: `{name, short, long, schema, examples, see_also}`

**Implementation site:** register `tools.get` **server-side** (see
§0.1) — an entry in `mcpToolDefsExtra()` (`server/mcp_more.go`) plus
a `case "tools.get"` in `dispatchTool` (`server/mcp.go`). The handler
enumerates the composed `mcpToolDefs()`, finds the named entry, and
returns its description body. Until W2 lands the `short` / `long`
split, `tools.get` returns the single existing `description` field.

**Acceptance:**
- `mcp__termipod__tools.get(tool_name="documents.get")`
  returns the full v1.0.630 description body.
- Unknown name returns `is_error: true` with body `"unknown tool
  'X'; call tools/list for the available set"`.

**Tests:** `TestMCP_ToolsGet_Known`,
`TestMCP_ToolsGet_Unknown` in `internal/server`
(`mcp_tools_get_test.go`).

#### W2 — Two-tier descriptions + D-1 structured payload

**Already done (rode in with ADR-033).** `ToolSpec` carries a `Short`
field and it is populated for all 92 tools — the short ↔ long data
model D-1 specifies exists. What is *not* done is emitting `short`
and the rest of D-1's structured payload.

**W2.a — `tools/list` serves `short` — ✓ shipped.**
`RegistryCatalogDefs()` / `nativeRegistryCatalogDefs()` now emit a
`"short"` key (from `ToolSpec.Short`) alongside the long
`description`. A new projection `mcpToolListDefs()` (`server/mcp.go`)
substitutes `short` into the MCP-standard `description` field and
drops the long body; `tools/list` serves that. `tools_get`
(`mcpToolsGet`) keeps reading the full `mcpToolDefs()`, so it still
returns the long `Description`. `description` stays present and
meaningful in `tools/list`, so a client that reads only `description`
keeps working (no backward-incompat break).

**Verified sizes — the win is the description bytes, not the wire
total.** Measured across 167 catalog entries: the summed long
descriptions are **63.5 KB**; the summed `short`s are **13.7 KB** —
that ~50 KB is what no longer ships in every dispatch's context. The
*whole* `tools/list` wire only drops 129 KB → 78 KB, because
`inputSchema` (~52 KB) is the real bulk and the MCP `tools/list` spec
**requires** it — clients need each schema to construct calls, so it
cannot be projected out. (The plan's earlier "~30KB → ~5KB" figure
ignored `inputSchema`; corrected here. Shrinking the schema payload
is a separate concern, out of scope for ADR-031.)

Convention for `short` (already followed by the populated fields,
restated for new tools): one present-tense sentence, contract only,
names required params + canonical input type; ≤ 200 chars (the
widest in the catalog today is 181). Example:
`Fetch a document by id. Required: document_id (ULID).`

**W2.b — D-1 structured payload.** D-1 specifies `tools.get` returns
more than short + long: `examples`, `failure_modes`, `see_also`, and
the operational metadata `concurrency_safe` / `side_effecting`
(`permission_tier` is already covered by `ToolSpec.Tier`). This adds
these as `ToolSpec` fields and populates them per tool (~92 tools of
authoring — large).

**Sequenced after W4** (the split, taken at W2.a ship time): the
per-persona index (W4) is the higher-leverage discovery fix and does
not depend on the structured payload, and W2.b's `failure_modes`
authoring naturally fuses with the W3 / W5 hint work (same
`{hint_text, see_tool}` shape).

**Acceptance (W2.a) — met:**
- `tools/list` carries `short` (≤ 200 chars) in place of the long
  `description`; the long-body bytes (~50 KB) no longer ship in the
  catalog. ✓
- `tools_get` still returns the full long body. ✓
- No `Short` exceeds 200 chars. ✓ (widest 181)

**Tests (shipped):** `TestMCP_ToolListDefs_ServesShort`
(`mcp_tools_get_test.go`) — asserts every `tools/list` entry's
`description` equals its `short`, the projection drops no tools, the
summed list descriptions are smaller than the full catalog's, and
`tools_get` returns a longer body than `short` for the widest tool.
`hubmcpserver`'s `TestToolsList_RoundTrip` updated to assert each
entry carries a non-empty `short`.

#### W3 — Hint-bearing errors — top paths — ✓ shipped

> **Shipped.** The `Hint` envelope + `writeErrHint` helper landed in
> `server/server.go`; four of the five paths below carry a hint. Path 3
> (`search` no-results) is **deferred** — see the note under it.

Add a structured recovery `hint` to the worst error paths surfaced by
the 2026-05-18 incident and similar:

1. **✓** `get_project_doc` 404 (`handleGetProjectDoc`, file not found) →
   hint names `documents_get`: a ULID is not a filesystem path, fetch
   it by id instead.
2. **✓** `documents_get` 404 (`handleGetDocument`, no such row) → hint
   names `get_project_doc`: for a file under the project's `docs_root`,
   use the filesystem-path tool.
3. **deferred** `search` no-results → "try a shorter query; or list via
   `get_feed` / `documents_list`". **Why deferred:** `GET /v1/search`
   returns a JSON *array* on success, and a zero-result search is a
   `200`, not a `4xx`. Attaching a `hint` means changing the success
   shape to an object (`{results: [...], hint: {...}}`) — a breaking
   change to a list endpoint, inconsistent with every other list tool.
   Out of scope for a hint-on-4xx wedge; revisit if/when `search`'s
   response shape is reworked, or surface it as its own decision.
4. **✓** Role-gate denial on a worker-called tool (`authorizeMCPCall`,
   `roleDeniedErr`) → the message names the tool and the escalation
   path: `request_help(target='@<parent_handle>', question=...)`.
5. **✓** `agents_spawn` 422 (missing `backend.cmd`) → the existing
   error (which already names the field, v1.0.620 W4) gained a tail:
   "Call `tools_get('agents_spawn')` for the full input shape."

**Hint envelope.** Per ADR-031 D-3 the hint is a *nested* structured
object on the 4xx body:

```go
writeErrHint(w, status, message, Hint{HintText: "...", SeeTool: "..."})
// Hint{HintText string `json:"hint_text"`;
//      SeeTool  string `json:"see_tool,omitempty"`;
//      SeeDoc   string `json:"see_doc,omitempty"`}
// → {"error": "...", "hint": {"hint_text": "...", "see_tool": "..."}}
```

`hint_text` is required when `hint` is present; `see_tool` / `see_doc`
are optional but at least one of the three carries actionable signal.
A client that ignores `hint` still reads `error` exactly as before.

**Exception — the role gate (path 4).** A JSON-RPC error reaches the
agent only as `message`; the `data` field is not reliably surfaced to
the model. So the role-gate hint is folded into the `jrpcError.Message`
text rather than a nested `Hint` envelope — the envelope is an
HTTP-body construct. `roleDeniedErr(role, tool)` builds the message.

**Tests (shipped):** `mcp_error_hints_test.go` —
`TestErrorHint_GetDocument_NotFound`,
`TestErrorHint_GetProjectDoc_NotFound`,
`TestErrorHint_RoleDenied_NamesEscalation`; plus a `tools_get`
assertion added to `TestDoSpawn_FailFast_NoBackendBlock`.

### Phase 2 — Index

#### W4 — Per-persona intent → tool index — ✓ shipped

> **Shipped.** All 14 persona prompts under `hub/templates/prompts/`
> were updated: the 10 main personas gained a full `## Tools at a
> glance` table; the 4 per-engine stewards gained the one-line
> `tools_get` pointer (see the standalone finding below). Names are
> ADR-033 canonical — `TestBundledTemplatesUseCanonicalNames` and
> `TestAuditBundledTemplateVarRefs` stay green.

Each of the 10 main persona prompts gains an "Intent → tool"
section, listing 8-16 (intent, tool) pairs tailored to that
persona's actual surface (stewards: spawn / plan / delegate;
workers: docs / runs / report-up).

**Persona prompts in scope** — `hub/templates/prompts/` holds 15
files; the 10 *main* personas get the full index:
- `steward.v1.md`, `steward.general.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md` (4 stewards).
- `coder.v1.md`, `critic.v1.md`, `lit-reviewer.v1.md`,
  `ml-worker.v1.md`, `paper-writer.v1.md`, `briefing.v1.md`
  (6 workers).

**Not full-index personas (5 files):**
- `steward.codex.v1.md`, `steward.gemini.v1.md`,
  `steward.kimi.v1.md`, `steward.claude-m4.v1.md` — per-engine
  stewards. **Confirmed at W4 time:** each agent template carries a
  single `prompt:` field (no `context_files` stacking), so these are
  **standalone full prompts**, not thin overlays composing on
  `steward.v1.md` — they do *not* inherit the index. Per the plan's
  standalone branch, each got the one-line `tools_get` pointer rather
  than the full table: the engine-specific prompts already carry
  their own tool listing, and the pointer ("call `tools_get` /
  `tools/list`, don't guess") is the load-bearing teaching; a fourth
  hand-curated copy of the steward table would just drift.
- `worker_report.v1.md` — a report template, not a persona;
  excluded.

**Section template (steward example):**

```markdown
## Tools at a glance

Quick map from intent → tool. Call `tools_get(name)` for
shape + examples before invoking a tool you don't recall.

| Intent | Tool |
|---|---|
| Read a doc by ULID | documents_get |
| Read a filesystem file under project docs_root | get_project_doc |
| List recent project activity | get_feed |
| Search by text content | search |
| Read this agent's journal | journal_read |
| Read a delegated task's body | tasks_get |
| Read attention/approval queue | get_attention |
| Look up an agent by handle | agents_list |
| Update a task you assigned | tasks_update |
| Read what a worker reported | documents_get with doc_id from tasks_complete summary |
| Escalate something you can't decide | request_help |
| Direct-message a peer steward | a2a_invoke |
| Spawn a worker | agents_spawn |
```

(Tool names above are the ADR-033 canonical `snake_case` forms — the
W4 index must use these, not the deprecated dotted aliases, or the
`TestBundledTemplatesUseCanonicalNames` drift-lock fails.)

**Convention:**
- ~15 lines max per index (don't enumerate all 50 tools).
- Intents in principal's mental vocabulary, not termipod jargon.
- Tool names verbatim (no `mcp__termipod__` prefix).
- One section per persona; no duplication via context_files
  unless the persona is purely a delegator.

**Implementation site:** each `hub/templates/prompts/*.md` file
gets a `## Tools at a glance` section inserted before any
existing "## Available tools" or "## Authority" section.

**Acceptance:** every main persona prompt has the section;
audit lint (auditBundledTemplateVarRefs unchanged — index is
plain markdown, no `{{var}}` refs introduced).

**Tests:** the existing `TestAuditBundledTemplateVarRefs`
suite must continue to pass.

**Finding — dangling non-tool refs in the prompt bodies.** Authoring
the index surfaced that the persona *bodies* still cite names that
are neither canonical tools nor deprecated aliases — `documents.read`
(should be `documents_get`), `runs.attach_metric_uri`,
`attention.create`, `plan.advance`, `agents.archive`,
`runs.register` / `runs.complete`. The drift-lock only catches
*deprecated aliases*, so these slip past it; a worker that calls
`documents.read` gets `unknown tool`. The W4 indexes use the correct
canonical names, but the surrounding prose was **not** swept — that
is a separate correctness wedge (judgement-heavy: some refs have no
clean canonical target). Fixed in **W6** below — kept a separate
wedge so the W4 index addition stays a reviewable atomic diff.

### Phase 3 — Polish (post-MVP)

#### W5 — Hint pass + CI lint

**Hint pass:** apply the W3 pattern to the remaining ~25 4xx
error paths in `handlers_*.go`. Each gets a `hint` field
naming the likely correct tool or recovery action. Mostly
mechanical.

**CI lint:** new test that walks every registered MCP tool and
asserts:
- Has a non-empty `Short`.
- Has a non-empty `Description` (long).
- Short ≤ 200 chars.
- Long contains a worked example (`\`\`\`json` or `\`\`\`yaml` block).
- Every parameter named in the long body is in `InputSchema.properties`.
- Every parameter in `InputSchema.required` is named in the long body.

(The last two are the "description ↔ schema audit" mentioned in
`validate-at-every-boundary.md` §3 Layer 4 — long-deferred,
finally lands.)

**Implementation site:** new test file
`hub/internal/server/tool_catalog_lint_test.go` — it walks the
composed `mcpToolDefs()` (§0.1), i.e. both `ToolSpec` registries
(92 tools). ADR-033's CI-locks already prove catalog × dispatch
lockstep, so this lint adds only the description/schema rules.

**Acceptance:** lint fails on any tool that violates one of
the rules; passes for all bundled tools after the hint pass.

**Tests:** the lint itself is the test. Plus a few negative
tests to verify each rule catches violations.

#### W6 — Sweep dangling non-tool refs from persona bodies

W4 surfaced it: the persona prompt *bodies* (and the agent-template
`default_capabilities` lists) cite tool-shaped names that resolve to
**nothing** — neither a canonical tool nor a deprecated alias.
`TestBundledTemplatesUseCanonicalNames` only catches deprecated
*aliases*, so a dangling name sails through; an agent that calls it
gets `unknown tool` mid-task. Split in two: W6.a clears the
mechanical renames, W6.b resolves the genuine gaps and adds the lint.

#### W6.a — Clean renames — ✓ shipped

> **Shipped.** `documents.read`, `agents.archive`, `runs.register`,
> and `attention.create(kind=…)` swept across `templates/prompts/*.md`
> + `templates/agents/*.yaml`. Drift-lock + var-ref tests stay green.

Refs with an unambiguous canonical target — a pure rename:

| In the bundled prompt | → Canonical |
|---|---|
| `documents.read` | `documents_get` |
| `agents.archive` | `agents_terminate` |
| `runs.register` | `runs_create` |
| `attention.create(kind=request_help\|request_select\|request_approval)` | `request_help` / `request_select` / `request_approval` (the `kind=` arg is dropped — the kind *is* the tool) |

#### W6.b — Genuine gaps + drift-lock lint — post-MVP

The refs left after W6.a name an operation with **no tool behind it**
— do not paper over them with a near-miss tool:

| Ref | Why it's a gap |
|---|---|
| `runs.complete` | no run-status-update MCP tool exists |
| `plan.advance` / `plan.instantiate` | no plan-lifecycle MCP tool — `plan_steps_update` is only the nearest |
| `runs.attach_metric_uri` | a REST endpoint (`POST …/metric_uri`, `handleAttachMetricURI`) exists but is **not** exposed as an MCP tool — agents can't call it |
| `run.metrics.read` | listed in `roles.yaml worker.allow` but not in the 92-tool catalog — a dead manifest entry or an unregistered tool |
| `templates.read` | ambiguous — the real tools are per-kind (`templates_agent_get`, …) |

Each names a real workflow need with no MCP surface. Per the
CLAUDE.md "choose terms precisely / surface the gap" conventions,
W6.b resolves each as a discussion note or ADR gap (register the
missing tool, or reword the prompt to name the real recovery path) —
not a silent map to a near-miss tool.

**Lint — drift-lock the sweep.** Extend the bundled-template check
(or add a sibling) so it flags any backticked, tool-shaped token in
`templates/prompts` + `templates/agents` that resolves to neither a
canonical name nor a deprecated alias. "Tool-shaped" needs a precise
rule to avoid false positives on ordinary identifiers — scope it to
backticked tokens matching the `verb_noun` / `noun.verb` tool-naming
shapes, with a small explicit allowlist for engine-native tools
legitimately outside the hub catalog (`WebSearch`, `WebFetch`,
`Bash`, `Read`, `Edit`, `git`) and for the W6.b gap tokens until
they are resolved.

**Acceptance (W6.b):** every gap token is resolved or explicitly
allowlisted with a tracking pointer; the lint fails if a *new*
dangling ref is introduced.

**Tests:** the lint is the test; a negative case proving it catches
a planted dangling ref.

---

## 2. Out of scope for this plan

- **Tool renaming.** If `documents.list` and `get_feed` have
  intent overlap, the per-persona index helps the agent pick;
  renaming for clarity is a separate wedge.
- **Polymorphic input acceptance** (the `documents.get(id_or_path)`
  temptation). Discussion §3 explicitly recommends against; not
  in this plan.
- **Backward-incompat catalog change.** This plan ships both
  fields (`short` and `description`). Clients that only read
  `description` keep working.
- **Engine-side prompt rules** for using `tools.get`. The
  index in W4 implicitly teaches "call tools.get before
  invoking unfamiliar tools"; an explicit rule is left for the
  persona's discretion. (If on-device testing shows agents
  don't reach for tools.get naturally, follow-up to make
  the index header bolder.)

---

## 3. Risks

- **Catalog short might still grow** — once contributors realize
  short is the always-loaded version, they may stuff it. The
  W5 lint caps at 200 chars; PR review enforces the spirit.
- **Hint strings rot** — when a referenced tool is renamed, the
  hint silently lies. Mitigation: the W5 lint also walks all
  hint `see_tool` references and asserts they exist in the
  catalog.
- **Per-persona index drift** — when a new tool is added, no
  index is automatically updated. Acceptable — the indexes are
  curated for the persona's needs, not exhaustive. Audit pass
  during big catalog changes.
- **The `tools.get` round-trip cost** — one extra MCP call
  per unfamiliar tool. Bounded; agents typically use 5-10 tools
  per task, ~1-2 unfamiliar. Net win vs ~30KB every dispatch.

---

## 4. Acceptance for the bundle

The plan ships as one or two releases (MVP = phases 1 + 2 in
one release; phase 3 in a follow-up):

- A steward can read back a doc by ULID in one tool call:
  `documents.get(document_id=<from tasks.complete summary>)`.
  No guessing.
- A steward who tries `get_project_doc(path=<ULID>)` gets an
  error naming `documents.get` as the right tool, recovers
  next turn.
- `tools/list` carries one-line `short`s, not the long bodies —
  the ~50 KB of long-description text no longer ships in every
  dispatch's context.
- The persona "Tools at a glance" section is present in all 10
  main prompts.
- The CI lint passes for all tools.

Verification on-device with the same smoke task from
2026-05-18: ask steward to read back a doc by ULID. Steward
should hit `documents.get` first try.

---

## 5. References

- [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md)
  — the framing this plan implements.
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md)
  §3 Layer 4 — the description ↔ schema audit lint that finally
  lands as W5.
- [`../reference/hub-mcp.md`](../reference/hub-mcp.md) — the
  current MCP surface reference; this plan's two-tier split
  applies to the catalog enumerated there.

The MCP description hygiene rule (descriptions are present-tense
contract; no version markers, no doc references, no rationale
prose — v1.0.621) is compatible with this plan: the short
field stays pure contract, the long field can carry worked
examples. The catalog × dispatcher × handler lockstep rule
(v1.0.591 burn — request_project_steward dispatcher case
shipped without tools/list entry) is what W5's lint enforces
across the new short/long fields and the schema.
