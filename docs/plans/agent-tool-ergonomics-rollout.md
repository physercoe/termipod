---
name: Agent tool ergonomics rollout
description: Phased rollout of the agent-tool-ergonomics design — two-tier descriptions (catalog short + tools.describe long), per-persona intent → tool index in each persona prompt, hint-bearing errors on every 4xx, and a CI lint that asserts catalog × dispatcher × describe stay in lockstep. Five wedges across hub server + hubmcpserver + bundled prompts + CI; ~600 LOC + ~400 lines prose. Three-phase plan (foundation → index → polish); MVP is phases 1 + 2 (foundation + index). Companion discussion at agent-tool-ergonomics.md.
---

# Agent tool ergonomics rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18) — three phases, five wedges total, no work started. Companion discussion at [`../discussions/agent-tool-ergonomics.md`](../discussions/agent-tool-ergonomics.md); the failure-mode taxonomy + recommendation are there.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Close the discovery / depth / error-recovery gap
revealed by the 2026-05-18 steward incident (6 turns guessing
the right tool to read a doc by ULID). Three phases:

- **Phase 1 — Foundation (MVP, 3 wedges):** add `tools.describe`
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
| 1 | W1 | `tools.describe` meta-tool | ~80 LOC | — |
| 1 | W2 | Two-tier descriptions across catalog | ~250 LOC | W1 |
| 1 | W3 | Hint-bearing errors — top 5 paths | ~80 LOC | — |
| 2 | W4 | Per-persona intent → tool index | ~250 prose | W2 |
| 3 | W5 | Hint pass + CI lint | ~150 LOC | W1, W2, W3 |

Implementation order is **W1 → W2 (depends W1) → W3 (parallel) →
W4 (depends W2) → W5 (depends all)**.

---

## 1. Wedges in detail

### Phase 1 — Foundation

#### W1 — `tools.describe` meta-tool

A new MCP tool: given a name, return the full description body.
Mirrors `tools/list` in shape; the difference is that
`tools/list` returns short descriptions (~1 line each) per W2,
while `tools.describe` returns the full body (shape, examples,
failure modes, see-also).

**Surface (MCP tool definition):**

- Name: `tools.describe`
- Input schema: `{tool_name: string}` (required)
- Returns: `{name, short, long, schema, examples, see_also}`

**Implementation site:** `hub/internal/hubmcpserver/tools.go`
add a new `Tool` entry whose handler looks up the requested
name in the in-memory catalog and returns the long description.
Catalog needs a new field for long body (see W2).

**Acceptance:**
- `mcp__termipod__tools.describe(tool_name="documents.get")`
  returns the full v1.0.630 description body.
- Unknown name returns `is_error: true` with body `"unknown tool
  'X'; call tools/list for the available set"`.

**Tests:** `TestToolsCall_ToolsDescribe_Known`,
`TestToolsCall_ToolsDescribe_Unknown`.

#### W2 — Two-tier descriptions across catalog

Every tool entry in `hub/internal/hubmcpserver/tools.go` and
`hub/internal/server/mcp.go` (both registries) gains a `short`
field. The existing `description` field becomes the `long`
form. `tools/list` returns short; `tools.describe` returns long.

**Convention for short:**
- One sentence, present tense, contract only.
- Names required params + canonical input type.
- Example: `documents.get — Fetch a document by id. Required: document_id (ULID).`

**Implementation site:** every `Tool` struct definition. Add a
`Short` field; populate. The MCP `tools/list` response uses
Short; `tools.describe` uses Description (existing long).

**Acceptance:**
- `tools/list` response is ≤ 5KB across all tools (down from
  ~30KB today).
- Each short line has the canonical input type named.
- No short line exceeds 200 chars.

**Tests:** snapshot test that the tools/list payload size is
bounded.

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
   (v1.0.620 W4); add hint: "see tools.describe('agents.spawn')
   for the full spec or use spawn_spec_yaml: 'template:
   agents.coder' as a starting shape".

**Implementation site:** the relevant handlers in
`hub/internal/server/handlers_*.go`. Each adds a `hint` field
to its 4xx return shape.

**Hint envelope (extending `writeErr` or paralleling it):**

```go
writeErrHint(w, status, message, hint, seeTool)
// → {"error": "...", "hint": "...", "see_tool": "..."}
```

**Acceptance:** each of the 5 cases above returns the named
hint when reproduced via test.

**Tests:** per-handler test that asserts the hint string is
present and names the suggested tool.

### Phase 2 — Index

#### W4 — Per-persona intent → tool index

Each of the 10 main persona prompts gains an "Intent → tool"
section, listing 10-20 (intent, tool) pairs.

**Persona prompts in scope:**
- `steward.v1.md`, `steward.general.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md` (4 stewards).
- `coder.v1.md`, `critic.v1.md`, `lit-reviewer.v1.md`,
  `ml-worker.v1.md`, `paper-writer.v1.md`, `briefing.v1.md`
  (6 workers).

**Section template (steward example):**

```markdown
## Tools at a glance

Quick map from intent → tool. Call `tools.describe(name)` for
shape + examples before invoking a tool you don't recall.

| Intent | Tool |
|---|---|
| Read a doc by ULID | documents.get |
| Read a filesystem file under project docs_root | get_project_doc |
| List recent project activity | get_feed |
| Search by text content | search |
| Read this agent's journal | journal_read |
| Read a delegated task's body | get_task |
| Read attention/approval queue | get_attention |
| Look up an agent by handle | list_agents |
| Update a task you assigned | tasks.update |
| Read what a worker reported | documents.get with doc_id from tasks.complete summary |
| Escalate something you can't decide | attention.create(kind=request_help) |
| Direct-message a peer steward | a2a.invoke |
| Spawn a worker | agents.spawn |
```

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
`hub/internal/hubmcpserver/tool_catalog_lint_test.go`.

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
- **Engine-side prompt rules** for using `tools.describe`. The
  index in W4 implicitly teaches "call tools.describe before
  invoking unfamiliar tools"; an explicit rule is left for the
  persona's discretion. (If on-device testing shows agents
  don't reach for tools.describe naturally, follow-up to make
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
- **The `tools.describe` round-trip cost** — one extra MCP call
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
