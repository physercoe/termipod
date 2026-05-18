---
name: Tool catalog W6 teardown
description: The W6 tail of the ADR-033 tool-catalog rollout — four wedges that finish the migration after every tool was registered (W6.1). W6.2 collapses each native tool's three-place definition (legacy catalog def + handler map + metadata table) into one buildNativeTools() declaration and deletes the legacy mcpToolDefsBase/Extra/orchestrationToolDefs functions. W6.3 deletes the dead native handlers stranded by the W5 duplicate-pair consolidation. W6.4 sweeps bundled templates onto the canonical snake_case tool names. W6.5 settles the standalone hub-mcp-server daemon, whose catalog has diverged from the registry. Companion to the tool-catalog-rollout plan and ADR-033.
---

# Tool catalog W6 teardown

> **Type:** plan
> **Status:** Done (2026-05-18) — all four wedges shipped. The W6 tail
> of [tool-catalog-rollout](tool-catalog-rollout.md); W6.1 (templates
> migration + `authorityToolDefs()` removal) shipped at `d7956e3`.
> Landings: **W6.3** dead-handler deletion `b9f26a3`; **W6.2** native
> definition unification — commit A (`buildNativeTools()` + verified
> move) `524b67f`, commit B (legacy-def deletion + `tools_get` fold)
> `e89fb6d`; **W6.4** template tool-name sweep + drift-lock `da85dd7`;
> **W6.5** standalone-daemon catalog alignment `4fd811a`. ADR-033 is
> now functionally and structurally complete.
> **Audience:** contributors · QA
> **Last verified vs code:** HEAD `4fd811a` — `buildNativeTools()` is
> the sole native-tool declaration (26 tools incl. `tools_get`); the
> legacy `mcpToolDefsBase/Extra/orchestrationToolDefs` are gone;
> `dispatchTool` routes no tool by literal `case`.

**TL;DR.** ADR-033 is functionally done — every MCP tool is in one
of the two `ToolSpec` registries, all three duplicate pairs are
consolidated, dispatch is unified and CI-locked. What remains is
teardown the rollout plan's W6 named but that W6.1 only started:

- **W6.2 — native definition unification.** A native tool is still
  declared in *three* places. Collapse to one `buildNativeTools()`
  table; delete the legacy `mcpToolDefsBase/Extra/orchestrationToolDefs`.
- **W6.3 — delete the dead native handlers** stranded by W5.
- **W6.4 — bundled-template tool-name sweep.**
- **W6.5 — settle the standalone `hub-mcp-server` daemon.**

W6.2 is the substantive one and carries real transcription risk;
the rest is bounded cleanup. The wedges are independent except
where noted — recommended order **W6.3 → W6.2 → W6.4 → W6.5**.

---

## 0. Where W6.1 left things

Two registries, by import direction (`server` imports
`hubmcpserver`, not the reverse):

- **Authority registry** — `hubmcpserver/toolspec.go`,
  `toolRegistry()`, 66 tools. Each `ToolSpec` borrows its
  `Description` + `InputSchema` from `buildTools()` (the REST-adapter
  table) via the `spec()` helper. Dispatch: `dispatchAuthorityToolRaw`
  → `hubmcpserver.Dispatch` → the `buildTools()` `call` closure.
- **Native registry** — `server/native_tools.go`,
  `nativeToolRegistry()`, 25 tools. Dispatch: the `nativeHandlers`
  map → a `(*Server)` method.

`mcpToolDefs()` is now `RegistryCatalogDefs()` +
`nativeRegistryCatalogDefs()` + whatever survives in the legacy
`mcpToolDefsBase/Extra/orchestrationToolDefs` defs un-dropped by
`registryServedNames()` — today only `tools.get`. The
`dispatchTool` switch is down to the protocol cases + `tools.get`.

**The remaining defect.** A native tool is declared in three
places that must stay in lockstep:

1. a legacy catalog def in `mcpToolDefsBase()`, `mcpToolDefsExtra()`,
   or `orchestrationToolDefs()` — carries `Description` + `InputSchema`;
2. an entry in the `nativeHandlers` map — carries the handler;
3. an entry in the `nativeToolMeta` table — carries name, aliases,
   short, tier, worker-eligibility.

`nativeToolRegistry()` zips (1) and (3) at runtime. This is the
ADR-033 D-3 four-place-lockstep class, merely relocated — CI-locked
(`TestNativeRegistry_*`) but not *collapsed*. W6.2 collapses it.

---

## 1. The wedges

### W6.2 — Native definition unification

**Goal.** One declaration per native tool; delete the legacy
`mcpToolDefsBase()`, `mcpToolDefsExtra()`, `orchestrationToolDefs()`.

**Target structure** (`server/native_tools.go`):

```go
// nativeTool is the single declaration for one native MCP tool —
// catalog metadata, schema, and handler in one value (ADR-033 D-3).
type nativeTool struct {
    Name           string
    Aliases        []string
    Short          string
    Description    string
    InputSchema    json.RawMessage
    Tier           string
    WorkerEligible bool
    Handler        nativeHandler
}

func buildNativeTools() []nativeTool { /* the one table */ }
```

