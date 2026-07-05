# 052. Breakglass terminal, two SSH paths, and a zero-knowledge key vault

> **Type:** decision
> **Status:** Accepted (2026-07-05) — resolves the key-posture fork in
> [`discussions/desktop-breakglass-ssh.md`](../discussions/desktop-breakglass-ssh.md)
> §7: the director chose **B2 (zero-knowledge encrypted vault)**. Records the
> terminal backend, the two terminal paths, the cross-device sync model, and — the
> load-bearing part — an **amendment to forbidden-pattern #15** (now in effect,
> [`spine/forbidden-patterns.md`](../spine/forbidden-patterns.md) updated) so a hub
> that holds only blind ciphertext is permitted. **D-6 (hub PTY-relay + A2A-relay
> auth) is DEFERRED** by director direction (2026-07-05); the first build is the
> **vault key-sync service** (D-4) — the hub as a blind cross-device blob store.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** v1.0.820
>
> **Implementation status (2026-07-05).** D-1 + D-2 Path 2 (personal direct
> SSH) are **built** in the desktop client: xterm.js in the webview + a `russh`
> PTY transport in the Tauri Rust core (`desktop/src-tauri/src/ssh.rs`,
> `desktop/src/surfaces/Terminal.tsx`), password / private-key auth, keys held
> in-process only. Director directed this ahead of D-4 (vault) this session;
> the vault sync (D-4), host-key pinning, and the managed-host hub-brokered PTY
> (D-2 Path 1 / D-6) remain the open workstreams.

**TL;DR.** The breakglass SSH terminal is **xterm.js + a Tauri Rust `russh`
transport** (not `libghostty` — wrong layer), mirroring mobile. There are **two
paths**: to a **host-runner-managed host** the terminal is a **hub-brokered
interactive PTY — no SSH, no keys, fully audited** (the safe primary path); to a
**personal bare-SSH host** it is direct `russh` with client-held keys. The
**whole personal setup — connections (host/user/jump/tmux) *and* keys/passphrases/
passwords — syncs across devices as one bundle** via a **zero-knowledge vault
(B2)**: the hub stores it **encrypted client-side under a key it never sees**,
wrapped per enrolled device, plus a **recovery envelope** (the vault key wrapped
under a director-escrowed recovery code) so losing every device is survivable — a
blind ciphertext blob store throughout. This requires
**amending forbidden-pattern #15**: the rule forbids the hub holding *usable* SSH
secrets; a vault the hub cannot decrypt or use preserves the rule's intent (the hub
can never authenticate as the user) and is permitted. Two security fixes travel with
it: **authenticate the A2A relay**, and **retire the cleartext backup** (the vault
subsumes it).

## Context

ADR-051 fixed the desktop client stack (Tauri + React, Rust core with keychain).
The director then directed an SSH connect function mirroring mobile, **sharing the
same keys/host info**, with **the hub brokering it safely**. The discussion doc
grounded the state of play and surfaced a hard constraint: the hub holds **zero SSH
secret today, actively enforced** — `validateSSHHint` rejects any hint carrying
`password/private_key/passphrase/secret/token` with HTTP 400
(`hub/internal/server/handlers_hosts.go:59-86`), the code embodiment of
**forbidden-pattern #15** ("Only non-secret `ssh_hint_json` … may live in the hub.
Secrets stay in the phone's secure storage.", `docs/spine/forbidden-patterns.md:92`).
Mobile SSH is fully client-side (`dartssh2` + headless `xterm`), keys are device-local
(`flutter_secure_storage`), and the only off-device path is a **cleartext** backup
(`lib/services/data_port_service.dart:87-126`). There is no hub PTY-stream primitive,
and the A2A relay is unauthenticated (`hub/internal/server/tunnel_a2a.go:256`,
mounted outside auth at `server.go:369`).

Presented the three key postures (B1 enroll-per-device / B2 zero-knowledge vault /
B3 SSH-CA), the director chose **B2**. B2 necessarily means the hub *stores*
encrypted key material — which the letter of forbidden-pattern #15 forbids. This ADR
reconciles that by amending the rule to match its intent.

## Decision

- **D-1 — Backend: xterm.js + Tauri `russh`, not `libghostty`.** The renderer is
  **xterm.js** (the reference implementation mobile's Dart `xterm` was ported from —
  matching VT semantics, themed by the ADR-051 shared-token pipeline). The SSH
  transport is **`russh`** (pure-Rust, no-cgo) in the Tauri Rust core, which opens
  the PTY and pipes bytes over IPC to xterm.js, keyed from the OS keychain.
  `libghostty` is rejected: it is a *renderer, not an SSH client*, and paints to a
  native GPU surface, not our React webview.

