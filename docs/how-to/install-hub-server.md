# Install hub-server

> **Type:** how-to
> **Status:** Current (2026-04-28)
> **Audience:** operators
> **Last verified vs code:** v1.0.314

**TL;DR.** End-to-end walkthrough for standing up `hub-server` and
connecting the TermiPod mobile app's **Hub Dashboard** to it. Two
tracks:

- **A. LAN / Tailscale quick test** — one command on a laptop,
  `http://` over a trusted network overlay. Takes ~2 minutes.
- **B. Public VPS with nginx + TLS** — systemd unit, nginx reverse
  proxy, Let's Encrypt cert. The setup you want for anything
  longer-lived than a demo.

**Companion docs:**

- [`install-host-runner.md`](install-host-runner.md) — register a host so agents
  have somewhere to run (host-runner daemon, token, systemd).
- [`../reference/hub-agents.md`](../reference/hub-agents.md) — spawn agents from mobile, REST,
  MCP, or on a schedule; spec YAML schema; lifecycle knobs.
- [`run-the-demo.md`](run-the-demo.md) — exercise the
  research-demo pipeline end-to-end without a GPU, using the shipped
  `seed-demo` subcommand and `mock-trainer` CLI.

---

## 1. Get the mobile build

The release workflow (`.github/workflows/release.yml`) fires on tag push
and attaches three Android APKs plus an unsigned iOS IPA.

```bash
# From the repo root, on the branch containing the hub changes.
# Substitute X.Y.Z below with the next available version (current
# is in `pubspec.yaml`; bump via `make bump VERSION=...`).
git tag vX.Y.Z-alpha
git push origin vX.Y.Z-alpha
gh run watch                       # wait for the release build
gh release view vX.Y.Z-alpha       # grab the asset URLs
```

Assets (where `vX.Y.Z` matches the tag you pushed):

- `termipod-vX.Y.Z-alpha-arm64-v8a.apk`     ← modern phones
- `termipod-vX.Y.Z-alpha-armeabi-v7a.apk`   ← older 32-bit ARM
- `termipod-vX.Y.Z-alpha-x86_64.apk`        ← emulator / ChromeOS
- `termipod-vX.Y.Z-alpha-ios-unsigned.ipa`  ← sideload via AltStore/Sideloadly

### Sideload on Android

1. Download `termipod-*-arm64-v8a.apk` in a mobile browser.
2. Tap the file. Grant the browser "install unknown apps" permission.
3. Accept the install prompt. Upgrades preserve data + settings.

---

## 2. Build `hub-server`

```bash
cd hub
go build -o /tmp/hub-server ./cmd/hub-server
/tmp/hub-server help
```

The binary is self-contained — no libc dependency beyond the platform
default. Cross-compile for a VPS with e.g.
`GOOS=linux GOARCH=amd64 go build -o hub-server-linux-amd64 ./cmd/hub-server`.

> **Version reporting.** `go build` inside a git tree automatically
> embeds the commit hash + build time (via `runtime/debug.ReadBuildInfo`),
> so `/v1/_info` will return them alongside `server_version`. The
> `server_version` string itself is the constant in
> `hub/internal/buildinfo/buildinfo.go`, which **must** match
> `pubspec.yaml`'s `version:`. Bump both atomically from the repo
> root with:
>
> ```bash
> make bump VERSION=1.0.262-alpha
> ```
>
> This sed-edits both files and computes the matching Android build
> number. Always use this target rather than editing either file by
> hand.

---

## Track A — LAN / Tailscale quick test

### A.1 Initialize a data root and get the owner token

```bash
/tmp/hub-server init -data ~/hub-test
```

Output ends with:

```
Owner token (shown once — store it in your TUI / mobile config):

  <paste-me-into-the-app>
```

The hub stores only the SHA-256 hash. If you lose it, issue a new token
with `tokens issue` (§5).

### A.2 Serve on the LAN

