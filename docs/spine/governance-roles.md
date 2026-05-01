# Governance roles

> **Type:** axiom
> **Status:** Current (2026-05-01)
> **Audience:** contributors · template authors · prompt authors
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Termipod has five role concepts that recur across the codebase, the docs, and the prompts: **principal**, **director**, **steward**, **worker**, **operator**. They are not interchangeable. Confusing them produces drift in UI labels, prompt language, and policy gates. This file is the canonical ontology — what each role *is*, what authority it holds, and where it shows up. Read this before authoring a prompt, naming a UI surface, or wiring a permission decision.

---

## The five roles

| Role | Identity | Authority | Lifecycle |
|---|---|---|---|
| **Principal** | A human. The owner of the team and its data. There is exactly one principal per team in MVP. | Ultimate. The principal can override any policy, archive any agent, edit any template that's not frozen. | Permanent (the team is theirs). |
| **Director** | The principal *in the act of directing the system*. Same human as the principal; different framing. | Same as principal. The "director" framing shows up wherever the UI is helping the human shape the system's work — approve a plan, ratify a result, decide between options. | Per-interaction; the principal is *always* available, but they're the *director* in the moments when they're directing. |
| **Steward** | An agent. CEO-class operator that runs the system on the principal's behalf. Two tiers (general + domain) per [ADR-017](../decisions/017-layered-stewards.md). | Broad — manager scope. Authors templates and schedules, spawns workers, drives plans, surfaces decisions to the director. *Does not* do IC work (manager/IC invariant). | Persistent (general) or project-scoped (domain). |
| **Worker** | An agent. IC-class executor. | Bounded — IC scope (per-template `tool_allowlist` + ADR-016 worker role). Reads, writes, runs experiments, produces artifacts. *Does not* spawn peers or author templates. | Project-scoped or task-scoped; archives at completion. |
| **Operator** | A role label, **not a termipod-managed identity**. Anyone hands-on at a host shell, the hub binary, or an engine's terminal. Could be the principal, could be a contractor, could be the cloud provider's support tech. | Whatever the hosting environment grants (root on the VPS, etc.). Termipod does not enumerate or gate this. | Whenever someone is at the keyboard. |

---

## Why these distinctions matter

### Principal vs. Director: same person, different frame

The product is *for* the principal — the person who owns the data and bears the cost. The product *talks to* the director — the principal-in-the-act-of-directing.

This shows up in copy:

- **Principal-framed.** "Your data lives on your hosts." "You own this team." Settings screens, account setup, security boundaries.
- **Director-framed.** "Approve this plan?" "Ratify the briefing." "Pick a workdir." Action-taking surfaces.

A reviewer looking at a screen should be able to answer: is this surface telling the user about their *ownership* or asking them to *direct*? The answer shapes the copy. Mixing the framings ("Approve your plan?" vs. "You approved this plan") is fine; using the wrong framing for the surface ("Your team owns this approval queue") is not.

### Steward vs. Worker: the manager/IC invariant

The steward is a manager. It authors, plans, orchestrates, surfaces. It does not write production code, run experiments, or produce IC artifacts. The worker is the IC. It writes, runs, produces, reports.

**Why the separation is structural, not stylistic.** The steward has broad scope (templates, schedules, projects, A2A relay). If the steward also did IC work, a misbehaving prompt or a poorly-scoped tool would let it both escalate (author templates) *and* execute (write code) — a confused-deputy class problem. Splitting manager and IC work across two role types means scope grows along one axis (manager: broad authority, narrow output) or the other (IC: narrow authority, broad output), never both.

The split is enforced by:

- [ADR-016](../decisions/016-subagent-scope-manifest.md) D2/D3 role middleware — the `roles.yaml` manifest pins which `hub://*` tools each role can call.
- [ADR-016](../decisions/016-subagent-scope-manifest.md) D7 self-modification guard — a steward cannot edit its own kind's template.
- Bundled prompts — every steward prompt names the manager/IC invariant explicitly.
- [ADR-017](../decisions/017-layered-stewards.md) D4 — the general steward inherits the invariant from D6 + D7 and from the prompt.

### Steward (general) vs. Steward (domain): two tiers, same role

Both are stewards in the role sense — both gate through the steward middleware. They differ in scope:

- **General steward** (`steward.general.v1`, handle `@steward`): team-scoped, persistent, frozen template. The director's concierge.
- **Domain steward** (`steward.<domain>.v1`, handle `<domain>-steward`): project-scoped, archives at project close, overlay-authored.

Code that wants "any steward" (the role-level question) calls `isStewardHandle()` *or* `isGeneralStewardHandle()`. Code that wants "the team's concierge specifically" calls only `isGeneralStewardHandle()`. See `lib/services/steward_handle.dart`.