- **D-2 — Two terminal paths.** The target dictates the transport:
  - **Managed host (host-runner present) → a hub-brokered interactive PTY.** The
    host-runner opens a shell PTY (it already owns tmux panes) and relays it through
    the hub, authorized by the director's bearer token and written to
    `audit_events`. **No SSH, no keys, works through NAT and in the browser build.**
    This is the **primary** breakglass path.
  - **Personal bare-SSH host (no host-runner) → direct `russh`.** Client-held keys,
    mirroring mobile. The hub's role is limited to the **non-secret** `ssh_hint`.

- **D-3 — The vault carries the *whole* connection bundle, hub-blind (revised
  2026-07-05).** So a new device is directly reusable, the vault seals the complete
  personal setup — **connection bookmarks (host, port, username, auth method, jump
  host, tmux path) *and* the keys/passphrases/passwords** — as one client-encrypted
  bundle. Personal connection metadata is therefore carried **inside** the
  zero-knowledge vault (the hub never sees it, not even hostnames), **not** as
  hub-visible personal `ssh_hint`. The team-host `ssh_hint` primitive keeps its
  original, separate purpose (binding/displaying a *team* host row, non-secret,
  hub-visible). This supersedes the earlier "non-secret metadata syncs via
  `ssh_hint`" framing and also retires the smell of serializing `proxyPassword` into
  unencrypted prefs (`connection_provider.dart:138`).

- **D-4 — The connection+key bundle shares via a zero-knowledge vault (B2).** The
  hub stores the sealed bundle (D-3: connections + private keys + passphrases +
  passwords) **encrypted client-side under a vault key the hub never sees**, and
  serves it as an opaque, versioned blob — a **blind blob store**. The crypto shape:
  - Each secret is sealed with **authenticated encryption** (an AEAD such as
    XChaCha20-Poly1305 / AES-256-GCM) under a symmetric **vault key**.
  - The vault key is **wrapped per enrolled device** (envelope encryption to each
    device's public key held in that device's OS keychain / `flutter_secure_storage`),
    and/or derived from a director **passphrase** via a strong KDF (Argon2id) or a
    **passkey/WebAuthn-PRF** wrapping key. The hub stores the sealed secrets + the
    per-device wrapped-key envelopes; it holds **neither** the vault key **nor** any
    plaintext.
  - **Enrolling a new device** = an already-enrolled device wraps the vault key to
    the new device's public key after an explicit trust step (short code / passkey);
    the hub only relays the envelope. **Revoking** a device removes its envelope and
    triggers a client-side vault re-key.
  - The vault is **cross-client**: both the Flutter mobile app and the web desktop
    client are vault devices, which is how "share the same keys" is realized. Crypto
    is pure-Go on the hub side only for envelope *storage* (it never decrypts);
    sealing/opening happens on clients (Rust RustCrypto / Dart `cryptography`).
  - The vault **replaces** the cleartext `DataPortService` export as the
    cross-device mechanism.
  - **Recovery escrow (decided 2026-07-05).** The vault key is *also* wrapped under
    a director-held **recovery key** (a high-entropy recovery code, or a passphrase
    via Argon2id), stored on the hub as an opaque **recovery envelope** — so a
    principal who has lost **every** enrolled device can still recover. The director
    **escrows the recovery code offline** (self-escrow, à la a BIP-39 phrase); the
    hub holds only the wrapped envelope, never the recovery key, so zero-knowledge
    holds. On a re-key (device revocation) the client re-wraps the recovery envelope
    too. Operator/org escrow and Shamir-split recovery are possible extensions on the
    same hub slot (one opaque envelope today; a keyed table later). Without a
    recovery envelope set, losing all devices means losing the vault — the client
    prompts the director to create one at first setup.

