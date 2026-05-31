---
name: Multi-team isolation
description: Gap analysis and remediation plan for promoting multi-team (per-team) isolation from post-MVP to MVP, driven by onboarding real external testers who each get their own team_id and must not see or collide with each other. Catalogs the current state (verified against code) — the schema is fully team-scoped and every data query already filters WHERE team_id = the URL path team, but nothing authorizes that path team against the caller's token, the owner credential is a hub-wide root with no team binding, there is no team-provisioning endpoint (only the seeded `default` team exists), the on-disk template directory is shared hub-wide, and the agent workdir derivation carries no team segment so two teams sharing a host collide on disk. Defines the isolation invariant, lays out a wedge sequence (auth path-team gate → hub-superadmin / team-owner role split → team provisioning → per-team templates → team-scoped workdir → cross-cutting sweep), and surfaces the central decision: split the conflated `owner` credential into a hub operator vs a per-team principal. Spawns an auth-model ADR because the changes are realized in code (middleware, schema, host-runner).
---

# Multi-team isolation

> **Type:** discussion
> **Status:** Open (2026-05-31) — **promoted from post-MVP to MVP.**
> External testers are about to be onboarded; each is assigned a
> `team_id` and different teams MUST isolate (no cross-team read,
> write, control, or on-host collision). Feeds an auth-model ADR.
> **Audience:** contributors
> **Last verified vs code:** v1.0.754

**TL;DR.** termipod's data model is multi-team from migration 0001 —
`teams` exists and `hosts` / `agents` / `projects` / everything
downstream carry `team_id`, and every data query already filters
`WHERE team_id = ?` using the team taken from the `/v1/teams/{team}/…`
URL path. **So the read/write surface already isolates by team — on
paper.** What's missing is everything that would make that boundary
*trustworthy*: nothing checks that the caller's token is allowed to
address the team named in the path; the `owner` credential is a
hub-wide root with no team binding; there is no way to provision a new
team or its first credential (only the seeded `default` team exists);
the on-disk template directory is shared across teams; and the agent
workdir derivation has no team segment, so two teams sharing a host
write into the same `~/hub-work/…` folder. This doc catalogs the gaps,
states the isolation invariant, and proposes a wedge sequence. The
load-bearing decision is splitting the conflated `owner` token into a
**hub operator** vs a **per-team principal**.

---

## 1. Why now

Isolation was deliberately deferred — `handlers_agent_families.go:15`
says it plainly: *"Multi-team isolation is post-MVP. The path
component is team-scoped … but storage is hub-wide; the directory is
shared across teams."* That was fine while the only team was
`default` (`init.go:21`).

The driver changed: real external testers are coming, each gets their
own `team_id`, and the explicit requirement is that **different team
ids isolate**. A tester in team `acme` must not be able to read,
mutate, or control anything in team `globex`, and their agents must
not collide on a shared host. That promotes this work to MVP.

## 2. Current state (verified vs code)

What already works in our favour:

- **Schema is fully team-scoped.** `teams` table; `hosts.team_id`,
  `agents.team_id`, `projects.team_id`, … all
  `REFERENCES teams(id) ON DELETE CASCADE`
  (`migrations/0001_initial.up.sql:5,25,37,60+`).
- **Data queries filter by the path team.** List/get/update handlers
  scope on the URL team, e.g.
  `FROM agents WHERE team_id = ?` (`handlers_agents.go:127`),
  `… WHERE team_id = ? AND id = ?` (`:197`, `:288`, `:519`, `:539`,
  `:1226`). The team comes from `chi.URLParam(r, "team")`.
- **Tokens carry a team in their scope.** `auth_tokens(kind,
  scope_json, …)` (`0001_initial.up.sql:11-20`); the owner is minted
  with `{"team":"default","role":"principal"}` (`init.go:60`); issued
  user/host tokens get `{"team": <path team>, "role": …}`
  (`handlers_tokens.go:137`).

So the moment the caller's token-team is bound to the path team, the
existing queries isolate. **The gap is the authorization edge, not the
data edge.**

## 3. The gaps

### G1 — The path team is never authorized against the token *(critical)*

