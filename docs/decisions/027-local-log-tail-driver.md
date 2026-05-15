# 027. LocalLogTailDriver replaces agent-mode M4

> **Type:** decision
> **Status:** Accepted (2026-05-15)
> **Audience:** contributors
> **Last verified vs code:** claude-code 2.1.129, 200k-line live JSONL sample on 2026-05-15

**TL;DR.** Replace the agent-mode M4 driving mode — which currently
renders an interactive agent's TUI by piping the PTY screen through
a headless xterm VT state machine — with a `LocalLogTailDriver` that
tails the CLI's on-disk session JSONL and routes mobile input via
`tmux send-keys`. For MVP, only claude-code's adapter ships; gemini /
codex / kimi keep their current M4 binding until their adapters
land. The plain-SSH terminal viewer (`raw_pty_backend.dart` +
`lib/services/terminal/xterm` integration) is **untouched** —
agent-mode M4 and plain-SSH viewing are independent code paths and
the swap is per-engine via `agent_families.yaml`. The new driver
emits the same `AgentEvent` shapes M1 (ACP) and M2 (stream-json)
already produce, so the entire mobile surface (cards, approval
flow, compose box, action bar, snippet bar, cancel button) is
reused unchanged. Permission state is co-determined from disk rules
(`<cwd>/.claude/settings.local.json`) and `tmux capture-pane`
corroboration; the digit-key shortcut model is rejected in favor of
arrow-key navigation based on empirical inspection of claude-code's
Ink-based TUI.

## Context

Today's agent-mode M4 path:

- Reads the agent's tmux pane bytes over SSH.
- Runs them through a headless `xterm` state machine in
  `lib/services/terminal/raw_pty_backend.dart`.
