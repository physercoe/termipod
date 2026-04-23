# Termipod Hub — Agent Spawning

How to spawn an **agent** — a backend CLI process (claude-code, codex,
…) running in a tmux pane on a registered host. Covers the four paths
that land a row in the `agents` table: mobile, REST, MCP bridge, and
scheduler.

## 1. Preconditions

- A hub (`hub-server serve`) reachable from both the mobile / CLI
  caller and the host that will run the agent.
- At least one **host** registered — see `docs/hub-host-setup.md`.
- A bearer token with a kind that permits `/agents/spawn`. Owner and
  user tokens are fine; agent- and host-kind tokens are not.

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
  cmd: "claude --model opus-4-7 --no-update"

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

## 4. Spawning from mobile

**Prebuilt.** Open the TermiPod app → **Hub** tab (bottom nav) →
**Agents** tab (one of four: Projects · Agents · Hosts · Templates) →
bottom-right **Spawn Agent** FAB.

The sheet has:

1. **Preset chips** (device-local) — tap to prefill handle / kind /
   YAML. Long-press to delete. "Save preset" at the bottom captures
   the current form.
2. **Load template** — fetches `GET /v1/teams/{team}/templates/agents`,
   lists them, and pastes the selected YAML.
3. **Handle** — the child agent's handle (must be unique per team).
4. **Kind** — free-form backend identifier (`claude-code`, `codex`, …).
5. **Host** — dropdown of registered hosts. Defaults to the first
   `status=online` host (see *Status indicators* below about the
   staleness caveat).
6. **Spec YAML** — the YAML from §3 above.

Submit → one of two outcomes:

- `"Agent \"…\" spawned."` — the agent row is created; a pending
  row appears on the Agents tab within a couple of seconds and flips
  to running once the host picks it up.
- `"Spawn request sent — awaiting approval."` — policy-gated; an
  `approval_request` attention item is filed. Approvers (including
  you, if you're on the list) can approve from the Me tab (attention
  section); the real spawn then happens.

### Can I spawn from mobile today?

**Yes.** The FAB → dialog path described above is the shipped mobile
spawn flow (v1.0.41+).

What you cannot do yet from mobile:

- Edit an existing agent's spawn spec (read-only in the tree view).
- Bulk-spawn multiple agents from one YAML.
- Create a brand-new template; only *use* server-side templates or
  save device-local presets.
- Approve-in-place on the spawn dialog — approvals live in the
  Me tab's attention section.

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
newline-delimited JSON-RPC to `/mcp/{token}`. The current MCP tool set
(see `hub/internal/server/mcp.go`) is:

    post_message, get_feed, list_channels, search,
    journal_append, journal_read, get_project_doc,
    get_attention, post_excerpt

There is **no `spawn` MCP tool yet**. An agent that wants to delegate
must either:

- emit a `post_message` asking the user/steward to spawn, or
- call the REST `/agents/spawn` endpoint directly with its own token
  (possible but not set up as a one-liner).

Adding `spawn` to the MCP tool registry is an obvious next step and is
tracked as a follow-up.

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
  colored via `_agentStatusColor` in `lib/screens/hub/hub_screen.dart`.
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
# 0. Register a host (see hub-host-setup.md §4) — keep host-runner running.
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

From the mobile, the same flow is: **Projects → tap a project → Agents
sub-tab → Spawn Agent FAB**
→ set handle `smoke-1`, pick the host, paste the backend.cmd line,
submit. Watch the row flip to green ~3s later.
