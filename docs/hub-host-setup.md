# Termipod Hub — Host Setup

How to add a **host** to a running hub so it can execute agents. Covers
the `host-runner` daemon: what it is, how to register the host, how to
run it under systemd, and how to tell whether it's healthy.

> **Terminology.** In the codebase, *host*, *agent*, and *host-runner*
> are three distinct things:
>
> - **Host** — a row in the `hosts` table. A machine (or a login user on
>   a machine) that can run backend CLIs in tmux panes. Identified by a
>   `host_id`, tracked via heartbeat + `last_seen_at`.
> - **Agent** — a row in the `agents` table. A single backend CLI
>   process with its own handle, journal, MCP token, and pane.
> - **`host-runner`** — the Go daemon that runs *on* a host and launches
>   agent processes on behalf of the hub. It is not itself an agent
>   (no row in the `agents` table).

---

## 1. What the host-runner does

`host-runner run` is a long-running daemon. Loop:

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

The hub is the source of truth — host-runner holds no persistent state
across restarts.

## 2. Prerequisites on the host

- Linux / macOS box reachable to the hub (Tailscale, LAN, or public).
- `tmux` ≥ 3.2 (required for `-F` format variables the launcher uses).
- `git` (only if you'll use `worktree:` specs).
- One or more backend CLIs on `PATH` for the login user: e.g. `claude`,
  `codex`.
- Go 1.23+ **only if you build from source**; prebuilt binaries land
  next to the hub-server in the release workflow (planned).

> **Why it runs as a login user, not a dedicated system user.** The
> TermiPod mobile app views/operates tmux by SSH'ing into this box as
> your login account. tmux sockets live in `/tmp/tmux-<uid>/`, so the
> runner must share a uid with the SSH user or the mobile app cannot
> attach to the session it creates. The systemd unit is therefore a
> template — one instance per login user.

## 3. Issue a host token (on the hub machine)

Same pattern as the VPS hub setup in `hub-mobile-test.md` §5 — run the
issuer as the hub's service user against the hub's data root:

```bash
sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
     -data /var/lib/termipod-hub \
     -kind host -team default -role host
```

The plaintext token is printed once. Issue **one token per host-runner
instance** (so one per login user if you plan to run multiple on a
box) — tokens bind 1:1 to the `host_id` the runner registers under.

Transfer each token to the target host over a secure channel. The env
file lives at `/etc/termipod-host/<username>.env` — the basename must
match the Linux login user the instance will run as, because the
systemd template reads `EnvironmentFile=/etc/termipod-host/%i.env`:

```bash
# On the host, as root. Replace "ubuntu" with the target login user.
sudo install -d -o root -g root -m 0755 /etc/termipod-host
printf 'HUB_URL=https://hub.example.com\nHUB_TEAM=default\nHUB_TOKEN=%s\n' \
       "paste-the-plaintext-token-here" \
    | sudo install -o root -g ubuntu -m 0640 /dev/stdin /etc/termipod-host/ubuntu.env
```

The file is group-readable by the login user, mode `0640`. Re-run with
a different username + token for each additional instance.

`kind=host` scopes the token to register / heartbeat / spawn-list /
command-list / agent-patch and nothing else. Never reuse an owner
token here.

## 4. Build and install `host-runner`

Mirror of hub-mobile-test.md §2 + §B.1 — build in a scratch location,
then `sudo install` the static binary into `/usr/local/bin`. The
binary is self-contained; cross-compile from a dev box if the target
architecture differs.

```bash
# On a builder box (Go 1.23+):
cd hub
go build -o /tmp/host-runner ./cmd/host-runner

# Copy /tmp/host-runner to the target host (scp / rsync / release asset),
# then on the target:
sudo install -o root -g root -m 0755 /tmp/host-runner /usr/local/bin/host-runner
/usr/local/bin/host-runner --help
```

No system user to create — the runner uses an existing login account
(`ubuntu`, `admin`, whatever you already SSH as from TermiPod). Backend
CLIs (claude, codex, git) will find `~/.claude`, `~/.codex`,
`~/.gitconfig`, `~/.ssh` under that user's real home.

### Quick one-shot test before wiring systemd

```bash
# As the login user the runner will run as (here, "ubuntu"):
sudo -u ubuntu env $(cat /etc/termipod-host/ubuntu.env | xargs) \
     /usr/local/bin/host-runner run \
       --hub   "$HUB_URL" \
       --team  "$HUB_TEAM" \
       --token "$HUB_TOKEN" \
       --name  "$(hostname)-ubuntu" \
       --launcher tmux --tmux-session hub-agents
```

Expected stderr: `host registered host_id=h_… name=…`. Verify from
mobile (Hub → Hosts tab) or:

```bash
curl -fsS -H "Authorization: Bearer <owner-or-user-token>" \
     https://hub.example.com/v1/teams/default/hosts | jq
```

Ctrl-C out of the one-shot before moving to systemd.

## 5. Install the shipped systemd template unit

The repo ships a **template unit** at
`hub/deploy/systemd/termipod-host@.service`. The `@` means the instance
name is the login user it runs as: `termipod-host@ubuntu`,
`termipod-host@admin`, etc. The unit reads
`/etc/termipod-host/%i.env`, runs as `User=%i`, and registers under the
name `%H-%i` so each instance shows up distinctly on the Hosts tab.

```bash
sudo install -m 0644 hub/deploy/systemd/termipod-host@.service \
     /etc/systemd/system/
sudo systemctl daemon-reload

# Enable the instance for the login user you want the runner to run as:
sudo systemctl enable --now termipod-host@ubuntu
sudo systemctl status termipod-host@ubuntu
sudo journalctl -fu termipod-host@ubuntu
```

The hub-side row appears as `<hostname>-ubuntu`. When you spawn an
agent from the mobile app, pick that row; the pane lands in
`ubuntu`'s tmux session, which your TermiPod connection (SSH'd in as
`ubuntu`) can attach to.

### Multiple users on the same host

Each instance is independent: its own token, its own host row, its own
tmux session in `/tmp/tmux-<uid>/`. To add a second login user:

```bash
# 1. Issue a second host token on the hub (§3).
# 2. Install its env file under the second user's name:
printf 'HUB_URL=https://hub.example.com\nHUB_TEAM=default\nHUB_TOKEN=…\n' \
    | sudo install -o root -g admin -m 0640 /dev/stdin /etc/termipod-host/admin.env
# 3. Enable the second instance:
sudo systemctl enable --now termipod-host@admin
```

Hub now sees `host-01-ubuntu` **and** `host-01-admin` as distinct host
rows. The mobile app, with two separate TermiPod connections (one for
each user), can view/drive each user's tmux panes independently.

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
  `journalctl -u termipod-host@<user>` will show the error. Most
  common: no `tmux` on PATH for that login user, or the `--hub` URL
  redirects (host-runner's HTTP client doesn't follow 30x — point it
  straight at the proxy).
- **Heartbeat 401 / 403.** The host row exists but the token's scope
  is wrong (e.g. `team=foo` but the host row was created under
  `team=default`). Recreate either the host or the token to match.
- **Spawns accepted by the hub but pane never opens.** host-runner
  logs `launch failed`. Check that the `tmux-session` exists / can be
  created, and that `backend.cmd` in the spawn YAML resolves to an
  executable on PATH for the instance's login user.
- **Mobile attaches to tmux but doesn't see the spawned pane.** The
  TermiPod connection is SSH'd in as a different user than the
  instance's `%i`. Either switch the mobile connection to that user,
  or enable a second `termipod-host@<mobile-user>` instance and spawn
  there instead.
