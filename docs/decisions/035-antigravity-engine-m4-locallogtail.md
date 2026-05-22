# 035. Antigravity CLI as the fifth engine, via M4 LocalLogTail; Gemini CLI sunset

> **Type:** decision
> **Status:** Proposed (2026-05-22)
> **Audience:** contributors
> **Last verified vs code:** `agy` 1.0.1 on the dev host (model "Gemini 3.5 Flash (High)") on 2026-05-22 — headless `agy -p` runs incl. a live-polled 36-step run (snapshot/rewrite + `RUNNING`→`DONE`), resume-mode probes (interactive `--conversation <id>` works; headless `-p` hangs), a 20-step interactive session (arrow-nav approval menu, `CODE_ACTION` type, one transcript spans a multi-turn spawn), a subagent dispatch (`INVOKE_SUBAGENT` + child brain dir + `SYSTEM_MESSAGE` return), and a full stdio-MCP round-trip via the global config (`tools/call` carries the conversationId in `_meta`)

**TL;DR.** Google is retiring Gemini CLI: on **2026-06-18** the
`gemini` binary stops serving requests for AI Pro/Ultra and free
consumer tiers (enterprise Code Assist licences keep it). The
successor is **Antigravity CLI** (`agy`), which shares the Antigravity
2.0 agent harness. We add `agy` as a **fifth engine** — `antigravity`
— rather than mutating the `gemini-cli` family, which stays for
enterprise users and is marked deprecated. Today `agy` 1.0.1 has
**no `--output-format`** and **no ACP**, so M1 (ACP) and M2
(stream-json) are both unavailable; the only viable driving mode is
**M4 via `LocalLogTailDriver`** (ADR-027). This is exactly ADR-027 D9's
"Phase 2/3 engines" extension point. `agy` writes a tailable
per-conversation transcript JSONL — same shape M4 already consumes —
so a new per-engine adapter plus a family YAML entry is the whole
change. No M1/M2 wiring, no mobile changes (M4 emits identical
`AgentEvent` shapes).

## Context

### The sunset is real and close

Per Google's developer blog, Gemini CLI and the Gemini Code Assist IDE
extensions stop serving consumer-tier requests on 2026-06-18 (~4 weeks
from this ADR). Antigravity CLI is the named successor. It keeps Skills,
Hooks, Subagents, and Extensions (now "plugins"), and offers a
migration helper `agy plugin import gemini`. We must give the fleet a
Gemini-family engine that still works after the cutoff.

### What `agy` 1.0.1 can and cannot do (verified on host)

`agy --help` (binary at `~/.local/bin/agy`):

| Flag | Meaning |
|---|---|
| `-p` / `--print` / `--prompt` | Run a single prompt non-interactively and print the response |
| `-i` / `--prompt-interactive` | Run an initial prompt, then continue interactively |
| `-c` / `--continue` | Continue the most recent conversation |
| `--conversation <id>` | Resume a previous conversation by ID |
| `--dangerously-skip-permissions` | Auto-approve all tool permission requests |
| `--sandbox` | Run with terminal restrictions |
| `--add-dir` | Add a workspace directory (repeatable) |
| `--log-file` | Override the CLI log file path |
| `--print-timeout` | Timeout for `--print` mode (default 5m) |
| subcommands | `changelog`, `help`, `install`, `plugin`/`plugins`, `update` |

**Absent today (the host owner confirmed, and `--help` corroborates):
no `--output-format`/`--json`, and no `--acp`.** Consequences:

- **M1 (ACP) is unavailable** — there is no JSON-RPC-over-stdio
  control channel.
- **M2 (structured stdio) is unavailable** — there is no
  `--output-format stream-json` to parse into typed frames (this is
  exactly what the `gemini-cli` M2 profile depends on; see
  `agent_families.yaml:317`).
- **M4 (LocalLogTail) is available** — `agy` persists a per-conversation
  transcript JSONL on disk that we can tail, and routes input via the
  tmux pane the TUI runs in. This is the same disk-tail + send-keys
  shape ADR-027 built for claude-code.

### On-disk layout (verified)

Store root: `~/.gemini/antigravity-cli/`.

```
~/.gemini/antigravity-cli/
├── conversations/<conversationId>.pb       # protobuf, engine-internal resume state (NOT tailable)
├── cache/last_conversations.json           # { "<workspace abspath>": "<conversationId>" }
├── history.jsonl                           # { display, timestamp, workspace, conversationId } per user line
├── settings.json                           # { enableTelemetry, statusLine, trustedWorkspaces:[...] }
├── mcp_config.json                         # global MCP servers (web-sourced; not present until configured)
└── brain/<conversationId>/.system_generated/logs/
        ├── transcript.jsonl                # tailable event stream (content truncated where large)
        └── transcript_full.jsonl           # same events, untruncated content
```

