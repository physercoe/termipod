# Coding conventions (Flutter / Dart)

> **Type:** reference
> **Status:** Current (2026-04-28) — rewrites the prior React Native version (pre-rebrand legacy in `../archive/tmux-mobile-design-v2.md`)
> **Audience:** contributors
> **Last verified vs code:** v1.0.311

**TL;DR.** Code style for the Flutter/Dart mobile app + the Go hub.
Conventions land here when they're durable; see `../doc-spec.md` for
the doc system and `../how-to/` for runbooks.

---

## 1. Naming

### Dart / Flutter

| Target | Rule | Example |
|---|---|---|
| Class / type | PascalCase | `class HubNotifier` |
| Constructor | PascalCase, named per type | `HubClient.fromConfig(...)` |
| File | snake_case | `hub_provider.dart`, `agent_compose.dart` |
| Function / method | camelCase | `listAgentsCached()`, `_decodeListMaps()` |
| Variable / parameter | camelCase | `hubKey`, `staleSince` |
| Private (file-scope) | leading underscore | `_AgentComposeState`, `_loadConfig` |
| Constant | camelCase, prefer `const` | `const defaultEgressPort = 41825` |
| Enum value | camelCase | `enum _DiffKind { context, insert, delete }` |
| Provider / notifier | name + `Provider` / `Notifier` | `hubProvider`, `HubNotifier` |

### Go (hub side)

Standard Go: PascalCase for exported, camelCase for unexported. File
names lowercase with underscores (`mcp_authority.go`).

### Don't

- Hungarian notation (`strHostUrl`, `iCount`)
- Abbreviations beyond standard ones (`req` / `resp` / `err` / `ctx`
  are fine; `cfg` is fine; `mgr` / `svc` / `ctlr` are not)
- Trailing version markers in any name (`HubClientV2`, `_loadConfigNew`)

---

## 2. File and directory layout

### `lib/` (mobile)

```
lib/
├── main.dart               entry point
├── providers/              Riverpod state
├── screens/                top-level routes (one folder per area)
├── widgets/                shared widgets across screens
├── services/               business logic + I/O
│   ├── hub/                hub client + cache + read-through
│   ├── ssh/                dartssh2 wrapper
│   ├── tmux/               tmux CLI orchestration
│   └── notification/       local notifications
├── theme/                  colors + typography
├── l10n/                   i18n strings
└── models/                 plain data classes (rare; usually inline)
```

### `hub/` (Go)

```
hub/
├── cmd/                    binaries
│   ├── hub-server/         the server
│   ├── host-runner/        the host agent (multicall)
│   └── …
├── internal/               package code (Go internal/ visibility)
│   ├── server/             HTTP + MCP server
│   ├── hostrunner/         host-side bookkeeping
│   ├── hubmcpserver/       MCP tool catalog
│   └── …
├── migrations/             golang-migrate up/down SQL
└── templates/              embedded YAML/MD agent templates
```

### One concern per file

A file holds one widget, one provider, one service, or one Go type.
Big files (1000+ LOC) are a refactor signal — see
`../discussions/monolith-refactor.md` for known offenders.

---

## 3. State management — Riverpod 3.x

- `AsyncNotifierProvider` for state with async lifecycle (network +
  cache). Example: `hubProvider`.
- `NotifierProvider.family` for parameterized state (one notifier per
  agent ID, per session ID, etc.). Example: `composeDraftProvider`.
- `FutureProvider.autoDispose` for one-shot fetches that close
  themselves when the screen unmounts. Example:
  `recentAuditProvider`.
- `StreamProvider` for server-sent events (SSE). Example:
  `agentEventsProvider`.

### Watch vs read

- `ref.watch(...)` in `build()` / when reactive rebuild is wanted
- `ref.read(...)` in event handlers / one-shot calls — never watch in
  callbacks

### Disposing

Long-lived clients (HubClient, SQLite handles) clean up via
`ref.onDispose(...)` in the notifier's `build()`.

### Cache-first pattern

When a provider has a network + cache shape, default to *render
cache, then refresh* — not network-with-fallback. See
`../decisions/006-cache-first-cold-start.md`. Implementation lives
in `lib/services/hub/hub_read_through.dart`.

---

## 4. Storage layering

Three separate stores, each for one concern. **Do not mix.**

| Store | Used for | Example |
|---|---|---|
| `SharedPreferences` | Stable config / metadata | hub URL, team id, theme |
| `flutter_secure_storage` | Secrets only | hub bearer token, SSH keys, passwords |
| `sqflite` (`HubSnapshotCache`) | Mutable server content for offline | list/get response snapshots |

**Anti-pattern:** caching server data in SharedPreferences (no
eviction, no per-hub partitioning, no TTL). **Anti-pattern:**
storing config in secure storage (slower; no real benefit).

---

## 5. Async / Future / Stream

