# Glossary

> **Type:** reference
> **Status:** Current (2026-05-09)
> **Audience:** contributors (humans + AI)
> **Last verified vs code:** v1.0.462

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
surface. Always lives inside an active hub session. The role has
two tiers (see *general steward*, *domain steward* below).
- *Distinguish from:* **worker** (steward-spawned subordinate),
  **principal** (the human), **general-purpose steward**
  (blueprint §3.4 anti-pattern — manager + IC collapsed; **not**
  the same as *general steward*).
- *Canonical:* ADR-005, blueprint §3.3, memory
  `feedback_steward_executive_role`.

### general steward
The frozen, team-scoped, persistent steward kind
(`steward.general.v1`). Bundled in the hub binary; one instance per
team, always-on; archived only by manual director action. Bootstraps
new projects (authors domain-steward + worker templates + plan in
phase 0), then remains available as the director's concierge for
cross-project debugging, free discussion, template/schedule edits,
and future-project bootstraps.
- *Distinguish from:* **domain steward** (overlay, project-scoped),
  **general-purpose steward** (blueprint §3.4 anti-pattern — manager
  + IC collapsed; the *general* steward is general in the sense of
  *team-scoped and project-agnostic*, **not** in the sense of
  *manager + IC collapsed*).
- *Canonical:* blueprint §3.3, ADR-001 D-amend-2,
  `discussions/research-demo-lifecycle.md` §4.

### domain steward
A steward kind authored as overlay by the general steward, scoped to
one project's lifecycle. Examples: `steward.research.v1`,
`steward.infra.v1`, `steward.briefing.v1`. Editable by the director;
archived at project completion.
- *Distinguish from:* **general steward** (frozen, persistent,
  team-scoped).
- *Canonical:* ADR-001 D-amend-2.

### worker
An agent role — a steward-spawned subordinate that does bounded
work on a host. Reports back via A2A (to its parent steward only —
see operation scope) and channel messages.
- *Distinguish from:* **steward** (the boss).
- *Canonical:* ADR-016 D3.

### general-purpose steward
**Anti-pattern.** A single agent that answers questions, edits files,
runs tests, AND arbitrates approvals — collapsing manager and IC into
one role. Single-engine clients (Happy, CCUI) collapse the two by
necessity (one role per app); termipod's positioning depends on
keeping them separate.
- *Distinguish from:* **general steward** (a *steward kind* in this
  design — frozen, persistent, team-scoped — that does **not** do IC).
  Different despite the close lexical neighbour.
- *Canonical:* blueprint §3.4.

### operation scope
The set of `hub://*` MCP tools an agent may invoke, gated by role
(steward vs worker) at the hub MCP boundary. Defined in
`hub/config/roles.yaml`, enforced in `mcp_authority.go`. Termipod's
**only** governance line in MVP — `budget_cents` is deferred,
per-tool approval gates are deferred. Engine-internal subagents
inherit the parent's operation scope by construction and are not
separately monitored.
- *Distinguish from:* **driving mode** (how host-runner wires the
  agent's stdio, not what it can call), **permission mode** (the
  engine-side allow/deny for engine-native tools).
- *Canonical:* ADR-016.

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

### host capabilities
The JSON blob in `hosts.capabilities_json` describing what one
host-runner-managed worker box reports (OS, arch, CPU count, RAM,
kernel, hostname). Probed once at host-runner startup via
`hostrunner.ProbeHostInfo`; pushed up to the hub.
- *Distinguish from:* **hub stats** (`/v1/hub/stats` payload — the
  hub-self capacity report; ADR-022 D2).

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
`text`, `thought`, `tool_call`, `tool_call_update`, `tool_result`,
`usage`, `session.init`, `lifecycle`, `system`, `error`, `raw`,
`turn.result`, `plan`, `diff`, `approval_request`,
`attention_request`, plus the ADR-014 markers
`context.compacted`, `context.cleared`, `context.rewound`. Mobile
renders by kind. Drivers tagged `producer=agent`/`system` emit
these; `producer=user`/`a2a` events use input-kind names like
`input.text` / `input.approval`.
- *Distinguish from:* **agent kind**, **input kind**, **attention
  kind**.