`nativeHandlers` (the map) and `nativeToolRegistry()` (→
`[]hubmcpserver.ToolSpec`) both become thin derivations of
`buildNativeTools()`. `nativeToolMeta` is deleted; the legacy three
catalog-def functions are deleted; `legacyNativeDefs()` is deleted.
This makes native symmetric with authority (`buildTools()` +
`toolRegistry()`) and collapses the lockstep to one place.

**The risk: faithful schema move.** Each native tool's
`Description` string and `InputSchema` JSON literal currently live
in a legacy def. They must move into `buildNativeTools()` **verbatim**
— a mis-paste silently changes a tool contract agents depend on.
Mitigation — a two-commit verified move:

- **Commit A.** Add `buildNativeTools()` with every field moved in.
  *Keep* `mcpToolDefsBase/Extra/orchestrationToolDefs` for now.
  Point `nativeToolRegistry()` + `nativeHandlers` at the new table.
  Add a temporary test `TestNativeDefs_MatchLegacy`: for every
  native tool, assert the new `Description` + `InputSchema` equal
  the legacy def's (compare `InputSchema` as canonicalised JSON, not
  raw bytes — whitespace differs). Green ⇒ the move is faithful.
- **Commit B.** Delete `mcpToolDefsBase/Extra/orchestrationToolDefs`,
  `legacyNativeDefs()`, `nativeToolMeta`, the standalone
  `nativeHandlers` literal, and `TestNativeDefs_MatchLegacy`. Update
  `mcpToolDefs()` — it no longer iterates the legacy defs.

**`tools.get` — fold it in.** `tools.get` is the last switch
special-case and its catalog entry lives in `mcpToolDefsExtra()`,
which Commit B deletes. Make `tools.get` a native tool:
canonical `tools_get`, alias `tools.get`, handler `mcpToolsGet`.
`mcpToolsGet`'s signature is `(raw json.RawMessage)` — give it the
standard `nativeHandler`-compatible shape (ignore the extra args)
or add a one-off adapter. Consequence: the `dispatchTool` switch
retires entirely to protocol handling (`initialize`,
`notifications/initialized`, `tools/list`, `tools/call`, `ping`) —
no tool is dispatched by a literal `case` any more.

**Acceptance.** `buildNativeTools()` is the sole native-tool
declaration; the three legacy functions and `nativeToolMeta` are
gone; `grep` finds no `mcpToolDefsBase`; the `TestNativeRegistry_*`
CI-locks still pass; `go test ./...` green; a `tools/list` diff
against `d7956e3` shows no description/schema change.

### W6.3 — Delete the dead native handlers

W5 made `list_agents` / `get_audit` / `get_task` aliases of their
authority twins; their native `(*Server)` handlers are now
unreachable. Delete:

- `mcpListAgents` + `listAgentsArgs` (`mcp_more.go`).
- `mcpGetAudit` + `getAuditArgs` (`mcp_more.go`).
- `mcpGetTask` + `nullStringOrEmpty` (`mcp_more.go`) — `nullStringOrEmpty`
  has no other caller (verified); `idArg` is **shared with
  `mcpGetEvent`** and stays.
- `hubmcpserver.RegistryBackends()` — unused since W5's
  `registryServedNames()` replaced it.
- `hubmcpserver.ToolCatalog()` and `ToolNames()` — unused since
  W6.1 deleted `authorityToolDefs()` (confirm no caller remains;
  `ToolCatalog` may be wanted by W6.5 — sequence accordingly).

**Test fallout — check coverage before deleting.** Direct-call
tests must go with their targets: `handlers_audit_coverage_test.go`
calls `mcpGetAudit`; `mcp_more_test.go` (~line 392) calls
`mcpListAgents`. Before deleting a test, confirm the *underlying
behaviour* is still covered via the REST path the alias now uses
(`handleListAudit` / `handleListAgents` / `handleGetTaskByID`). If a
deleted test was the only coverage of a real invariant, port that
assertion to the REST handler rather than dropping it.

**Acceptance.** No dead handler remains; `go vet` clean; coverage
of the live REST paths is not reduced.

### W6.4 — Bundled-template tool-name sweep

~19 files under `templates/prompts/`, `templates/agents/`, and
`internal/agentfamilies/` reference tool names in prose. The
deprecated aliases keep them working, so this is **cosmetic /
hygiene** — but a contributor reading a template should see the
canonical name, and an agent reading `tools/list` sees the old
names tagged `[DEPRECATED]`.

**It is not a blind `.`→`_`.** A template naming a *retired* twin
must map to the surviving canonical, not to a dotted-to-underscore
transform of the dead name:

- `list_agents` → `agents_list`, `get_audit` → `audit_read`,
  `get_task` → `tasks_get` (W5 consolidations).
- `agents.fanout` → `agents_fanout`, etc. (W4n dotted flatten).
- every other `domain.verb` → `domain_verb`.

Build the full old→canonical map from the two `toolRegistry()` /
`buildNativeTools()` alias lists — do not hand-transform.

