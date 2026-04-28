# Coding conventions

> **Type:** reference
> **Status:** Current (2026-04-28)
> **Audience:** contributors
> **Last verified vs code:** v1.0.313

**TL;DR.** Code style for the termipod Flutter mobile app and Go hub.
Upstream (Effective Dart, Effective Go, `analysis_options.yaml`, `gofmt`)
is the source of truth for universal conventions — this doc only
covers **project-specific deltas** and architectural rules with
explicit rationale. Read this before opening a PR.

---

## 1. Sources of truth (read these first)

| Surface | Authority | Link |
|---|---|---|
| Dart language conventions | Effective Dart | https://dart.dev/effective-dart |
| Flutter widget patterns | Flutter style guide | https://github.com/flutter/flutter/blob/main/docs/contributing/Style-guide-for-Flutter-repo.md |
| Lints (machine-enforced) | `analysis_options.yaml` | repo root |
| Riverpod state mgmt | Riverpod docs | https://riverpod.dev |
| Go style | Effective Go + `gofmt` | https://go.dev/doc/effective_go |
| Comments & WHY-not-WHAT | `CLAUDE.md` "Doing tasks" | repo root |
| Doc system | `docs/doc-spec.md` | this directory's parent |

If a convention is universal (PascalCase for Dart types, ALL_CAPS
for Go env vars, `gofmt`-canonical formatting), it lives upstream.
**This doc only covers what's specific to termipod**, plus project
policies that span both languages.

---

## 2. Project layout — where things go

The directory tree is itself a contract. New code lands in the right
layer or doesn't land.

### `lib/` (Flutter mobile)

```
lib/
├── main.dart               entry point — wire providers, run MyApp
├── providers/              Riverpod state (one notifier per concern)
├── screens/                top-level routes (one folder per IA area)
├── widgets/                shared widgets across screens
├── services/               business logic + I/O (no widgets)
│   ├── hub/                HubClient + read-through cache
│   ├── ssh/                dartssh2 wrapper
│   ├── tmux/               tmux CLI orchestration
│   └── notification/       local notifications
├── theme/                  colors + typography (DesignColors)
├── l10n/                   .arb files; generated via flutter gen-l10n
└── models/                 plain data classes (rare; mostly inline)
```

**Why this layout:** the IA defines visible regions
(`docs/spine/information-architecture.md`); `screens/` mirrors them.
Cross-cutting widgets live in `widgets/` so they're not
accidentally screen-coupled. `services/` is pure logic — never
imports `package:flutter/widgets.dart`. State is the bridge between
services and screens.

### `hub/` (Go)

```
hub/
├── cmd/                    binaries (each a thin main.go)
│   ├── hub-server/         the server
│   ├── host-runner/        host agent — multicall (also serves
│   │                       hub-mcp-bridge by basename)
│   └── …
├── internal/               package code (Go internal/ visibility)
│   ├── server/             HTTP + MCP catalog
│   ├── hostrunner/         host-side bookkeeping
│   ├── hubmcpserver/       rich-authority MCP tools (consumed
│   │                       in-process by mcp_authority.go)
│   └── …
├── migrations/             golang-migrate (NNN_description.up/down.sql)
└── templates/              embedded YAML/MD agent templates
```

**Why `internal/`:** Go's `internal/` makes packages unreachable
outside the module. Prevents external consumers from binding to
unstable APIs. Everything that's not a deliberate public surface
belongs there.

---

## 3. Architecture patterns (load-bearing)

These are non-negotiable. Each ties to an ADR or a memory entry; the
"why" is durable.

### 3.1 State management — Riverpod 3.x

| Pattern | Use when | Reference |
|---|---|---|
| `AsyncNotifierProvider` | Async lifecycle (network + cache) | `hubProvider` |
| `NotifierProvider.family` | Parameterized state (per agent, per session) | `composeDraftProvider` |
| `FutureProvider.autoDispose` | One-shot fetch tied to a screen | `recentAuditProvider` |
| `StreamProvider` | Genuinely streaming sources (SSE) | `agentEventsProvider` |

**Watch vs read:**
- `ref.watch(...)` only in `build()` (or where reactive rebuild is
  wanted)
- `ref.read(...)` in event handlers and one-shot callbacks
- Never watch in callbacks — produces stale closures and silent rebuild bugs

**Disposal:** long-lived clients (HubClient, SQLite) clean up via
`ref.onDispose(...)` in the notifier's `build()`. *Why:* Riverpod
doesn't know your client owns sockets/connections; explicit cleanup
prevents resource leaks across hot-reload and config changes.

### 3.2 Storage layering — three stores, no overlap

| Store | Concern | Example |
|---|---|---|
| `SharedPreferences` | Stable config / metadata | hub URL, team id, theme |
| `flutter_secure_storage` | Secrets only | hub bearer, SSH keys |
| `sqflite` (`HubSnapshotCache`) | Mutable server content for offline | list/get response snapshots |

**Why this rule:** mixing causes real bugs. Caching server data in
`SharedPreferences` has no eviction, no per-hub partitioning, no TTL —
data grows forever and stale data leaks across hub reconnects.
Storing config in secure storage adds keychain latency for no
benefit. Memory: `feedback_storage_layering`.

### 3.3 Cache-first UX

When a provider has both network and cache: render from cache on
first paint, refresh on a microtask after `build()` returns. **Not**
"network first, fall back to cache only when offline."

