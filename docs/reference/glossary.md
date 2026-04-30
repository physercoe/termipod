# Glossary

> **Type:** reference
> **Status:** Current (2026-04-30)
> **Audience:** contributors (humans + AI)
> **Last verified vs code:** v1.0.349

**TL;DR.** Canonical definitions for every project-specific term that
has more than one possible meaning, or whose meaning is non-obvious
to a reader cold-opening the codebase. The unit of truth: when a doc
or commit message uses one of these terms, this file is what it
means. If you discover a new collision or ambiguous usage, add an
entry here in the same change.

This is **not** the design-vocabulary doc — `vocabulary.md` is the
swappable role-bound vocab axes for post-MVP theme packs. They
coexist; this one is fixed engineering terminology, that one is
intentionally swappable.

---

## How to use this doc

- **Reading.** If a term in another doc feels overloaded, search
  here. Every entry has a one-line definition, a *Distinguish from*
  line for known collisions, and a link to the canonical concept doc
  for depth.
- **Writing.** When you introduce a project-specific term in a doc
  or commit, link to its glossary entry on first use, e.g.
  `[hub session](../reference/glossary.md#hub-session)`. If the term
  isn't here yet, add it in the same commit.
- **Disambiguation rule.** If a sentence reads ambiguously without a
  qualifier, add one. "Session" almost always needs to be *hub
  session* or *engine session*; "kind" almost always needs to be
  *agent kind*, *event kind*, *input kind*, or *attention kind*.

---

## 1. The big picture (one diagram)

```
                +-------------------+
   principal -> |   mobile / web    |
   (human)      +---------+---------+
                          | HTTPS + SSE
                          v
                +-------------------+        +-----------------+
                |       hub         |<------>|  hub DB (sqlite) |
                |  (Go server)      |        |  agent_events    |
                |                   |        |  sessions        |
                |  REST + SSE +     |        |  agents          |
                |  MCP gateway      |        |  attention_items |
                +---------+---------+        +-----------------+
                          | host-runner protocol
                          v
                +-------------------+
                |   host-runner     |  per host
                |  (Go daemon)      |
                +---------+---------+
                          | spawns + drives
                          v
                +-------------------+        +-----------------+
                |   engine process  |<------>|  engine record  |
                |  (claude / codex  |        |  ~/.claude/...  |
                |   / gemini)       |        |  (per engine)   |
                +-------------------+        +-----------------+
```

Three "session" concepts cut across this picture:
- **Hub session** = a row in `sessions` table; the transcript window
  the principal sees.
- **Engine session id** = the engine's own conversation cursor
  (claude `session_id`, codex `threadId`, gemini `session_id`); lives
  in the engine record, threaded into `--resume`.
- *(There's no third — but readers often conflate "agent" with
  "session" since each live hub session has exactly one current
  agent.)*

ADR-014 pins the boundary: `sessions.engine_session_id` captures the
engine cursor onto the hub session row at `session.init` time, so
resume can splice `--resume <id>` and the principal's transcript
stays continuous across paused→resumed lifecycle.

---

## 2. Sessions

### hub session
A row in the `sessions` table. The principal's transcript window.
Stable across pause→resume. Carries `current_agent_id`, optional
`worktree_path`, optional `spawn_spec_yaml`, optional
`engine_session_id`.
- *Distinguish from:* **engine session id** (engine-side cursor).
- *Canonical:* [`spine/sessions.md`](../spine/sessions.md), ADR-009.

### engine session id
The conversation cursor an engine maintains in its own record store.
Claude's `session_id` (in `~/.claude/projects/<cwd>/<sid>.jsonl`),
gemini's `session_id` (in `<projdir>/.gemini/sessions/`), codex's
`threadId`. Captured into `sessions.engine_session_id` and threaded
into `--resume` on respawn.
- *Distinguish from:* **hub session** (the table row), **session**
  used unqualified (avoid).
- *Canonical:* ADR-014.

### session (unqualified)
**Avoid using this term unqualified in load-bearing prose.** It
collides with engine session id, the mobile Sessions screen, and
sometimes "agent." Prefer *hub session* or *engine session id*. The
unqualified word is fine in casual references where context
disambiguates.

