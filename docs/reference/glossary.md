# Glossary

> **Type:** reference
> **Status:** Current (2026-05-13)
> **Audience:** contributors (humans + AI)
> **Last verified vs code:** v1.0.556

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
1. **Hub-session resume.** `POST /sessions/{id}/resume` (or, keyed by
   the worker, `POST /agents/{id}/resume-session`). Spawns a fresh
   agent into a **paused** hub session. The inverse of **stop**.
2. **Engine resume.** `claude --resume <id>` (or
   `gemini --resume <UUID>`, `thread/resume` on codex). The CLI
   flag the hub-session resume threads under the hood.
- *Canonical:* ADR-014.

### stop (a worker)
The **reversible** halt. Kills the agent process but flips its hub
session to **`paused`** — RESUMABLE: a fresh agent can respawn into it
via **resume**. Surfaces: principal "Stop session" / steward
`agents.stop` / `POST /agents/{id}/stop`. The agent's process is gone;
the *session* survives so the work can continue.
- *Distinguish from:* **terminate** (permanent), **pause** (SIGSTOP a
  still-alive process — see `pause_state`).

### terminate (a worker)
The **permanent** end. Kills the agent process and **archives** its
hub session (`status='archived'`, fork-only, NOT resumable). Surfaces:
principal "Archive" / steward `agents.terminate` /
`POST /agents/{id}/terminate`. Use when the work is finished or
abandoned for good.
- *Distinguish from:* **stop** (resumable). The historical
  `agents.terminate` was a *stop* (left the session paused/resumable) —
  renamed/split so the verb matches its effect.
- *Canonical:* `archived session`, ADR-009 D4.

---

## 3. Agents

### agent
A row in the `agents` table; identifies a spawned engine process or
peer entity. Multiple meanings layered: the row, the live process,
the conceptual actor in mobile UX.
- *Distinguish from:* **engine** (the bin/process), **steward** /
  **worker** (agent roles), **intra-engine agent** (not a hub row).
- *Canonical:* [`spine/agent-lifecycle.md`](../spine/agent-lifecycle.md),
  ADR-009.

### inter-engine agent
A hub-managed agent: its own engine process, **hub session**, tmux
pane, and governance envelope, spawned via `agents.spawn` /
`agents.fanout`. The default meaning of **agent**. Reach across hosts
and engines; durable across respawn. Reserve a spawn for work that
warrants a director-visible, governed, or cross-host unit — see the
ADR-016 delegation-tier promotion triggers.
- *Distinguish from:* **intra-engine agent** (engine-internal,
  ungoverned, ephemeral).
- *Canonical:* ADR-016 (Amendment 2026-06-07),
  [`discussions/intra-vs-inter-engine-delegation.md`](../discussions/intra-vs-inter-engine-delegation.md).

### intra-engine agent
An engine-internal subagent — claude-code's `Task` tool, codex's
parallel subagents, kimi-code's `Agent` tool — that runs *inside* a
parent agent's process. Not a hub row; shares the parent's MCP client
and inherits its **operation scope** by construction (ADR-016 D5); not
separately monitored. Cheap (tokens only); the steward's preferred tier
for same-engine, same-host, ephemeral parallelism. (antigravity's
`agy invoke_subagent` is the exception — it runs on a private bus and
its steward prompt disallows it.)
- *Distinguish from:* **inter-engine agent** (a hub worker), **agent**
  (a hub row).
- *Canonical:* ADR-016 D5 + Amendment 2026-06-07,
  [`discussions/intra-vs-inter-engine-delegation.md`](../discussions/intra-vs-inter-engine-delegation.md).

### agent kind
The `kind` column on `agents` row. Values: `claude-code`, `codex`,
`gemini-cli`. Selects the driver, frame profile, MCP config format,
and resume mechanism. **Not** the engine's product/build version.
- *Distinguish from:* **event kind**, **input kind**, **attention
  kind**.
- *Canonical:* `agentfamilies/agent_families.yaml`.

