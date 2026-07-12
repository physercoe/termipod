# Workbench agent companion + offline diagrams

> **Type:** discussion
> **Status:** Open (2026-07-12) — scopes the director asks around the research
> workbench. **J2 Author multi-document tabs + on-disk file save/open shipped**
> (`AuthorSurface.tsx` + `state/documents.ts` + Rust `docfile.rs`). A **cross-surface
> agent companion panel** (Read J1 *and* Author J2) and **offline draw.io
> diagrams** are designed here and deferred pending the decisions below — plus a
> concrete analysis of **how the POSIX-only host-runner could support Windows**.
> Feeds [desktop-workbench-jobs.md](../plans/desktop-workbench-jobs.md); relates to
> [research-tooling-landscape.md](research-tooling-landscape.md) (embed vs build)
> and the host-runner in [../spine/agent-lifecycle.md](../spine/agent-lifecycle.md).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop v0.3.25 (post-release, on main)

**TL;DR.** The Author tab now holds **multiple documents as tabs**, each a
Markdown split-editor, device-local by default and **saveable to a real file on
disk** (native dialog → `doc_save`/`doc_open`/`doc_write`). Three follow-ups need
a decision: **(2) an agent companion panel** — the director wants it *alongside
both reading (J1: ask about a PDF / an item) and writing (J2)*, so it's one shared
component, not an Author-only feature. It's gated by a platform fact: the
**host-runner is POSIX-only**, so a Windows desktop can't host a local agent as-is
— but there **are** ways to support Windows (§2.4: WSL2 today, a native ConPTY +
Job-Objects port, or a **desktop-local mini-runner reusing the ConPTY the desktop
already ships**). **(3) draw.io diagrams offline** — the director chose **bundle
drawio locally**, which is feasible (Apache-2.0, fully client-side) but costs
installer size and a vendoring step.

---

## 1. Shipped — multi-document tabs + file storage (director ask #1)

"The Author tab should support multiple tabs, and where are files saved?"

- `state/documents.ts` — a `useDocuments` store: a list of `Doc { id, kind,
  title, body, filePath?, dirty?, updatedAt }` persisted to `localStorage`
  (`termipod.documents.v1`). Migrates the old single `termipod.draft.author`
  draft into the first document so nothing is lost.
- `AuthorSurface.tsx` — a **tab strip** (one tab per open document, `+ New`,
  close-with-confirm) over the existing split Markdown editor.
- **Where files live** (the explicit answer):
  - **By default: device-local.** A document's text lives in the WebView's
    `localStorage` on the user's own machine — nothing is uploaded. The editor's
    meta bar says *"Saved in-app (this device)"*.
  - **Optionally: a real file on disk.** `Save` opens a native Save dialog and
    writes the file (Rust `doc_save`); `Open file…` reads one (`doc_open`); a
    linked document re-saves in place (`doc_write`). The meta bar then shows the
    filename and a ● dirty marker. These reuse the same `tauri-plugin-dialog`
    already added for the Zotero storage link — no new dependency.

**Not yet:** promoting a document to a **hub Document/Deliverable** (with run
provenance, fleet-shared, agent-readable) — the same graduation the
[reference library](reference-library-and-reading.md) is taking. That is the
natural convergence point with ask #2.

---

## 2. Agent companion panel — Read *and* Author (director ask #2) — DECISION NEEDED

"There should be an agent page alongside the Author edit page, since
editing/writing is agent-assisted" — **and** (follow-up) "there should be an agent
page/panel alongside reading a PDF or the items page too."

So this is **one shared companion panel**, not an Author feature: a collapsible
right-hand agent panel that mounts on **both**:

- **J1 Read** — ask the agent about the open PDF / selected item: *summarise,
  extract claims into a table (the Elicit/Undermind pattern from the
  [reference-library discussion](reference-library-and-reading.md)), find related
  work, draft notes.* The agent's reply can be pulled into the item's Notes/Read
  body.
- **J2 Author** — draft/critique/rewrite the active document; a diagram document's
  reply is draw.io XML (§3).

Build it once as a shared `ui/AgentCompanion` that takes a **context payload**
(the current item/PDF text, or the current document) and renders a transcript +
composer. The surface decides what context to feed and where "insert reply" lands.

The blocking question the director raised — **does this support Windows, given the
host-runner seems POSIX-only?** — is answered in §2.1–2.4.

### 2.1 Finding: the host-runner is POSIX-only

Verified in the code — the deputy that spawns and supervises agents is Unix-bound:

- `hub/internal/hostrunner/tmux_launcher.go` — every agent is placed in a **tmux**
  window/pane (`exec.Command(ctx, "tmux", …)`); tmux has no Windows port.
