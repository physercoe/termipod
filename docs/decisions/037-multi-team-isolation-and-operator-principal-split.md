# 037. Multi-team isolation and the operator/principal split

> **Type:** decision
> **Status:** Accepted (2026-05-31) — the four open questions are
> **resolved** (§Resolved decisions); **W1 (D1 path-team gate,
> v1.0.760), W2 (D2/D4 operator/principal split, v1.0.761), W3 (D3
> provisioning, v1.0.762), W4 (D5 per-team templates, v1.0.763), and W5
> (D6 team-scoped workdir, v1.0.764) have landed.** D7 (cross-cutting
> sweep) remains as tracked wedge W6; the D6 per-team-OS-user guard is a
> documented hardening follow-up (see D6 impl note).
> **Audience:** contributors
> **Last verified vs code:** v1.0.764

**TL;DR.** External testers are being onboarded; each gets a `team_id`
and different teams MUST isolate. The data layer is already
team-scoped — every query filters `WHERE team_id = ?` from the
`/v1/teams/{team}/…` path — but nothing authorizes that path team
against the caller's token, and the `owner` credential is a hub-wide
root. We **(1)** add a path-team authorization gate so a token may only
address the team in its `scope_json.team`, and **(2)** split the
conflated `owner` credential into a **hub operator** (cross-team ops,
provisioning) and a **per-team principal** (the director of one team).
Provisioning, per-team template overrides, and a team-scoped agent
workdir follow. The full gap analysis and rationale live in
[discussions/multi-team-isolation.md](../discussions/multi-team-isolation.md);
the wedge sequence is in
[plans/multi-team-isolation-rollout.md](../plans/multi-team-isolation-rollout.md).

## Context

`docs/discussions/multi-team-isolation.md` catalogs six gaps (G1–G6),
all verified against code. The load-bearing facts:

- **The data edge already isolates; the auth edge does not.** Handlers
  filter on the path team (`handlers_agents.go:127`, `:197`, …), but
  `auth.Middleware` (`internal/auth/token.go:106-152`) never compares
  the path `{team}` to the token's `scope_json.team`. Any valid
  `owner` / `user` / `host` token can address any team by editing the
  URL (G1).
- **`owner` is a hub-wide root with no team binding.** `requireOwner`
  checks only `Kind == "owner"` (`handlers_tokens.go:49`); the
  owner-gated `/v1/admin/*` endpoints are deliberately cross-team
  (`handlers_admin_audit.go:10-22` "drops the team filter"). A tester's
  owner would shut down the fleet and read other teams' audit (G2).
- **No provisioning path.** Only `default` is ever created
  (`init.go:21,43`); token issuance is owner-gated *under a team*
  (`handlers_tokens.go:116`), a chicken-and-egg for a new team (G3).
- **Templates and workdirs are team-blind.** On-disk templates live in
  a shared `<dataRoot>/team/templates/…` (`init.go:30`); `DeriveWorkdir`
  has no team segment (`spec.go:132-159`), so two teams sharing a host
  collide on disk (G4, G5).

This ADR supersedes the single-team assumption in
[005](005-owner-authority-model.md) (owner == director == operator) by
splitting that role along the team axis. ADR-005's "the human is the
authority root" axiom is unchanged; we are partitioning *which* root
governs *which* scope.

## Decision

### D1 — A path-team authorization gate (G1)

A request authenticated as team **T** may only address resources under
`/v1/teams/T/…`. Enforcement is a **single middleware** on the
team-scoped route group, not a per-handler call — the per-handler shape
is exactly what let G1 exist (one missed handler = one hole).

- The gate reads the matched route's `{team}` URL param
  (`chi.RouteContext(r.Context()).URLParam("team")`). If the route has
  no `{team}` param (e.g. `/v1/admin/*`, `/v1/_info`), the gate is a
  no-op — those are governed by D2.
- If present, require `token.scope_json.team == {team}` **or**
  `token.Kind == "operator"` (operators are team-transcendent, D2).
  Mismatch → `403`.
- **Agents:** the in-process authority dispatch
  (`auth.WithInProcessDispatch`, `mcp_authority.go`) carries the
  agent's own token, whose `scope_json.team` is the agent's team
  (`handlers_agents.go:1323`); since the dispatch builds the path with
  that same team, the gate passes for legitimate calls and blocks a
  forged cross-team path. No special case.

### D2 — Split `owner` into operator and principal (G2) — *resolved Q1: new kind*

