# Hub MCP tool surface

> **Type:** reference
> **Status:** Current (2026-05-13)
> **Audience:** agent authors, steward template authors, integrators
> **Last verified vs code:** v1.0.556

**TL;DR.** Single canonical entry for "what MCP tools does the
hub expose, and what does each one do?" Every tool is a thin
adapter over a hub REST endpoint — the relay principle from
[`../spine/blueprint.md`](../spine/blueprint.md) §5.2: MCP never
bypasses the hub's authority, it's protocol translation in front
of the existing `/v1/...` surface. Role-gated per
[`../decisions/016-subagent-scope-manifest.md`](../decisions/016-subagent-scope-manifest.md);
the steward role can call everything, the worker role gets the
allow-list in [`../../hub/internal/server/roles.yaml`](../../hub/internal/server/roles.yaml).

Source of truth for tool definitions:
[`../../hub/internal/hubmcpserver/tools.go`](../../hub/internal/hubmcpserver/tools.go)
+ [`../../hub/internal/hubmcpserver/tools_templates.go`](../../hub/internal/hubmcpserver/tools_templates.go).
This doc lags one release behind tagging by design — re-grep
`Name:` in those files for an unconditional truth.

---

## 1. Discovery + dispatch

The hub speaks MCP over stdio (`hub-mcp` binary) and over HTTP
(reverse-tunnelled from host-runner). Both expose the same tool
catalog. Three protocol methods carry the surface:

| Method | Purpose |
|---|---|
| `initialize` | Handshake; hub returns server capabilities + tool count. |
| `tools/list` | Returns the tool catalog: name, description, JSON-Schema for `arguments`. |
| `tools/call` | Run one tool; payload is `{name, arguments}`. |

Each tool result is JSON — typically the raw REST response body,
sometimes a small wrapper like `{ok: true, ...}` for endpoints
that return 204 No Content. Errors surface as MCP protocol
errors with the REST status code in the message.

---

## 2. Authorization

Two enforcement layers stack:

1. **Bearer token** — every REST call the MCP layer makes carries
   the agent's bearer token (issued at spawn time, scoped to one
   `team_id`). The hub rejects calls that cross the team boundary
   regardless of role.
2. **Role manifest** — `roles.yaml` decides which tool names the
   caller's role may invoke. Roles map from `agent_kind`:
   - `steward.*` (any kind starting with `steward.`) → **steward**
     role; `allow_all: true`.
   - Anything else → **worker** role; allow-list in `roles.yaml`.
   - Engine-internal subagents (claude-code `Task`, codex
     app-server children, gemini-cli subagents) inherit the
     parent's role by construction and aren't separately gated.

Worker-callable tools follow these patterns by default
(`*.list`, `*.get`, `*.read`, `documents.*`, `runs.*`,
`channels.post_event`, …). Anything not matched falls closed.

A2A has an extra gate: workers may only `a2a.invoke` against
their parent steward (`mcp_authority_roles.go::authorizeA2ATarget`).

---

## 3. Tool catalog

Grouped by resource. Required `arguments` are bolded; optional
fields read straight from the JSON-Schema embedded in
`tools.go`.

### projects

| Tool | Purpose |
|---|---|
| `projects.list` | List projects. Optional `kind` filter (`goal` / `standing`). |
| `projects.create` | Create. **`name`**, **`kind`**, optional `goal`, `docs_root`, `config_yaml`, `parent_project_id`, `template_id`. |
| `projects.get` | Fetch one. **`project`**. |
| `projects.update` | Patch fields on one project. |

### plans + steps

| Tool | Purpose |
|---|---|
| `plans.list` | List plans. Optional `project` filter. |
| `plans.create` | Create. **`project_id`**, **`title`**; optional `goal`, `success_criteria`. |
| `plans.get` | Fetch one plan. **`plan`**. |
| `plans.steps.create` | Append a step. **`plan_id`**, **`title`**; optional `description`, `acceptance_criteria`. |
| `plans.steps.list` | List steps under a plan. **`plan_id`**. |
| `plans.steps.update` | Patch one step (status, body). **`plan_id`**, **`step_id`**. |

### runs + artifacts

| Tool | Purpose |
|---|---|
| `runs.list` | List runs. Optional `project_id` filter. |
| `runs.get` | Fetch one. **`run`**. |
| `runs.create` | Create. **`project_id`**; optional `agent_id`, `config_json`, `seed`, `parent_run_id`, `trackio_host_id`, `trackio_run_uri`. |
| `runs.attach_artifact` | Attach an artifact to an existing run. **`run`**, **`project_id`**, **`kind`**, **`name`**, **`uri`**; optional sha256/size/mime/producer/lineage. |
| `artifacts.list` | List artifacts under a project. |
| `artifacts.get` | Fetch one. **`artifact`**. |
| `artifacts.create` | Project-scope artifact (not tied to a run). Same shape as `runs.attach_artifact` minus `run`. |

### documents + reviews

| Tool | Purpose |
|---|---|
| `documents.list` | List docs under a project. **`project_id`**. |
| `documents.create` | Create. **`project_id`**, **`title`**, **`body_md`**; optional `kind`. |
| `reviews.list` | List reviews. **`project_id`**. |
| `reviews.create` | Submit a review against a doc/run/artifact. |

### agents + hosts

