# M4 replacement — local log tail vs other interception layers

> **Type:** discussion
> **Status:** Active (2026-05-15) — design locked + empirically validated 2026-05-15; spec frozen in [plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md); ADR-027 accepted + amended same day. Flip to Resolved once the adapter ships.
> **Audience:** principal · contributors · reviewers
> **Last verified vs code:** claude-code 2.1.129; 200k-line live JSONL sample + 9-hook payload corpus on 2026-05-15

**TL;DR.** The current M4 [driving mode](../reference/glossary.md#driving-mode)
renders an interactive agent's TUI by piping the PTY screen through
a headless xterm VT state machine — works, but on a phone the
result is a messy text dump that lacks the semantic cards M1/M2
produce. The principal asked: can we tap a deeper, more structured
layer than the terminal screen? This doc records the comparative
survey (PTY screen-scrape vs network/TLS interception vs on-disk
session logs), why on-disk JSONL tailing won as the **transcript**
source, why claude-code's **hook surface** (via the existing
host-runner MCP gateway) won as the **TUI-interactive-state**
source, the empirical validation that proved both, and the
trade-offs we explicitly accept. The plan is in
[plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md);
this doc is the *why* behind it.

**Design evolution within the same day (2026-05-15):** an earlier
draft of this doc proposed a capture-pane regex probe to detect
"awaiting approval" state. The on-device probe (`hub/cmd/probe-claude-hooks/`)
established that claude-code's hook surface provides this signal
**structurally** — `Notification.notification_type:"permission_prompt"` +
`PreToolUse(ExitPlanMode).tool_input.plan` carry every field the
adapter needs without regex. Capture-pane is removed entirely; the
sections below that reference it are kept for the historical record
of *why* it was considered and *why* we abandoned it.

---

## 1. Framing — what the principal asked

> *"M4 mode session transcript page can show the text but not user
> friendly and a lot of redundant info across different cards. Are
> there better ways to handle this (exclude using our tmux backend)?
> Can we decode the agent-CLI's data through a deeper layer, such as
> network or stdio, since the agent-CLI communicates over a
> structured protocol?"*

Two requirements distill from the directive:

1. **Don't regress the streaming experience.** The principal later
   clarified: redundancy across cards is not the issue — the
   *streaming feel* (per-block updates, no collapse) is the right
   behavior. The fix is to expose the right structured units to the
   mobile card renderer, not to merge cards that the streaming
   exposes.
2. **Stop relying on the alt-screen VT replay.** Coding-agent TUIs
   own the alt-screen and repaint it aggressively. Anything we
   decode out of that screen is a lossy reconstruction of what the
   agent emitted to its vendor's API. There's a richer source —
   find it.

---

## 2. The interception-layer survey

The agent CLI is the only thing that sees a structured event stream
end-to-end (vendor SSE → in-process events → screen render). We can
tap that stream at four candidate layers:

| Layer | What's available | Verdict |
|---|---|---|
| **PTY screen (current M4)** | Post-VT pixels via headless xterm | Already in use, already too lossy — alt-screen repaint destroys event boundaries. |
| **Process stdio of the CLI in non-interactive mode** | Newline-delimited JSON when CLI is launched with `--output-format=stream-json` | This is M1 / M2; requires controlling launch, removes the TUI. Out of scope for "user already has it running interactively." |
| **Network (TLS) — vendor SSE** | `content_block_delta` / `tool_use` deltas straight from the vendor | eCapture (eBPF uprobes on `SSL_read`) works for Node-based engines (claude, gemini) but **fails on codex's rustls** — no `SSL_read` symbol to hook. mitmproxy works across all four (no cert pinning observed today) but requires CA trust + `HTTPS_PROXY` per process on every host the user SSH's into. Friction, root in the eBPF path, no unified mechanism. |
| **On-disk session logs** | The CLI itself appends a structured JSONL to disk during the session | All four engines have a path. Live-buffered. Stock. No privileges. **JSONL is richer than the SSE stream** — `tool_use` ↔ `tool_result` already correlated, `thinking` blocks segregated. |

The **on-disk JSONL** layer wins on simplicity and privilege, and
it's *strictly richer* than the wire stream because the CLI has
already done the work of pairing tool_use↔tool_result and tagging
content-block types.

### 2.1 Per-engine path confirmation (2026-05-14)

The principal supplied paths after the initial survey, and we
verified on this host where applicable:

