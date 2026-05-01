# Audit events — schema, action taxonomy, contract

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · auditors · template authors
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** `audit_events` is termipod's authoritative log of system-level actions — agent spawns, attention decisions, schedule edits, template changes, project mutations, etc. It's *separate from* `events` (which is the per-channel cross-agent message log; see [ADR-019](../decisions/019-channels-as-event-log.md)). Every consequential mutation in the hub emits a row via `recordAudit(ctx, team_id, action, target_kind, target_id, summary, meta_json)`. The mobile **Activity feed** (`lib/screens/activity/activity_screen.dart`) renders this stream chronologically. This file is the canonical action taxonomy + per-action `meta_json` shape + contributor contract.

---

## Schema

`hub/migrations/0003_audit_events.up.sql`:

```sql
CREATE TABLE audit_events (
    id             TEXT PRIMARY KEY,
    team_id        TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    ts             TEXT NOT NULL,
    actor_token_id TEXT,                    -- auth_tokens.id; NULL for system events
    actor_kind     TEXT NOT NULL DEFAULT 'system',  -- owner | user | agent | host | system
    actor_handle   TEXT,                    -- resolved from scope.handle / role
    action         TEXT NOT NULL,           -- canonical action string (taxonomy below)
    target_kind    TEXT,                    -- agent | attention | schedule | host | …
    target_id      TEXT,                    -- the target's primary key
    summary        TEXT NOT NULL,           -- one-line human-readable
    meta_json      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_team_ts ON audit_events(team_id, ts DESC);
CREATE INDEX idx_audit_team_action_ts ON audit_events(team_id, action, ts DESC);
```

Indexes serve the two primary read paths: chronological feed (`team_id` + `ts`) and filtered feed (`team_id` + `action` + `ts`).

---

## Action taxonomy

The action string is **dot-namespaced lowercase**. The first segment is the *target domain*; the second is the *verb*. New actions follow this pattern.

Current taxonomy as of v1.0.350-alpha:

### Agents
| Action | target_kind | When |
|---|---|---|
| `agent.spawn` | `agent` | New agent spawned (any kind: steward, worker, etc.) |
| `agent.archive` | `agent` | Agent archived (manual or auto on project close) |
| `agent.terminate` | `agent` | Agent killed (forced, vs. clean archive) |
| `agent_family.deleted` | `agent_family` | Agent kind family removed from the team's overlay |
| `agents.fanout` | `agents` | Steward fanned out a multi-worker spawn batch via MCP |

### Attention
| Action | target_kind | When |
|---|---|---|
| `attention.decide` | `attention` | Principal answered an attention item (`/decide`) |
| `select.request` | `attention` | Agent emitted `request_select` |
| `help.request` | `attention` | Agent emitted `request_help` |
| `permission_prompt.request` | `attention` | Engine routed a `permission_prompt` through the bridge |
| `permission_prompt.auto_allowed` | (none) | Hub auto-allowed a routine tool call (mode 3 path) |

### Sessions
| Action | target_kind | When |
|---|---|---|
| `session.open` | `session` | New session row created |
| `session.archive` | `session` | Session archived (close→archive rename per ADR-009) |
| `session.delete` | `session` | Session permanently deleted |
| `session.fork` | `session` | New session forked from existing one (ADR-014) |
| `session.rename` | `session` | Session title changed |
| `session.resume` | `session` | Session reused via engine `--resume <id>` (ADR-014) |

### Projects + plans
| Action | target_kind | When |
|---|---|---|
| `project.create` | `project` | New project row |
| `project.update` | `project` | Mutable project fields edited (goal, parameters_json, …) |
| `project.archive` | `project` | Project archived |
| `plan.create` | `plan` | Plan instantiated (from a template) |
| `plan.update` | `plan` | Plan status / metadata changed |
| `plan_step.update` | `plan_step` | Plan step transitioned (e.g. agent_driven → human_gated) |

