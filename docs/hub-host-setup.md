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
   Updates `hosts.last_seen_at` and clamps `status='online'`. Body
   carries the host-runner binary's build metadata
   (`runner_commit`, `runner_build_time`, `runner_modified`) so the
   hub-side host detail sheet can show "host-runner is at commit X,
   built Y" — first reflected in mobile within ~10s of a redeploy.
   Empty body still works (older host-runners just don't update
   those columns).
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

On startup, the runner also ensures `~/hub-work` exists (mode 0755).
This is the default workdir for the built-in steward template
(`templates/agents/steward.v1.yaml` `backend.default_workdir`); the
M2 launcher `cd`'s into it before spawning the agent process. The
mkdir is idempotent and non-fatal — a custom template using a
different workdir is the operator's responsibility.

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

The plaintext token is printed once — copy it now. `kind=host` scopes
the token to register / heartbeat / spawn-list / command-list /
agent-patch and nothing else; never reuse an owner token here.

> **Principal tokens and `-handle`.** Human tokens (`-role principal`) should
> also pass a `-handle <name>` so the Members tab shows `@<name>` rather
> than `@principal (unnamed)`. Host-role tokens don't need a handle; they
> aren't shown on Members.

Issue **one token per host-runner instance** (so one per login user if
you plan to run multiple on a box) — tokens bind 1:1 to the `host_id`
the runner registers under.

How the token reaches the host depends on which track you pick below:
Track A pastes it on the command line, Track B writes it to an env
file that systemd reads.

## 4. Build the binary

A host needs **one** Go binary: `host-runner`. It is a busybox-style
multicall binary that also runs the MCP bridge (the stdio↔HTTP shim
that lets the spawned agent reach the hub's MCP endpoint) when
invoked as `hub-mcp-bridge` — typically via a symlink under
`/usr/local/bin`. Without that symlink (or `host-runner mcp-bridge`
in the spawn's `.mcp.json`), claude-code's `session.init` reports
`termipod: failed` and the agent runs without hub-mediated tools.

```bash
# On any box with Go 1.23+ (cross-compile for a different target via
# GOOS=linux GOARCH=amd64):
cd hub
go build -o ~/host-runner ./cmd/host-runner
```

Copy `~/host-runner` to the target host. Two install paths follow —
pick one.

---

## Track A — Quick start inside tmux (no sudo, no systemd)

For beginners, a local dev box, or trying things out. You end up with
a host-runner process attached to your **current** tmux session,
spawning agents into new windows alongside whatever else you're doing.

**What you need:** the host token from §3, a running tmux session,
and the binary on `$PATH` so claude-code (running inside the agent
process spawned by host-runner) can find `hub-mcp-bridge` by name
without needing root.

The cleanest no-sudo recipe is to drop the binary under `~/.local/bin`
(usually already on PATH for login shells) and add the bridge symlink
beside it:

```bash
mkdir -p ~/.local/bin
mv ~/host-runner ~/.local/bin/host-runner
ln -sf host-runner ~/.local/bin/hub-mcp-bridge
hash -r          # bash/zsh: refresh PATH lookup cache
which host-runner hub-mcp-bridge  # both should resolve under ~/.local/bin
```

If `~/.local/bin` isn't on your PATH, add it (e.g. in `~/.bashrc`:
`export PATH="$HOME/.local/bin:$PATH"`) and reopen the shell. Without
the symlink, the agent's MCP handshake fails the same way Track B
fails when its symlink is missing — see §7 troubleshooting.

```bash
# Inside a tmux session (attach first with `tmux attach` or start one
# with `tmux new -s work`):

host-runner run \
  --hub   https://hub.example.com \
  --team  default \
  --token <paste-the-host-token> \
  --tmux-session "$(tmux display-message -p '#S')"
```

That's it. What the flags do:

- `--hub` / `--team` / `--token` — where to connect and who you are.
- `--tmux-session "$(tmux display-message -p '#S')"` — the magic bit.
  Tells the runner to use the tmux session you're **already in**, so
  spawned agents appear as new windows in that session instead of a
  separate `hub-agents` session you'd have to attach to.
- `--a2a-addr` / `--a2a-public-url` (optional) — enable the A2A
  agent-card server. See "Enabling the A2A agent-card server" in Track B
  for details.

Leave the shell running (or detach the whole tmux session with
`Ctrl-b d` and reattach later). The runner heartbeats every 10s and
polls for spawns every 3s. Each spawn becomes a new window in your
current session — switch to it with `Ctrl-b n` / `Ctrl-b <N>` or via
the mobile app's tmux viewer.

**Stopping:** `Ctrl-C` in the runner's pane. The agents it already
launched keep running in their own windows — close them with
`Ctrl-b &` or let them finish.

**Limits of Track A.** No auto-restart if the runner crashes, no boot
persistence, no isolation from your shell environment. Fine for trying
things out; move to Track B for a long-lived host.

---

## Track B — Systemd template (production, multi-user)

For a host that should come up on boot and survive crashes. Uses the
shipped template unit so one box can host multiple instances (one per
login user — see the multi-user section below).

### Install the binary system-wide

`host-runner` goes into `/usr/local/bin` so it's on PATH for every
login user. A symlink named `hub-mcp-bridge` next to it routes
agent-side spawns into the same binary's bridge mode (claude-code
spawns `hub-mcp-bridge` by bare name from `.mcp.json`):

