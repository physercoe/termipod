---
name: Internal tech-debt cleanup
description: Acts on the 2026-06-03 tech-debt review, scoped to an internal-test posture (no external API consumers, so deprecated paths are deleted outright rather than ledgered). Four workstreams — retire all compatibility/legacy/alias paths; add controller seams to the actively-edited Flutter surfaces (sessions, projects, project-detail; the breakglass terminal is excluded); add protocol-edge fixture tests for the highest-churn JSON shapes; and give host-runner a structured command model with an explicit shell escape hatch. Demo-seed extraction and terminal_screen refactoring are deliberately out of scope.
---

# Internal tech-debt cleanup

> **Type:** plan
> **Status:** Open (2026-06-04) — drafted from the `/tmp/termipod_techdebt_review_2026-06-03.md`
> review after verifying its claims against the tree. No wedge landed yet.
> **Audience:** contributors · QA
> **Last verified vs code:** HEAD `151eb0f`.

**TL;DR.** The review is factually accurate but reads as a size/structure
audit; this plan re-scopes it to *risk × rate-of-change* and to our
**internal-test posture** — there are no external API consumers, so
backwards-compatibility shims are **deleted**, not ledgered. Four
workstreams, independent except where noted:

- **WS1 — Retire compatibility paths** (the review's strongest finding,
  inverted: delete instead of ledger).
- **WS2 — Controller seams for the *active* surfaces** (`sessions_screen`,
  `projects_screen`, `project_detail_screen`). The breakglass
  `terminal_screen` is **excluded** — it is legacy and seldom edited.
- **WS3 — Protocol-edge fixture tests** for the 2–3 highest-churn JSON
  shapes (sessions, agents, session-digest).
- **WS4 — Structured host-runner command model** with a `shell:` opt-in.

**Explicitly out of scope** (per director decision): demo-seed extraction
(`seed_demo_lifecycle.go` has nearly finished its UI-test mission) and any
`terminal_screen.dart` refactor (breakglass legacy).

**Recommended order:** WS3 → WS1 → WS4 → WS2. WS3 pins current
behaviour and de-risks WS1's Dart status-vocabulary deletion; WS1 and WS4
are bounded; WS2 is the largest and most continuous, so it trails and can
overlap.

---

## WS1 — Retire compatibility paths

**Goal.** Remove every `legacy` / `deprecated` / alias branch now that no
external consumer depends on the old shapes. The review's "compatibility
ledger" is the right instinct for a public API; for an internal fleet the
cheaper, cleaner move is deletion. Each sub-wedge is independent.

Most of these are already CI-locked by registry/contract tests, so
deletion is a *subtraction* the existing tests will catch if it breaks a
canonical path.

### W1.1 — MCP tool aliases (ADR-033 tail)

ADR-033 is **Done** (`docs/plans/tool-catalog-w6-teardown.md`, 2026-05-18):
every tool is registered under its canonical snake_case name, dispatch is
unified. What remains is the *aliases* that migration kept resolving.

- `hub/internal/hubmcpserver/toolspec.go` — `ToolSpec.Aliases`
  (`:39`) is `append([]string{backend}, extraAliases...)` (`:115`). The
  dotted `Backend` name doubles as the REST adapter **and** an alias, and
  `extraAliases` (e.g. `list_agents` on `agents_list`, `:208`) are pure
  old names. Drop the alias-resolution path in `LookupToolSpec` (`:480`)
  and the per-alias catalog rows in the `tools/list` builder (`:528`);
  keep `Backend` only as the adapter key, not as a name clients may call.
- `hub/internal/server/mcp.go` (`:260`) — the catalog filters
  non-compliant dotted names out of `tools/list`, but dispatch still
  *accepts* them. Make dispatch reject them too (the filter becomes a
  hard contract).
- `hub/internal/server/native_tools.go` (`:19`) — finish the deferred
  verb-first → resource-first pass (`get_feed → feed_get`) and drop the
  dotted orchestration aliases (`agents.fanout` …).
- `hub/internal/server/roles.yaml` (`:103`) — grants reference dotted
  `agents.gather`; rewrite to canonical names in lockstep.