### transcript
The append-only log of everything that happened in a hub session.
Storage: `agent_events` rows stamped with `session_id`. Mobile
renders entirely off `agent_events`; the hub never reads engine
records directly for transcript display.
- *Distinguish from:* **engine record** (each engine's native
  conversation file).
- *Canonical:* [`discussions/transcript-source-of-truth.md`](../discussions/transcript-source-of-truth.md),
  ADR-014 (operation-log framing).

### archived session
A hub session whose `status='archived'`. Transcript preserved,
agent terminated. Only fork-eligible. Resume requires
`status='paused'`, not archived.
- *Canonical:* ADR-009 D4.

### paused session
A hub session whose `status='paused'`. Auto-flips from `active`
when the agent reaches a terminal status. Resumable: a fresh agent
spawns into the same session row.

### active session
A hub session whose `status='active'`. Has a live `current_agent_id`.

### fork
The hub-level operation that creates a new hub session shell from
an archived source. Writes a fresh `sessions` row with same scope
and title, **`engine_session_id = NULL`** (cold-start invariant —
ADR-014), `worktree_path = NULL`, `spawn_spec_yaml = NULL`. No
engine has a native fork primitive.
- *Distinguish from:* **resume** (same hub session, new agent
  process).
- *Canonical:* ADR-009 D4, [`discussions/fork-and-engine-context-mutations.md`](../discussions/fork-and-engine-context-mutations.md).

### resume
Two distinct resumes — context disambiguates which:
1. **Hub-session resume.** `POST /sessions/{id}/resume`. Spawns a
   fresh agent into a paused hub session.
2. **Engine resume.** `claude --resume <id>` (or
   `gemini --resume <UUID>`, `thread/resume` on codex). The CLI
   flag the hub-session resume threads under the hood.
- *Canonical:* ADR-014.

---

## 3. Agents

### agent
A row in the `agents` table; identifies a spawned engine process or
peer entity. Multiple meanings layered: the row, the live process,
the conceptual actor in mobile UX.
- *Distinguish from:* **engine** (the bin/process), **steward** /
  **worker** (agent roles).
- *Canonical:* [`spine/agent-lifecycle.md`](../spine/agent-lifecycle.md),
  ADR-009.

### agent kind
The `kind` column on `agents` row. Values: `claude-code`, `codex`,
`gemini-cli`. Selects the driver, frame profile, MCP config format,
and resume mechanism. **Not** the engine's product/build version.
- *Distinguish from:* **event kind**, **input kind**, **attention
  kind**.
- *Canonical:* `agentfamilies/agent_families.yaml`.

### driving mode
The `driving_mode` column. Values: `M1` (ACP / interactive), `M2`
(structured stdio), `M4` (tmux pane PTY). Selects which driver and
launch path the host-runner uses.
- *Distinguish from:* **permission mode** (auto-allow vs prompt),
  **output mode** (stream-json vs text).
- *Canonical:* `spine/blueprint.md` §5.3.

### permission mode
The flag-time tool-call gating policy claude templates resolve via
`{{permission_flag}}`. Values: `skip` →
`--dangerously-skip-permissions`; `prompt` →
`--permission-prompt-tool mcp__termipod__permission_prompt`; empty →
claude default. Codex / gemini have engine-specific equivalents.
- *Distinguish from:* **driving mode** (M1/M2/M4 process model).

### output mode
The CLI flag selecting an engine's output framing — claude/gemini
`--output-format stream-json`, codex `app-server` JSON-RPC. Picked
per agent kind; not a runtime per-spawn choice today.
- *Distinguish from:* **driving mode**, **permission mode**.

### handle
The human-readable name of an agent (`agents.handle`). Unique
within `(team_id, status='live')`. `steward`, `worker-1`, etc.

### steward
An agent role — the principal-facing operator that drives every
surface. Always lives inside an active hub session. Today, every
team has one steward; per-member is post-MVP.
- *Distinguish from:* **worker** (steward-spawned subordinate),
  **principal** (the human).
- *Canonical:* ADR-005, memory `feedback_steward_executive_role`.

### worker
An agent role — a steward-spawned subordinate that does bounded
work on a host. Reports back via A2A and channel messages.
- *Distinguish from:* **steward** (the boss).

