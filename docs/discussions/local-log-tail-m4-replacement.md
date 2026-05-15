# M4 replacement — local log tail vs other interception layers

> **Type:** discussion
> **Status:** Active (2026-05-15) — design locked through 2026-05-15 thread; spec frozen in [plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md); ADR pending. Flip to Resolved once the ADR lands.
> **Audience:** principal · contributors · reviewers
> **Last verified vs code:** claude-code 2.1.129; 200k-line live JSONL sample on 2026-05-15

**TL;DR.** The current M4 [driving mode](../reference/glossary.md#driving-mode)
renders an interactive agent's TUI by piping the PTY screen through
a headless xterm VT state machine — works, but on a phone the
result is a messy text dump that lacks the semantic cards M1/M2
produce. The principal asked: can we tap a deeper, more structured
layer than the terminal screen? This doc records the comparative
survey (PTY screen-scrape vs network/TLS interception vs on-disk
session logs), why on-disk JSONL tailing won, the empirical findings
that shaped the input-action and approval-state design, and the
trade-offs we explicitly accept. The plan is in
[plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md);
this doc is the *why* behind it.

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

## 3. Why we don't replicate claude-code's permission engine

The principal raised: *"claude-code has a programmed permission
system to decide which tool call needs user decision — can we
extract that from JSONL?"*

What we found in the binary (`/home/ubuntu/.local/share/claude/versions/2.1.129`):

1. **The tool_use JSONL block carries no approval-state flag.**
   Fields are `{type, id, name, input, caller:{type:"direct"}}` —
   identical whether approval is needed, was granted, or was
   auto-allowed.
2. **The decision is computed from disk rules at runtime.** The
   rules live in `~/.claude/settings.json` (global) and
   `<cwd>/.claude/settings.local.json` (project-local). Patterns
   like `Bash(git push *)` auto-allow exact-prefix matches.
3. **`settings.local.json` is *live-written* by claude-code.** When
   the user picks row 2 ("Yes, and don't ask again"), claude-code
   appends a new pattern to the allow list. We confirmed this by
   observing patterns matching commands the principal had just
   approved in this very session.

This shifts the design:

- **The adapter reads the rules from disk on each tool_use and
  predicts approval-needed.** If a rule matches → no card, skip the
  whole approval flow.
- **It does *not* re-implement claude-code's decision logic.** That
  would couple us to a vendor-internal contract and break on every
  upstream change. Instead, prediction-via-rules is best-effort;
  the empirical signal (capture-pane) is the source of truth.
- **"Always allow" via mobile** flows through naturally: the user
  taps row 2 → adapter sends `Down Enter` → claude-code writes the
  pattern itself. No custom writer needed.

The 600 ms grace window absorbs the gap between "tool_use lands"
and "we know whether the rule actually matched." If `tool_result`
arrives inside the grace, our prediction was right (or the rule
matcher was wrong-and-tolerant); if not, capture-pane confirms the
prompt is up and we emit the approval card.

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

## 5. Input — arrow keys, not digits

The principal flagged a likely assumption:

> *"I'm not sure whether typing 1, 2, 3 is equivalent to Enter
> (default Yes row), Down Enter, Down Down Enter."*

Empirical answer from the claude-code binary:

- **Library used:** Ink (React for CLI), via `useInput` hook
- **Digit-key bindings on the prompt:** none (`grep` returned zero
  matches for `key==="0"`..`key==="9"`)
- **Bindings present:** `upArrow`, `downArrow`, `return`, `escape`,
  letter shortcuts (`Y`, `N`, `j`, `k`) in some prompts but not
  uniformly on the permission prompt

So sending `"1"` does not select row 1. The principal's hunch was
right: the design must use arrow navigation. The adapter sends
`Enter` for row 1 (default highlighted), `Down Enter` for row 2,
`Down Down Enter` for row 3.

### 5.1 Highlight-position arithmetic

Claude-code remembers the user's last choice on similar prompts —
the *default-highlighted* row may not always be row 1. To stay
robust, the capture-pane probe returns `(options[], highlighted_index)`
and the adapter computes `Down × (target − highlighted) + Enter`.
Hard-coded `Down Enter` is just the common-case shortcut.

### 5.2 Why this matters more than the wire format

The action table (compose → text, approval → arrows + Enter, etc.)
is small. The intelligence is in *knowing what state the prompt is
in*. That state lives only on the TUI screen — JSONL has no
"awaiting decision" event — so capture-pane is unavoidable as the
state probe. It's not a wire-format question; it's a state-machine
question.

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
  JSONL spec. We pin against current versions, add a schema-drift
  fallback (unknown types render as muted system cards, not silent
  drops), and version-probe per engine.
- **File size grows unbounded.** The principal's current claude-code
  session JSONL is 773 MB. The adapter tails from a remembered byte
  offset, never re-reads from byte 0.
- **No flock, no rotation.** Multiple concurrent claude-code
  sessions on the same cwd write to different files (UUID per
  session). Adapter picks the newest mtime under the project dir.
- **Capture-pane introduces a 200 ms poll for each approval moment.**
  Acceptable; only triggers on unmatched-rule tool_uses; auto-allow
  paths skip it entirely (zero added latency).
- **Highlight-index arithmetic depends on capture-pane regex
  fidelity.** A claude-code TUI redesign could break it. The
  fallback when the regex doesn't recognize the prompt: render the
  raw screen slice as a muted system card so the user can manually
  use the action bar's arrow keys.
- **Gemini's full coverage depends on its JSONL adapter landing.**
  Per the principal's path correction, gemini does have a JSONL
  stream — we just don't ship its adapter in MVP.

---

## 8. Open questions (to lock during on-device verification)

1. **`grace_ms` = 600 default.** Is this comfortable on real hardware?
   If approval prompts visibly lag, drop to 300. If auto-allows
   regularly miss the window, raise to 900.
2. **Capture-pane glyph for highlight.** Spec says `❯ \d+\.`; this
   must be verified against the user's terminal font / Ink theme.
   Falls back gracefully (treat all rows as unhighlighted, default
   to row 1) if the glyph differs.
3. **Pane lookup by `claude` PID.** Multiple `claude` processes on
   the same host (rare but possible — split-pane workflow) means
   the PID→pane mapping needs a session-ID disambiguator. MVP
   picks the most-recently-active pane; revisit if collision
   surfaces.
4. **Y/N letter shortcuts on the permission prompt.** Binary has
   `key==="Y"` / `key==="N"` handlers in non-permission contexts.
   If they also work on the permission prompt, `Y` could replace
   `Enter` (one keystroke instead of two for row 1). Cosmetic; do
   not block MVP.
5. **MCP tool prompts.** Claude-code's MCP tools may render
   permission prompts with different option text. MVP's regex
   library covers numbered-select and y/n-inline; MCP-specific
   prompts may need a Phase 2 regex.
6. **Plan-mode prompts.** When the user activates `acceptEdits`
   permissionMode, edit-tool prompts disappear but other tools
   still gate. The state machine should be robust to permissionMode
   shifts mid-session (the JSONL emits a `permission-mode` event
   each time).

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
- [decisions/010-frame-profiles-as-data.md](../decisions/010-frame-profiles-as-data.md) — the per-engine adapter pattern (YAML profiles); same shape applies to LocalLogTailDriver's engine sub-adapters.
- [decisions/014-claude-code-resume-cursor.md](../decisions/014-claude-code-resume-cursor.md) — claude-code session model that the JSONL path resolver relies on.
- [decisions/021-acp-capability-surface.md](../decisions/021-acp-capability-surface.md) — per-driver capability surface; this driver shares the shape.
- `hub/cmd/probe-claude-jsonl/main.go` (committed `48c6a93`) — the empirical validation that grounded every decision in this doc.
