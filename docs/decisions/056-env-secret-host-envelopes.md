# 056. Env-profile secrets — host-sealed envelopes (E3)

> **Type:** decision
> **Status:** Accepted (2026-07-28) — director approval after wedge E3 of
> [`plans/env-profiles-and-session-teleport.md`](../plans/env-profiles-and-session-teleport.md)
> shipped (#404–#414) and was implementation-reviewed (fixes `88a71b5a`
> env_vars-shadowing, `f1d63f48`/`cc8b3b7e` re-trust flow, `eb8fb2c4`
> same-host envelope replay on resume per D-3). Extends
> [ADR-052](052-breakglass-ssh-and-key-vault.md)'s per-device vault
> wrapping (D-4) to **hosts**, under the amended forbidden-pattern #15
> (ADR-052 D-5).
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `eb8fb2c4` (E3 shipped end-to-end:
> seal in all three clients, hub 422 gate, host-side injection)

**TL;DR.** An env profile's `secret_refs` point at zero-knowledge vault items;
the hub must never hold the values in usable form. E3 delivers them as
**host-sealed envelopes**: each host-runner generates an **X25519 identity
keypair** (private key in `StateDir`, never leaves the host; public key rides
`capabilities_json`); the **client** — the only party holding the vault key —
resolves the refs and seals `{KEY: value}` to the **pinned** target-host key
using the **same sealed-box construction the vault already ships** (X25519 +
AES-256-GCM; `vault-core` Rust on desktop, Dart `cryptography` on mobile); the
hub stores and forwards **opaque ciphertext** on the spawn row; the host-runner
unseals at launch and injects via **real process environment only — never the
launch command string** (the E1b `envExportPrefix` path is explicitly forbidden
for secrets: it lands values in `ps`, tmux scrollback, and the hub-visible
spec). Host keys are **trusted explicitly, never silently**: first sight of a
host key requires an operator fingerprint confirmation (short-code shown on the
host console vs. the client), and the pin lives **inside the zero-knowledge
vault bundle** — hub-relayed keys with hub-stored pins would let the hub
substitute its own key and open every envelope. Envelope AAD binds
`team|host_id|profile`, so the hub cannot re-target ciphertext to a host it
controls. Teleport of a secret-bearing session requires the initiating client
to **re-resolve and re-seal** to the target host — headless teleport of
secret-bearing sessions is impossible **by design**.

## Context

- E1a–E2c are on main: the `env_profiles` entity, plain `env_vars`
  materialization into the rendered `spawn_spec_yaml` (snapshot semantics),
  `setup_script` execution, attach points and management UI on both clients.
  `secret_refs` exist in the schema but are inert: the materializer skips
  them (`env_profile_materialize.go`) and a spawn from a secret-bearing
  profile warns loudly.
- The vault threat model (ADR-052 D-3/D-4, forbidden-pattern #15 as amended
  by D-5) fixes the constraint: the hub may carry **ciphertext it cannot
  decrypt**, never usable secrets. Vault sealing/opening happens on clients
  only — Rust `vault-core` (x25519-dalek + aes-gcm, compiled to WASM for the
  Electron shell) and Dart `cryptography` (X25519 sealed box,
  `lib/services/vault/vault_crypto.dart`).
- The plan (§"Secret delivery", items 1–3) sketches the mechanism and names
  the two open design duties this ADR discharges: the **host-key trust
  step** (the #365 review amendment: without explicit pinning, hub-relayed
  host keys reduce hub-blindness to nominal) and the concrete
  **envelope/injection format**.
- Shipped-code fact that shapes D-5: E1b injects plain env by splicing
  `export K=V && …` into the single shared launch chokepoint (`cd <wd> &&
  export … && <cmd>`), with the gemini exec-per-turn path using the driver's
  `Env` slice. The export-prefix path is *correct for hub-visible plain
  vars* and **unacceptable for secrets** — the string is stored in the spec,
  visible in `ps`/`tmux` history, and replayed on restarts.