### approval_request (event kind)
Emitted by ACPDriver when the agent calls
`session/request_permission` mid-turn. Payload: `request_id`
(opaque, round-tripped to the eventual `input.approval`),
`params` (the agent-supplied tool/option summary). The mobile
Attention surface materializes this as an `attention_items` row.
- *Distinguish from:* `attention_request` (infrastructure
  failure surface — auth, etc.), where the principal can't
  resolve via a single yes/no decision.

### attention_request (event kind)
Driver-emitted event surfacing an out-of-band failure that needs
principal action — most commonly an ACP `authenticate` failure
(only-interactive methods + no cached creds, rpc-error,
preference-mismatch, timeout). ADR-021 W1.4. Payload carries a
sub-`kind` (`auth_required` so far), the configured method (when
applicable), the agent's advertised options, and a one-line
`remediation` hint. Mobile renders these distinctly from
`approval_request` because the resolution isn't a single
allow/deny — the principal typically fixes the host
(`gemini auth`, `GEMINI_API_KEY`) or edits the steward template,
then retries the spawn.

### replay (event payload flag)
When a session/load is in flight (W1.2), session/update
notifications carrying historical turns are tagged
`payload.replay = true` by ACPDriver. AgentFeed's W1.3 ingest
filter uses a content-stable key
(`agentEventReplayKey`) to drop replay frames whose semantic
content matches an event already in the cached transcript. Live
events (`replay` flag absent or false) bypass this filter.

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
`approval`, `answer`, `attention_reply`, `cancel`, `attach`,
`set_mode`, `set_model`. Stored on `agent_events` as
`input.<kind>`. ADR-021 W2.1 added `set_mode` and `set_model`;
those carry `mode_id` / `model_id` and route by the active
agent's family `runtime_mode_switch` declaration rather than
landing as bare events for every driver.
- *Distinguish from:* **event kind** (the broader event-row
  vocabulary), **attention kind**.

### runtime_mode_switch (family attribute)
ADR-021 D4 declaration on each `agent_families.yaml` entry —
a map from driving_mode (M1/M2/M4) to one of `rpc | respawn |
per_turn_argv | unsupported`. Tells the hub how to dispatch
`POST /agents/{id}/input` with kind `set_mode` / `set_model`:
- `rpc` — emit input event for the driver to forward as a live
  ACP `session/set_mode` / `session/set_model` RPC (W2.2).
- `respawn` — call `respawnWithSpecMutation` to swap the agent
  with a mutated backend.cmd inside one DoSpawn tx (W2.3); the
  engine_session_id resume cursor preserves transcript continuity.
- `per_turn_argv` — emit input event for the driver to stash;
  the next subprocess argv carries the override (W2.4).
- `unsupported` — 422 with a typed error.

Keyed by mode rather than per-family because gemini-cli
supports both M1 (rpc) and M2 exec-per-turn (per_turn_argv) and
a single string couldn't disambiguate.

### set_mode / set_model (input kinds)
ADR-021 W2.1 input kinds for the runtime mode/model picker.
Mobile sends one shape (`{kind: 'set_mode', mode_id: 'yolo'}`);
the wire path varies per engine via the family's
`runtime_mode_switch` table. Driver-side validation against the
agent's advertised `availableModes`/`availableModels` lives in
the M1 (W2.2) and M2 exec-per-turn (W2.4) drivers; the hub
respawn path (W2.3) validates by attempting the
`mutateBackendCmdFlag` edit and surfacing `errFlagNotInCmd` as
422 when the rendered cmd doesn't carry the target flag.

### images (input field)
ADR-021 D5 / W4.1 — optional `images: [{mime_type, data}]` array
on `POST /agents/{id}/input` text inputs. Hub validates a single
contract (mime allowlist `image/png|jpeg|webp|gif`, ≤5 MiB
decoded per image, ≤3 images per turn) and persists onto
`payload_json["images"]`. Each driver maps to its engine-native
content block:
- **claude** (StdioDriver, W4.2) → Anthropic
  `{type:"image", source:{type:"base64", media_type, data}}`