The authoritative conversation state is the **protobuf** `.pb` — not
human-readable, used by `agy` itself for resume. The **`brain/.../logs/
transcript.jsonl`** is the readable event stream and is our M4 oracle —
analogous to claude-code's `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`.

### Transcript event schema (verified)

Every line is a JSON object with these top-level keys:

`step_index` · `source` · `type` · `status` · `created_at` · `content` ·
`tool_calls` (optional) · `truncated_fields` (optional, `transcript.jsonl` only).

Observed `type` values and how each maps to an `AgentEvent`:

| `type` | `source` | Carries | → AgentEvent |
|---|---|---|---|
| `USER_INPUT` | `USER_EXPLICIT` | the prompt, wrapped in `<USER_REQUEST>…</USER_REQUEST>` + metadata | (drop — already in `agent_events` from the POST; mirrors gemini user-echo handling) |
| `CONVERSATION_HISTORY` | `SYSTEM` | `content: null` | (drop — bookkeeping) |
| `PLANNER_RESPONSE` | `MODEL` | assistant prose + optional `tool_calls[]` | `text` (+ a `tool_call` per entry) |
| `RUN_COMMAND` | `MODEL` | command + output block | `tool_call` / `tool_result` |
| `CODE_ACTION` | `MODEL` | file create/edit (write tool) | `tool_call` / `tool_result` |
| `VIEW_FILE`, `LIST_DIRECTORY`, `SEARCH_WEB`, `GENERIC` | `MODEL` | tool result text | `tool_result` (typed by `type`) |
| `INVOKE_SUBAGENT` | `MODEL` | child `conversationId` + `logAbsoluteUri` | `tool_result` (+ child-discovery hook, §D-subagents) |
| `SYSTEM_MESSAGE` | `SYSTEM`/`MODEL` | injected system context / notice; **also** subagent completion (`sender=<childId> priority=… content=…`) | `text` or drop (TBD by content) |

`tool_calls[]` entries look like `{"name":"list_dir","args":{...}}`.
A multi-turn session interleaves several `USER_INPUT` steps in one
file, so **a single transcript spans the whole spawn** — one tail per
agent (verified: a 20-step interactive session, five user turns over
~9 min, one `transcript_full.jsonl`, monotonic, stable conversationId).

**Snapshot file, rewritten per step — NOT append-only (verified).** The
transcript is a *rewritten snapshot*, not an append log. In a live
36-step run, polling the file throughout showed steps first written with
`status: RUNNING` and later the **same `step_index`** carrying `DONE`;
the final file has **36 lines / 36 unique `step_index`** (no duplicate
lines). So a given step is **updated in place**, last-writer-wins, and
`status` ∈ {`RUNNING`, `DONE`}. Two consequences:

- **The tailer cannot be byte-offset append-following** (claude-code's
  ADR-027 model). The antigravity adapter must **re-read the whole file
  on change and diff by `step_index`**, emitting on new steps and on
  `RUNNING→DONE` transitions.
- **Granularity is per-step, not per-token** — coarser than claude-code
  M4, but no delta-coalescing. Async subtasks can leave a step at
  `RUNNING` even after a headless `-p` returns (observed: a background
  file-search left steps 12/16 `RUNNING` at exit).

**Headless parity (verified).** A headless `agy -p "…"
--dangerously-skip-permissions` run produces the same
`brain/<id>/.system_generated/logs/transcript.jsonl`, and the new
`conversationId` is recorded in `cache/last_conversations.json` keyed by
the workspace abspath. So the host-runner can launch `agy` in a tmux
pane, read back the conversationId, and tail the transcript — no TUI
required for the disk artifact to exist.

## Decision

### D1. Add `antigravity` as a new engine; do not mutate `gemini-cli`

A new `agentfamilies` entry `family: antigravity`, `bin: agy`. The
`gemini-cli` family stays (enterprise users keep `gemini` past the
cutoff) and gains a deprecation note pointing here. Rationale: different
binary, different on-disk layout, different MCP config surface, and a
live install base — superseding in place would churn ~30 files that
reference `gemini-cli` and break enterprise. This mirrors ADR-026
(kimi-code added as the fourth engine = a YAML family, not Go) and the
"behaviour is data" rule.

