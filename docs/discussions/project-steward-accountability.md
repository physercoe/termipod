# Project steward accountability — workers, project scope, director consent

> **Type:** discussion
> **Status:** Resolved (2026-05-13) → [decisions/025-project-steward-accountability.md](../decisions/025-project-steward-accountability.md)
> **Audience:** contributors thinking about agent scope, mobile UX, or the
> spawn pipeline
> **Last verified vs code:** v1.0.556

**TL;DR.** Walked from "worker spawned in the wrong mode + wrong cwd" all
the way to a load-bearing question: *who can spawn workers, and what
project do they belong to?* The current code lets the team-level
general steward (ADR-017) spawn workers directly into "the team," with
no project binding. That confuses accountability and breaks the
project-detail UX's already-built filter. This discussion enumerates
the alternatives, picks two-tier accountability with lazy
project-stewards and principal-confirmed spawns, and hands the
specifics off to ADR-025.

If you want the rules, skip to ADR-025. This doc is the *why*.

---

## 1. The trigger

A tester asked the general steward to spawn an `ml-worker.v1` for a
specific project. Three things went wrong simultaneously:

1. The worker launched in **M4 mode** instead of **M2** (`agents` table
   column was empty because the worker template never declared
   `driving_mode`).
2. The pane opened in **`$HOME`**, not the template's
   `default_workdir: ~/hub-work` — because M4's tmux launcher does
   not `cd <workdir>`, only M2 does. Side effect: claude-code prompted
   the tester to "trust this folder?" pointing at their entire home
   directory. Bigger side effect: no `.mcp.json` or `CLAUDE.md` was
   materialized either — both are produced by `launch_m2.go`. So the
   worker ran without MCP and without persona.
3. The mobile project-detail "Agents" tab filtered by
   `a['project_id']`. But the `agents` table has no `project_id`
   column. The worker existed; it was just invisible.

Bug 1 is a 6-line template fix. Bug 2 is a consequence of bug 1
(M4 has none of the file-materialization machinery). Bug 3 is
structural.

We could have shipped a templates-only fix for #1 and called it
done. We didn't — because #3 forced the architecture question:
**what does it mean for a worker to belong to a project?** And once
you ask that, you can't avoid asking *who* decides which project a
worker belongs to, and what the relationship is to the existing
steward layers from ADR-017.

---

## 2. What the system already half-believes

The mobile UI was built expecting project-scoped agents:

- `lib/screens/projects/project_detail_screen.dart:1098` —
  `final rows = all.where((a) => (a['project_id'] ?? '').toString() == projectId)`.
- `lib/screens/projects/spawn_agent_sheet.dart:55-60` — seeds
  `project_id: "$pid"` into the spawn YAML when the sheet was
  opened from a project context.

The hub does not finish the wiring:

- `agents` table (`migrations/0001_initial.up.sql`) — no
  `project_id` column.
- `spawnIn` struct (`handlers_agents.go:362`) — no `ProjectID`
  field; the YAML's `project_id:` line is unparsed text.
- `agentOut` (`handlers_agents.go:28`) — no `project_id` JSON
  field.

So mobile filters by something the backend never delivers; the
filter is unconditionally empty.

Meanwhile workers don't get sessions:

- `migrations/0026_sessions_create.up.sql:90` — the migration's
  backfill shim only inserts sessions for agents with
  `handle = 'steward'`. Workers got nothing.
- Mobile's Sessions screen joins by `current_agent_id`. Workers
  never show up because there's no session row pointing at them.

The data exists. The transcript of A2A back-and-forth between a
steward and a worker is fully captured in `agent_events` with
`producer='a2a'` on inbound messages and `producer='agent'` on the
worker's output. It just isn't reachable from any UI surface because
no `session_id` is stamped.

So the system *almost* believes workers are project-scoped, first-class,
observable entities. Three small pieces of plumbing were never
wired up. The fix is to finish them, but doing so forces us to
choose what we believe.

---

## 3. The first-principles question

Stripped to essentials: **what is the unit of accountability for a
worker?**

Two coherent answers, mutually exclusive:

- **Team.** Workers belong to the team. Any steward in the team
  may spawn them. The general steward sits at the same accountability
  layer as project stewards for the purpose of spawning. (This is
  what the current code does by accident.)
- **Project.** Workers belong to one project. Only the project's
  steward may spawn them. The general steward must delegate down
  to the project steward; it has no direct line to workers.

The first treats projects as a *tag* (a way to group artifacts after
the fact). The second treats projects as a *scope* (a boundary that
constrains who can do what).

Termipod's `discussions/agent-driven-mobile-ui.md` and ADR-017 both
lean hard on the "scope" framing. The director directs intent, the
general steward routes, the domain steward operates within its
project, workers execute within their assigned slice. If the team
steward can also spawn workers into any project, the scoping is
advisory rather than load-bearing — and the audit trail forks at
every worker ("which steward put you here?").

