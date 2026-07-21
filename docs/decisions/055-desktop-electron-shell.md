# 055. Desktop shell — Electron, superseding the Tauri OS-webview

> **Type:** decision
> **Status:** Proposed (2026-07-21) — supersedes [ADR-051](051-desktop-client-stack.md)
> **D-1 only** (the shell). ADR-051's D-2–D-5 (React + TypeScript, TanStack
> Query + SSE manager, the DTCG token pipeline, REST+SSE-never-MCP) are
> **unchanged** and remain in force. Planned in
> [`plans/desktop-electron-migration.md`](../plans/desktop-electron-migration.md).
> **Audience:** contributors · maintainers
> **Last verified vs code:** desktop v0.3.85 (figure registry Phase B, `71e0e328`)

**TL;DR.** The desktop shell moves from **Tauri v2 (three OS webviews)** to
**Electron (one pinned Chromium)**. ADR-051 D-1 kept Electron as "the fallback
only on a concrete Tauri capability gap" — that gap has now materialized, not as
a missing API but as the **webview rendering matrix itself**: WebView2, WKWebView,
and WebKitGTK each required their own workarounds (xterm WebGL black-screen,
blob-iframe refusals, SVG rasterization failures, unreliable dialogs/pointer
capture), every feature pays a three-engine QA tax, and the engines regress with
OS updates we don't control. The 5.4k-line Rust core is reimplemented as an
Electron main process in TypeScript — **except the vault crypto, which is
compiled from the existing Rust to WASM** so byte-compatibility with the mobile
client is never re-derived by hand. The IPC contract (71 commands, 10 events)
is preserved verbatim behind a runtime-agnostic bridge, so the 38k-line
frontend migrates by adapter, not by rewrite.

## Context

ADR-051 D-1 chose Tauri for footprint and a Rust core, with an explicit escape
hatch: *"Electron is the fallback only on a concrete Tauri capability gap."*
Eleven releases in (v0.3.85), the gap is concrete — but it is not a missing
capability, it is the **OS-webview rendering matrix**. The codebase now carries
a documented trail of per-engine damage:

- **WebView2 (Windows).** xterm's WebGL renderer black-screens under ANGLE
  (#333) — the terminal is **canvas-only on Windows**, permanently gated by OS
  probe (`terminal/Screen.tsx:133-190`). `blob:`-URL iframes are refused — the
  PDF read path was **rewritten onto raw canvas** (`ui/PdfCanvas.tsx`,
  `ReadSurface.tsx:416`). Data-URL PDFs blocked (`ArtifactViewer.tsx:126`).
  `window.confirm`/`prompt` are unreliable — replaced app-wide with custom
  modals and two-step "arm then confirm" delete patterns (6+ surfaces).
  `setPointerCapture` is unreliable — every drag interaction uses hand-rolled
  window-listener models (5 files). Clipboard image writes are best-effort.
- **WebKitGTK (Linux).** SVG-as-image reports `naturalWidth === 0` for
  viewBox-only SVGs — figure PNG export needs a dimension-injection shim
  (`FigureEditor.tsx:114`), and mermaid's `foreignObject` labels may rasterize
  blank (Phase A review, unresolved). General compositing/scroll performance
  lags the other two engines.
- **Non-secure-context origins.** `tauri://localhost` denies
  `crypto.randomUUID` — five call sites carry fallbacks.
- **Structural cost.** Every new surface (figures, PDF, terminal, draw.io
  embed) is QA'd against three engines, and each engine ships on the **OS
  update cadence, not ours** — a Windows or GTK update can regress installed
  builds with no code change on our side.

The trade has inverted: Tauri's benefits to us are small binaries and the Rust
core, but the Rust core's jobs (SSE-with-auth proxy, keychain, PTY, SSH) all
have first-class Node equivalents, while the webview matrix costs us real
features (Windows WebGL terminal), real rewrites (PDF), and an unbounded QA
surface. Electron ships one pinned Chromium on all three platforms — the
engine becomes part of our release, not the OS's.

Scale of the coupling (from the migration inventory): **84 Rust commands**
(~5,388 lines, 18 modules), **71 commands actually invoked** from the frontend
across ~85 call sites (all string-literal, funneled through ~12 modules),
**10 event channels**, one custom `drawio://` scheme, 3 Tauri plugins
(dialog, updater, process), and 32 frontend files branching on `isTauri()`.

## Decision

- **D-1 — Shell: Electron, one pinned Chromium.** The desktop client is
  packaged with current Electron; the renderer is the existing Vite/React app
  unchanged; the main process is TypeScript. The plain-browser build (no shell)
  remains a supported target exactly as today.

- **D-2 — The IPC contract is preserved verbatim, then relaxed.** A
  runtime-agnostic bridge (`src/bridge/`) fronts all `invoke`/`listen` usage;
  round 1 keeps every command name, event name, and payload shape (including
  base64 byte encodings) identical, so the frontend diff is import-swaps, not
  logic. Binary-channel cleanups (dropping base64 where Electron IPC passes
  buffers natively) come only after parity ships.

- **D-3 — Native layer: TypeScript ports, except crypto.** Main-process
  reimplementations use the mature Node equivalents — `node-pty` (PTY), `ssh2`
  (SSH/SFTP), `ws` (voice ASR socket), `undici`/net (hub proxy + sync engines),
  Electron `dialog`/`shell`/`protocol`. **Exception: `vault.rs` is not
  re-implemented** — the existing Rust crypto (AES-256-GCM seal, X25519+HKDF
  device wrap, Argon2id recovery wrap) compiles to **WASM** with its test
  vectors, because byte-compatibility with the mobile Dart implementation is
  load-bearing (ADR-052) and a hand port is exactly how interop breaks.
  Keychain entries are read through a binding over the same Rust `keyring`
  store semantics (or migrated once), so installed users keep their secrets.

- **D-4 — Security posture: preload allowlist replaces capabilities.**
  `contextIsolation` on, `nodeIntegration` off, sandbox on; the preload
  exposes exactly the bridge surface (the command allowlist that
  `capabilities/default.json` used to express). The CSP carries over; the
  `drawio://` scheme re-registers as a privileged custom protocol with the
  same path-traversal guard.

- **D-5 — Distribution: electron-builder + electron-updater.** CI keeps the
  three-platform matrix (NSIS/msi, dmg, AppImage/deb/rpm) via
  electron-builder; auto-update moves from the minisign `latest.json` to
  electron-updater's manifest with new signing material (macOS notarization
  becomes mandatory for auto-update). One final Tauri release ships a
  data-egress step so localStorage-resident user data (documents, notes,
  library) survives the webview-profile change.