- **codex** (AppServerDriver, W4.3) → OpenAI responses-API
  `{type:"input_image", image_url:"data:<mime>;base64,<b64>"}`
- **ACP** (ACPDriver, W4.4) → ACP
  `{type:"image", mimeType, data}`; gated by
  `agentCapabilities.promptCapabilities.image`
- **gemini-exec** (ExecResumeDriver, W4.5) → strip + emit
  `kind=system` event with the upgrade path (`gemini -p` argv
  has no inline-image affordance)

Image blocks lead the content array; the text block (if any)
trails so the model reads imagery before the question. Body
becomes optional when ≥1 image is queued — image-only turns
are first-class.

### prompt_image (family attribute)
ADR-021 D5 / W4.6 declaration on each `agent_families.yaml`
entry — a map from driving_mode (M1/M2/M4) to bool. Mobile
composer reads `family.prompt_image[active driving_mode]` to
gate the inline-image attach affordance:
- claude-code: `{M1: true, M2: true, M4: false}`
- codex: `{M1: true, M2: true, M4: false}`
- gemini-cli: `{M1: true, M2: false, M4: false}` (M1 ACP
  accepts; M2 exec-per-turn strips)

Per-mode keying parallels `runtime_mode_switch`; a single
per-family bool couldn't capture gemini's M1↔M2 split. The
mobile gate (`resolveCanAttachImages`) returns false on missing
declarations so old hubs that haven't backfilled the field
degrade safely (no attach button rather than mis-route).

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

### insights surface
The mobile screen rendering scope-parameterized aggregate metrics
(spend / latency / errors / capacity / concurrency). Phase 1 =
project-scoped sub-section on Project Detail; Phase 2 = multi-scope
fullscreen view reachable from Project / Me / Activity / Hosts /
Agent details.
- *Distinguish from:* **activity surface** (chronological audit
  feed; forensic, not aggregate), **transcript view** (per-session
  event scroll).
- *Canonical:* ADR-022, plans `insights-phase-1.md` / `insights-phase-2.md`.

### activity surface
The mobile Activity tab — chronological feed of `audit_events`
plus a 24h digest card. Forensic, not aggregate.
- *Distinguish from:* **insights surface** (aggregate, scope-
  parameterized).

### hub stats
The `/v1/hub/stats` payload — hub-self capacity, returned as
machine + DB + live blocks. Distinct from per-team
`hosts.capabilities_json` (which the host-runner pushes up to the
hub for each NAT'd worker box).
- *Distinguish from:* **host capabilities** (per-worker, written by
  host-runner into `hosts.capabilities_json`).
- *Canonical:* ADR-022 D2.

### tier-1 metric
The five mobile-glance metric families on the insights surface:
spend, latency, reliability (errors), capacity, concurrency.
Backed by FinOps Inform + SRE Golden Signals. Tier-2 is drilldown
sheets (engine arbitrage, lifecycle flow, tool-call efficiency,
unit economics, snippet usage, multi-host distribution); Tier-3 is
post-MVP (governance, security, knowledge curves).
- *Canonical:* ADR-022.

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
- **general steward** vs **domain steward** — frozen-persistent vs
  overlay-project-scoped, both *steward* role.
- **general steward** vs **general-purpose steward** — the kind
  (this design) vs blueprint §3.4 anti-pattern (manager + IC
  collapsed). Different despite the close lexical neighbour.
- **operation scope** vs **driving mode** vs **permission mode** —
  hub-MCP tool gating vs how host-runner wires stdio vs engine-side
  tool allow/deny.
- **screen** vs **sheet** vs **dialog**.
- **AppBar** vs **action bar** — top vs bottom.
- **snapshot cache** vs **secure storage** vs **SharedPreferences**.
- **insights surface** vs **activity surface** — aggregate vs
  chronological; ADR-022 D1.
- **hub stats** vs **host capabilities** — hub-self capacity
  endpoint vs per-worker capability blob; ADR-022 D2.

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