A new token **kind** `operator`, distinct from the per-team `owner`
(chosen over a `scope.team == "*"` flag for a clean, auditable mental
model — see §Resolved decisions Q1).

- **operator** — the hub root. The only credential allowed at
  `/v1/admin/*` (fleet shutdown/update/restart, cross-team audit,
  token rotation, kill-any-agent) and at team provisioning (D3).
  Team-transcendent: exempt from the D1 gate. Added to the F-01 bearer
  allowlist (`token.go:134`).
- **owner** — the **per-team principal/director**. Authority root for
  one team (spawn, decide, issue that team's `user`/`host` tokens).
  No `/v1/admin/*` reach.
- `requireOwner` is re-pointed: `/v1/admin/*` call sites switch to a
  new `requireOperator`; per-team owner actions (e.g.
  `/v1/teams/{team}/tokens`) keep `requireOwner` but now also pass the
  D1 gate, so an owner issues tokens only for its own team.

### D3 — Team provisioning (G3)

An operator-gated `POST /v1/admin/teams` creates a team and mints its
first `owner` token, returning `(team_id, owner_token)` once (hash-only
storage, same one-time-display contract as `init.go:58-62`). A
companion hub-server CLI subcommand (`team create <id>`) provides an
out-of-band path that doesn't need a live operator token. Both reuse
the existing `ensureTeam` (`init.go:87`) + `auth.InsertToken`.

### D4 — Bootstrap; `default` is the operator's home (G3) — *resolved Q3*

`init` mints **one operator** token (the hub root) whose home team is
`default`, and stops minting a separate `default` *owner*. `default`
remains a seeded team row (the operator and the demo run inside it),
but it has no dedicated per-team principal — the operator is its
de-facto director and reaches it by bypassing the D1 gate. Existing
single-user installs: the bootstrap token is now an **operator**, which
keeps full access to `default` (and everything else). The demo seed
(`seed_demo_lifecycle.go:220`) and any "`default` has a director"
assumption are reworked to not depend on a `default` owner.

### D5 — Per-team template overrides (G4) — *resolved Q4: global + overrides*

Built-in templates stay **global and read-only** (they ship embedded in
the binary via `//go:embed`, so a new engine/prompt ships once for all
teams); only user-authored on-disk overrides move under
`<dataRoot>/teams/<team_id>/templates/…`. The resolver checks the
team's override dir first, then the embedded built-ins. DB
`project_templates` are already `team_id`-keyed (`init.go:221-224`).
(Chosen over copy-per-team, which would drift built-ins across teams —
§Resolved decisions Q4.)

*Implementation note (W4, v1.0.763):* shipped as a 3-tier resolver —
**per-team override → global operator baseline (`team/templates/`) →
embedded** — a superset of the above that keeps the existing global dir
as a read-only operator fallback, so no FS migration of existing
`default` edits was needed and new teams are still clean. Agent + prompt
templates are per-team; project-template *disk YAML* hydration is a
deferred follow-up (its `project_templates` rows are already team-keyed,
so project data is isolated). Agent-families and the envelope config stay
hub-global (system config, not per-team work templates).

### D6 — Team-scoped workdir on a shared host (G5) — *resolved Q2: shared + guard*

Hosts are **shared** across teams (testers do not each get a dedicated
box). So `team_id` threads into `DeriveWorkdir` and the segment is
**mandatory**:
`~/hub-work/<team_id>/<pid8>/<handle>` and
`~/hub-work/<team_id>/_team/<handle>`.

Because the box is shared, path separation alone is not isolation — a
stochastic agent can run arbitrary shell and `cd` out of its subtree.
The guard:

- **MVP (semi-trusted testers):** the host-runner spawns each team's
  agents under a **per-team OS user** (or restricted filesystem
  permissions on `~/hub-work/<team_id>/`), so the OS denies a
  cross-team read/write regardless of where the agent `cd`s. This is
  the concrete guard, not a shell-level `cd` block (which is
  unenforceable).
- **Residual risk (documented, not yet closed):** a determined
  adversarial agent sharing a kernel is a weaker boundary than separate
  hosts. If testers become untrusted, revisit with per-team sandboxing
  (bwrap/container) or pinned hosts. Tracked as a hardening follow-up,
  out of scope for the isolation MVP.

In-flight agents keep their persisted `worktree_path`
(`agents.worktree_path`); only new spawns adopt the segment.

*Implementation note (W5, v1.0.764):* the **workdir team segment** and
the **0o700 FS-perms guard** shipped; the **per-team-OS-user spawn** did
not (deferred, see below). `DeriveWorkdir(team, …)` and a shared
`teamWorkRoot(team)` helper (`internal/hostrunner/spec.go`) prefix every
*derived* workdir with `<team>`; an operator-pinned `default_workdir` is
taken verbatim and an empty team collapses to the legacy `~/hub-work/…`
path (back-compat). The host-runner is a **single-team process**
(`--team`), so the team threads in from `Client.Team` at the four launch
call sites — no spawn-JSON change was needed. The **M4 launchers**
(`launch_m4_locallogtail.go` for claude-code, `launch_m4_antigravity.go`)
inline their own derivation rather than calling `DeriveWorkdir`, so they
were threaded through `teamWorkRoot` explicitly — without that, the
primary engine (claude-code) would have missed the segment.
`ensureTeamWorkRoot` creates `~/hub-work/<team>` 0o700 before the full
workdir. **Deferred:** the per-team-OS-user spawn (true cross-team
isolation under one shared uid) and its host-runner capability check
need an on-host spawn mechanism (sudo/setuid + user provisioning) that
can't be validated on the dev box; the natural seam is running each
team's single-team host-runner under a per-team OS user. This is the
hardening follow-up named in the residual-risk paragraph above.

### D7 — Cross-cutting sweep (G6)

Before Accepted, grep every storage path that bypasses the
team-scoped query layer (A2A relay attribution in `handlers_a2a.go`,
blob paths under `<dataRoot>/blobs/…`, any hub-wide `SELECT` without
`team_id`) and either team-scope it or document why it is safe. This is
verify-don't-guess, not a fixed list.

## Consequences

**Easier.** Onboarding a tester becomes "provision a team, hand over
`(team_id, owner_token)`." Isolation is enforced at one chokepoint
(D1) instead of trusted per-handler. Audit and admin gain a clear
operator identity.

**Harder.** Two human roles to reason about (operator vs principal);
the bootstrap mints an operator, so existing runbooks that assume "the
owner token is root" change. Mobile gains an operator-vs-owner
distinction (the Admin pane must be operator-gated).

**Now forbidden.** A per-team `owner` cannot touch `/v1/admin/*` or
another team's data. An agent token cannot be used to reach a team
other than its own. The shared on-disk template dir and the team-blind
workdir are removed.

**Migration.** Existing `default` installs: the single bootstrap owner
token is reclassified as the **operator** (a one-row `kind` update, or
a documented re-mint), so it keeps full access. New installs mint an
operator at init. `hub-work/` paths change shape; in-flight agents keep
their already-resolved workdir (`agents.worktree_path`), so only new
spawns adopt the team segment. The per-team OS-user guard (D6) requires
the host to be able to create/assume those users — a host-runner
capability check at W5.

## Resolved decisions (2026-05-31)

The four open questions were decided in discussion:

1. **Operator mechanism (D2) → new `operator` token kind.** A distinct
   kind is the cleanest, most auditable model and makes the D1 gate
   unambiguous; the cost (F-01 allowlist + `requireOperator` sites) is
   accepted.
2. **Host model (D6) → shared host + workdir guard.** Testers share a
   box; the workdir team segment is mandatory and the guard is per-team
   OS-level isolation (users/permissions), with the shared-kernel
   residual risk documented as a hardening follow-up.
3. **`default` (D4) → operator's home only.** No separate `default`
   owner; the bootstrap token becomes the operator. The demo seed is
   reworked accordingly.
4. **Templates (D5) → global built-ins + per-team overrides.** Embedded
   built-ins stay global read-only; on-disk overrides go under
   `teams/<team_id>/`.

## References

- Discussion: [multi-team-isolation.md](../discussions/multi-team-isolation.md)
- Plan: [multi-team-isolation-rollout.md](../plans/multi-team-isolation-rollout.md)
- Supersedes the single-team reading of
  [005](005-owner-authority-model.md) (owner == director == operator).
- Related: [016](016-subagent-scope-manifest.md) (operation-scope
  manifest), the F-05/F-06 token-scope items in
  [security-audit.md](../discussions/security-audit.md) (this
  generalises token-scope enforcement to the team axis).
- Code: `internal/auth/token.go:106-152` (middleware, F-01),
  `handlers_tokens.go:49,116-141` (requireOwner, issuance),
  `handlers_admin_audit.go` (cross-team admin), `init.go:21,30,43,60`
  (bootstrap, shared template dir), `spec.go:132-159` (DeriveWorkdir).