- `hub/internal/hostrunner/launch_m2.go:53,63,131` — `exec.Command(ctx, "bash",
  "-c", …)`, `syscall.SysProcAttr{Setpgid: true}`, and
  `syscall.Kill(-pid, SIGKILL)` (process-group teardown). These fields/calls are
  Unix-only and the file is **not** build-tagged, so it **won't compile** on
  Windows.
- `runner.go:284` requires the `tmux` and `hub-mcp-bridge` binaries on PATH.

So a **Windows machine cannot run the host-runner as-is** and cannot host a local
agent. This is the fleet's execution substrate, not a desktop-client limitation.

### 2.1a Director decision (2026-07-12): agent must edit local Windows files

The director ruled: **the agent must read/edit the files on the Windows machine**,
so a **remote hub agent (model A) is rejected** — a remote POSIX agent sees the
remote FS, not the user's Windows files. That forces a **locally-running agent on
Windows** with native filesystem access → **path P2** (desktop-local mini-runner),
below, is the chosen direction.

**Do the agent CLIs even run natively on Windows? Yes (verified 2026-07):**

- **Claude Code** — runs **natively on Windows** since late 2025 (PowerShell,
  `irm https://claude.ai/install.ps1 | iex`); WSL is optional (only needed for
  bash-tool sandboxing). Reads/edits native Windows files.
- **Codex CLI** — runs **natively on Windows** (early 2026); `npm i -g
  @openai/codex` works in PowerShell, with AppContainer-based Windows sandbox
  modes that keep writes inside the working folder. WSL optional.
- **Gemini CLI / antigravity** — Node-based, cross-platform (run on Windows).

So a Windows-local agent with native file access is available today; the missing
piece is the **runner** that spawns and drives it (the POSIX host-runner can't —
§2.1).

### 2.2 Two consumer models for the companion

- **A. Remote hub agent (architecture-consistent).** The panel reuses the existing
  hub agent transcript + composer (`surfaces/AgentTranscript.tsx`,
  `ui/Composer.tsx`, `hub/` client) to talk to an agent on a **remote POSIX host**;
  replies are pulled into the doc / notes. Works from a Windows desktop **iff** the
  fleet has ≥1 POSIX host with a running agent. Fits the data-ownership law once the
  item/document is a **hub entity** the agent reads via MCP (the reference library
  already graduated — [ADR-053](../decisions/053-hub-reference-library-entity.md);
  Author docs would follow, §1 "not yet").
- **B. Inline LLM assistant (self-contained).** A lightweight in-pane assistant
  calling an LLM directly with the doc/PDF as context — no spawned agent, works on
  a lone Windows desktop with no fleet. *Cost:* the desktop has **no LLM
  endpoint/key path wired** today, and a raw provider key bypasses hub
  provenance/telemetry/policy (ADR-030) — a governance decision, not just
  engineering. Could be routed through the hub's `llm_call` (M3) to keep
  provenance, which needs a reachable hub but no host-runner.

### 2.3 Is there a way to support Windows on the host-runner? — yes, three paths

Ordered lowest-effort → heaviest:

- **P1. Run the host-runner in WSL2 (works today, ~zero code).** WSL2 is a real
  Linux kernel on Windows; tmux, bash, and process groups all work, and GPU/CUDA
  pass through. The user installs WSL2 and runs the existing host-runner binary
  there. *Tradeoff:* agents operate in the **WSL/Linux filesystem**, not native
  Windows paths (usually fine for coding agents; surprising for native-Windows
  tools). This is the pragmatic "support Windows now" answer and needs only docs.
- **P2. Desktop-local mini-runner reusing the ConPTY the desktop already ships
  (medium effort, best fit for *this* feature).** The desktop already bundles a
  **cross-platform PTY** — `src-tauri/src/pty.rs` uses
  `portable_pty::native_pty_system()` (ConPTY on Windows) for its local terminal
  dock. A small Rust "agent runner" could spawn an engine process **through that
  same ConPTY on Windows**, speak MCP to the hub, and drive it via **M2 structured
  stdio** (JSON lines) — no tmux, no `bash -c`, no Unix process groups. Teardown
  uses a Windows **Job Object** (`AssignProcessToJobObject` +
  `TerminateJobObject`) instead of `Kill(-pid)`. This makes the companion work
  **standalone on Windows** without WSL2, reusing infrastructure already in the
  build. *Tradeoff:* it's a new, smaller runner (not the full host-runner) — no
  tmux session-survives-disconnect, and the M4 claude-code path (which routes
  input via `tmux send-keys`, ADR-027) would fall back to M2 or a direct-PTY write
  on Windows.
