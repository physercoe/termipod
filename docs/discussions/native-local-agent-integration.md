# Native local-agent integration — how the engine CLIs talk to editors, and what the desktop should reuse

> **Type:** discussion
> **Status:** Open (2026-07-13) — investigates how Claude Code / Codex / Gemini /
> Kimi CLIs integrate with editors (VS Code, JetBrains, Zed), and what it takes for
> the desktop workbench's **local agent** to be *native and full-functional* rather
> than the current one-shot `claude -p`. Companion to
> [author-agent-assist-and-diagrams.md](author-agent-assist-and-diagrams.md) (which
> scoped the companion panel + the POSIX-only host-runner's Windows options) and
> [codex-m2-app-server-surface-audit.md](codex-m2-app-server-surface-audit.md).
> Grounds in the hub's existing drivers ([../spine/agent-lifecycle.md](../spine/agent-lifecycle.md),
> [../spine/protocols.md](../spine/protocols.md)).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop v0.3.32; hub `internal/hostrunner` on main

**TL;DR.** Every editor integration for these agents is one of **three wire
protocols**, and none of them is one-shot print mode:
(1) **Claude Code** — the editor runs a **localhost WebSocket-MCP server** (lockfile
`~/.claude/ide/<port>.lock`) that the CLI auto-connects to; for embedding, the CLI
also exposes a **bidirectional NDJSON stream** (`--input-format stream-json
--output-format stream-json`). (2) **Codex** — a **JSON-RPC "app-server"** over
stdio (`codex app-server`) that powers *all* its surfaces; OpenAI deliberately
rejected MCP for it. (3) **ACP** (Agent Client Protocol) — the open convergence
standard (Zed, MS Intelligent Terminal); Gemini/Codex native, Claude via adapter.
**The decisive finding: the TermiPod hub already implements all three in tested
Go** — `StdioDriver` (Claude stream-json), `AppServerDriver` (Codex app-server),
`ACPDriver` (Gemini/Kimi ACP), `LocalLogTailDriver` (Claude M4) — behind one
`Driver`/`Inputter` interface with permission bridging, MCP injection, and session
resume already solved. So the recommendation is **reuse, not rebuild**: make the
desktop's local agent an agent on a **localhost host-runner** (shipped as a Tauri
sidecar), so "local" and "remote" agents share one lifecycle and one transcript —
*not* a parallel Rust protocol client that will diverge. The one real gap is
Windows (host-runner is POSIX/tmux-bound); M1/M2 don't need tmux, so a ConPTY
runner covers Claude/Codex/Gemini there — the M4 tail path stays POSIX-only.

---

## 1. The three integration mechanisms

The current desktop path (`src-tauri/src/local_agent.rs` → `claude -p <prompt>` →
stdout, rendered by `ui/LocalCompanion.tsx`) is a **stub**: no token streaming, no
tool-call visibility, no permission/approval UI, no session continuity, no
workspace context. "Native and full-functional" means adopting one of the
protocols the vendors' own IDE integrations use.

### 1a. Claude Code — editor-as-MCP-server, or embedded NDJSON stream

**IDE mode (VS Code / JetBrains / Zed).** The extension does *not* reimplement the
agent. It starts a **localhost WebSocket server** and writes a lockfile at
`~/.claude/ide/<port>.lock` carrying the port and a fresh per-activation auth token
(`0600` in a `0700` dir). The `claude` CLI, running in the integrated terminal,
**auto-discovers the lockfile and connects over a WebSocket variant of MCP**
(`ws-ide` transport). The *editor* is the MCP server; it exposes ~a dozen tools to
the CLI — `openDiff`, `getCurrentSelection`, `getDiagnostics`, `saveDocument`,
`executeCell`, `closeTab` — but filters all but ~2 out of the *model's* view. That
is how you get the **native diff viewer with accept/reject**, `@`-mention of the
current selection, and Jupyter cell execution: the CLI stays the engine, the editor
lends context and native UI as MCP tools. (Community reimplementations document the
wire format: coder/claudecode.nvim `PROTOCOL.md`.)

**Embedded / programmatic mode.** `claude -p --input-format stream-json
--output-format stream-json --verbose [--include-partial-messages]` gives a
**persistent bidirectional NDJSON session over stdio**. Events: `system` /
`subtype:init` (carries `session_id`, `model`, `tools`, `mcp_servers`), `assistant`
(content blocks: `text`, `tool_use`), `user` (`tool_result` blocks), `stream_event`
(`event.delta.type:"text_delta"` for token streaming), `result` (cost/duration),
`system`/`api_retry`. Multi-turn = keep writing stream-json `user` frames; resume
via `--resume <session_id>` or `--continue`. Approvals: `--permission-mode`
(`acceptEdits`/`dontAsk`/`plan`), or `--permission-prompt-tool <mcp-tool>` (an MCP
tool that *is* your approval UI), or `--dangerously-skip-permissions`. MCP:
`--mcp-config <file-or-json>` (or an auto-discovered `.mcp.json` in cwd). The
**Claude Agent SDK** (TS/Python) wraps the same loop with tool-approval callbacks.
Claude Code has **no native ACP** (Anthropic closed the request `NOT_PLANNED`); only
Zed's `@zed-industries/claude-code-acp` adapter bridges it.

