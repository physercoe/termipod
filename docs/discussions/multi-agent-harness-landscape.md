# Multi-agent harness landscape

> **Type:** discussion
> **Status:** Open (2026-05-28) — landscape capture; one borrow shipped (v1.0.723); two new entries added in this revision (OpenBMB/PilotDeck, asheshgoplani/agent-deck); other borrows remain unselected
> **Audience:** contributors
> **Last verified vs code:** v1.0.723
> **Freshness:** snapshot (refresh when a new harness crosses ~10k stars or shifts architecture)

**TL;DR.** Five open-source multi-agent harnesses are pulling away
from the pack in mid-2026: **code-yeongyu/oh-my-openagent** ("omo",
~60k stars, TS plugin on top of OpenCode), **asheshgoplani/agent-deck**
(~2.5k stars, Go tmux harness whose "conductor" persistent agent +
Telegram/Slack bridge is the closest coordination twin to TermiPod's
principal+steward archetype), **HKUDS/OpenHarness** (~13k stars,
Python full harness with the Ohmo personal agent), **OpenBMB/PilotDeck**
(~387 stars, TS LLM-API harness whose white-box memory + smart routing
are the most interesting design lens), and **AndreBaltazar8/artificial**
(39 stars, Go hub-and-spoke orchestrating Claude Code / Codex / ACP —
the closest architectural twin). The pattern that is winning is
**"harness on top of a stochastic-executor engine"** — exactly
TermiPod's architecture. We are not behind the frontier; we are in
the same lane. Verifying §7.0's "cross-host file sharing" claim
against the codebase surfaced a half-implemented state (producer
side worked, consumer side dropped file parts and had no read tool);
**v1.0.723 closed that gap** by adding `blob_get` and lifting the
A2A text-only render gate. The earlier Tier A borrow candidates
(per-agent fallback model chains, deep-introspection `doctor`) remain.
**Three new Tier B candidates** surface from this revision: stuck-
session heartbeat nudges (agent-deck's 15-minute idle escalation),
fork-with-history session branching (agent-deck), and white-box memory
editing as a design lens for ADR-029 tasks + ADR-032 envelopes
(PilotDeck). The hook-taxonomy framing still flags an under-built
recovery layer that v1.0.711 (`a2aPosterTap` mask) and v1.0.722
(recursive disconnect) both pointed at. OAuth-enabled MCP, auto-
compaction, and the omo mailbox three-state lifecycle remain
deferred. This doc inventories the landscape and ranks what to
borrow; concrete picks belong in follow-up plans or ADRs.

Companion to [integrating-open-source-agents.md](integrating-open-source-agents.md),
which asks "can these engines drop into our [driving modes](../reference/glossary.md#driving-mode)
M1/M2/M4?" — this doc asks the inverted question: *what design ideas
from their harness layer should we borrow into ours?*

---

## 1. The five projects

| Project | Stars | Layer | Built on | License | Tech |
|---|---|---|---|---|---|
| **code-yeongyu/oh-my-openagent** ("omo") | ~60k | Plugin on top of OpenCode CLI | OpenCode | SUL-1.0 | TypeScript / Bun |
| **asheshgoplani/agent-deck** | ~2.5k | tmux harness + persistent conductor + bridge | tmux + PTY over CC/Codex/Gemini/OpenCode/Cursor/Copilot | MIT | **Go** |
| **HKUDS/OpenHarness** ("ohmo") | ~13k | Full harness with React/Ink TUI | own engine loop | MIT | Python (94%) |
| **OpenBMB/PilotDeck** | ~387 | LLM-API harness with WorkSpace isolation | OpenAI-compatible / Anthropic / DeepSeek / Qwen / Kimi / MiniMax APIs | AGPL-3.0 | TypeScript |
| **AndreBaltazar8/artificial** | 39 | Hub-and-spoke orchestrator | wraps engines via PTY + ACP | MIT | **Go** |

**omo is still the deepest to study.** It is the most mature, ships
the most concretely-specified team-coordination protocol, and its
problem domain maps closest to ours. **agent-deck is the closest
coordination twin** — its persistent "conductor" agent that watches
children, auto-handles routine cases, and escalates uncertain ones to
a Telegram/Slack bot is structurally the same shape as our
principal+[steward](../reference/glossary.md#steward) archetype,
except its cockpit is a chat bot and ours is a Flutter app.
**Artificial is the closest architectural twin** — Go central
service, WebSocket workers, kanban board, terminal streaming — and is
worth a read precisely because the design constraints map onto our
[hub](../reference/glossary.md#hub) +
[host-runner](../reference/glossary.md#host-runner) split.
**OpenHarness leads on long-session survival** (auto-compaction
preserving task state + channel logs), which is acute for us because
the mobile cockpit makes "it broke at midnight" more visible.
**PilotDeck pushes in a different direction** — wrapping LLM APIs
directly rather than CLI engines — but its white-box memory pattern
(visible, editable agent memory) is a design lens worth carrying when
ADR-029 tasks and ADR-032 envelopes evolve.

---

## 2. omo — what it is, in one screen

**Plugin on top of OpenCode.** It does not ship its own LLM client;
it bolts onto OpenCode the way a Claude Code plugin does to Claude
Code. Two layers:

1. **Agents** — named personas with a model + prompt + tool gates:
   Sisyphus (orchestrator, opus-4.7), Hephaestus (deep worker,
   gpt-5.5), Prometheus (planner, "interview mode"), Atlas (todo
   executor, sonnet-4.6), Oracle (read-only review), Librarian
   (multi-repo lookup), Explore (fast grep), Sisyphus-Junior (cannot
   re-delegate). 11 in total.
2. **Team mode** — 1 lead + up to 8 parallel members coordinated via
   a filesystem mailbox, a file-locked task list, optional per-member
   git [worktrees](../reference/glossary.md#worktree), and a tmux
   pane layout that streams each member's TUI output.

Numbers worth knowing: 54 base hooks + 7 with team mode = 61 total ·
20–39 tools depending on config gates · 11 agents · 6 model
"categories" · MCP loader has 3 tiers. 6.9k commits on `dev` with
~daily releases; v4.5.1 landed 2026-05-26.

### 2.1 Team-mode filesystem protocol

Storage tree under `~/.omo/teams/{name}/` or
`<project>/.omo/teams/{name}/`:

```
config.json              ← team spec
state.json               ← runtime state
mailbox/
  inboxes/{member}/{uuid}.json                ← unread message
  inboxes/{member}/.delivering-{uuid}.json   ← in-flight delivery
  inboxes/{member}/processed/                ← acknowledged
tasklist.jsonl
worktrees/                                   ← per-member git worktrees
tasks/{id}.json
```

**Three-state mailbox lifecycle** (the load-bearing piece):

1. *Unread* — `{uuid}.json` exists.
2. *Delivering* — system writes `.delivering-{uuid}.json` as a
   reservation token while `promptAsync` runs.
3. *Processed or released* — success moves to `processed/`; failure
   reverts to `{uuid}.json`.

Crash recovery: stranded `.delivering-*` files reclaimed after a
**10-minute TTL**. `listUnreadMessages` ignores dotfiles, preventing
double-injection during poll fallback.

**Size discipline**: `message_payload_max_bytes` default 32 KB,
`recipient_unread_max_bytes` default 256 KB per member,
`mailbox_poll_interval_ms` default 3000.

**Shutdown protocol** (4-phase): lead calls `team_shutdown_request`
→ member or lead calls `team_approve_shutdown` /
`team_reject_shutdown` → `team_delete` rejects teams with active
members → per-member shutdown closes one pane;
`team_delete` tears down all worktrees + state.

**Team-tool surface** (12 tools, gated on `team_mode.enabled`):
`team_create`, `team_delete`, `team_shutdown_request`,
`team_approve_shutdown`, `team_reject_shutdown`,
`team_send_message`, `team_task_create`, `team_task_list`,
`team_task_update`, `team_task_get`, `team_status`, `team_list`.

**Lead vs member capability split**:
- *Lead* — broadcast, create/update tasks, request shutdown of any
  member, see aggregate status.
- *Member* — peer-to-peer messages, claim/update tasks, respond to
  shutdown. **No nested team creation, no member-driven delegate.**

**Bounds** (all configurable): `max_parallel_members` (4, hard cap 8),
`max_wall_clock_minutes` (120), `max_member_turns` (500),
`max_messages_per_run` (10,000).

**No synchronous reply-wait** — messaging is fire-and-forget.
Completion is signaled implicitly by responding to a shutdown
request.

### 2.2 Hook taxonomy (54 + 7)

The categorization is more interesting than the individual hooks.
Six event types (`PreToolUse / PostToolUse / Message / Event /
Transform / Params`) collapse into five functional categories:

| Category | Representative hooks | What it solves |
|---|---|---|
| **Context injection** | `directory-agents-injector` (walks AGENTS.md from file → root), `directory-readme-injector`, `rules-injector` (`.claude/rules/*.mdc` with glob frontmatter) | Stacking context without manual prompting |
| **Productivity** | `keyword-detector` (triggers `ultrawork`/`ulw`/`search`/`analyze`/`team` modes), `think-mode`, `ralph-loop` | Mode-switching from prose |
| **Quality & safety** | `comment-checker` (blocks AI-slop comments; bypass `// @allow`), `thinking-block-validator`, `write-existing-file-guard`, `hashline-read-enhancer` | Pre-/post-emit invariants |
| **Recovery** | `session-recovery`, `runtime-fallback`, `anthropic-context-window-limit-recovery`, `json-error-recovery` | Self-healing |
| **Task management** | `tasks-todowrite-disabler`, `todo-continuation-enforcer` (yanks idle agents back), `empty-task-response-detector` | Prevent agent idling |

`disabled_hooks: ["comment-checker"]` opts out.

### 2.3 MCP three-tier loader

| Tier | Location | What | In `opencode mcp list`? |
|---|---|---|---|
| 1 — Built-in | runtime-injected | `websearch` (Exa), `context7` (docs), `grep_app`, `lsp`, `ast_grep` | **No** — only via `doctor --verbose` |
| 2 — Claude Code loader | `~/.claude.json`, `~/.config/opencode/.mcp.json`, `.mcp.json`, `.claude/.mcp.json` with `${VAR}` expansion | User's existing MCP config | Yes |
| 3 — Skill-embedded | declared in skill frontmatter | Scoped to a single skill, unloaded when skill exits | Per-skill |

**OAuth-enabled MCP servers**:
- Discovery via **RFC 9728** (Protected Resource Metadata) + **RFC 8414** (Authorization Server Metadata).
- **RFC 7591** dynamic client registration.
- **PKCE mandatory.**
- Auto-refresh on 401, token persistence at
  `~/.config/opencode/mcp-oauth.json` (chmod **0600**).
- Pre-authenticate via
  `bunx oh-my-opencode mcp oauth login <name> --server-url ...`.

### 2.4 Other notables

- **Categories** — 6 preset agent configs (`visual-engineering`,
  `ultrabrain`, `deep`, `artistry`, `quick`, `writing`); composable
  as `{model, temperature, prompt_append, thinking, tools,
  fallback_models}`. User-defined categories slot in.
- **Hashline edit** — lines tagged `11#VK| function hello()` where
  ID alphabet is `ZPMQVRWSNKTXJBYH`. Engine-side; not our layer.
- **Ralph loop** (`/ralph-loop`) — auto-iterates until
  `<promise>DONE</promise>` marker. Default max 100 iterations.
- **Fallback model chains, per agent**:
  ```json
  "fallback_models": [
    "opencode/glm-5",
    {"model": "openai/gpt-5.5", "variant": "high"},
    {"model": "anthropic/claude-sonnet-4-6",
     "thinking": {"type": "enabled", "budgetTokens": 64000}}
  ]
  ```
- **File-based prompts** — `"prompt": "file:///path/to/custom.md"`
  with `~` expansion. `prompt_append` lets you compose.
- **Doctor command** — `bunx oh-my-opencode doctor` checks tmux/git
  availability, lists declared teams, surfaces runtime-injected MCPs.
- **OpenClaw** — bidirectional bridges to Discord, Telegram, HTTP,
  shell, with reply-listener daemon. Conceptually overlaps with our
  mobile-cockpit but the wire shape is webhooks not gRPC/MCP.

---

## 3. OpenHarness — long-session survival as headline

Python (94%), MIT, ~13k stars. Ships **Ohmo**, a personal agent that
integrates Feishu / Slack / Telegram / Discord using existing Claude
Code or Codex *subscriptions* (no API keys required). Ten subsystems
(Engine, Tools, Skills, Permissions, Memory, Commands, Plugins, MCP,
Coordinator, UI). 43+ tools, 54 commands.

**The differentiator**: auto-compaction preserves task state and
channel logs across context compression — agents run multi-day
sessions without manual `/compact` or `/clear`. They sell it as
the headline feature for a reason.

`channel logs` is a primitive worth understanding: communication
endpoints (Slack thread, Discord channel, Feishu session) where Ohmo
receives messages and maintains conversation history as part of
workspace persistence. Closest TermiPod analog is
[A2A](../reference/glossary.md#a2a-relay) plus the mobile transcript
view — but ours doesn't currently survive `/clear` cleanly.

---

## 4. Artificial — the closest architectural twin

**Go**, MIT, 39 stars. Hub-and-spoke design:
- **Central service** (`svc-artificial`) — dashboard, REST API,
  WebSocket hub, SQLite database.
- **Worker processes** (`cmd-worker`) — individual agent instances
  communicating via WebSocket.
- **Shared protocol layer** (`pkg-go-shared`) — type definitions
  across components.

Supported backends: Claude Code (PTY + MCP tools), OpenAI Codex
(PTY), [ACP](../reference/glossary.md#driving-mode) (Cursor Agent,
opencode), local via OpenAI-compatible APIs (LM Studio, Ollama).

Web dashboard provides chat, kanban task management, team
visualization, and real-time terminal output streaming. Worth a deep
read precisely because the design constraints — Go central service,
WebSocket workers, multi-engine via PTY + ACP — map almost 1:1 onto
ours. They are 39 stars and we are not racing them on adoption;
we are racing on engineering clarity, and a read of their handlers
would surface convergent or divergent decisions cleanly.

---

## 5. PilotDeck — white-box memory & smart routing

**TypeScript**, AGPL-3.0, ~387 stars, by OpenBMB (the lab behind
ChatDev / MiniCPM). Tagline: *"Task-oriented AI Agent productivity
platform — redefining operational boundaries and memory evolution,
one WorkSpace at a time."* Stack: central Gateway (Node.js) + spoke
agents per WorkSpace; HTTP/REST + WebSocket + filesystem + native
MCP; YAML config at `~/.pilotdeck/pilotdeck.yaml` + per-WorkSpace
filesystem isolation. Latest release v0.0.11 (2026-05-27).

**Critical: it wraps LLM APIs (Claude, DeepSeek, Qwen, Kimi,
MiniMax) directly — not CLI engines.** That's a different lane from
us — closer to a Cline/Cursor alternative than to a Claude-Code-or-
Codex harness. So the borrows from PilotDeck are *design lenses*,
not drop-in components.

### 5.1 What's distinctive

- **White-Box Memory.** *"Memory generation, extraction, storage and
  retrieval are visible end-to-end. When AI mis-remembers, pinpoint
  and fix the offending entry."* Most agent harnesses keep memory
  opaque — a blob in a context window or a vector store; PilotDeck
  exposes it as inspectable entries the user can directly edit. This
  is the most interesting pattern in the project for us; it lands as
  a Tier B borrow (§7.2 B6).
- **Smart Routing.** Automatic task-difficulty detection: complex
  calls route to flagship models, simple ones drop to lighter
  models. Demonstrated 70% cost savings on social-media workloads
  and 1/6 cost vs. frontier models on hard tasks. Pairs with
  manually-configured fallback chains.
- **WorkSpace isolation.** Per-project filesystem + memory + skills,
  preventing "projects bleed into each other" context pollution.
  TermiPod's project primitive already does most of this; PilotDeck's
  framing reinforces that the per-project boundary should also bind
  memory, not just files.
- **Dream Mode.** Auto-compaction in idle windows — same family as
  OpenHarness's auto-compaction.
- **Hooks.** `PreToolUse`, `UserPromptSubmit`, and other lifecycle
  interception points (an omo-like taxonomy, but TS-native).
- **ClawHub.** Community skills marketplace. Open question whether
  this maps to our agent-kind YAML or to a separate primitive.
- **Always-On Background Execution.** *"After you sign off, the agent
  keeps discovering candidate tasks, running long-horizon monitors,
  and lands deliverables as local files."* Strongly resonates with
  TermiPod's mobile-cockpit positioning.
- **Cross-frontend consistency.** README claims "behaves consistently
  across front-ends: Web / CLI / IM" — but no explicit Slack/Discord
  bridges are documented.
- **Desktop app.** macOS ARM64 + Windows x64/ARM64 packaged
  releases.
- **Rollback.** *"One-click rollback to the prior state"* — a session-
  level undo primitive we don't have.

---

## 6. agent-deck — the conductor pattern & phone bridge

**Go (86%)**, MIT, ~2.5k stars (295 forks), by asheshgoplani.
Tagline: *"Your AI agent command center."* Latest release v1.9.42
(2026-05-27). tmux-based TUI + CLI on top of multiple CLI engines:
Claude Code (primary, full integration), Gemini CLI, Codex,
OpenCode, Copilot, Cursor. State in SQLite at
`~/.agent-deck/<profile>/state.db`. Optional web UI at
`http://127.0.0.1:8420` with bearer-token auth and a read-only mode.

**This is the closest coordination twin to TermiPod's
principal+steward archetype.** Worth a deep read because its problem
set overlaps almost completely with ours; the carriers differ but
the shapes match.

### 6.1 The conductor pattern

A *conductor* is a persistent, autonomous Claude or Codex agent
session running in its own tmux pane:

- **Lifecycle.** Created via `agent-deck conductor setup <name>`;
  state at `~/.agent-deck/conductor/<name>/` (identity, config,
  task log); survives across multiple child session lifecycles.
- **Monitoring.** Polls children every ~3 seconds; transitions
  (`running → waiting | error | idle`) trigger a transition
  notifier.
- **Escalation.** If a child enters `waiting` (needs input) or
  `error`, the conductor decides: auto-respond if confident, else
  send a message to the user's phone via a Telegram bot or Slack
  app bridge with a `NEED:` marker.
- **Heartbeat nudge.** Every 15 minutes if a child is stuck in
  `waiting`, the conductor pings to keep the user informed.
- **Routing.** Multi-conductor setups use `name: message` prefixes
  (`ops: worker-1 stuck, restart?` → user replies `ops: yes`).
- **Per-conductor config.**
  `[conductors.<name>.claude].config_dir` + `env_file` lets one
  conductor use a different Claude account than other sessions in
  the same profile.

Compare with TermiPod's
[steward](../reference/glossary.md#steward): both are persistent
supervisors that watch child agents. Substantive differences: (a)
our steward emits
[attention items](../reference/glossary.md#attention-item) routed
via the hub to the mobile cockpit; theirs routes via Telegram/Slack
to a chat bot. (b) Our steward is just another agent under hub
authority; theirs is a special class with bespoke bridge
infrastructure. The *shape* is the same; the carrier is different.

### 6.2 Other distinctive patterns

- **Fork sessions with full history.** Press `f` in the TUI to fork
  a Claude conversation at its current point with full transcript
  inheritance. Forks themselves can be forked (branching
  exploration). Lands as Tier B borrow §7.2 B5.
- **Global search across all conversations.** `G` opens a fuzzy
  search across all Claude transcripts on disk, including closed
  sessions.
- **MCP socket pooling.** Shared Unix-socket proxy for MCP servers
  across sessions; 85-90% memory savings; auto-recovery in ~3
  seconds on MCP crash via a reconnecting proxy.
- **Worktrees with `.worktreeinclude`.** Gitignore-style patterns
  for files to copy into new worktrees (`.env`, `.mcp.json`, custom
  per-project secrets). Post-creation `.agent-deck/worktree-setup.sh`
  runs with `AGENT_DECK_REPO_ROOT` + `AGENT_DECK_WORKTREE_PATH` env
  vars (60-second timeout). Bare-repo support (nested `.bare/` and
  true-bare-at-root layouts) auto-detected.
- **Watchers.** Event-forwarding framework: GitHub webhooks
  (HMAC-SHA256 verified, deduplicated in SQLite by
  `(watcher_name, event_id)` to prevent retry double-fire), ntfy.sh
  push, Slack events, custom HTTP. Inbound events route to
  conductors.
- **Docker sandbox.** Run sessions in isolated containers with bind-
  mounted project dir; host tool auth (API keys, Keychain) auto-
  shared.
- **Skills Manager.** Per-project Claude skills attach/detach via a
  pool-based workflow (`~/.agent-deck/skills/pool/` read-only pool,
  applied via `.agent-deck/skills.toml`, materialized into
  `.claude/skills/`).
- **Socket isolation.** Optional `[tmux].socket_name = "agent-deck"`
  so agent-deck never touches the user's personal tmux.
- **Cost dashboard.** 14 models priced, budget limits, Chart.js +
  SSE live updates in the web UI; `$` key opens the TUI cost view;
  `costs recompute --dry-run` for backfill. Same family as our
  [ADR-036](../decisions/036-claude-code-statusline-telemetry.md)
  telemetry but explicitly user-facing.
- **Remote SSH.** `agent-deck remote add <name> user@host` registers
  remote instances; `remote sessions` browses across all remotes;
  StrictHostKeyChecking enforced. Multi-host but via SSH-poll rather
  than a hub-mediated relay.
- **Feedback hook.** `Ctrl+E` posts feedback to a GitHub Discussion;
  paced prompts (first after 7 launches or 3 days, max 3 per
  version).

### 6.3 Where agent-deck differs from TermiPod

- **No relay.** Multi-host is SSH-based polling; we use the
  hub-mediated reverse-tunnel relay, which lets NAT'd hosts host
  workers.
- **No mobile app.** Telegram/Slack chat bots are the away-from-
  keyboard carrier; our Flutter app is the cockpit.
- **No bytes-on-hosts split.** State lives in SQLite + tmux + a web
  server; we split hub-as-authority + host-runner-as-spawner +
  bytes-where-they-live per [blueprint](../spine/blueprint.md) §3.2.
- **TUI-first ergonomics.** Their TUI keybinds (`f`, `G`, `s`, `m`,
  `$`) are first-class; ours is mobile-first with `hub-tui` as a
  fallback.

---

## 7. What's borrowable for TermiPod

Ranked by *fit × leverage × cost*.

### 7.0 Grounding — what already works cross-host

Before reading the borrow list it helps to know what cross-host
coordination TermiPod already supports today. Two facts that change
which pieces are urgent:

- **Cross-host project membership works.** `agents.project_id`
  (`hub/migrations/0040_agents_project_id.up.sql:18`) and
  `agents.host_id` (`hub/migrations/0001_initial.up.sql:44`) are
  independent columns; no constraint binds a project to a single
  host. Two stewards under the same project on different hosts is a
  supported configuration — A2A through the hub's reverse-tunnel
  relay carries their messages.
- **Cross-host file sharing works end-to-end at the agent layer for
  ≤25 MiB via the hub blob store** (closed in **v1.0.723**).
  Producer side: `attach` MCP tool or `<<mcp:attach {"path":...}>>`
  pane marker puts bytes at `<DataRoot>/blobs/<aa>/<bb>/<sha>`
  (`hub/internal/server/handlers_blobs.go:22-65`). Consumer side:
  `blob_get` MCP tool reads the bytes by sha or by full URI
  (`hub/internal/server/mcp_more.go:mcpGetBlob`), and
  `renderInboundParts` (`hub/internal/hostrunner/a2a_dispatcher.go`)
  surfaces file parts to the receiving agent as
  `[file: <uri> (<mime>, <size> bytes)]` lines instead of dropping
  them. Pre-v1.0.723 the consumer side was missing both pieces, so
  the path was half-implemented — bytes reached the hub but the
  receiving agent had no way to act on them. Above 25 MiB the
  design slot exists (blueprint §4: hub holds references, hosts
  hold bytes) but the host-runner serve-bytes endpoint is not
  implemented yet.

These two facts together make cross-host coordination viable at
the agent layer today — both messaging and bounded file transfer
work without operator-level intervention.

### 7.1 Tier A — borrow soon, well-shaped pieces

**A1. Per-agent fallback model chains** with variant + thinking
config. `agent_families.yaml` drives engine selection but lacks
graceful degradation. Lands as a new `fallback_models:` key in
`hub/internal/agentfamilies/agent_families.yaml`, consumed by
`hub/internal/hostrunner/launch_*.go`. Pairs naturally with the
allowlist-over-denylist discipline already documented in
[consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md).

**A2. Doctor command (`hub doctor --verbose`).** We have `/health`
but no deep introspection that lists *(runtime-injected MCPs / loaded
YAML profiles / spawned agents / tmux pane status / migration
version)*. Lands as `hub/cmd/hub-server/doctor.go` or as a
[hub-tui](../reference/glossary.md#host-runner) screen. Cheap;
immediately useful when triaging mobile reports of "it isn't
working."

> **Removed from Tier A in this revision.** The earlier draft listed
> *Mailbox three-state lifecycle for A2A* (unread / delivering /
> processed with `.delivering-{uuid}` reservation + 10-minute TTL
> reclamation) as A1. Its urgency was tied to the half-implemented
> cross-host file path described in §7.0. That gap was closed by
> v1.0.723 with a different mechanism — adding the agent-side blob
> read tool, not by reshaping envelope durability. Without that
> motivation, the mailbox lifecycle is a defensive pattern without
> a real incident to justify it; it moves to Tier C until one
> lands.

### 7.2 Tier B — borrow with adaptation

**B1. Hook taxonomy as a design lens.** Not the hooks themselves,
but the **five-category framing** (Context injection / Productivity
/ Quality & safety / Recovery / Task management). Map our existing
surfaces:

- Context injection — [ADR-030](../decisions/030-governed-actions-and-propose-verb.md)
  governed actions, profile rules in `agent_families.yaml`.
- Productivity — ADR-032 envelopes, /loop skill.
- Quality & safety — `lint-glossary.sh`, `lint-docs.sh`,
  `lint-doc-anchors.sh`.
- **Recovery — we are underweight here.** The v1.0.711
  `a2aPosterTap` masking and v1.0.722 recursive disconnect both
  point at the same gap.
- Task management —
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) tasks.

A single page in [spine/](../spine/) mapping our surfaces to these
categories would surface what is missing.

**B2. Categories with `prompt_append` composition.** We have agent
kinds but no first-class "category overlay" that says "this is a
quick task, use the fast model, add this prose." Mobile already
shows `permission_mode` and `effort`; a category overlay would slot
beside them. Sites: `hub/internal/agentfamilies/agent_families.yaml`,
mobile session-details sheet.

**B3. File-based prompt loading with `prompt_append`.** Making
`prompt: file://...` and `prompt_append: file://...` first-class
composition primitives in the template loader would let users
override one section without forking a whole prompt. Cheap.

**B4. Stuck-session heartbeat nudge.** agent-deck's 15-minute idle
escalation is a missing surface for us. If an agent has been in a
`waiting`/`needs-input`-shaped state — or, for us, an
[attention card](../reference/glossary.md#attention-item) has been
open — for more than N minutes, post a heartbeat reminder back to
the cockpit. Sites: `hub/internal/server/handlers_attention.go`
for the polling logic, mobile attention surfaces for the rendering.
Tiny LOC; immediately useful for the "it broke at midnight" case
OpenHarness markets. The signal feeds the same mobile cockpit
ADR-036 telemetry already lights up.

**B5. Fork session with full history.** agent-deck's `f`/`F` lets
you branch a Claude conversation at its current point with full
transcript inheritance — useful for "try alternative B without
losing alternative A." TermiPod has *respawn* (carries the engine
thread forward) but not *fork* (creates a sibling sharing history
up to a point). Sites: `hub/internal/server/handlers_sessions.go`
for a fork endpoint, mobile session-details sheet for the action.
Implementation question: what to do with downstream effects
(artifacts, attention items, A2A receipts) — copy, share, or
split. Defer until B4 lands so the surface is warm.

**B6. White-box memory editing as a design lens.** PilotDeck's
memory pipeline is visible and editable end-to-end. We don't have
a single "agent memory" primitive — closest analogs are
[task](../reference/glossary.md#task) descriptions,
[envelope](../reference/glossary.md#envelope) payloads, and
[ADR-029](../decisions/029-tasks-as-first-class-primitive.md)
status notes — but the framing is worth carrying when those
surfaces evolve. Ask: can the director directly edit what the agent
"believes" about a task? Today, no — the agent rewrites it; the
director can only approve or comment. PilotDeck makes the case that
this is a UX gap, not a safety feature. Not a wedge yet; a lens
for future ADR-029 / ADR-032 evolution.

### 7.3 Tier C — watch, don't copy

- **Mailbox three-state lifecycle for A2A envelopes** (omo's
  `unread / .delivering-{uuid} / processed/` shape with a 10-minute
  TTL reclamation pass). A defensive durability pattern;
  [ADR-032](../decisions/032-message-routing-envelope.md) envelopes
  carry sender stamping but no in-flight state that survives
  host-runner crash. Demoted from Tier A in the v1.0.723 revision
  — its urgency was tied to the now-closed cross-host file path
  (§7.0). Re-open if a stranded-envelope incident lands; the borrow
  cost is one new package under `hub/internal/mailbox/` (or
  additional state on the existing A2A envelope record) plus a
  reaper goroutine for the TTL sweep.
- **Auto-compaction preserving task state** (OpenHarness's
  headline). claude-code and codex both ship their own
  context-compaction surfaces (`/compact`, automatic on context
  pressure) which already preserve the engine record; layering a
  hub-side carry-forward on top is not urgent. Re-open if a real
  user-visible "tasks vanished after compaction" incident lands.
- **OAuth-enabled MCP servers** (RFC 9728 + 8414 + 7591 + PKCE +
  auto-refresh + 0600 token store). Deferred to post-MVP — adds
  weeks of spec implementation to support a tier-3 MCP ergonomics
  that bring-your-own-bearer already covers for our user shape.
- **Hash-anchored edit (Hashline)** — engine-side; Claude Code's
  Edit tool already does this. Not our layer.
- **Ralph loop completion marker** — we have `/loop` with cron +
  dynamic modes; `<promise>DONE</promise>` is one specific
  convention, not architecture.
- **OpenClaw external bridges** (Discord / Telegram) — our mobile
  app *is* the cockpit; webhook bridges would be a different product
  (and they collide with the principal/director archetype called out
  in [integrating-open-source-agents.md](integrating-open-source-agents.md) §3).
- **Bun-only TypeScript runtime** — ecosystem mismatch; hub is Go
  for the reasons in [blueprint.md](../spine/blueprint.md).
- **PilotDeck Smart Routing** (auto-detect task difficulty, route
  to model tier). Cheap-fallback chains (Tier A1) cover the
  under-provisioned case; the over-provisioned case (flagship on
  trivial work) is real but less acute for our usage shape today.
  Revisit when cost telemetry surfaces high spend on trivial turns.
- **PilotDeck Dream Mode** (idle-window auto-compaction). Same
  family as OpenHarness's auto-compaction — engine-native
  `/compact` already handles the user-visible case.
- **PilotDeck rollback** ("one-click rollback to prior state"). Our
  respawn + thread-resume already carries forward; reverting *to a
  past state* is a different action. Worth reading their
  implementation if/when a session-level undo surface lands here.
- **agent-deck MCP socket pooling** (85-90% memory savings via
  Unix-socket proxy). A scale optimization. Revisit when fleet
  size makes per-agent MCP processes the dominant memory cost.
- **agent-deck Watchers** (GitHub HMAC webhooks → conductor; ntfy
  / Slack inbound). We route external events differently (mobile
  push, hub-mediated). Worth the read for the SQLite
  `(watcher_name, event_id)` deduplication pattern, which is
  generalizable to any retry-prone inbound channel.
- **agent-deck per-group / per-conductor multi-account auth**
  (`[conductors.<name>.claude].config_dir` overrides). Useful when
  one director runs work + personal fleets against different Claude
  accounts. Sized as a small `agents.auth_profile` column +
  launch-path read; defer until a real user-shape forces it.
- **agent-deck `.worktreeinclude` + post-create setup script.**
  We have workdir derivation but no per-project hook for "copy
  these files / run this script before the agent starts." A small
  ergonomics borrow when worktree-as-default lands.

### 7.4 Tier D — already have, no work needed

- Per-agent tmux panes — host-runner owns them,
  [ADR-027](../decisions/027-local-log-tail-driver.md) LocalLogTailDriver tap.
- YAML-driven engine behavior — `agent_families.yaml`.
- Sender-stamped A2A envelopes — ADR-032.
- Status-line telemetry —
  [ADR-036](../decisions/036-claude-code-statusline-telemetry.md).
- Multi-engine support — claude-code / codex / gemini-cli /
  kimi-code.
- Tasks first-class — ADR-029.
- Attention / policy — ADR-030.
- Persistent supervisor agent watching children — our steward
  pattern is structurally agent-deck's conductor pattern (§6.1);
  the design exists, the carrier differs.
- Away-from-keyboard escalation for stuck agents — our attention
  items + mobile cockpit do this end-to-end; agent-deck's
  Telegram/Slack bot is one specific carrier.
- Cost telemetry — ADR-036 statusLine sweeps usage cents per session
  and surfaces them in the mobile cost chip; agent-deck's web cost
  dashboard is the same data, different surface.

---

## 8. The frontier signal

Three things to take from the velocity, separate from the technical
specifics:

1. **The "harness on top of an open agent" pattern is winning.** omo
   wraps OpenCode; OpenHarness wraps Claude Code / Codex
   subscriptions; Artificial wraps via PTY. The harness layer is
   where coordination, recovery, and policy live — the agent stays a
   stochastic executor. This is exactly TermiPod's architecture; we
   are in the same lane, not behind it.

2. **Filesystem-as-protocol is becoming the default for inter-agent
   state.** omo's mailbox is JSON files on disk with
   `.delivering-` reservations. OpenHarness's memory is markdown
   files on disk. We picked hub-as-authority + bytes-on-hosts (per
   blueprint §3.2) — a different trade-off (concurrency, multi-host,
   audit) worth defending. The crash-recovery piece of the
   filesystem pattern (envelope-durability state machine) is
   captured as a Tier C item for now (§7.3); revisit if a real
   stranded-envelope incident lands rather than borrowing it
   preemptively.

3. **Long-session survival is a shared frontier.** OpenHarness
   leads with auto-compaction; omo has session-recovery +
   intelligent compaction; PilotDeck has Dream Mode idle compaction.
   Mobile-first makes this acute for us in theory — but claude-code
   and codex both ship their own `/compact` surfaces today, so the
   user-visible gap is smaller than the marketing suggests.
   Re-evaluate if/when a real "context-pressure lost my tasks"
   incident lands; until then the engine-native compaction does the
   work.

4. **Mobile / IM escalation is now table stakes.** Three of the five
   projects ship a carrier to take stuck-session messages off the
   terminal: agent-deck (Telegram bot, Slack app, ntfy.sh push),
   PilotDeck (claimed Web / CLI / IM consistency), and TermiPod
   (Flutter app). The harness owning the away-from-keyboard channel
   is no longer differentiation. The differentiation is the
   *richness* of that channel — read-only chat bot vs. interactive
   cockpit with attention cards, voice input, A2A relay. Our mobile
   app is the strongest of the five along that axis today; the risk
   is letting agent-deck's chat-bot ergonomics close the gap via
   richer slash commands before our mobile UX hardens.

5. **Cost-aware routing is becoming default.** PilotDeck Smart
   Routing (auto-route by task difficulty) + agent-deck cost
   dashboard + omo's per-agent fallback chains all converge on "the
   harness decides which model gets the call." We have
   [ADR-036](../decisions/036-claude-code-statusline-telemetry.md)
   telemetry that captures spend post-hoc; the routing decision is
   still pinned by `agent_families.yaml`. Per-agent fallback chains
   (Tier A1) closes the under-provisioned case; routing-by-
   difficulty closes the over-provisioned case. Both are post-MVP
   unless cost telemetry surfaces a hot spot.

6. **White-box state is the next ergonomics frontier.** PilotDeck
   makes the loudest case (visible, directly-editable memory), but
   the tension is present across all five: agent-deck exposes task
   logs as markdown files; omo's mailbox is JSON files on disk;
   OpenHarness's memory is markdown files. The black-box LLM is the
   stochastic core; the harness layer is being pushed to make
   *everything around it* inspectable and editable by the director.
   ADR-029 tasks + ADR-032 envelopes already point this direction;
   the question is whether we make that pull explicit (B6 in §7.2).

---

## 9. Open questions

- Should we cut a quarterly refresh cycle for this doc (status
  flipped to *Stale* every 90 days unless re-verified)? The
  landscape moved enough between [integrating-open-source-agents.md](integrating-open-source-agents.md)
  (2026-04-30) and this doc (2026-05-27) that a stale-by-default
  policy would be honest.
- If/when the mailbox three-state lifecycle (§7.3) gets reopened by
  a real incident, does it warrant its own package
  (`hub/internal/mailbox/`), or does it live as additional state on
  the existing A2A envelope record? The former is cleaner; the
  latter is closer to ADR-032's "envelope as durable record"
  framing.
- When auto-compaction (§7.3) eventually gets revisited, what is
  the right shape for the carry-forward blob — JSON dropped into
  the next system prompt, or a synthesized "previous session
  summary" appended to the YAML profile's `prompt_append`? The
  latter generalizes; the former is faster to ship.
- If B4 (stuck-session heartbeat nudge) lands, what's the right
  cadence: agent-deck's 15-minute fixed nudge, or an exponential
  backoff (5m → 15m → 60m) that respects the director's sleep?
  Mobile-first answers this differently from chat-bot-first.
- B6 (white-box memory editing) is framed as a *design lens*; what
  would the actionable next step be — an ADR sketch, a discussion
  prompt to the principal, or a small spike on rendering envelope
  payloads as inline-editable rows? The lightest of those is the
  prompt.

---

## 10. Sources

- code-yeongyu/oh-my-openagent — https://github.com/code-yeongyu/oh-my-openagent
- omo features reference — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/docs/reference/features.md
- omo AGENTS.md (dev) — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/AGENTS.md
- omo team-mode guide — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/docs/guide/team-mode.md
- asheshgoplani/agent-deck — https://github.com/asheshgoplani/agent-deck
- HKUDS/OpenHarness — https://github.com/HKUDS/OpenHarness
- OpenBMB/PilotDeck — https://github.com/OpenBMB/PilotDeck
- AndreBaltazar8/artificial — https://github.com/AndreBaltazar8/artificial
- ComposioHQ/agent-orchestrator — https://github.com/ComposioHQ/agent-orchestrator
- Microsoft Conductor announcement (2026-05-14) — https://opensource.microsoft.com/blog/2026/05/14/conductor-deterministic-orchestration-for-multi-agent-ai-workflows/
- Oh My OpenAgent landing — https://ohmyopenagent.com/

---

## 11. Related

- [integrating-open-source-agents.md](integrating-open-source-agents.md)
  — companion doc; asks the inverted question (can these engines be
  drop-in [driving-mode](../reference/glossary.md#driving-mode)
  M1/M2 engines?).
- [codex-m2-app-server-surface-audit.md](codex-m2-app-server-surface-audit.md)
  — recent example of source-grounded audit before borrowing.
- [consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)
  — the allowlist-over-denylist discipline A1 (fallback model
  chains) should follow.
- [ADR-027](../decisions/027-local-log-tail-driver.md),
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md),
  [ADR-030](../decisions/030-governed-actions-and-propose-verb.md),
  [ADR-032](../decisions/032-message-routing-envelope.md),
  [ADR-036](../decisions/036-claude-code-statusline-telemetry.md) — the
  existing primitives Tier A/B borrows would extend.