**Blast radius:** **low.** Bundled `templates/` and `agentfamilies/` carry
**no** legacy tool-name references (verified by grep) — W6.4 already swept
them. Risk is confined to `roles.yaml` and any prompt text.
**Verification:** `tool_registry_test.go`, `native_tools_meta_test.go`,
`tool_contract_sweep_test.go` already lock the cross-registry invariants;
add a negative test that a dotted/aliased name now 404s at dispatch.

### W1.2 — Session `/close` route alias

`hub/internal/server/server.go` (`:433-437`) keeps `POST …/close` as an
alias for `…/archive` (ADR-009). Delete the route. Grep the Dart client
for `/close` callers first (expect none — `archiveSession` posts
`/archive`).

### W1.3 — Dart legacy session-status vocabulary

The hub emits the ADR-009 vocabulary (`active|paused|archived|deleted`);
`open|interrupted|closed` are tolerated only for a migration-era hub. With
an internal fleet on a current hub, delete the legacy arm:

- `lib/providers/sessions_provider.dart` (`:240-241`) — `_isLive` ORs in
  `open`/`interrupted`.
- `lib/screens/sessions/sessions_screen.dart` — ~14 sites branch on
  `open`/`interrupted`/`closed` (`:177,:233,:810,:945-47,:1978,:2046-48,
  :2091,:2555`).

The fleet runs a current, clean hub (ADR-009 vocab only), so the legacy
arm is dead code — delete it outright; no migration scan.
**Sequencing:** land **after WS3's session fixture test** so the
normalization is pinned before the branches are removed.

### W1.4 — Attention legacy decision shapes

`hub/internal/server/handlers_attention.go` (`:545-559`) maps legacy
`approval_request` / `template_proposal` payloads into the propose-kind
registry with `dispatchVia="alias_legacy"`, and preserves the legacy
`{kind, error}` response shape (`:575`). The fresh hub holds no attention
rows of those kinds, so remove the `alias_legacy` arms and the legacy
error shape outright — no DB scan.

### W1.5 — Lint guard