### D2. Drive `antigravity` via M4 LocalLogTail only, for now

`supports: [M4]` with no M1/M2. Reactivation path is documented data,
not code: when `agy` ships `--acp`, add `M1` + an ACP profile (the
ADR-021 capability surface applies unchanged); when it ships
`--output-format stream-json`, add `M2` + a frame profile. Until then a
spawn that requests M1/M2 for `antigravity` is refused at validation
(per "validate at every boundary").

### D3. Transcript source + path resolution

Follow **`transcript_full.jsonl`** (untruncated content; line volume is
per-step, not per-token). Because the file is a rewritten snapshot
(§"Snapshot file"), the "tailer" is a **watch-and-diff** loop: on each
change re-read the file, key events by `step_index`, and emit on
first-sight and on `RUNNING→DONE`. The other novel piece versus
claude-code is **conversationId discovery**: `agy` mints
the id, so the resolver, after launch, reads
`~/.gemini/antigravity-cli/cache/last_conversations.json` and looks up
the spawn's workspace abspath to get the conversationId, then waits for
`brain/<id>/.system_generated/logs/transcript_full.jsonl` to appear.
(Fallback: newest `brain/*/` mtime under the store root, for the race
before the cache file is written.)

### D4. Event mapping = D3's schema table, in a new adapter

A new adapter at `internal/drivers/local_log_tail/antigravity/` mirrors
the claude_code leaf set (pathresolver, tailer, mapper, sendkeys,
paneresolver, state). Only the **mapper** and **pathresolver** are
genuinely new; the shared `LocalLogTailDriver` skeleton handles
AgentEvent emission and send-keys, but its **tail-from-offset reader is
not reusable here** — the antigravity tailer is watch-and-diff (§D3),
which the skeleton must accommodate (a pluggable reader) or the adapter
supplies its own. The mapper
implements the §"Transcript event schema" table. No delta coalescing
(events arrive whole). `USER_INPUT` and `CONVERSATION_HISTORY` are
dropped to avoid duplicating the bubble already posted from the mobile
POST (same reasoning as the gemini user-echo drop, ADR-013).

### D5. Input + approval navigation via tmux send-keys (arrow nav)

Launch `agy` interactively in the spawn's tmux pane and route mobile
input with `tmux send-keys` (the shared M4 mechanism); the paneresolver
reuses the claude-code approach. Multi-turn within one live process
appends to a single transcript (verified), so the process stays up and
each turn is a send-keys, not a relaunch. The permission prompt is an
**arrow-up/down list confirmed with Enter** (verified on host) — the
same Ink-style menu shape ADR-027 D5 chose arrow-navigation for, so the
existing send-keys arrow-nav approach ports directly.

### D6. Permission state read from the pane, not the transcript

`agy` has **no `--permission-prompt-tool` equivalent** — only flag-time
`--dangerously-skip-permissions` (and `--sandbox`). Critically, the
transcript logs only the **resolved** outcome: an approved command
appears as a `RUN_COMMAND`/`PLANNER_RESPONSE` step reading "successfully
approved and executed" (verified) — the *pending* approval prompt is
**never written to the transcript**. So a transcript tail cannot detect
that the agent is blocked on a prompt; the **pending state must be read
from the pane** (`tmux capture-pane`), the co-determination half of
ADR-027 D4 (rules-on-disk does not apply, but capture-pane does). Two
modes:

- **Auto-approve** (`--dangerously-skip-permissions`): no prompts;
  risky/higher-order decisions routed through the vendor-neutral,
  turn-based `request_approval` (gemini precedent, ADR-013 D4).
- **Interactive approve**: detect the menu via capture-pane, surface it
  as an attention item, answer with send-keys arrow-nav + Enter.

The ADR-027 W6 hook installer step is **skipped** for this engine.

### D7. MCP wiring goes in the global `~/.gemini/config/mcp_config.json`

**Corrected on host (2026-05-22)** — the web's workspace
`.agents/mcp_config.json` claim is wrong/incomplete: `agy`'s discovery
(`discovery.go:334`, per its own logs) reads the **global**
`~/.gemini/config/mcp_config.json`, and never touched a workspace
`.agents/mcp_config.json` we planted. Schema is the standard
`{"mcpServers": {"<name>": {"command","args","description"}}}` (stdio) —
the gemini/claude family (which is why `agy plugin import gemini`
works); remote servers use `serverUrl`.