## Consequences

**Easier / unlocked:**
- One rendering engine, pinned by us: the Windows terminal gets WebGL back,
  the PDF blob-iframe rewrite pressure disappears, figure PNG export works
  identically everywhere, and new surfaces are QA'd once.
- Chromium-only APIs open up (proper clipboard, pointer capture, secure
  context everywhere); the per-engine workaround inventory becomes deletable
  debt with a paydown list instead of load-bearing mystery code.
- CI simplifies: no Rust toolchain in the desktop lane (except the small
  vault-WASM build), no cargo cache, faster builds.
- The main process is the same language as the app; one team, one toolchain.

**Harder / cost:**
- **Footprint:** installers grow from ~10–15 MB to ~90–120 MB; idle RAM rises
  by roughly a Chromium process tree. Accepted deliberately — this is a
  professional workbench, not a utility tray app.
- **A migration release**, not just a rebuild: secrets, localStorage data,
  and the draw.io install must carry over; auto-update changes manifest and
  signing; the updater handoff needs a final Tauri release that points at the
  new artifacts.
- ~5.4k lines of Rust are re-implemented (~3.5k of it mechanical); the SSH
  host-key TOFU pinning, PTY subscribe-gate, and sync-engine decision logic
  must be ported with their subtleties, under tests.
- Electron's security posture is ours to hold (the preload allowlist replaces
  Tauri's declarative capability model).

**Unaffected:** the browser build, the Flutter mobile client, the hub API, the
token pipeline (D-4 of ADR-051), and all workbench/React architecture.

## Alternatives considered

- **Stay on Tauri, keep patching per-engine.** The status quo. Rejected: the
  workaround inventory is growing, not shrinking; engines regress on OS
  cadence; Windows WebGL and WebKitGTK rasterization are upstream bugs we
  cannot fix, only avoid — feature work is already being shaped by the
  weakest engine.
- **Tauri with an embedded Chromium (CEF/Verso).** Would fix the matrix while
  keeping the Rust core, but Tauri has no production-supported embedded-
  Chromium runtime today; adopting an experimental one trades a known matrix
  for an unknown runtime. Rejected for now; re-evaluate if it matures.
- **Dual-shell (Electron only where the webview is worst).** E.g. Electron on
  Linux/Windows, Tauri on macOS. Rejected: two shells is a *worse* matrix —
  every native module and the updater ship twice.
- **Wails / Neutralino / Flutter desktop.** Same OS-webview problem (Wails,
  Neutralino) or already rejected on embedding grounds (ADR-050).
- **Rust core as a napi-rs addon under Electron.** Keeps all 5.4k lines
  verbatim. Seriously considered; rejected as the default because it retains
  the full Rust toolchain and an FFI boundary for code whose Node equivalents
  are mature — kept **selectively** for the one module where re-implementation
  is dangerous (vault crypto, via WASM) and as the fallback if a TS port of
  the sync engines proves error-prone.

## References

- Plan: [`plans/desktop-electron-migration.md`](../plans/desktop-electron-migration.md)
  — phases M0–M4, the command/event mapping, and the data-migration design.
- Supersedes: [ADR-051](051-desktop-client-stack.md) **D-1** (shell);
  D-2–D-5 remain in force.
- Builds on: [ADR-050](050-desktop-workbench-delivery-model.md) (web-tech
  desktop client); [ADR-052](052-breakglass-ssh-and-key-vault.md) (the vault
  whose crypto interop makes D-3's WASM exception load-bearing).
- Evidence: the Phase A figure review (WebKitGTK rasterization), #333 (WebView2
  WebGL), `ui/PdfCanvas.tsx` / `ReadSurface.tsx` (blob-iframe rewrite).
