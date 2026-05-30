# TermiPod

TermiPod is a **mobile-first control plane for a fleet of AI agents**
across multiple machines. A human acts as *director*; a *steward*
agent coordinates the work on their behalf. Formerly MuxPod (an
Android SSH/tmux client) — that client now survives only as a
breakglass layer.

## Architecture

Three layers (see `docs/spine/blueprint.md`):

- **Hub** — a Go daemon. The authority layer: owns names, policies,
  events, references — metadata, *not bytes*. Exposes a REST API and
  an MCP tool surface for agents.
- **Host-runner** — a Go deputy on each host. Spawns agents, owns
  their tmux panes, enforces policy, relays agent↔hub MCP calls.
- **Agent** — the stochastic executor: Claude Code, Codex, Gemini
  CLI, Kimi Code, or Antigravity.

**A2A** (agent-to-agent) tunnels through the hub via a reverse-tunnel
relay, so a steward on a VPS can drive a worker on a NAT'd GPU box.
The **mobile app** (Flutter) is the director's cockpit — five tabs:
Projects · Activity · Me · Hosts · Settings.

## Repository layout

Monorepo:

```
lib/        Flutter mobile app (Dart)
hub/        Go — hub daemon, host-runner, MCP bridges
hub-tui/    terminal UI for the hub
docs/       documentation (start at docs/README.md)
scripts/    lint + tooling
test/       Flutter tests
android/ ios/ linux/ macos/ web/ windows/   platform shells
```

`lib/screens/` has one folder per surface — projects, me, hosts,
sessions, activity, insights, team, settings, plus the SSH/tmux ones
(connections, terminal, keys, vault). `lib/services/` holds the hub
client, SSH, tmux, voice, etc.; `lib/providers/` holds Riverpod
providers.

`hub/internal/server` is the REST API; `hub/internal/hostrunner`
spawns agents; `hub/internal/hubmcpserver` is the MCP catalog +
dispatcher; `hub/internal/drivers` holds engine drivers;
`hub/migrations` holds numbered SQL migrations; `hub/templates` and
`hub/internal/agentfamilies` hold bundled YAML.

## Tech stack

- **Mobile** — Flutter 3.24+ / Dart 3.10+; `flutter_riverpod` 3.x
  (state); `dartssh2` + `xterm` (SSH/terminal); `flutter_secure_storage`
  (keys/tokens); `sqflite` (offline snapshot cache); `record` +
  `web_socket_channel` (streaming voice input).
- **Hub** — Go 1.23; `modernc.org/sqlite` (pure-Go, no cgo); numbered
  SQL migrations; MCP server + UDS/stdio bridges.

## Development commands

```bash
# Mobile app
flutter run / flutter analyze / flutter test / flutter build apk

# Hub (Go)
cd hub && go build ./... && go test ./...
go run ./cmd/hub-server     # run the hub daemon
```

## Documentation

Read `@/docs/README.md` first — the index. Doc structure follows
`@/docs/doc-spec.md` (seven primitives: axiom / vision / plan /
decision / reference / how-to / discussion).

- `@/docs/roadmap.md` — Now/Next/Later; `@/docs/changelog.md` — per release
- `@/docs/spine/` — architecture (blueprint, information-architecture,
  agent-lifecycle, sessions, protocols)
- `@/docs/decisions/` — append-only numbered ADRs
- `@/docs/reference/glossary.md` — canonical definitions for
  collision-prone terms
- `@/docs/reference/coding-conventions.md` — code style (Flutter + Go)

## Domain model

The hub owns these primitives; the mobile app reads them as JSON maps
(the hub holds names + events, hosts hold bytes). Canonical
definitions are in `docs/reference/glossary.md`.

- **Project** — a unit of directed work; owns plans, tasks, runs,
  documents, channels.
- **Agent** — a spawned engine instance. `kind` = the engine.
  Lifecycle: pending → running → terminated/crashed/failed → archived.
- **Steward** — a coordinating agent (`kind` starts with `steward.`).
  General steward = frozen concierge; project/domain stewards are
  scoped overlays.
