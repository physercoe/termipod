# Wedge: Single-Agent Demo (claude-only v1)

> **Status:** proposed · **Owner:** physercoe · **Estimate:** ~3.75 dev-days
> **Parent:** [agent-harness.md](../agent-harness.md) §11.1 (B1–B5 single-agent path)
> **Demo lineage:** demo-choice-locked (nanoGPT-Shakespeare) · supersedes nothing

## Goal

Cut the smallest path from "user opens an empty mobile app with one host
they SSHed into 30 seconds ago" to "user is talking to their own Claude
Code instance through the mobile channel UI, with permission gates and
worktree isolation working end-to-end".

This is the **first testable harness milestone**: a single steward,
running `claude`, replying in a chat-like channel surface that matches
[Happy](https://github.com/slopus/happy) ergonomics. Multi-agent fan-out
(B6–B9) is explicitly out of scope.

## Non-goals

- **Codex backend.** Defer to fast-follow; the codebase keeps it pluggable
  but v1 ships claude-only. Rationale: codex `exec --json --resume` flow
  is shaped differently enough (per-turn process, no long-lived stdio)
  that bundling it doubles the surface area.
- **Spawned children.** A steward calling `delegate` to launch a worker
  pane is B6+. The steward in v1 has `spawn.descendants: 20` capability
  on paper but no UI to fan out.
- **Checkpoint / budget enforcement** (agent-harness B3). Sessions in v1
  are unbounded; cost dashboards are post-demo.
- **State separation** (B5). Worktrees give us per-agent FS isolation
  for free; per-agent DB schema and credential scoping wait.
- **M1 ACP launcher.** `driver_acp.go` exists but no spawner wires it.
  M2 is sufficient for claude-only — Claude Code's stream-json is the
  canonical M2 input.

## Acceptance criteria

The wedge is done when a fresh user can:

1. **AC1 — One-line host bootstrap.** SSH into a host, run a single
   curl-piped installer command from the team's host-add screen, and
   see the host appear in the mobile hosts list within 30s. The
   installer creates `$HOME/hub-work` if missing.
2. **AC2 — Bootstrap sheet.** Open the mobile app to a team that has
   ≥1 connected host but zero stewards. A "Start your steward" sheet
   auto-presents — claude is the only backend choice — with an editable
   persona seed.
3. **AC3 — Steward channel renders.** After bootstrap, the team's
   default channel renders the steward's first turn as a chat-style
   transcript: assistant text bubbles, tool-call cards (collapsed by
   default with the tool name + first-arg preview), and result cards.
   No raw JSON is visible.
4. **AC4 — User → steward typing works.** Typing "hello, who are you?"
   into the channel composer produces a stream-json `user` frame on
   the steward's stdin and a streamed reply within ~2s. The reply
   shows token-by-token, not buffered-on-newline.
5. **AC5 — Permission gates land in attention_items.** When the
   steward attempts a tool the user has not pre-approved (e.g.
   `Bash(rm …)`), a permission prompt arrives as a hub
   `attention_items` row with kind=approval_request. The mobile
   inbox renders it with Approve / Deny buttons; the response
   resolves the prompt within the same turn.

**diff-1 — testable differentiator.** The Happy-equivalent moment:
the user can see *what tool the agent is about to run, before it runs*,
and stop it from their phone. Without AC5, this is a fancy SSH client.
With it, it's a director's harness.

## Architecture (v1, claude-only)

```
mobile/composer ──▶ hub /v1/teams/{t}/agents/{id}/input
                                │
                                ▼
                    host-runner SSE input subscription
                                │
                                ▼
              StdioDriver.WriteFrame(`{"type":"user",…}`)
                                │
                                ▼
                       claude (M2 child of host-runner)
                  stdin: stream-json   stdout: stream-json
                                │
                                ▼
            StdioDriver parses → AgentEventPoster → hub
                                │
                                ▼
                      mobile channel renders chat
```

Permission tool callback (out-of-band of the main I/O):

```
claude attempts tool ──▶ MCP gateway tool `permission_prompt`
                                │
                                ▼
                  host-runner mcp_gateway records attention_item
                                │ (kind=approval_request, blocking)
                                ▼
              user taps Approve in mobile inbox ──▶ resolves item
                                │
                                ▼
            host-runner returns {"behavior":"allow"} to claude
```

### Existing infrastructure we reuse

This wedge is small because most pieces are built. Inventory of what's
already there, with file pointers:

| Capability                                    | File / symbol                                          |
| --------------------------------------------- | ------------------------------------------------------ |
| M2 driver (stream-json → agent_events)        | `hub/internal/hostrunner/driver_stdio.go` (StdioDriver) |
| M2 launcher (spawn child, tee log, pane)      | `hub/internal/hostrunner/launch_m2.go` (`launchM2`)    |
| Bidirectional stdin (frame writer)            | `StdioDriver.Stdin` already wired (line 36-37 doc)     |
| Per-agent worktree creation                   | `hub/internal/hostrunner/worktree.go` (EnsureWorktree) |
| MCP gateway per host-runner                   | `hub/internal/hostrunner/mcp_gateway.go`               |
| Approval-flavored attention items             | existing `attention_items` table, kind=approval_request|
| Host registration (one-line installer)        | hub host-add flow + `cmd/host-installer`               |
| Agent template loading                        | `hub/templates/agents/steward.v1.yaml`                 |

### Gaps the wedge fills

| Gap                                                       | Wedge task |
| --------------------------------------------------------- | ---------- |
| `~/hub-work` not auto-created on host                     | W0         |
| `default_workdir` field declared in YAML but never parsed | W1         |
| `launchM2` does not honor any workdir (cmd starts in `$HOME`) | W1     |
| Steward template `cmd:` lacks stream-json + perm-tool flags | W1       |
| `prompt:` field never read; CLAUDE.md not materialized    | W1.5       |
| `permission_prompt` MCP tool not registered               | W2         |
| No mobile bootstrap sheet (auto-open when team is empty)  | W4         |
| Channel renders raw markers, not tool-call cards          | W5         |

## Reference: claude command line invocation

The steward template's `backend.cmd` becomes (single line in YAML):

```
claude --model opus-4-7 \
       --print \
       --output-format stream-json \
       --input-format stream-json \
       --permission-prompt-tool mcp__termipod__permission_prompt
```

Why each flag:

- `--print` — non-interactive (no TUI repl). Required for stream-json.
- `--output-format stream-json` — Claude emits one JSON object per line:
  `{type:"system"|"assistant"|"user"|"result", …}`. The driver already
  knows how to parse these.
- `--input-format stream-json` — the *bidirectional* mode. Claude reads
  user turns as `{type:"user", message:{role:"user", content:[…]}}`
  frames on stdin and stays alive across turns. **Without this flag,
  claude exits after one print.**
- `--permission-prompt-tool mcp__<server>__<tool>` — Claude routes any
  tool-use that would normally show an interactive permission UI to
  this MCP tool instead. Our MCP gateway exposes
  `permission_prompt(tool_name, input) → {behavior, updatedInput|message}`
  and bridges to attention_items.

The MCP server name `termipod` and tool path
`mcp__termipod__permission_prompt` are determined by what the
host-runner's MCP gateway advertises in the agent's `~/.claude.json`
(or via `claude mcp add`). W2 covers wiring that registration.

## JSONL → agent_events mapping

Claude's stream-json output produces these frame types. The driver
already emits agent_events for most; the table makes it explicit so the
mobile renderer (W5) knows what to expect.

| Claude frame                                    | agent_event kind         | Mobile render |
| ----------------------------------------------- | ------------------------ | ------------- |
| `{type:"system", subtype:"init", session_id:…}` | `session.start`          | header chip "Session started" |
| `{type:"assistant", message:{content:[{type:"text",text:…}]}}` | `assistant.text`         | chat bubble (streamed) |
| `{type:"assistant", …, content:[{type:"tool_use",name,input}]}` | `assistant.tool_call`    | collapsed tool card with name + first arg |
| `{type:"user", …, content:[{type:"tool_result",content,is_error}]}` | `tool.result`            | expand-on-tap result, red border if `is_error` |
| `{type:"result", subtype:"success", duration_ms,total_cost_usd}` | `turn.complete`          | footer: "12.3s · $0.04" |
| `{type:"result", subtype:"error", …}`           | `turn.error`             | inline error banner |

The MCP `permission_prompt` callback is *not* in this stream — it
travels through the host-runner MCP gateway as a separate channel.

## Wedge tasks

### W0 — Host installer creates `~/hub-work` (~0.25d)

**Why option (a):** The steward template hard-codes
`default_workdir: ~/hub-work`. If the directory doesn't exist when
launchM2 spawns claude, claude will start in `$HOME` (option b: launcher
mkdir's it on demand) or refuse (option c: error and surface). Option
(a) — installer does it once, statically — keeps launch_m2 simple and
makes the directory visible to a sysadmin who SSHs in to inspect.

**Changes:**

- `cmd/host-installer/install.sh` (or whatever the curl-piped script is
  called today): add `mkdir -p "$HOME/hub-work"` after the binary is
  installed, before the systemd unit (or equivalent) is enabled.
- Document in the host-add screen's installer copy: "Creates
  `~/hub-work` for steward worktrees."

**Done when:** running the installer on a fresh host yields a
`$HOME/hub-work` directory with mode 0755.

### W1 — Steward template + workdir plumbing (~0.5d)

**1.1** Update `hub/internal/hostrunner/spec.go`:

```go
Backend struct {
    Cmd            string `yaml:"cmd"`
    DefaultWorkdir string `yaml:"default_workdir"`
} `yaml:"backend"`
```

**1.2** Honor it in `launch_m2.go`. RealProcSpawner currently runs
`exec.CommandContext(ctx, "bash", "-c", command)`. Add an explicit
workdir parameter to the Spawn signature, or wrap the command in
`cd <workdir> && <command>` (preferred — keeps the `bash -c` shape so
shell expansion of `~` still works).

```go
if spec.Backend.DefaultWorkdir != "" {
    command = fmt.Sprintf("cd %s && %s",
        shellEscape(expandHome(spec.Backend.DefaultWorkdir)),
        command)
}
```

(`expandHome` is needed because `bash -c "cd ~/hub-work && …"` would
work, but expanding here lets us validate the directory exists before
spawn and emit a clear error if not.)

**1.3** Update `hub/templates/agents/steward.v1.yaml` `backend.cmd`:

```yaml
cmd: "claude --model opus-4-7 --print --output-format stream-json --input-format stream-json --permission-prompt-tool mcp__termipod__permission_prompt"
```

**1.4** Set the steward's `driving_mode: M2` (or whatever the runner
calls the mode-selection knob — verify in `runner.go` line ~408 where
the M4 fallback comment lives). M1 launcher absent → must explicitly
choose M2.

**Done when:** spawning a steward on a real host produces a claude
process whose cwd is `$HOME/hub-work`, visible via `lsof -p <pid> | grep cwd`.

### W1.5 — Materialize CLAUDE.md into workdir (~0.5d)

The steward template already declares `prompt: steward.v1.md`, but
nothing reads it. Without CLAUDE.md sitting in the workdir, Claude
Code launches as a generic assistant — no persona, no etiquette
rules, no decomposition recipes. M2 supports CLAUDE.md (it's the
same Claude Code binary as M4), but the launcher has to write the
file.

**1.5.1** Hub-side: extend `hub/internal/server/template.go` to
support dotted variable names so prompts can reference
`{{principal.handle}}` (the bare handle, no `@`-prefix). Regex →
`\{\{\s*([a-z_][a-z0-9_.]*)\s*\}\}`; `principal.handle` is added
to the var map alongside the existing `principal` key.

**1.5.2** Add `(s *Server) resolveContextFiles(rendered, vars)` to
the same file. It reads the `prompt:` field out of the rendered
spec, loads the prompt from
`<dataRoot>/team/templates/prompts/<name>` (falling back to the
embedded FS), expands `{{var}}` placeholders against the same
binding map renderSpawnSpec used, and inlines the result under
`context_files.CLAUDE.md` in the spec YAML.

**1.5.3** Call resolveContextFiles in `DoSpawn` right after
`renderSpawnSpec` so the persisted `spawn_spec_yaml` carries the
inlined CLAUDE.md.

**1.5.4** Host-runner: add
`ContextFiles map[string]string \`yaml:"context_files"\`` to
`hub/internal/hostrunner/spec.go::SpawnSpec`. In
`launch_m2.go`, before `Spawner.Spawn`, walk
`spec.ContextFiles` and write each entry under the expanded
`default_workdir` (creating parents as needed). Reject keys that
escape the workdir or set context_files without a workdir.

**Done when:** spawning a steward yields
`$HOME/hub-work/CLAUDE.md` with the rendered persona body, and
the launched claude process can quote its principal-handle on
the first turn.

### W2 — Permission tool through MCP gateway (~1.0d)

**2.1** Register a `permission_prompt` tool in
`hub/internal/hostrunner/mcp_gateway.go`. Per Anthropic's spec the tool
takes:

```json
{ "tool_name": "Bash", "input": { "command": "rm -rf /tmp/foo" } }
```

…and must return:

```json
{ "behavior": "allow", "updatedInput": { … } }
// or
{ "behavior": "deny", "message": "user declined" }
```

**2.2** When the gateway receives the call, it:

1. POSTs an `attention_items` row to the hub
   (`kind=approval_request`, payload includes tool_name + input,
   target=this agent's session/turn).
2. Long-polls (or SSE-subscribes) for the resolution.
3. Returns the appropriate response shape to claude.

**2.3** Hub-side: `attention_items.resolve` endpoint already exists
for other approval flavors; wire the resolution payload (allow/deny +
optional updated_input) so it round-trips back to the host-runner.

**2.4** Make sure the MCP gateway advertises the tool such that the
`mcp__termipod__permission_prompt` path resolves. The exact
registration is whatever pattern the gateway already uses for its
existing tools (mirror it, don't invent).

**Done when:** asking the steward "delete /tmp/foo" produces a phone
notification, tapping Deny in the mobile inbox makes claude reply
"I won't do that — the user declined", and the attention_item is
marked resolved.

### W4 — Mobile bootstrap sheet (~1.0d)

**Trigger:** The team route (`/teams/<id>`) detects:

```
team.hosts.where(connected).count >= 1 AND team.agents.count == 0
```

…and pushes a non-dismissible (but skippable) sheet over the channel
list. "Skip" sets a `bootstrap_dismissed_at` flag on the user-team
membership so the sheet doesn't reappear; "Start steward" runs the
flow.

**Sheet content:**

```
┌─────────────────────────────────────┐
│  Start your steward                 │
│  ─────────────────                  │
│  Your steward is the AI you talk to │
│  about the team. It runs on:        │
│   ◉ host-alpha   (selected)         │
│                                     │
│  Backend                            │
│   ◉ Claude Code (claude-3-opus)     │
│                                     │
│  Persona seed (optional)            │
│  ┌─────────────────────────────────┐│
│  │ You're the steward of project   ││
│  │ Foo. Be terse and direct.       ││
│  └─────────────────────────────────┘│
│                                     │
│             [Cancel]  [Start →]     │
└─────────────────────────────────────┘
```

**On Start:** mobile calls existing
`POST /v1/teams/{t}/agents/spawn` with template=agents.steward,
host_id=<selected>, plus the persona seed appended to the prompt
override field (template still owns `prompt: steward.v1.md`; the seed
is concatenated in the spawn payload). Sheet dismisses, channel list
shows a new "general" channel with the steward attached, transcript
starts streaming as soon as `session.start` arrives.

**Done when:** a tester with a clean app + connected host but no
steward sees the sheet on first open of the team, can complete it,
and lands on a live transcript.

### W5 — Mobile channel renders structured events (~1.0d)

The channel is currently a thin wrapper around hub message history.
Extend the renderer so an agent_event stream becomes a chat transcript:

**Component layout (Flutter):**

```
ChatTranscript
├── _SessionHeader      ← session.start frame
├── _AssistantBubble    ← assistant.text (streamed; subscribe to delta)
├── _ToolCallCard       ← assistant.tool_call (collapsed by default)
│   └── _ToolResultRow  ← tool.result (expand-on-tap)
├── _PermissionPrompt   ← inline card driven by attention_items live
│                          for this turn (hub pushes via SSE)
└── _TurnFooter         ← turn.complete (latency, cost)
```

Reuse existing `attention_items` SSE subscription so the prompt card
appears in-line in the transcript at the moment Claude pauses, not
just in the global inbox. Tapping Approve/Deny resolves the same row
the inbox would.

**Streaming text:** assistant.text frames carry deltas. Keep a
ChatBubbleController that appends to the active bubble until a
`turn.complete` arrives, then locks the bubble and starts a new one.

**Composer → input:** existing channel composer's "send" button
already POSTs to `/messages`; for steward channels, add a branch that
POSTs to `/v1/teams/{t}/agents/{id}/input` instead with a stream-json
user frame body. Hub forwards the bytes to the host-runner via the
existing SSE input subscription.

**Done when:** the AC1–AC5 round-trip works on a real device.

## Out of scope (fast-follow / post-demo)

- **Codex backend.** Once W1–W5 are stable, add a second backend
  template (`agents.steward-codex`) that uses `codex exec --json` per
  turn with `--resume <session_id>`. The mobile bootstrap sheet's
  Backend radio gets a second option. ~1.5d, isolated.
- **Worktree-per-steward.** v1 steward runs in `~/hub-work` flat. If
  multiple stewards on the same host become a thing, swap `cd <workdir>`
  for `EnsureWorktree(handle)` so each gets `~/hub-work/<handle>`.
  Already wired for spawned children; just not used for the steward.
- **Checkpoint snapshots** (B3). The session_id from the system-init
  frame is the only handle to claude's resume. v1 trusts the CLI to
  manage it; v2 records it in `agent_runs.checkpoints`.
- **Spawn fan-out** (B6+). The differentiator that makes us not-Happy
  but it can wait until single-agent stops being embarrassing.

## Verification

A scripted demo that proves the wedge:

1. Fresh dev hub + fresh host VM. Run installer, watch host appear.
2. Open mobile, see bootstrap sheet, tap Start (default values).
3. Wait for `session.start` chip, type "list files in this directory".
4. See `assistant.tool_call` card render with tool=Bash; expand it; see
   the command. Tap Approve; result card appears underneath.
5. Type "what's in /etc/shadow". See approval prompt; tap Deny. Steward
   replies acknowledging.
6. Cold-kill the steward process on the host (`pkill claude`). Verify
   the channel shows a clean `turn.error` and the mobile offers a
   "restart" affordance. (Light coverage — full auto-restart is B2.)

## Open questions

1. **Persona seed placement.** Concatenate to template prompt vs. send
   as the first user turn? Concatenation keeps the prompt out of the
   visible transcript. **Lean: concatenate** (matches Happy / Cursor).
2. **Bootstrap dismissal scope.** Per-user-per-team or global per-user?
   **Lean: per-team** so a user joining a second team gets the sheet
   again.
3. **Tool-call card preview length.** First 80 chars of the first arg
   feels right; longer for `Bash` since the command is the whole
   point. **Lean: kind-aware preview** — Bash shows full command up
   to ~120 chars, others show first arg.
4. **Permission TTL.** If the user never resolves an attention_item,
   does claude wait forever? Default claude behavior is to wait. We
   should probably auto-deny with `message: "timed out"` after, say,
   5 minutes — but that's policy, not plumbing, so flagging here.

## Total

5 wedges, ~3.75 dev-days, claude-only, lands the agent-harness B1–B5
single-agent path with the differentiator (AC5/diff-1) intact.
