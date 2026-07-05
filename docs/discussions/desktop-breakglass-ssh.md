# Desktop breakglass terminal — SSH backend & the shared-key / hub-safety model

> **Type:** discussion
> **Status:** Resolved (2026-07-05) → [ADR-052](../decisions/052-breakglass-ssh-and-key-vault.md).
> Director directive: the desktop client
> ([ADR-051](../decisions/051-desktop-client-stack.md)) needs an SSH connect
> function mirroring mobile, sharing the same keys/host info, with the hub
> brokering it safely. This reasons the **terminal backend** (is it `libghostty`?)
> and the **shared-key model**, which collides with an *enforced* invariant (the
> hub holds no SSH secret). The key-posture fork (§7) was resolved to **B2
> (zero-knowledge vault)**; ADR-052 records the design and amends
> forbidden-pattern #15.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** v1.0.820

**TL;DR.** The breakglass terminal is **two layers, don't conflate them**: a
terminal *emulator/renderer* and an SSH *transport*. `libghostty` is the wrong fit
for **both** reasons — it is a renderer (not an SSH client) and it paints to a
native GPU surface, not into our React webview. Mirror mobile instead:
**xterm.js** as the renderer (mobile's Dart `xterm` is a port of it) + the **Tauri
Rust core running `russh`** (pure-Rust, no-cgo) as the SSH transport, keyed from
the OS keychain. But the deeper design is that there are **two terminal paths**:
to a **hub-managed host** (running a host-runner) the safe path is a
**host-runner-brokered PTY through the hub — no SSH, no keys, fully audited, works
through NAT and in the browser build**; only **personal bare-SSH hosts** need
direct `russh` + client-held keys. On sharing keys/host-info: the hub holds **zero
SSH secret today, enforced by a denylist** — so we **sync the non-secret layer**
(host/connection metadata, extending the existing `ssh_hint`) freely, and for the
private keys pick a posture (§7): enroll-per-device, a **zero-knowledge encrypted
vault** the hub stores blind, or an **SSH-CA with short-lived certs** (the strongest,
and a natural end-state for host-runner-managed hosts). Two security fixes are owed
regardless: the A2A relay is unauthenticated, and today's backup file is cleartext.

## 1. The directive and the tension

The director wants: (a) desktop SSH mirroring mobile, (b) desktop + mobile sharing
the same keys/host info, (c) the hub handling it safely. (a) and (b) are
straightforward; (c) collides with a **load-bearing, actively-enforced invariant** —
the hub deliberately stores **no** SSH secret. Resolving the directive means honoring
that invariant, not waiving it. That is the whole design problem, and it has a good
answer.

## 2. What exists today (grounded)

- **Mobile SSH is fully client-side** — `dartssh2` 2.13.0 wrapped in `SshClient`
  (`lib/services/ssh/ssh_client.dart:228,643`), interactive PTY via
  `SSHClient.shell()`, ProxyJump (via `forwardLocal`), SOCKS5, adaptive keep-alive.
- **Terminal render = the `xterm` Dart package used headlessly** (its VT state
  machine) feeding a custom `AnsiTextView` widget (`lib/services/terminal/raw_pty_backend.dart:5,29`,
  `lib/screens/terminal/terminal_screen.dart`). The Dart `xterm` is a port of the
  web **xterm.js**.
- **Keys + connections are device-local.** Connection bookmarks in
  `SharedPreferences` (`lib/providers/connection_provider.dart:198`), key metadata
  in `SharedPreferences` (`lib/providers/key_provider.dart:122`), private
  keys/passphrases/passwords in `flutter_secure_storage`
  (`lib/services/keychain/secure_storage.dart:33,51`). **Nothing syncs to the hub.**
  The only off-device path is a **cleartext** `DataPortService` backup JSON that
  includes private keys (`lib/services/data_port_service.dart:87-126`).
- **The hub holds zero SSH secret — enforced.** A `Host` stores only
  id/team/name/status/caps/build-info + a non-secret **`ssh_hint_json`**
  (hostname/port/username/jump hint). `validateSSHHint` **rejects (HTTP 400)** any
  hint containing `password/private_key/privatekey/passphrase/secret/token`
  (`hub/internal/server/handlers_hosts.go:59-86`); the MCP tool mirrors it
  (`hub/internal/hubmcpserver/toolspec.go:257`). `host_token_hash` is the
  host-runner's own bearer token, not an SSH credential.
- **No hub PTY-stream primitive.** `GET /agents/{id}/pane` returns cached tmux
  `capture-pane` **text** snapshots, polled, read-only, agent-scoped
  (`hub/internal/server/handlers_agent_control.go:75`; host side
  `hub/internal/hostrunner/runner.go:419`). Not an interactive raw PTY.
- **A2A relay is unauthed request/response.** `handleRelay` is mounted *outside*
  auth middleware (`server.go:369`), token-less by A2A spec
  (`tunnel_a2a.go:256-259`), discrete request/response with a 20 s deadline and
  1 MiB cap over the host-runner's authed long-poll tunnel — it multiplexes traffic
  classes via a `Kind` discriminator (`tunnel_a2a.go:55-64`) but is not a byte
  stream.

## 3. The backend question — two layers, and why not `libghostty`

A terminal is **two separable layers**:

1. **Emulator / renderer** — parse the VT byte stream, maintain the screen model,
   paint it. (`libghostty`, xterm.js, the Dart `xterm` live here.)
2. **Transport** — open the connection, authenticate, allocate a PTY, move bytes.
   (`dartssh2`, `russh`, OpenSSH live here.)

**`libghostty` is the wrong fit on both counts:**
- It is a **renderer, not an SSH client** — it paints a PTY you feed it; it does no
  SSH transport, so it answers the wrong half of the question.
- It **renders to a native GPU surface** (Metal/OpenGL), not into an HTML/DOM
  webview. Our unified client (ADR-051) is React in a webview — you cannot mount
  `libghostty` in the DOM. Using it would force a **native Tauri window** for the
  terminal, breaking the one-web-runtime + shared-design-token story (ADR-051 D-2/D-4)
  for a surface used occasionally. Its embeddable C API was also still nascent in
  early 2026.

**Recommended backend — mirror mobile:**
- **Renderer = xterm.js** in the React app. It *is* the reference implementation the
  Dart `xterm` was ported from, so VT semantics match mobile, and the shared token
  pipeline (ADR-051 D-4) themes both clients identically. (Desktop can use xterm.js's
  own renderer directly rather than mobile's headless-VT-plus-`AnsiTextView` detour.)
- **Transport = the Tauri Rust core running `russh`** (pure-Rust SSH — matches the
  codebase's no-cgo ethos, unlike `libssh2` C bindings). The Rust core already holds
  secrets in the OS keychain and proxies streams (ADR-051 D-1); it opens the SSH
  channel + PTY and pipes bytes over Tauri IPC to xterm.js. `russh` covers PTY,
  agent auth, and ProxyJump-style chaining — parity with mobile's dartssh2 feature set.
- **Browser build caveat:** browsers cannot open raw TCP, so **direct** SSH to a
  personal host is **installed-app-only**. The hub-brokered path (§4 Path 1) works in
  the browser too, so browser users still get breakglass to *managed* hosts.

## 4. Two terminal paths (the crux)

The target dictates the backend — and the safe answer for most targets needs no SSH
at all:

**Path 1 — Hub-managed host (host-runner present): a host-runner-brokered PTY.**
The fleet is already host-runner-managed; the host-runner owns tmux panes and can
spawn a shell. A breakglass terminal to a managed host should be an **interactive PTY
the host-runner opens and relays through the hub**, authorized by the director's
bearer token and written to `audit_events` — **no SSH, no keys, no direct network
reachability** (works through NAT, works in the browser build). This is the *safe,
hub-native* path and should be the **primary** desktop breakglass path.
*Gap:* the hub has no PTY-streaming primitive today (§2). This path is a **hub
workstream**: (a) a host-runner "open interactive shell PTY" capability (it already
spawns panes), and (b) an **authenticated, streaming** channel — either upgrade the
A2A tunnel `Kind` to carry a bidirectional PTY stream *and authenticate the public
relay entry* (owed regardless, §6), or add a dedicated authed
`GET …/hosts/{host}/shell` (WebSocket) the host-runner services over its tunnel.

**Path 2 — Personal bare-SSH host (no host-runner): direct SSH.** The MuxPod-legacy
breakglass-to-any-box. Desktop uses Rust `russh` + keychain keys, mirroring mobile.
The hub's role is minimal and *non-secret*: store the `ssh_hint` (already exists) so
a host row binds to a connection without re-typing details; **keys stay client-side.**
This is the path that needs the shared-key story (§5).

## 5. Shared keys / host info — honoring "hub holds no secret"

Split the data by sensitivity:

- **Non-secret metadata syncs freely.** Host/connection bookmarks (hostname, port,
  user, key *fingerprint*, jump host, tmux path) are non-secret — the hub can sync
  them as **personal-scoped** metadata across the director's devices. The `ssh_hint`
  primitive already models exactly this for team hosts; extend it to a personal
  connection-bookmark sync. This delivers "share the same host info" with **no
  invariant change**. (It also lets us stop serializing `proxyPassword` into
  unencrypted prefs — a current smell, `connection_provider.dart:138`.)

- **Private keys — pick a posture (the director's fork):**
  - **B1 — Enroll per device (no key sync).** Keys never leave the device that made
    them; a new device generates its own keypair and adds its pubkey to hosts. Hub
    stores only fingerprints. Safest, least convenient; the "correct" SSH model.
  - **B2 — Zero-knowledge encrypted vault, synced through the hub.** The hub stores
    private keys **encrypted client-side** with a key it never sees (director
    passphrase / passkey / device master key); the hub is a **blind ciphertext blob
    store**. True cross-device sharing, and it honors the invariant *because* the hub
    holds only opaque bytes it cannot use — the 1Password model. This also replaces
    the current cleartext backup file. (Judgment call to confirm: is hub-stored
    *ciphertext-it-can't-read* acceptable under forbidden-pattern #15, or does the
    letter of "no key material on the hub" rule it out? — a glossary/blueprint
    question for the ADR.)
  - **B3 — SSH certificate authority / short-lived certs (strongest, hub-native
    end-state).** No long-lived keys to sync at all: the director's identity
    (passkey/SSO) → the hub (or a `step-ca`/Teleport-style CA it fronts) issues
    **short-lived SSH certs**; hosts trust the CA (`TrustedUserCAKeys`). Centrally
    brokered, auditable, revocable — the maximal "hub handles it safely." Cost: hosts
    must trust the CA; for **host-runner-managed** hosts the runner can automate that
    trust, making B3 a natural fit for the managed fleet and eliminating the shared-key
    problem there entirely.

## 6. Security fixes owed regardless of the above

- **Authenticate the A2A relay.** `handleRelay` is an unauthenticated public
  entrypoint (`server.go:369`, `tunnel_a2a.go:256`). Any hub-brokered terminal (Path 1)
  rides this transport, so it must be authenticated first — and the gap is worth
  closing on its own merits.
- **Encrypt the export/backup.** `DataPortService` writes private keys and passwords
  in cleartext (`data_port_service.dart:87-126`). The B2 vault (if chosen) subsumes
  this; otherwise the backup should be passphrase-encrypted.

## 7. Recommendation and the open fork

- **Renderer + transport:** xterm.js + Tauri/`russh`. Not `libghostty`. (§3)
- **Primary path:** Path 1 — host-runner-brokered PTY (keyless, audited) for managed
  hosts; it covers the common case and sidesteps key-sharing entirely. Path 2 (direct
  `russh`) for personal bare-SSH hosts.
- **Shared data now:** sync non-secret connection/host metadata (extend `ssh_hint`,
  personal scope).
- **Key posture — the director's decision (§5):** recommend **B2 (zero-knowledge
  vault) now** as the cross-device key-sharing mechanism *plus* **B3 (SSH-CA) as the
  long-term end-state for managed hosts**, with **B1** as the zero-effort fallback. B2
  satisfies the directive ("share the same keys, hub handles it safely") without
  breaking the no-plaintext-secret invariant.

**Open fork for the director:** which key posture — **B1 / B2 / B3** (or B2→B3
staged)? The answer sets whether the hub grows a zero-knowledge vault, an SSH-CA, or
neither, and is the trigger to promote this to an ADR.

## 8. Impact on the desktop plan

- **Client (ADR-051 / the control-plane plan):** a **breakglass-terminal workstream** —
  xterm.js pane + Rust `russh` transport + keychain, mirroring the mobile
  Connections/Keys/Terminal surfaces. Fits the plan's WS7/WS8 neighborhood; the
  Navigator's host rows gain an "open terminal" action.
- **Hub:** a **PTY-relay + auth workstream** — host-runner interactive-shell capability
  + an authenticated streaming channel (§4 Path 1) + A2A relay auth (§6). This is the
  only substantial hub change the desktop client implies, and it benefits mobile too.
- **ADR trigger:** once the key posture (§7) is picked, promote to an ADR covering the
  two paths, the metadata-sync, the chosen key model, and the relay-auth fix.

## Related

- [ADR-051](../decisions/051-desktop-client-stack.md) — the desktop client stack this
  terminal lives in (Tauri Rust core, keychain, shared tokens).
- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the breakglass
  layer is the "drop-to-metal" complement to the PI-directs-agents spine.
- [`plans/desktop-control-plane.md`](../plans/desktop-control-plane.md) — where the
  client-side terminal workstream lands.
- [`spine/blueprint.md`](../spine/blueprint.md) — the data-ownership law + the
  forbidden pattern this design must not violate.
