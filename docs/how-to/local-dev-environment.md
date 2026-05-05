# Local development environment

> **Type:** how-to
> **Status:** Current (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** Cold-start guide: clone the repo, install Flutter + Go,
build and run the hub locally, point the mobile app at it, register a
host-runner, exercise the dress-rehearsal pipeline with `mock-trainer`,
make a test change, and submit a PR. Targets a fresh laptop in ≤90
minutes (excluding SDK download time).

This doc is the contributor-side cousin of
[`install-hub-server.md`](install-hub-server.md) and
[`install-host-runner.md`](install-host-runner.md). Those target
**operators** standing up production-ish hubs; this targets a
**contributor** who needs the whole stack on one machine to develop +
test changes.

---

## 1. Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| Git | any recent | for clone |
| Flutter SDK | 3.24+ | bundles Dart 3.x; install via the [official guide](https://docs.flutter.dev/get-started/install) |
| Go | 1.23+ | for hub / host-runner / mock-trainer; [official tarball](https://go.dev/dl/) recommended (Ubuntu's apt build is older) |
| Android Studio + SDK | latest stable | for Android target — emulator or real device with USB debugging |
| Xcode | 15+ | for iOS target (macOS only) |
| `tmux` | 3.2+ | required for host-runner; `apt install tmux` / `brew install tmux` |
| `jq`, `curl` | any | for the smoke-test recipes below |

**Platform support.** macOS / Linux / WSL2 are tested. Windows native
is untested — use WSL2.

**Network.** Outbound HTTPS to your chosen engine vendor (Anthropic /
OpenAI / Google) for end-to-end agent flows. The hub itself does not
need internet; only spawned agents do.

**Verify SDKs:**

```bash
flutter --version             # ≥ 3.24
flutter doctor                # resolve any 'X' rows before continuing
dart --version                # bundled with Flutter
go version                    # ≥ 1.23
tmux -V                       # ≥ 3.2
```

If `go` isn't on `PATH`, prepend `/usr/local/go/bin:$PATH` (per the
official-tarball install).

---

## 2. Clone the repo

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
```

Repo layout (top-level):

```
termipod/
├── lib/                     # Flutter app source
├── hub/                     # Go services (hub-server, host-runner,
│                            #              mock-trainer, MCP bridge)
├── docs/                    # documentation (read docs/README.md first)
├── scripts/                 # lint scripts (lint-docs.sh, lint-glossary.sh)
├── android/  ios/  …        # Flutter platform shells
├── pubspec.yaml             # mobile version + dart deps
└── Makefile                 # build / bump / analyze / test
```

Read [`../README.md`](../README.md) (index) and
[`../spine/blueprint.md`](../spine/blueprint.md) (architecture) before
making non-trivial changes.

---

## 3. Hub — local development

### 3.1 Build

```bash
cd hub
go build -o /tmp/hub-server ./cmd/hub-server
/tmp/hub-server help
```

Self-contained binary; no libc dependency beyond the platform default.
The build embeds commit hash + build time via
`runtime/debug.ReadBuildInfo`, which surfaces in `/v1/_info`.

### 3.2 Initialize a data root

```bash
/tmp/hub-server init -data ~/hub-dev
```

The output ends with an **owner token** — copy it now, it's only
printed once and only the SHA-256 lands in the database.

### 3.3 Serve locally

```bash
/tmp/hub-server serve -listen 127.0.0.1:8443 -data ~/hub-dev
```

Verify reachability from another terminal:

```bash
curl -fsS -H "Authorization: Bearer <owner-token>" \
  http://127.0.0.1:8443/v1/_info
# {"server_version":"1.0.351-alpha","supported_api_versions":["v1"],…}
```

If you'll point a phone at this hub, swap `127.0.0.1` for
`0.0.0.0` and use your laptop's LAN IP. Plain `http://` is fine for
local development; the mobile app rejects self-signed TLS over
`https://`, so use `http://` + LAN, not self-signed.

### 3.4 Seed sample data (optional)

The hub ships a demo seeder that populates a complete project
(ablation sweep) so the mobile UI has something to render without
running real agents:

```bash
/tmp/hub-server seed-demo --data ~/hub-dev
```

Idempotent; pass `-reset` to wipe and re-insert. See
[`run-the-demo.md`](run-the-demo.md) for what the seed contains.

---

## 4. Mobile — local development

### 4.1 Install dependencies

```bash
# from the repo root
flutter pub get
```

### 4.2 Run on a device or emulator

Plug in an Android device with USB debugging enabled, or start an
emulator from Android Studio:

```bash
flutter devices                # list connected targets
flutter run                    # picks the first; or pass -d <id>
```

For a release-style build:

```bash
make build-apk                 # uses Makefile's --dart-define=GIT_REF
```

### 4.3 Configure the app to point at your hub

In the running app:

1. Bottom tab **Projects** → tap **Configure Hub** (first run is
   empty).
2. Fill in:
   - **Display name** — any label
   - **Base URL** — `http://<your-lan-ip>:8443` (not `localhost`
     when running on a real device)
   - **Team ID** — `default`
   - **Bearer Token** — paste the owner token from §3.2
3. Tap **Probe URL** → green banner means reachable.
4. Tap **Save & Connect**.

The token lives in `flutter_secure_storage` (OS keychain). URL + team
ID land in `SharedPreferences`.

### 4.4 Spawn the steward (optional)

The Me tab's **Steward** FAB walks you through spawning the team
steward agent. You'll need a host-runner first — see §5. The full
sequence is documented in
[`install-hub-server.md`](install-hub-server.md) §5.

---

## 5. Host-runner — local development

The host-runner is a Go daemon that runs on a host (laptop or VPS)
and launches agent processes in tmux panes on the hub's behalf.

### 5.1 Build

```bash
cd hub
go build -o ~/host-runner   ./cmd/host-runner
go build -o ~/mock-trainer  ./cmd/mock-trainer   # for §6
```

The host-runner binary doubles as the MCP bridge when invoked as
`hub-mcp-bridge` — set up the symlink so spawned agents find it on
PATH:

```bash
mkdir -p ~/.local/bin
mv ~/host-runner ~/.local/bin/host-runner
ln -sf host-runner ~/.local/bin/hub-mcp-bridge
hash -r
which host-runner hub-mcp-bridge   # both resolve under ~/.local/bin
```

If `~/.local/bin` isn't on your PATH, add
`export PATH="$HOME/.local/bin:$PATH"` to your shell rc.

### 5.2 Issue a host token

```bash
/tmp/hub-server tokens issue -data ~/hub-dev \
  -kind host -team default -role host
```

Copy the printed token.

### 5.3 Run host-runner inside tmux

```bash
tmux new -s work     # or attach an existing session
host-runner run \
  --hub   http://127.0.0.1:8443 \
  --team  default \
  --token <host-token> \
  --tmux-session "$(tmux display-message -p '#S')"
```

Detach with `Ctrl-b d`. The runner heartbeats every 10s and polls for
spawns every 3s. On the phone, the **Hosts** tab now shows your
laptop with a green "online" pill within ~10s.

For a longer overview (Track B systemd unit, A2A relay flags, etc.)
see [`install-host-runner.md`](install-host-runner.md).

---

## 6. Mock-trainer harness

The dress-rehearsal pipeline lets you exercise project → runs →
sparkline → review without a GPU. Drives the same code paths a real
trainer would hit.

```bash
# Restart host-runner with --trackio-dir pointing at where mock-trainer
# will write:
mkdir -p ~/trackio
tmux kill-session -t work 2>/dev/null
tmux new -d -s work "host-runner run \
  --hub http://127.0.0.1:8443 --team default --token <host-token> \
  --tmux-session work --trackio-dir ~/trackio"

# Create a project + run, then run mock-trainer. Full recipe with
# environment variables and the curl POSTs to create project + run
# rows is in run-the-demo.md §B.5–B.7. Quick version:
~/mock-trainer --vendor trackio --dir ~/trackio \
  --project mock-live --run run-1 \
  --size 384 --optimizer lion --iters 1000 --interval-ms 500
```

The full recipe (including `seed-demo`, ablation-sweep loop, and the
wandb variant) is in [`run-the-demo.md`](run-the-demo.md). Use that
when you need a complete worked example.

---

## 7. Engine credentials

Spawned agents need a real engine on PATH. Each vendor has its own
auth flow:

| Engine | CLI | Auth |
|--------|-----|------|
| Claude Code | `claude` | `claude login` (interactive) |
| Codex CLI | `codex` | `codex login` |
| Gemini CLI | `gemini` | `gcloud auth login` |

The CLIs and their credentials live in your **host-user** environment
— the host-runner inherits them when it spawns the agent. Never
commit credentials to the repo; never paste them into hub config.

For local development, install at least one engine CLI (Claude Code is
the default in `templates/agents/steward.v1.yaml`) and confirm a bare
invocation works before spawning an agent:

```bash
claude --version
echo 'hello' | claude   # reaches the API
```

---

## 8. Making a change

```bash
git checkout -b fix/<short-description>
# edit files
flutter analyze              # static check (must be clean)
flutter test                 # unit + widget tests
scripts/lint-docs.sh         # if you touched docs/
scripts/lint-glossary.sh     # if you touched docs/
cd hub && go test ./... && go vet ./...   # if you touched hub/
```

See [`run-tests.md`](run-tests.md) for the full test surface and CI
parity table.

Verify the change end-to-end on a device or emulator before opening
the PR. If the change is mobile-side, use `flutter run` with hot reload.

Bump the version when shipping a release. From the repo root:

```bash
make bump VERSION=1.0.NNN-alpha
```

This sed-edits `pubspec.yaml` and `hub/internal/buildinfo/buildinfo.go`
to match. Doc-only commits skip the bump.

Commit + push:

```bash
git add -A
git commit -m "<type>(<scope>): <subject>"   # see CONTRIBUTING.md
git push -u origin fix/<short-description>
gh pr create
```

The PR template at `.github/pull_request_template.md` walks the rest.

---

## 9. Common issues + fixes

**`flutter pub get` fails with version-solving error.** Likely a stale
`.dart_tool/` after a pubspec change. Run `flutter clean && flutter
pub get`.

**Mobile app shows blank Projects tab after first connect.** The
HubSnapshotCache may be empty until the first server fetch lands. Pull
to refresh, or check the Activity tab for an `audit_events` row
confirming the connection.

**Hub `init` fails with `sqlite: unable to open database file`.** The
data root path doesn't exist or isn't writable by the running user.
`mkdir -p ~/hub-dev` and re-run.

**`hub-server tokens …` fails with the same sqlite error.** You're
likely running as a different user than the one that owns the data
root. Match users (or use `sudo -u` per the production guide if the
data root is owned by `termipod-hub`).

**`flutter analyze` reports infos you didn't add.** CI runs
`flutter analyze --no-fatal-infos`; infos don't block but warnings and
errors do. Fix anything you introduced.

**Mobile can't reach hub on `localhost`.** Real devices and emulators
don't share the host's loopback. Use the host's LAN IP instead.

**Engine CLI not found when agent spawns.** The host-runner inherits
PATH from the user that started it. Confirm `which claude` (or
`codex`, `gemini`) resolves under that user's environment.

**Steward FAB returns `202 pending_approval`.** Spawn is policy-gated
in `policies/default.v1.yaml`. Approve from **Me → Attention**.

**Port 8443 already in use.** Pick another port via
`-listen 127.0.0.1:18443` (avoid 80/443/8080/8443/etc. on shared
machines per project preference for uncommon ports).

---

## 10. Cleanup

```bash
# Stop host-runner
tmux kill-session -t work

# Stop hub
# Ctrl-C in the hub-server terminal, or `pkill hub-server`

# Wipe local state (everything reset; safe — no production data)
rm -rf ~/hub-dev ~/trackio
rm ~/.local/bin/host-runner ~/.local/bin/hub-mcp-bridge
```

The mobile app caches its hub config in `SharedPreferences` and the
token in the keychain — clear from **TeamSwitcher pill → Manage
profiles… → Delete** if you want a clean app state.

---

## 11. Next steps

- [`run-tests.md`](run-tests.md) — full test surface + CI parity
- [`install-hub-server.md`](install-hub-server.md) — production hub on a
  VPS with nginx + Let's Encrypt
- [`install-host-runner.md`](install-host-runner.md) — host-runner
  systemd, A2A relay, troubleshooting
- [`run-the-demo.md`](run-the-demo.md) — end-to-end demo path with
  `mock-trainer`
- [`run-lifecycle-demo.md`](run-lifecycle-demo.md) — research-lifecycle
  demo
- [`../spine/blueprint.md`](../spine/blueprint.md) — architecture
  blueprint (read before non-trivial changes)
- [`../reference/coding-conventions.md`](../reference/coding-conventions.md)
  — code style
- [`../doc-spec.md`](../doc-spec.md) — doc taxonomy + status block
  contract (any new doc must conform)

---

## 12. Cross-references

- [`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) — PR contract
- [`../../README.md`](../../README.md) — user-level project intro
- [`../../.github/pull_request_template.md`](../../.github/pull_request_template.md)
  — PR checklist
- [`../README.md`](../README.md) — docs index
