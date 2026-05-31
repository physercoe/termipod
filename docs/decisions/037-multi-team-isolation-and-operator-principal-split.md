# 037. Multi-team isolation and the operator/principal split

> **Type:** decision
> **Status:** Proposed (2026-05-31) — recommendations below are
> provisional pending the four open questions in §Open questions;
> promotes to Accepted once those are resolved and W1+W2 land.
> **Audience:** contributors
> **Last verified vs code:** v1.0.754

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

### D2 — Split `owner` into operator and principal (G2)

> **PROPOSED mechanism (Open Q1):** a new token **kind** `operator`,
> distinct from the per-team `owner`. Alternative under discussion: a
> scope flag on `owner` (`scope.team == "*"`). The decision shapes the
> F-01 allowlist and every `requireOwner` site.

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

### D4 — Bootstrap and the `default` team (Open Q3)

> **PROPOSED:** `init` mints **one operator** (the hub root, replacing
> today's single owner) and keeps seeding the `default` team. Whether
> `default` also gets a seeded `owner` at init, or is treated purely as
> the operator's home, is **Open Q3**. Existing installs and the demo
> seed (`seed_demo_lifecycle.go:220`) depend on `default` existing.

### D5 — Per-team template overrides (G4, Open Q4)

> **PROPOSED:** built-in templates stay **global and read-only** (they
> ship embedded in the binary via `//go:embed`); only user-authored
> on-disk overrides move under `<dataRoot>/teams/<team_id>/templates/…`.
> The resolver checks the team's override dir first, then the embedded
> built-ins. DB `project_templates` are already `team_id`-keyed
> (`init.go:221-224`). Alternative under discussion: copy built-ins
> per-team at provisioning (Open Q4).

### D6 — Team-scoped workdir (G5, Open Q2)

> **PROPOSED:** thread `team_id` into `DeriveWorkdir` and prefix the
> derived path with the team —
> `~/hub-work/<team_id>/<pid8>/<handle>` and
> `~/hub-work/<team_id>/_team/<handle>`. Whether a host is **shared**
> across teams (then this segment is mandatory *and* a policy guard
> must stop an agent `cd`-ing outside its team subtree) or **pinned to
> one team** (`hosts.team_id` + a spawn-time check, making the segment
> belt-and-suspenders) is **Open Q2**.

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
becomes either the operator or the `default` principal (Open Q3) — the
chosen path must keep the current owner token working or document the
re-mint. `hub-work/` paths change shape; in-flight agents keep their
already-resolved workdir (the value is persisted on the agent row,
`agents.worktree_path`), so only new spawns adopt the team segment.

## Open questions (for discussion — block Accepted)

1. **Operator mechanism (D2).** New `operator` token *kind* (clean
   mental model, auditable, but touches the F-01 allowlist and every
   `requireOwner` site) vs a scope flag on `owner` (`team == "*"`)
   (fewer code changes, but overloads one kind for two roles).
   *Recommendation: new kind.*
2. **Host-sharing model (D6).** Testers **share** a host (workdir team
   segment is mandatory + needs a policy guard against cross-team `cd`)
   vs each team gets a **pinned** host (`hosts.team_id` + spawn-time
   check; cheaper, stronger isolation, but needs enough hosts).
   *Recommendation: depends on tester logistics — see question.*
3. **`default` after the split (D4).** Keep `default` as a real seeded
   team with its own owner (back-compat, demo seed keeps working) vs
   treat `default` as the operator's home only. *Recommendation: keep
   it a real team.*
4. **Template inheritance (D5).** Global read-only built-ins + per-team
   on-disk overrides (no duplication, new engines ship once) vs copy
   built-ins per team at provisioning (full per-team control, but drift
   between teams). *Recommendation: global built-ins + overrides.*

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