```bash
sudo install -o root -g root -m 0755 ~/host-runner /usr/local/bin/host-runner
sudo ln -sf host-runner /usr/local/bin/hub-mcp-bridge
/usr/local/bin/host-runner --help
which hub-mcp-bridge   # must print /usr/local/bin/hub-mcp-bridge
```

No system user to create — the runner uses an existing login account
(`ubuntu`, `admin`, whatever you already SSH as from TermiPod). Backend
CLIs (claude, codex, git) will find `~/.claude`, `~/.codex`,
`~/.gitconfig`, `~/.ssh` under that user's real home.

### Drop the token into an env file

The systemd template reads `/etc/termipod-host/%i.env`, where `%i` is
the Linux login user the instance will run as. The file basename must
match:

```bash
# On the host, as root. Replace "ubuntu" with the target login user.
sudo install -d -o root -g root -m 0755 /etc/termipod-host
printf 'HUB_URL=https://hub.example.com\nHUB_TEAM=default\nHUB_TOKEN=%s\n' \
       "paste-the-plaintext-token-here" \
    | sudo install -o root -g ubuntu -m 0640 /dev/stdin /etc/termipod-host/ubuntu.env
```

Mode `0640`, group-readable by the login user. Re-run with a different
username + token for each additional instance.

### Install the shipped systemd template unit

The repo ships a **template unit** at
`hub/deploy/systemd/termipod-host@.service`. The `@` means the instance
name is the login user it runs as: `termipod-host@ubuntu`,
`termipod-host@admin`, etc. The unit runs as `User=%i` and registers
under the name `%H-%i` so each instance shows up distinctly on the
Hosts tab.

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

### Enabling the A2A agent-card server (optional, blueprint §5.4)

The host-runner can expose agent-cards at
`http://<host>:<port>/a2a/<agent-id>/.well-known/agent.json` per A2A v0.3
so other agents can discover what each live agent on this host can do.
Disabled by default; enable by adding two flags (or env vars in the
systemd unit):

- `--a2a-addr :47821` — bind address. `:0` picks a free port; a fixed
  port is easier to publish. Firewall this to peers that need it.
- `--a2a-public-url https://host.example:47821` — the URL advertised in
  the `url` field of each agent-card. Use this when peers reach the
  host through a reverse proxy or tunnel; otherwise the server falls
  back to the request Host header.

Verify with:

```bash
curl -fsS http://<host>:47821/a2a/agents | jq .
curl -fsS http://<host>:47821/a2a/<agent-id>/.well-known/agent.json | jq .
```

The card lists skills derived from the agent handle (e.g., handles
starting with `steward` advertise `plan` + `brief`, `ml-worker` /
`worker` advertise `train`, `briefing` advertises `brief`).

When `--a2a-addr` is set, the host-runner also **pushes its card set to
the hub directory** every 30s (change-hashed, so idle hosts don't write
needlessly):

