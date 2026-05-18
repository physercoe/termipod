# TermiPod

TermiPod is a **mobile-first control plane for a fleet of AI agents**
distributed across multiple machines. A single human acts as
*director*; a *steward* agent coordinates the work on their behalf.
Formerly MuxPod — an Android SSH/tmux client — TermiPod has since
diverged into a multi-agent, multi-host system; the SSH/tmux client
survives as a breakglass layer, not the product.

## Architecture

Three layers, each resisting a specific failure mode (see
`docs/spine/blueprint.md`):

- **Hub** — a Go daemon. The authority layer: owns names, policies,
  events, and references. Stores metadata, *not bytes*. Exposes a
  REST API and an MCP tool surface for agents.
- **Host-runner** — a deterministic Go deputy on each host. Spawns
  agents, owns their tmux panes, enforces budget/policy, and relays
  agent↔hub calls through an MCP gateway.
- **Agent** — the stochastic executor: Claude Code, Codex, Gemini
  CLI, or Kimi Code, driven by a per-engine driver.

An **A2A** (agent-to-agent) protocol tunnels through the hub via a
reverse-tunnel relay, so a steward on a VPS can delegate work to a
worker on a NAT'd GPU box.

The **mobile app** (Flutter) is the director's cockpit over this
stack. Its five-tab IA is **Projects · Activity · Me · Hosts ·
Settings**. The phone is opened in glances, not sessions — the
director ratifies and reviews, the steward operates.

## Repository layout

This is a monorepo:

```
termipod/
├── lib/                Flutter mobile app (Dart)
├── hub/                Go — hub daemon, host-runner, MCP bridges
├── hub-tui/            terminal UI for the hub
├── android/ ios/ linux/ macos/ web/ windows/   platform shells
├── assets/             bundled app assets
├── docs/               project documentation (see doc-spec.md)
├── scripts/            lint + tooling (lint-docs.sh, lint-glossary.sh, …)
└── test/               Flutter widget/unit tests
```

## Tech stack

**Mobile app** — Flutter 3.24+ / Dart 3.10+:
- `flutter_riverpod` 3.x — state management
- `dartssh2` — SSH; `xterm` — terminal rendering
- `flutter_secure_storage` — SSH keys / tokens; `local_auth` —
  biometric unlock
- `sqflite` — offline snapshot cache; `shared_preferences` — config
- `flutter_markdown` / `flutter_math_fork` / `flutter_highlight` /
  `pdfrx` / `webview_flutter` — artifact rendering
- `record` + `web_socket_channel` — streaming voice input
- `flutter_local_notifications` / `flutter_foreground_task` —
  background + notifications

**Hub + host-runner** — Go 1.23:
- `modernc.org/sqlite` — pure-Go SQLite (no cgo)
- numbered SQL migrations under `hub/migrations/`
- MCP server + UDS/stdio bridges for the agent tool surface

## Development commands

**Mobile app:**
```bash
flutter run             # dev run
flutter analyze         # static analysis
flutter test            # tests
flutter build apk       # Android release
```

**Hub (Go):**
```bash
cd hub
go build ./...
go test ./...
go run ./cmd/hub-server     # run the hub daemon
```

Doc-only changes do not bump the app version. Release tags are cut
only on explicit request.

## Documentation

Read `@/docs/README.md` first — it's the index. Doc structure
follows `@/docs/doc-spec.md` (seven primitives: axiom / vision /
plan / decision / reference / how-to / discussion, plus
tutorial/archive adjuncts).

- `@/docs/README.md` — index, where to start
- `@/docs/roadmap.md` — vision + phases + Now/Next/Later
- `@/docs/changelog.md` — what shipped, per release
- `@/docs/doc-spec.md` — contract every doc honors
- `@/docs/spine/` — architecture (blueprint, information-architecture,
  agent-lifecycle, sessions)
- `@/docs/decisions/` — append-only ADRs (numbered)
- `@/docs/reference/glossary.md` — canonical definitions for
  collision-prone terms (session, fork, kind, transcript, …)
- `@/docs/reference/coding-conventions.md` — code style for the
  Flutter app + Go hub (project-specific deltas only)
- `@/docs/reference/ui-guidelines.md` — UI/UX guidelines
- `@/docs/archive/tmux-mobile-design-v2.md` — legacy MuxPod design,
  archived

## Mobile directory structure

