# Tutorial 00 — Getting started

> **Type:** tutorial
> **Status:** Current (2026-05-05)
> **Audience:** new contributors, new directors
> **Last verified vs code:** v1.0.351

**Goal.** Stand up the hub locally, point a mobile device at it,
spawn a steward, and see one director ↔ steward exchange. ~60 minutes
on a clean Linux/macOS box (excluding SDK download time).

**You'll learn:**
- How termipod's three layers (Mobile / Hub / Host-runner) connect.
- How owner / host tokens are minted and which is for which.
- What "spawning the steward" actually does.

**Prerequisites:**
- Flutter 3.24+, Go 1.23+, tmux 3.2+, an Android device or emulator
  (or iOS via sideload). Cold-start setup in
  [`../how-to/local-dev-environment.md`](../how-to/local-dev-environment.md).
- A working engine CLI on PATH: `claude` (most common), `codex`, or
  `gemini`. Run `claude --version` to check.

If you want a fully no-GPU demo path with mock training, you'll do
that in 01–02 — this tutorial gets you to "first steward online."

---

## Step 1 — Build the hub binary

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod/hub
go build -o /tmp/hub-server ./cmd/hub-server
go build -o ~/host-runner   ./cmd/host-runner
ln -sf host-runner ~/.local/bin/hub-mcp-bridge
mkdir -p ~/.local/bin && mv ~/host-runner ~/.local/bin/host-runner
```

> **Why the symlink.** The host-runner binary doubles as
> `hub-mcp-bridge` (the stdio↔HTTP shim). Spawned agents look for that
> name on PATH; without the symlink they can't reach the hub MCP
> service.

Verify:

```bash
/tmp/hub-server --help          # should print subcommands
which host-runner hub-mcp-bridge
```

---

## Step 2 — Initialize a data root

```bash
/tmp/hub-server init -data ~/hub-tut
```

Output ends with an **owner token** — this is plaintext, only printed
once, only the SHA-256 lands in the database. Copy it now.

```text
Owner token (shown once — store it in your TUI / mobile config):

  <copy-this-into-the-app>
```

> **What just happened.** `init` creates `~/hub-tut/hub.db` with a
> `default` team, a hashed owner token, and a `default/templates/`
> directory copied from the bundled embed.FS. User edits to that
> directory win on subsequent boots — `init` never overwrites.

---

## Step 3 — Serve the hub on the LAN

```bash
LAN_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || ipconfig getifaddr en0)
echo "LAN_IP=$LAN_IP"
/tmp/hub-server serve -listen 0.0.0.0:8443 -data ~/hub-tut
```

In another terminal, sanity-check from the host machine:

```bash
curl -fsS -H "Authorization: Bearer <owner-token>" \
  http://127.0.0.1:8443/v1/_info
# {"server_version":"1.0.351-alpha", ...}
```

Leave the server running.

> **Why `0.0.0.0`** — your phone needs to reach this hub from the LAN,
> so binding only to `127.0.0.1` won't work on a real device. Plain
> `http://` is fine on a trusted LAN; the mobile app rejects
> self-signed TLS but accepts `http://`.

---

## Step 4 — Issue a host token + start host-runner

In another terminal:

```bash
/tmp/hub-server tokens issue -data ~/hub-tut \
  -kind host -team default -role host
# prints: <host-token>
```

Then, inside a tmux session:

```bash
tmux new -s tut
host-runner run \
  --hub   http://127.0.0.1:8443 \
  --team  default \
  --token <host-token> \
  --tmux-session "$(tmux display-message -p '#S')"
```

You should see:

```text
INFO registered with hub host_id=host-...
INFO heartbeat ok
```

Detach with `Ctrl-b d` and leave it running. The host-runner
heartbeats every 10s and polls for spawns every 3s.

> **Why a separate token kind.** The host token can register hosts,
> heartbeat, list spawns, list commands, and patch agent rows — and
> *nothing else*. It cannot create projects or runs. The owner token
> from Step 2 has full authority.

