# Desktop terminal — first-class persistent surface & a professional-class feature path

> **Type:** discussion
> **Status:** Accepted → in build (2026-07-08). Director directive: the terminal
> should be a *separate function*, not a pop-up — switchable at any time like VS
> Code's integrated terminal — and "professional-class" (director cited **Warp**),
> using the fact that the desktop is a **native (Tauri/Rust) app, not a pure web
> app**. This reasons the **information-architecture change** (modal → persistent
> panel) and the **engine/feature strategy**, building on the two-layer split
> ratified in [ADR-052](../decisions/052-breakglass-ssh-and-key-vault.md).
> **Director decisions (§6):** dock **bottom**; first slice bundles **persistent
> panel + Blocks together**; **local shells (local PTY) in scope**; xterm.js is the
> endpoint (no native GPU surface). Now being implemented; graduates to **ADR-053**
> once the first slice lands.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** desktop-v0.3.10

**TL;DR.** Two independent asks, two answers.
1. **Make the terminal a first-class, persistent surface** — mount it once as a
   dockable panel (VS Code model), toggle it with a keybinding, keep SSH
   sessions alive as you switch away and back, and allow multiple terminal tabs.
   Today it is a modal overlay that *unmounts on close*, which tears down the SSH
   session — the opposite of what we want.
2. **Professional-class does not mean a new renderer.** Warp's crown-jewel
   features (**Blocks**, command palette, prompt navigation) are a **UI layer on
   top of the OSC 133 shell-integration protocol**, not something locked to
   Warp's GPU engine. Stay on **xterm.js** (what VS Code ships) in the webview,
   add its GPU + utility addons, and spend the "native power" in the **Rust core**
   (PTY, SSH, shell-integration injection, tmux, local shells) — where it
   actually pays off. A native GPU-surface rewrite is rejected for the same
   reasons ADR-052 already rejected `libghostty`.

---

## 1. Where we are

The desktop terminal (ADR-052) is a **breakglass SSH surface**: an xterm.js
screen in the webview, with russh providing the PTY/SSH transport in the Rust
core. It is opened from the top bar as a **modal overlay**
(`AppShell.tsx` → `{terminalOpen && <Terminal onClose=… />}`), sitting on a
backdrop over the whole app. Consequences of the modal model:

- **It is not persistent.** Closing it unmounts `<Terminal>`, which unmounts the
  live `<Screen>`, whose effect cleanup calls `sshClose()` — the SSH session
  dies. You cannot "switch away and come back." (This is the same
  effect-lifecycle seam behind the recent tab-switch session-drop bug, fixed in
  desktop-v0.3.10 — but the modal itself is still ephemeral.)
- **It is single-session.** One connect form → one screen. No notion of several
  open terminals you tab between.
- **It steals the whole surface.** A backdrop modal is the wrong shape for
  something you keep open beside your work, like a build log or a tailed file.

Only `@xterm/addon-fit` is installed today — no GPU renderer, search, serialize,
or shell-integration parsing yet.

## 2. Ask 1 — terminal as a first-class, persistent surface

Adopt the **VS Code integrated-terminal model**:

- **A dockable panel, mounted once for the app's lifetime.** Live in a bottom (or
  right) panel region of the shell, *shown/hidden* via CSS — never unmounted — so
  sessions and scrollback survive toggling. Toggle with **Ctrl+`** (and a
  status-bar/`⌘K` entry).
- **Multiple terminals as tabs**, each its own SSH session; a `+` opens another,
  and you switch between them instantly. (Later: split panes within a tab.)
- **Session ownership moves out of the view.** Sessions become app-scoped state
  (a small store keyed by session id), so the panel is a *view* over live
  sessions rather than the *owner* of one. Closing a tab closes that session;
  hiding the panel closes nothing.
- **Reconnect/restore.** With the `serialize` addon we can snapshot a screen and
  restore it after a transient SSH drop, matching the "session survives respawn"
  posture elsewhere in the product.

This is a self-contained, high-value slice that needs no engine change — it is
mostly moving the terminal from the overlay set into a persistent shell region
and lifting session state up.

## 3. Ask 2 — "professional-class": engine & feature strategy

### 3.1 Engine — stay on xterm.js, reject a native-surface rewrite

The instinct on hearing "professional-class, like Warp" is to reach for a native
GPU terminal. For a Tauri app this is the wrong trade:

- **The features that make Warp feel pro are a UI layer, not a renderer.** Blocks,
  command palette, workflows, agent panels — all live above the shell, driven by
  OSC 133 (§3.2). VS Code proves you get all of this on xterm.js.
- **A native GPU surface fights Tauri.** Engines that *are* embeddable from Rust
  today — `alacritty_terminal` (headless grid, you render), `Rio`/`sugarloaf`
  (WGPU renderer crate), `termwiz` — would render into a **separate native child
  surface composited over the webview**: manual z-order, DPI, resize, focus, per
  OS. Highest effort, worst cross-platform consistency, and it **throws away the
  React/TS UI where the pro features actually live**. This is the same reasoning
  ADR-052 used to reject `libghostty` (a renderer that paints to a native
  surface, not an SSH client).
- **`libghostty` still isn't ready.** Its zero-dep VT-parsing core (`libghostty-vt`)
  exists, but the full library with GPU renderer + windowing is a maturing C API
  (2026 alpha) — not a drop-in surface.

**Decision proposed:** keep the renderer in the webview (xterm.js), add the
GPU + utility **addons** — `webgl` (GPU raster; VS Code ships this at scale),
`fit` (have), `search`, `serialize` (restore), `unicode11`, `web-links`,
`ligatures` — and invest native/Rust effort in the **plumbing**: russh PTY/SSH,
the shell-integration script injection, tmux control, and a **local PTY** (via a
Rust `portable-pty`-class crate) so the terminal also opens a *local* shell on
the user's machine — the most direct way the desktop "empowers the user" beyond
the mobile SSH-only story.

Keep two things on the watchlist for a possible future native phase:
**`libghostty`'s C API** (the cleanest embeddable option if we ever want a native
GPU surface) and **Warp's now-MIT `warpui`/`warpui_core` crates** (a permissively
licensed Rust UI framework worth studying).

### 3.2 The signature feature — Blocks via OSC 133

Warp/iTerm2/VS Code/Ghostty all build "blocks" from the same open protocol:
a small **shell-integration script** injected into bash/zsh/fish emits **OSC 133**
semantic-prompt markers — `A` prompt-start, `B` input-start, `C` output-start,
`D;<exit>` command-end. From these the terminal reconstructs, per command:
**command text, output span, exit code, cwd, duration** — i.e. a Block.

xterm.js can do this: register a custom OSC handler
(`Terminal.parser.registerOscHandler`) — VS Code's `ShellIntegrationAddon`
parses OSC 133/633 exactly this way — and drive a **React Blocks UI** (select /
copy / re-run one command's output, jump between prompts, fold long output,
exit/duration chips). For TermiPod this is doubly valuable: per-command
exit/duration/output is precisely what a **steward** wants to observe, so Blocks
aligns with the agent-control mission, not just aesthetics.

### 3.3 Feature borrow order (highest leverage first)

1. **Blocks (OSC 133)** — the single feature that most reads as "professional",
   reuses our stack, and serves the steward-observability mission.
2. **Command palette** — pure React, Ctrl/⌘+P fuzzy over commands, hosts,
   sessions, saved workflows. Cheap, high perceived value, fits our surface model.
3. **Workflows / notebooks** — parameterized saved commands. These map onto the
   hub's existing run/task primitives — **store them hub-side**, don't rebuild
   Warp Drive.
4. **IDE-grade input line** — multi-line (Shift+Enter), history, selections; the
   input affordance is React over xterm's grid.
5. **Local PTY + `serialize` restore** — a local shell (native power) and
   snapshot/restore across reconnects.
6. **Later:** split panes, theme system (map to our design tokens), then
   *optionally* a native GPU surface if throughput ever demands it.

## 4. Licensing note — and why integrating Warp's crates is not a shortcut

Warp's client is open source with a **split license** (verified against
`warpdotdev/Warp` `/crates`, 2026-07-08): the `warpui` / `warpui_core` /
`warpui_extras` **UI-framework** crates are **MIT**; the application crates —
`warp_terminal`, `warp_core`, `editor`, `command`, and the rest — are **AGPL v3**
(cloud "Warp Drive" stays closed).

A tempting idea is to accept AGPL (TermiPod is open source) and lift Warp's
terminal/Blocks code to save time. This does **not** hold up, for two reasons:

- **Legal — AGPL is a one-way door against our Apache-2.0 licence.** AGPL is not
  a "non-commercial" licence; it permits commercial use. Its obligation is that
  *distribution or network interaction* forces publication of the **entire
  combined work's** source under AGPL — and we ship signed installers, so it
  fires. Apache-2.0 code may flow *into* an AGPL project but not the reverse:
  linking any AGPL crate into the desktop binary **relicenses the whole desktop
  app to AGPL**, foreclosing permissive reuse and changing the deal for every
  contributor and downstream adopter. That is a deliberate relicensing decision,
  not a side effect worth taking for one feature.
- **Technical — there is no liftable "Blocks" crate.** Blocks live inside
  `warp_terminal` / `warp_core` / `editor`, and they render through `warpui`,
  Warp's **own native GPU surface** — not a webview, not xterm.js. Adopting them
  means adopting Warp's entire native rendering stack composited over/in place of
  our webview, discarding the React + xterm.js UI where every other surface
  lives: precisely the native-surface rewrite rejected in §3.1 (and by ADR-052
  for `libghostty`). AGPL or not, it is *more* effort than our own path, not
  less.

Meanwhile the crown-jewel feature is **not Warp IP**: Blocks is the open
**OSC 133** protocol (§3.2), implemented by iTerm2/VS Code/Ghostty alike. On our
existing stack it is a `registerOscHandler(133,…)` call plus a React navigator —
roughly a day or two, no licence entanglement.

**Consequences for our plan:** (1) **read the AGPL client freely for ideas** —
concepts (Blocks model, palette, workflows) aren't copyrightable and we already
planned to borrow them; (2) the **MIT `warpui*` crates remain permissively
usable** *if* we ever deliberately choose a native GPU surface — a separate
future project, not the route to Blocks; (3) our path (borrow *concepts*,
implement on xterm.js/React) keeps us Apache-2.0 and is the shorter route.

## 5. Recommended first slice

**Slice 1 — persistent terminal panel (Ask 1), no engine change.** Move the
terminal from a modal overlay to a mounted, toggleable dock panel; lift session
state into an app-scoped store; support multiple tabs; add the `webgl` +
`search` + `serialize` addons while we're in there. This delivers the exact thing
the director asked for ("switch to it anytime, like VS Code") and lays the
foundation every pro feature builds on.

**Slice 2 — Blocks via OSC 133.** Inject a shell-integration script over the
session, parse OSC 133 in xterm.js, render the React Blocks UI.

Slices 3+ follow §3.3.

## 6. Open questions for the director

- **Dock placement:** bottom panel (VS Code default, good for wide logs) or right
  panel (keeps the fleet tree + focus visible)? Recommendation: bottom.
- **Scope of Slice 1:** ship the persistent panel + tabs first and iterate, or
  bundle Blocks into the first visible release?
- **Local shells:** is opening a *local* PTY (not just SSH) in scope? It is the
  strongest "empower the user" lever and low-risk via a Rust PTY crate.
- **Ambition ceiling:** is a native GPU surface ever a goal (watch `libghostty`),
  or is xterm.js-forever the intended endpoint?

Resolution of §6 graduates this into an ADR (next free number: **053**).