- Renders the resulting text as the [agent_event](../reference/glossary.md#agent_event)
  stream's M4 fallback.

This works as a last-resort transcript surface but has three
structural limits the mobile UX can't paper over:

1. **Alt-screen TUIs destroy event boundaries.** Coding-agent CLIs
   (claude-code, codex, gemini-cli, kimi-code) own the alt-screen
   and repaint aggressively. The pane bytes are pixel output, not
   event records; recovering "user said X / agent replied Y / tool
   ran Z" from them is brittle reconstruction.
2. **Lossy compared to what the CLI emits.** The CLI internally
   has a structured event stream (vendor SSE → typed in-process
   events → screen render). Tapping the screen sits at the lossiest
   layer.
3. **Inconsistent with M1/M2 surfaces.** M1 and M2 emit
   `AgentEvent` rows that the mobile renderer turns into typed
   cards (text / thought / tool_call / tool_result / approval). M4
   surfaces a single text dump — same screen, different cognitive
   model.

The principal raised the question 2026-05-14: can we tap a deeper,
more structured layer than the terminal screen — network, stdio,
something else?

The comparative analysis is captured in
[discussions/local-log-tail-m4-replacement.md](../discussions/local-log-tail-m4-replacement.md);
the empirical validation against a 773 MB live JSONL is at
`hub/cmd/probe-claude-jsonl/` (committed `48c6a93`). The short
version:

| Layer | Verdict |
|---|---|
| PTY screen (current M4) | Lossy; the problem we're replacing. |
| TLS interception (eBPF eCapture / mitmproxy) | Breaks on codex's rustls (no `SSL_read` symbol). Needs root or per-process env+CA. No unified stock mechanism. |
| Process stdio in non-interactive mode | This is M1 / M2. Removes the TUI; requires controlling launch. |
| **On-disk session JSONL** | All four engines have one. Live-buffered. Stock. Richer than the SSE stream (tool_use ↔ tool_result already correlated). **Winner.** |

claude-code's claude-code 2.1.129 binary inspection further showed:

- Tool_use blocks carry no approval-state flag.
- Permission rules live on disk at `~/.claude/settings.json` +
  `<cwd>/.claude/settings.local.json` and are written by
  claude-code itself when the user picks "always allow."
- The Ink-based TUI binds no digit-key handlers on permission
  prompts; navigation is arrow keys + Enter, not numeric shortcuts.

These three findings shape the design.

## Decision

### D1. Bind agent-mode M4 to `LocalLogTailDriver`, claude-code first

Add a new hub-side driver `LocalLogTailDriver` with per-engine
adapters (`ClaudeCodeAdapter` for MVP). Rebind the
[driving mode](../reference/glossary.md#driving-mode) `M4` entry in
`agent_families.yaml` from raw-PTY to local-log-tail **for the
claude-code engine family only**. Other engines retain whatever M4
binding they have today until their adapters ship.

### D2. Reuse M1/M2 surfaces; emit identical `AgentEvent` shapes

The driver emits `text` / `thought` / `tool_call` / `tool_result` /
`attachment` / `system` / `approval_request` events with payload
shapes identical to those produced by `ACPDriver` and the
stream-json drivers. Zero changes to `lib/widgets/agent_feed.dart`,
the approval card, the compose box, the action bar, the snippet
bar, or the cancel button. The driver swap is transparent to the
mobile UX.

### D3. Preserve the streaming card model; do not collapse

Per principal clarification 2026-05-14, the "streaming feel"
(per-block updates, one card per JSONL event) is the desired UX,
not a bug to fix. The adapter does **not** merge cards by
`message_id` or fold partial chunks. Two specific behaviors:

- `thinking` blocks emit a `thought` event with `payload.text =
  "Thinking…"` and `marker_only:true`. The plaintext is empty
  (signed for API verification, not for human display); only the
  marker shows.
- `tool_use_id` correlation between `tool_call` and `tool_result`
  remains the renderer's job (folding the result under the parent
  card), exactly as M1/M2 already do.

### D4. Permission state is co-determined; rules on disk, prompt on screen

The driver reads `.permissions.allow` from
`~/.claude/settings.json` and `<cwd>/.claude/settings.local.json`
on attach and on each `tool_use`. Supported pattern forms for MVP:
`<ToolName>` bare, `Bash(<cmd-prefix> *)`, `Bash(<exact-cmd>)`,
`WebFetch(domain:<host>)`, `WebSearch`. A matched rule skips the
approval flow entirely.

For unmatched rules, the state machine waits a configurable grace
window (default 600 ms) for the `tool_result` to land — this
absorbs auto-allows the rule-matcher doesn't recognize. If grace
expires, `tmux capture-pane -p -e` polls at 200 ms cadence; on
detecting the approval-prompt screen pattern, the driver emits an
`approval_request` `AgentEvent` with the parsed
`(options[], highlighted_index)`.

We deliberately do **not** re-implement claude-code's full
permission engine. That couples us to a vendor-internal contract.
Prediction-via-rules is best-effort; capture-pane is the source of
truth.

### D5. Input uses arrow navigation; digit shortcuts are rejected

Inspection of the claude-code binary's Ink-based TUI showed zero
digit-key bindings on permission prompts. The driver sends:

- `Enter` for row 1 (Approve)
- `Down Enter` for row 2 (Always allow / session allow)
- `Down Down Enter` for row 3 (Deny + reason)

Highlight-position arithmetic from the capture-pane parse keeps
this robust against drift (claude-code may pre-highlight a non-row-1
default based on prior choices).

The deny-reason path runs as two turns: arrow-keys select row 3 →
claude-code re-renders with a "tell me what to do differently"
text prompt → the user's reason flows through the existing compose
box as a normal next `SendInput`. No pre-injection.

### D6. raw_pty_backend.dart stays untouched

The new driver lives at `hub/internal/drivers/local_log_tail/`. It
does not import, replace, or modify `lib/services/terminal/raw_pty_backend.dart`
or the xterm-VT integration. The plain-SSH terminal viewer
(termipod's original use case) continues to use raw_pty_backend
exactly as today.

### D7. Schema-drift policy: graceful degradation, not fallback to xterm-VT

When the JSONL emits an unknown top-level `type` or an unexpected
content-block shape, the driver emits a `system` event with
`subtype=unknown_type` and the type name. Mobile renders a muted
info card. The driver does **not** revert to the old xterm-VT path
— that would undo the structural win.

### D8. MVP catchup window: 5 turns; scroll-up pagination is Phase 2

On attach, replay the last 5 turns from JSONL (turn = events
between consecutive user-typed messages). Earlier turns are not
loaded automatically; a "load earlier" pagination wedge is
explicitly deferred.

### D9. Phase 2 / 3 engines

The same driver shape extends to gemini-cli, codex, and kimi-code
via per-engine adapters at the same paths the principal supplied
(`~/.gemini/tmp/<wd>/chats/...jsonl`,
`~/.codex/sessions/<date>/...jsonl`,
`~/.kimi/sessions/<hash>/<uuid>/context.jsonl`). Each adapter ships
as its own wedge with its own schema mapping and key tables. The
shared `LocalLogTailDriver` skeleton handles the path resolver,
tail-from-offset, AgentEvent emission, and tmux send-keys routing;
adapters only provide the schema map, the permission-rule reader
(if applicable), and the capture-pane regex library.

## Consequences

### Positive

- **Mobile transcript jumps from text-dump to typed cards** for any
  agent session running under the new driver, with no mobile-side
  changes.
- **Approval cards work in agent-mode M4** for the first time —
  previously only available via M1 / M2.
- **"Always allow" enrolment via mobile** flows through
  claude-code's own settings.local.json writer; no custom code.
- **Same architectural pattern across all four engines** when their
  adapters ship; no per-engine wire format divergence.
- **No root, no kernel pin, no CA install** on the SSH'd-into host.

### Negative

- **Schema is observed, not contractual.** Vendors don't publish
  the JSONL spec; upstream changes may require adapter updates.
  Mitigated by the schema-drift fallback (D7) and per-engine
  version probes.
- **File sizes grow unbounded.** The principal's current claude-code
  session JSONL is 773 MB. The adapter must tail from a remembered
  byte offset, never re-read from byte 0.
- **Capture-pane probe adds ~1 s p99 latency to the approval card**
  for unmatched-rule tool_uses. Auto-allow paths skip it (zero
  added latency).
- **The highlight-index regex must be maintained.** A claude-code
  TUI redesign could break the parse; the fallback when the regex
  doesn't recognize the prompt is "render the raw screen slice as
  a muted system card" so the user can manually drive the action
  bar's arrow keys.

### Neutral

- The xterm-VT machinery stays in service for plain-SSH terminal
  viewing (D6). No removed code.
- ADR-010's "frame profiles as data" pattern extends naturally —
  each per-engine adapter is the JSONL analog of an ACP frame
  profile. No new abstraction.

## References

- [discussions/local-log-tail-m4-replacement.md](../discussions/local-log-tail-m4-replacement.md) — comparative interception-layer survey, empirical findings, open questions for on-device verification.
- [plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md) — frozen contract for the claude-code adapter (schema map, state machine, key tables, capture-pane regex, MVP knobs, implementation checklist).
- [decisions/010-frame-profiles-as-data.md](010-frame-profiles-as-data.md) — the per-engine YAML-profile pattern this driver mirrors.
- [decisions/014-claude-code-resume-cursor.md](014-claude-code-resume-cursor.md) — claude-code session model that the JSONL path resolver depends on.
- [decisions/021-acp-capability-surface.md](021-acp-capability-surface.md) — capability surface shared across drivers; this one adds a row.
- [decisions/026-kimi-code-engine.md](026-kimi-code-engine.md) — kimi-code engine definition; this ADR's Phase 3 adapter targets it.
- `hub/cmd/probe-claude-jsonl/main.go` (committed `48c6a93`) — empirical validation against a live 200k-line / 773 MB JSONL.