```bash
/tmp/hub-server serve -listen 0.0.0.0:8443 -data ~/hub-test
```

> **Do not bind `0.0.0.0:8443` on a public network** without TLS. Over a
> coffee-shop Wi-Fi that's a cleartext bearer token on the wire. For LAN
> testing with known peers, or a Tailscale interface, it's fine.

Tailscale variant — bind to the tailnet IP only:

```bash
TS_IP=$(tailscale ip -4)
/tmp/hub-server serve -listen ${TS_IP}:8443 -data ~/hub-test
```

> **A2A relay (optional, cross-host demo only)**: add
> `-public-url https://<hub-public-host>:8443` when NAT'd GPU hosts
> publish agent-cards. The hub rewrites each card's `url` field to
> `<public-url>/a2a/relay/<host>/<agent>` at list time so off-box peers
> dial the hub relay instead of the unreachable host-runner address the
> host stamped locally. Unset is fine for single-host and LAN demos —
> the hub falls back to the request Host header.

### A.3 (Optional) seed a little data

So the tabs aren't empty:

```bash
HUB=http://<host>:8443
TOK=<owner-token>
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/projects" -d '{"name":"test"}'
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/attention" \
  -d '{"scope_kind":"team","kind":"decision","summary":"Approve staging deploy?","severity":"major"}'
```

Skip to §4 to configure the app.

---

## Track B — VPS with nginx + Let's Encrypt

### B.1 Install the binary and data dir

```bash
sudo install -o root -g root -m 0755 hub-server /usr/local/bin/hub-server
sudo useradd --system --home /var/lib/termipod-hub --shell /usr/sbin/nologin termipod-hub
sudo install -o termipod-hub -g termipod-hub -m 0750 -d /var/lib/termipod-hub
sudo -u termipod-hub /usr/local/bin/hub-server init -data /var/lib/termipod-hub
# ↑ prints the owner token — copy it now, it's not recoverable.
```

### B.2 Install the systemd unit

The repo ships a hardened unit at `hub/deploy/systemd/termipod-hub.service`.
It runs `hub-server serve -listen 127.0.0.1:8443` as the `termipod-hub`
user with `ProtectSystem=strict` and `ReadWritePaths=/var/lib/termipod-hub`.

```bash
sudo install -m 0644 hub/deploy/systemd/termipod-hub.service \
     /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now termipod-hub
sudo systemctl status termipod-hub
```

### B.3 Install the nginx bootstrap config (port 80 only)

**Order matters.** The final config references cert files at
`/etc/letsencrypt/live/<host>/` — those don't exist yet, so if you drop
the full config in first, `nginx -t` fails with *"cannot load
certificate"* and reload is rejected. Install a port-80-only bootstrap
first so certbot's HTTP-01 challenge can land, then swap in the full
config.

```bash
sudo apt install nginx certbot python3-certbot-nginx
sudo install -d -m 0755 /var/www/html
sudo install -m 0644 hub/deploy/nginx/termipod-hub-bootstrap.conf \
     /etc/nginx/sites-available/termipod-hub.conf
sudo ln -sf ../sites-available/termipod-hub.conf \
     /etc/nginx/sites-enabled/termipod-hub.conf
sudoedit /etc/nginx/sites-available/termipod-hub.conf   # replace hub.example.com
sudo nginx -t
sudo systemctl reload nginx
```

Sanity: `curl -fsS http://hub.example.com/` should return
`termipod-hub bootstrap — awaiting TLS`. If it doesn't, fix DNS / the
default server before continuing.

### B.4 Obtain a TLS cert

```bash
sudo certbot certonly --webroot -w /var/www/html -d hub.example.com
# — or —
sudo certbot --nginx -d hub.example.com   # rewrites the config for you
```

Confirm the cert landed at `/etc/letsencrypt/live/hub.example.com/fullchain.pem`.
`certbot.timer` handles renewals.

### B.5 Swap in the full reverse-proxy config

