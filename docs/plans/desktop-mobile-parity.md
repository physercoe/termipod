# Desktop ↔ mobile parity — catch the web client up to the Flutter app

> **Type:** plan
> **Status:** Proposed (2026-07-06) — the desktop control plane
> ([ADR-051](../decisions/051-desktop-client-stack.md),
> [plan](desktop-control-plane.md)) shipped WS2–WS8 *first slices* and now runs
> as a signed, self-updating app. But most surfaces are read-only stubs while the
> mobile Flutter app is mature. This plan grounds the gap against the code on both
> sides and sequences the catch-up. Triggered by director review: "the UI is very
> rough; the desktop version should be at least comparable with mobile." All
> `file:line` claims verified against HEAD via a six-way survey.
> **Audience:** principal · contributors
> **Last verified vs code:** v1.0.821 (desktop-v0.2.0)

## Context

The desktop client is a three-region mission-control shell (Navigator | Focus |
Attention dock) over the hub's REST+SSE API. It can *read* the fleet, render a
(broken) transcript, decide approvals, drive agent lifecycle, run one operator
cockpit, and open a bare SSH terminal. Almost everything else the mobile app does
— rich transcripts, the digest dashboard, attachments, saved connections, a key
vault, multi-hub profiles, an offline cache, and nearly all *write* paths — is
missing or partial.

The director named six concrete gaps; each has a verified root cause below, and
they seed the phases.

## The six named gaps — root cause + fix

