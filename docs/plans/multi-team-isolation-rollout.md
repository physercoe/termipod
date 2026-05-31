# Multi-team isolation — phased rollout

> **Type:** plan
> **Status:** In progress (2026-05-31) — ADR-037 open questions Q1–Q4
> are resolved. **W1 shipped (v1.0.760-alpha); W2 shipped
> (v1.0.761-alpha) — the W1+W2 MVP isolation bar is met and ADR-037 is
> Accepted.** W3 (provisioning) next.
> **Audience:** contributors
> **Last verified vs code:** v1.0.761

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
