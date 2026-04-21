# Termipod Hub — Host Setup

How to add a **host** to a running hub so it can execute agents. Covers
the `host-agent` daemon: what it is, how to register the host, how to
run it under systemd, and how to tell whether it's healthy.

> **Terminology note.** In the current codebase, *host* and *agent* are
> distinct concepts:
>
> - **Host** — a row in the `hosts` table. A machine that can run
>   backend CLIs (claude-code, codex) in tmux panes. Identified by a
>   `host_id`, tracked via heartbeat + `last_seen_at`.
> - **Agent** — a row in the `agents` table. A single backend CLI
>   process with its own handle, journal, MCP token, and pane.
> - **`host-agent`** — the Go daemon that runs *on* a host and launches
>   agent processes on behalf of the hub. It is not itself an agent
>   (not in the `agents` table). The naming is currently ambiguous;
>   see the *Open naming question* section at the bottom.

---

## 1. What the host-agent does

`host-agent run` is a long-running daemon. Loop:

1. On first boot, call `POST /v1/teams/{team}/hosts` with a display
   name and capabilities → receives a `host_id`. Persist nothing
   locally; on restart, pass `--host-id` so it skips re-registration.
2. **Heartbeat** every 10s: `POST /v1/teams/{team}/hosts/{host}/heartbeat`.
   Updates `hosts.last_seen_at` and clamps `status='online'`.
3. **Poll** every 3s:
   - `GET /v1/teams/{team}/agents/spawns?host_id=…&status=pending` —
     any spawn assigned to this host that hasn't started yet. For each,
     parse `spawn_spec_yaml`, optionally materialize a git worktree,
     launch a tmux pane via the configured launcher, then
     `PATCH /v1/teams/{team}/agents/{id}` with status=running + pane_id.
   - `GET /v1/teams/{team}/hosts/{host}/commands?status=pending` —
     out-of-band commands (pane capture, tmux signals). Each gets run
     then patched done/failed.
   - Idle-detection pass over each running pane: capture stdout via
     `tmux capture-pane`, hash it, compare against the previous tick.
     When the hash stays stable past a threshold, post a `kind=idle`
     attention item.

The hub is the source of truth — host-agent holds no persistent state
across restarts.

## 2. Prerequisites on the host

