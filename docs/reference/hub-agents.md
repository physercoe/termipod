# Termipod Hub — Agent Spawning

How to spawn an **agent** — a backend CLI process (claude-code, codex,
…) running in a tmux pane on a registered host. Covers the four paths
that land a row in the `agents` table: mobile, REST, MCP bridge, and
scheduler.

## 1. Preconditions

- A hub (`hub-server serve`) reachable from both the mobile / CLI
  caller and the host that will run the agent.
- At least one **host** registered — see `docs/install-host-runner.md`.
- A bearer token with a kind that permits `/agents/spawn`. Owner and
  user tokens are fine; agent- and host-kind tokens are not.
- The agent family (template `backend.kind`) must appear in the
  hub's family registry. The embedded defaults live in
  `hub/internal/agentfamilies/agent_families.yaml`; operators add or
  override entries on the fly via the API or the **Agent Families**
  screen in the mobile app. Either source feeds host-runner's probe
  (`bin`, `version_flag`, `supports`) and the spawn handler's
  mode×billing `incompatibilities` rules. Adding a new family is a
  hot edit, not a rebuild — see §3.5 below.

## 2. The spawn pipeline

Every spawn path ends up at `POST /v1/teams/{team}/agents/spawn`. The
handler then:

1. **Policy check.** If `spawn` is gated at tier `significant` or
   `critical` in `templates/policies/default.v1.yaml`, the server
   writes an `attention_items` row (`kind=approval_request`) and
   returns `202 pending_approval` + an `attention_id`. The real spawn
   happens only when quorum approves via
   `POST /v1/teams/{team}/attention/{id}/decide`.