| Engine | Path | Verified live-tailable |
|---|---|---|
| claude-code | `~/.claude/projects/<urlencoded-cwd>/<session-uuid>.jsonl` | ✅ — 773 MB / 200k lines, mtime advancing during this session |
| codex | `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-*.jsonl` | ✅ — present, schema matches OpenAI Responses-API item shape |
| gemini-cli | `~/.gemini/tmp/<workdir>/chats/session-<id>.jsonl` + `chats/<sid>/<nid>.jsonl` + `logs.jsonl` | Live-tailable per principal; verification post-MVP |
| kimi-code | `~/.kimi/sessions/<hash>/<uuid>/context.jsonl` | Live-tailable per principal; verification post-MVP |

For MVP, only claude-code's path matters — the other three become
Phase 2/3 adapters of the same shape.

### 2.2 What we ruled out and why

- **asciinema / script / ttyrec.** Cannot attach to an existing PTY
  owned by another process. Requires launching the session under
  the recorder, which the user already did when they started
  `claude` interactively. Workflow disruption.
- **strace / bpftrace on the agent process.** Same byte-shape as
  pipe-pane, plus CAP_SYS_PTRACE / root and a noticeable perf hit.
  No information advantage over either pipe-pane or eCapture.
- **Switching the user from tmux to Zellij** for its plugin/WASM
  pane API. Pane *content* remains VT bytes; the gain is metadata
  only. Migration cost is enormous relative to the win.
- **Shell-hook approaches (PROMPT_COMMAND, preexec, Atuin).** Don't
  fire inside a TUI — the agent owns the screen, not the shell's
  REPL.
- **xdotool / ydotool / TIOCSTI ioctl.** Either irrelevant for SSH
  (desktop-only), or restricted on modern kernels (Debian/Ubuntu
  default `dev.tty.legacy_tiocsti_restrict=1` since 2023).

---

## 3. Permission detection — from disk-rule prediction to hook structural signal

**The original framing** (early 2026-05-15 thread) treated permission
detection as a JSONL-vs-screen problem. Findings at that stage:

1. The `tool_use` JSONL block carries no approval-state flag —
   fields are `{type, id, name, input, caller:{type:"direct"}}`.
2. claude-code's permission rules live on disk (`~/.claude/settings.json`
   + `<cwd>/.claude/settings.local.json`) and are live-written when
   the user picks "always allow."

This led to a design that **predicted** approval-required from disk
rules + a 600 ms grace + a `tmux capture-pane` probe to confirm
the prompt was visible.

**The principal then redirected the goal:** M4 in real usage runs
`--dangerously-skip-permissions` or `acceptEdits` — permissions are
bypassed, so this whole pipeline was solving the wrong problem.
The *interactive* events that DO fire even in bypass mode (plan-mode
approval, compaction, idle wait) come from a different surface:
claude-code's hook system.

**The on-device probe (2026-05-15, `hub/cmd/probe-claude-hooks/`)
established that the hook surface gives us the signal structurally**:

| Hook payload field | Value |
|---|---|
| `Notification.notification_type` | structured categorical: `idle_prompt` \| `permission_prompt` (no regex needed) |
| `PreToolUse.tool_name` | structured discriminator: `ExitPlanMode` for plan-approval, `Bash`/`Write`/`Edit`/… for tool-permission |
| `PreToolUse(ExitPlanMode).tool_input.plan` | full plan body (~600 chars markdown, observed); no JSONL cross-reference needed |
| `PreCompact.trigger` | `manual` \| `auto` |
| every hook payload | includes `permission_mode` — tracks Shift+Tab mode cycling without JSONL parsing |

So the disk-rule prediction layer and the capture-pane probe are
both **superseded**. The hook surface delivers structured payloads
through the existing host-runner MCP gateway (same UDS transport
`mcp__termipod__permission_prompt` uses today), with parked-MCP-call
semantics for awaiting mobile decisions. This is empirically simpler
and structurally cleaner than what the early-thread design proposed.

The **"always allow" path** still works exactly as before: the user
taps row 2 on mobile → adapter returns `permissionDecision:"allow"`
from the PreToolUse hook → claude-code writes the new allow pattern
to `settings.local.json` itself. No custom writer needed.

This section is retained as the audit trail of the design evolution
within 2026-05-15; the load-bearing decisions are in
[plans/local-log-tail-claude-code-adapter.md §5](../plans/local-log-tail-claude-code-adapter.md)
and ADR-027 D-amend-1.

---

## 4. Streaming over collapse — a redundancy-isn't-redundancy clarification

The initial framing of "redundant info across cards" suggested a
collapse-the-duplicates fix. The principal corrected:

> *"User wants a stream-like experience of the output of agent, so
> no need to collapse."*