### Runs + artifacts + documents + reviews
| Action | target_kind | When |
|---|---|---|
| `run.create` | `run` | Worker registered a new run |
| `run.complete` | `run` | Run terminated (success or failure) |
| `artifact.create` | `artifact` | Worker attached a new artifact |
| `document.create` | `document` | Worker or steward created a document |
| `review.request` | `review` | Document/artifact submitted for principal review |
| `review.decide` | `review` | Principal approved / rejected the review |

### Schedules
| Action | target_kind | When |
|---|---|---|
| `schedule.create` | `schedule` | New cron schedule |
| `schedule.delete` | `schedule` | Schedule removed |
| `schedule.run` | `schedule` | Schedule fired (instantiated a plan per [forbidden #11](../spine/blueprint.md#forbidden-patterns)) |

### Channels + templates + policy
| Action | target_kind | When |
|---|---|---|
| `channel.create` | `channel` | New channel (team-scope or project-scope) |
| `template.renamed` | `template` | Overlay template renamed |
| `template.deleted` | `template` | Overlay template removed |
| `policy.edit` | `policy` | Team policy file edited from mobile |

### Hosts + tokens
| Action | target_kind | When |
|---|---|---|
| `host.delete` | `host` | Host-runner registration removed |
| `token.issue` | `token` | New auth token minted |
| `token.revoke` | `token` | Auth token revoked |

---

## `meta_json` shape per action

`meta_json` is a free-form JSON object capturing action-specific context. Conventions:

- Keep keys snake_case.
- Reference foreign-keyed ids with `<thing>_id` keys (e.g. `parent_agent_id`, `project_id`).
- Free text goes in `summary`, not `meta_json`. `meta_json` is for *structured* context that programmatic consumers can read.
- Avoid embedding large payloads; if you need to carry a body, attach an artifact and reference its id.

Per-action `meta_json` keys (canonical):

| Action | Common keys |
|---|---|
| `agent.spawn` | `parent_agent_id`, `template_id`, `host_id`, `engine_kind` |
| `agent.archive` / `agent.terminate` | `reason` (free text), `final_status` |
| `attention.decide` | `attention_id`, `decision`, `reason`, `option_id` (for select) |
| `select.request` / `help.request` | `agent_id`, `severity`, `mode` (for help) |
| `permission_prompt.request` | `agent_id`, `tool_name`, `tool_input_keys` |
| `session.fork` | `source_session_id`, `new_agent_id` |
| `session.resume` | `engine_session_id` |
| `plan.create` | `template_id`, `parameters_json` |
| `plan_step.update` | `plan_id`, `step_index`, `from_status`, `to_status` |
| `run.create` / `run.complete` | `agent_id`, `project_id`, `metric_uri`, `exit_code` (complete) |
| `artifact.create` | `producer_agent_id`, `mime`, `sha256`, `lineage_json` |
| `document.create` | `producer_agent_id`, `kind`, `size_bytes` |
| `review.request` / `review.decide` | `target_kind` (document/artifact), `target_id`, `reviewer_id`, `decision` |
| `schedule.create` / `schedule.delete` | `cron`, `template_id` |
| `schedule.run` | `plan_id` (the plan instantiated) |
| `template.renamed` / `template.deleted` | `template_kind`, `old_name`, `new_name` |
| `policy.edit` | `path` (file path), `change_summary` |
| `host.delete` | `host_handle`, `reason` |
| `token.issue` / `token.revoke` | `token_role`, `scope_summary` |

Per-action invariants (non-exhaustive):

- `actor_kind = "agent"` requires `actor_handle` to resolve to a current or recently-archived agent's handle.
- `target_id` is required when `target_kind` is non-null.
- `attention.decide.meta_json.decision` is one of `"approve" | "reject"`.
- `plan_step.update.meta_json.from_status` and `.to_status` are valid plan-step statuses.

These invariants are not enforced by the schema (no JSON-schema check); they are conventions. Adding a CI lint that walks recent audit events and validates per-action shapes is on the post-MVP list.

---

## The `recordAudit` API

Server-side, every audit emit looks like:

```go
s.recordAudit(ctx, teamID,
    "agent.spawn",                       // action
    "agent",                             // target_kind
    spawn.AgentID,                       // target_id
    fmt.Sprintf("Spawned %s on %s", handle, host),  // summary
    map[string]any{                      // meta_json
        "parent_agent_id": parentID,
        "template_id":     tmpl,
        "host_id":         host,
        "engine_kind":     kind,
    },
)
```

`recordAudit` is on `*server.Server` (or its equivalent) and accesses the bound `*sql.DB`. It picks up `actor_*` from the request context (set by the auth middleware) automatically, so the call site only supplies the action-specific fields.

**Synchronous, in the same transaction as the mutation.** A failed audit record means the originating action *also* fails — atomic-with-the-write. This is intentional: we never want a state where the mutation happened but the log didn't.

---

## Contributor contract — when to emit

**Emit when:**

- A user-visible mutation lands on the hub (an agent spawns, a session forks, a schedule fires, a template gets edited).
- The principal will care later for review or debugging.
- The action is not already covered by an `events` row in a channel (events go in `events`, the channel log; system actions go here).

**Do *not* emit when:**

- The change is purely internal hub bookkeeping with no principal-visible meaning (e.g. cache refresh, periodic reconciliation polls).
- The action is already represented as an `agent_event` row (engine stream events live there, not here).
- You are tempted to "audit log everything" — chatty audit logs decay into noise. Aim for one row per consequential principal-visible action.

**Adding a new action:**

1. Pick a name in `domain.verb` form. If `domain` is new, the next entry in §Action taxonomy gets a new domain section.
2. Document the `meta_json` shape in this file's per-action table.
3. Wire the `recordAudit` call at the mutation site (synchronous with the write).
4. If your action belongs to a new `target_kind` not in §Schema's list, add the kind to the schema comment and update this file.

---

## Mobile rendering

Surface: `lib/screens/activity/activity_screen.dart` (Activity tab in the home screen) + `lib/screens/team/audit_screen.dart` (filterable team-scope audit feed).

**Activity feed (home tab).** Chronological list of audit events for the team, with action-specific iconography and per-row tap → `target_kind`-specific deep link (e.g. `agent.spawn` → agent detail).

**Team audit screen.** Same data, with:
- Filter by `actor_kind` (agent / system / user).
- Filter by `action` prefix (e.g. `attention.*` to see only attention-related rows).
- Date-range scrub.
- Search across `summary` (FTS5 not currently wired here; full-text on `summary` is a post-MVP wedge).

---

## What this is *not*

- Not a security audit log. The hub does not currently surface "who tried to call X and was denied" rows here. That information lives in HTTP access logs + the role middleware's denial responses, not in `audit_events`.
- Not the channel message log. That's `events` ([ADR-019](../decisions/019-channels-as-event-log.md)).
- Not engine output. That's `agent_events`.
- Not metrics. Those live in run-attached `metric_uri`s.

These are four separate primitives addressing four separate concerns. Keep them separate; resist the temptation to "just unify the logs."

---

## Open / post-MVP

- **CI lint for action shapes.** Validate per-action `meta_json` keys against this file's table. Today the contract is convention.
- **FTS5 over `summary`.** Mobile audit screen wants full-text search; trivial to add but not yet wired.
- **Retention policy.** Audit rows are forever today. Multi-year accumulation will eventually need a retention policy; not pressing for personal-tool MVP.
- **Per-action notification rules.** A future "notify me on `agent.terminate`" rule would key off this taxonomy.

---

## References

- Schema: `hub/migrations/0003_audit_events.up.sql`.
- Code: `hub/internal/server/handlers_*.go` — every site that calls `recordAudit`.
- [ADR-019](../decisions/019-channels-as-event-log.md) — the *other* event log; orthogonal concerns.
- [ADR-009](../decisions/009-agent-state-and-identity.md) — close→archive vocabulary that drives `session.archive` / `agent.archive`.
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — role gate (orthogonal — gate decisions don't currently land here).
- Memory: `project_activity_feed_foundation.md`, `project_artifacts_wedge.md`.