Now that the cert files exist, replace the bootstrap with the real config.
It terminates TLS and proxies to `127.0.0.1:8443`, with dedicated location
blocks for SSE streams (`/v1/teams/*/stream` and the per-channel variant)
that set `proxy_buffering off` and `proxy_read_timeout 3600s` — without
those, mobile Feed / Attention drop every ~60s.

```bash
sudo install -m 0644 hub/deploy/nginx/termipod-hub.conf \
     /etc/nginx/sites-available/termipod-hub.conf
sudoedit /etc/nginx/sites-available/termipod-hub.conf   # replace hub.example.com
sudo nginx -t
sudo systemctl reload nginx
```

> **nginx < 1.25** (Ubuntu 22.04 default is 1.18) doesn't know the
> standalone `http2 on;` directive. The shipped config uses the legacy
> `listen 443 ssl http2;` form, which works on both old and new nginx.

### B.6 Sanity check

From any machine:

```bash
curl -fsS -H "Authorization: Bearer <owner-token>" \
     https://hub.example.com/v1/_info
```

Expected (v1.0.256+):

```json
{
  "server_version": "1.0.262-alpha",
  "supported_api_versions": ["v1"],
  "schema_versions_supported": [1],
  "commit": "0426abe…",
  "build_time": "2026-04-25T15:00:00Z",
  "modified": false
}
```

`server_version` matches `pubspec.yaml`'s `version:` string (kept in
sync via `make bump`). `commit` / `build_time` / `modified` come from
`runtime/debug.ReadBuildInfo` and are populated automatically by
`go build` inside a git tree — empty when built from a tarball.

---

## 3. Issue additional tokens

The owner token is fine for the first phone. For additional devices,
agents, or hosts, mint scoped tokens:

Run the issuer as the hub's service user so it can read the sqlite DB
under `/var/lib/termipod-hub` (owned by `termipod-hub`, mode `0750`).
Bare `hub-server tokens ...` from your login shell will fail with
`sqlite: unable to open database file`:

```bash
# Another user device on the default team
sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
     -data /var/lib/termipod-hub \
     -kind user -team default -role member

# An MCP agent, bound to a specific agent id
sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
     -data /var/lib/termipod-hub \
     -kind agent -team default -role agent -agent-id claude-42

# A host-runner
sudo -u termipod-hub /usr/local/bin/hub-server tokens issue \
     -data /var/lib/termipod-hub \
     -kind host -team default -role host

sudo -u termipod-hub /usr/local/bin/hub-server tokens list \
     -data /var/lib/termipod-hub
```

Plaintext is printed once. Only SHA-256 hashes land in `auth_tokens`.
(Track A / LAN test is simpler — if the data root is in your own
`$HOME`, just run `hub-server tokens …` directly without sudo.)

---

## 4. Configure the mobile app

1. Launch TermiPod → bottom tab **Projects**.
2. First run is empty with a *Configure Hub* button — tap it.
3. Fill in:
   - **Display name** *(optional)*: any label. Defaults to
     `team @ host`. Useful when you save more than one profile.
   - **Base URL**: `https://hub.example.com` (Track B) or
     `http://<lan-ip>:8443` (Track A). Not `localhost`.
   - **Team ID**: `default` (the value `init` writes; change if you
     created other teams).
   - **Bearer Token**: paste the token.
4. Tap **Probe URL** → green banner with hub version = reachable.
5. Tap **Save & Connect** → back to the dashboard.

Subsequent profiles (different hub, or same hub + different team)
are added via the **TeamSwitcher pill → Add profile…**. The pill's
popup menu also lets you switch between saved profiles, rename or
delete them via **Manage profiles…**, and reach **Templates &
engines** + **Team settings**.

Token → device keychain (`flutter_secure_storage`). URL + team id →
SharedPreferences.

---

## 5. Bootstrap: your first steward