### driving mode
The `driving_mode` column. Values: `M1` (ACP / interactive), `M2`
(structured stdio), `M4` (per-engine local-stream tap). Selects
which driver and launch path the host-runner uses.

M4's implementation is **per-engine** as of
[ADR-027](../decisions/027-local-log-tail-driver.md): claude-code's
M4 is `LocalLogTailDriver` (tails the on-disk session JSONL and
routes input via `tmux send-keys`); gemini-cli, codex, and
kimi-code retain the legacy tmux-pane PTY binding until their
adapters ship. Whichever implementation is bound, the driver emits
the same `agent_event` shapes M1/M2 produce.

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

**Stored bare — no `@` prefix.** The `@` is a *display* sigil
(used in prose, like Slack/GitHub mentions: `@steward`) and is
NOT part of the stored name. On the wire — `child_handle` on
`agents.spawn`, `handle` on `a2a.invoke`, the `agents.handle`
column, every a2a_card — the value is the bare form. The hub
strips a single leading `@` on insert (`normalizeAgentHandle`)
and the a2a lookup tolerates extras for safety, but templates
and tool callers SHOULD always pass bare; the strip is the
last-line defence, not the contract. Migration 0044 normalized
pre-existing `@`-prefixed rows.

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
archived at project completion. **Lazy-materialized (ADR-025 D2):**
a project row can exist without a domain steward; the steward is
spawned on first engagement (director taps the project's steward
overlay, sends a message into it, or another steward delegates into
it), with explicit director consent via a host-picker sheet (D4).
- *Distinguish from:* **general steward** (frozen, persistent,
  team-scoped).
- *Canonical:* ADR-017, ADR-025.

### worker
An agent role — a steward-spawned subordinate that does bounded
work on a host. Reports back via A2A (to its parent steward only —
see operation scope) and channel messages. **Project-scoped
(ADR-025 D1):** every worker carries a `project_id` and may only
be spawned by that project's steward (the general steward delegates
rather than spawning directly, D3). Every worker has its own session
(`scope_kind='project'`), so the steward↔worker conversation is
observable from the mobile session viewer.
- *Distinguish from:* **steward** (the boss).
- *Canonical:* ADR-016 D3, ADR-025.

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

### claude-code / codex / gemini-cli / kimi-code / antigravity
The five supported `agent kind` families (see
`hub/internal/agentfamilies/agent_families.yaml`). Each has its own
driver, frame profile, MCP config format, and resume mechanism.
ADR-010 pulls vendor schema parsing into YAML, so adding another is a
YAML edit, not a Go rebuild. `gemini-cli` is deprecated (Google
retires it 2026-06-18 for consumer tiers); `antigravity` is its
M4-only successor (ADR-035).

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
`select_request`, `help_request`, `notice`, `permission_prompt`,
`approval_request_external`, etc.
- *Distinguish from:* **agent kind**, **event kind**, **input
  kind**.

### notice
The answerless attention kind — a one-way FYI an agent surfaces to the
principal that needs no response (posted via `post_notice`). It carries
**no `pending_payload`**, so mobile files it under the Me-page
**Messages** slice (FYI) rather than **Requests**. Contrast the
`request_*` family, which all await a decision.
- *Distinguish from:* **a2a message** (agent↔agent), **channel
  message** (deferred), and the steward's **session/chat** turns —
  `notice` is the only one that lands in the director's Me-page inbox.
- *Canonical:* [`reference/attention-kinds.md`](attention-kinds.md).

### decision
The principal's response to an attention item. `POST /decide` flips
the row to `resolved` and fans out the answer to the waiting
driver. Outcomes: `approve` / `deny` / `cancel` / `select`.

### request_approval / request_select / request_help / post_notice
The MCP tool names a steward calls to raise an attention item. The three
`request_*` verbs each map to one `attention_kind` and await a response
(ADR-011 turn-based attention delivery); `post_notice` is the answerless
sibling — it raises a `notice` (FYI, Me-page Messages) and returns
immediately without awaiting anything.

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

