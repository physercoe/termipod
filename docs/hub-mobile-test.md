# Termipod Hub — Setup & Mobile Dashboard Guide

End-to-end walkthrough for standing up `hub-server` and connecting the
TermiPod mobile app's **Hub Dashboard** to it. Covers two tracks:

- **A. LAN / Tailscale quick test** — one command on a laptop, `http://`
  over a trusted network overlay. Takes ~2 minutes.
- **B. Public VPS with nginx + TLS** — systemd unit, nginx reverse proxy,
  Let's Encrypt cert. The setup you want for anything longer-lived than a
  demo.

The current release used for testing is **v1.0.47-alpha**.

**Companion docs:**

- [`hub-host-setup.md`](hub-host-setup.md) — register a host so agents
  have somewhere to run (host-runner daemon, token, systemd).
- [`hub-agents.md`](hub-agents.md) — spawn agents from mobile, REST,
  MCP, or on a schedule; spec YAML schema; lifecycle knobs.

---

## 1. Get the mobile build

The release workflow (`.github/workflows/release.yml`) fires on tag push
and attaches three Android APKs plus an unsigned iOS IPA.

```bash
# From the repo root, on the branch containing the hub changes.
git tag v1.0.47-alpha
git push origin v1.0.47-alpha
gh run watch                      # wait for the release build
gh release view v1.0.47-alpha     # grab the asset URLs
```

Assets:

- `termipod-v1.0.47-alpha-arm64-v8a.apk`   ← modern phones
- `termipod-v1.0.47-alpha-armeabi-v7a.apk` ← older 32-bit ARM
- `termipod-v1.0.47-alpha-x86_64.apk`      ← emulator / ChromeOS
- `termipod-v1.0.47-alpha-ios-unsigned.ipa` ← sideload via AltStore/Sideloadly

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

Expected: `{"server_version":"…","supported_api_versions":["v1"],"schema_versions_supported":[1]}`.

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

1. Launch TermiPod → **Settings** (bottom-right).
2. Scroll to **Termipod Hub** → **Open Hub Dashboard**.
3. First run is empty with a *Configure Hub* button — tap it.
4. Fill in:
   - **Base URL**: `https://hub.example.com` (Track B) or
     `http://<lan-ip>:8443` (Track A). Not `localhost`.
   - **Team ID**: `default` (the value `init` writes; change if you
     created other teams).
   - **Bearer Token**: paste the token.
5. Tap **Probe URL** → green banner with hub version = reachable.
6. Tap **Save & Connect** → back to the dashboard.

Token → device keychain (`flutter_secure_storage`). URL + team id →
SharedPreferences.

---

## 5. Walk the workspace

The app splits Hub functionality across three surfaces:

- **Inbox** (bottom-nav center tab) — unified workflow feed. Attention
  items, unread channels, and recent tasks roll up here. Search icon
  in the app bar hits `/v1/search`.
- **Hub** (bottom-nav tab) — four tabs over the registered inventory:
  Projects · Agents · Hosts · Templates.
- **Team settings** — people-icon button in the Hub app bar. Holds
  Schedules, Usage, Members, Policies, Channels.

Pull-to-refresh works on every list view. SSE keeps the Inbox and
project channels live.

| Surface | What to try |
|---------|-------------|
| **Inbox → Attention section** | The `decision` item you seeded shows up, with an orange severity chip. Tap **Approve** or **Reject** — the row disappears and a decision is recorded. |
| **Inbox → search icon** | Full-text search over events, tasks, attention items. 350 ms debounce, autofocus TextField. |
| **Hub → Projects → tap a project** | Linear-style detail page with Overview · Tasks · Channels · Docs · Blobs. Channel events stream live; excerpt parts render with a monospace line-number gutter. Docs renders markdown read-only. Blobs uploads / downloads content-addressed attachments (25 MiB cap, sha256 dedup). |
| **Hub → Projects → tap a task** | Subtasks and parent chevron navigation. Create tasks with the project-scoped FAB. |
| **Hub → Agents** | **List / Tree** toggle in the app bar. Tree view renders the `agent_spawns` parent/child graph with indent + cycle guard. **Spawn Agent** FAB opens a YAML sheet; preset chips at the top mint pre-filled YAML (long-press a chip to delete). "Save preset" stores the current YAML device-locally. |
| **Hub → Hosts** | Hosts running `host-runner` with a host-kind token show up here with `last_seen_at`. |
| **Hub → Templates** | Lists YAML agent templates under `<dataRoot>/default/templates/<category>/`. Tap to preview YAML. |
| **Hub → Team → Schedules** | Cron-triggered spawn schedules. Create / enable / disable / delete. |
| **Hub → Team → Usage** | Per-project and per-agent budget rollup with progress bars (`spent_cents` / `budget_cents`). |

### Round-trip smoke test

Open the project detail Channels tab (Hub → Projects → your project →
Channels → pick one) while running on the dev machine:

```bash
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/projects/<pid>/channels/<cid>/events" \
  -d '{"type":"message","from_id":"@ops","parts":[{"kind":"text","text":"hello phone"}]}'
```

Expected: a new row at the top of the feed within a second.

---

## 6. Operations

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

### Rotating a token

There is no `tokens rotate` yet. Issue a new token with `tokens issue`,
update the app, and let the old token age out (revocation is a future
task — currently deleting the row in `auth_tokens` is the workaround).

---

## 7. Known caveats

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

## 8. CI verification

Every push to `main` runs `flutter analyze --no-fatal-infos` and
`flutter test` (`.github/workflows/ci.yml`) plus the Go hub tests
(`.github/workflows/hub.yml`).

```bash
gh workflow run CI --ref <branch>
gh run watch
```