### principal
The human user. Authority root for all spawn/decision actions.
Identified by `@handle` in audit rows.
- *Distinguish from:* **agent** (machine actor), **steward** (the
  agent that talks to the principal).

### parent / child agent
Spawn lineage. `agent_spawns.parent_agent_id` is who spawned this
one; `child_agent_id` is the spawnee. Steward → worker is the
common shape.

### pause_state
Per-agent flag (`running` | `paused`) controlled by the principal's
pause/resume action on a live agent. **Different** from session
status: a session can be `paused` (because its agent died), an
agent can be `paused` (because the principal paused it), and the
two flip independently.

### status
Per-agent lifecycle: `pending`, `running`, `stale`, `paused`,
`terminated`, `failed`, `crashed`. Reconcile loop on the host-runner
patches this based on the agent's process state.
- *Distinguish from:* **session status** (`active`, `paused`,
  `archived`, `deleted`).

---

## 4. Engines

### engine
The CLI bin a driver spawns and pipes — `claude`, `codex`, `gemini`.
Owned by the vendor; their schema, their record store, their
lifecycle.
- *Distinguish from:* **agent** (hub's row + role).

### claude-code / codex / gemini-cli
The three supported `agent kind`s. Each has its own driver
(StdioDriver, AppServerDriver, ExecResumeDriver), frame profile,
MCP config format, and resume mechanism. ADR-010 pulls vendor
schema parsing into YAML so adding a fourth is a YAML edit, not a
Go rebuild.

### engine record
The engine's native conversation file. Claude
`~/.claude/projects/<cwd>/<sid>.jsonl`, gemini
`<projdir>/.gemini/sessions/<uuid>/`, codex CLI thread store. **Not**
read by the hub for transcript display.
- *Distinguish from:* **transcript** (the hub's `agent_events` log).
- *Canonical:* [`discussions/transcript-source-of-truth.md`](../discussions/transcript-source-of-truth.md).

### frame profile
A YAML block in `agent_families.yaml` declaring how the driver
translates a vendor's stream into typed `agent_event` rows. ADR-010.

### stream-json
Newline-delimited JSON over stdout, the protocol claude and gemini
both speak in `--output-format stream-json`. Each line is a frame.
- *Distinguish from:* **JSON-RPC** (codex's app-server protocol).

### JSON-RPC
Line-delimited JSON-RPC 2.0 over stdio, the protocol codex's
`app-server` speaks. Same wire format as stream-json (line-delim
JSON), different framing semantics.

### app-server
Codex's persistent JSON-RPC daemon mode (`codex app-server --listen
stdio://`). Driven by `AppServerDriver`. ADR-012.

### exec-per-turn
Gemini's process model: one `gemini -p <text>` subprocess per user
turn, cross-process continuity via `--resume <UUID>`. Driven by
`ExecResumeDriver`. ADR-013.

---

## 5. Hosts

### host
A machine running the host-runner daemon. Row in `hosts` table.
Every running agent has a `host_id`.

### host-runner
The Go daemon (`cmd/host-runner`) that runs on each host, polling
the hub for spawn requests, launching engines, owning their
processes, translating their output, and posting events back.

### pane
A tmux pane. M4 agents are anchored to a pane (the user can attach
a real terminal). M2 agents have an optional cosmetic pane running
`tail -f` against a log file the driver writes; the real I/O is
host-runner-owned.

### worktree
A `git worktree`-managed directory. Each agent gets its own so
parallel agents don't clobber each other's `.git/index`.
`worktree_path` on `agents` and `sessions` rows.
- *Distinguish from:* **workdir** (any cwd, may or may not be a
  git worktree).

### workdir
The cwd a spawned engine launches with. Set by
`spec.backend.default_workdir`. Often a worktree, not always.

---

## 6. Events & data

### agent_event
A row in the `agent_events` table. The atomic unit of the hub
transcript. Carries `kind`, `producer`, `payload`, `session_id`,
`seq` (monotonic per agent).

### event kind
The `kind` column on `agent_events`. Stable typed vocabulary:
`text`, `tool_call`, `tool_result`, `usage`, `session.init`,
`lifecycle`, `system`, `error`, `raw`, plus the ADR-014 markers
`context.compacted`, `context.cleared`, `context.rewound`. Mobile
renders by kind.
- *Distinguish from:* **agent kind**, **input kind**, **attention
  kind**.

### producer
The `producer` column on `agent_events`. One of `agent` (engine
output), `user` (principal-typed input), `system` (hub-emitted —
lifecycle, context-mutation markers), `a2a` (peer-agent input).

### session.init
The first agent_event a fresh engine emits. Payload carries
`engine_session_id`, model, cwd, permission mode, mcp_servers, etc.
ADR-014 captures `engine_session_id` from this event.

### context.compacted / context.cleared / context.rewound
The three ADR-014 OQ-4 input-side mutation markers. Emitted with
`producer=system` when the principal types `/compact`, `/clear`, or
`/rewind` (claude) or `/compress`, `/clear` (gemini). Pin where the
engine's view of the conversation diverges from the hub's
operation-log transcript.

### input kind
The `kind` field on agent input requests. Values: `text`,
`approval`, `answer`, `attention_reply`, `cancel`, `attach`. Stored
on `agent_events` as `input.<kind>`.
- *Distinguish from:* **event kind** (the broader event-row
  vocabulary), **attention kind**.

---

## 7. Attention

### attention item
A row in the `attention_items` table — work the principal needs to
respond to (approve a tool call, pick an option, answer a help
request). Surfaced in mobile's Attention surface.
- *Canonical:* [`reference/attention-kinds.md`](attention-kinds.md).

### attention kind
The `kind` column on `attention_items`. Values: `approval_request`,
`select_request`, `help_request`, `permission_prompt`,
`approval_request_external`, etc.
- *Distinguish from:* **agent kind**, **event kind**, **input
  kind**.

### decision
The principal's response to an attention item. `POST /decide` flips
the row to `resolved` and fans out the answer to the waiting
driver. Outcomes: `approve` / `deny` / `cancel` / `select`.

### request_approval / request_select / request_help
The MCP tool names a steward calls to raise an attention item. Each
maps to one `attention_kind`. ADR-011 turn-based attention delivery.

### severity
Attention urgency tier: `info`, `warn`, `block`. Drives
notification routing and badge color.

### assignee
The principal handle the attention item is targeted at. Multiple
assignees allowed (per-member fan-out is post-MVP per ADR-004).

---

## 8. Protocols

### MCP
Model Context Protocol — the hub↔agent protocol. Engines call
`mcp__termipod__*` tools (e.g. `permission_prompt`,
`request_approval`) through their per-agent MCP token. ADR-007.

### A2A
Agent-to-Agent — the agent↔agent protocol. Hub-relayed because
GPU/worker hosts are typically NAT'd. ADR-003.

### hub-mcp-bridge
The stdio binary engines spawn (per their MCP config) that proxies
JSON-RPC MCP traffic to the hub's `/mcp/{token}` endpoint over
HTTPS. Same shape across all three engine kinds.

### per-agent MCP token
A bearer token minted on spawn, written into the engine's MCP
config (`.mcp.json` for claude, `.codex/config.toml` for codex,
`.gemini/settings.json` for gemini). Scoped to one agent.

---

## 9. Storage

### hub DB
The hub's SQLite database. Schema in `hub/migrations/`. Source of
truth for `agent_events`, `sessions`, `agents`, `attention_items`,
`audit_events`, etc.

### snapshot cache
The mobile-side SQLite cache of hub list/get responses. Cache-first
per ADR-006. Stores in the same SQLite tier as offline cache; not
mixed with secure storage or shared preferences.
- *Distinguish from:* **secure storage** (SSH keys, passwords),
  **SharedPreferences** (config metadata).
- *Canonical:* memory `feedback_storage_layering`.

### secure storage
`flutter_secure_storage`. SSH private keys, passwords, biometric-
gated secrets. **Never** mixed with cache content.

### SharedPreferences
Plain-JSON key-value store on mobile. Stable config and metadata
only. Not for mutable server content; use the snapshot cache for
that.

---

## 10. UI surfaces

### screen
A full-screen Flutter widget registered as a route. `lib/screens/`.
The user navigates between screens via tab bar, push, or deep link.
- *Distinguish from:* **sheet** (modal overlay), **view** (inner
  layout primitive).

### sheet
A modal overlay attached to a screen — bottom sheet, side sheet,
draggable sheet. Holds an action set (Pause/Resume/Terminate),
detail view, or form. Doesn't replace the underlying screen.
- *Distinguish from:* **screen**, **dialog** (a smaller modal).

### dialog
An alert-style modal with a confirmation/dismissal action set.
Used for destructive confirmations and one-line errors.

### card
A bordered/elevated container holding a single conceptual thing
(an agent, a session, a snippet). Tappable to drill in.
- *Distinguish from:* **chip**, **list tile**.

### chip
A small inline pill — status indicator, kind tag, scope badge.
Read-only; tappable in a few places (snippets).

### action bar
The horizontal strip of action buttons at the bottom of the
terminal/chat screen. Holds snippets, send, special keys.
- *Distinguish from:* **AppBar** (top, contains title/menu).

### snippet
A canned input — preset (system-supplied) or custom (user-
authored). Tappable to insert into the compose field. Bolt icon
trigger.

### bolt icon
The lightning-bolt icon. Reserved for snippet ActionChips. Don't
reuse for unrelated AppBar entries.
- *Canonical:* memory `feedback_bolt_icon_ambiguity`.

### AppBar
Flutter's top-of-screen app bar widget. Holds title, back/leading,
and menu actions.

### BottomNav
Flutter's `BottomNavigationBar`. The main mobile tab strip
(Projects / Activity / Hosts / Me).

### attention surface
The mobile screen that lists pending attention items. The principal
acts on them here.

### transcript view
The chat-style scroll of `agent_events` for one hub session. The
mobile session detail screen embeds this.

---

## 11. Process / project meta

### wedge
A feature-sized increment shipped as one PR (or one tagged
release). Larger than a polish commit, smaller than a quarter.
Memory `feedback_wedge_size` is the rule.

### slice
A sub-step of a wedge. Wedges typically ship as numbered slices
(e.g. ADR-013 had slices 1–6).

### blueprint
The architectural axiom doc — `docs/spine/blueprint.md`. Source of
truth for protocol edges, ontology, forbidden patterns.

### ADR
Architecture Decision Record. Files in `docs/decisions/`. Numbered,
append-only, immutable once accepted. ADR-NNN format.

### roadmap
`docs/roadmap.md`. Vision + Now / Next / Later. The single planning
artifact.

### status block
The five-line header every doc must have (Type / Status / Audience
/ Last verified vs code / [Supersedes]). Per `doc-spec.md` §3.

---

## 12. Index of "easy to confuse with" pairs

A flat list of the high-traffic confusion points, for grep:

- **session** vs **engine session id** — `hub session` is the row;
  `engine session id` is the engine's cursor.
- **transcript** vs **engine record** — hub's `agent_events` vs
  engine's native file.
- **resume** (handler) vs **resume** (CLI flag) — same word, two
  layers.
- **fork** vs **resume** — fork = new hub session, cold engine;
  resume = same hub session, threaded engine cursor.
- **agent kind** vs **event kind** vs **input kind** vs **attention
  kind** — four disjoint vocabularies.
- **driving mode** vs **permission mode** vs **output mode**.
- **status** (agent) vs **status** (session) vs **pause_state**.
- **worktree** vs **workdir**.
- **steward** vs **worker** vs **agent** vs **principal**.
- **screen** vs **sheet** vs **dialog**.
- **AppBar** vs **action bar** — top vs bottom.
- **snapshot cache** vs **secure storage** vs **SharedPreferences**.

If you find a confusion not in this list, add an entry above and
extend this index in the same change.

---

## 13. References

- `vocabulary.md` — the *swappable* role-bound vocab axes (different
  artifact, different goal).
- `doc-spec.md` §3 status block + the new term-consistency
  convention.
- `scripts/lint-glossary.sh` — the CI lint that detects drift in
  this glossary's terms across other docs.
- ADR-014 — where the operation-log framing and the hub-vs-engine
  session boundary are pinned.