### Insight transcript
The per-run **sealed analysis transcript** (`InsightTranscript`,
ADR-040) — a random-access, read-only view of one session's
`agent_events` driven by the digest, distinct from the live feed
(`LiveFeed`). The "Insights" tab body. ADR-041 reshapes it into a
workbench: a card stream + a left Sessions rail + a right Navigator.
- *Distinguish from:* **insights surface** (the aggregate
  spend/latency/errors metrics screen, ADR-022) — they collide on
  "insight(s)" but are different surfaces; and **transcript view**
  (the live chat scroll).
- *Canonical:* ADR-038, ADR-040, ADR-041.

### lens
A **card-family filter** over the Insight transcript: `All / Text /
Tools`. Narrows the visible cards in place; never navigates. The
Text/Tools lens pages its kinds across the whole run via the `kind=`
keyset (ADR-039).
- *Distinguish from:* **outline** (a structural index you jump from,
  not a filter).
- *Canonical:* ADR-039, ADR-041.

### outline
The whole-run **structural index** of the Insight transcript —
**Turns** and **Errors** — rendered as summary rows you jump *from*
into the full transcript. Lives in the Navigator; does not filter the
stream. Also called the TOC.
- *Distinguish from:* **lens** (a card filter).
- *Canonical:* ADR-041; rows in `transcript/feed_misc.dart`.

### Navigator
The Insight transcript's **right drawer** (phone overlay) hosting the
outline tabs (Turns, Errors) plus the **Map** (the minimap). Opened on
demand; jumping from it lands the transcript in full context.
- *Canonical:* ADR-041.

### Map (transcript)
The whole-run **minimap** as a Navigator tab — ticks per turn/error +
a viewport indicator + drag-scrub + the "jump to any event" scrubber.
Relocated off the floating card overlay (ADR-041) to end the
top-right-control collision.
- *Distinguish from:* **map** in any geographic sense (there is none
  here).
- *Canonical:* ADR-041.

### Sessions rail
The Insight transcript's **left drawer** (phone overlay) — a *scoped*
quick-switcher for the current project/agent's sessions (and related
agents); selecting one retargets the analyzed run. A convenience
inside the surface, **not** a global tree or a top-level navigator.
- *Distinguish from:* the **Sessions** screen / top-level IA tabs
  (Projects · Activity · Me · Hosts · Settings).
- *Canonical:* ADR-041.

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

## 10b. Project lifecycle entities

Six entities cluster around a project's execution. The relationship
arrows are non-obvious from the schema alone, and the words are easy
to misuse — this section is the canonical map.

```
Project
 ├── phase (one TEXT column on the project + phase_history JSON)
 │     ├── Deliverables  (per-phase, ratifiable)
 │     │     └── Components  (refs to documents / artifacts / runs / commits)
 │     └── Acceptance criteria  (per-phase, optionally per-deliverable)
 ├── Plan  (one per project in practice)
 │     └── Plan-steps  (phase-bucketed via phase_idx)
 └── Tasks  (project-scoped, phase-tagged via tasks.phase — WS1)
```

### project
A top-level work container. Owns phases, plans, tasks, deliverables,
acceptance criteria, channels, schedules, artifacts, runs.
- *Distinguish from:* **project spec / project template** — a project's
  spec *is* its own `config_yaml` (ADR-046); the on-disk presets under
  `team/templates/projects/` are **reference examples** of that schema,
  not a library you install from.
- *Canonical:* migration `0001_initial.up.sql` + `0034_project_lifecycle`.

### project spec (config_yaml)
A project's full definition, carried **inline** in its own
`config_yaml` (ADR-046): `phases:` (≥1; a one-off job is a 1-phase
project), per-phase `deliverables:` / `criteria:` / `tasks:` / `plan:`,
typed `parameters:`, and the bound domain steward
(`on_create_template_id:`). An agent creates a project by composing this
spec and calling `propose(kind="project.create")`; the principal reviews
it on the approval card and on approve the project **materializes** with
every phase early-bound. There is no separate install-a-project-template
step.
- *Distinguish from:* **template.install** — still a normal
  agent-proposes / principal-approves action for agent / prompt / plan
  templates; it is **not** the project-creation path.
