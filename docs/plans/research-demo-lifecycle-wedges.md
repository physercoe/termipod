# Research-demo lifecycle — wedge plan

> **Type:** plan
> **Status:** In flight (2026-04-30) — W1–W6, no wedge started yet
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Six wedges to ship the amended demo
([ADR-001 amendment](../decisions/001-locked-candidate-a.md), design in
[`discussions/research-demo-lifecycle.md`](../discussions/research-demo-lifecycle.md)).
Each wedge is independently shippable; the demo is end-to-end after
W6. Roughly 10–13 days of work. **No schema migrations. No new
primitives. No new ADRs after [ADR-016](../decisions/016-subagent-scope-manifest.md).**
Architecturally zero risk; demo risk is in prompt quality (W4, W5) and
mobile template editor UX (W3).

This plan is the implementation arm of the discussion + ADRs. It does
not re-litigate decisions; it tracks deliverables, files, verification
steps, and order.

---

## W1 — Operation-scope role middleware

**Goal:** [ADR-016](../decisions/016-subagent-scope-manifest.md) D6
becomes load-bearing: every `hub://*` MCP call is gated by role before
it dispatches. Foundational because every later wedge assumes the
boundary holds.

**Files:**

- `hub/config/roles.yaml` — new. The manifest from ADR-016 D2/D3.
- `hub/internal/server/mcp_authority.go` — new `authorizeMCPCall`
  middleware; called from every tool handler entry point.
- `hub/internal/hubmcpserver/tools.go` — wire the middleware into the
  dispatch path. Pattern: every handler starts with
  `if err := s.authorizeMCPCall(ctx, agentID, "<tool-name>"); err != nil { return err }`.
- `hub/internal/server/mcp_authority_roles.go` — new. Loads
  `roles.yaml` (embedded + overlay), exposes `Roles.Allows(role,
  tool) bool`, hot-reload via `Invalidate()`.
- `hub/internal/server/mcp_authority_test.go` — coverage for the
  middleware: steward all-allowed, worker scope-restricted, A2A
  parent-only, hot-reload.
- A2A target restriction (D4): in `a2a_invoke` handler, look up
  caller's `parent_agent_id`; reject if target ≠ parent for workers.

**Verification:**

- Unit test: worker calling `hub://agents.spawn` returns
  `tool not permitted for role`.
- Unit test: worker calling `hub://documents.create` succeeds.
- Unit test: worker calling `hub://a2a.invoke` to non-parent target
  returns scope error.
- Integration smoke: spawn a steward, have it spawn a worker; worker
  posts a document, posts a channel event, requests a `request_help`
  attention — all succeed. Then have the worker attempt
  `agents.spawn` — fails with the manifest error.
- Hot-reload: edit `<DataRoot>/roles.yaml` to add a denied tool to a
  role; next call observes the change without restart.

**Effort:** ~1 day.

**Out of scope:** template `tool_allowlist` cleanup is W2's job (after
the middleware is the security boundary, the per-template lists are
informational).

---

## W2 — Template-authoring MCP tools + team overlay loader

**Goal:** Stewards (and director-via-MCP-bridge, eventually) can
create/update/delete team-scoped templates via MCP. Templates live
in a team overlay path that the existing overlay loader pattern
already understands.

**Files:**

- `hub/internal/server/templates_overlay.go` — new. Reads/writes
  `<DataRoot>/teams/<team>/templates/{agents,prompts,plans}/<name>.{yaml,md}`.
  Validates YAML structure on write; rejects path traversal in name;
  enforces self-modification guard (D7) — caller cannot edit a
  template whose kind matches caller's own kind.
- `hub/internal/hubmcpserver/tools_templates.go` — new. MCP tools:
  - `templates.agent.create(name, content)`
  - `templates.agent.update(name, content)`
  - `templates.agent.delete(name)`
  - `templates.agent.list()` returns names + last-modified
  - `templates.agent.get(name)` returns content
  - … same shape for `templates.prompt.*` and `templates.plan.*`
- `hub/internal/agentfamilies/families.go` — extend overlay loader to
  watch the team templates dir; existing `Invalidate()` propagates.
- Seed-import path: hub binary's bundled `embed.FS` of seed templates
  (W4 + W5 produce content). On first project-create per team, the
  seeds are copied to the team overlay; subsequent project-creates
  do **not** overwrite. New seed versions imported via explicit
  director action (deferred — `templates.import_seed` MCP added in
  W3 if needed by mobile UX, otherwise post-MVP).

**Verification:**

- Steward creates a template; reads back via `get`. Update changes
  content; delete removes it.
- Self-modification guard: general-steward attempting to edit
  `steward.general.v1` is rejected.
- Worker attempting any `templates.*write*` is rejected by W1's
  middleware.
- Overlay loader picks up new template within `Invalidate()` window;
  next agent spawn uses new content.
- Seed copy: first project-create on a fresh team copies all bundled
  seeds to the overlay; second project-create leaves overlay
  unchanged.