2. **Render.** The YAML spec is expanded: `{{handle}}`, `{{principal}}`,
   `{{journal}}` (parent's markdown path) are substituted.
3. **Insert transactionally:**
   - an `agents` row with `status='pending'` + `pause_state='running'`,
   - an `agent_spawns` audit row capturing the parent/child edge +
     the rendered spec + authority blob.
4. **Poll.** The target `host-runner` sees the pending spawn on its
   next poll tick (≤3s), materializes a worktree if requested, opens
   a tmux pane, launches `backend.cmd`, and PATCHes the agent to
   `status='running'` with the pane id.

See `hub/internal/server/handlers_agents.go:222` (`handleSpawn`) and
`hub/internal/hostrunner/runner.go:170` (`tickPoll`).

## 3. Spawn spec YAML

Only a handful of keys are read by `host-runner`; extras are tolerated
for forward compat.

```yaml
# Required-ish for a working pane:
backend:
  # The hub binds {{model}} from this field so changing models is a
  # YAML edit, not a code change.
  model: claude-opus-4-7
  # Optional: declare the per-mode flag the hub should expand into
  # {{permission_flag}} when the spawn request sets permission_mode.
  # Adding a new mode is a YAML-only operation — Go has no fallback.
  permission_modes:
    skip:   "--dangerously-skip-permissions"
    # {{mcp_namespace}} expands to the hub's MCP server name
    # (`hub.MCPServerName`, currently "termipod"). Substitution is
    # fixed-point so this nested placeholder is resolved.
    prompt: "--permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt"
  cmd: "claude --model {{model}} --no-update {{permission_flag}}"

# Optional: bind marker forwarding to a channel so any
#   <<mcp:post_message {"text":"…"}>>
# the backend prints becomes an event in this channel.
project_id: "p_abcd"
channel_id: "c_xyz"

# Optional: create a git worktree before launch. The backend
# process will almost always cd into it.
worktree:
  repo:   "/home/me/code/termipod"
  branch: "agent/issue-142"   # created if absent
  base:   "main"              # starting point when created
```

`worktree_path` comes from the spawn request itself (not the YAML) so
the hub can pre-validate uniqueness.

Template expansion runs server-side. Useful placeholders:

- `{{handle}}` — child agent handle
- `{{principal}}` — the user / parent agent that initiated the spawn
- `{{journal}}` — relative path to the parent agent's markdown journal
- `{{model}}` — value of `backend.model` (empty if unset)
- `{{permission_flag}}` — `backend.permission_modes[<spawn.permission_mode>]`
  (empty if the mode isn't declared in the YAML)
- `{{mcp_namespace}}` — hub-controlled MCP server name (`hub.MCPServerName`).
  Use it inside a permission flag value like
  `mcp__{{mcp_namespace}}__permission_prompt` so the rendered command
  always tracks the hub binary's notion of the namespace.

## 3.5 Editing the agent family registry

The registry started life as a compile-time-only YAML and is now a hot-
editable surface so a new CLI (e.g. Kimi) can land without a hub rebuild.
Three layers cooperate:

1. **Embedded defaults** — `hub/internal/agentfamilies/agent_families.yaml`,
   shipped in the binary. Always present, read-only at runtime.
2. **Disk overrides** — `<DataRoot>/agent_families/<family>.yaml`. The
   loader scans this directory on every probe sweep and overlays files
   on top of embedded entries by `family` name. A file with a name not
   in embedded → adds a new family. A file with a matching name →
   replaces the embedded entry wholesale.
3. **API** — `GET/PUT/DELETE /v1/teams/{team}/agent-families[/<family>]`.
   PUT writes to the override directory and invalidates the in-memory
   cache so the next spawn-mode resolution sees the change instantly.

Each list entry is tagged with a `source` field:

- `embedded` — the compile-time default. The mobile editor opens
  these read-only with an "Override" affordance that scaffolds a
  starter override body.
- `override` — embedded family with an override file on disk.
- `custom` — operator-authored family with no embedded counterpart.

Validation on PUT is strict: unknown YAML keys are rejected, `supports`
must be a non-empty subset of `{M1, M2, M4}`, and `incompatibilities`
billing values must be one of `api_key` / `subscription` / unset.
The `family` in the body must match the URL path component.

DELETE on a `custom` family removes the file. DELETE on an
`override` reverts the family to its embedded default. DELETE on an
embedded-only entry returns 409 — there is no override file to remove.
Disabling an embedded family (e.g. hiding `aider`) is a follow-up;
today, write an override with a narrowed `supports` list.

Operationally, after an edit:

- Hub: changes are visible to spawn-mode resolution on the next request.
- Host-runner: pulls the merged registry on each probe sweep
  (`Runner.ProbeInterval`, default 15 min). On a hub outage, falls
  back to the embedded YAML so capabilities aren't blanked.

Audit rows (`agent_family.created` / `.updated` / `.deleted`) land
under the team's audit log.

## 4. Spawning from mobile

There are two spawn paths, matching the IA-redesign agent ontology
(`docs/information-architecture.md` §3): one team-scoped singleton (the steward)
and N project-scoped workers. Each has its own UI.

### 4a. Spawning the steward

The steward is the one agent that speaks for the team in `#hub-meta`.
Every team has exactly one, and it is spawned from the shipped
`agents/steward.v1` template — operators don't hand-roll handles or
principal/journal wiring.

Open the TermiPod app → **Projects** tab. Top of the AppBar shows a
steward chip:

- `🤖 Steward ready` (live colour) — tap to open the `#hub-meta`
  channel.
- `🤖 No steward` (muted) — tap to open the **Spawn the team
  steward** sheet.

The steward sheet is deliberately minimal: a host dropdown and a
**Spawn Steward** button. The handle (`steward`, reserved), the kind
(`claude-code`), and the spec YAML (from `agents/steward.v1`) are
fixed.

### 4b. Spawning a project agent

Project agents are scoped to a single project — the `project_id:`
key in the spawn spec YAML is what makes marker forwarding, `#channel`
routing, and the Agents pill all agree about ownership.

In the app: **Projects → tap a project → Agents pill →** bottom-right
**Spawn Agent** FAB.

The sheet pre-fills `project_id: "<id>"` into the YAML for you, then
shows:

1. **Preset chips** (device-local) — tap to prefill handle / kind /
   YAML. Long-press to delete. "Save preset" at the bottom captures
   the current form.
2. **Load template** — fetches `GET /v1/teams/{team}/templates/agents`,
   lists them, and pastes the selected YAML.
3. **Handle** — the child agent's handle (must be unique per team).
   The handle `steward` is reserved — use the steward chip flow above.
4. **Kind** — free-form backend identifier (`claude-code`, `codex`, …).
5. **Host** — dropdown of registered hosts. Defaults to the first
   `status=online` host (see *Status indicators* below about the
   staleness caveat).
6. **Spec YAML** — the YAML from §3 above.

Submit → one of two outcomes:

- `"Agent \"…\" spawned."` — the agent row is created; a pending
  row appears on the project's Agents pill within a couple of seconds
  and flips to running once the host picks it up.
- `"Spawn request sent — awaiting approval."` — policy-gated; an
  `approval_request` attention item is filed. Approvers (including
  you, if you're on the list) can approve from the Me tab (attention
  section); the real spawn then happens.

### Can I spawn from mobile today?

**Yes.** Both paths above are shipped. The steward chip landed with
the IA-redesign wedges; the project-agent FAB was relifted in
v1.0.221-alpha after the Agents sub-tab was retired in favour of the
project-detail Agents pill.

What you cannot do yet from mobile:

- Edit an existing agent's spawn spec (read-only in the detail sheet).
- Bulk-spawn multiple agents from one YAML.
- Approve-in-place on the spawn dialog — approvals live in the
  Me tab's attention section.

### 4c. Editing templates

The mobile **Templates** screen (Settings → Team → Templates) lists
every file under `<dataRoot>/team/templates/{agents,prompts,policies}/`
with full read/write/rename/delete from the device. Behind the
hood:

- `GET    /v1/teams/{team}/templates`               — list files
- `GET    /v1/teams/{team}/templates/{cat}/{name}`  — read body
- `PUT    /v1/teams/{team}/templates/{cat}/{name}`  — create/overwrite
- `DELETE /v1/teams/{team}/templates/{cat}/{name}`  — remove disk file
- `PATCH  /v1/teams/{team}/templates/{cat}/{name}`  — rename within
  the same category (`{"new_name": "..."}`); cross-category moves are
  rejected because the resolver paths differ.

Every mutation lands in the audit log as `template.created`,
`template.updated`, `template.deleted`, or `template.renamed`. The
bundled defaults stay in the embedded FS, so deleting a disk file
falls back to the built-in copy on the next read — there is no
"restore" endpoint because re-init is the same operation.

## 5. Spawning from the REST API

```bash
HUB=https://hub.example.com
TOK=your-owner-or-user-token

cat <<'YAML' > /tmp/spec.yaml
backend:
  cmd: "claude --model opus-4-7"
project_id: p_abcd
channel_id: c_xyz
YAML

curl -fsS -X POST \
  -H "Authorization: Bearer $TOK" \
  -H 'content-type: application/json' \
  "$HUB/v1/teams/default/agents/spawn" \
  -d "$(jq -n \
         --arg h "worker-42" \
         --arg k "claude-code" \
         --arg y "$(cat /tmp/spec.yaml)" \
         --arg host "h_abc123" \
         '{child_handle:$h, kind:$k, host_id:$host, spawn_spec_yaml:$y}')"
```

Responses:

| Status | Body | Meaning |
|--------|------|---------|
| 201 Created | `{spawn_id, agent_id, spawned_at, status:"spawned"}` | Agent row exists, pending the host. |
| 202 Accepted | `{status:"pending_approval", attention_id, tier}` | Policy gated; waits for approvals. |
| 4xx | `{error:"…"}` | Malformed YAML, duplicate handle, wrong scope. |

## 6. Spawning from an existing agent (MCP)

Agents reach the hub over stdio via `hub-mcp-bridge`, POSTing
newline-delimited JSON-RPC to `/mcp/{token}`. As of v1.0.298 the
`/mcp/<token>` endpoint serves the union of the narrow surface plus
the rich-authority catalog — one symlink installs everything spawned
agents need. Catalog (see `hub/internal/server/mcp.go` +
`mcp_more.go` + `mcp_orchestrate.go` + `mcp_authority.go` →
`internal/hubmcpserver/tools.go`):

- **Narrow surface** — `post_message`, `get_feed`, `list_channels`,
  `search`, `journal_append`, `journal_read`, `get_project_doc`,
  `get_attention`, `post_excerpt`, `delegate`, `request_approval`,
  `request_select`, `attach`, `get_event`, `get_task`,
  `get_parent_thread`, `list_agents`, `update_own_task_status`,
  `templates.propose`, `pause_self`, `shutdown_self`, `get_audit`,
  `permission_prompt`.
- **Orchestrator-worker primitives** — `agents.fanout`,
  `agents.gather`, `reports.post`.
- **Rich authority** — `projects.{list,get,create,update}`,
  `plans.{list,get,create}`, `plans.steps.{create,list,update}`,
  `runs.{list,get,create,attach_artifact}`, `documents.{list,create}`,
  `reviews.{list,create}`, `policy.read`, `artifacts.{list,get,create}`,
  `agents.spawn`, `channels.post_event`, `a2a.invoke`,
  `hosts.update_ssh_hint`, `project_channels.create`,
  `team_channels.create`, `tasks.{list,get,create,update}`,
  `schedules.{list,create,update,delete,run}`, `audit.read`.

Spawning from MCP: call `agents.spawn` with `child_handle`, `kind`,
`spawn_spec_yaml` (and optional `host_id` / `parent_agent_id` /
`worktree_path` / `budget_cents` / `mode`). Returns `201` with the
new agent row, or `202` with an `attention_id` when policy gates the
spawn on approval (Significant tier per `tiers.go`).

## 7. Spawning on a schedule

`hub-server` supports cron-style schedules that spawn agents
unattended. Manage them from mobile at **TeamSwitcher pill (top-left)
→ Schedules**, or via REST:

- `POST /v1/teams/{team}/schedules` — create
- `GET  /v1/teams/{team}/schedules` — list
- `PATCH /v1/teams/{team}/schedules/{id}` — enable / disable / edit
- `DELETE /v1/teams/{team}/schedules/{id}` — delete

Payload:

```json
{
  "name":            "nightly-triage",
  "cron_expr":       "0 2 * * *",
  "spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude triage"
}
```

The scheduler tick runs inside `hub-server serve`; no extra daemon.
Schedule-spawned agents flow through the same `DoSpawn` path as the
HTTP endpoint — including policy gating.

## 8. Lifecycle knobs on an existing agent

Tap an agent row on the Agents tab to open the detail sheet; each verb
also exists as REST.

| Action | Endpoint | Mobile |
|--------|----------|--------|
| Pause | `POST /v1/teams/{team}/agents/{id}/pause` | Pause button |
| Resume | `POST /v1/teams/{team}/agents/{id}/resume` | Resume button |
| Terminate | `PATCH …/agents/{id}` with `{"status":"terminated"}` | Terminate button |
| Read pane | `GET …/agents/{id}/pane` | "Pane capture" section |
| Journal | `GET /journal`, `POST /journal` | "Journal" section + note field |

**How `terminate` reaches the pane.** `PATCH status=terminated` is the
single source of truth — the handler updates the row *and* enqueues a
`terminate` host_command against the agent's host. The host-runner
kills the pane and attempts best-effort worktree cleanup. MCP
`shutdown_self` converges on the same host_command.

**Pane capture semantics.** `GET /pane` returns the *last cached*
capture (cached on the `agents` row by a prior capture command). Adding
`?refresh=1` enqueues a new capture; the current call still returns the
previous text, so the mobile sheet shows the stale value and then lets
you tap Refresh again after ~3s to pull the fresh one.

## 9. Status indicators on mobile — what exists today

Agents carry two flags, both rendered on mobile:

- `status`: `pending` / `running` / `idle` / `failed` / `terminated`,
  colored via `_agentStatusColor` in `lib/screens/projects/projects_screen.dart`.
- `pause_state`: `running` / `paused` — shown inline in the org-chart
  row subtitle.

Hosts carry:

- `status`: only ever flipped to `'online'` on register / heartbeat.
  **There is no sweeper that flips it to `offline`**, so after a
  host-runner exits the row persists at `online` indefinitely. Treat
  the status chip as advisory and use `last_seen_at` (rendered in the
  trailing column) as the real health signal.

What the mobile does **not** yet show:

- Hub reachability from the device (HTTP probe or SSE connection
  state). The bootstrap screen probes on save but there's no
  persistent indicator in the dashboard header. A red "hub offline"
  banner and a green dot in the app bar when the SSE stream is
  connected would close this gap.
- Per-agent *live* heartbeat — agents don't heartbeat; they update
  state implicitly through pane captures, the feed, and attention
  items.

See also: `TODO hub-connectivity-indicator` and
`TODO host-offline-sweeper` follow-ups.

## 10. End-to-end smoke

```bash
# 0. Register a host (see install-host-runner.md §4) — keep host-runner running.
# 1. Issue a user token for yourself (run on the hub box, as the hub service user).
TOK=$(sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
        -data /var/lib/termipod-hub \
        -kind user -team default -role member | awk '/^  /{print $1}')
HOST_ID=$(curl -fsS -H "Authorization: Bearer $TOK" \
          https://hub.example.com/v1/teams/default/hosts | jq -r '.[0].id')

# 2. Spawn.
curl -fsS -X POST -H "Authorization: Bearer $TOK" \
  -H 'content-type: application/json' \
  https://hub.example.com/v1/teams/default/agents/spawn \
  -d "$(jq -n --arg host "$HOST_ID" '
      { child_handle: "smoke-1",
        kind:         "claude-code",
        host_id:      $host,
        spawn_spec_yaml: "backend:\n  cmd: bash -lc \"echo hello; sleep 600\"\n"
      }')"

# 3. Watch the agent row flip pending → running.
watch -n1 "curl -fsS -H 'Authorization: Bearer $TOK' \
  https://hub.example.com/v1/teams/default/agents | \
  jq '[.[] | select(.handle==\"smoke-1\") | {status, pane_id}]'"

# 4. Clean up.
AGENT_ID=$(curl -fsS -H "Authorization: Bearer $TOK" \
           https://hub.example.com/v1/teams/default/agents | \
           jq -r '.[] | select(.handle=="smoke-1") | .id')
curl -fsS -X PATCH -H "Authorization: Bearer $TOK" \
  -H 'content-type: application/json' \
  https://hub.example.com/v1/teams/default/agents/$AGENT_ID \
  -d '{"status":"terminated"}'
```

From the mobile, the same flow is: **Projects → tap a project →
Agents pill → Spawn Agent FAB** → set handle `smoke-1`, pick the host,
paste the backend.cmd line, submit. Watch the row flip to green ~3s
later. (For the team steward, use the AppBar steward chip instead —
see §4a.)