- Linux / macOS box reachable to the hub (Tailscale, LAN, or public).
- `tmux` ≥ 3.2 (required for `-F` format variables the launcher uses).
- `git` (only if you'll use `worktree:` specs).
- One or more backend CLIs on `PATH`: e.g. `claude`, `codex`.
- Go 1.23+ **only if you build from source**; prebuilt binaries land
  next to the hub-server in the release workflow (planned).

## 3. Issue a host token

On the hub machine:

```bash
hub-server tokens issue \
  -data /var/lib/termipod-hub \
  -kind host \
  -team default \
  -role host
```

The plaintext token is printed once. Copy it to the host box via any
secure channel (scp, password manager, `ssh … 'cat > ~/.termipod-host.token'`).

The `kind=host` scope authorizes the register / heartbeat / spawn-list /
command-list / agent-patch endpoints and nothing else; do not reuse an
owner token for this.

## 4. Build and install host-agent on the host

From the repo root on the host (or cross-compile and scp):

```bash
cd hub
go build -o /usr/local/bin/host-agent ./cmd/host-agent
host-agent --help
```

Quick one-shot test before wiring systemd:

```bash
host-agent run \
  --hub https://hub.example.com \
  --token "$(cat ~/.termipod-host.token)" \
  --team default \
  --name "$(hostname)" \
  --launcher tmux \
  --tmux-session hub-agents
```

Expected stderr:

```
host registered host_id=h_… name=…
```

Verify from the mobile (or curl):

```bash
curl -fsS -H "Authorization: Bearer $TOK" \
  https://hub.example.com/v1/teams/default/hosts
# → [{"id":"h_…","name":"…","status":"online","last_seen_at":"…"}]
```

On the mobile, **Hub → Hosts** now lists the host with a recent
`last_seen_at`.

## 5. Run it under systemd

Copy the token to a root-owned file so only the service user can read:

```bash
sudo install -o termipod-host -g termipod-host -m 0600 \
  ~/.termipod-host.token /etc/termipod-host/token
```

Install a unit (save as `/etc/systemd/system/termipod-host.service`):

```ini
[Unit]
Description=Termipod host-agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=termipod-host
Group=termipod-host
# Keep the token out of the command line / proc listing.
Environment=HUB_URL=https://hub.example.com
Environment=HUB_TEAM=default
EnvironmentFile=/etc/termipod-host/env
ExecStart=/usr/local/bin/host-agent run \
          --hub ${HUB_URL} \
          --team ${HUB_TEAM} \
          --token ${HUB_TOKEN} \
          --name %H \
          --launcher tmux \
          --tmux-session hub-agents
Restart=on-failure
RestartSec=3s

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=yes
ReadWritePaths=/home/termipod-host

[Install]
WantedBy=multi-user.target
```

Where `/etc/termipod-host/env` is:

```
HUB_TOKEN=paste-the-plaintext-token-here
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now termipod-host
sudo journalctl -fu termipod-host
```

`%H` is the unit hostname specifier — expands to the machine's
hostname at start time.

## 6. Health: how to tell if a host is alive

There are three signals, in decreasing reliability:

| Signal | Source | Caveat |
|-------|--------|--------|
| `hosts.last_seen_at` is within the last ~30s | `GET /v1/teams/{team}/hosts` | Most reliable. If the daemon is wedged, this drifts. |
| `hosts.status == 'online'` | Same row | **Stale.** Today there is no background sweeper that flips status to `offline`, so an abandoned host sits on `online` forever. Treat status as advisory; rely on `last_seen_at`. See follow-up `TODO host-offline sweeper`. |
| Mobile **Hub → Hosts** tab | Renders both fields | Surfaces the stale status. Until the sweeper lands, trust the timestamp column over the status chip. |

Command-line smoke:

```bash
curl -fsS -H "Authorization: Bearer $TOK" \
  https://hub.example.com/v1/teams/default/hosts \
  | jq '.[] | {name, status, last_seen_at}'
```

## 7. Troubleshooting

- **Host never appears in `GET /hosts`.** Token is wrong or scoped to
  the wrong team. Check `hub-server tokens list`; reissue if needed.
- **Host registers, then disappears from logs.** The daemon exited.
  `journalctl -u termipod-host` will show the error. Most common: no
  `tmux` on PATH, or the `--hub` URL redirects (host-agent's HTTP
  client doesn't follow 30x — point it straight at the proxy).
- **Heartbeat 401 / 403.** The host row exists but the token's scope
  is wrong (e.g. `team=foo` but the host row was created under
  `team=default`). Recreate either the host or the token to match.
- **Spawns accepted by the hub but pane never opens.** host-agent logs
  `launch failed`. Check that the `tmux-session` exists / can be
  created, and that `backend.cmd` in the spawn YAML resolves to an
  executable on PATH for the service user.

## 8. Open naming question

The term **host-agent** is ambiguous because "agent" is also the name
of the per-task records in the `agents` table. A user reading "I need
to run a host-agent on each machine" can reasonably wonder whether
that registers an agent row.

Candidate renames (not yet done):

- **host-runner** — mirrors GitHub Actions' runner / GitLab's executor;
  clearly a per-host process orchestrator, not a workload itself.
- **host-daemon** — neutral, explicit that it's a daemon.
- **hub-worker** — emphasizes the hub-side relationship.

A rename touches the binary name, `hub/cmd/host-agent/`, the Go
package `hostagent`, the systemd unit, every doc, and the mobile
string `'(${h['status']} )'`. It is deferred until we agree on a
final term.