- *Canonical:* ADR-046, ADR-044 amendment.

### Start (a project)
The explicit gesture that spawns a project's **bound** domain steward.
Create *binds* the steward (records `on_create_template_id`) but does
**not** spawn it; `POST …/projects/{id}/start` (mobile: the "Not
started — review & Start" affordance) spawns it. A project read exposes
the derived `steward_started` flag.
- *Distinguish from:* **create** (binds the steward, materializes the
  phases) and **ensure-steward** (ADR-025 lazy-materialize on first
  engagement — Start is the deliberate up-front variant).
- *Canonical:* ADR-046.

### phase
A string column on `projects` (e.g. `idea`, `lit-review`, `method`,
`experiment`, `paper` for the research template). Templates declare
the phase set + ordering; progression is logged on
`projects.phase_history` (JSON, append-only). **Phases are not
separate rows** — there's no `phases` table. The phase is a *state*
on the project, not an entity.
- *Distinguish from:* **plan-step**'s `phase_idx` (a numeric pointer
  into the phase ordering, not the phase itself).
- *Canonical:* migration 0034, `docs/reference/research-template-spec.md` §3.

### plan
One execution-spec row per project (in practice — the schema doesn't
enforce uniqueness; the seed + UI assume it). Carries `template_id`,
`spec_json` (the recipe), `version`, and a lifecycle `status`
(draft / ready / running / completed / failed / cancelled). **Not
per-phase** — one plan spans all of a project's phases. The
seed-demo `--shape lifecycle` seeds one plan per project, partitioned
into per-phase work via `plan_steps`.
- *Distinguish from:* **phase** — phases live on `projects.phase`,
  plans live in the `plans` table. A plan covers all of a project's
  phases; phases live on the project, not on the plan.
- *Distinguish from:* **task** — plans describe execution units
  (`agent_spawn` / `llm_call` / `shell` / `mcp_call` /
  `human_decision`); tasks describe kanban work items.
- *Canonical:* migration 0009, blueprint §6.2.

### plan-step
A work unit inside a plan. Bucketed by `phase_idx` (which phase the
step belongs to) and ordered within a phase by `step_idx`. `kind`
must be one of `agent_spawn` / `llm_call` / `shell` / `mcp_call` /
`human_decision` — these are the only execution kinds the chassis
understands. Carries `spec_json` (kind-specific payload),
`input_refs_json` / `output_refs_json` (data flow), and an optional
`agent_id` (which agent is running this step). Phase-scoped via
`phase_idx`, but the *owning plan* spans all phases.
- *Distinguish from:* **task** — plan-steps belong to a plan;
  tasks are independent kanban entities.
- *Canonical:* migration 0009 + `handlers_plans.go:27` (the closed
  `planStepKinds` set).

### task
A project-scoped kanban entity. No phase column. Has a `parent_task_id`
(subtasks), optional `milestone_id`, `assignee_id` (agent doing the
work), `created_by_id` (agent or NULL=principal-direct, see ADR-029
D-2), `priority`, `body_md`, and `status` ∈
`todo` / `in_progress` / `blocked` / `done` / `cancelled`. ADR-029:
- `in_progress` and `done` are auto-derived from the linked
  `agent_spawns.task_id` lifecycle — `in_progress` on spawn (flip-on-
  spawn), `done` on agent terminated, `blocked` on crashed/failed.