The previous sections stand up the hub and point the app at it, but the
Me / Activity tabs are still empty. For the app to be useful someone has
to sit at the other end of `#hub-meta` — that's the **steward**. This
section walks the zero-agents → steward-running transition end to end.

A steward is just an agent (a backend CLI running in a tmux pane) with
the `handle='steward'` reserved name. The hub doesn't auto-spawn one;
the mobile app does, via a shipped welcome card.

### 5.1 Prerequisite: one online host

You cannot spawn any agent — steward included — without a host to run it
on. Register one now if you haven't:

1. Build and run `host-runner` on the online server that will host your
   steward. See [`install-host-runner.md`](install-host-runner.md) — Track A for a
   quick tmux-attached test, Track B for systemd.
2. On the **Hosts** bottom tab, confirm a row with a recent
   `last_seen_at`. The steward spawn sheet requires `status='online'`;
   the row appears within ~10s of the first heartbeat.

The host just needs `tmux` and one backend CLI on PATH for its login
user (the default template uses `claude`, see §5.3 to change).

### 5.2 Tap the steward chip

1. **Projects** bottom tab. The AppBar carries a steward chip. With no
   steward yet it reads `🤖 No steward` (muted colour) — tap it.
2. The **Spawn the team steward** sheet opens. The handle (`steward`,
   reserved), the kind (`claude-code`), and the spec YAML (from
   `agents/steward.v1`) are fixed — the only choice is the host
   dropdown, which pre-selects the first `status='online'` host. Tap
   **Spawn Steward**. The app:
   - fetches the bundled template YAML via
     `GET /v1/teams/default/templates/agents/steward.v1.yaml`,
   - POSTs `/v1/teams/default/agents/spawn` with
     `child_handle='steward'`, `kind='claude-code'`, `host_id=<picked>`,
     and the rendered YAML.
3. Within ~3s the host-runner's poll tick picks up the pending spawn,
   opens a tmux window, launches `claude --model opus-4-7`, and PATCHes
   the agent row to `status='running'`.
4. The AppBar chip flips to `🤖 Steward ready` (live colour). Tapping
   it now opens `#hub-meta` directly.
5. In `#hub-meta`, type a message; the steward backend reads it through
   its MCP token and replies. You now have a working director ↔
   steward loop.

If the spawn is policy-gated instead (tiers `significant` or `critical`
in `policies/default.v1.yaml`), step 2 returns `202 pending_approval` +
an `attention_id`. The sheet shows "Spawn request sent — awaiting
approval"; approve it from **Me → Attention** and the real spawn runs.

### 5.3 Customizing the steward before first spawn

The template that ships in the binary is at
`hub/templates/agents/steward.v1.yaml`. On first `hub-server init`, it's
copied to `<dataRoot>/team/templates/agents/steward.v1.yaml` — **user
edits win**, subsequent init calls never overwrite. To change the model,
default workdir, or A2A skills before your first spawn, edit the on-disk
copy, then tap Spawn Steward. Anything beyond that (prompt, tone,
autonomy level) is exposed in **TeamSwitcher pill → menu → Team
settings → Steward Config**, though some values are still
SharedPreferences-local pending the server round-trip endpoint.

### 5.4 What's covered by automated tests

The spawn-from-scratch sequence above is exercised end-to-end by
`hub/internal/server/e2e_acceptance_test.go` step `04_spawn_steward`,
which GETs the bundled template and POSTs `/agents/spawn` against a
freshly-initialized data root. That test is the regression gate for this
flow; if the welcome card stops working, that test is where to look
first. The underlying agent-spawn pipeline is documented in
[`../reference/hub-agents.md`](../reference/hub-agents.md) §2.

---

## 6. Walk the workspace

The IA redesign (v1.0.175–v1.0.182) rebuilt the workspace around five
top-level tabs — **Me · Projects · Activity · Hosts · Settings** —
center-anchored on Me. Team-level configuration, hub-profile
switching, and the templates/engines library all live behind the
**TeamSwitcher pill** (top-left of every tab) which opens a popup
menu.