- `PUT /v1/teams/{team}/hosts/{host}/a2a/cards` — the host replaces its
  entire card set atomically. Called by `Client.PutA2ACards`.
- `GET /v1/teams/{team}/a2a/cards?handle=worker.ml` — steward lookup
  across hosts. Returns `{host_id, agent_id, handle, card, registered_at}`
  per match.

This lets a steward on a different host (typically the VPS) discover
worker agents on a GPU host by handle — necessary because typical GPU
hosts sit behind NAT and can't be dialed directly.

When `--a2a-addr` is set, the host-runner *also* opens an outbound
reverse tunnel to the hub:

- `GET /v1/teams/{team}/hosts/{host}/a2a/tunnel/next?wait_ms=25000` —
  host-runner long-polls; hub returns a queued A2A request envelope
  (`req_id`, `method`, `path`, `headers`, `body_b64`) or 204 on timeout.
- `POST /v1/teams/{team}/hosts/{host}/a2a/tunnel/responses` —
  host-runner POSTs the dispatched response back, keyed by `req_id`.

Public A2A peers (e.g., a steward on another host) call
`<hub>/a2a/relay/<host>/<agent>/.well-known/agent.json` (token-less per
A2A v0.3 spec). The hub queues the request for the named host, blocks
up to 20s waiting for the host-runner's response, then flushes it to
the caller. If no tunnel is connected or dispatch is slow, the hub
returns `504 Gateway Timeout`.

The host-runner dispatches relayed requests through its local
`a2a.Server.Handler()`, so the exact same routes serve direct peers
and tunneled peers. Today only the agent-card path is implemented.

Task endpoints (send / get / cancel) are a follow-up wedge.

### Enabling the trackio metric-digest poller (optional, blueprint §6.5)