- **P3. Native Windows port of the host-runner itself (heaviest, ongoing).**
  Replace the three POSIX pillars behind build-tagged `_windows.go` files: **tmux →
  ConPTY + an in-process session/pane manager** (re-implements multiplexing +
  scrollback + the "viewers SSH to tmux" breakglass path, plan §5); **`bash -c` →
  a configurable shell** (pwsh/cmd, or git-bash for POSIX commands); **process
  groups → Job Objects** (`golang.org/x/sys/windows`). Behaviour is data (frame
  profiles are YAML), but the launcher/PTY substrate is Go that assumes tmux — this
  is a large, permanently-maintained second substrate. Only worth it if native
  Windows *hosts* (not just desktops) become a first-class fleet target.

### 2.4 Open questions (decide before building)

1. Must the companion work **standalone on Windows with no fleet**? If yes → **P2**
   (desktop-local ConPTY runner) or model **B**; if a fleet is always present →
   **model A** is simplest and Windows is a non-issue for the desktop.
2. If a Windows machine should be a real **agent host** (not just a client), is
   **WSL2 (P1)** an acceptable answer, or is a **native port (P3)** required?
3. Should "agent-assisted" mean the item/document is a **hub entity the agent CRUDs
   via MCP** (like [ADR-053](../decisions/053-hub-reference-library-entity.md))
   rather than a chat panel — converging ask #1's file model and the Read library
   with the fleet?

**Recommendation (updated per §2.1a decision): build P2 — the desktop-local
ConPTY mini-runner.** The director requires local Windows file access, so model A
is out and P2 is the path: a small Rust runner spawns a **native-Windows agent CLI**
(Claude Code / Codex, both native on Windows now) through the ConPTY already in
`pty.rs`, drives it via **M2 structured stdio**, and tears it down with a Windows
**Job Object**. The agent reads/edits the user's Windows files directly; the
companion panel (shared Read + Author) is its UI. P1 (WSL2) is the interim
fallback if a runner isn't ready; **P3** (native host-runner port) stays off the
table unless native Windows *hosts* become a fleet goal.

**Next-step scoping for P2:** (a) reuse `pty.rs`'s `portable_pty` to spawn the
agent; (b) a minimal MCP bridge desktop→hub so the local agent still gets hub
tools/identity (or run it hub-detached for pure local editing); (c) engine profile
= M2 (claude-code's M4 `tmux send-keys` input path doesn't apply without tmux —
use stdio); (d) permission/sandbox: lean on the engine's own native-Windows
sandbox (Codex AppContainer) rather than bwrap/seatbelt.

---

## 3. Offline draw.io diagrams (director ask #3) — chosen: BUNDLE