- **Me** — your attention items + "My Work" strip. Director-first view:
  what's waiting on a human decision, what the steward raised
  (StewardBadge lights up on `actor_kind='agent'` + `actor_handle='steward'`
  rows since v1.0.183), and "Since you were last here" digest.
- **Projects** — one home for projects and their templates. Tap a
  project for Overview · Tasks · Channels · Docs · Blobs · Agents.
- **Activity** — team-wide audit/event feed. Steward filter chip in
  the app bar isolates agent-originated rows.
- **Hosts** — unified host inventory (SSH connections ∪ hub-registered
  hosts joined on `hostBindingsProvider`).
- **Settings** — app + account settings; Team Settings, the
  Templates library, and hub-profile management are reached via the
  TeamSwitcher pill (Steward Config, Councils stub, Schedules, etc.).

Pull-to-refresh works on every list view. SSE keeps Me and project
channels live.

| Surface | What to try |
|---------|-------------|
| **Me → Attention** | The `decision` item you seeded shows up, with an orange severity chip and a **StewardBadge** next to the actor. Tap **Approve** or **Reject** — the row disappears and a decision is recorded. |
| **Me → search icon** | Full-text search over events, tasks, attention items. 350 ms debounce, autofocus TextField. |
| **Projects → tap a project** | Linear-style detail page with Overview · Tasks · Channels · Docs · Blobs · Agents. Channel events stream live; excerpt parts render with a monospace line-number gutter. Docs renders markdown read-only. Blobs uploads / downloads content-addressed attachments (25 MiB cap, sha256 dedup). |
| **Projects → tap a task** | Subtasks and parent chevron navigation. Create tasks with the project-scoped FAB. |
| **Projects → project detail → Agents pill** | Project-scoped agent list. Bottom-right **Spawn Agent** FAB opens a YAML sheet pre-filled with `project_id:` for this project; preset chips mint pre-filled YAML (long-press a chip to delete). "Save preset" stores the current YAML device-locally. Handle `steward` is reserved — use the AppBar steward chip instead. Tap an agent row to open its detail sheet (pause / resume / terminate / pane preview / journal / respawn). |
| **Hosts** | Hosts running `host-runner` with a host-kind token show up here with `last_seen_at`, alongside any SSH-only connections. |
| **Projects → Templates row** | Lists YAML agent templates under `<dataRoot>/default/templates/<category>/`. Tap to preview YAML. |
| **TeamSwitcher pill → Team settings → Schedules** | Cron-triggered spawn schedules. Create / enable / disable / delete. |
| **TeamSwitcher pill → Team settings → Usage** | Per-project and per-agent budget rollup with progress bars (`spent_cents` / `budget_cents`). |
| **TeamSwitcher pill → Team settings → Steward Config** | Form to edit the team steward's principal handle, tone, constraints (SharedPreferences-local today — server round-trip is an open follow-up). |
| **TeamSwitcher pill → Templates & engines** | The bundled YAML/MD template library, grouped by category. Tap a row to preview the body. Same surface as the old AppBar Library icon. |
| **TeamSwitcher pill → Manage profiles…** | Add / rename / re-edit / delete saved hub profiles. The active profile is marked with a check; tap a row to switch active. |
| **Activity → Steward filter chip** | Restricts the feed to rows where `actor_kind='agent'` AND `actor_handle='steward'`. |

### Round-trip smoke test

Open the project detail Channels tab (Projects → your project →
Channels → pick one) while running on the dev machine:

```bash
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/projects/<pid>/channels/<cid>/events" \
  -d '{"type":"message","from_id":"@ops","parts":[{"kind":"text","text":"hello phone"}]}'
```

Expected: a new row at the top of the feed within a second.

---

## 7. Operations

### Backups

The `<dataRoot>/` directory holds everything: `hub.db` (sqlite) plus an
append-only `event_log/*.jsonl`. A tarball of the whole tree is a full
backup. `hub-server reconstruct-db -data <dataRoot>` rebuilds the DB from
the JSONL log when the sqlite file is lost or corrupted.

