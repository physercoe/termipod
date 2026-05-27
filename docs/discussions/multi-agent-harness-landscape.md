# Multi-agent harness landscape

> **Type:** discussion
> **Status:** Open (2026-05-27) — landscape capture; no decisions taken
> **Audience:** contributors
> **Last verified vs code:** v1.0.722
> **Freshness:** snapshot (refresh when a new harness crosses ~10k stars or shifts architecture)

**TL;DR.** Three open-source multi-agent harnesses are pulling away
from the pack in mid-2026: **code-yeongyu/oh-my-openagent** ("omo",
~60k stars, TS plugin on top of OpenCode), **HKUDS/OpenHarness**
(~13k stars, Python full harness with Ohmo personal agent), and
**AndreBaltazar8/artificial** (39 stars, Go hub-and-spoke
orchestrating Claude Code / Codex / ACP). The pattern that is winning
is **"harness on top of a stochastic-executor engine"** — exactly
TermiPod's architecture. We are not behind the frontier; we are in
the same lane. The most directly borrowable pieces are omo's
filesystem-mailbox three-state lifecycle (urgent because cross-host
A2A — including blob-attachment payloads — already moves real bytes
through the hub), per-agent fallback model chains, and a deep-
introspection `doctor` command. The hook-taxonomy framing surfaces a
recovery-layer gap that v1.0.711 (`a2aPosterTap` mask) and v1.0.722
(recursive disconnect) both pointed at. OAuth-enabled MCP and auto-
compaction are deferred — the former adds weeks of spec work for
post-MVP ergonomics; the latter overlaps with engine-native
`/compact` surfaces in claude-code and codex. This doc inventories
the landscape and ranks what to borrow; concrete picks belong in
follow-up plans or ADRs.

Companion to [integrating-open-source-agents.md](integrating-open-source-agents.md),
which asks "can these engines drop into our [driving modes](../reference/glossary.md#driving-mode)
M1/M2/M4?" — this doc asks the inverted question: *what design ideas
from their harness layer should we borrow into ours?*

---

## 1. The three projects

| Project | Stars | Layer | Built on | License | Tech |
|---|---|---|---|---|---|
| **code-yeongyu/oh-my-openagent** ("omo") | ~60k | Plugin on top of OpenCode CLI | OpenCode | SUL-1.0 | TypeScript / Bun |
| **HKUDS/OpenHarness** ("ohmo") | ~13k | Full harness with React/Ink TUI | own engine loop | MIT | Python (94%) |
| **AndreBaltazar8/artificial** | 39 | Hub-and-spoke orchestrator | wraps engines via PTY + ACP | MIT | **Go** |