When workers on this host log training curves to [trackio](https://github.com/gradio-app/trackio),
the host-runner can read each run's local SQLite file, downsample every
scalar series, and push a compact digest to the hub so the mobile app
can render sparklines (§6.5, P3.1).

Enable by adding one flag (or env var in the systemd unit):

- `--trackio-dir /home/worker/.cache/huggingface/trackio` — trackio's
  root dir. Falls back to `$TRACKIO_DIR` then `~/.cache/huggingface/trackio`
  if unset; leave empty to disable the poller entirely. Must be the same
  directory trackio itself uses; each project gets its own `{project}.db`
  SQLite file under this root.

The poller ticks every 20 s:

1. Lists runs via `GET /v1/teams/{team}/runs?trackio_host=<host>`.
2. For each run, parses `trackio_run_uri` (canonical form
   `trackio://<project>/<run_name>`), opens `{project}.db` read-only, and
   reads every `(step, metric_json)` row for the run.
3. Splits the JSON blobs into one series per scalar key, downsamples to
   ≤100 points per curve (uniform stride, first + last preserved), then
   PUTs the digest to `PUT /v1/teams/{team}/runs/{run}/metrics`.

`sample_count`, `last_step`, and `last_value` come from the raw,
un-downsampled series so the mobile headline number matches trackio's
own dashboard exactly. Non-numeric JSON values (strings, arrays, nested
objects) are skipped silently — only sparkline-renderable scalars
propagate.

Blueprint §4 data-ownership law: the hub stores digest rows only. Bulk
time-series never leaves the host.

### Enabling the wandb metric-digest poller (optional, blueprint §6.5)

When workers on this host log training curves to [wandb](https://wandb.ai/)
in offline mode, the host-runner can read each run's local
`wandb-history.jsonl` file, downsample every scalar series, and push a
compact digest to the hub so the mobile app can render sparklines
(§6.5, P3.1). This loop is independent of the trackio poller — both can
run side-by-side, and runs are discriminated by the `trackio_run_uri`
scheme column.

Enable by adding one flag (or env var in the systemd unit):

- `--wandb-dir /home/worker/wandb` — wandb's offline-run root dir. The
  host-runner has no notion of the worker's cwd, so this must be passed
  explicitly when enabled (unlike wandb's in-process `./wandb` default).
  Leave empty to disable the poller entirely. Each run lives at
  `<root>/<run-dir>/files/wandb-history.jsonl` under this root.

The poller ticks every 20 s:

1. Lists runs via `GET /v1/teams/{team}/runs?trackio_host=<host>`.
2. For each run whose `trackio_run_uri` starts with `wandb://`, parses
   the canonical form `wandb://<project>/<run-dir>`, opens
   `<wandb-dir>/<run-dir>/files/wandb-history.jsonl`, and reads each
   JSON-per-line row.
3. Extracts `_step` (integer) and every other numeric scalar key as one
   `(step, value)` sample per metric. Underscore-prefixed keys
   (`_step`, `_timestamp`, `_runtime`, `_wandb`) are wandb metadata and
   stay out of the digest. Strings, arrays, nested objects
   (histograms, images), and nulls are skipped silently — only
   sparkline-renderable scalars propagate.
4. Downsamples each series to ≤100 points per curve (uniform stride,
   first + last preserved) and PUTs the digest to
   `PUT /v1/teams/{team}/runs/{run}/metrics`.

`sample_count`, `last_step`, and `last_value` come from the raw,
un-downsampled series so the mobile headline number matches wandb's
own dashboard exactly.

Blueprint §4 data-ownership law: the hub stores digest rows only. Bulk
time-series never leaves the host.

### Enabling the TensorBoard metric-digest poller (optional, blueprint §6.5)

When workers on this host log training curves as TensorBoard tfevents
files instead of (or in addition to) trackio, the host-runner can walk
each run's logdir, decode the tfevents stream directly, downsample
every scalar series, and push the same compact digest to the hub. The
two readers are independent — a host may enable one, both, or neither.

Enable by adding one flag (or env var in the systemd unit):

- `--tb-dir /home/worker/tb-logs` — TensorBoard root logdir. Each run
  lives in a subdirectory under this root (the `<run-path>`), and its
  tfevents files (`events.out.tfevents.<ts>.<host>.<pid>.v2`) sit
  directly inside that subdirectory. Leave empty to disable the
  TensorBoard poller entirely.

The poller ticks every 20 s:

1. Lists runs via `GET /v1/teams/{team}/runs?trackio_host=<host>`
   (shared with the trackio poller — the digest wire format doesn't
   care which reader produced it).
2. For each run whose `trackio_run_uri` parses as `tb://<run-path>`,
   opens `<tb-dir>/<run-path>/`, reads every `events.out.tfevents.*`
   file in lex order, and folds each record's scalar values into a
   per-tag series keyed by `Summary.Value.tag`.
3. Downsamples each series to ≤100 points per curve (uniform stride,
   first + last preserved) and PUTs the digest to
   `PUT /v1/teams/{team}/runs/{run}/metrics`.

Both the legacy `simple_value` encoding and the newer single-element
`DT_FLOAT` `TensorProto` scalars are read. Non-float tensors,
histograms, images, and audio events are skipped silently — only
sparkline-renderable scalars propagate. `sample_count`, `last_step`,
and `last_value` come from the raw, un-downsampled series so the
mobile headline number matches TensorBoard's own scalar panel.

The TFRecord parser skips CRC verification: TensorBoard writers fsync
on close so partial writes are vanishingly rare, and when they do
happen the safer policy is "treat as clean EOF" rather than fail the
whole file. A truncated trailing record simply ends iteration.

The URI scheme is independent of trackio's. Workers that want to
expose TensorBoard curves should set
`runs.trackio_run_uri = "tb://<run-path>"` via the existing
`POST /v1/teams/.../runs/<id>/metric_uri` endpoint.

## 5. Health: how to tell if a host is alive

There are three signals, in decreasing reliability:

| Signal | Source | Caveat |
|-------|--------|--------|
| `hosts.last_seen_at` is within the last ~30s | `GET /v1/teams/{team}/hosts` | Most reliable. If the daemon is wedged, this drifts. |
| `hosts.status == 'online'` | Same row | **Stale.** Today there is no background sweeper that flips status to `offline`, so an abandoned host sits on `online` forever. Treat status as advisory; rely on `last_seen_at`. See follow-up `TODO host-offline sweeper`. |
| `hosts.runner_commit` / `runner_build_time` | Same row, populated from heartbeat body (v1.0.261+) | Confirms which host-runner build is actually running. Mobile shows this on the host detail sheet as `Runner: commit abc1234 · built 2026-04-25`. Empty for hosts that haven't heartbeated since the v1.0.261 runner was deployed. |
| Mobile **Hub → Hosts** tab | Renders both fields | Surfaces the stale status. Until the sweeper lands, trust the timestamp column over the status chip. |

Command-line smoke:

```bash
curl -fsS -H "Authorization: Bearer $TOK" \
  https://hub.example.com/v1/teams/default/hosts \
  | jq '.[] | {name, status, last_seen_at}'
```

## 6. Deleting a host

Use when you've decommissioned a box or want to retire a stale row
(e.g. the hostname changed). Two equivalent paths:

- **Mobile:** Hub → Hosts → tap the row → **Delete host**.
- **REST:** `DELETE /v1/teams/{team}/hosts/{host_id}`.

The hub refuses the delete with `409 Conflict` if any agents on this
host are still alive (status not in `terminated`/`failed`). Terminate
them from the Agents tab first — terminating also tells the host-runner
to kill the pane, so the row flips cleanly once cleanup completes.

`host_commands` cascade-delete with the host row. Agents that were
attached keep their history but lose their `host_id` (ON DELETE SET
NULL), so the org chart still shows the record.

## 7. Backup and restore

The hub keeps all team state in `<data-root>/hub.db` (SQLite) plus
`<data-root>/team/` (templates, policy, agent_families overlay) and
`<data-root>/blobs/` (content-addressed attached files). A backup needs
to capture all three.

**Take a backup** (safe while the daemon is live — uses
`VACUUM INTO` so the snapshot is a transactionally-consistent copy):

```bash
sudo -u termipod-hub /usr/local/bin/hub-server backup \
  -data /var/lib/termipod-hub \
  --to /var/backups/termipod/hub-$(date +%F).tar.gz
```

The output is a single `.tar.gz` containing `hub.db.snapshot`, `team/`
and `blobs/`. Move it off-host to wherever you keep cold backups.

**Restore on a fresh box**:

```bash
sudo install -d -m 0700 -o termipod-hub -g termipod-hub /var/lib/termipod-hub
sudo -u termipod-hub /usr/local/bin/hub-server restore \
  --from /var/backups/termipod/hub-2026-04-27.tar.gz \
  -data /var/lib/termipod-hub
```

`restore` refuses to clobber a non-empty data root unless `--force` is
passed; the guard is the difference between "I lost my hub" and "I lost
my hub twice". After restore the hub-server will run pending migrations
on the next `serve` so an older backup boots cleanly on a newer binary.

**After restore on a new host:** host-runner tokens reference the old
hub URL. Reissue them via `hub-server tokens issue` and update each
host's `--hub-token` so heartbeats line up again. The `hosts` rows
themselves survive the round-trip.

What backup does **not** include: `event_log/` JSONL spool (rebuildable
via `reconstruct-db`), live tmux/pane state on connected hosts, and
mobile-side data (connections, SSH keys, snippets — those export from
Settings → Data).

## 8. Troubleshooting

- **Host never appears in `GET /hosts`.** Token is wrong or scoped to
  the wrong team. List tokens with
  `sudo -u termipod-hub /usr/local/bin/hub-server tokens list -data /var/lib/termipod-hub`;
  reissue if needed (see §3).
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
- **Steward's session.init lists `termipod` MCP server as `failed`.**
  The `hub-mcp-bridge` symlink is missing or not on PATH for the
  spawned agent. Check `which hub-mcp-bridge` as the host-runner's
  user; if empty, recreate per §4 (`sudo ln -sf host-runner
  /usr/local/bin/hub-mcp-bridge`). Other symptoms: the mobile
  transcript shows tool calls failing with "command not found"-style
  errors, and `journalctl -u termipod-host@<user>` may report the
  bridge exec returned ENOENT. After install, terminate and re-spawn
  the steward — the failed MCP handshake doesn't auto-recover within
  a running session.