**Effort:** ~2 days.

---

## W3 — Mobile template editor + phase-0 review surface + persistent-steward entry

**Goal:** Director can review/edit overlay templates from phone, and
the phase-0 approval surface bundles plan + templates in one
director-friendly review. Persistent general-steward gets a top-level
entry point on the home tab so the director can always reach it.

**Files (Flutter):**

- `lib/screens/templates/template_list_screen.dart` — new. Lists team
  templates (agents/prompts/plans), shows file name + last-modified +
  last-edited-by (steward vs director).
- `lib/screens/templates/template_edit_screen.dart` — new. Monospace
  `TextField` for the full file content; save button writes via the
  hub's existing template-overlay HTTP endpoint (already needs to
  exist if mobile is to write — confirm in W2 endpoint surface, add
  if missing).
- `lib/screens/projects/phase_review_screen.dart` — new. For
  `human_gated` phase boundaries that bundle a plan + templates +
  artifacts; director sees a tabbed view, taps Approve / Request
  Revision / Abort.
- `lib/widgets/home/persistent_steward_card.dart` — new. Home-tab
  card surfacing the team's general-steward agent feed; one tap opens
  the steward chat. Distinct from project-scoped domain stewards.
- `lib/screens/me/me_screen.dart` — modify to expose the persistent-
  steward card prominently.

**Verification:**

- Open template list; view; edit; save; reopen — content persisted.
- Phase-0 review: pending plan + 5 draft templates render; approve
  flips plan draft→ready and archives general-steward's bootstrap
  attention item.
- Persistent steward: tap card from home → land in steward agent feed
  whether or not any project is active.
- Concurrent edit (steward writes via MCP while director edits in
  app): last-write-wins; no crash.

**Effort:** ~3 days, plus 0.5 day for the persistent-steward home
card.

**Out of scope (post-MVP):** YAML syntax highlighting, schema
validation in the editor (server validates on save), diff view
against bundled seed.

---

## W4 — `steward.general.v1` template + bootstrap-and-concierge prompt

**Goal:** The frozen general-steward exists, behaves as designed
(bootstrap-then-concierge), and runs cleanly on claude-code engine.

**Files:**

- `hub/templates/agents/steward.general.v1.yaml` — frozen agent
  template, bundled in `embed.FS`, never copied to overlay (the
  general steward kind is the only one that lives only in
  `embed.FS`). Capabilities: full steward-tier MCP set per ADR-016.
- `hub/templates/prompts/steward.general.v1.md` — substantial. Two
  modes interleaved:
  - **Bootstrap mode** (when project is new and director chats an
    idea): propose a 5-phase plan, draft a domain-steward template
    customized to the idea, draft worker templates (lit-reviewer,
    coder, paper-writer, critic) tuned to the domain, present all
    via `attention.create(request_approval, choices=[approve, edit,
    abort])`.
  - **Concierge mode** (always-on, after bootstrap completes):
    answer cross-project questions, debug stalled projects, edit
    templates/schedules at director's request, do **not** perform
    IC work (delegate to a worker or politely decline).
  - Self-discipline: don't edit `steward.general.v1` itself
    (enforced server-side by W2; reinforce in prompt).
- Spawn rule: hub auto-spawns `steward.general.v1` for a team on
  first director interaction with that team. Singleton — second
  spawn is a no-op while first is `running`. Implementation in
  `hub/internal/server/handlers_teams.go` or a new
  `general_steward_bootstrap.go`.

**Verification:**

- New team, director chats first idea: general-steward spawns and
  responds.
- Bootstrap: general-steward authors 5 templates + plan in overlay,
  surfaces phase-0 attention item.
- Concierge: after phase-0 approval, general-steward stays
  `running`; director chats an unrelated question, gets a response.
- IC delegation: director asks general-steward to write a code
  snippet; steward delegates to worker (or declines with a
  delegate-to suggestion).
- Manual archive: director archives general-steward; subsequent
  director interaction respawns a fresh one.

**Effort:** ~2 days (mostly prompt engineering + a couple of small
Go pieces for singleton spawn).

---

## W5 — Domain steward seed (`steward.research.v1`) + worker seeds + safety guardrails

**Goal:** Seed templates the general-steward can copy + customize.
Each worker prompt encodes the safety guardrails from
[discussion §5](../discussions/research-demo-lifecycle.md).

**Files (all under `hub/templates/`, bundled in `embed.FS`):**

- `agents/steward.research.v1.yaml` — overlay-authored seed (the
  general-steward customises this per project). Capabilities: full
  steward-tier; spawn-children allowed; A2A as parent.
- `prompts/steward.research.v1.md` — phase-by-phase orchestration
  recipe; spawn-the-right-worker-per-phase; aggregate via A2A;
  produce phase artifact via `documents.create`; request approval
  via `attention.create`; advance plan via `plan.advance`.