**Why:** without cache-first, the UI shows empty during the network
roundtrip even when SQLite has the answer locally in microseconds.
ADR: `../decisions/006-cache-first-cold-start.md`.

Pattern: implemented in `lib/services/hub/hub_read_through.dart` +
`lib/providers/hub_provider.dart` `_hydrateFromCache`.

### 3.4 Service composition — prefer one over many

When two services could combine into one (single binary, single
symlink, single endpoint), do that. Architectural cleanliness
("two services keep concerns separated") loses to ops simplicity in
self-hosted MVP.

**Why:** every install step is friction. ADR:
`../decisions/002-mcp-consolidation.md`. Memory:
`feedback_one_install_command`.

### 3.5 The director model (UX implication for code)

User expresses intent; agents operate. The mobile app is a
**conversational + ratification surface**, not a control panel.

For developers: when a feature could be a button or an MCP tool the
steward calls, prefer the MCP tool. Buttons fall back; the steward's
toolset must be CEO-class (every authority operation reachable from
its session).

**Why:** ADR `../decisions/005-owner-authority-model.md`. Memories:
`feedback_ux_principal_director`, `feedback_steward_executive_role`.

---

## 4. Naming — project-specific deltas only

Effective Dart covers PascalCase / camelCase / snake_case file rules.
The deltas:

- **Providers / notifiers** carry the suffix: `hubProvider`,
  `HubNotifier`. Riverpod doesn't enforce this; we do for grep-ability.
- **Family providers** include the parameter shape: e.g.,
  `composeDraftProvider(connectionId)` not `composeProvider`.
- **Files in `screens/`** end with `_screen.dart`; in `widgets/`
  end with `.dart` only (the dir context is enough).
- **Service classes** drop the `Service` suffix when the file name
  already says it: `class HubClient` in `hub_client.dart`, not
  `class HubClientService`.
- **No version markers in any name** (`HubClientV2`, `_loadConfigNew`).
  Versions go in commit messages and changelog.
- **No abbreviations** beyond Dart standards (`req` / `resp` / `err` /
  `ctx` ok; `mgr` / `svc` / `ctlr` not).

For the Go side: standard Go naming. Migration files use the
`golang-migrate` convention `NNNN_description.up.sql` /
`NNNN_description.down.sql` with 4-digit zero-pad.

---

## 5. Comments

CLAUDE.md is canonical:

> Default to writing no comments. Only add one when the WHY is
> non-obvious: a hidden constraint, a subtle invariant, a workaround
> for a specific bug, behavior that would surprise a reader.
>
> Don't explain WHAT the code does, since well-named identifiers
> already do that.

This applies equally to Dart and Go. Multi-paragraph docstrings are
a refactor signal — extract the explanation into the right
`docs/discussions/` doc and link.

Don't reference the current task / commit ("added for the X flow").
That belongs in the PR description and rots as the codebase evolves.

---

## 6. Tests

| Layer | Test approach |
|---|---|
| Mobile unit | `test/` mirrors `lib/`; `ProviderContainer(overrides: [...])` for notifier tests |
| Mobile integration | Fake `HubClient` is the standard injection point — don't mock the cache/database |
| Hub Go | `httptest.NewServer(s.router)` against a real SQLite (`internal/server/e2e_acceptance_test.go` is the canonical example) |
| Database in tests | Real SQLite via `sqflite_common_ffi` (mobile) or in-memory (Go); never mock |

**Why no DB mock:** mocking the cache produces tests that pass
against fiction. Real SQLite is fast enough and exercises the actual
contract.

---

## 7. Build cadence

- Every commit bumps the version: `make bump VERSION=x.y.z-alpha`
- Version bump runs *before* the commit so each commit gets its own
  build
- Version markers are in `pubspec.yaml` and
  `hub/internal/buildinfo/buildinfo.go`; `make bump` updates both
- Tag releases on a clean main; CI builds APK + IPA from the tag
- Doc-only commits use the `docs:` prefix and bump like any other
- Hook bypasses (`--no-verify`, `--no-gpg-sign`) are forbidden
  unless the user explicitly asks; if a hook fails, fix the
  underlying issue

---

## 8. Project-specific anti-patterns

These have all caused real bugs in this codebase and have an entry
in memory or an ADR:

- **Backwards-compat shims for code that isn't actually deployed
  somewhere we can't redeploy.** This is a single-developer fork —
  just change the code; don't write feature flags for your own
  machine. Memory: `feedback_collaboration_lessons` ("delete old
  paths" gate).
- **Mocked databases / caches in tests.** See §6 — produces
  false-passing tests.
- **Multi-service install where one would do.** See §3.4 + ADR-002.
- **Network-first cache UX.** See §3.3 + ADR-006.
- **Hardcoded role-bound strings.** Use the vocabulary axes in
  `docs/reference/vocabulary.md` so a future per-team overlay
  (post-MVP packs) is a rename, not a rewrite.
- **Mixing storage tiers.** See §3.2.
- **Common ports as defaults.** Ports under 10000 collide with
  standard services. Memory: `feedback_uncommon_default_ports`.
- **`tmux` commands from Claude on this dev box.** Memory:
  `feedback_no_tmux_on_this_machine` (it's running inside tmux —
  any tmux command kills the user's session).

---

## 9. References

- Doc system: `../doc-spec.md`
- Architecture: `../spine/blueprint.md`
- Decisions log: `../decisions/`
- Active memory: see `MEMORY.md` index in
  `~/.claude/projects/-home-ubuntu-mux-pod/memory/` (loaded into
  every session)