- `cancelled` is an explicit human/steward override (the work is
  being stopped intentionally, vs `done` which means "the agent
  terminated"). Auto-derive never enters or leaves `cancelled`.
- `tasks.started_at` / `completed_at` are auto-stamped at the same
  flips; `result_summary` is worker-supplied (typically via
  `tasks.complete`).

**Information-flow edges (ADR-029 D-8):**
- **Down (steward → worker):** `agents.spawn task: {…}` materializes
  the task row, inlines title + body_md into the worker's
  engine-specific agent-memory file under a `## Task` section
  (`CLAUDE.md` for claude-code, `AGENTS.md` for codex/kimi-code,
  `GEMINI.md` for gemini-cli — see `contextFileNameForKind` in
  `hub/internal/server/template.go`, v1.0.615-alpha). The section
  carries a system-rendered "Task close-out protocol" footer with
  the literal `project_id` + `task_id` so the worker can call
  `tasks.complete` without looking itself up (W2.6.1,
  v1.0.614-alpha). The spawn also posts a `producer='user'
  kind='input.text'` event immediately after commit so the worker's
  first turn fires automatically. The footer's prose explicitly
  overrides any `TOOLS:` / `BOUNDARIES:` restrictions a steward
  might write into `body_md`: close-out verbs are orchestration
  protocol, not domain tools.
- **Up (worker → assigner):** terminal status flips (done / blocked
  / cancelled) post a `kind='task.notify' producer='system'` event
  into the assigner's most-recent active session carrying title +
  from → to + result_summary. Fired by both manual updates
  (`tasks.update`, `tasks.complete`, mobile flips) and auto-derive
  (worker terminate / crash / fail).

Tasks are **independent of plan** — they don't share rows or schemas
with `plan_steps`. Tasks exist for human-tracked work; plan-steps
exist for chassis-driven execution.

- *Distinguish from:* **plan-step** — different table, different
  purpose, different schema. They can coexist on the same project
  (e.g. plan-step "spawn critic.v1" + task "babysit the 384-d sweep").
- *Distinguish from:* **note** — a note is a device-local scratch
  entry, never synced; a task is a hub-side primitive that drives
  agent work and audit.
- *Canonical:* migration `0001_initial.up.sql` (`CREATE TABLE tasks`)
  + `0021_tasks_priority` + `0041_tasks_spawn_lifecycle`.

### task.notify
The agent_events row that lands in an assigner's session when a task
they delegated reaches a terminal state (done / blocked / cancelled).
`kind='task.notify'`, `producer='system'`. Payload carries
`{task_id, title, from, to, result_summary, body}`; the prerendered
`body` field is what the mobile chat surface displays. Best-effort —
NULL `created_by_id` (principal-direct task) and no live session for
the assigner both silently degrade; the audit row remains the durable
record. See ADR-029 D-8.
- *Distinguish from:* **task.status** — `task.status` is an
  `audit_events.action`, written for every status flip including
  non-terminal ones (`in_progress`); `task.notify` is an
  `agent_events.kind`, written only for terminal flips and only when
  there's a live assigner session to push into.
- *Canonical:* `hub/internal/server/task_notify.go`.

### run.notify
The agent_events row that lands in the owning worker's session when a
run reaches a terminal state (completed / failed / cancelled).
`kind='run.notify'`, `producer='system'`. Payload carries
`{run_id, project_id, status, started_at, body}`. Closes the gap
where ML engineers running sweeps had no push signal — `runs.update
status='completed'` previously wrote audit only. The worker, which
may have async-waited on the run via trackio polling, sees the
terminal signal immediately. Standalone runs (NULL `agent_id`)
silently degrade.
- *Distinguish from:* **run.complete** — `run.complete` is the
  `audit_events.action` written on the same flip; `run.notify` is
  the in-session push. Both fire from `handleCompleteRun`.
- *Canonical:* `hub/internal/server/run_notify.go`.

### a2a.sent
The agent_events row that lands in a *sending* agent's session every
time a successful A2A relay delivers (status < 400).
`kind='a2a.sent'`, `producer='system'`. Payload carries
`{to_handle, to_agent_id, preview, body}`. The sender's chat surfaces
what it just dispatched — without this push the outbound turn was
invisible (the MCP call returns the receiver's reply, but the chat
showed nothing for the request itself).
- *Why no receiver-side sibling?* The host-runner's
  `a2aHubDispatcher` already POSTs the message body to the hub as an
  `input.text producer='a2a'` event, which renders as the actual A2A
  turn in the receiver's chat. A receiver banner on top would
  double-render the same content.
- *Distinguish from:* **a2a.message_sent** — `a2a.message_sent` is
  the `audit_events.action` written by `recordA2ARelayAudit` on the
  same delivery; `a2a.sent` is the sender-side in-chat push.
- *Sender unknown* (unauthed peer call with no forwarded bearer) →
  notification is skipped; only the audit row records the relay.
- *Canonical:* `hub/internal/server/a2a_notify.go`.

### note
A device-local personal scratch entry (`note` or `reminder` kind),
backed by sqflite in `lib/services/notes/notes_db.dart`. Never synced
to the hub in v1 — see the file header for the v2 sync story. Surfaced
on the Me page.
- *Distinguish from:* **task** — tasks are hub-side, project-scoped,
  drive agents, and write audit rows; notes are private to the device.

### todo
Two distinct senses in the codebase; both are valid in their own
scope but the lint pin requires disambiguation in prose.

1. **Task status `todo`** — a `tasks.status` value meaning "created,
   not yet executing." See [task](#task).
2. **(deprecated) NoteKind.todo** — the on-device note kind was named
   `todo` until v1.0.610; ADR-029 W5 renamed it to `NoteKind.reminder`
   to free up the name. Existing on-device rows migrate automatically.

### document
A single body of prose (markdown) attached to a project. Carries a
`kind` (`memo` / `draft` / `report` / `review` / typed-document
slugs like `proposal` / `method` / `paper`), an optional `schema_id`
that flips it from "plain markdown body" to "structured typed
document with named sections," and the section rows that hold the
content. Documents are addressable on their own and are often used
as the body of a deliverable component.
- *Distinguish from:* **deliverable** — a deliverable is a *bundle*
  with a ratification state and optional components; a document is
  one body of prose that might be a component of a deliverable.
- *Canonical:* migration `0007_documents` + `0034_project_lifecycle`
  (schema_id column) + ADR-A1.

### deliverable
A per-(project, phase) ratifiable artifact. Carries `kind` (free-form,
e.g. `lit-review` / `method` / `experiment-results`),
`ratification_state` (`draft` / `in-review` / `ratified`), and one
or more `deliverable_components` (refs to documents / artifacts /
runs / commits). Ratification is the phase-advance gate.
- *Distinguish from:* **document** — a document is a single body of
  prose (markdown); a deliverable is a *ratifiable bundle* that
  often contains one or more documents alongside artifacts / runs /
  commits.
- *Canonical:* migration 0034.

### acceptance criterion (AC)
A per-(project, phase, optional deliverable) checkable condition.
`kind` ∈ {`text`, `metric`, `gate`}; `state` ∈ {`pending`, `met`,
`failed`, `waived`}. Gate kinds reference another runtime fact
(e.g. `deliverable.ratified`); metric kinds reference a measurement;
text kinds are director-attested prose. ACs gate phase advancement.
- *Distinguish from:* **deliverable** — deliverables are *what* the
  phase produces; ACs are *whether what was produced is good enough*.
- *Canonical:* migration 0034.

---

## 10c. Project detail surface vocabulary

Four words that all describe "the type of something on a project
page" and routinely get confused. Added 2026-05-11 after the
References-vs-Documents tile overlap surfaced as a UX bug. Each is
on a different axis; the four together compose what the user sees.

| Term | Axis | Where typed | Closed set? |
|---|---|---|---|
| **tile** | UI affordance — which folder icon appears on project detail | `TileSlug` enum (mobile) + `phase_specs[<phase>].tiles` YAML + `phase_tile_overrides` per-project | Yes — 9 today |
| **artifact-kind** | Content type — what the blob *is* (image, pdf, tabular, code-bundle, …) | `artifacts.kind` (hub, **schemaless today** — see `artifact-type-registry` plan) | No — currently free-form |
| **document-kind** | Prose-document type — kind of authored writeup (memo, draft, report, …) | `documents.kind` (hub, handler whitelist) | Yes — 5: `memo`, `draft`, `report`, `review`, `sample` |
| **deliverable-component-kind** | How a deliverable composes its parts (document / artifact / run / commit) | `deliverable_components.kind` (hub, CHECK constraint) | Yes — 4 |

### tile
A UI slot on the project detail page. *A navigation affordance, not
content.* The closed `TileSlug` mobile enum + phase-specific overrides
chain determine which tiles appear for a given (template, phase,
per-project-override) triple. Tapping a tile routes to a screen that
*lists items*; the items themselves have artifact-kind / document-kind.
- *Distinguish from:* **artifact-kind** — tiles don't have content;
  they're entry points. One tile can list items of multiple kinds.
- *Canonical:* `lib/widgets/shortcut_tile_strip.dart` (TileSlug enum)
  + research template YAML `phase_specs[*].tiles`.

### artifact-kind
The *type* of a content blob stored under `artifacts` (URI + MIME +
size). Today schemaless — the column accepts any string; comment-only
examples are `checkpoint`, `eval_curve`, `log`, `dataset`, `report`.
Plan [`../plans/artifact-type-registry.md`](../plans/artifact-type-registry.md)
locks this into a closed set grounded in adopted industry taxonomies
(Claude Artifacts, ChatGPT Canvas, MCP resource types, Notion blocks,
Jupyter MIME bundles).
- *Distinguish from:* **document-kind** — documents have an editable
  prose body (single markdown text); artifacts have an opaque blob
  behind a URI.
- *Distinguish from:* **tile** — artifact-kind says *what* the blob
  is; tile says *where* to find it on the project page.
- *Canonical:* migration 0019 (`artifacts.kind`).

### document-kind
The type of an *authored prose* entity in the `documents` table.
Closed set of five validated by the create handler: `memo`, `draft`,
`report`, `review`, `sample`. All five share a single editable
markdown body + optional `sections` array; the kind drives UI labels
and validates against template hydration.
- *Distinguish from:* **artifact-kind** — see above.
- *Canonical:* migration 0007 + `hub/internal/server/handlers_documents.go`.

### deliverable-component-kind
How a `deliverable_component` row points at its underlying entity:
exactly one of `document`, `artifact`, `run`, `commit`. *A typed
foreign-key discriminator, not a content type.* A deliverable
bundles N components; ratification gates phase advancement.
- *Distinguish from:* **artifact-kind** / **document-kind** —
  component-kind says "which table the ref points to"; the other
  two say "what kind of thing that row IS."
- *Canonical:* migration 0034 `deliverable_components.kind`.

**Which to use when:**

- *"Where does the user navigate to?"* → **tile**.
- *"What MIME / schema is this blob?"* → **artifact-kind**.
- *"Is this a memo or a draft?"* → **document-kind**.
- *"What table does this deliverable reference?"* → **deliverable-component-kind**.

If a feature crosses two axes (e.g. "the References tile should
list tabular artifacts of kind `citation`"), say both explicitly:
*"tile=References, artifact-kind=citation"*. Don't let one word
stand in for two axes.

---

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
- **plan** vs **plan-step** vs **phase** vs **task** — plan is the
  per-project execution spec, plan-step is one work unit inside it,
  phase is a TEXT column on the project (not a row), task is an
  independent kanban entity. See §10b.
- **deliverable** vs **document** — deliverables are ratifiable
  bundles (often containing documents); documents are single bodies
  of prose. §10b.
- **tile** vs **artifact-kind** vs **document-kind** vs
  **deliverable-component-kind** — four orthogonal "type of thing
  on a project page" axes. UI slot vs content blob type vs prose
  entity type vs FK discriminator. §10c.

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
