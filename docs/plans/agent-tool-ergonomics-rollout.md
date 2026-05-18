---
name: Agent tool ergonomics rollout
description: Phased rollout of the agent-tool-ergonomics design — two-tier descriptions (catalog short + tools.get long), per-persona intent → tool index in each persona prompt, hint-bearing errors on every 4xx, and a CI lint that asserts catalog × dispatcher × describe stay in lockstep. Five wedges across hub server + hubmcpserver + bundled prompts + CI; ~600 LOC + ~400 lines prose. Three-phase plan (foundation → index → polish); MVP is phases 1 + 2 (foundation + index). Companion discussion at agent-tool-ergonomics.md.
---

# Agent tool ergonomics rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18) — three phases, five wedges total. **W1 shipped** — `tools.get` meta-tool added server-side; the pre-existing `documents.get` missing-tier gap was closed alongside. **Reconciled against post-[ADR-033](../decisions/033-tool-catalog-naming-and-registration.md) code** (the W6-teardown landing): ADR-033 is complete — the catalog is now the two `ToolSpec` registries, not the old four `mcpToolDefs*` sources — so §0.1, W1, W2 and W5 are rewritten to that topology. **W2's data model is now done**: every `ToolSpec` carries a populated `Short` field (it rode in with the ADR-033 migration, which ADR-033's plan flagged as subsuming ADR-031 W2's per-tool work). Remaining W2 is small — emit `short` from the catalog functions, and add D-1's structured-payload fields. Companion discussion at [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md); the failure-mode taxonomy + recommendation are there.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** ADR-033 W6 teardown complete (HEAD `4a6e2ce`)

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
- **Phase 3 — Polish (post-MVP, 1 wedge):** hint-pass on the
  remaining ~25 4xx error paths, CI lint asserting every
  registered tool has both short + long descriptions and
  matching schema. ~150 LOC + tests.

The MVP phases (1 + 2) are the minimum for the agent's
discovery experience to feel solid. Phase 3 closes the long
tail.

---

## 0. Phase / wedge summary

| Phase | # | Wedge | Approx | Depends on |
|---|---|---|---|---|
| 1 | W1 | `tools.get` meta-tool | ~80 LOC | — — **✓ shipped** |
| 1 | W2 | Two-tier descriptions + D-1 structured payload | ~150 LOC (data model done; see W2) | W1 |
| 1 | W3 | Hint-bearing errors — top 5 paths | ~80 LOC | — |
| 2 | W4 | Per-persona intent → tool index | ~250 prose | W2 |
| 3 | W5 | Hint pass + CI lint | ~150 LOC | W1, W2, W3 |

Implementation order is **W1 ✓ → W2 → W3 (parallel) →
W4 (depends W2) → W5 (depends all)**.

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
- **W2** — the `Short` field already exists on `ToolSpec` and is
  populated for all 92 tools. The work left is (a) make
  `RegistryCatalogDefs()` / `nativeRegistryCatalogDefs()` emit
  `short` (today they emit only the long `Description`), and (b) add
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

**W2.a — emit `short` so `tools/list` shrinks.** Today
`RegistryCatalogDefs()` / `nativeRegistryCatalogDefs()` emit only the
long `Description` into the catalog map. Change them to emit a
`"short"` key (from `ToolSpec.Short`); the MCP `tools/list` response
returns `short`, while `tools.get` (`mcpToolsGet`) keeps returning
the long `Description`. This is the ~30KB → ~5KB win. Small — two
functions, ~20 LOC.

Convention for `short` (already followed by the populated fields,
restated for new tools): one present-tense sentence, contract only,
names required params + canonical input type; ≤ 200 chars. Example:
`Fetch a document by id. Required: document_id (ULID).`