`auth.Middleware` validates the token and the bearer-kind allowlist
only; it does **not** compare the token's `scope_json.team` to the
`{team}` path segment (`internal/auth/token.go:106-152`). Any valid
`owner` / `user` / `host` token can address **any** team by changing
the URL. Every per-team query (§2) is correct but reachable by the
wrong principal. This is the single load-bearing hole — closing it is
what actually turns the existing team-scoped queries into isolation.

Agent calls need the same treatment: an agent's MCP token carries its
team (`handlers_agents.go:1323`), and the in-process authority
dispatch forwards it through the REST routes
(`auth.WithInProcessDispatch`). The dispatch's effective team must be
pinned to the agent token's team, not a caller-supplied path.

### G2 — `owner` is a hub-wide root with no team binding *(critical)*

`requireOwner` checks only `tok.Kind == "owner"` — no team
(`handlers_tokens.go:49`). The owner-gated `/v1/admin/*` endpoints are
deliberately **cross-team**: fleet shutdown/update/restart, kill any
agent, rotate tokens, and `GET /v1/admin/audit` which *"drops the team
filter"* (`handlers_admin_audit.go:10-22`); `handleListTokens` reads
every token with no team filter (`handlers_tokens.go:67+`). Under
single-team this is just "the operator." Under multi-team it is a
privilege-escalation surface: a tester's owner token would shut down
the fleet and read other teams' audit. **`owner` conflates two roles
that must split** — a hub operator vs a per-team principal.

### G3 — No team provisioning / onboarding path *(blocker for the stated goal)*

The only team ever created is `default`, via the internal `ensureTeam`
called at init and demo-seed (`init.go:43`,
`seed_demo_lifecycle.go:220`). There is **no** API to create a team or
mint its first credential, and token issuance is owner-gated under
`/v1/teams/{team}/tokens` (`handlers_tokens.go:116-117`) — a
chicken-and-egg for a brand-new team. "Assign a team id to each
tester" has no mechanism today.

### G4 — On-disk templates are shared hub-wide *(leak)*

Template files live at `<dataRoot>/team/templates/{agents,prompts,
policies,projects}` — `team` is a literal directory name, not
`<team_id>` (`init.go:30`); `handlers_agent_families.go:16-17`
confirms storage is hub-wide. DB `project_templates` rows *are*
`team_id`-keyed (`UNIQUE(team_id,name)`, seeded with `defaultTeamID` —
`init.go:221-224`), but user-authored on-disk overrides and the
agent-families directory are global, so one team's edits are visible
to all.

### G5 — Workdir derivation is team-blind → on-host collision *(corruption risk)*

`DeriveWorkdir` resolves to `~/hub-work/<pid[:8]>/<handle>`
(project-bound) or `~/hub-work/_team/<handle>` (project-less); no
`team_id` appears in the path (`internal/hostrunner/spec.go:132-159`).
Two teams sharing a host, with the same 8-char project-id prefix +
handle (or `_team` + handle), land in the **same directory** — one
agent's working tree clobbers another's. `_team` is a project-less
namespace, not the team id.

### G6 — Cross-cutting sweep *(verify, don't assume)*

Anything that reaches storage outside the `/v1/teams/{team}/` query
path needs a team check once G1/G2 land: the A2A relay attribution
(`handlers_a2a.go`), blob paths under `<dataRoot>/blobs/…`, and any
hub-wide `SELECT` that omits `team_id`. Treat as a grep-and-verify
pass, not a guessed list.

## 4. The isolation invariant (target end state)

> For any request authenticated as team T, the hub serves only rows,
> files, and host resources owned by T; and no two teams share a
> mutable on-host path. The sole exception is an explicit **hub
> operator** credential, whose cross-team reach is named, audited, and
> never granted to a tester.

## 5. Proposed remediation (wedge sequence)

Ordered by "isolation per unit of risk." W1+W2 are the security core;
W3 unblocks onboarding; W4/W5 close the on-host leaks; W6 is cleanup.