`hub/deploy/litestream/` is a future spot for a continuous-replication
config (currently empty).

### Upgrades

1. Build the new `hub-server`.
2. `sudo systemctl stop termipod-hub`
3. `sudo install -m 0755 hub-server /usr/local/bin/hub-server`
4. `sudo systemctl start termipod-hub`

Migrations run on start. Data root layout is stable — no dump/restore
needed between alpha builds so far.

**Notable changes since v1.0.54-alpha** (the previous release tag) —
no breaking changes to the install flow above, just new surfaces to
try:

- **`seed-demo` subcommand** (v1.0.169) — `hub-server seed-demo --data
  <root>` inserts a ready-to-browse `ablation-sweep-demo` project so
  the mobile UI has something to render without running real agents.
  Idempotent.
- **`mock-trainer` CLI** (v1.0.170) — `hub/cmd/mock-trainer` writes
  trackio SQLite or wandb-offline JSONL files for dress-rehearsing
  the host-runner → digest → mobile sparkline path. See
  [`run-the-demo.md`](run-the-demo.md).
- **Activity timeline** (v1.0.49 / audit log, extended v1.0.166–167)
  — mutations across runs, documents, reviews, projects, and
  channels now emit `audit_events` rows. `MCP get_audit` tool lets
  an agent surface the timeline.
- **MCP tool surface expansion** (v1.0.153–156) — `schedules.*`,
  `tasks.*`, `channels.create`, `projects.update`,
  `hosts.update_ssh_hint`. Lets a steward agent drive the full
  research-demo loop end-to-end.
- **Metric-digest pollers** (v1.0.14+) — host-runner reads trackio
  SQLite / wandb-offline JSONL / TensorBoard tfevents and PUTs ≤100
  -point digests to the hub. Per-vendor flags on host-runner; see
  [`install-host-runner.md`](install-host-runner.md) §"Enabling the … poller".
- **A2A relay + tunnel** (v1.0.157) — NAT'd GPU hosts advertise
  agent-cards via the hub relay, tunneled over a long-poll
  connection. Add `-public-url` to `hub-server serve` on the
  nginx/VPS side; add `--a2a-addr` + `--a2a-public-url` on
  host-runner.
- **Project templates as data** (v1.0.158–161) — first-party
  templates (ablation-sweep, write-memo, benchmark-comparison,
  reproduce-paper) seed automatically on first init. Existing data
  roots pick them up via `INSERT OR IGNORE` on next start.

### Rotating a token

There is no `tokens rotate` yet. Issue a new token with `tokens issue`,
update the app, and let the old token age out (revocation is a future
task — currently deleting the row in `auth_tokens` is the workaround).

---

## 8. Known caveats

- **Token recovery**: bearer tokens are stored hashed. Lose one, issue a
  fresh one — there is no recovery flow.
- **Self-signed TLS**: `dart:io.HttpClient` rejects invalid certs. Use
  Let's Encrypt (Track B) or a trusted network overlay with plain `http://`
  (Track A). A proper TLS opt-out in-app is a future task.
- **Stream memory cap**: the Feed keeps 200 entries in memory. Older
  events drop silently — use the hub's backfill API for full history.
- **Approvals**: tapping *Approve* on an `approval_request` that carries
  a `pending_payload` (e.g. a gated spawn) executes the action on the
  hub. Confirm via hub logs or a new Feed entry.
- **Push notifications**: not yet — the app only updates while the Hub
  Dashboard is foreground. Design is deferred pending FCM config
  decisions.

---

## 9. CI verification

Every push to `main` runs `flutter analyze --no-fatal-infos` and
`flutter test` (`.github/workflows/ci.yml`) plus the Go hub tests
(`.github/workflows/hub.yml`).

```bash
gh workflow run CI --ref <branch>
gh run watch
```