## Decision

- **D-1 — Host identity keypair.** At first registration the host-runner
  generates an **X25519 keypair**. The private key is written to
  `StateDir` (0600, beside the cached `host_id`) and **never leaves the
  host**; the public key + a `env_envelope_v: 1` capability ride
  `capabilities_json` (the existing registration payload). No `StateDir`
  (ephemeral host-runner) → no key → the host advertises no envelope
  support and secret-bearing spawns to it are rejected (D-4). Deleting the
  key file or explicit `--rekey` regenerates; a changed key is a **new
  identity** (D-2).

- **D-2 — Explicit trust, pinned in the vault — never silent TOFU.** Before
  a client will seal to a host key it must be **operator-confirmed**: the
  client shows the key fingerprint as a short code; the host-runner prints
  the same short code on its console at startup/registration (the ready-
  banner idiom); the operator compares and confirms once. The pin
  `{host_id → pubkey}` is stored **inside the zero-knowledge vault bundle**
  (it syncs across enrolled devices via ADR-052 D-4 and is invisible to the
  hub). A key mismatch **hard-fails sealing** with a re-trust prompt; re-key
  ⇒ re-trust. Rationale (the #365 amendment, now normative): host pubkeys
  are hub-relayed; with silent TOFU or hub-side pins, a malicious hub
  substitutes its own key at first sight — or resets the pin — and opens
  every envelope. Pins the hub cannot read or write are the property that
  makes hub-blindness real rather than nominal.

- **D-3 — Envelope format v1.** The client resolves `secret_refs` from the
  vault, builds the `{KEY: value}` map, and seals it with the **same
  sealed-box construction the vault already uses** (ephemeral X25519 →
  shared secret → AES-256-GCM; no new cryptography). The AEAD **AAD binds
  context**: `"tp-env1" | team_id | target host_id | env_profile_id`. The
  envelope `{v:1, host_id, profile_id, epk, nonce, ct}` is stored on the
  spawn row as `env_secret_envelope` — opaque to the hub (the ADR-052 D-5
  pattern). The host-runner **refuses an envelope whose AAD `host_id` is
  not its own** — the hub cannot re-target ciphertext to a host it
  controls. (Replay of the envelope to *another spawn on the same host* is
  accepted: same key, same trust domain — it enables client-free restarts,
  D-5, and confers no new access.)

- **D-4 — Hub enforces presence, verifies nothing else.** A spawn whose
  resolved profile carries `secret_refs` **must** carry
  `env_secret_envelope`; otherwise the hub rejects (422, typed error
  telling the caller a resolving client must perform the spawn). This flips
  E1b's loud warning. Consequence, accepted: **headless/API spawns of
  secret-bearing profiles fail by design** — only a party holding the vault
  key can produce the envelope. The hub never validates or opens ciphertext
  (it cannot); malformed envelopes surface host-side as spawn failure with
  a present/absent-grade error, never contents.

- **D-5 — Host-side unseal + injection: process env only, never the command
  string.** The host-runner unseals at launch, in memory. Injection:
  - M1/M2 and every exec-per-turn driver: the child's **`Env` slice**
    (the E1b gemini path generalized).
  - M4 tmux panes: `tmux new-session/new-window/split-window -e K=V`
    (tmux ≥ 3.2), or session-scoped `set-environment` immediately before
    the respawn on older tmux — never the pane command string.
  - **Forbidden:** the `envExportPrefix` shell-string path, writing values
    into `spawn_spec_yaml`, temp files, or logs. Logging is
    present/absent-only (`auditAuthEnv` idiom: key names, never values).
  - Merge order (plan, unchanged): profile `env_vars` < template < engine
    < **sealed secrets win**.
  - Values are scrubbed from the runner's memory after the child starts;
    same-user visibility via `/proc/<pid>/environ` is in-trust-domain and
    accepted (any same-user process could read the child anyway).

- **D-6 — Teleport re-seal.** Envelopes are host-bound (D-3 AAD + key), so
  `POST …/teleport` of a secret-bearing session requires the **initiating
  client** to re-resolve the refs and seal a fresh envelope to the
  **target** host's pinned key as part of the teleport request. No client
  with the vault key present ⇒ teleport refused with a typed error.
  Headless teleport of secret-bearing sessions is impossible **by design**
  (already an accepted #365 review anchor; this ADR makes it normative).

- **D-7 — Lifecycle: snapshot, rotation, revocation.** The envelope is
  fixed at spawn (snapshot semantics matching E1b's materialization):
  later edits to the profile or vault item affect **future spawns only**;
  restarts reuse the stored envelope (D-3). Host re-key invalidates the
  pin and every stored envelope for that host — affected sessions need a
  re-seal (client prompt) or respawn. There is **no remote revocation of a
  delivered secret** — once injected, revocation means rotating the secret
  at its provider; the ADR records this honestly rather than pretending
  scrubbing achieves it.

**Non-goals.** E4 network policy (separate wedge); per-secret host ACLs
(any host the operator explicitly trusted may receive any secret the
operator seals to it — the trust step is the gate); hub-side audit beyond
envelope presence; multi-recipient envelopes (one spawn, one host).

## Consequences

- Go gains the sealed-box **open** side host-side and nothing hub-side
  (`golang.org/x/crypto/curve25519` + stdlib AES-GCM; the hub only stores
  bytes). Envelope test vectors must be produced by `vault-core` **and**
  Dart `vault_crypto` and opened by the Go implementation in CI — three
  implementations, one construction, cross-checked (the producer→consumer
  round-trip discipline E1b already set).
- Both clients grow: secret-ref resolution at spawn/teleport time, the
  trust-confirmation dialog (short-code compare), and the vault-bundle
  schema gains the pinned-host-keys map (versioned blob, existing sync).
- Host-runner grows: keypair + fingerprint banner, unseal path, the two
  injection mechanisms, `--rekey`.
- UX cost, accepted: first secret-bearing spawn to a new host requires a
  one-time operator confirmation; headless automation cannot spawn
  secret-bearing profiles.

## Alternatives considered

- **Hub-side sealing** (client uploads plaintext once, hub seals per host):
  hub sees values — violates forbidden-pattern #15's intent outright.
- **Host as an enrolled vault device** (host fetches from the vault
  directly): grants the host the whole bundle (connections, SSH keys) when
  it needs five env values — scope violation; also makes every host a
  vault-recovery attack surface.
- **Just-in-time delivery over a client↔host channel at every launch**: no
  stored ciphertext, but requires an online, connected client for every
  restart; envelopes give client-free restarts with the same blindness.
- **Silent TOFU on host keys**: rejected — hub-relayed keys make first-sight
  substitution trivial; nominal blindness only (the #365 amendment).
- **Hub-stored pins**: rejected for the same reason — the pin store must be
  unwritable by the party being defended against.
- **age/SOPS envelopes**: same primitive with an extra dependency and no
  AAD context binding; the vault's existing construction is already in the
  repo twice.

## References

- [ADR-052](052-breakglass-ssh-and-key-vault.md) — D-3/D-4 vault crypto
  shape + device trust step; D-5 forbidden-pattern #15 amendment.
- [`plans/env-profiles-and-session-teleport.md`](../plans/env-profiles-and-session-teleport.md)
  — E3 wedge + "Secret delivery" §1–3; teleport Part 2 (re-seal anchor).
- E1b record (issue #397) — materialization, `envExportPrefix`, merge
  order, producer→consumer round-trip tests.
- `desktop/vault-core` (x25519-dalek + aes-gcm), `desktop/vault-wasm`,
  `lib/services/vault/vault_crypto.dart` — the existing sealed-box
  implementations this ADR reuses.