The two-tier accountability model wins on every first-principle
test:

1. **Single accountability chain.** Worker W has exactly one
   spawning operator (project steward S₁), which has exactly one
   spawning operator (the director, via project create). No
   forks.
2. **Scope matches authority.** A worker can read/write only the
   project it belongs to; a project steward sees only its
   project; a general steward sees across projects but writes at
   team scope. Reading and writing don't have to share a level.
3. **Locality of context.** A worker spawned by its project's
   steward inherits project context for free. A worker spawned
   by the general steward would have to be told its project
   manually — a manual step that humans forget.
4. **Discoverability.** "What's running in project X?" becomes a
   single query: `agents.list?project_id=X`. No second hop to
   reconcile "spawned by general but tagged X."
5. **Least privilege.** Team-wide spawn authority is a foot-gun:
   a misconfigured prompt to the general steward could in principle
   spawn 50 workers across 5 projects. Bounding the spawn authority
   to the project's steward bounds the blast radius.

---

## 4. Industry-grade analogues, briefly

To avoid inventing a new vocabulary when one exists:

- **Kubernetes.** Cluster admin doesn't run pods. They create
  namespaces and let namespace admins run pods within. Cross-namespace
  operations require explicit RBAC.
- **GitHub orgs.** Org owners don't open PRs in repos; repo
  maintainers do. Cross-repo work goes through PRs + reviews.
- **Real research lab.** PI sets direction (= team / general
  steward). Postdocs run experiments (= project stewards). Grad
  students execute tasks (= workers). PI doesn't pipette.
- **Engineering org.** VP Eng enables; tech leads decide; ICs
  execute. VP Eng doesn't merge code.

The shape is identical across four orthogonal domains: **two
layers of accountability — enablers above, operators below — with
the IC tier below them both.** When we keep that pattern, the
system behaves like environments humans already know.

---

## 5. The lifecycle question

Once "every project has a steward" is a candidate rule, the
question is **when**. Three options:

- **Eager at create.** `projects.create` atomically spawns the
  project steward in the same transaction. Pro: hard invariant.
  Con: requires a live host at project-create time; breaks
  seed-demo, which seeds projects without a host.
- **Lazy at engagement.** Project rows exist without stewards
  until the director (or another steward, via delegation)
  engages with the project. First engagement auto-creates
  the steward. Pro: data and runtime are separated cleanly.
  Con: invariant is soft — projects can exist in
  "stewardless" state, which has to be a valid UI surface.
- **Hybrid.** Eager when the create flow knows a host is
  available; lazy otherwise. Pro: tries to give both UX
  predictability and seed-demo compatibility. Con: two paths
  for one concept; the more complex outcome.

Lazy wins for first-principle reasons. Projects are *data*
(plans, tasks, artifacts, the intention they encode); stewards
are *runtime* (a process on a host with a model attached).
Conflating data lifetime with runtime lifetime is exactly the
mistake the project_layered_stewards work was undoing for the
general steward (frozen template + ensure-spawn). The same logic
applies here: a project plan can exist before anyone's assigned
to it; the assignment happens when work starts. Lazy
materialization is the honest model.

Bonus: lazy makes the seed-demo case trivial. Seeded projects
display an empty state with a "spawn project steward" CTA. The
director taps it when they're ready. No special "seeded but not
real" state in the schema.

---

## 6. The consent question

If a project steward materializes on first engagement, **does the
director consent or does it happen silently?**

The temptation is silence — "the director already said they want
to work on this project; spawning the steward is implied." We
rejected that temptation:

- **Resource commitment.** A steward is a long-lived process
  with an engine license attached. It is not a transient cache
  entry. The principal-as-resource-allocator pattern says: each
  meaningful commit gets a click.
- **Asymmetry of cost.** "Don't ask, just spawn" is cheap for
  Anthropic's local Claude Code (one process, one model, one
  conversation). Termipod can run N stewards on M hosts; the
  cost of accidentally spawning the wrong one is "you have to
  archive it and try again" — small but nonzero.
- **Director scope guardrails (see §7).** The whole point of
  letting the director direct rather than operate is to keep the
  consent surface explicit. Silent auto-spawn is the opposite of
  explicit.
- **Industry analogue.** VS Code asks "reopen in container?";
  Docker Desktop asks "run this image?"; Kubernetes asks "apply
  this manifest?" Each click is one tap, but the tap is
  intentional. Spotify-style "just play it" is appropriate for
  content; not for spawning long-lived operators.

Consent flow varies by who initiates:

- **Director taps into the project's steward overlay.** Show a
  bottom sheet with host/model/permission picker. Even when only
  one host exists, the sheet appears — same mental model for 1
  host and N hosts. Director taps "Spawn"; spawn happens; overlay
  populates.
