# Multi-team isolation — phased rollout

> **Type:** plan
> **Status:** Complete (2026-05-31) — ADR-037 open questions Q1–Q4
> resolved; **all six wedges shipped: W1 path-team gate (v1.0.760), W2
> operator/principal split (v1.0.761), W3 provisioning (v1.0.762), W4
> per-team templates (v1.0.763), W5 team-scoped workdir (v1.0.764), W6
> cross-cutting sweep (v1.0.765).** Remaining hardening
> (per-team-OS-user spawn, blob ownership, team deletion) is tracked as
> separate follow-ups.
> **Audience:** contributors
> **Last verified vs code:** v1.0.765

**TL;DR.** Turn termipod's already-team-scoped data layer into enforced
multi-team isolation so external testers can each be handed a `team_id`
that cannot see, touch, control, or collide with another team's. Six
wedges, ordered by isolation-per-unit-of-risk: **W1** authorizes the
URL team against the token (the actual isolation gate); **W2** splits
`owner` into a hub operator and a per-team principal; **W3** lets an
operator provision a team; **W4/W5** close the on-disk template and
workdir leaks; **W6** sweeps the residual storage paths. Decisions are
locked in
[decisions/037-multi-team-isolation-and-operator-principal-split.md](../decisions/037-multi-team-isolation-and-operator-principal-split.md);
motivation in
[discussions/multi-team-isolation.md](../discussions/multi-team-isolation.md).

Each wedge is independently shippable and CI-green before the next.
W1 alone makes cross-team data access impossible; W2 makes the
operator/principal boundary real; together they are the MVP isolation
bar. W3 unblocks onboarding. W4–W6 harden.

---

## Wedge sequence

### W1 — Path-team authorization gate *(the isolation gate)* — **SHIPPED v1.0.760-alpha**

**Goal.** A token scoped to team T may only address `/v1/teams/T/…`.

**Outcome.** Shipped as `Server.teamGate` (`internal/server/team_gate.go`),
mounted via `r.Use(s.teamGate)` on the existing `/v1/teams/{team}`
group. Notable deltas from the original sketch below:

- **No route-group refactor was needed.** The routes were *already*
  registered under a single `r.Route("/v1/teams/{team}", …)` group
  (`server.go:302`) — the plan's "registered flat" note was stale. The
  gate is one `r.Use` line.
- **Scope-team helper landed in `auth`, not the gate.** Added
  `auth.Token.ScopeTeam()` (parses `scope_json.team`) and
  `auth.WithToken()` (the `FromContext` constructor, for unit tests).
  The gate itself lives in `server` because it needs `chi.URLParam` +
  `writeErr`.
- **Operator bypass is wired but only unit-tested.** `operator` is not
  yet a legitimate bearer (the F-01 allowlist admits it in W2), so the
  end-to-end operator test joins the suite in W2. The branch is in
  place so W2 is a pure allowlist change.
- **In-process agent dispatch confirmed safe** — it builds the path
  from the agent token's own `scope.Team` (`mcp.go:410,422`), so the
  gate passes legitimate calls with no special case.
- **Fail-closed on a teamless token** (empty `scope_json.team`) → `403`.
- **Test-fixture sweep:** ~30 existing tests minted the `default`-scoped
  bootstrap `Init` token but addressed another team; they now mint a
  team-scoped token (`mintTeamToken`). One was a *real* latent
  cross-team reference (a deliverable's document created in `default`
  while the deliverable lived in the test team) — fixed by threading
  the team through `createTypedDocument`/`mustCreateAnnotation`.

**Tests:** `team_gate_test.go` — cross-team `403` for owner/user/host,
same-team admitted, teamless fail-closed, operator bypass (unit).
Full `go test ./...` green.

### W2 — Operator / principal split — **SHIPPED v1.0.761-alpha**

**Goal.** A hub operator (cross-team ops) distinct from a per-team
principal (`owner`).

**Outcome.** Shipped. Deltas from the original sketch:

- **`operator` ⊇ `owner` (the load-bearing decision).** `requireOwner`
  now admits an operator too, so the bootstrap operator remains the
  de-facto director of its home team `default` (issues `default`'s
  tokens, decides its attention). `requireOperator` is operator-only.
  This hierarchy is what kept the test impact small — `Init` now returns
  an operator, and operator passes every gate an owner did.