**Verified working (full round-trip, host 2026-05-22):** with a valid
global config, `agy` launches the stdio server, completes the handshake
(`initialize` as `clientInfo:antigravity-client`, **protocolVersion
`2025-11-25`**, caps `elicitation`+`roots`), `tools/list`-discovers the
tool, and **calls it** — a test `ping` returned its unique token through
to agy's stdout. Crucially, every `tools/call` carries a `_meta` block:
`antigravity.google/conversation_id`, `antigravity.google/artifacts_dir`,
and a `progressToken`. **The conversationId on every MCP call is a
built-in correlation hook** — the hub MCP server can attribute each call
to the spawn without extra plumbing (compare the per-spawn token the
claude-code path threads). Confirmed over **both** headless `-p` and the
**interactive TUI** pane path (the TUI re-inits the server per session
and emits `notifications/roots/list_changed`; the `tools/call` path is
identical). *(Two earlier `-p` runs that hung were not reproduced and
look like model latency, not an MCP fault.)*

**Consequence for the launch path.** `agy` MCP config is **host-global,
not per-spawn** — the claude-code model of writing a per-workdir
`.mcp.json` does not port. Options for W-impl: (a) merge the `termipod`
egress-proxy entry into the global `~/.gemini/config/mcp_config.json`
once per host (simplest; but global to every `agy` on the host), or
(b) per-spawn isolation via a private `HOME`/config dir per agent so the
"global" file is actually per-spawn. **(b) is preferred** for tenant
isolation; pick in the plan. Also fix the latent bug observed: an
*empty* `mcp_config.json` makes `agy` log a JSON parse error every run —
write `{}` not an empty file.

### D8. Resume on respawn via interactive `--conversation <id>`; conversationId is the cursor

`engine_session_id` ← the `agy` conversationId (from
`cache/last_conversations.json`), used as the transcript path key, for
bookkeeping, and as the **resume cursor** (ADR-014 pattern). On respawn
we relaunch the agent **interactively in the pane** with
`--conversation <id>`, which reattaches and appends to the same
transcript (verified). The headless `-p` resume path is a trap; the
mode matters.

**Resume path by mode (verified):**

- *Interactive* `agy --conversation <id>` (the pane path M4 uses)
  **works** — it reattaches and appends to the same transcript across
  turns. So on respawn we relaunch interactively with the recorded
  conversationId.
- *Headless* `agy --conversation <id> -p "…"` **hangs** in `agy` 1.0.1
  (exit 124, three probes; `--print-timeout` does not save it) — avoid.
- *Headless* `agy --continue -p` works (exit 0) but is cwd-scoped and
  starts fresh if the cwd has no prior conversation — usable only as a
  fallback when the explicit id is unavailable.

Since M4 drives interactively, the hang is not on the critical path:
respawn uses interactive `--conversation <id>`, with `--continue`-from-
workdir as the degraded fallback.

### D9. Frame behaviour stays data

The family entry declares `frame_translator: profile` with a
`frame_profile` describing the transcript schema, plus
`prompt_image/pdf/audio/video: {M4: false}` and `default_auth_method`
(oauth-personal, reusing the existing Google OAuth cache at
`~/.gemini/antigravity-oauth-token`). A future M1/M2 reactivation adds
profiles without touching Go.

### D-subagents. MVP ignores engine-native subagents (all engines)

Every engine ships its own in-process subagent mechanism — `agy`'s
`invoke_subagent` (verified: a child conversation with its own brain
dir/transcript; result returns to the parent as a `SYSTEM_MESSAGE`
`sender=<childId>`), claude-code's Task/sub-agents, codex's, etc. **For
MVP, termipod ignores all of them.** The antigravity adapter tails
**only the parent** transcript; it does not discover, tail, or
attribute engine-spawned children (the parent transcript already shows
dispatch via `INVOKE_SUBAGENT` and the rolled-up child result via
`SYSTEM_MESSAGE`, so a single tail is still a coherent view). This is an
engine-agnostic stance, not antigravity-specific.

**Why ignore, and the caveat to revisit.** An engine-native subagent is
a *second* orchestration layer beneath termipod's own: it is not a
termipod agent, gets no `agents` row, no scope manifest (ADR-016), and
its messages ride the engine's private bus, not the termipod message
envelope (ADR-032) or loop-closure runtime (ADR-034) — a governance
blind spot and a duplicated-orchestration smell. MVP treats engine
subagents as opaque internal tool-use of the parent agent. Templates
**prefer termipod task dispatch** over engine-native fan-out. The deeper
reconciliation (govern vs forbid vs surface engine subagents) belongs in
a cross-engine discussion doc, not this ADR.

