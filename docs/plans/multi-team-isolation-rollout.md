# Multi-team isolation — phased rollout

> **Type:** plan
> **Status:** Ready (2026-05-31) — ADR-037 open questions Q1–Q4 are
> resolved; wedges below reflect the locked branches. Not started.
> **Audience:** contributors
> **Last verified vs code:** v1.0.754

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

### W1 — Path-team authorization gate *(the isolation gate)*

**Goal.** A token scoped to team T may only address `/v1/teams/T/…`.

- Add a middleware on the team-scoped route group that reads
  `chi.RouteContext(r.Context()).URLParam("team")`; when non-empty,
  require `scope_json.team == team` or `Kind == "operator"`; else `403`.
  No `{team}` param (admin/_info) → no-op.
- Refactor `server.go` route registration so team-scoped routes share a
  `r.Route("/v1/teams/{team}", …)` group the middleware mounts on
  (today they are registered flat).
- Confirm the in-process agent dispatch passes (agent token's
  `scope.team` == the path it builds).
- **Tests:** cross-team `403` for owner/user/host; same-team `200`;
  operator bypass; agent in-process self-call still works; the existing
  `default` owner keeps full access to `default`.
- **Files:** `internal/auth/token.go` (or a new `internal/auth/team.go`),
  `internal/server/server.go`. **Risk:** route-group refactor is broad
  but mechanical; the gate logic is small.

### W2 — Operator / principal split

**Goal.** A hub operator (cross-team ops) distinct from a per-team
principal (`owner`).

- Add token kind `operator` (extend the F-01 allowlist `token.go:134`;
  add `requireOperator`). Operators are exempt from the W1 team gate.
- Re-gate `/v1/admin/*` from `requireOwner` → `requireOperator`
  (`handlers_admin*.go`). Keep `requireOwner` for per-team owner
  actions (now also behind the W1 gate).
- Bootstrap (`init.go`): mint an **operator** as the hub root (home
  team `default`); **stop minting a `default` owner**. Reclassify the
  existing bootstrap owner token as operator on migrated installs, and
  rework `seed_demo_lifecycle.go` to not need a `default` owner.
- **Tests:** owner blocked from `/v1/admin/*`; operator allowed;
  operator bypasses the W1 team gate; owner issues tokens only for its
  own team.
- **Files:** `internal/auth/token.go`, `internal/server/handlers_admin*.go`,
  `handlers_tokens.go`, `init.go`. **Risk:** touches every admin
  handler + bootstrap; back-compat for the existing owner token.

### W3 — Team provisioning

**Goal.** Onboard a tester as `(team_id, owner_token)`.

- Operator-gated `POST /v1/admin/teams` → `ensureTeam` +
  `auth.InsertToken` (owner scope for the new team) → return the
  one-time owner token.
- Hub-server CLI `team create <id>` for out-of-band bootstrap.
- **Tests:** provision → the new owner can reach only its team (exercises
  W1) and cannot reach `/v1/admin/*` (exercises W2).
- **Files:** new `handlers_admin_teams.go`, a CLI subcommand under
  `cmd/hub-server`. **Risk:** low; reuses existing primitives.

### W4 — Per-team template overrides

**Goal.** One team's template edits invisible to others.

- Keep embedded built-ins global read-only; move on-disk user overrides
  to `<dataRoot>/teams/<team_id>/templates/…`; resolver checks the team
  dir then embedded. (DB `project_templates` already team-keyed.)
- **Files:** `init.go` (dir layout), `handlers_agent_families.go`,
  template resolver(s). **Risk:** medium; template path resolution is
  threaded through several read sites — grep `team/templates`.

### W5 — Team-scoped workdir

**Goal.** No two teams share a mutable on-host path.

- Thread `team_id` into `DeriveWorkdir` (`spec.go`) and prefix the path
  with the team (`~/hub-work/<team_id>/…`); update callers
  (`launch_m1/m2/m4*.go`).
- **Shared-host guard (the decided model):** the host-runner spawns a
  team's agents under a **per-team OS user** (or restricted perms on
  `~/hub-work/<team_id>/`), so the OS — not a shell `cd` block — denies
  cross-team access. Add a host-runner capability check (can it
  create/assume per-team users?); degrade or refuse cleanly if not.
- In-flight agents keep their persisted `worktree_path`; only new
  spawns adopt the segment.
- **Tests:** `DeriveWorkdir` table test gains the team segment; two
  teams + same project-prefix/handle resolve to distinct paths; the
  per-team-user mapping is unit-tested.
- **Files:** `internal/hostrunner/spec.go`, `launch_m*.go`, host-runner
  spawn path (OS-user mapping). **Risk:** medium-high; the OS-user
  guard is new surface and load-bearing for isolation. Residual
  shared-kernel risk is documented in ADR-037 D6 as a hardening
  follow-up (sandbox/pinned hosts if testers become untrusted).

### W6 — Cross-cutting sweep

**Goal.** No storage path bypasses the team boundary.

- Grep + verify: A2A relay attribution (`handlers_a2a.go`), blob paths
  under `<dataRoot>/blobs/…`, any hub-wide `SELECT` missing `team_id`.
  Team-scope each or document why it is safe.
- **Risk:** unknown until the grep; treat findings as their own
  micro-wedges.

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
