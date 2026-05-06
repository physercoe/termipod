# Team peer stewards

> **Type:** plan
> **Status:** Proposed (2026-05-06)
> **Audience:** contributors
> **Last verified vs code:** v1.0.370-alpha

**TL;DR.** Implementation tracker for [ADR-017 Amendment 1](../decisions/017-layered-stewards.md#amendment-1--peer-steward-tier-2026-05-06) — the peer steward tier. Adds a third row to the existing general/domain ladder: overlay-authored, persistent, team-scoped specialists (`steward.peer.code.v1`, `steward.peer.ops.v1`, …) with one instance per `(team_id, kind)`. Seven wedges, each a self-contained commit + version bump. Order is the safe-to-ship sequence; single-tier and two-tier deployments stay unchanged until W5 lands the surfacing UX.

---

## 1. Goal

A director can spawn N peer stewards per team, one per peer kind. Each peer is a persistent, team-scoped specialist with its own overlay-authored prompt — distinct from the singleton general concierge (which it complements) and the per-project domain orchestrator (which it sits above in scope).

Concretely, when this plan lands:

- The director can mention `@steward.code` from the Me-tab compose box and reach a team-level code-review specialist.
- The Stewards overview screen has a "Team peers" section between General and Project domain.
- Project domain stewards can hand off to peers via A2A (e.g. research-steward asks `@steward.code` to review a method-phase commit).
- The general steward can author/edit peer templates; the director can edit them in the mobile template editor; peers cannot rewrite their own kind.

## 2. Non-goals

- **Per-member stewards** (F-1 thread / ADR-004). Still deferred. Peers are *team-scoped*, not member-scoped — the whole team shares each peer.
- **Peer stewards within a project.** A project-bound steward stays a *domain* steward (ADR-017 D6). Peers never own a `project_id`.
- **Routing automation.** No "the system picks the right peer for you" magic; the director (or a project domain steward via A2A) picks explicitly. A future routing wedge can layer on top.
- **Peer stewards as MCP servers / external endpoints.** Peers are normal agents. They're reachable through the same surfaces every other agent is.

## 3. Vocabulary

- **Peer steward** — overlay-authored, persistent, team-scoped specialist agent. Kind: `steward.peer.<domain>.v1`. Handle: `@steward.<domain>`. Many per team, one per kind.
- **General steward** (unchanged, ADR-017 D1) — singleton frozen concierge. Kind: `steward.general.v1`. Handle: `@steward`.
- **Domain steward** (unchanged, ADR-017 D1) — project-scoped orchestrator. Kind: `steward.<domain>.v1`. Handle: `<domain>-steward`.
- **Team-level steward** — colloquial for general OR peer (the `@`-prefixed pair).

## 4. Surfaces affected

| Surface | Change | Wedge |
|---|---|---|
| `agents` table, `(team_id, handle)` unique index | None (already supports per-team handle uniqueness) | — |
| `roles.yaml` (ADR-016 D6) | New `peer-steward` role bucket | W3 |
| `hub/internal/server/handlers_general_steward.go` | Existing logic factored out as `ensureSteward(kind, …)` reusable across tiers | W1 |
| `hub/internal/server/server.go` | New route `POST /v1/teams/{team}/steward.peer/ensure` | W2 |
| `hub/templates/agents/steward.peer.*.v1.yaml` (new files) | Seed peer templates: `steward.peer.code.v1.yaml`, `steward.peer.ops.v1.yaml` | W2 |
| `lib/services/steward_handle.dart` | New predicates (`isPeerStewardHandle`, `isTeamLevelStewardHandle`); existing `isStewardHandle` widens to include peers | W4 |
| Stewards overview screen (currently in [`plans/multi-steward.md`](multi-steward.md) §4.1) | New section group "Team peers" between General and Project | W5 |
| `AgentCompose` (`@`-mention picker) | Suggestion strip surfaces peer handles from session.init's `mentions` list | W6 |
| Project domain steward → peer A2A handoff | Allow worker→non-parent edge for peer targets (cross-link to ADR-016) | W7 |

## 5. Wedges

Each wedge is one commit + one version bump. Single-team and two-tier installs see no behaviour change before W5; that's intentional so the foundation is well-tested before any UI surfaces it.

### W1. Generalise the ensure-spawn handler — refactor only

Factor `handleEnsureGeneralSteward` (`hub/internal/server/handlers_general_steward.go`) into a kind-agnostic helper:

```go
// ensureSteward is the shared idempotent-spawn primitive for any
// singleton-per-(team, kind) steward. The caller passes the kind, the
// handle to use, and the template loader; ensure handles the
// fast-read / spawn / coalesce / respawn-after-archive logic identical
// across tiers.
func (s *Server) ensureSteward(ctx context.Context, team, kind, handle string,
                                loadSpec func() (string, error)) (ensureStewardOut, int, error)
```

`handleEnsureGeneralSteward` becomes a one-liner that calls `ensureSteward` with `kind="steward.general.v1"`, `handle="@steward"`, and the bundled-template loader.

**No behavioural change.** Tests: existing `handlers_general_steward_test.go` keeps passing; one new test asserts `ensureSteward(kind=...)` works for an arbitrary kind (using a bundled fixture template).

### W2. Peer ensure endpoint + seed templates

Add `POST /v1/teams/{team}/steward.peer/ensure` (request body `{"kind": "steward.peer.<x>.v1"}`). The handler:

1. Validates `kind` matches `steward.peer.<domain>.v1` regex (rejects `steward.general.v1` and `steward.<domain>.v1`).
2. Resolves the handle as `@steward.<domain>`.
3. Looks up the template with overlay-first fallback (overlay path: `<DataRoot>/teams/<team>/templates/agents/<kind>.yaml`; bundled fallback: `hub/templates/agents/<kind>.yaml`).
4. Calls `ensureSteward(...)`.

Ship two seed templates in `hub/templates/agents/`:

- **`steward.peer.code.v1.yaml`** — code-review specialist. Default workdir `~/hub-work/peer-code/`. Prompt focus: review pull requests, audit diffs, flag code-quality issues across projects.
- **`steward.peer.ops.v1.yaml`** — ops/infra specialist. Default workdir `~/hub-work/peer-ops/`. Prompt focus: host health, capacity, schedule edits, agent-family hygiene.

Tests: round-trip test for the new endpoint (spawn one, retry returns the same agent_id, archive then ensure again returns a fresh one); template fixture loading test.

### W3. roles.yaml — `peer-steward` role bucket

Extend `hub/internal/server/roles.yaml` with:

```yaml
- agent_kind_pattern: "steward.peer.*.v1"
  role: peer-steward
  allowed_tools:
    # Read across all projects in the team
    - hub://projects.list
    - hub://projects.get
    - hub://runs.list
    - hub://runs.get
    - hub://documents.list
    - hub://documents.get
    - hub://artifacts.list
    # Write only within authoring/advisory scope
    - hub://templates.list
    - hub://templates.get
    - hub://schedules.list
    - hub://attention.notify
    - hub://channels.post
    # NB: no runs.start, no projects.create, no documents.write
```

Per-peer-kind narrowing (e.g. `steward.peer.code.v1` should be allowed `templates.edit` but `steward.peer.ops.v1` should not) is a follow-up; the W3 default is "permissive across the bucket."

ADR-016 D7 (no agent edits its own kind) is enforced by middleware unchanged — peers inherit the guard from the role layer.

Tests: new entries in `mcp_authority_test.go` covering peer-kind allow-list shape; cross-tier denial test (peer cannot call `runs.start` even within scope).

### W4. Mobile predicates + handle parser

Update `lib/services/steward_handle.dart`:

```dart
bool isGeneralStewardHandle(String h) => h == '@steward';
bool isPeerStewardHandle(String h)    => h.startsWith('@steward.');
bool isTeamLevelStewardHandle(String h) =>
    isGeneralStewardHandle(h) || isPeerStewardHandle(h);
bool isStewardHandle(String h) =>
    isTeamLevelStewardHandle(h) ||
    h == 'steward' ||
    h.endsWith('-steward');
```

Replace any call site that currently special-cases `@steward` with the right tier-specific predicate. Audit the 9 sites listed in [`plans/multi-steward.md`](multi-steward.md) §1, plus the steward-state handler at `hub/internal/server/handlers_steward_state.go` and the home-tab card.

Add a small Dart helper `peerKindFromHandle('@steward.code') == 'steward.peer.code.v1'` for round-tripping in spawn flows.

Tests: dart unit tests on the predicates with the 4 handle shapes; round-trip on `peerKindFromHandle`.

### W5. Stewards overview screen — peer section

Existing plan: [`plans/multi-steward.md`](multi-steward.md) §4.1 already designed a Stewards overview as wedge 3 of the multi-steward effort. Extend that screen with a new section between General and Project:

```
─ General ─────────────────────────
@steward                  [▶ open]
  claude · opus · host=hub
  3 sessions · 2h ago

─ Team peers ──────────── [+ new peer]
@steward.code             [▶ open]
  codex · gpt-5 · host=hub
  1 session · 18m ago
@steward.ops              [▶ open]
  gemini · 2.5 · host=hub
  active

─ Project domain ─────────────────
research-steward (project Foo)  [▶]
infra-east-steward (project Bar) [▶]
```

The `[+ new peer]` affordance opens a sheet:

- Picker for which peer kind to spawn (filtered to `steward.peer.*.v1` overlay+bundled templates the team has access to).
- Calls `POST /v1/teams/{team}/steward.peer/ensure` with the chosen kind.
- Pushes the new peer's session on success.

If the multi-steward overview screen has not yet shipped at W5 time, this wedge ships *with* it (i.e. W5 = create the screen + render all three sections at once).

Tests: golden-image of the section-grouped layout with a fixture team holding all three tiers; spawn-flow integration test.

### W6. `@`-mention picker surfaces peer handles

`AgentCompose`'s `@`-prefix suggestion strip currently sources mentions from the active session.init payload (`lib/widgets/agent_compose.dart` `_activeMatch()` + `widget.mentions`). The hub already publishes the current team's agent handles in the session.init `mentions` list; the only thing missing is that **peer handles must be included** for any session — not just the steward's own.

Two pieces:

1. **Hub side** — `session.init` payload generation includes all live `@steward.*` handles in the team's mentions list, regardless of which agent's session is opening. (Today the list scopes to "agents you can see," which already covers peers; verify and add a test.)
2. **Mobile side** — no code change if the hub-side data is correct; just verify the strip renders peer handles.

Tests: integration test that opens a session in a team with one peer steward live and asserts `@steward.code` appears in the mentions strip.

### W7. Allow project domain steward → peer A2A handoff

ADR-016 forbids worker→non-parent A2A edges by default. Peers as handoff targets need an explicit allowance: a project domain steward (e.g. `research-steward`) should be able to invoke `@steward.code` for a code-review consultation without the middleware rejecting the call.

Implementation:

1. Update the A2A authorisation middleware to allow `<any-agent>` → `<peer-steward>` when the caller has the `peer-consult` capability. Default-on for all domain stewards (since cross-tier consultation is the design intent).
2. The peer's task-handling MCP namespace (`mcp__termipod__a2a__handle_task`, etc.) is unchanged; it just sees a new caller class.

Tests: integration test that a research-steward can post a task to `@steward.code` and the call lands in the peer's input router.

This is the "biggest" wedge in terms of system-level implications — review carefully against ADR-016 D5 (engine-internal subagents) before merging.

## 6. Order discipline

Wedges 1–4 are foundation: they ship the ability to have a peer at all, but no surface user-facing features. Wedges 5–7 surface the foundation in the UI and routing layer.

Recommended sequence (one wedge per commit/version bump, do not bundle):

1. W1 — refactor (zero-risk, preserves existing tests)
2. W2 — endpoint + templates (new entry point, invisible to existing UIs)
3. W3 — role bucket (locks the security model before any UI exposes peers)
4. W4 — mobile predicates (no UI change — predicates are correct for any future caller)
5. W5 — Stewards overview screen (the first user-visible peer)
6. W6 — `@`-mention surfacing (peers reachable from compose)
7. W7 — A2A handoff (peers reachable from other stewards)

After W4 the system is *capable* of holding peers; after W5 the user can *create* them; after W6/W7 they're *reachable* from the relevant surfaces.

## 7. Risks

- **Two team-scoped tiers confuses the picker UX.** The director sees `@steward` and `@steward.code` in the same mention list; the difference (concierge vs specialist) only resolves in the prompt body. Mitigation: subtitles in the picker (`code review specialist`, `team concierge`) sourced from the template's `description:` field.
- **Cross-tier permission overlap.** A peer's `templates.list` capability lets it see (but not modify) `steward.general.v1`; an over-eager peer might propose edits that go nowhere. Mitigation: the no-self-mod rule (ADR-016 D7) plus a UX cue when a peer references a frozen template ("this is read-only").
- **A2A blast radius (W7).** A misbehaving project steward could spam `@steward.code` with low-quality requests. Mitigation: rate-limit per-caller per-target, follow-up if real traffic shows the issue.
- **Template proliferation.** Without governance, every new hand-rolled peer kind ends up overlay-only on one team and re-discovery is painful. Mitigation: keep the bundled seed set small (W2 ships only `code` + `ops`); document the authoring contract in `reference/steward-templates.md`.
- **Routing-precedence drift.** If a future "auto-route" feature picks the wrong tier, debugging is harder than today's "you typed the handle yourself." Mitigation: keep W7's routing explicit — the *user* (or domain steward) names the target.

## 8. Open questions

- **What's the right initial bundled peer set?** Code + ops covers the most-asked-for cases. Should `steward.peer.security.v1` ship initially, or wait for a request? Recommendation: ship the two; add others on demand.
- **Per-kind permission narrowing in roles.yaml.** Should `steward.peer.code.v1` get `templates.edit` while `steward.peer.ops.v1` gets `schedules.edit` instead? Open. W3 ships permissive defaults; the narrowing wedge is post-MVP.
- **Mobile editor for peer templates.** Existing template editor (TemplatesScreen) covers any agent template. Verify it picks up `steward.peer.*` files; UX-tag if filtering is needed.
- **Discoverability of peers from the home tab.** Today the home-tab steward card surfaces only the general. Should it also list active peers (collapsible "and 2 peer stewards…")? Held; first see how the Stewards overview lands.

## 9. Verification

When the plan is fully implemented (W1–W7 shipped):

1. Open the Stewards overview screen on a fresh team. General appears; no peers. Tap "+ new peer", pick `steward.peer.code.v1`. Verify the peer appears in the Team-peers section and a session opens against it.
2. From the Me-tab compose box, type `@s` and verify both `@steward` and `@steward.code` appear in the suggestion strip with appropriate subtitles.
3. From a research project's domain steward, A2A-invoke `@steward.code` with a method-phase commit ref. Verify the peer's input router receives the task and the task surfaces on its session feed.
4. Try to spawn a second `steward.peer.code.v1` on the same team. Verify the API returns the existing instance (idempotency).
5. Archive the peer; ensure-spawn returns a fresh agent (respawn).
6. Try to call `runs.start` from inside a peer's MCP session. Verify the role-middleware rejects with a clear "peer-steward role is read-only on `runs`" error.
7. Try to edit `steward.peer.code.v1.yaml` from the peer steward's own MCP. Verify ADR-016 D7 self-mod guard rejects.

## 10. References

- [ADR-017 Amendment 1](../decisions/017-layered-stewards.md#amendment-1--peer-steward-tier-2026-05-06) — the design decision this plan implements.
- [ADR-016: subagent scope manifest](../decisions/016-subagent-scope-manifest.md) — D6 role middleware, D7 self-mod guard.
- [ADR-004: single-steward MVP](../decisions/004-single-steward-mvp.md) — what this amendment further evolves.
- [Plan: multi-steward](multi-steward.md) — handle convention W1+W2 (shipped); Stewards overview screen design.
- [Reference: steward templates](../reference/steward-templates.md) — template authoring contract.
- Code: `hub/internal/server/handlers_general_steward.go` (W1 source of refactor), `hub/internal/server/roles.yaml` (W3), `lib/services/steward_handle.dart` (W4), `lib/widgets/agent_compose.dart` (W6).