## Consequences

**Positive.** The fleet keeps a Google engine past 2026-06-18 with no
mobile changes (M4 emits identical AgentEvent shapes). Whole-event
(non-streaming) transcripts make the mapper simpler than gemini's
delta-only stream. `gemini-cli` is untouched, so enterprise users and
the existing M1/M2/M4 wiring keep working.

**Negative.** No live token streaming — updates arrive in step-sized
chunks, not token-by-token (coarser UX than claude-code M4). The
transcript is a rewritten snapshot, so the shared tail-from-offset
reader doesn't apply: this engine needs a **watch-and-diff** reader,
which either generalises the `LocalLogTailDriver` skeleton or forks a
reader for it. conversationId discovery adds a launch-time race the
resolver must tolerate. Permission co-determination (ADR-027 D4) does
not apply, so the only gate is launch-flag + `request_approval`.

**Now forbidden / superseded.** ADR-027 D9 named a future `gemini-cli`
M4 adapter at `~/.gemini/tmp/<wd>/chats/…jsonl`. For the Gemini family
that path is moot post-sunset; the LocalLogTail Google adapter targets
`antigravity` at the `brain/<id>/…/transcript_full.jsonl` path instead.

## Open questions (host-verify before/while implementing)

1. ~~**Resume.**~~ **RESOLVED (2026-05-22):** interactive
   `agy --conversation <id>` reattaches and appends across turns
   (verified); headless `--conversation <id> -p` hangs (avoid);
   `--continue -p` is a cwd-scoped fallback. Folded into D8.
2. ~~**MCP config (D7).**~~ **RESOLVED (2026-05-22):** config is the
   **global** `~/.gemini/config/mcp_config.json` (not workspace
   `.agents/`); standard `{"mcpServers":{name:{command,args}}}` schema;
   a stdio server connects + lists + is **called** end-to-end (token
   returned), with the conversationId in `tools/call` `_meta`. Confirmed
   over **both** headless `-p` and the **interactive TUI** pane path
   (TUI also re-inits the server per session and emits
   `notifications/roots/list_changed`). Folded into D7. Only plan-time
   item left: pick global-vs-per-spawn config isolation (D7 option a/b).
3. ~~**Intermediate status.**~~ **RESOLVED (2026-05-22):** live runs do
   write `status: RUNNING` then update the same `step_index` to `DONE`
   in a rewritten snapshot file — the tailer is watch-and-diff with
   last-writer-wins, not append-follow. Folded into §"Snapshot file" + D3/D4.
4. ~~**Permission UX in TUI.**~~ **RESOLVED (2026-05-22):** an
   arrow-up/down list confirmed with Enter (send-keys arrow-nav ports
   from ADR-027 D5); the *pending* prompt is not in the transcript, so
   it must be read via capture-pane. Folded into D5/D6.
5. ~~**Subagents.**~~ **RESOLVED (2026-05-22):** each subagent is its
   own conversationId with its **own** `brain/<childId>/…/transcript_full.jsonl`
   (not in the parent, not in the workspace cache). The parent transcript
   is fully legible: an `invoke_subagent` tool_call → an `INVOKE_SUBAGENT`
   result carrying the child `conversationId` + `logAbsoluteUri` → the
   child's completion arrives back as a `SYSTEM_MESSAGE`
   (`sender=<childId>`, `priority`). See §D-subagents for the MVP
   stance + the orchestration-overlap caveat.

## References

- Code: `internal/drivers/local_log_tail/` (shared skeleton +
  claude_code adapter to mirror); `internal/hostrunner/launch_m4_locallogtail.go`;
  `internal/agentfamilies/agent_families.yaml` (`gemini-cli` entry at
  `:262`, the frame-profile precedent at `:315`).
- On-host: `agy` 1.0.1, `~/.gemini/antigravity-cli/`.
- Related ADRs: [027](027-local-log-tail-driver.md) (M4 LocalLogTail,
  D9 extension point); [013](013-gemini-exec-per-turn.md) (Gemini
  engine + permission/user-echo precedent); [026](026-kimi-code-engine.md)
  (engine-as-YAML precedent); [014](014-claude-code-resume-cursor.md)
  (resume-cursor pattern); [010](010-frame-profiles-as-data.md)
  (behaviour-is-data); [021](021-acp-capability-surface.md) (the M1
  reactivation surface).
- External: Google Developers Blog — "Transitioning Gemini CLI to
  Antigravity CLI" (sunset 2026-06-18).