This reframed the work. M1/M2's card duplication (text + thought +
tool_call_update + tool_result all rendering tool_name independently)
is not actually a UX bug — it's the streaming feel. The principal
wants the same per-block cadence in M4. JSONL gives us that for free:
each event in the log is one card. Done.

The only adapter-side aggregation needed:

- `thinking` block → render once as `"Thinking…"` marker, never
  with content (claude-code stores only a signature, not plaintext)
- `tool_use` and the corresponding `tool_result` correlate by
  `tool_use_id` for the renderer to fold a result under its parent
  call card — this is the same correlation M1/M2 already do

No partial-collapse logic. No deduplication of card kinds. Just emit.

---

## 5. Input — narrowed to non-approval surfaces

The principal flagged a likely assumption mid-thread:

> *"I'm not sure whether typing 1, 2, 3 is equivalent to Enter
> (default Yes row), Down Enter, Down Down Enter."*

Empirical answer from the claude-code binary inspection:

- **Library used:** Ink (React for CLI), via `useInput` hook
- **Digit-key bindings on the prompt:** none (`grep` returned zero
  matches for `key==="0"`..`key==="9"`)
- **Bindings present:** `upArrow`, `downArrow`, `return`, `escape`,
  letter shortcuts (`Y`, `N`, `j`, `k`) in some prompts but not
  uniformly on the permission prompt

Sending `"1"` does not select row 1 — arrow navigation is the
TUI's wire format for selection.

**However**, the hook-driven design (§3) means the adapter doesn't
need to send arrow keys for approve/deny **at all**. The hook
surface delivers the structured payload; mobile resolves; the
adapter returns `permissionDecision` to the hook, which routes back
into claude-code's permission engine. claude-code's TUI gets the
result through its own internal channel — no keystrokes synthesized.

The `tmux send-keys` path is **output-only direction** and covers
only the surfaces hooks can't reach:

| Mobile surface | tmux send-keys |
|---|---|
| Compose box → text | `send-keys -l <text>` + `Enter` (or paste-buffer for long/multiline) |
| Cancel | `send-keys C-c` (soft) → `kill -INT <pid>` (hard fallback) |
| Escape | `send-keys Escape` |
| Slash command | `send-keys "/clear" Enter` (or similar) |
| Mode cycle (Shift+Tab) | `send-keys S-Tab` (optional; usually user does this in TUI) |

**Removed from the action table compared to the early-thread draft:**
`Down Enter`, `Down Down Enter`, `Down Down Down Enter`, all the
highlight-position arithmetic, and the Y/N letter-shortcut probing.
Those collapse to the parked-hook decision path (§3).

The early-thread observation that "send-keys is dumb; the
intelligence is the state machine" still holds — except the state
machine is now the hook surface, not capture-pane regex.

---

## 6. The raw_pty_backend safety boundary

When the design called for "delete the old M4," the principal
corrected:

> *"raw_pty_backend.dart + xterm-VT should be treated very
> carefully — do not mix the basic termipod pty/tmux backend
> function for SSH hosts."*

The clarification:

- `lib/services/terminal/raw_pty_backend.dart` and the xterm-VT
  integration power the **plain-SSH terminal viewer** — termipod's
  original use case, separate from agent sessions.
- Only the *agent-mode M4 binding* swaps. The underlying terminal
  backend stays in service for all non-agent SSH viewing.
- This is enforced by binding the new driver to claude-code
  agent-family entries in `agent_families.yaml`, not by replacing
  the terminal backend wholesale.

The plan reflects this: no file deletion under `lib/services/terminal/`,
no change to the SSH terminal viewer, the M4 swap is per-engine.

---

## 7. Trade-offs we explicitly accept

- **Schema is observed, not contractual.** No vendor publishes the
  JSONL or hook payload spec. We pin against current versions, add
  a schema-drift fallback (unknown types render as muted system
  cards, not silent drops), and version-probe per engine.
- **File size grows unbounded.** The principal's current claude-code
  session JSONL is 773 MB. The adapter tails from a remembered byte
  offset, never re-reads from byte 0.
- **No flock, no rotation.** Multiple concurrent claude-code
  sessions on the same cwd write to different files (UUID per
  session). Adapter picks the newest mtime under the project dir.
- **Hook payloads may add fields across claude-code releases.**
  Adapter reads only the keys it knows about; extra fields are
  ignored. New `Notification.notification_type` values degrade to
  `system{subtype:"unknown_notification"}` rather than misroute.