- `agents/lit-reviewer.v1.yaml` + `prompts/lit-reviewer.v1.md` —
  capabilities: WebSearch, WebFetch (engine-native), document write.
  Prompt encodes the lit-review safety rules (arxiv.org / papers-
  with-code / openreview / github read-only / well-known proceedings;
  not random blogs).
- `agents/coder.v1.yaml` + `prompts/coder.v1.md` — capabilities:
  Bash, Edit, Read, Write, Test (engine-native), document write.
  Safety rules: PyPI signed packages from well-known maintainers;
  load-bearing libraries (PyTorch, NumPy, transformers, datasets,
  scipy, matplotlib, pandas); no `curl <random> | bash`; no API-
  key tools.
- `agents/paper-writer.v1.yaml` + `prompts/paper-writer.v1.md` —
  capabilities: documents.read (lit-review + method docs),
  run.metrics.read, runs.list (read-side only); documents.create.
  Prompt: 6-section paper (Abstract, Intro, Method, Results,
  Discussion, Limitations, References). No related-work novelty
  claims (the demo doesn't do real novelty checks; if it must
  cite, cite from the lit-review's findings only).
- `agents/critic.v1.yaml` + `prompts/critic.v1.md` — optional but
  cheap. Reviews a target document, scores per-axis, returns to
  steward via A2A. Used for code-review loop in phase 2 and paper-
  review loop in phase 4.

**Verification:**

- Each seed loads into the overlay loader without YAML errors.
- Each seed agent spawns successfully on a single host with claude-
  code engine.
- Lit-reviewer end-to-end: spawn, give it a topic, observe it
  websearches arxiv + writes a document with citations — does **not**
  fetch random URLs.
- Coder end-to-end: spawn, give it a small coding task, observe it
  installs only PyPI packages, writes code, runs tests.
- Paper-writer end-to-end: spawn with mock digests + lit-review +
  method doc inputs, observe it writes a 6-section document.

**Effort:** ~3 days (mostly prompt engineering).

---

## W6 — `research-project.v1` plan template + `seed-demo --shape lifecycle`

**Goal:** The plan that ties phases 0–4 together exists; the no-GPU
harness can stage a multi-phase project for reviewers.

**Files:**

- `hub/templates/plans/research-project.v1.yaml` — bundled. 5 phases
  per discussion §3, each with `human_gated` boundaries between.
  `parameters_json = {idea: "<free-text>"}`.
- `hub/cmd/hub-server/seed_demo.go` — extend `--shape lifecycle`.
  Seeds a `research-project.v1` instance with: phase 0 done, phase 1
  done, phase 2 in-progress (one `coder.v1` running), phase 3
  pending (next gate), phase 4 not yet started. All phases produce
  realistic-looking artifacts (lit-review doc with 6 citations, code
  worktree commit, etc.). Reviewers see all phase states without
  running anything live.
- `hub/cmd/hub-server/seed_demo_test.go` — verify each phase's
  artifact renders on mobile after seed.

**Verification:**

- Run `hub-server seed-demo --shape lifecycle --data ./hub-data`.
- Open mobile app; land on the seeded project; observe all 5 phases
  in plan viewer with correct status.
- Tap each phase artifact; renders as document.
- Approve the pending phase-3 gate; observe plan advancing (running
  one mock-trainer cycle for phase 3 if available, otherwise
  manually mark complete via existing UI).

**Effort:** ~2 days.

---

## Sequencing notes

Wedges are mostly parallel-able after W1, but the natural sequence
is:

```
W1 (foundational) → W2 (overlay infra) → W4 (general steward —
needs W2's overlay tools) → W5 (seeds) → W3 (mobile, can land
mid-stream) → W6 (plan + harness, last)
```

W3 (mobile) is the only Flutter-side wedge; it can land in parallel
with W4/W5 once W2's overlay HTTP surface is defined.

## What's explicitly out of scope of this plan

These belong to other plans or are deferred:

- **`attention.request_secret`** — deferred to post-MVP per
  [discussion D5](../discussions/research-demo-lifecycle.md). Not
  needed because demo is constrained to non-key operations.
- **Cross-project memory for general steward** — deferred per OQ-4.
- **Hard enforcement of the manager/IC invariant** on general
  steward — deferred per OQ-5; MVP is prompt-soft.
- **`agent_families.yaml` overlay UI in mobile** — already exists;
  this plan reuses it.
- **Per-tool budget enforcement** — deferred per ADR-001 D-amend-3.
- **The hardware run of phase 3 (Candidate A's original sweep)** —
  tracked in [`research-demo-gaps.md`](research-demo-gaps.md), not
  here.

## References

- [ADR-001 (amended)](../decisions/001-locked-candidate-a.md)
- [ADR-016](../decisions/016-subagent-scope-manifest.md)
- [Discussion: research-demo-lifecycle](../discussions/research-demo-lifecycle.md)
- [Plan: research-demo-gaps (original Candidate A tracker)](research-demo-gaps.md) — phase 3's hardware-run still tracked there
- [Blueprint §3.3](../spine/blueprint.md) — steward/worker invariant the wedges respect