**Optional CI-lock.** Add a `scripts/` lint or a Go test that scans
the bundled templates and fails on any token matching a known
deprecated alias — so templates cannot drift back. Recommended;
small.

**Acceptance.** No bundled template references a deprecated alias;
the optional lint (if added) is green.

### W6.5 — Settle the standalone daemon

`cmd/hub-mcp-server` → `hubmcpserver.Run()` serves a `tools/list`
built from `buildTools()` — **authority tools only, dotted names**.
Post-W6 the in-process hub serves the registry catalog (66
authority + 25 native + `tools_get`, snake_case). The daemon has
therefore diverged on both *naming* (dotted vs snake) and
*completeness* (no native tools — and it structurally cannot have
them: native handlers live in `server`, which the daemon does not
import).

**Step 1 — establish its role.** `grep` for what launches
`hub-mcp-server` / routes through host-runner's multicall to it,
and likewise `cmd/hub-mcp-bridge`. Determine whether the daemon is
a live path or vestigial.

**Step 2 — decide:**
- *If vestigial* — deprecate or delete `cmd/hub-mcp-server`; record
  the decision.
- *If live* — at minimum make its `tools/list` emit
  `RegistryCatalogDefs()` (snake_case + `[DEPRECATED]` aliases) so
  the authority names match the in-process surface. State plainly
  in package docs that the daemon is an **authority-only** surface
  by construction; native tools are reachable only through the
  in-process `/mcp/{token}` endpoint.

**Acceptance.** The daemon's catalog either matches the registry's
authority slice or the daemon is removed; the divergence is
documented, not silent.

---

## 2. The verb-first naming question (decide before W6.2)

W4n deferred the resource-first rename of the verb-first native
names (`get_feed`→`feed_get`, `post_message`→`messages_post`, …)
"past W5". W5 is done, so it is now unblocked, and W6.2 rewrites
the native table anyway — the cheap moment to apply it.

**But it is judgment-heavy, not mechanical.** `get_feed`,
`post_message`, `get_event` have a clean resource; `search`,
`delegate`, `attach`, `pause_self`, `shutdown_self`,
`permission_prompt`, `update_own_task_status` do not. ADR-033 D-1
is "resource-first *where there is a resource*" — forcing a
resource onto `search` is worse than leaving it.

**Recommendation.** Treat the rename as an explicit sub-step of
W6.2, not a silent ride-along: produce the proposed
old→canonical map first, get it reviewed (it is a public
agent-facing surface), then apply — old names stay as D-2 aliases,
so it is reversible and cheap. If the map is contentious, ship
W6.2's structural change with names unchanged and do the rename as
a separate follow-up — the unification does not depend on it.

---

## 3. Sequencing

Recommended order **W6.3 → W6.2 → W6.4 → W6.5**:

- **W6.3 first** — removes dead code so W6.2 rewrites a smaller,
  cleaner native surface. Independent of the others.
- **W6.2** — the structural core. Depends on the §2 naming
  decision.
- **W6.4** — depends on W6.2 only if the verb-first rename lands
  (the sweep map must then include it).
- **W6.5** — independent; last because it is the least urgent and
  may resolve to a deletion.

Each wedge lands build-green and CI-green on its own.

## 4. Risks

- **Schema transcription (W6.2).** The dominant risk. Mitigated by
  the two-commit verified-move with `TestNativeDefs_MatchLegacy`.
  Do not skip Commit A's checkpoint.
- **Dropping real coverage (W6.3).** Deleting a handler's test can
  silently delete the only coverage of a behaviour. Mitigated by
  the "check the REST path is covered, port the assertion if not"
  step.
- **`tools.get` orphaned (W6.2).** Deleting `mcpToolDefsExtra()`
  removes `tools.get`'s catalog entry; folding it into
  `buildNativeTools()` is in-scope, not an afterthought.
- **Daemon consumers (W6.5).** Changing or removing the daemon
  could break a host-runner path — hence "establish its role
  first."
- **Naming churn (§2).** A contested rename map can stall W6.2;
  the fallback (ship structure, rename later) removes that
  dependency.

## 5. Acceptance for the bundle

- One declaration per native tool (`buildNativeTools()`); the
  legacy `mcpToolDefsBase/Extra/orchestrationToolDefs` and
  `nativeToolMeta` are deleted.
- No dead native handler, no unused registry export remains.
- `dispatchTool` dispatches no tool by literal `case` — only the
  MCP protocol verbs.
- No bundled template references a deprecated alias.
- The standalone daemon's catalog matches the registry's authority
  slice, or the daemon is removed; the decision is recorded.
- `go build ./... && go test ./... && go vet ./...` green; a
  `tools/list` diff against `d7956e3` shows only intended changes.

## 6. References

- [tool-catalog-rollout](tool-catalog-rollout.md) — W1–W6.1; this
  plan is its W6 tail.
- [ADR-033](../decisions/033-tool-catalog-naming-and-registration.md)
  — D-1 (naming), D-3 (single registration point) are what W6.2
  finishes.
- [tool-catalog-structure discussion](../discussions/tool-catalog-structure.md)
  — the original four-place-lockstep audit (§2.4).