### 1b. Codex — the App Server (JSON-RPC over stdio)

Codex went the opposite way and **explicitly rejected MCP**. Every Codex surface
(CLI, VS Code, web, macOS, JetBrains, Xcode) spawns **`codex app-server`** and
speaks **JSON-RPC 2.0 as JSONL over stdio**. Handshake: `initialize`(clientInfo) →
response(`userAgent`, `codexHome`) → **`initialized` notification** (required).
Primitives are **Thread / Turn / Item**. Methods: `thread/start|resume|fork|list|read`,
`turn/start|interrupt|steer`, `command/exec`, `fs/readFile|writeFile|watch`,
`model/list`, `config/*`. Streaming notifications: `turn/started`,
`item/started|completed`, `item/agentMessage/delta` (token streaming),
`turn/completed`. OpenAI's stated reason for rejecting MCP: streaming diffs,
**approval flows**, and thread persistence "did not map cleanly onto MCP's
tool-oriented model." The schema is versioned — pin it with `codex app-server
generate-ts` / `generate-json-schema`.

### 1c. ACP — the convergence layer (and what the hub's M1 already is)

The **Agent Client Protocol** (Zed, open, JSON-RPC over stdio) is the emerging
standard: editor = client, agent = server. Methods: `session/new`,
`session/prompt`, streaming `session/update`, **`session/request_permission`** (tool
approval), `fs/read_text_file` / `write_text_file`. **Gemini CLI and Codex speak it
natively; Claude via the adapter.** Zed runs all three side-by-side over ACP, and
Microsoft's Intelligent Terminal 0.1 auto-detects whichever ACP CLI is installed.
**This is exactly TermiPod's M1 driving mode.**

| Engine | Native protocol its own IDE uses | ACP support |
|---|---|---|
| Claude Code | ws-ide MCP (IDE) · stream-json NDJSON (embedded) | adapter only (`claude-code-acp`) |
| Codex | app-server JSON-RPC | native |
| Gemini CLI | ACP (`gemini --acp`) | native |
| Kimi Code | ACP (`kimi … acp`) | native |

---

## 2. What the hub already implements (the reuse asset)

The hub is not a bystander here — it already drives these exact protocols in
production Go, behind a single `Driver` interface (`Start`/`Stop`) + optional
`Inputter` (`Input(ctx, kind, payload)`) in `hub/internal/hostrunner/driver.go`.
The desktop would be re-solving problems that are already solved and tested:

| Mode | Driver (file) | Protocol | Engines today | Permission flow |
|---|---|---|---|---|
| **M2** | `driver_stdio.go` (`StdioDriver`) | Claude stream-json NDJSON | claude-code | `--permission-prompt-tool` MCP → attention_items; `Input("approval")` writes a `tool_result` frame |
| **M2** | `driver_appserver.go` (`AppServerDriver`) | Codex app-server JSON-RPC | codex | server-initiated `requestApproval` / MCP elicitation → attention_items |
| **M1** | `driver_acp.go` (`ACPDriver`) | ACP JSON-RPC | gemini-cli, kimi-code | agent-initiated `session/request_permission` → parked → JSON-RPC response |
| **M4** | `drivers/local_log_tail/` (`LocalLogTailDriver`) | on-disk JSONL tail + tmux send-keys | claude-code, antigravity | gateway hooks + MCP `permission_prompt` |

Already handled inside these drivers, all of which the desktop otherwise reinvents:
**streaming translation** to a uniform event model (`text`/`tool_call`/`tool_result`/
`turn.result`/`usage`); **session resume** (Claude `--resume`; ACP `session/load`
with a replay-dedup window; Codex `thread/resume`); **MCP injection** per family
(`.mcp.json` / `.codex/config.toml` / `.gemini/settings.json` / `--mcp-config-file`
for Kimi); **permission bridging** to attention-items; and platform gotchas
(process-group kill, ACP stderr-split, camel/snake coalescing, tmux `paste-buffer
-r`). The desktop already **renders** hub agent transcripts (`AgentTranscript.tsx` +
`EventCard.tsx`) — so a local agent driven this way inherits the full UI for free.

---

## 3. What "native + full-functional" requires

Whatever the transport, the local agent must gain, versus today's one-shot:

1. **Token streaming** — assistant text as it generates (`stream_event` /
   `item/agentMessage/delta` / ACP `agent_message_chunk`).
2. **Tool-call visibility** — a card per tool use with inputs and results (the
   `tool_call` / `tool_result` events all three protocols emit).
3. **Permission / diff accept-reject** — the defining "native IDE" feature: a
   native approval prompt for shell/edits, and a **diff view with accept/reject**
   for file changes. All three protocols model this (`--permission-prompt-tool`;
   `requestApproval`; `session/request_permission`).
4. **Session / thread persistence** — resume a conversation across turns and app
   restarts.
5. **Workspace context** — feed current file / selection / diagnostics (Claude's
   ws-ide tools; ACP `fs/*`; Codex `fs/*`), and apply edits back into the Author
   buffers / on-disk files.

---

## 4. Options and recommendation

### Option A — bespoke per-engine Rust clients
Grow `local_agent.rs` into a real `StdioDriver`-equivalent for Claude stream-json
**and** an app-server JSON-RPC client for Codex, in Rust. *Max fidelity per engine.*
But it duplicates ~4 tested Go drivers, forks the permission/transcript model into a
second implementation that will drift, and re-hits every gotcha the hub already
documents. **Rejected as the primary path** — it is exactly the "parallel agent
stack" the codebase's one-lifecycle principle warns against.

### Option B — one bespoke Rust ACP client
Implement a single **ACP** client in Rust (`session/new|prompt|update|request_permission`,
`fs/*`) and launch each engine via its ACP entrypoint (Gemini/Codex native, Claude
via the `claude-code-acp` Node adapter). *One protocol for all engines, matches the
Zed/MS direction.* Cheaper than A, but: still a second implementation of M1 (the hub
already has `ACPDriver`); Claude needs the Node adapter (extra runtime dep, and
Claude's ACP is "demonstration-grade" and lags its native features — no native diff
tools); Codex's richest features live in its own app-server, of which ACP is a
subset.

### Option C — reuse the hub's drivers via a local host-runner  ★ recommended
Make the desktop's **local agent an agent on a `localhost` host-runner**, so it is
driven by the *same* `StdioDriver`/`AppServerDriver`/`ACPDriver` the hub already
ships. The desktop already talks to the hub over REST+MCP for remote agents; "local"
becomes "an agent on the localhost host" with **zero new protocol code** and the
full transcript/permission/MCP feature set immediately. Concretely:

- Ship the Go **host-runner as a Tauri sidecar binary** (`bundle.externalBin` +
  the shell plugin's sidecar spawn) and manage its lifecycle from the app; pair it
  with either the user's existing hub or an embedded lightweight hub.
- The AgentCompanion's **"local" source** becomes a thin selector for "spawn on
  localhost host," rendered by the existing `AgentTranscript`/`EventCard` path — the
  `LocalCompanion` one-shot UI is retired.
- **Windows nuance** (the one real gap): the host-runner is POSIX/tmux-bound, so M4
  (JSONL-tail + `tmux send-keys`) can't run there. But **M1 (ACP) and M2 (stdio /
  app-server) need only process spawn + stdio pipes, not tmux** — so a **ConPTY-based
  runner** (already tracked as the "P2 Windows ConPTY runner" and in
  [author-agent-assist-and-diagrams.md](author-agent-assist-and-diagrams.md) §2.4)
  covers Claude/Codex/Gemini on Windows; only the Claude-M4 tail path stays
  POSIX-only. Claude on Windows would run via M2 stream-json, which needs no tmux.

**Why C.** It is the only option that keeps a single agent lifecycle, single
transcript model, and single permission story across local and remote — the whole
premise of "two clients, one API." A and B buy a faster minimal demo at the cost of
a permanently diverging second stack.

### A pragmatic sequencing
1. **Now:** replace the one-shot with **M2 stream-json** for Claude specifically —
   the smallest step to real streaming + tool cards + resume — but implement it as a
   *local host-runner spawn* (reusing `StdioDriver`) rather than new Rust, so it is
   Option C's first slice, not Option A.
2. **Next:** bring Codex (`AppServerDriver`) and Gemini/Kimi (`ACPDriver`) along for
   free once the local runner exists.
3. **Windows:** the ConPTY runner (M1/M2), gated as its own workstream.

---

## 5. Open decisions

- **Embedded hub vs required hub.** Does the desktop bundle a minimal local hub, or
  require the user to point at one? The local-agent appeal was *bypassing* the hub
  for quick help — Option C reintroduces it. Is an embedded single-user hub
  acceptable, or should the local runner speak a cut-down direct protocol?
- **Sidecar packaging.** Bundling the Go host-runner per-OS (three targets) grows the
  installer and adds a build step. Acceptable?
- **Editor-as-server vs app-as-client for Claude.** Should the desktop *also* expose
  the ws-ide MCP server + lockfile, so a user's **own** `claude` in the desktop
  terminal gets native-diff/selection context — in addition to app-spawned agents?
- **Windows Claude parity.** M2 stream-json gives Claude on Windows streaming +
  approvals but not the M4 on-disk-tail fidelity. Is M2-only parity acceptable for
  the Windows local agent?