- `async`/`await` for sequential work
- `Future.wait([...])` when calls are independent
- `Stream` only when the source is genuinely streaming (SSE, file
  watch). Don't wrap a single Future in a one-shot Stream.
- Cancel pattern: hold a `StreamSubscription` field and cancel in
  `dispose()`

### Error handling

- `try / on SpecificError / catch (e)` — typed catches first
- Don't swallow errors silently; either rethrow, log, or surface to
  the user
- Network errors: `HubApiError` (typed) for hub-side; `SocketException`
  / `TimeoutException` / `HttpException` for the offline-failure
  taxonomy in `hub_read_through.dart`

---

## 6. Widgets

### File structure

```dart
// 1. imports — dart core, then flutter, then package, then relative
import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/hub_provider.dart';
import 'agent_compose.dart';

// 2. public widget class
class AgentFeed extends ConsumerStatefulWidget {
  final String agentId;
  final String? sessionId;
  const AgentFeed({super.key, required this.agentId, this.sessionId});

  @override
  ConsumerState<AgentFeed> createState() => _AgentFeedState();
}

// 3. state class
class _AgentFeedState extends ConsumerState<AgentFeed> {
  // fields
  // initState / dispose
  // private methods (alphabetical or by lifecycle)
  // build last
}

// 4. private helper widgets used only by this file
class _NewEventsPill extends StatelessWidget { … }
```

### Stateless vs stateful

- `StatelessWidget` if the widget has no internal state and the
  parent fully drives it
- `StatefulWidget` for animations, controllers, scroll positions, or
  any local mutation
- `ConsumerWidget` / `ConsumerStatefulWidget` to read providers

### Keys

- `ValueKey` for list items where order can change (per
  `feedback_flutter_sliver_keys` memory: stateful widgets in
  reorderable Sliver lists need ValueKey even with keyed providers)
- `GlobalKey` only when truly necessary (e.g., grabbing state from a
  parent via `currentState`); usually a sign of a structural problem

---

## 7. Comments

Default to no comments. Add one when the WHY is non-obvious: a
hidden constraint, a subtle invariant, a workaround, or behavior
that would surprise a reader.

- ✗ `// increment counter` — adds nothing
- ✓ `// Service tier 'priority' is silently downgraded to 'standard'
   when over budget; we treat both as success.`

Don't reference the current task or commit ("added for the X flow").
That belongs in the PR description.

Multi-paragraph docstrings are usually a refactor signal — extract
the explanation into the right `discussions/` doc and link.

---

## 8. Imports

- Dart core first (`dart:async`, `dart:convert`)
- Flutter SDK next
- Third-party `package:` imports
- Relative imports last

Within each group, alphabetical.

`package:termipod/...` form for files referenced from generated
locations (l10n); relative paths everywhere else.

---

## 9. Tests

- Unit tests in `test/` mirroring the source path
- Integration tests for hub interactions live in `hub/internal/server/*_test.go`
- For the mobile app, a fake `HubClient` is the standard injection
  point; don't mock the database — use `sqflite_common_ffi` for FFI
  in tests
- Fake providers: `ProviderContainer(overrides: [...])` for unit
  testing notifiers without widgets

---

## 10. Go conventions (hub side)

- Standard `gofmt` — non-negotiable, runs in CI
- Errors wrap with `fmt.Errorf("context: %w", err)`; check with
  `errors.Is` / `errors.As`
- Context plumbing — every public function takes `ctx context.Context`
  as first arg
- HTTP handlers in `handlers_*.go`; MCP tools in `mcp.go` /
  `mcp_more.go` / `mcp_authority.go` / `mcp_orchestrate.go`
- Migrations append-only; never edit a numbered file after merge
- Tests use `httptest.NewServer(s.router)` — see existing examples in
  `internal/server/e2e_acceptance_test.go`

---

## 11. Build + ship cadence

- Every commit bumps the version (`make bump VERSION=x.y.z-alpha`)
  before commit, so each commit gets its own build
- Version markers live in: `pubspec.yaml`, `hub/internal/buildinfo/buildinfo.go`
- Tag releases on a clean main; CI then builds APK + IPA from the
  tag
- Doc reorgs / pure-docs commits use `docs:` prefix and bump the
  version like any other change

---

## 12. Anti-patterns to avoid

- **Backwards-compat shims** for code that isn't actually deployed
  somewhere we can't redeploy. Just change the code; don't write
  feature flags for the developer's own machine.
- **Half-finished implementations.** If a feature can't ship, don't
  land partial code with a TODO; revert and write a plan in
  `plans/`.
- **Comments explaining WHAT.** The code does that. Comments are for
  WHY.
- **Speculative abstractions.** Three similar lines is better than a
  premature interface. Extract on the third or fourth use, not the
  second.
- **Mocks where the real thing fits in a test.** SQLite, httptest,
  and a fake HubClient give you the contract; mocking
  `HubSnapshotCache` directly gives you a test that passes against
  fiction.
