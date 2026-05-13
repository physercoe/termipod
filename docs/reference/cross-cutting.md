# Cross-cutting concerns

> **Type:** reference
> **Status:** Current (2026-05-13)
> **Audience:** contributors
> **Last verified vs code:** v1.0.547

**TL;DR.** One umbrella doc for the architecture-spanning concerns
that touch every container — security boundaries, observability,
error handling, performance budgets, offline guarantees, i18n,
accessibility. Per-topic detail stays in the canonical sibling refs;
this doc links them up under one view (arc42 §8).

This doc is **summary + cross-link**, not a re-derivation of the
sibling refs. If a section here disagrees with a sibling, the sibling
wins — file an issue and bring this doc in line.

---

## 1. Security boundaries

The system has four security boundaries:

| Boundary | Mechanism | Detail |
|---|---|---|
| Director ↔ Hub | Bearer token (per device) | [`permission-model.md`](permission-model.md), [`api-overview.md §2`](api-overview.md) |
| Hub ↔ Host-runner | Bearer token (per host-runner instance) + Tailnet | [ADR-018](../decisions/018-tailnet-deployment-assumption.md) |
| Host-runner ↔ Agent | Local UDS / stdio + per-spawn MCP token | [ADR-002](../decisions/002-mcp-consolidation.md), [ADR-016](../decisions/016-subagent-scope-manifest.md) |
| Agent ↔ Engine vendor | Vendor's own auth | engine docs |

### 1.1 Token model

- **Storage.** Tokens are SHA-256 hashed in `auth_tokens.token_hash`;
  plaintext leaves the issuer once. Mobile keeps plaintext in
  `flutter_secure_storage` (OS keychain).
- **Kinds.** `owner` / `user` / `agent` / `host` —
  see [`api-overview.md §2`](api-overview.md).
- **Scope.** Each kind binds to a different capability set; the
  middleware resolves token → actor → role → capability per request.
- **Rotation.** No `tokens rotate` yet; issue a new token, swap, let
  the old one age out (revoke via `POST /auth-tokens/{id}/revoke`).

### 1.2 Secret storage