- **Gemini's full coverage depends on its JSONL adapter landing.**
  Per the principal's path correction, gemini does have a JSONL
  stream — we just don't ship its adapter in MVP.

---

## 8. Open questions — resolved by the 2026-05-15 probe

Most questions from the pre-probe draft were answered by the
`hub/cmd/probe-claude-hooks/` corpus on 2026-05-15. Summary:

| Question | Resolution |
|---|---|
| Plan-mode prompts even with `--dangerously-skip-permissions`? | ✅ **Yes** — confirmed: ExitPlanMode tool_use → Notification{notification_type:"permission_prompt"} fires even in bypass mode |
| Does `PreToolUse(ExitPlanMode).tool_input` carry the plan body? | ✅ **Yes** — full markdown plan in `tool_input.plan` (~600 chars observed) + `planFilePath` field |
| Notification message vocabulary | ✅ **Better than expected** — `notification_type` is a structured categorical field, not a free-form message. Observed values: `idle_prompt`, `permission_prompt` |
| SubagentStop payload completeness | ✅ **Yes** — `agent_id`, `agent_type`, `last_assistant_message`, `agent_transcript_path`, `permission_mode`. Fires twice per Task call: once for the subagent (agent_type set), once at parent turn end (agent_type empty — adapter must filter) |
| PreCompact payload | ✅ `{trigger:"manual"\|"auto", custom_instructions}` confirmed |
| Stop payload | ✅ `last_assistant_message` is the canonical final-message-of-turn signal |
| Every hook carries `permission_mode` | ✅ Confirmed — no JSONL parsing needed for mode tracking |

### Still open (Probe v2 territory)

1. **`mcp_tool`-type hook long-park behavior** — need a hub-side MCP tool that takes >30s to return; confirm claude-code doesn't time out the hook before mobile resolves.
2. **`PreToolUse permissionDecision:"allow"` skip of plan-mode-exit prompt** — does returning `"allow"` from the hook bypass plan-mode's separate gate, or does plan-mode still prompt? Affects whether the mobile decision can fully replace the TUI prompt.
3. **`SessionStart.source` vocabulary** — only `startup` observed; probe needs `claude --resume` and `/clear` runs to capture other values.
4. **Auto-compaction trigger** — only manual `/compact` was tested; force context-fill to observe `PreCompact{trigger:"auto"}`.
5. **Write-tool overwrite in default mode** — confirm the Write-permission Notification's discriminator (was `permission_prompt` in the observed sample but check the full PreToolUse context).

These don't block MVP design — they're tuning details for the
implementation pass.

---

## 9. What's NOT in this design

For each, the deliberate reason:

- **Predictive permission matching for non-MVP pattern forms** (regex
  rules, cross-pattern coverage, glob negation). Capture-pane is
  the fallback; complexity here buys only a few hundred ms of
  latency savings.
- **inotify watcher on `settings.local.json`.** MVP re-reads on
  each tool_use — file is small, read is cheap, no race window
  worth optimizing.
- **xterm-VT fallback when JSONL schema drifts.** The current
  fallback is "render unknown events as muted system cards" — a
  graceful degradation that keeps the rest of the stream readable.
  Falling back to the alt-screen replay would undo the entire
  win of this work.
- **Scroll-up pagination of replay turns.** Phase 2 per the
  principal's "N=5 for MVP, scroll for more is Phase 2" call.
- **A custom "always allow" writer or pattern editor.** Claude-code
  writes its own patterns; mobile only sends keys.

---

## 10. Cross-references

- [plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md) — frozen contract for the adapter (this discussion's *what*).
- [decisions/027-local-log-tail-driver.md](../decisions/027-local-log-tail-driver.md) — the ADR (Accepted 2026-05-15, amended same day with the hook surface).
- [decisions/010-frame-profiles-as-data.md](../decisions/010-frame-profiles-as-data.md) — the per-engine adapter pattern (YAML profiles); same shape applies to LocalLogTailDriver's engine sub-adapters.
- [decisions/014-claude-code-resume-cursor.md](../decisions/014-claude-code-resume-cursor.md) — claude-code session model that the JSONL path resolver relies on.
- [decisions/021-acp-capability-surface.md](../decisions/021-acp-capability-surface.md) — per-driver capability surface; this driver shares the shape.
- `hub/cmd/probe-claude-jsonl/main.go` (committed `48c6a93`) — JSONL schema validation.
- `hub/cmd/probe-claude-hooks/` (committed `a45d24f`) — hook payload empirical corpus + on-device test plan; the 2026-05-15 evidence that drove the capture-pane→hook swap.