- **D-5 — Amend forbidden-pattern #15 (the reconciliation).** The rule's intent is
  that **the hub must never be able to authenticate as the user** — never hold a
  *usable* SSH secret. A zero-knowledge vault holds only ciphertext the hub **cannot
  decrypt or use**, so the intent is fully preserved. Amend #15 to read: *"The hub
  must not store SSH secrets it can read or use. Non-secret `ssh_hint_json`
  (hostname, port, username) may live in the hub. Private keys, passphrases, and
  passwords may be stored **only** as client-side-encrypted, zero-knowledge vault
  ciphertext the hub cannot decrypt (the hub never holds the vault key or any
  plaintext); everything else stays in device secure storage."* The vault is
  key-material-scale, not bulk bytes, so forbidden-pattern #1 is not engaged.
  **Applied 2026-07-05 on acceptance:** `docs/spine/forbidden-patterns.md` #15 now
  carries this text; the zero-knowledge vault store (D-4) is thereby permitted.

- **D-6 — Hub workstream: authenticate the relay + a streaming PTY channel.**
  **DEFERRED (2026-07-05, director direction)** — tracked, not scheduled; the
  managed-host PTY path (D-2 Path 1) waits on it, and personal direct-SSH (Path 2)
  plus the vault sync (D-4) ship first. Path 1
  needs a hub PTY-relay that does not exist today. Build: (a) a host-runner
  "open interactive shell PTY" capability, and (b) an **authenticated, streaming**
  channel — either upgrade the A2A tunnel's `Kind` to carry a bidirectional PTY
  stream, or add a dedicated authed `…/hosts/{host}/shell` (WebSocket) serviced over
  the host-runner tunnel. **Authenticating the A2A relay** (`handleRelay`, currently
  token-less and mounted outside auth) is a prerequisite and is owed regardless.

## Consequences

**Easier / unlocked:**
- Keys and host info follow the director across phone and desktop, **without the hub
  ever being able to read them** — the directive satisfied and the invariant intact.
- The primary breakglass path needs no keys at all and is fully audited, so most
  terminal access is governed and NAT/browser-friendly.
- The cleartext backup file — a standing security smell — is retired.
- The A2A relay gets authenticated, closing a pre-existing gap.

**Harder / cost:**
- Real client-side crypto and a **device-enrollment / re-key / recovery** flow
  (trust a new device, revoke one, re-wrap the recovery envelope on re-key) on
  **both** clients — the highest-risk new surface. Recovery is now designed (D-4
  escrow), so "lose all devices ⇒ lost vault" is survivable *when the director set a
  recovery code*; the client must make first-setup recovery prominent so it isn't
  skipped.
- A new **hub PTY-relay + streaming-auth** workstream (D-6), plus mobile joining the
  vault — this spans hub, desktop, and mobile.
- Amending a foundational axiom (D-5); done deliberately and narrowly.

**Unaffected:**
- The data-ownership law's spirit (hub = names + events + now *opaque* secrets it
  cannot use; hosts = bytes). `hub-tui/` is orthogonal.

## Alternatives considered

- **B1 — enroll per device (no key sync).** Zero new secret-handling, safest, but
  does not satisfy "share the same keys." Kept as the trivial fallback if the vault
  crypto slips.
- **B3 — SSH certificate authority / short-lived certs.** The strongest posture and
  a natural **end-state for host-runner-managed hosts** (the runner can automate CA
  trust), eliminating long-lived keys entirely. Not chosen now (it changes host
  config and is heavier); **B2 now, B3 later for managed hosts** remains the intended
  trajectory, and Path 1 (D-2) already avoids keys for managed hosts in the interim.
- **Store keys in the hub in plaintext / hub-readable form.** Rejected — the exact
  thing forbidden-pattern #15 exists to prevent; the vault's whole point is that the
  hub cannot read it.
- **`libghostty` renderer / native terminal window.** Rejected (D-1): wrong layer,
  and a native window breaks the one-web-runtime + shared-token story for an
  occasional-use surface.

## References

- Discussion: [`desktop-breakglass-ssh.md`](../discussions/desktop-breakglass-ssh.md)
  (the grounded backend + two-path + posture analysis this ADR resolves).
- Amends: [`spine/forbidden-patterns.md`](../spine/forbidden-patterns.md) #15 (on
  acceptance) — the zero-knowledge carve-out.
- Builds on: [ADR-051](051-desktop-client-stack.md) (Tauri Rust core + keychain +
  shared tokens) and [ADR-050](050-desktop-workbench-delivery-model.md) (the
  breakglass layer as the drop-to-metal complement).
- Plan: [`plans/desktop-control-plane.md`](../plans/desktop-control-plane.md) WS8
  (client terminal) + the new hub PTY-relay workstream (D-6).
- Axiom: [`spine/blueprint.md`](../spine/blueprint.md) — the data-ownership law this
  amendment refines.