| Secret | Where | Mechanism |
|---|---|---|
| Bearer tokens (mobile) | Device keychain | `flutter_secure_storage` |
| SSH private keys | Device keychain | `flutter_secure_storage` |
| SSH passphrases | Device keychain | `flutter_secure_storage` |
| Hub-side token hashes | `hub.db` | SHA-256 |
| Engine-vendor API keys | Host environment | inherited by host-runner / agent |
| TLS material | Filesystem (Let's Encrypt) | unix permissions; nginx-managed |

The mobile app **never** uploads device-only secrets. The user can
opt into a backup export ([`feedback_storage_layering.md`](../../.claude/)
in memory; user pattern); the export is plaintext JSON and the app
warns explicitly before producing one.

### 1.3 Network boundaries

- **Tailnet assumption** ([ADR-018](../decisions/018-tailnet-deployment-assumption.md)).
  The hub ↔ host-runner edge is assumed to ride a private overlay
  (Tailscale, ZeroTier, Nebula, …) or LAN. The shipped nginx config
  terminates TLS for the public mobile-↔-hub edge, then proxies
  cleartext to `127.0.0.1:8443`; the host-runners reach the hub via
  the public URL but with a host-kind token.
- **Public URL rewrite** ([ADR-003](../decisions/003-a2a-relay-required.md)).
  When NAT'd hosts publish A2A agent-cards, the hub rewrites each
  card's `url` field to `<public-url>/a2a/relay/<host>/<agent>` so
  off-box peers dial the hub relay.
- **Self-signed TLS rejected.** `dart:io.HttpClient` rejects invalid
  certs; use Let's Encrypt or a plain `http://` LAN URL.

### 1.4 Agent sandboxing

Bounded sandboxing (bwrap / seatbelt / microVM / egress proxy) is
**post-MVP** — see memory `project_post_mvp_sandbox`. Today the
host-runner runs as the SSH user; agents run with that user's
filesystem + network access, gated only by `tool_allowlist` declared
in their template and the host-runner's MCP gateway scope.

### 1.5 Subagent scope manifest

Per-role scope is declared in `roles.yaml` and enforced by the
host-runner's MCP middleware ([ADR-016](../decisions/016-subagent-scope-manifest.md)).
Steward roles can call `hub://*` tools that author plans, spawn,
review; worker roles can read state and emit artifacts but not author
templates or spawn peers (the manager/IC invariant).

---

## 2. Observability

The system's primary observability surface is *user-facing*:
`audit_events` is what the director sees on the Activity tab and what
on-call queries when triaging.

### 2.1 Audit events

All hub mutations emit one or more `audit_events` rows — see
[`audit-events.md`](audit-events.md) for the schema and the canonical
event-kind taxonomy. The Activity tab is a read of this table; the
StewardBadge fires on rows where
`actor_kind='agent' AND actor_handle='steward'`. The `MCP get_audit`
tool lets agents read the timeline.

### 2.2 Logs

Hub and host-runner emit structured logs to stderr (JSON lines under
systemd; pretty-printed in dev). Today there's no centralised
collector — operators read systemd journal or tail the file. The
log lines mirror `audit_events` for hot paths so most diagnosis can
happen from the audit feed alone.

### 2.3 Metrics

The hub exposes basic process metrics on a sidecar admin port (TBD —
not part of MVP). Agent + host-runner heartbeats include a `last_seen_at`
timestamp, surfaced in the Hosts tab as the "online" pill.

### 2.4 Tracing

No distributed tracing in MVP. Cross-component flows can be
reconstructed from audit events by `correlation_id` —
see [`../spine/system-flows.md`](../spine/system-flows.md) for how
each cross-component flow chains audit rows.

---

## 3. Error handling

### 3.1 Wire format

Errors over HTTP follow RFC 7807 problem-detail. Schema in
[`api-overview.md §4.2`](api-overview.md):

```json
{ "type": "https://termipod/errors/<slug>",
  "title": "<short>", "status": 409,
  "detail": "<actionable description>",
  "audit_event_id": "ae-...",
  "context": { ... } }
```

Status code policy is canonical there.

### 3.2 Retry strategy

- **Idempotency keys** are required for any host-initiated POST (see
  [`api-overview.md §4.3`](api-overview.md)). Without one, retries
  risk double-mutation.
- **Exponential backoff** for transient failures: 1s, 2s, 4s, 8s,
  capped at 30s. Mobile applies this for revalidation; host-runner
  applies it for hub heartbeats.
- **Circuit breaker.** None today. Long-running disconnects degrade
  to cached reads on mobile and buffered writes on host-runner.

### 3.3 User-facing surfaces

- **Inline banners** in the affected screen for soft errors (rate
  limited, stale cache, slow network).
- **Toasts** for transient successes / acknowledgements.
- **Modal dialogs** for hard errors that need confirmation.
- **Activity feed entries** for any state change the director should
  notice asynchronously; severity is mapped to a chip color (info,
  minor, major).

### 3.4 Crash + recovery

- **Hub crash.** Restart applies pending migrations; the append-only
  event log lets `hub-server reconstruct-db` rebuild the SQLite DB
  from JSONL if the file is corrupted.
- **Host-runner crash.** No persistent local state; on restart, pass
  `--host-id` to skip re-registration and resume heartbeating.
  In-flight agents reattach to their tmux panes.
- **Mobile crash.** The cache is unaffected; reopen the app and
  resume. SSE streams reconnect with `Last-Event-ID`.

---

## 4. Performance budgets

Quantified targets for the MVP demo (see also `quality-attributes.md`,
P2.10 in [`../plans/doc-uplift.md`](../plans/doc-uplift.md)):

| Surface | Budget | Mechanism |
|---|---|---|
| Mobile cold-start | ≤ 200 ms to first paint with cache | [ADR-006](../decisions/006-cache-first-cold-start.md) cache-first |
| Mobile list re-render | ≤ 50 ms for 100-row list | Sliver-based incremental rendering |
| SSE reconnect | < 1 s under network blips | `Last-Event-ID` + 30 s server-side buffer |
| Hub p50 list endpoint | < 100 ms (cached) | Indexes per [`database-schema.md §4`](database-schema.md) |
| Hub p99 list endpoint | < 500 ms | Same |
| Heartbeat interval | 10 s | host-runner constant |
| Spawn poll interval | 3 s | host-runner constant |
| Run-metric digest size | ≤ 100 points per metric | host-runner downsampler |

### 4.1 Cache-first cold start

[ADR-006](../decisions/006-cache-first-cold-start.md) is load-bearing.
Mobile reads `HubSnapshotCache` immediately on launch, renders, then
revalidates over the network with `If-None-Match`. The user sees the
last-known-good world before the network responds.

### 4.2 Pagination defaults

Default page size is 50 rows; max 200. Cursor-based; the cursor is
opaque to the client. See [`api-overview.md §4.4`](api-overview.md).

---

## 5. Offline guarantees

The mobile app is **read-mostly when offline**: cached endpoints serve
last-known-good data. Mutations are not queued; the app surfaces a
banner explaining the hub is unreachable.

| Action | Offline behavior |
|---|---|
| View Projects / Activity / Hosts / Me | Cached snapshot served |
| Open agent transcript | Cache miss → "Hub unreachable" |
| Approve / decide attention | Blocked; retry when online |
| Spawn agent | Blocked; retry when online |
| File local note (`notes.db`) | Works offline |

Host-runner, in contrast, **buffers writes** during hub outages —
heartbeats queue, audit events buffer locally, agents continue running
under the host-runner's authority. When the hub comes back, the
host-runner replays buffered writes in order.

Conflict resolution is *avoided by design*: writes go through the hub
(authoritative), reads can stale-serve, and cache is invalidated on
write. There is no client-side "last write wins" merging.

---

## 6. Internationalization

Mobile strings live under `lib/l10n/` per Flutter's standard
ARB-file pipeline:

```
lib/l10n/
├── app_en.arb       # canonical English source
└── app_zh.arb       # Simplified Chinese
```

Configuration in `l10n.yaml`. Strings are referenced via the
generated `AppLocalizations` class.

To add a locale:

1. Copy `app_en.arb` → `app_<lang>.arb`, translate values.
2. Add the locale to `MaterialApp.supportedLocales`.
3. Run `flutter gen-l10n` (auto-runs in CI).
4. Add a row to the README's locale table if user-facing.

`README.zh.md` exists; `README.ja.md` is deferred (per
`docs/plans/contributor-readiness.md §3.3`). Doc-side translation is
post-MVP.

---

## 7. Accessibility

Targets are not yet quantified; a11y audit is post-MVP. Current
patterns in the codebase:

- **Tap targets** ≥ 44 × 44 dp (Material default).
- **Color is never the only signal.** Severity chips, status pills,
  and badges pair color with an icon or label.
- **Dynamic type.** Settings exposes a font-size scale; the app
  respects it across most surfaces.
- **VoiceOver / TalkBack.** Standard widget semantics apply; bespoke
  widgets (sparklines, tile cards) need explicit `Semantics` wrappers
  — open follow-up work.
- **Dark mode** is supported and toggled in Settings.

---

## 8. Maintainability + portability

- **Code style.** [`coding-conventions.md`](coding-conventions.md);
  `flutter analyze` and `go vet` clean as the gate.
- **Doc spec.** [`../doc-spec.md`](../doc-spec.md) (every doc has a
  status block; CI lint enforces).
- **Glossary discipline.** [`glossary.md`](glossary.md) is canonical
  for project-specific terms; `lint-glossary.sh` enforces.
- **Test surface.** [`../how-to/run-tests.md`](../how-to/run-tests.md).
- **Supported platforms.** Mobile: Android primary, iOS secondary.
  Web is N/A in MVP. Hub: Linux focus (the systemd unit ships in
  `hub/deploy/systemd/`); macOS supported for development.
  Host-runner: Linux + macOS only — POSIX-only by design (`bash -c`
  agent launch, `tmux` for every pane, `syscall.SysProcAttr.Setpgid`
  for process-group cleanup). Windows is not supported and would
  not compile under `GOOS=windows`; WSL2 is the workaround. Full
  matrix in [`quality-attributes.md` §6](quality-attributes.md).

---

## 9. Cross-references

- [`architecture-overview.md`](architecture-overview.md) — C4 view
- [`database-schema.md`](database-schema.md) — physical schema
- [`api-overview.md`](api-overview.md) — HTTP surface
- [`audit-events.md`](audit-events.md) — observability surface
- [`permission-model.md`](permission-model.md) — token / actor /
  scope resolution
- [`rate-limiting.md`](rate-limiting.md) — vendor + hub-level limits
- [`attention-delivery-surfaces.md`](attention-delivery-surfaces.md)
  — attention as user-facing telemetry
- [`coding-conventions.md`](coding-conventions.md) — code style
- [`../decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache-first cold start
- [`../decisions/018-tailnet-deployment-assumption.md`](../decisions/018-tailnet-deployment-assumption.md)
  — Tailnet assumption
- [`../how-to/install-hub-server.md`](../how-to/install-hub-server.md)
  — TLS / nginx / systemd hardening