- **Re-gated 14 sites** across `handlers_admin*.go` + `handlers_hub_config.go`
  to `requireOperator`; the 3 `handlers_tokens.go` sites stay
  `requireOwner` (per-team token mgmt).
- **One extra authz site found by tests:** `principalActor` (F-04,
  attention decide/override) allowlisted `owner|user` — added
  `operator`. (Swept all token-kind authz checks; this was the only
  other one.)
- **Migration `0047_owner_tokens_to_operator`** converts existing owner
  tokens → operator (pre-split every owner was a hub root, so reach is
  preserved). New per-team owners minted post-split stay `owner`.
- **`seed_demo_lifecycle.go` needed NO rework** — verified it inserts
  *data* principals, not auth tokens, so it never depended on a
  `default` owner (the ADR's concern was speculative).
- **CLI:** `hub init` prints "Operator token (the hub root)"; `tokens
  issue --kind` accepts `operator`.
- **Mobile:** no required change in W2 — the hub enforces operator at
  `/v1/admin/*`; hiding fleet controls from non-operators in the Admin
  pane is a UX follow-up.

**Tests:** `handlers_operator_test.go` — bootstrap mints operator; owner
refused at `/v1/admin/*` while operator passes; operator bypasses the W1
gate end-to-end (upgrades W1's unit-level bypass test); owner issues
own-team tokens. Full `go test ./...` green.

### W3 — Team provisioning — **SHIPPED v1.0.762-alpha**

**Goal.** Onboard a tester as `(team_id, owner_token)`.

**Outcome.** Shipped. `server.ProvisionTeam` is the shared core (validate
slug → 409 if exists → insert team → mint owner), called by both the
operator-gated `POST /v1/admin/teams` (+ `GET` list) and the
`hub-server team create <id>` / `team ls` CLI. Notes:

- **Team id is a DNS-label slug** (`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
  — it lands in URL paths and (W5) on disk, so no leading/trailing
  hyphen, no separators, lowercase only.
- **No per-team template seeding** — built-ins are global (D5), so a
  fresh team can spawn from them immediately; W4 adds overrides.
- **Channel leak surfaced (deferred to W6):** team-scope channels
  (`hub-meta`) are hub-wide — `handleListTeamChannels` filters
  `scope_kind='team' AND project_id IS NULL` with no team column (the
  `channels` table lacks `team_id`). A provisioned team shares the global
  `hub-meta`; closing it needs a schema migration (W6 / D7).

**Tests:** `handlers_admin_teams_test.go` — onboarding contract (new
owner reaches only its team, not default, not `/v1/admin/*`, cannot
provision siblings), duplicate→409, invalid-id→400, requires-operator,
list. Full `go test ./...` green.

**Files:** `internal/server/provision.go`, `handlers_admin_teams.go`,
`server.go` (routes), `cmd/hub-server/main.go` (`team` subcommand).

### W4 — Per-team template overrides — **SHIPPED v1.0.763-alpha**

**Goal.** One team's template edits invisible to others.

**Outcome.** Shipped for **agent + prompt templates** (the
spawn-critical, commonly-edited path). New helper `teamTemplatesDir` +
`resolveTeamTemplatePath`. Resolution order is **per-team override
(`<dataRoot>/teams/<team>/templates/…`) → global operator baseline
(`<dataRoot>/team/templates/…`) → embedded built-in** — a strict
superset of the ADR's "team dir → embedded" (the global baseline is kept
as a read-only operator fallback, so no FS migration of existing
`default` edits was needed; new teams are clean).

- **Threaded `team`** through `readAgentTemplate`, `readPromptTemplate`,
  `loadBuiltinAgentTemplate`, `mergeTemplateReference`,
  `resolveContextFiles` (+ `renderSpawnSpec`/`buildSpawnVars`/`DoSpawn`
  callers and ~14 test call sites).
- **Writes per-team:** REST `PUT`/`DELETE`/`PATCH`, `GET`/`LIST` (overlay
  shadows baseline by name), and the `template.install` governed action
  (`installProposedTemplate(team, …)`). Delete/rename touch only the
  team's own overrides.
- **Hub-global, left alone (by design):** agent-families
  (`<dataRoot>/agent_families`) and the envelope config — engine/system
  config, not per-team work templates.
- **Deferred sub-item — project-template disk YAML.**
  `readProjectTemplateYAML` (phase tile/widget/criteria hydration) still
  reads the global baseline; its 6-caller cascade is a deep thread for
  low value, and the instantiated `project_templates` rows are already
  team-keyed (project *data* is isolated). Thread `team` through the
  hydration cascade when a concrete per-team project-template need
  appears.
- **Files:** `template.go`, `handlers_templates.go`,
  `handlers_general_steward.go`, `handlers_project_steward.go`,
  `handlers_attention.go`, `apply_template_install.go`,
  `handlers_agents.go`. **Risk realised:** medium — the resolver
  threading touched the render path + many test sites, but the
  3-tier-with-baseline design kept it additive (no behaviour change for
  the global tier).

**Tests:** `handlers_templates_isolation_test.go` — team-a's override
lands only in its dir, team-b GET 404s, lists don't cross, and
`readAgentTemplate` resolves per-team (team-b can't resolve team-a's
override). Full `go test ./...` green.

### W5 — Team-scoped workdir — SHIPPED (v1.0.764-alpha)

**Goal.** No two teams share a mutable on-host path.

**Outcome.** Every derived agent workdir now carries a `<team>` segment,
and the per-team root is reserved 0o700. **The host-runner is a
single-team process** (`--team`), so the team threads in from
`Client.Team` at the launch call sites — no hub/JSON change was needed
(the earlier note assuming the team had to be stamped into the spawn was
overtaken by this: the runner already knows its team).

- **`DeriveWorkdir(team, …)`** (`spec.go`) derives
  `~/hub-work/<team>/<pid8>/<handle>` (project-bound) and
  `~/hub-work/<team>/_team/<handle>` (project-less steward) via a new
  `teamWorkRoot(team)` helper. An operator-pinned `default_workdir` is
  taken verbatim (no segment); an **empty** team collapses to the legacy
  `~/hub-work/…` path, keeping pre-W5 callers and demo spawns unchanged.
- **The M4 launchers were the load-bearing catch.** `launch_m4_*` inline
  their own derivation (they do *not* call `DeriveWorkdir`), so threading
  only `DeriveWorkdir` would have missed **claude-code — the primary
  engine**. Both M4 paths (claude-code locallogtail + antigravity) now
  route through `teamWorkRoot`, so all four launch modes get the segment.
- **Shared-host guard (FS-perms tier of the decided model):**
  `ensureTeamWorkRoot(team)` creates `~/hub-work/<team>` 0o700 before the
  full workdir, called by all four launchers. This is the OS-enforced
  boundary **when teams run under distinct OS users**; under a single
  shared uid it walls the fleet off from *other* OS users and lays the
  path a per-team-user spawn keys on.
- **Deferred (ADR-037 D6 residual risk):** the **per-team-OS-user spawn**
  (true cross-team isolation under one uid) and its host-runner
  capability check are *not* in W5 — they need an on-host spawn mechanism
  (sudo/setuid + user provisioning) that can't be validated on the dev
  box, and the natural seam is "run each team's single-team host-runner
  as a per-team OS user." Tracked as the hardening follow-up alongside
  bwrap/container sandboxing.
- In-flight agents keep their persisted `worktree_path`; only new spawns
  adopt the segment.
- **Tests** (`spec_test.go`): the `DeriveWorkdir` table gains the team
  segment + an empty-team back-compat row; `TestDeriveWorkdir_TeamsDoNotCollide`
  asserts two teams with the same project-prefix/handle resolve to
  distinct paths; `TestEnsureTeamWorkRoot` checks the 0o700 perm + the
  empty-team no-op. Full `go test ./...` green.
- **Files:** `internal/hostrunner/spec.go`, `launch_m1.go`,
  `launch_m2.go`, `launch_m4_locallogtail.go`, `launch_m4_antigravity.go`,
  `runner.go`. **Risk realised:** medium — the segment threading was
  additive (empty team = legacy path), and the OS-user tier was
  deliberately deferred rather than half-built.

### W6 — Cross-cutting sweep — SHIPPED (v1.0.765-alpha)

**Goal.** No storage path bypasses the team boundary.

**Outcome.** The grep found three real leaks (channels, channel-event
handlers, `/v1/search`) and two safe-by-design paths (A2A, blobs); all
are now closed or documented.

- **`channels.team_id` (the highest-value item, surfaced in W3) —
  FIXED.** Team-scope channels (`scope_kind='team'`, `project_id` NULL)
  had no team binding, and `ensureTeamChannel` /
  `handleListTeamChannels` / `handleGetTeamChannel` / `mcpListChannels`
  filtered without a team — so every team shared one `#hub-meta`. (The
  earlier note that the `UNIQUE` *prevents* a second `hub-meta` was
  imprecise: SQLite treats NULL `project_id` as distinct, so the real
  effect was that `ensureTeamChannel` found the first team's row and
  never created a second.) **Migration 0048** rebuilds `channels` with
  `team_id NOT NULL` (project-scope backfilled from `projects.team_id`,
  team-scope from `default`), adds a partial unique index
  `(team_id, scope_kind, name) WHERE project_id IS NULL` so each team
  gets its own `#hub-meta`, and the SQLite rebuild runs under the
  migrations connection's `foreign_keys=OFF` so `DROP TABLE channels`
  doesn't cascade-wipe `events` / `channel_members`. All five query
  sites are team-scoped; `handleCreateChannel` (project-scope) and
  `ProvisionTeam` (seeds the new team's `#hub-meta`) updated too.
- **Channel event handlers — class-level guard added.**
  `handlePostEvent` / `handleListEvents` / `handleStreamEvents` consume a
  bare `{channel}` id and never verified its team; new
  `requireChannelTeam` 404s a foreign/missing channel, closing the
  read/write/stream-by-id hole rather than patching each query.
- **`/v1/search` — FIXED.** The FTS5 match over `events` had no team
  filter, returning every team's message text to any bearer. It now
  joins `channels` and filters `c.team_id = <token team>` (the route is
  outside the `/v1/teams/{team}` group, so it scopes by the caller's
  token, not a path param); a teamless token 403s.
- **A2A relay — verified safe (no change).** `a2a_cards` carry `team_id`;
  `a2a_notify` and `tunnel_a2a` resolve the team from the agent and
  filter on it.
- **Blobs — documented residual.** `/v1/blobs` is a content-addressed
  global dedup store (sha256 PK, no `team_id`); a cross-team read needs
  an exact unguessable hash, undiscoverable once the channel/search
  leaks are closed. Team-scoping needs a blob-ownership/refs model
  (ADR-level, would break dedup) — tracked as a hardening follow-up.
- **Tests:** `handlers_channels_isolation_test.go` — two teams don't
  share `#hub-meta`; one team can't list/get/post/stream another team's
  channel; `/v1/search` returns only the caller team's events. Plus the
  fixture updates for the now-enforced per-team channel uniqueness. Full
  `go test ./...` green.
- **Files:** `migrations/0048_channels_team_id.{up,down}.sql`,
  `handlers_channels.go`, `handlers_events.go`, `handlers_stream.go`,
  `handlers_search.go`, `mcp.go`, `init.go`, `provision.go`.
- **Risk realised:** medium — the migration is a table rebuild, but the
  rebuild pattern (0023) and `foreign_keys=OFF` migration connection are
  well-trodden; the per-team uniqueness surfaced two test fixtures that
  leaned on the old NULL-distinct loophole (fixed).

**Multi-team isolation rollout is complete (W1–W6).** Remaining
hardening — per-team-OS-user spawn (D6), blob ownership, and team
deletion/offboarding — are tracked as separate follow-ups, not part of
this plan.

## Sequencing & gates

- **W1 → W2** are the MVP isolation bar; ship both before any tester
  gets a non-`default` team.
- **W3** is the onboarding unblock; needs W1+W2.
- **W4, W5, W6** can land in any order after W2; W5 needs the Q2
  decision, W4 needs Q4.
- Each wedge: hub `go build ./... && go test ./...` green, then push,
  then read the explicit CI conclusion. Mobile changes (operator-vs-
  owner in the Admin pane, surfaced by W2) are CI-only (no local
  Flutter) — budget a watch-CI round-trip.

## Decisions

The four design forks are resolved — see
[ADR-037 §Resolved decisions](../decisions/037-multi-team-isolation-and-operator-principal-split.md#resolved-decisions-2026-05-31):
Q1 new `operator` kind, Q2 shared host + per-team-OS-user guard, Q3
`default` is the operator's home (no default owner), Q4 global built-ins
+ per-team overrides. No blockers remain; W1 is the recommended first
PR.