1. **Transcript shows only the event kind, not the content.** The hub emits
   **flat events**, one row per content block: `{seq, ts, kind, producer,
   payload}` (`hub/internal/server/handlers_agent_events.go:26-44`). The
   claude-code driver already explodes Anthropic's `content:[…]` array into one
   event per block and flattens each
   (`hub/internal/drivers/local_log_tail/claude_code/mapper.go`). The desktop's
   `eventText()` looks for `text/body/content/summary` and otherwise shows the
   kind — the generic fallback. **Fix:** dispatch on `kind` and read per-kind
   `payload` fields (mobile's dispatch is `lib/widgets/transcript/event_card.dart`):
   `text`/`thought` → `payload.text` (markdown); `tool_call` →
   `payload.name` + `payload.input` + `payload.tool_use_id`; `tool_result` →
   `payload.content` (string) + `payload.is_error` + `payload.tool_use_id`;
   `turn.result` → tokens/cost/`duration_ms`; `input.text` → `payload.text` +
   `payload.from`; `error` → `payload.error|message`; `diff`/`plan`/`session.init`
   as specialized cards. Pair `tool_call`↔`tool_result` on **`tool_use_id`** and
   build a `tool_use_id→name` map. Accent by kind
   (`lib/widgets/transcript/feed_reducer.dart:1096-1124`).

2. **Digest is raw JSON.** `GET …/agents/{id}/digest`
   (`hub/internal/server/handlers_agent_digest.go`) returns a structured rollup:
   `outcome`, `turn_count`, `event_count`, `active_ms`/`duration_ms`, `cost_usd`,
   `by_model{in,out,cache_read,cache_create,cost_usd}`, `errors{class→{count,
   sample_ordinals,sample_labels}}`, `tools{name→{calls,failed}}`, `latency{p50_ms,
   p95_ms}`. Mobile renders it as `RunReportCard`
   (`lib/widgets/run_report_card.dart`): outcome badge + one-line summary, a
   `Wrap` of stat tiles (Events/Turns/Active/Elapsed/Cost/Tools/Errors/Latency),
   a per-model token breakdown, an "as of · live/cached" footer, plus an Errors
   list (from `errors[*].sample_*`, no extra fetch). Optionally pair with
   `GET …/agents/{id}/turns` (`handlers_agent_turns.go`) for a per-turn timeline.
   **Fix:** port `RunReportCard` to a React component reading the digest map.

3. **No attach file/picture on the composer.** The wire already supports it — no
   hub change needed. `POST …/agents/{id}/input` accepts
   `{kind:"text", body, images:[{mime_type,data}], pdfs:[…], audios:[…],
   videos:[…]}` where `data` is **raw base64** (`handlers_agent_input.go:197-322`).
   Caps: images ≤3/turn ≤5 MiB (png/jpeg/webp/gif); pdf ≤1 ≤32 MiB; audio/video
   ≤1 ≤20 MiB. Text/code files are inlined into the body as a fenced code block
   (mobile `composer_text_attach.dart`, no backend). Gate the image/multimodal
   buttons on the agent family's `prompt_image`/`prompt_pdf`/… capability flags.
   **Fix:** add picker → base64 → clamp → send the same JSON;
   `client.postAgentInput` gains optional attachment arrays.

4. **No vault/sync for terminal hosts + keys.** The zero-knowledge vault
   (ADR-052 D-3/D-4) is **already built on mobile and the hub** — not planned:
   `lib/services/vault/{vault_service,vault_crypto}.dart` seal a
   `{connections, sshKeys, passwords}` bundle with **AES-256-GCM**, wrap the vault
   key **per device (X25519 sealed box)** and under a **recovery code (Argon2id)**,
   and drive `GET/PUT /vault`, `/vault/recovery`, `/vault/devices`
   (`hub/internal/server/handlers_vault.go`, migration `0061_key_vault.up.sql`).
   The desktop has none of it — no saved connections, no key store, not a vault
   device. **Fix (two steps):** first a local saved-connection + key store
   (`Connection` model `lib/providers/connection_provider.dart:8-169`; `SshKeyMeta`
   `lib/providers/key_provider.dart:16-93`; secrets in the OS keychain via the Rust
   core, keyed `password_<id>`/`privatekey_<id>`/`passphrase_<id>`); then port
   `VaultCrypto`'s exact byte shapes to Rust (RustCrypto) and join the vault as a
   device. No hub changes required.

5. **No hub switching / profiles.** Mobile persists a `HubProfile{id,name,baseUrl,
   teamId}` list (`lib/services/hub/hub_profiles.dart`) with the **token per
   profile in the keychain** (`hub_token_<id>`), an active-profile pointer, and a
   persistent AppBar switcher pill (`lib/widgets/team_switcher.dart`); switching
   re-binds the client and **rehydrates that profile's cache partition**
   (`lib/providers/hub_provider.dart:424-462`). Desktop has a single in-memory
   config. **Fix:** a profile store (non-secret fields on disk, token in OS
   keychain), a titlebar switcher, and client re-bind on switch.

6. **No local cache / related settings.** Mobile is cache-first (ADR-006): a
   partitioned sqflite snapshot store keyed `"<baseUrl>#<teamId>"` (7-day TTL,
   500-row cap), a content-addressed 200 MiB blob byte cache, and a `readThrough`
   pattern that returns cached bodies tagged `staleSince` on **offline** failures
   only (4xx never cached) (`lib/services/hub/{hub_snapshot_cache,hub_read_through}.dart`),
   with an offline banner and "Clear cache" settings. Desktop renders only live
   queries and blanks offline. **Fix:** a cache-first layer (webview IndexedDB or
   a Tauri SQL store) partitioned by hub+team, `staleSince` plumbing into the
   surfaces, an offline banner, and cache settings.

## Coverage matrix (condensed)

**Present:** shell/palette/status bar · fleet navigator · agent lifecycle ·
attention/approvals (by-kind + override) · audit (poll) · admin cockpit
(hosts/agents/teams/upkeep + policy YAML) · SSH terminal (live) · device settings
(theme/lang) · signed auto-updater.

**Partial:** ~~transcript (renders but content broken — gap 1)~~ **✅ rich (1a)** ·
~~digest (raw JSON — gap 2)~~ **✅ dashboard (1b)** · ~~composer (no attach — gap 3)~~
**✅ attachments (1c)** · projects (tree, no list/create) · tasks (view + status patch, no create)
· runs/plans (tables, no launch/author) · deliverables (counts, no ratify) ·
hosts (grouped, no detail) · team governance (policy YAML only) · ~~SSH (no saved
profiles/keys)~~ **✅ saved connections + key store + vault (2a/2b)** · connect
(single, in-memory).

Phase 3 also cleared the last two **Partial** rows: hosts still lack a detail
view, but *connect* is now multi-profile (switcher + keychain tokens + offline
cache), not single/in-memory.

**Missing:** create/edit for projects·tasks·runs·plans·agents·schedules·docs ·
agent spawn · deliverable reviews/ratification · documents/artifacts/blobs viewers
· project channels (chat) · sessions surface · search · Insights analytics · Me
home · decision history · notes · templates/agent-families · budget · councils ·
steward config · team switcher · SSH keys/vault/snippets/history · tmux pane mgmt
· file transfer/remote browser · multimodal/image attach · voice input · offline
cache · voice/action-bar settings.

## Foundations (cross-cutting, do early)

- **F1 — a small component kit.** Reusable primitives the surfaces above all need:
  `Modal`/`Sheet`, `Card`, `StatTile`, `Table`, `Field`/`Form`, `Tabs`, `Badge`,
  `Markdown` (with code highlighting), `Toast`. Today each surface hand-rolls
  markup. A thin kit over the shared DTCG tokens keeps parity work fast and
  consistent.
- **F2 — a local persistence + secret layer.** Needed by profiles (5), cache (6),
  and connections/vault (4). Decide: webview IndexedDB vs a Tauri SQL store for
  cache; **OS keychain via the Rust core** for all secrets (tokens, SSH keys,
  vault material). One module (`state/persist.ts` + Rust `keychain` commands) that
  the rest build on.
- **F3 — write-path ergonomics.** A shared "governed action" submit helper (the
  hub's propose→approve loop, ADR-030) and optimistic-invalidate patterns so the
  many create/edit surfaces in Phase 4 are uniform.

## Phased plan

**Phase 1 — the conversation loop feels real (director gaps 1–3). ✅ SHIPPED
(desktop-v0.2.2+, commits 6555356b / 83b81cb7 / 76943317).** The daily surface.
Shipped: (1a) rich transcript — per-kind `EventCard` dispatch, tool call↔result
pairing on `tool_use_id` (avoiding the mobile `p['id']` latent bug), markdown via
the F1 `Markdown` primitive, accent stripes by kind; (1b) the digest dashboard
(`RunReport` port of `RunReportCard`: outcome badge + stat-tile grid + per-model
token table + errors list + live/cached footer); (1c) composer attachments
(`Composer` + `attach.ts`: image/pdf/audio/video base64 send with caps mirrored
from the hub, text/code files inlined as fenced blocks). *Deferred within Phase 1:*
per-turn timeline (`GET …/turns`); capability-gating the attach button (hub
strip-and-warns unsupported modalities, so it's cosmetic); syntax highlighting in
code blocks (bundle weight — react-markdown added ~170 KB).

**Phase 2 — breakglass parity + vault (gap 4). ✅ SHIPPED (commits 540ca76d /
7500f74d / f8ab20e4 / ba3e29c7).** (2a) saved SSH connections + a local key store
(import) with secrets in the OS keychain (`keyring` crate, pure-Rust zbus backend
on Linux) — the terminal now has a saved-connections sidebar + key manager instead
of "retype every time"; (2b) the zero-knowledge vault — a byte-for-byte Rust port
of `vault_crypto.dart` (AES-256-GCM bundle, ephemeral-X25519 + HKDF device wrap,
Argon2id recovery), CI-verified via `cargo test` round-trips, behind a Settings →
Vault panel (create / sync up-down / restore-with-recovery). *Foundation F2 landed
here:* `state/persist.ts` (keychain bridge + JSON store). *Deferred:* SSH key
**generation** (import only); **jump-host connect** (fields persisted for vault
parity, but russh has no ProxyJump yet); device-to-device enrollment approval
(recovery-code restore covers cross-device). **⚠ Cross-device Rust↔Dart vault
interop is unverified — needs a real desktop↔phone test before relying on sync.**

**Phase 3 — multi-hub + offline (gaps 5–6). ✅ SHIPPED (commit 4650b5c6).** (3a)
hub profiles — `HubProfile{id,name,baseUrl,teamId}` in localStorage, token per
profile in the OS keychain (`hub_token_<id>`), a titlebar `ProfileSwitcher`
(switch/add/edit/remove), and `session.init()` auto-binds the active profile on
launch; switching re-binds the client and drops the query cache. (3b) cache-first
— the TanStack QueryClient is persisted to localStorage (`PersistQueryClientProvider`,
7-day maxAge; only successful results persist → 4xx never cached), so surfaces
render the last snapshot instantly and keep showing it offline, with an offline
banner + a Settings → Offline cache section (size + clear). *Note:* used the
mobile team-id-in-key + clear-on-switch for partitioning rather than a full
`baseUrl#teamId` sqflite store; localStorage rather than IndexedDB (adequate for
the snapshot sizes; revisit if it grows).

**Phase 4 — write paths + missing surfaces (breadth).** The long tail, roughly by
value: project/task/run/plan create+edit and agent spawn; deliverable
ratify/reviews; documents + artifacts/blobs viewers; project channels (chat —
`streamChannel` already in the client); sessions surface (`listSessions` already in
the client); search; Insights analytics; Me home + decision history + notes; team
governance depth (templates/families/budget/councils/steward-config). Depends on
F1/F3. Cheap first wins: channels and sessions already have client methods with no
surface.

**Phase 5 — polish.** Voice input (Alibaba DashScope WS, `lib/services/voice`),
tmux pane management, file transfer/remote browser, voice/action-bar/keyboard
settings depth. Desktop-lower-priority; some (custom keyboard, NavPad) are
mobile-only and out of scope.

## Sequencing & first ticket

F1 (component kit) + F2 (persist/keychain) are prerequisites for most of the work;
stand up their minimum viable slice first, then **Phase 1** as the first visible
win. Phases 2 and 3 are independent of Phase 1 and of each other (both gated on
F2) and can run in parallel once F2 lands. Phase 4 is a backlog to burn down
surface-by-surface behind F1/F3.

**First ticket:** Phase 1a — rich transcript rendering. It is the loudest gap, the
schema is fully mapped above, and it needs only a `Markdown` primitive from F1. It
also unblocks 1b/1c on the same Focus region.

## References

- Builds on: [`plans/desktop-control-plane.md`](desktop-control-plane.md) (WS0–WS8),
  [ADR-051](../decisions/051-desktop-client-stack.md) (stack + tokens),
  [ADR-052](../decisions/052-breakglass-ssh-and-key-vault.md) (SSH + vault).
- Grounds against: [`spine/information-architecture.md`](../spine/information-architecture.md)
  (mobile IA), and the hub handlers cited inline.