**omo is the deepest to study.** It is the most mature, ships the
most concretely-specified team-coordination protocol, and its problem
domain maps closest to ours. **Artificial is the closest
architectural twin** — Go central service, WebSocket workers, kanban
board, terminal streaming — and is worth a read precisely because the
design constraints map onto our [hub](../reference/glossary.md#hub) +
[host-runner](../reference/glossary.md#host-runner) split.
**OpenHarness leads on long-session survival** (auto-compaction
preserving task state + channel logs), which is acute for us because
the mobile cockpit makes "it broke at midnight" more visible.

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

## 5. What's borrowable for TermiPod

Ranked by *fit × leverage × cost*.

### 5.0 Grounding — what already works cross-host

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
- **Cross-host file sharing works for ≤25 MiB via the hub blob
  store.** `hub/internal/server/handlers_blobs.go:22-65` puts bytes
  at `<DataRoot>/blobs/<aa>/<bb>/<sha>` on POST and serves them on
  GET. Agent A uploads → references the sha in an A2A envelope or
  artifact row → Agent B downloads. Above 25 MiB the design slot
  exists (blueprint §4: hub holds references, hosts hold bytes) but
  the host-runner serve-bytes endpoint is not implemented yet.

Both facts make **A1 (mailbox three-state lifecycle) more urgent
than the abstract pitch suggests** — every cross-host A2A envelope
that references attached bytes is a real file transfer that should
survive host-runner crash with a defined in-flight state.

### 5.1 Tier A — borrow soon, well-shaped pieces

**A1. Mailbox three-state lifecycle for A2A.** Adopt
*unread / delivering / processed* with a `.delivering-{uuid}`
reservation file and a 10-minute TTL reclamation pass.
[ADR-032](../decisions/032-message-routing-envelope.md) envelopes
already carry sender stamping; what we lack is a defined "in-flight"
state that survives [host-runner](../reference/glossary.md#host-runner)
crash. Lands in `hub/internal/server/handlers_a2a.go` or a new
`hub/internal/mailbox/` package. The TTL pattern is the
load-bearing piece — we would otherwise reinvent it after a stuck-
message incident.

**A2. Per-agent fallback model chains** with variant + thinking
config. `agent_families.yaml` drives engine selection but lacks
graceful degradation. Lands as a new `fallback_models:` key in
`hub/internal/agentfamilies/agent_families.yaml`, consumed by
`hub/internal/hostrunner/launch_*.go`. Pairs naturally with the
allowlist-over-denylist discipline already documented in
[consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md).

**A3. Doctor command (`hub doctor --verbose`).** We have `/health`
but no deep introspection that lists *(runtime-injected MCPs / loaded
YAML profiles / spawned agents / tmux pane status / migration
version)*. Lands as `hub/cmd/hub-server/doctor.go` or as a
[hub-tui](../reference/glossary.md#host-runner) screen. Cheap;
immediately useful when triaging mobile reports of "it isn't
working."

### 5.2 Tier B — borrow with adaptation

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

### 5.3 Tier C — watch, don't copy

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

### 5.4 Tier D — already have, no work needed

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

---

## 6. The frontier signal

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
   blueprint §3.2). That is a different trade-off (concurrency,
   multi-host, audit), and worth defending — but our A2A relay
   should match the crash-recovery properties of the filesystem
   pattern (the `.delivering-` TTL is the load-bearing piece).

3. **Long-session survival is a shared frontier.** OpenHarness
   leads with auto-compaction; omo has session-recovery +
   intelligent compaction. Mobile-first makes this acute for us in
   theory — but claude-code and codex both ship their own
   `/compact` surfaces today, so the user-visible gap is smaller
   than the marketing suggests. Re-evaluate if/when a real
   "context-pressure lost my tasks" incident lands; until then the
   engine-native compaction does the work.

---

## 7. Open questions

- Does the mailbox three-state lifecycle warrant its own package
  (`hub/internal/mailbox/`), or does it live as additional state on
  the existing A2A envelope record? The former is cleaner; the
  latter is closer to ADR-032's "envelope as durable record" framing.
- Is OAuth-enabled MCP a `hub/internal/mcp/oauth/` subpackage, or
  does it belong in `hub/internal/auth/` next to the existing
  middleware? The OAuth flows are MCP-specific (dynamic client
  registration, PKCE) but the token store is auth.
- For B4 (auto-compaction), what is the right shape for the
  carry-forward blob — JSON dropped into the next system prompt, or
  a synthesized "previous session summary" appended to the YAML
  profile's `prompt_append`? The latter generalizes; the former is
  faster to ship.
- Should we cut a quarterly refresh cycle for this doc (status
  flipped to *Stale* every 90 days unless re-verified)? The
  landscape moved enough between [integrating-open-source-agents.md](integrating-open-source-agents.md)
  (2026-04-30) and this doc (2026-05-27) that a stale-by-default
  policy would be honest.

---

## 8. Sources

- code-yeongyu/oh-my-openagent — https://github.com/code-yeongyu/oh-my-openagent
- omo features reference — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/docs/reference/features.md
- omo AGENTS.md (dev) — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/AGENTS.md
- omo team-mode guide — https://github.com/code-yeongyu/oh-my-openagent/blob/dev/docs/guide/team-mode.md
- HKUDS/OpenHarness — https://github.com/HKUDS/OpenHarness
- AndreBaltazar8/artificial — https://github.com/AndreBaltazar8/artificial
- ComposioHQ/agent-orchestrator — https://github.com/ComposioHQ/agent-orchestrator
- Microsoft Conductor announcement (2026-05-14) — https://opensource.microsoft.com/blog/2026/05/14/conductor-deterministic-orchestration-for-multi-agent-ai-workflows/
- Oh My OpenAgent landing — https://ohmyopenagent.com/

---

## 9. Related

- [integrating-open-source-agents.md](integrating-open-source-agents.md)
  — companion doc; asks the inverted question (can these engines be
  drop-in [driving-mode](../reference/glossary.md#driving-mode)
  M1/M2 engines?).
- [codex-m2-app-server-surface-audit.md](codex-m2-app-server-surface-audit.md)
  — recent example of source-grounded audit before borrowing.
- [consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)
  — the allowlist-over-denylist discipline A3 should follow.
- [ADR-027](../decisions/027-local-log-tail-driver.md),
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md),
  [ADR-030](../decisions/030-governed-actions-and-propose-verb.md),
  [ADR-032](../decisions/032-message-routing-envelope.md),
  [ADR-036](../decisions/036-claude-code-statusline-telemetry.md) — the
  existing primitives Tier A/B borrows would extend.