Add a `scripts/` lint that fails on **new** `// legacy` / `deprecated` /
`alias` markers in `hub/` and `lib/` unless the comment names a removal
target. (Inverts the review's "ledger" idea into a forward-only ratchet
so retired debt can't quietly grow back.)

---

## WS2 — Controller seams for the active surfaces

**Goal.** Give the screens that *actually change* a non-widget
orchestration seam, so state/transport logic is testable without a widget
harness. **Not** `terminal_screen.dart` — it is breakglass legacy.

Targets, by edit frequency and size:

- `lib/screens/sessions/sessions_screen.dart` — **2833 lines**. Mixes the
  sessions rail, the chat surface (`SessionChatScreen`), the session-list
  buckets, and lifecycle routing. Note the feed *logic* is already
  extracted into `lib/widgets/transcript/` (ADR-041 — `feed_reducer`,
  `seek_controller`, `random_access_loader`); this WS extracts the
  **session-list + lifecycle** orchestration, not the feed.
- `lib/screens/projects/projects_screen.dart` — **2225 lines**.
- `lib/screens/projects/project_detail_screen.dart` — **1868 lines**.

**Approach (prove on one first):**

1. Start with `project_detail_screen` — smallest of the three and freshly
   in hand (the v1.0.799 stopped-agent work touched its `_AgentsView` and
   the new `_projectTerminatedAgentsProvider`). Extract a
   `ProjectAgentsController` / view-model that owns the live+stopped row
   merge, resumability resolution, and refresh fan-out (hub + sessions +
   the terminated fetch). The widget keeps view composition only.
2. Add controller tests (no widget harness): live/stopped merge + dedup,
   resumability from a session snapshot, refresh ordering.
3. Only after the pattern proves out, repeat for `sessions_screen` then
   `projects_screen`.

**Guardrail.** Follow the existing `agent_feed` precedent — extract into a
sibling file, do not pile onto the screen; grep for an existing helper
before adding one. This WS is **continuous**: land one controller per PR,
not a big-bang rewrite.

---

## WS3 — Protocol-edge fixture tests

**Goal.** Catch hub→app schema drift at parse time instead of at screen
runtime, **without** adopting typed DTOs (the app's `Map<String,dynamic>`
stance is a deliberate decision — CLAUDE.md). The seam is a small set of
**normalization functions** + **captured-fixture** tests, not a type
system.

Highest-churn shapes (chosen by recent edit activity):

1. **Session** — status vocabulary, `current_agent_id`, `scope_kind`/
   `scope_id`. Pins WS1.3. (Today only `session_display_test.dart` exists,
   and it is display-oriented.)
2. **Agent** — `status` + the stop-vs-archive resumability fact that lives
   on the session (`sessionStatusForAgent`, `agentResumability`,
   `agentStatusLabelResumable`); the v1.0.799 area.
3. **Session digest / agent_turns** — the insight surface (event digest +
   turn index), the most actively reshaped JSON.

**Approach.** Capture a real hub response per shape into
`test/fixtures/`, write a parse/normalize test that asserts the fields the
UI depends on, and run the existing pure resolvers
(`sessionStatusForAgent`, `_isLive`, digest folders) against the fixture.
Keep raw-map escape hatches everywhere else.

**Payoff.** When the Go contract moves, a fixture test fails in CI rather
than a card rendering blank on a device — the exact gap the review names,
addressed at one-tenth the cost of DTOs.

---

## WS4 — Structured host-runner command model

**Goal.** Stop routing every agent launch through a single `bash -c`
string; make the common case structured and keep shell as an explicit,
audited opt-in.

Today:

- `hub/internal/hostrunner/launch_m2.go` — `RealProcSpawner.Spawn` /
  `SpawnWithStderr` run `exec.CommandContext(ctx, "bash", "-c", command)`
  (`:53,:85`); the command is `spec.Backend.Cmd` (`:186`), a single
  string.
- `hub/internal/hostrunner/tmux_launcher.go` (`:52`) — passes the same
  string as the tmux new-window command.

**Approach.**

1. Extend the backend spec: alongside `cmd` (string), add structured
   `exec` (executable), `args` (list), `env`, `cwd`, and an explicit
   `shell: true` flag. Behaviour is **data** — this is a YAML schema
   addition under `agentfamilies/` + `templates/`, plus the Go spec
   struct.
2. When `shell` is false (the new default for structured specs), launch
   via `exec.CommandContext(exec, args...)` directly — no shell parsing.
   When `cmd`/`shell:true` is set, keep the `bash -c` path as the marked
   escape hatch.
3. Add a template audit (extend the existing `*_meta_test.go` family) that
   reports which bundled templates still require shell execution, so the
   shell surface is visible and shrinking.

**Risk.** Process-group kill semantics (`Setpgid`, `killProcessGroup`,
`launch_m2.go:122`) must hold for both launch paths — the orphaned-engine
file-lock bug the current comment documents is the regression to guard.
Keep tmux/git/system probes on direct `exec` (they already are).

---

## Sequencing summary

| Order | Workstream | Size | Risk | Notes |
|------|------------|------|------|-------|
| 1 | WS3 fixtures (session, agent, digest) | S | low | de-risks W1.3 |
| 2 | W1.1–W1.5 compat retirement | M | low (CI-locked) | mostly subtraction |
| 3 | WS4 host-runner structured cmd | M | med | guard pgroup kill |
| 4 | WS2 seams (project_detail → sessions → projects) | L | med | continuous, one PR each |

## Out of scope (recorded so it isn't re-litigated)

- **Demo seed** (`seed_demo_lifecycle.go`, 2893 lines) — leave in place;
  its UI-test mission is nearly complete.
- **`terminal_screen.dart`** (4903 lines) — breakglass legacy, seldom
  edited; refactoring it is low leverage.
- **Typed Dart DTOs** — superseded by WS3 (fixtures over the existing
  map stance); the no-DTO decision stands.

## Review limitations carried forward

The source review was static (no test runs, no runtime profiling). Each
wedge above must land with its verification green before the next. This
plan assumes a clean, current hub (ADR-009 vocab, no legacy attention
rows), so W1.3 and W1.4 delete dead arms without a migration scan.
