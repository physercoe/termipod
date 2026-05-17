# Structured server-side VT screen (dinotty reference)

> **Type:** discussion
> **Status:** Open — not actionable now. Recorded so the design pattern is on file before we hit a trigger that would make it actionable. Resolves either into an ADR (if/when we adopt) or into a "dropped" footnote (if/when we conclude the trigger never fires).
> **Audience:** contributors
> **Last verified vs code:** v1.0.616-alpha; against [`xichan96/dinotty`](https://github.com/xichan96/dinotty) at clone-time `/tmp/dinotty`, HEAD as of 2026-05-17.

**TL;DR.** dinotty is a Rust terminal server (Axum + tokio + the
`vte` crate) that maintains a **structured server-side virtual
terminal** — a per-cell grid of `{char, combining[3], attrs}`
covering color, bold/dim/italic/underline/inverse/strikethrough,
cursor, scrollback, and alt-screen. The grid is faithful enough to
fully reconstruct what a user sees on a terminal screen, and lets
downstream consumers query the visible state programmatically
(find prompt, wait for text, region diff) rather than scanning a
raw byte tape. The pattern is interesting for termipod's M1
(tmux-pane) capture path; the implementation is Rust, our
host-runner is Go, and our current pipeline isn't suffering enough
for the swap to pay off today. Decision: **do nothing now**. This
doc captures the architecture map + revisit triggers so the work
is half-done when we need it.

---

## 1. What dinotty actually is

A mobile-first web terminal server for coding-agent CLIs (Claude
Code, opencode, Codex, OpenClaw). The README compares it to ttyd /
gotty / Wetty and the headline differentiator is *"the server runs
the VT emulator, not just a WebSocket-to-PTY pipe"*. That single
choice unlocks:

- Session survives client disconnect (PTY owned by server; mobile
  network drop doesn't kill the agent)
- Reconnect replays an ANSI-encoded snapshot + chunked scrollback,
  not a transcript of escape sequences
- Bandwidth: text deltas, not pixel frames — claimed ~1–10 KB/s vs.
  VNC/RDP's ~1–10 MB/s
- Mobile-friendly: customizable on-screen shortcut keyboard for
  Ctrl / Esc / function keys (UI layer)

Stack:

| Layer | Code |
|---|---|
| `src/vt_screen.rs` (687 LOC) | `vte::Parser` → structured grid |
| `src/session.rs` (189 LOC) | `Arc<Session>` in `DashMap`, 5-min detach GC |
| `src/pty.rs` (206 LOC) | `portable_pty` master/slave |
| `src/ws.rs` (317 LOC) | WS framing, reconnect snapshot dispatch |
| `src/history.rs` (343 LOC) | Shell-history indexing (auxiliary) |
| `src/proxy.rs` (831 LOC) | Reverse proxy for embedded web preview (auxiliary) |
| `src/workspace.rs` (1438 LOC) | File browser + Monaco editor backend (auxiliary) |

The **load-bearing innovation is `vt_screen.rs` + `session.rs` +
`ws.rs`** — roughly 1200 LOC. The rest is product surface area
(file editor, web preview, system monitor, notification panel) that
makes dinotty a complete mobile IDE; for us it's irrelevant.

---

## 2. What `vt_screen.rs` captures (and doesn't)

In-memory representation per cell:

```rust
struct Cell {
    ch: char,
    combining: [char; 3],
    combining_len: u8,
    attrs: CellAttrs {
        fg: Option<Color>,    // Indexed(u8) | Rgb(u8,u8,u8)
        bg: Option<Color>,
        bold, dim, italic, underline, inverse, strikethrough: bool,
    },
}
```

Per screen: `Vec<Vec<Cell>>` + cursor + scroll region + alt-screen
buffer + 10 000-row scrollback `VecDeque`. The screen is a faithful
output-side mirror of what the terminal user sees.

**Captured, lossless:**

- Per-cell base character + up to 3 combining marks
- Foreground / background — 256-palette or 24-bit truecolor
- Bold, dim, italic, underline, inverse, strikethrough
- Cursor row / col / current attrs
- Primary vs. alternate screen (vim/htop's full-screen overlay)
- Scroll region (DECSTBM — for status-bar carve-outs)
- Cursor save/restore
- Wide chars (East Asian width 2 spans two cells, second cell holds
  `'\0'` continuation marker)
- Scrollback colors + attrs (not just plaintext)

**Deliberately dropped (each is a no-op `Perform` stub):**

- Hyperlinks (OSC 8) — claude-code emits these; URL is lost
- Window title (OSC 0/2) — dinotty's `session.rs` sniffs OSC titles
  separately for cwd, but the grid drops them
- Cursor visibility, style, shape
- Blink / concealed / double-underline / overline SGR attrs
- Charset switching (G0/G1/SS2/SS3 — legacy DEC line-drawing misrenders)
- Bracketed paste mode
- Mouse reporting modes
- Image protocols (sixel, kitty graphics, iTerm2 inline images) —
  cells where the image would render are blank

For our engines (claude-code, codex, gemini-cli, kimi-code) and the
typical TUI surfaces (shell, htop, vim, less), none of the drops are
load-bearing. **If we ever lift this code, the choice to extend
`CellAttrs` with hyperlinks is the most likely thing we'd regret not
doing at the start** — adding a new attr to a calcified wire
protocol is painful; adding it on day 1 is free.

---

## 3. What structured grid unlocks

Two distinct affordances, both worth naming explicitly:

**Output-side: state recovery + programmatic query.**

- Reconnect / app-foreground: server hands the client the canonical
  grid (or only the diff since last sync) instead of a tape of "draw,
  erase, redraw" bytes the client has to replay.
- Programmatic queries on captured output: scan for prompt row, wait
  for a string to appear, diff a sub-region, detect red-foreground
  errors in the last N rows. Today our PaneDriver does plaintext diff
  against `tmux capture-pane`; structured grid makes "did `[error]`
  appear in red?" trivially answerable.

**Input-side: nothing changes.**

Input (keystrokes to the PTY) is bytes regardless of how output is
modeled. Ctrl+C is still `\x03`, arrow-up is still `\e[A`. dinotty
handles this on the frontend with a customizable shortcut keyboard
that maps named buttons to raw escape sequences. The structured grid
buys us no input affordance — that's a UI-layer concern.

---

## 4. Per-mode applicability for termipod

| Our mode | Today's mechanism | Where vt_screen would slot | Verdict |
|---|---|---|---|
| **M1 — tmux pane (mobile renders)** | `RawPtyBackend` ↔ xterm.dart client-side VT | Client already has the structured grid (xterm.dart owns it). Stacking server-side VT in front = double emulation. | **Not a fit** |
| **M1 — tmux pane (PaneDriver polls)** | Hub polls `tmux capture-pane -e`, plaintext diff, emits `text` events (`driver_pane.go:37-150`) | In-process grid: feed `capture-pane -e` raw bytes into a Go `vt_screen` port, emit structured cell deltas as agent events | **Best fit if/when we move** |
| **M2 — structured stdio (ACP)** | Agent CLI emits JSON-RPC over stdio; no terminal pane | No VT to emulate | **N/A** |
| **M4 — LocalLogTailDriver** | Host-runner tails `~/.claude/projects/.../session.jsonl`; emits claude-code structured agent events | Claude-code-specific JSONL is already richer than any grid would be | **N/A** |
| **(Hypothetical) generic structured capture for an engine with no JSONL and no ACP** | Doesn't exist today | Spawn agent inside dinotty (sidecar) → consume grid over WS → translate to agent events | **Real fit when triggered** |

The two boxes worth caring about are **M1 PaneDriver** (existing,
incremental win) and **hypothetical generic engine** (doesn't exist
yet, would be a real fit).

---

## 5. Three architectural options

Recorded in order of feasibility, not preference.

### Option A — Embed `vt_screen` in the host-runner, keep tmux

`tmux` stays the launcher (process supervisor + multi-pane +
`ssh + tmux attach` for M1). `PaneDriver`'s capture loop changes from

```
capture-pane -e → plaintext diff → text agent event
```

to

```
capture-pane -e → in-process VT emulator → diff structured grid → structured agent event
```

Implementation: port `vt_screen.rs` to Go (~500-700 LOC) or run it
as a Rust sidecar invoked over stdio.

- **Pro:** lowest disruption. Agent never knows the difference. M1
  user can still SSH-attach. Adds zero deployment dependencies if
  we port to Go.
- **Con:** doesn't move PTY ownership; if all we want is reconnect
  resilience for mobile, this doesn't deliver it (tmux already
  delivers that for M1).

### Option B — Dinotty as a sidecar for a new `StructuredCaptureDriver`

A fifth driver alongside `PaneDriver` / `LocalLogTailDriver`. Host-
runner spawns the agent **inside dinotty's PTY** (not tmux), then
taps dinotty's WebSocket as an internal client to consume the JSON
grid + cell deltas. This is the fallback for engines that have
neither structured stdio (M2) nor a JSONL log to tail (M4).

- **Pro:** answers a real gap — engine-agnostic structured capture
  without per-engine log parsing. Wins the day a new engine ships
  that lacks both M2 and M4 affordances.
- **Con:** adds dinotty as a deployment artifact per host. The agent
  lives in dinotty, not tmux, so `ssh + tmux attach` doesn't show
  it. Two PTY supervisors per host. Dinotty crash kills every agent
  it owns (no equivalent to tmux's session-survives-supervisor).

### Option C — Replace tmux entirely with dinotty

Don't do this. tmux is doing four jobs we'd have to rebuild from
scratch:

1. Multi-pane / multi-window per project (dinotty's DashMap of
   single PTYs is not the same semantic)
2. Detach-and-reattach across SSH sessions (M1 depends on this)
3. Battle-tested process supervision (dinotty's 5-min GC is thinner)
4. The actual `tmux` binary, which 100% of our users already have

---

## 6. Concrete blockers if we ever pursued Option B

Not deal-breakers, but real:

1. **No programmatic-spawn API in dinotty.** Its WebSocket is
   designed for interactive browser clients. We'd patch
   (or fork) to accept *"spawn this command, hold the PTY, expose
   grid over WS"* without the interactive shell wrapper. <100 LOC.
2. **No JSON grid output mode.** `snapshot()` (vt_screen.rs:229-277)
   re-emits ANSI for replay convenience. We'd add a `snapshot_json()`
   returning `{rows: [[{ch, fg, bg, bold, ...}]], cursor,
   alt_screen}`. ~50-80 LOC + `serde::Serialize` derives.
3. **Go ↔ Rust IPC.** Host-runner is Go; dinotty speaks WS+JSON.
   `gorilla/websocket` covers the wire side. Decide UDS vs. TCP
   port — UDS avoids per-agent port allocation but limits to
   same-host.
4. **Lifecycle coupling.** Dinotty crash = every agent it owns
   dies. With tmux, sessions survive even host-runner restart.
   We'd need a supervisor for dinotty itself (systemd unit + restart
   policy + a "did dinotty come back with my agents?" check). This is
   the architectural cost most worth budgeting upfront.

### Don't try

- **Point dinotty at an existing tmux pane.** Dinotty owns a PTY it
  spawned; it cannot attach to a process tmux already owns. The two
  models are exclusive at the per-process level.
- **Run dinotty as a parallel observer alongside tmux.** Same
  problem — only one process gets to be the PTY parent.

---

## 7. Revisit triggers

Open this doc again, and consider acting, when ANY of these fires:

1. **A new engine ships that has neither M2 (structured stdio) nor a
   M4-style JSONL log we can tail.** Currently only claude-code has
   M4; codex/gemini-cli/kimi-code use ACP (M2). If we add an engine
   that's pure-TUI, structured grid capture (Option B) becomes the
   right pattern.
2. **Mobile complaints that "the chat surface lost state after
   backgrounding"** become load-bearing. The current PaneDriver
   sends plaintext deltas; structured grid + server-side snapshot
   replay (Option A flavor) would close that hole.
3. **A request for programmatic capture queries** lands —
   "wait until the prompt comes back", "detect when the agent
   prints a URL", "diff this region across turns". Today these
   require fragile regex on raw byte tape; structured grid makes
   them trivial.
4. **Client-side ANSI re-parsing CPU shows up as a mobile battery
   regression** in profiling. The double-parse path
   (`AnsiParser` in
   `lib/services/terminal/ansi_parser.dart:83-110` after
   xterm.dart) is wasteful but hasn't shown up as a top complaint.

If none of these fire, the doc stays Open and we keep doing what
we do.

---

## 8. License + portability notes

- **dinotty repo license:** no LICENSE file in the clone tree at
  time of analysis (2026-05-17). Default Rust-ecosystem dual
  MIT/Apache-2 cannot be assumed without checking the GitHub repo
  metadata. **Verify before copying any code.**
- **Termipod is Apache-2.** Compatible with MIT/Apache-2 upstreams
  if license confirms.
- **Rust → Go porting friction (Option A):**
  - `vte` crate is the load-bearing dep. Go's terminal-parsing
    landscape has fragments (`gdamore/tcell/v2` buffer abstraction,
    `mattn/go-runewidth` for width) but no production-grade
    equivalent to Rust's `vte`. Either FFI to Rust's `vte` or
    hand-roll ~80% of CSI handlers (cursor / erase / SGR /
    alt-screen). The CSI handler set in `vt_screen.rs:418-540` is
    a useful reference — maybe ~40 cases worth implementing.
  - East Asian width detection: `mattn/go-runewidth` covers this.
  - Mutex discipline: Rust's `Arc<Mutex<…>>` translates to Go's
    `sync.RWMutex` or channels. No surprises.
- **Combining-character edge case** (vt_screen.rs:23-46): 3 combining
  marks per cell. Go is UTF-8 native, no rune-handling concerns;
  Dart is UTF-16, would need care. Test with accented + emoji
  heavily if we port to Dart-side.

---

## 9. Status

**Decision recorded 2026-05-17: do nothing now.** Current
PaneDriver + LocalLogTailDriver + M2 ACP cover today's engines.
The win from structured grid is real but doesn't justify the cost
(Go VTE implementation OR Rust sidecar) until a revisit trigger in
§7 fires.

When a trigger fires, this doc + the source pointers in §2 are the
starting point. The recommended path on that day is **vendor the
500-LOC core** (vt_screen + session + ws minus the auxiliary
file-browser / web-preview / monitor / notification surfaces),
either as a Go port (Option A) or a Rust sidecar (Option B),
**not** adopt dinotty as a full deployment dependency.

This discussion flips to **Resolved** once either:
- An ADR records adoption (and links here as the priors), or
- A year passes with none of §7 triggering, at which point we
  flip to **Dropped** with a "we never needed it" footnote.