- **General steward needs a project steward to honor a delegation.**
  General steward raises an `attention_item` ("Project Alpha
  needs a steward to honor your spawn-worker request. Approve?").
  Director approves → same picker sheet opens, prefilled with
  the general steward's best guess. Reason for the
  attention-item-not-modal: delegation can happen when the
  director isn't looking at the screen; the attention surface
  persists.

Both paths land in the same picker sheet. Same mental model.

Note: even with one host, picking the host is a choice, because
**the worker the steward spawns will run on the same host**. The
director's decision about "which host should this project run on"
is consequential beyond the steward itself.

---

## 7. The director's scope

A side question that surfaced: **what is the director allowed /
expected / forbidden to do directly?** This is the principal-vs-operator
boundary in concrete terms.

| Category | Operations | Notes |
|---|---|---|
| **MUST do directly** | Sign in · Configure hub · Install host-runner · Edit roles.yaml / templates / policy · Create projects · Read everything · Reset/replace/fork stewards · Approve attention items | Stewards can't bootstrap themselves; configuration is out-of-band. |
| **MAY do, via the steward** | Spawn workers · Edit live agent config · Terminate agents · Run schedules · Create plans / tasks / docs · Open A2A conversations | Each of these has a direct mobile affordance today. The default flow should route through the steward; the direct affordance becomes an "advanced bypass." |
| **SHOULD NOT do, even though technically possible** | Spawn workers directly bypassing the steward · PATCH a live agent's mode/model to "fix" it · Send raw tmux/SSH commands as a substitute for an A2A request · Edit a project's policy mid-task to unblock a stuck worker | Each of these violates the principal/operator boundary. UI surfaces that enable them should be flagged or rerouted, not removed (escape valve still useful). |

The "SHOULD NOT" column is the load-bearing one. The director
retains root authority (config + escape-valve), but in the
normal operating loop the director sets intent and the steward
operates. The mobile UI today has buttons that let the director
operate directly; those buttons should reroute through the
steward by default (see ADR-025 D6).

---

## 8. What we considered and rejected

| Variant | Why rejected |
|---|---|
| **Workers are team-scoped (status quo).** | Project Agents tab filter unconditionally empty. Audit forks at every worker. Violates scope-matches-authority. |
| **Workers project-scoped, but team steward may spawn directly.** | Splits accountability layer. Both general and project stewards can spawn into a project — which one is responsible? Same fork problem as the team-scoped variant, with a coat of paint. |
| **Project optionally has a steward.** | Forces every reader (Activity, Sessions, MCP gate) to handle two cases. Two cases means three bugs. |
| **Multiple stewards per project (co-stewards).** | Two accountability roots for one project. If a project needs different specialties, the existing `parent_project_id` already lets you split. |
| **Eager-spawn the project steward at project create.** | Requires a live host at create-time; breaks seed-demo. Conflates data lifetime with runtime lifetime. |
| **Silent auto-spawn on first engagement.** | Silently committing engine licenses on the principal's behalf is a soft-power leak; subverts the "director consents" invariant. |
| **One-tap attention-item approval (steward's pick stands).** | Smuggles host/model/permission decisions into a yes/no click. Real consent requires seeing the choice. |

Each of these started as a candidate. None survive the
first-principle tests in §3 + §6.

---

## 9. Resolution

The set of decisions (full text in ADR-025):

- **Workers are project-scoped first-class agents.** They get a
  `project_id` on the `agents` row, a session at spawn, and visibility
  on the project's Agents tab.
- **Every project that's engaged with has exactly one steward.**
  Lazy materialization at first engagement; explicit `[Spawn]`
  consent from the director; default kind `steward.general.v1`.
- **Only the project steward may spawn workers in its project.**
  General steward delegates by sending a message into the project's
  steward (or raising an attention item asking the director to
  spawn the project steward first).
- **The general steward retains an emergency-stop authority.**
  It can `agents.terminate` any agent in the team, but cannot
  spawn workers.
- **Director consent on every steward spawn.** Same picker sheet
  for both engagement-initiated and delegation-initiated flows.
- **Mobile surface split.** Sessions screen lists steward sessions
  (team + project). Project Agents tab lists project steward +
  workers. Worker sessions appear only on the project page.

The wedge spec lives in ADR-025 §Decision. Implementation lands in
v1.0.561 (schema + lazy steward + worker session + visibility) +
v1.0.562 (role-gate enforcement + UI re-routing). (v1.0.557–v1.0.560
were claimed by successive steward-overlay IME hotfixes; v1.0.560
is the root-cause fix via explicit FocusScope per Flutter #28986.)

---

## References

- [ADR-017](../decisions/017-layered-stewards.md) — the two-tier
  steward design this discussion extends.
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — the role
  manifest the worker-project-binding lives within.
- [ADR-025](../decisions/025-project-steward-accountability.md) —
  the decision form of this discussion.
- [`spine/agent-lifecycle.md`](../spine/agent-lifecycle.md) — updated
  in this batch to reflect project-scoped workers.
- [`reference/hub-mcp.md`](../reference/hub-mcp.md) — updated to
  document `project_id` on `agents.spawn`.