- **W1 — Path-team auth gate.** Bind `scope_json.team` to the
  `{team}` path segment; reject mismatch with 403. Pin the in-process
  agent dispatch to the agent token's team (G1). Back-compat: the
  existing `default` owner keeps working because its scope team is
  `default`. *This is the wedge that makes isolation real.*
- **W2 — Split `owner` into operator vs principal.** Decide the
  mechanism (new `operator` token kind, or a reserved
  `scope.team == "*"` / role) so `/v1/admin/*` and cross-team views
  require the operator, while a per-team `owner` is principal of one
  team only (G2). Re-gate `requireOwner` call sites accordingly.
- **W3 — Team provisioning.** An operator-gated endpoint (and/or
  hub CLI) to create a team and mint its first `owner` token, so a
  tester can be handed `(team_id, owner_token)` (G3).
- **W4 — Per-team templates.** Either move the on-disk template root
  to `team/<team_id>/templates/…`, or keep shared built-ins and scope
  only user-authored overrides by `team_id`. Decide in the ADR (G4).
- **W5 — Team-scoped workdir.** Add a team segment to `DeriveWorkdir`
  (e.g. `~/hub-work/<team>/<pid|_team>/<handle>`), or formally require
  one host per team. Decide against the host-sharing model (G5).
- **W6 — Sweep.** Grep every storage path that bypasses the
  team-scoped query path and add the team check (G6).

Because these are realized in code (middleware, a likely token-kind
migration, host-runner workdir, provisioning routes), they warrant an
**ADR — "Multi-team isolation and the operator/principal split"** —
per this repo's rule that an ADR records a decision *in code*, not a
principle. This discussion is its backing.

## 6. Open questions / decisions needed

1. **Operator vs principal split (W2).** New token kind, or a scope
   flag on `owner`? A new kind is the cleanest mental model but touches
   the F-01 bearer allowlist (`token.go:124-147`) and every
   `requireOwner` site. → drives the ADR.
2. **Host-sharing model (W5).** Do testers share hosts (then the
   workdir needs a team segment *and* policy must stop one team's
   agent from `cd`-ing into another's tree), or is a host pinned to one
   team (then `hosts.team_id` + a spawn-time check suffices)? Cheaper
   isolation if hosts are per-team.
3. **`default` migration.** Keep `default` as a real team, or treat it
   as the operator's home? Existing installs and the demo seed depend
   on it.
4. **Template inheritance (W4).** Should built-in templates stay
   global (read-only) with per-team overrides layered on top, or be
   copied per team at provisioning? Affects how new engines/prompts
   ship.
5. **Quotas.** Out of scope for isolation, but onboarding real testers
   raises per-team budget/host caps — note for a follow-up, don't
   scope-creep this doc.

## 7. Sources (code)

- `hub/migrations/0001_initial.up.sql:5,11-20,25,37,60+` — teams,
  auth_tokens, team_id FKs.
- `hub/internal/auth/token.go:106-152` — middleware (no path-team
  check); F-01 bearer allowlist.
- `hub/internal/server/handlers_tokens.go:49,116-141` — `requireOwner`,
  team-scoped token issuance.
- `hub/internal/server/handlers_admin_audit.go:10-22` — cross-team
  owner audit ("team filter is dropped").
- `hub/internal/server/init.go:21,30,43,60,221-224` — default team,
  shared template dir, owner-token scope, team_id-keyed project
  templates.
- `hub/internal/server/handlers_agent_families.go:15-17` — "Multi-team
  isolation is post-MVP … directory is shared across teams."
- `hub/internal/server/handlers_agents.go:127,197,1323` — team-scoped
  queries; agent MCP token carries team.
- `hub/internal/hostrunner/spec.go:132-159` — `DeriveWorkdir` (no team
  segment).
- `lib/screens/hub/hub_bootstrap_screen.dart:45,295-296` — client
  Team ID field, defaults to `default`.

## 8. Cross-references

- [`information-architecture.md`](../spine/information-architecture.md)
  — role ontology (principal / operator).
- [`permission-model.md`](../reference/permission-model.md) — auth
  surfaces and bearer kinds.
- [`security-audit.md`](security-audit.md) — F-0x token-scope items
  (this generalises F-05/F-06 token-scope enforcement to the team
  axis).