- **Task** — the first-class unit of steward-dispatched work
  (ADR-029). Status: todo / in_progress / blocked / done / cancelled.
- **Session** — the conversational primitive that survives respawn.
- **Host / Run / attention_items / audit_events / Plan / A2A message
  / Document / Deliverable / Artifact** — see the glossary.

## Engines & driving modes

Five engine families: claude-code, codex, gemini-cli, kimi-code,
antigravity. (`gemini-cli` is deprecated — Google retires it
2026-06-18 for consumer tiers — and `antigravity` is its M4-only
successor; see ADR-035.) Each agent runs in one **driving mode** (the
`agents.driving_mode` column) — the control channel differs,
governance is identical. Authoritative source: `docs/spine/protocols.md` §5.

- **M1 — ACP.** JSON-RPC over stdio via an ACP adapter. Used by
  Codex, Gemini CLI, Kimi Code.
- **M2 — structured stdio.** An agent-native JSON-line protocol
  (e.g. `claude --output-format stream-json`).
- **M4 — per-engine local-stream tap.** claude-code uses
  `LocalLogTailDriver` — tails the on-disk session JSONL and routes
  input via `tmux send-keys` (ADR-027). Other engines retain the
  legacy tmux-pane PTY scrape until their adapters ship.

(M3 is not a mode — it's a one-shot `llm_call` plan step.)

Engine frame profiles are **data** — YAML under
`hub/internal/agentfamilies/` and `hub/templates/`. A new engine is a
YAML file, not Go code.

## Conventions

- **Verify, don't guess.** Reason from first principles and
  well-grounded practice; when a fact isn't certain, confirm it
  against the codebase, the docs, or the web before acting on it or
  writing it down. **Before claiming a tool, test, function, or
  behaviour exists — or doesn't — grep for it and cite the
  `file:line`.** A claim you can't cite is a guess; an absence you
  haven't searched for is a guess. (The invariants are also encoded
  as executable tests in `*_meta_test.go` / `*_sweep_test.go` — read
  those before reasoning about the tool catalog.)
- **Choose terms precisely.** Use the most accurate word for a
  concept; avoid coining or reusing one that collides with an
  existing term. `docs/reference/glossary.md` is canonical for
  collision-prone terms (`lint-glossary.sh` enforces it). When a term
  is ambiguous, or a needed concept has no clear name, raise the gap
  for discussion — don't settle for a vague or overloaded word.
- **Fix the root cause, not the symptom.** When fixing a bug, reflect
  on *why* it happened and what class of bug it belongs to; fix the
  class, not just the instance. When the cause is a system-wide gap
  or a load-bearing design issue, surface it for discussion (a
  discussion doc or an ADR) instead of patching locally.
- **English only** — all code, comments, and docs.
- **Docs** follow `docs/doc-spec.md`; read `docs/README.md` first.
  Reorgs go in their own `docs:`-prefixed commits.
- **ADRs** are append-only and numbered; the **changelog** has one
  section per tagged release.
- Doc-only changes do not bump the app version; release tags are cut
  only on explicit request.

### Easy to get wrong

- **MCP tools need three things in lockstep** — a `tools/list`
  catalog entry, a dispatcher case, and a handler. A handler without
  the catalog entry is invisible to agents.
- **The tool catalog has *two* registries — check both.** Authority
  tools live in `hub/internal/hubmcpserver/toolspec.go` (`ToolSpec`
  registry); native tools live in `hub/internal/server/native_tools.go`
  (`buildNativeTools`). A tool you "can't find" in one is often in the
  other; `SeeAlso`/alias targets cross between them. `tool_registry_test.go`
  + `native_tools_meta_test.go` + `tool_contract_sweep_test.go` lock
  the cross-registry invariants.
- **Behaviour is data.** Agent kinds, prompts, plans, and policies
  are editable YAML templates — adding one is not a code change.
- **`driving_mode` (M1/M2/M4) ≠ permission mode** (auto-allow vs
  prompt) — different columns, different concerns.
- The Flutter app has **no typed Dart classes** for hub entities —
  it reads them as `Map<String, dynamic>` JSON.