```
lib/
├── main.dart            entry point
├── models/              UI config models (action bar, snippets, …)
├── providers/           Riverpod providers (hub state, sessions, …)
├── screens/
│   ├── projects/        project inventory + detail (tasks/plans/runs/…)
│   ├── activity/        team-wide audit feed
│   ├── me/              director triage — approvals, tasks, digest
│   ├── hosts/           host-runner check-ins
│   ├── sessions/        agent/steward conversations
│   ├── insights/        observability dashboards
│   ├── team/            members, policies, templates, schedules
│   ├── connections/     SSH connection management (breakglass)
│   ├── terminal/        tmux terminal surface
│   ├── keys/  vault/    SSH key + snippet management
│   ├── documents/ deliverables/ artifacts/   agent outputs
│   ├── hub/             hub bootstrap + profile setup
│   └── settings/        settings
├── services/            ssh, tmux, terminal, keychain, hub client,
│                        sftp, voice, notifications, deep links, …
├── theme/  widgets/  l10n/
```

## Hub directory structure

```
hub/
├── hub.go               package entry
├── cmd/                 hub-server, host-runner, hub-mcp-server,
│                        hub-mcp-bridge, mock-trainer, probe-* tools
├── internal/
│   ├── server/          REST API + handlers + event log
│   ├── hostrunner/      agent spawn + pane ownership + MCP gateway
│   ├── hubmcpserver/    MCP tool catalog + dispatcher
│   ├── mcp/ mcpbridge/ mcpudsbridge/   MCP transport bridges
│   ├── drivers/         engine drivers (e.g. local_log_tail)
│   ├── agentfamilies/   engine family registry (YAML frame profiles)
│   ├── modes/  policy/  scheduler/  auth/  events/  tmux/  templates/
├── migrations/          numbered SQL schema migrations
└── templates/           bundled agents / prompts / plans / policies / projects
```

## Domain model

The hub owns these primitives; the mobile app reads them as JSON
maps (the hub holds names + events, hosts hold bytes — see the
blueprint's data-ownership law). Canonical definitions are in
`docs/reference/glossary.md`.

| Concept | What it is |
|---|---|
| **Project** | A unit of directed work — owns plans, tasks, runs, documents, deliverables, channels. |
| **Agent** | A spawned engine instance. `kind` = the engine. Lifecycle: pending → running → terminated/crashed/failed → archived. |
| **Steward** | A coordinating agent (`kind` starts with `steward.`). General steward = frozen, persistent concierge; domain/project stewards are scoped overlays. |
| **Task** | The first-class unit of steward-dispatched work (ADR-029). Status: todo / in_progress / blocked / done / cancelled. |
| **Session** | The conversational primitive that survives agent respawn. |
| **Host** | A machine running a host-runner. |
| **Run** | A tracked execution (e.g. a training run) with streamed metrics. |
| **attention_items** | The director's inbox — approvals, selects, help requests. |
| **audit_events** | Append-only activity log; backs the Activity feed. |
| **Plan / PlanStep** | A steward-authored decomposition of a project goal. |
| **A2A message** | Agent-to-agent traffic, relayed through the hub. |
| **Document / Deliverable / Artifact** | Agent-produced outputs surfaced for review. |
| **Connection / TmuxSession / TmuxWindow / TmuxPane** | The SSH/tmux breakglass layer. |

## Engines & drivers

Four engines, driven by per-engine drivers across three modes:

- **claude-code** — default driver is **M4** (`LocalLogTailDriver`,
  tails the engine's JSONL transcript).
- **codex** — **M2** ACP driver (`codex app-server` JSON-RPC).
- **gemini-cli** — exec-per-turn-with-resume.
- **kimi-code** — M2 ACP driver.
- **M1** (`PaneDriver`) — keystroke-pumps a tmux pane; the generic
  fallback.

Engine frame profiles are data (`hub/templates/` + `agentfamilies/`
YAML), not Go — a new engine is a YAML file, not a code change.

## Conventions

- **English only** — all code, comments, and docs.
- **Docs** follow `docs/doc-spec.md`; read `docs/README.md` first.
  Reorgs go in their own `docs:`-prefixed commits.
- **Glossary first** — `docs/reference/glossary.md` is canonical for
  every collision-prone term; `scripts/lint-glossary.sh` enforces it.
- **ADRs** are append-only and numbered (`docs/decisions/NNN-*.md`).
- **Changelog** — one section per tagged release, Keep-a-Changelog
  format (`docs/changelog.md`).
- Behaviour is data: project/agent/prompt/plan/policy templates are
  editable YAML — no code change to add a new agent kind.