**W2.b — D-1 structured payload.** D-1 specifies `tools.get` returns
more than short + long: `examples`, `failure_modes`, `see_also`, and
the operational metadata `concurrency_safe` / `side_effecting`
(`permission_tier` is already covered by `ToolSpec.Tier`). Decide at
W2 time whether to:
  - add these as `ToolSpec` fields and populate per tool (full D-1,
    but ~92 tools of authoring — large), or
  - ship W2.a now and split the structured-payload authoring into its
    own wedge sequenced after W4, since the per-persona index (W4) is
    the higher-leverage discovery fix and does not depend on it.

Recommendation: the split. W2.a is the cheap catalog-size win;
W2.b's `failure_modes` authoring naturally fuses with the W3 / W5
hint work (same `{hint_text, see_tool}` shape).

**Acceptance (W2.a):**
- `tools/list` response is ≤ 5KB across all tools (down from ~30KB).
- `tools.get` still returns the full long body.
- No `Short` exceeds 200 chars (W5 lint enforces; spot-check here).

**Tests:** a payload-size assertion on `tools/list`; `hubmcpserver`'s
`TestToolsList_RoundTrip` updated for the `short` key.

#### W3 — Hint-bearing errors — top 5 paths

Add a `hint` field to the 5 worst error paths surfaced by the
2026-05-18 incident and similar:

1. `get_project_doc` 404 with ULID-shaped path → "this tool
   reads filesystem files; for document ULIDs use
   documents.get".
2. `documents.get` 404 → "this tool reads docs from the
   documents table; for filesystem files under docs_root use
   get_project_doc".
3. `search` no-results → "try a shorter query; or list via
   get_feed / documents.list for full enumeration".
4. Any permission_denied (403) on a worker-called tool → "your
   role lacks this capability (see roles.yaml worker.allow). To
   escalate, call request_help(target='@parent_handle',
   question=...)".
5. `agents.spawn` 422 missing field → already names the field
   (v1.0.620 W4); add hint: "see tools.get('agents.spawn')
   for the full spec or use spawn_spec_yaml: 'template:
   agents.coder' as a starting shape".

**Implementation site:** the relevant handlers in
`hub/internal/server/handlers_*.go`. Each adds a `hint` field
to its 4xx return shape.

**Hint envelope (extending `writeErr` in `server/server.go`, or
paralleling it).** Per ADR-031 D-3 the hint is a *nested* structured
object, not flat fields:

```go
writeErrHint(w, status, message, Hint{HintText: "...", SeeTool: "..."})
// Hint{HintText string `json:"hint_text"`;
//      SeeTool  string `json:"see_tool,omitempty"`;
//      SeeDoc   string `json:"see_doc,omitempty"`}
// → {"error": "...", "hint": {"hint_text": "...", "see_tool": "..."}}
```

`hint_text` is required when `hint` is present; `see_tool` / `see_doc`
are optional but at least one of the three must carry actionable
signal.

**Acceptance:** each of the 5 cases above returns the named
hint when reproduced via test.

**Tests:** per-handler test that asserts the hint string is
present and names the suggested tool.

### Phase 2 — Index

#### W4 — Per-persona intent → tool index

Each of the 10 main persona prompts gains an "Intent → tool"
section, listing 10-20 (intent, tool) pairs.

**Persona prompts in scope** — `hub/templates/prompts/` holds 15
files; the 10 *main* personas get the full index:
- `steward.v1.md`, `steward.general.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md` (4 stewards).
- `coder.v1.md`, `critic.v1.md`, `lit-reviewer.v1.md`,
  `ml-worker.v1.md`, `paper-writer.v1.md`, `briefing.v1.md`
  (6 workers).

**Not full-index personas (5 files):**
- `steward.codex.v1.md`, `steward.gemini.v1.md`,
  `steward.kimi.v1.md`, `steward.claude-m4.v1.md` — thin
  (~90-line) per-engine steward overlays. **To confirm at W4 time:**
  inspect `hub/internal/agentfamilies` to see whether they compose
  on top of `steward.v1.md` (then the index is inherited — no edit)
  or are used standalone (then each gets a one-line pointer to
  `tools.get`, not the full table).
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
- `tools/list` payload is ≤ 5KB total.
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