### Operator: out of scope on purpose

Termipod does not model "operator" as a managed identity. The operator is whoever is at the host's shell or the hub's binary. They could be the principal (likely), a sysadmin (in a homelab), or a cloud provider's support team (rare). Termipod does not know, does not gate, does not audit operator actions outside its own surfaces.

**Why this is acceptable.** Termipod's threat model is the personal-tool frame ([discussions/positioning.md §1.5](../discussions/positioning.md)). The operator and the principal are usually the same person. Investing in operator-specific gating, auditing, or distinct identity is multi-tenant SaaS work — out of scope for MVP.

This is *not* the same as "termipod doesn't care about the operator." Operator concerns surface as how-tos (`how-to/install-host-runner.md`, `how-to/install-hub-server.md`) — they're the audience for those docs. They just aren't a *role* the system gates against.

---

## Mapping role to surface

### UI surfaces

| Surface | Role addressed | Example |
|---|---|---|
| Onboarding, settings, account | Principal | "Your team", "Your data lives on your hosts" |
| Approve / Ratify / Decide / Pick | Director | "Approve plan?", "Ratify the briefing" |
| Activity feed, audit log | Director (read) | "What did the steward do today?" |
| Steward chat (`@steward` card on Me) | Director ↔ Steward | "Director: ask the steward anything" |
| Project pages, plan viewer | Director | "Watch the project unfold" |
| Templates editor | Director | "Edit overlay templates" |
| Logs / SSH / tmux escape hatches | Operator | "Maintenance hatch reachable from inside" (IA A6) |

### Prompts

| Prompt audience | Role addressed | Tone |
|---|---|---|
| `steward.general.v1.md` | Steward (the agent itself) | "You are the team's concierge. You do not write code." |
| `steward.<domain>.v1.md` | Steward (the agent itself) | "You orchestrate this project's lifecycle." |
| `*-worker.v1.md` | Worker (the agent itself) | "You are an IC. You report up. You don't spawn peers." |
| Mobile copy | Director | "Approve plan?" |

### `roles.yaml`

The manifest at `hub/config/roles.yaml` enumerates which `hub://*` tools each *role* (steward, worker) can call ([ADR-016 D6](../decisions/016-subagent-scope-manifest.md)). The principal is not listed because the principal's authority comes through the bearer token, not the role gate — the principal's auth_token has `role: "principal"` in its scope and bypasses the role middleware.

---

## Adding a new role (post-MVP)

If a future tier is needed (auditor, reviewer, third-party-deputy), the steps are:

1. **Pick a role name.** Single-word noun, lowercase. Should be distinct from existing five.
2. **Define authority.** Which `hub://*` tools? Which agent kinds carry the role? What's the lifetime?
3. **Update `roles.yaml`.** Add the role's allowed-tool list ([ADR-016 D6](../decisions/016-subagent-scope-manifest.md)).
4. **Update predicates.** `lib/services/steward_handle.dart` (if a steward variant) or analogous identifier predicates.
5. **Update this file.** Add a row to the §The five roles table.
6. **Document the threat model.** Why this role and not principal authority + better tool scoping?

Adding a role is heavy work. Most "we need a new role" intuitions are actually scope adjustments to existing roles. Push back on the role-add framing first.

---

## Common pitfalls

**Calling the principal "the user."** Acceptable in marketing copy where audience is unclear; precise terms are better in design docs. "User" doesn't distinguish principal from director; the prompt or copy should pick.

**Calling a domain steward "the steward."** Ambiguous in a multi-project team. Use the handle (`research-steward`, `infra-steward`) or the qualified term ("the project's domain steward").

**Talking about "operator role" in product copy.** There is no operator role to gate against. If your text says "the operator," you mean either the principal (in some maintenance moment) or the host's sysadmin. Disambiguate.

**Saying "the agent" instead of "the steward" or "the worker."** "Agent" is correct when the role is irrelevant. When the role is *load-bearing* in the sentence ("the agent shouldn't author templates"), pick the right role term (worker shouldn't, steward should).

---

## References

- [ADR-005 — owner authority model](../decisions/005-owner-authority-model.md) — the principal/director authority design.
- [ADR-016 — subagent scope manifest](../decisions/016-subagent-scope-manifest.md) — role-gated `hub://*` tools.
- [ADR-017 — layered stewards](../decisions/017-layered-stewards.md) — general vs. domain steward.
- [Reference: steward templates](../reference/steward-templates.md) — what stewards can author.
- [Reference: permission model](../reference/permission-model.md) — tool-call gate, distinct from role gate.
- [Discussion: positioning §1.5](../discussions/positioning.md) — strategic frame; why operator isn't a managed role.
- Memory: `feedback_steward_executive_role.md`, `feedback_ux_principal_director.md`.
