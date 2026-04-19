# Termipod Hub — Mobile Dashboard Install / Test Guide

This guide walks through installing a test build of the TermiPod mobile app
that includes the **Hub Dashboard** (Slice 3), connecting it to a running
`hub-server`, and verifying each tab.

---

## 1. Build the APK via GitHub Actions

The release workflow (`.github/workflows/release.yml`) is already wired up.
Cutting a new tag triggers a release that builds **three APKs** (one per
ABI) and attaches them to a GitHub Release.

```bash
# From the repo root, on the branch containing the hub changes
git tag v1.0.39-alpha
git push origin v1.0.39-alpha
```

Watch the workflow:

```bash
gh run watch
gh release view v1.0.39-alpha
```

When the release is ready you'll see three assets:

- `termipod-v1.0.39-alpha-arm64-v8a.apk`   ← pick this for modern phones
- `termipod-v1.0.39-alpha-armeabi-v7a.apk` ← older 32-bit ARM only
- `termipod-v1.0.39-alpha-x86_64.apk`      ← emulator / ChromeOS

## 2. Sideload on Android

On the phone:

1. Open the release page in a mobile browser and download
   `termipod-*-arm64-v8a.apk`.
2. Tap the downloaded file. Android will ask you to allow this browser to
   install unknown apps — grant the permission once.
3. Accept the install prompt. If you already have TermiPod installed from
   an earlier tag it upgrades in place; data and settings are preserved.

## 3. Bring up a hub for the phone to talk to

Any machine the phone can reach over the network works. The simplest path
is a LAN/Tailscale host:

```bash
cd hub
go build -o /tmp/hub-server ./cmd/hub-server
/tmp/hub-server \
  --listen 0.0.0.0:8443 \
  --data-root ~/hub-test \
  --db        ~/hub-test/hub.db
```

Get a team + token:

```bash
# one-time bootstrap — prints a bearer token
/tmp/hub-cli bootstrap --team team --label phone-test
```

Copy the token string. It won't be shown again — the hub stores only the
SHA-256 hash.

> **Networking:** if your hub is on a laptop, make sure the phone and
> laptop are on the same Wi-Fi, or use Tailscale / ngrok to expose the
> port. `https://` is strongly recommended; over LAN you can also use
> `http://` for quick tests.

Seed a little data so the tabs have something to show:

```bash
# Create a project + channel, post one event, file one attention item.
HUB=http://<hub-host>:8443
TOK=<paste token>
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/team/projects" \
  -d '{"name":"test"}'
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/team/attention" \
  -d '{"scope_kind":"team","kind":"decision","summary":"Approve staging deploy?","severity":"major"}'
```

## 4. Configure the app

1. Open TermiPod. Tap **Settings** (bottom-right).
2. Scroll to the **Termipod Hub** section → **Open Hub Dashboard**.
3. The first time, the screen is empty with a *Configure Hub* button.
   Tap it.
4. Fill in:
   - **Base URL**: `http://<hub-host>:8443` (use your LAN IP / Tailscale
     name / ngrok host, *not* `localhost`)
   - **Team ID**: `team` (default)
   - **Bearer Token**: paste the token from step 3
5. Tap **Probe URL** — should show a green banner with the hub version.
6. Tap **Save & Connect** — returns to the dashboard.

The token is written to the device keychain
(`flutter_secure_storage`); the URL and team id go in SharedPreferences.

## 5. Walk the tabs

The dashboard has five tabs across the top. Pull-to-refresh works on
the list tabs.

| Tab | Expected |
|-----|----------|
| **Attention** | The `decision` item you created is listed. Severity chip is orange ("major"). **Approve** and **Reject** buttons record a decision and the row disappears. |
| **Feed** | Pick *Project: test*, then a channel. Events posted to that channel stream in live (SSE). Each event shows type, sender, and a preview of the first text part. |
| **Agents** | Empty until an agent is registered (`hub-cli spawn ...` or host-agent). |
| **Hosts** | Empty until a host-agent registers. Once one is running you see its name and `last_seen_at`. |
| **Projects** | Shows `test` with its created timestamp. |

## 6. Round-trip smoke test

From the dev machine, with the app open on the Feed tab of channel `X`:

```bash
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/team/projects/<pid>/channels/<cid>/events" \
  -d '{"type":"message","from_id":"@ops","parts":[{"kind":"text","text":"hello phone"}]}'
```

Expected: the row appears within a second at the top of the Feed tab.

## 7. Known caveats

- **Token handling**: the bearer token is stored in the OS keychain; the
  *app* never displays it back. If you forget it, issue a fresh token from
  the hub and re-enter it in **Settings → Termipod Hub → Open Hub
  Dashboard → ⚙ → Save & Connect**.
- **Self-signed TLS**: `dart:io.HttpClient` rejects invalid certs by
  default. For test-only deployments, use plain `http://` over a trusted
  network overlay (Tailscale). A proper TLS opt-out is a future task.
- **Stream backpressure**: the feed is capped at 200 entries in memory so
  a chatty channel doesn't OOM the phone. Older events drop silently; use
  the hub backfill API if you need a full history.
- **Approvals**: tapping *Approve* on an `approval_request` that carries a
  `pending_payload` (like a gated `spawn`) executes the action on the
  hub. The app reports "Decision recorded" — inspect the hub logs or the
  Feed tab to see the downstream effect.

## 8. CI verification

Every push to `main` runs `flutter analyze --no-fatal-infos` and
`flutter test` via `.github/workflows/ci.yml`. The hub files contribute
~1 kLoC of Dart and should be picked up by the analyzer automatically —
no new dependencies were added.

To run the CI manually on a branch:

```bash
gh workflow run CI --ref <branch>
gh run watch
```