"The Author should also support diagram drawing like draw.io" — and the director
chose **bundle drawio locally** (offline) over the remote embed, referencing
[next-ai-draw-io](https://github.com/DayuanJiang/next-ai-draw-io).

### 3.1 How the reference app works

`next-ai-draw-io` uses the **`react-drawio`** npm package (a thin wrapper over an
`<iframe>` to the diagrams.net editor + its `postMessage` embed protocol) and has
an **LLM generate/modify the draw.io XML (mxGraph)**, which it loads back into the
editor. The AI's whole job is emitting valid draw.io XML; the editor renders it.
By default `react-drawio` points at the hosted `embed.diagrams.net`.

### 3.2 Bundling drawio offline is feasible

- **License:** Apache-2.0 — vendoring/redistribution is permitted.
- **Client-side:** draw.io is a pure browser JS editor; once its files load it
  needs **no network**. The official **draw.io Desktop** (Electron) is exactly
  this — the same webapp bundled offline — so the pattern is proven.
- **Artifact:** the prebuilt webapp is `src/main/webapp/` in `jgraph/drawio`
  (releases also ship `.war`/Docker). We vendor those static files.

### 3.3 Proposed integration

1. **Vendor** a pinned drawio webapp build under the desktop app (e.g.
   `desktop/vendor/drawio/` copied into the bundle resources) — *offline*.
2. **Serve locally**: load it from the app origin (place under the Vite/`public`
   output or a Tauri asset path) and point an `<iframe>` at
   `…/drawio/index.html?embed=1&proto=json&ui=min&noSaveBtn=1`.
3. **Protocol** (hand-rolled, no `react-drawio` dep — keeps `npm ci` clean, same
   choice as the J1 browser tab): on the iframe's `init` message, post
   `{action:'load', xml}`; on its `save`/`autosave` messages, store `xml` back
   into the `Doc.body`. A `diagram` document kind already exists in
   `state/documents.ts` for this.
4. **AI (later):** a diagram document is just draw.io XML in `Doc.body`, so
   ask #2's agent panel can generate/patch that XML exactly like the reference
   app — no separate mechanism.

### 3.4 Measured size — full vs trimmed (fresh `jgraph/drawio` clone, 2026-07)

`src/main/webapp` on disk = **150 MB**. Largest parts: `js/` 66 MB
(`integrate.min.js` 22 MB, `app.min.js` 9.1 MB, `stencils.min.js` 7.3 MB,
`viewer-static` 3.8 MB, `extensions` 3.8 MB, `viewer` 2.4 MB, `shapes-*` 1.5 MB),
`stencils/` 42 MB (raw XML), `img/` 12 MB, `images/` 6.4 MB, `templates/` 5.6 MB,
`WEB-INF/` 5.1 MB (Java), `resources/` 4.5 MB (i18n), `mxgraph/` 3.4 MB, `math4/`
3.4 MB (MathJax), `shapes/` 2.4 MB.

- **Full, as-shipped: ~150 MB** — far too big to vendor as-is, and mostly
  redundant for an iframe embed.
- **Trimmed for embed: ~15–25 MB.** Keep the runtime bundle — `app.min.js` (9.1)
  + `mxgraph` (3.4) + `stencils.min.js` (7.3, the *precompiled* shapes) +
  `shapes-*.min.js` (1.5) + `index.html` + `styles/` + minimal UI icons. **Drop:**
  the raw `stencils/` dir (42 MB — already compiled into `stencils.min.js`),
  `integrate.min.js`/`viewer*`/`extensions` (~32 MB, alternate entry bundles),
  `WEB-INF/` (Java servlet), `templates/` (gallery), `math4/` (unless math),
  most `resources/` locales (keep one), shape-preview thumbnails in `img/`.
  - Floor **~14 MB** = editor with only basic shapes (also drop
    `stencils.min.js`); realistic **~23 MB** keeps the full compiled shape set.

So the real choice is **~20–25 MB trimmed** vs 150 MB full — and the trim is
mostly deleting the redundant raw `stencils/` tree and alternate JS bundles, not
losing editor capability.

### 3.5 Tradeoffs to accept / decide

- **Installer size.** Even trimmed, ~20 MB is added to every platform installer.
  Acceptable, but decide whether diagrams justify it for all users or ship as an
  optional/lazy-downloaded component.
- **Vendoring hygiene.** A pinned, checked-in third-party build is a large,
  opaque blob in-repo; note the pinned version + provenance and refresh
  deliberately. (Alternative: fetch-at-build in CI rather than commit the blob.)
- **Update cadence.** Bundled drawio is frozen at the pinned version until we
  re-vendor — fine for an editor, unlike a security-sensitive dependency.

Recommendation: vendor a **trimmed** drawio webapp, serve from the app origin,
hand-roll the `postMessage` bridge, store XML in the `diagram` document. Build
this as its own wedge (the vendor commit is heavy and worth isolating).

---

## Decisions — status

1. **Companion panel (§2): RESOLVED.** Shared panel on **both Read (J1) and Author
   (J2)**. Agent must edit **local Windows files** → model A rejected → build **P2,
   a desktop-local ConPTY mini-runner spawning a native-Windows agent CLI** (Claude
   Code / Codex both native on Windows). **Attachment (director, 2026-07-12):
   hub-attached is the DEFAULT** (MCP bridge → hub tools/identity/telemetry) **but
   it must also run when the hub is UNREACHABLE** — i.e. degrade to hub-detached
   local editing, then re-attach when the hub returns. So the runner buffers /
   tolerates a down hub rather than hard-depending on it. *Still open:* does the
   Author/Read item graduate to a **hub entity** (agent CRUDs via MCP) when
   attached, with a local-file fallback when detached?
2. **Diagrams (§3): RESOLVED — optional download, not bundled.** Director:
   **the installer does NOT bundle drawio**; instead a **clickable "Download
   draw.io (~20 MB)" button** fetches the trimmed webapp once into a **persistent
   app-data dir** (survives app updates — keyed by drawio version, not app
   version, so an app update does **not** re-download). First diagram use →
   prompt to download; thereafter served from the app-data copy. *Still open:*
   trim scope (~23 MB full compiled shapes vs ~14 MB basic) and the download host
   (GitHub release asset we publish vs upstream).
3. **Native Windows host-runner (P3)** stays deferred — not needed for P2.

### Implementation notes for the optional drawio download (§3 decision)

- **Store:** a persistent OS app-data path (Tauri `app_data_dir`), e.g.
  `…/drawio/<version>/` — *not* the app bundle, so reinstalls/updates keep it.
  Presence check = "is it installed?"; the button hides once present.
- **Fetch:** a Rust command downloads a single archive (we publish a trimmed
  `drawio-webapp-<ver>.zip` as a GitHub release asset — reuses the release
  pipeline + proxy resolution already in `lib.rs`), unzips into the versioned dir.
- **Serve:** point the diagram iframe at the local copy (Tauri asset protocol /
  `convertFileSrc`) with `?embed=1&proto=json`; XML persists in the `diagram`
  `Doc.body`. No react-drawio dep (hand-rolled postMessage).