---

## Step 5 — Install the mobile app + connect

Build the app on a connected device or emulator:

```bash
cd /path/to/termipod          # repo root
flutter pub get
flutter run                    # picks the first connected device
```

In the running app:

1. Bottom tab **Projects** → tap **Configure Hub**.
2. Fill in:
   - **Display name** — `tut`
   - **Base URL** — `http://<LAN_IP>:8443`
   - **Team ID** — `default`
   - **Bearer Token** — the **owner** token from Step 2
3. Tap **Probe URL** → green banner with hub version means reachable.
4. Tap **Save & Connect**.

You should land on an empty Projects tab. Switch to **Hosts** — your
laptop should appear with a green "online" pill within ~10 s.

---

## Step 6 — Spawn the steward

Switch to the **Me** tab. Bottom-right is the **Steward** FAB. Tap it.
A "Spawn the team steward" sheet appears with:

- Handle: `steward` (reserved)
- Kind: `claude-code` (default)
- Spec YAML: pre-filled from `agents/steward.v1.yaml`
- Host: pre-selected to your `online` host

Tap **Spawn Steward**. Within ~3 s the host-runner picks up the
pending spawn, opens a new tmux window in your `tut` session, and
launches `claude --model opus-4-7`. The steward's row in the agents
table flips to `status='running'`.

In your terminal:

```bash
tmux attach -t tut
# Ctrl-b n to next window — you'll see claude running
# Ctrl-b d to detach
```

Back on the phone, the **Steward** FAB now routes to the live steward
session (a chat-style transcript over `agent_events`).

---

## Step 7 — Talk to the steward

In the session screen, type `hello` and submit. The flow:

```
mobile  → POST /agents/{id}/input
hub     → forward to host-runner
runner  → write to claude's stdin
claude  → think + reply via stream-json
runner  → POST /agents/{id}/events  (per AG-UI kind)
hub     → SSE event
mobile  → render the reply card
```

Should take a few seconds. Congratulations — you have a working
director ↔ steward loop.

---

## What you just built

```
   ┌─────────────────┐        ┌──────────────────────┐
   │  Phone (Flutter)│ ◀────▶ │  /tmp/hub-server     │
   └─────────────────┘ HTTPS  │  ~/hub-tut data root │
                              └────────┬─────────────┘
                                       │ LAN
                              ┌────────▼─────────────┐
                              │  ~/.local/bin/       │
                              │    host-runner       │
                              │  in tmux session tut │
                              └────────┬─────────────┘
                                       │ ACP / stream-json
                              ┌────────▼─────────────┐
                              │  claude --model …    │
                              │  in tmux window      │
                              └──────────────────────┘
```

You walked the three layers from outside in. The next tutorials
extend this: 01 has you author a custom *project template* (the
recipe a steward decomposes), and 02 has you build a *worker agent*
that the steward spawns to do bounded work.

---

## Cleanup (optional)

```bash
# stop the steward agent: tap Pause on the agent's detail sheet, or
tmux kill-session -t tut

# stop the hub: Ctrl-C in its terminal

# wipe local state — safe; this is a dev hub
rm -rf ~/hub-tut
```

---

## Cross-references

- [`01-author-a-project-template.md`](01-author-a-project-template.md)
  — next up
- [`../how-to/install-hub-server.md`](../how-to/install-hub-server.md)
  — production-style hub with TLS
- [`../how-to/install-host-runner.md`](../how-to/install-host-runner.md)
  — host-runner systemd unit
- [`../how-to/local-dev-environment.md`](../how-to/local-dev-environment.md)
  — full SDK install + cold start
- [`../reference/architecture-overview.md`](../reference/architecture-overview.md)
  — what the C4 picture above looks like in detail
- [`../spine/agent-lifecycle.md`](../spine/agent-lifecycle.md) — the
  steward role you just spawned
- [`../spine/sessions.md`](../spine/sessions.md) — the conversation
  primitive you just used
