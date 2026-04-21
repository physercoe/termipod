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

## 3. Issue a host token (on the hub machine)

Same pattern as the VPS hub setup in `hub-mobile-test.md` §5 — run the
issuer as the hub's service user against the hub's data root:

```bash
sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
     -data /var/lib/termipod-hub \
     -kind host -team default -role host
```

The plaintext token is printed once. Transfer it to the host box over
a secure channel; a common pattern is:

```bash
# On the hub, copy the token to the clipboard or a pastebin-style store,
# then on the host:
sudo install -d -o root -g termipod-host -m 0750 /etc/termipod-host
printf 'HUB_URL=https://hub.example.com\nHUB_TEAM=default\nHUB_TOKEN=%s\n' \
       "paste-the-plaintext-token-here" \
    | sudo install -o root -g termipod-host -m 0640 /dev/stdin /etc/termipod-host/env
```

`kind=host` scopes the token to register / heartbeat / spawn-list /
command-list / agent-patch and nothing else. Never reuse an owner
token here.

## 4. Build and install `host-agent`

Mirror of hub-mobile-test.md §2 + §B.1 — build in a scratch location,
then `sudo install` the static binary into `/usr/local/bin`. The
binary is self-contained; cross-compile from a dev box if the target
architecture differs.

```bash
# On a builder box (Go 1.23+):
cd hub
go build -o /tmp/host-agent ./cmd/host-agent

# Copy /tmp/host-agent to the target host (scp / rsync / release asset),
# then on the target:
sudo install -o root -g root -m 0755 /tmp/host-agent /usr/local/bin/host-agent
/usr/local/bin/host-agent --help
```

Create a system user with a **real home directory** — backend CLIs
(claude, codex, git) expect `~/.claude`, `~/.codex`, `~/.gitconfig`,
`~/.ssh` to be readable and writable:

```bash
sudo useradd --system --create-home \
             --home /var/lib/termipod-host \
             --shell /bin/bash \
             termipod-host
```

(Hub uses `--shell /usr/sbin/nologin` because nothing runs shell
commands as `termipod-hub`. Host needs `/bin/bash` so tmux /
subprocess PATH resolution works normally.)

### Quick one-shot test before wiring systemd

```bash
sudo -u termipod-host env $(cat /etc/termipod-host/env | xargs) \
     /usr/local/bin/host-agent run \
       --hub   "$HUB_URL" \
       --team  "$HUB_TEAM" \
       --token "$HUB_TOKEN" \
       --name  "$(hostname)" \
       --launcher tmux --tmux-session hub-agents
```

Expected stderr: `host registered host_id=h_… name=…`. Verify from
mobile (Hub → Hosts tab) or:

```bash
curl -fsS -H "Authorization: Bearer <owner-or-user-token>" \
     https://hub.example.com/v1/teams/default/hosts | jq
```

Ctrl-C out of the one-shot before moving to systemd.

## 5. Install the shipped systemd unit

The repo ships a unit at `hub/deploy/systemd/termipod-host.service`
(the host counterpart to `termipod-hub.service`). It reads the
env-file from §3, runs as `termipod-host`, and keeps hardening light
enough for Node-JIT backends (claude-code) to function — see the
unit's own comment header for the rationale.

```bash
sudo install -m 0644 hub/deploy/systemd/termipod-host.service \
     /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now termipod-host
sudo systemctl status termipod-host
sudo journalctl -fu termipod-host
```

`%H` in the unit's `--name %H` is the systemd hostname specifier —
expands to the machine's hostname at start time, so you get a
human-readable row on the mobile **Hub → Hosts** tab without hard-
coding the name.

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