| Tool | Purpose |
|---|---|
| `agents.spawn` | Spawn a child agent. **`child_handle`**, **`kind`**, **`spawn_spec_yaml`**; optional `host_id`, `parent_agent_id`, `worktree_path`, `budget_cents`, `mode`. *Planned (v1.0.557, ADR-025):* `project_id:` in the spawn YAML binds the agent to a project, the hub persists it on the agent row, and atomically creates a `sessions` row for the new worker (D5). Project-scoped spawns will be gated to the project's steward as caller (D3); the general steward delegates rather than spawning directly. May return 202 + attention_id when policy gates the spawn. |
| `agents.list` | List agents. Optional `host_id`, `status` filters; `include_archived: true` to also show soft-deleted rows. *Planned (v1.0.557):* `project_id` filter — the lookup mobile uses to populate the project detail Agents tab. |
| `agents.get` | Fetch one. **`agent`**. Returns `spawn_spec_yaml` + `spawn_authority_json` when known. |
| `agents.terminate` | Mark status=`terminated`. Steward-only. Host-runner kills the underlying process on its next reconcile loop. |
| `hosts.list` | List host-runners. Returns id, name, status (online/stale/offline), capabilities, last_seen_at, ssh_hint. **The lookup table for hostname → host_id** needed by `agents.spawn`. |
| `hosts.get` | Fetch one host. **`host`**. |
| `hosts.update_ssh_hint` | Patch a host's non-secret SSH hints. **`host`**, **`ssh_hint`** (object). Hub rejects any payload containing password/private_key/passphrase/secret/token keys (data-ownership law §4). |

### channels + a2a

| Tool | Purpose |
|---|---|
| `channels.post_event` | Post an event into a channel. **`channel`**, **`kind`**, **`body`**. |
| `project_channels.create` | Create a project-scoped channel. **`project_id`**, **`name`**. |
| `team_channels.create` | Create a team-scoped channel (e.g. `#hub-meta`). **`name`**. |
| `a2a.invoke` | Send an A2A request to another agent. Workers may only target their parent steward. |

### tasks + schedules

| Tool | Purpose |
|---|---|
| `tasks.list` | List tasks under a project. **`project_id`**; optional `status`, `priority`, `sort`. |
| `tasks.create` | Create. **`project_id`**, **`title`**; optional `priority`, `plan_step_id`, `due_at`. |
| `tasks.get` | Fetch one. **`task`**. |
| `tasks.update` | Patch status/title/etc. **`task`**. |
| `schedules.list` | List schedules under a project. **`project_id`**. |
| `schedules.create` | Cron-style trigger. **`project_id`**, **`cron`**, **`payload`**. |
| `schedules.update` | Patch. **`schedule`**. |
| `schedules.delete` | Remove. **`schedule`**. |
| `schedules.run` | Fire one immediately, off-schedule. **`schedule`**. |

### templates

Templates live on disk under `<DataRoot>/team/templates/{agents,prompts,plans}/`.
Each kind has the same five verbs:

| Tool | Purpose |
|---|---|
| `templates.agent.{create,update,delete,list,get}` | Agent templates (`.yaml`). |
| `templates.prompt.{create,update,delete,list,get}` | Prompt templates (`.md`). |
| `templates.plan.{create,update,delete,list,get}` | Plan templates (`.yaml`). |

Steward-only — workers can `*.list` / `*.get` to inspect but not
mutate.

### misc

| Tool | Purpose |
|---|---|
| `policy.read` | Read the team's policy doc. |
| `audit.read` | Read audit events. Optional `agent_id`, `kind` filters. |
| `mobile.navigate` | Steward-facing intent dispatch — open a page in the mobile UI. |

---

## 4. Conventions across tools

**Path-style ids in arguments.** Tools that target one row take the
id under a short key (`project`, `plan`, `agent`, `host`,
`schedule`, `task`). Tools that need a project context use
`project_id`.

**JSON pass-through.** Most call sites return the REST body
verbatim. Successful no-content endpoints return `{ok: true, ...}`
so the agent always sees a non-empty result.

**Validation.** Required fields are checked before the REST call;
missing fields produce a protocol error with a `... is required`
message. Beyond that, the REST handler is authoritative — bad
enums, malformed YAML, conflicting state all surface as the hub's
own 4xx / 5xx response.

**Hot-reload of `roles.yaml`.** Owners can edit the manifest at
runtime from the mobile Hub config screen (v1.0.554+). The MCP
gate picks up changes on the next call; in-flight calls aren't
re-checked.

---

## 5. When to add a new tool

The relay principle says: **don't**, unless the REST endpoint
already exists. The MCP layer doesn't carry business logic; if
the operation isn't there in REST, it shouldn't be there in MCP
either — add it to REST first, document in `api-overview.md`,
*then* wrap in MCP.

Counter-pattern: tools that aggregate or compose. The hub policy
is "one MCP tool = one REST call." Composition is the agent's
job (it's an LLM; it can sequence). Tools that fan out to N
endpoints would hide complexity from the agent and break the
audit trail.

---

## 6. See also

- [`api-overview.md`](api-overview.md) — REST surface every MCP
  tool wraps.
- [`../decisions/016-subagent-scope-manifest.md`](../decisions/016-subagent-scope-manifest.md)
  — why roles are scope-not-budget.
- [`../decisions/002-mcp-consolidation.md`](../decisions/002-mcp-consolidation.md)
  — why MCP is one binary, not many.
- [`../spine/blueprint.md`](../spine/blueprint.md) §5.2 — the
  relay principle.
- [`../../hub/internal/server/roles.yaml`](../../hub/internal/server/roles.yaml)
  — operator-editable role manifest (what each role may call).
