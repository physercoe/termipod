# Desktop Electron migration — M0–M4

> **Type:** plan
> **Status:** In progress (M0–M3.4 done; M4 underway, 2026-07-22) — executes
> [ADR-055](../decisions/055-desktop-electron-shell.md) (Electron shell,
> superseding ADR-051 D-1). The M3.4 cutover is complete: the Tauri shell,
> `src-tauri/`, the `desktop-v*` release lane, and the `@tauri-apps` frontend
> dependencies are removed; the frontend bridge is Electron + browser only.
> Grounded in a full inventory of the Tauri coupling at desktop v0.3.85: 84
> Rust commands / 5,388 lines across 18 modules; 71 commands invoked from the
> frontend at ~85 string-literal call sites; 10 event channels; 1 custom URI
> scheme; 3 plugins.
> **Audience:** contributors · principal
> **Last verified vs code:** desktop CalVer `2026.722.252` (Tauri retirement)

**TL;DR.** A strangler migration in five phases. **M0** (inside the current
Tauri app) hides the runtime behind a `src/bridge/` adapter and starts
exporting localStorage-resident user data to disk — shippable, zero behavior
change, and it de-risks everything after. **M1** stands up the Electron shell
with the preload bridge and the *cheap* command families (files, dialogs, hub
proxy, keychain, draw.io protocol) — the app boots and the core surfaces work.
**M2** ports the heavy natives (PTY, SSH/SFTP, voice, script, sync engines)
and compiles the vault crypto to WASM rather than re-implementing it. **M3**
is the release cutover: electron-builder packaging, signing/notarization,
electron-updater, and the one-time data/secret migration from the Tauri
install. **M4** pays down the per-engine workaround debt the move makes
deletable (Windows WebGL terminal back on, base64 IPC dropped). Tauri keeps
shipping until M3 completes; the browser build is untouched throughout.

---

## 1. What migrates — the inventory in one table

| Block | Rust today | Node/Electron target | Effort |
|---|---|---|---|
| App shell, window, external-open, reveal | `lib.rs` (796) | `BrowserWindow`, `shell.openExternal`, `shell.showItemInFolder` | S |
| Hub REST/binary proxy + SSE proxy | `lib.rs` (`hub_request*`, `hub_sse_*`) | main-process `undici` fetch + stream pump (same events) | S–M |
| System proxy / hostname / OS probe | `lib.rs`, `net.rs` | `os`, `session.resolveProxy`, registry/scutil via child_process | S |
| Files: docs, workspace tree+mutations, localfs, storage/attachments | `docfile` `workspace` `workspacefs` `localfs` `storage` (851) | `fs/promises` + `dialog` + `app.getPath('userData')` | M (mechanical) |
| draw.io install + `drawio://` scheme | `drawio.rs` (188) | `protocol.handle('drawio')` (privileged scheme, same traversal guard); `yauzl`/`extract-zip` | S |
| Keychain | `keychain.rs` (93) | binding over the same OS stores with the **same service/account names** (spike: `@napi-rs/keyring`); fallback `safeStorage` + one-time migration | S + spike |
| Local PTY | `pty.rs` (327) | `node-pty` (keep the `pty_start` subscribe-gate and login-PATH recovery) | M |
| SSH + SFTP | `ssh.rs` (599) | `ssh2` (keep TOFU host-key pinning, shared-connection multiplexing) | L |
| Voice ASR bridge | `voice.rs` (224) | `ws` (can set auth headers natively — simpler than Rust) | S |
| Script runner + local agent CLI | `script.rs` `local_agent.rs` (172) | `child_process` with timeout + temp cleanup | S |
| Sync engines: WebDAV, folder-sync, S3 (hand-rolled SigV4) | `webdav` `foldersync` `s3` (1,854, shared helper layer) | TS port of the decision logic under tests; `aws4`/SDK for SigV4 | **L (riskiest port)** |
| Vault crypto | `vault.rs` (251, has test vectors) | **not ported — compiled to WASM from the existing Rust** (ADR-055 D-3) | M (build plumbing) |
| Updater | `tauri-plugin-updater` + minisign | `electron-updater` (new manifest + signing) | M (M3) |

Frontend side: all `@tauri-apps/*` usage funnels through ~12 modules
(`hub/transport.ts`, `hub/sse.ts`, `ssh/tauri.ts`, `terminal/pty.ts`,
`voice/bridge.ts`, `vault/crypto.ts`, `platform.ts`, `state/*` wrappers,
`UpdateSection.tsx`) — the bridge swap touches those, not the 38k-line app.

## 2. M0 — bridge + data egress (ships inside Tauri)

The prerequisite wedge; everything in it is useful even if the migration
stalled.

1. **`src/bridge/` adapter.** One module exporting `invoke<T>(cmd, args)`,
   `listen(event, cb)`, `isShell()`, `shellKind(): 'tauri' | 'electron' |
   'browser'`. All 26 files that import `@tauri-apps/api` re-point to it;
   `isTauri()` call sites become `isShell()` except the handful that are
   genuinely engine-specific (the xterm renderer ladder, which gains
   `shellKind()`-aware logic in M4). No behavior change; ships as a normal
   release.
2. **Data egress.** localStorage is webview-profile-bound and will NOT follow
   the app into Electron's Chromium profile. Add a boot-time export: every
   `termipod.*` localStorage key serialized to
   `<app-data>/migration/state-v1.json` (debounced, versioned). Electron's
   first boot imports it; the file also becomes a free local backup.
3. **Updater handoff prep.** The Tauri updater manifest gains the ability to
   point at an arbitrary installer URL, so the final Tauri release can offer
   the Electron build as a normal update.

**Acceptance:** app behaves identically; `state-v1.json` appears and
round-trips through a manual import test; no `@tauri-apps` import outside
`src/bridge/`.

## 3. M1 — Electron shell MVP

New `desktop/electron/` (main + preload, TypeScript, esbuild):

- `main.ts` — single `BrowserWindow` (1280×800, min 900×600), loads the same
  Vite `dist/`; `contextIsolation` on, sandbox on, `nodeIntegration` off.
- `preload.ts` — exposes exactly `{ invoke, listen }` over
  `ipcMain.handle`/`webContents.send`, with an explicit command allowlist
  (the successor of `capabilities/default.json`).
- Command families, in order: platform helpers → **hub transport, renderer-
  direct** (register §7 rows 1–2: the `hub_request*`/`hub_sse_*` proxies are
  *not* ported — the bearer injects via `session.webRequest`, the renderer
  `fetch`es the hub directly, and `hub/transport.ts`/`hub/sse.ts` take their
  existing browser-build paths under Electron; the bridge keeps call sites
  unchanged) → keychain (spike result) → docs/workspace/localfs/storage/
  attachment families + dialogs → `drawio://` privileged protocol →
  `open_browser_window` as `BrowserWindow`+preload.
- CSP carried over from `tauri.conf.json` (swap `ipc:` origins for the
  preload; keep `frame-src … drawio:`).
- `state-v1.json` import on first boot (before renderer load).

**Acceptance:** Fleet, Read, Author (incl. figures + draw.io), Settings work
end-to-end against a live hub on all three OSes from `electron .`; SSE
streams survive suspend/resume; no renderer access to Node globals.

## 4. M2 — heavy natives + vault WASM

- **PTY** (`node-pty`): preserve `pty_open`/`pty_start` two-step (the
  subscribe gate that prevents losing the first prompt) and Unix login-PATH
  recovery; Windows ConPTY comes free.
- **SSH/SFTP** (`ssh2`): one TCP+auth session shared by shell/dup/exec/SFTP
  (matching `ssh_duplicate` semantics); TOFU host-key pinning against the
  keychain, same pin format; `ssh-connect-progress` phases preserved.
- **Voice** (`ws`): direct header-capable WebSocket; same `voice-event`
  contract.
- **Script/local-agent**: `child_process` with the same interpreter
  allowlist, 120s cap, temp-file cleanup.
- **Sync engines**: port `SyncAction`/`decide_both` + enumerate/zip/prop
  helpers first, **under a shared test suite with fixture trees** (the
  decision logic is the risk, not the HTTP); then WebDAV, folder-sync, S3
  (use `aws4` or the SDK instead of hand-rolled SigV4). Never-delete
  invariant gets an explicit test.
- **Vault**: build `vault.rs` (+ its unit tests) to WASM via wasm-pack
  (getrandom js feature); main-process loads it; the nine `vault_*` commands
  keep their signatures. Cross-check against the Dart test vectors in CI.

**Acceptance:** terminal (local + SSH), file transfer, voice dictation,
Zotero/workspace sync, and vault open/seal/wrap all pass parity checks vs the
Tauri build; vault WASM output byte-matches Rust outputs for the recorded
vectors.

## 5. M3 — packaging, updater, cutover

- **electron-builder** matrix in `desktop-release.yml`: NSIS+msi / dmg
  (universal) / AppImage+deb+rpm. macOS signing + **notarization** (new
  requirement for auto-update) and Windows signing certs are the
  long-lead-time items — start them at M1.
- **electron-updater** with its manifest; proxy support wired from the
  existing `system_proxy` path.
- **Migration on first boot:** import `state-v1.json`; keychain entries
  either read in place (napi keyring spike succeeded) or migrated once;
  draw.io war re-downloaded or copied from the old app-data dir.
- **Handoff:** ~~a final Tauri release offers the Electron installer as the
  update~~ — **dropped** (see "Handoff prompt dropped" below); the Tauri lane
  was migrated by manual download instead.
- **Tauri retirement (M3.4, done 2026-07-22):** `desktop/src-tauri/`, the
  `desktop-release.yml` / `desktop-v*` lane, the `tauri` CI job, and the
  `@tauri-apps/*` frontend dependencies are removed; `src/bridge/` is Electron +
  browser only; `handoff.ts` is deleted. Packaged-bundle icons moved to
  `electron/assets/`. The Electron shell is the sole desktop shell.

**Acceptance:** clean-install and upgrade-install both land with hub session,
vault, library, documents, and draw.io intact; auto-update round-trips
(N → N+1) on all three OSes; state + secrets from a previous Tauri install are
imported on first boot.

**Cutover decisions (2026-07-21, at M3 review):**

- **Targets trimmed:** NSIS / dmg / AppImage+deb (msi and rpm dropped — no
  known demand). **macOS is arm64-only**: the Intel-Mac population is zero, so
  the universal dmg is dropped (universal would also require force-installing
  both arches of `@napi-rs/keyring`'s per-arch optionalDependencies).
- **Release/tag scheme:** the repo's `releases/latest` belongs to the mobile
  lane (its in-app checker reads it via the GitHub API), so the Electron lane
  avoids it entirely: releases are **prereleases on the pushed `electron-v*`
  tag** (electron-builder's own GitHub publisher would create `v<version>`
  tags — the mobile workflows' trigger namespace), and packaged apps poll an
  electron-updater **`generic` feed** at the rolling `electron-latest`
  release, advanced by the release workflow's `promote` dispatch. Promotion is
  the go-live switch (replacing the draft-publish gate).
- **Handoff prompt dropped:** `handoff.json`'s URL (shipped in Tauri builds
  since M0.3) is baked to `releases/latest`, which mobile owns, so the prompt
  can never fire; the Tauri install base is small enough to migrate by manual
  download from the releases page. (`handoff.ts` was deleted at the M3.4
  cutover.)

## 6. M4 — Chromium dividend (workaround paydown)

Now-deletable per-engine debt, each with a verification:

| Workaround | Action | Status |
|---|---|---|
| xterm canvas-only on Windows (#333) | re-enable WebGL ladder behind `shellKind()==='electron'`, keep context-loss fallback | **DONE** — `17c8c575` (additive, Electron-gated; Tauri/browser unchanged) |
| PDF blob-iframe avoidance | keep canvas pipeline (it's better), delete the WebView2 comments/guards | **DONE (2026-07-22, test-first)** — the runtime shell-guards were already removed at the M3.4 cutover; corrected the stale WebView2 rationale comments (`ReadSurface`, `ArtifactViewer`) to the real, shell-agnostic reason (canvas = text layer + reflow zoom; kept). An E2E test pins the capability the comments referenced: a same-origin `blob:` iframe loads + stays scriptable (what `HtmlDoc` needs, what WebView2 refused) |
| `sizedSvg` WebKit shim + mermaid foreignObject risk | verify on Chromium; simplify or annotate as belt-and-braces | **DONE (2026-07-22, test-first — simplified)** — `FigureEditor.sizedSvg` no longer injects explicit width/height into the `<svg>` (the WebKit `naturalWidth === 0` workaround); it's now `svgSize`, returning just the w/h needed to size the export canvas. An E2E test pins that Chromium rasterizes a viewBox-only SVG via `drawImage(img,0,0,w,h)` to real pixels (no injection) |
| custom confirm/prompt modals, two-step arm patterns | **keep** (better UX than native dialogs) — re-document rationale as house style, not engine bug | keep (house style) |
| `setPointerCapture` avoidance, clipboard-image best-effort, `crypto.randomUUID` fallbacks | re-test on Chromium; delete guards that no longer trigger (randomUUID works if the renderer origin is secure — decide `app://` custom scheme vs `file://` here) | **randomUUID DONE** (4 id-generators → `crypto.randomUUID`; `app://` is a secure origin — §7 row 12). `setPointerCapture` **KEPT**: the window-listener pattern is portable and works on Chromium — reverting is churn with regression risk, no benefit. clipboard-image best-effort deferred (device verify) |
| base64 IPC encodings (PTY/SSH/SFTP/blob/PCM) | switch to native `Uint8Array` transfer, family by family, behind the bridge | PTY **already bytes** (M2 authored it fresh: `pty-data` emits a `Buffer`, renderer wraps `new Uint8Array`); SSH/SFTP/blob/PCM deferred (shared renderer path — needs bridge byte-negotiation, family-by-family) |
| OS file drag-drop (never worked under Tauri) | free win: wire Chromium file drops into Author/Read | **Author DONE** — image drop into the WYSIWYG editor (capture-phase, inserts once even if Crepe also handles it; inert under Tauri). Read (library/PDF drop targets) deferred — needs a product decision + device verify |
| Native context menu (WebView2 default gone; only custom menus worked) | pop a native Cut/Copy/Paste/Select-All for editable fields + selections | **DONE** — `ipc/menu.ts` (role-based) + a renderer bubble-phase `contextmenu` fallback (`nativeContextMenu.ts`) gated on `!e.defaultPrevented` so in-app custom menus win, no double menus; Electron-only, inert under Tauri |
| Sync proxy not applied (webdav/folder/s3/zotero/drawio accepted `proxy` but ignored it) | route every request through a proxy-aware fetch | **DONE** — `ipc/net.ts` `proxyFetch` (undici `ProxyAgent` when set, direct global `fetch` otherwise; lazy-loaded, socks→direct); `proxy` threaded through all four transports. Additive (direct path unchanged); pairs with the Chromium `session.resolveProxy` detection fix |

**M4 execution status (updated 2026-07-22).** M4 runs *after* the M3 cutover in
the plan order. Precondition **(a) the Tauri lane has retired** is now **met**
(M3.4, done 2026-07-22) — so the guard-deletions are no longer blocked by a
still-shipping WebView2 build; they remain gated only on **(b) Chromium
behaviour verified on a real device build** (this repo's CI has no interactive
Electron run yet). The Windows-WebGL win (row 1) already landed as a pure
additive, `shellKind()==='electron'`-gated change with a robust fallback ladder
(WebGL → canvas → DOM):

- **Guard-deletions** (PDF blob-iframe, `setPointerCapture`, clipboard
  try/catch, WebView2 dialog shims): with the Tauri/WebView2 shell retired,
  these guards no longer protect a live product — but deleting them still needs
  device verification that the Chromium behaviour they compensated for is
  actually clean. Gated on (b).
- **base64→bytes IPC** (SSH/SFTP/blob/PCM): the renderer call path is shared
  across shells, so byte transfer needs the bridge to negotiate per-shell
  encoding (§7 row 4) — a family-by-family refactor, not a flag flip. PTY is
  already bytes end-to-end (no wire change was needed — it was authored under
  Electron in M2). Gated on (b) for throughput verification.
- **Net-new affordances** (OS file drag-drop, `printToPDF`, native context
  menus): additive Chromium capabilities with no Tauri equivalent, but each
  needs UX integration + a device build to prove out. Gated on (b).

House-style keeps (custom confirm/prompt modals, two-step arm, canvas PDF
reading) are settled per §7's non-goals and need no code change.

## 7. Optimization register — what the engine swap makes cheaper

Beyond the M4 workaround paydown (§6), the move unlocks structural
optimizations. Each is tagged with the phase that should claim it; the two
M1 rows shape the shell architecture and are folded into §3 above.

| # | Opportunity | Today | Under Electron | Phase |
|---|---|---|---|---|
| 1 | **Delete the SSE proxy** | `hub_sse_*` + the `hub-sse` base64 chunk pump exist because `EventSource` can't set an auth header | the browser build's fetch-SSE reader works in the renderer *with* headers — it becomes the only path; the proxy, per-stream cancellation state, base64 re-encode, and two IPC hops per event are deleted | **M1** |
| 2 | **Renderer-direct REST** | `hub_request`/`hub_request_bytes` proxy for CORS + bearer secrecy; every blob base64s through IPC twice | bearer injected via `session.webRequest.onBeforeSendHeaders` (the token never enters renderer JS — better than today); renderer `fetch`es the hub; blob inflation gone | **M1** |
| 3 | System probes | `reg query` / `scutil` child processes for proxy + build number | `session.resolveProxy()`, `os.release()` | M1 |
| 4 | Binary IPC | base64 on pty/ssh/sftp/storage/attachment channels | `Uint8Array` structured clone; `xterm.write` accepts bytes — felt in terminal throughput and file transfer | M2/M4 |
| 5 | Voice PCM frames | base64 strings per 16 kHz frame | transferable `ArrayBuffer`s to the main-process `ws` (the socket stays in main — renderer WebSocket still can't set headers) | M2 |
| 6 | Zotero import | `sql.js` WASM in the renderer (1–2 MB chunk, whole DB in memory, UI thread) | `better-sqlite3` in main, off-thread; the WASM chunk leaves the bundle | M2 |
| 7 | Sync-engine streaming | whole zips buffered in memory (Rust) | Node streams (`yauzl`/`archiver` piped to HTTP) — matters at the 100 MB folder-sync cap | M2 |
| 8 | Secret splitting | frontend splits secrets at CredMan's 2560-byte cap (`keychain_is_windows`) | if the keychain spike lands on `safeStorage`: cap, splitting, and probe all deleted | M2 (spike outcome) |
| 9 | xterm renderer | canvas-only on Windows (#333) behind an OS-probe ladder | WebGL everywhere + context-loss fallback | M4 |
| 10 | PDF export of figures/docs | deferred to the Phase E artifact pipeline ([figure-renderer-registry.md](figure-renderer-registry.md) OQ 2) | `webContents.printToPDF` — essentially free; answers that OQ platform-side | M4 |
| 11 | Context-menu clipboard | `execCommand` shim (webview blocks paste; native menu disabled) | native context menus / grantable clipboard read; shim deleted | M4 |
| 12 | `crypto.randomUUID` fallbacks | 5 call sites (non-secure `tauri://` context) | **DONE (2026-07-22)** — 4 id-generators (canvas/library/annotations/file-transfer) use `crypto.randomUUID`; `app://` is a secure origin | M4 |
| 13 | Vite build target | conservative multi-engine target; 2.77 MB entry chunk | **DONE (2026-07-22)** — `build.target: 'chrome120'`; modern syntax kept native (entry chunk 2,583 → 2,539 kB) | M4 |
| 14 | E2E coverage | not possible against Tauri webviews | **DONE — smoke + 3 flows (2026-07-22)** — Playwright drives the real Electron app under xvfb (`desktop/electron/e2e/`, `desktop.yml` → `e2e` job). Smoke (boot+paint, bridge + `app_version` round-trip, secure `app://`) + **terminal** (real node-pty round-trip — the job rebuilds node-pty for the Electron ABI with plain node-gyp — plus a UI open-local-shell → xterm-mounts check), **draw.io** (`drawio_status` round-trip), **figure export** (SVG→canvas→PNG rasterization). Next: dialog-driven save flows (need a headless dialog seam) + hub-backed document surfaces (need a hub fixture) | M4 → ongoing |
| 15 | New OS affordances | file drop inert; in-app toasts only; SSE reconnects on-error | Chromium file drag-in (+ `startDrag` out), OS notifications for attention requests, `powerMonitor`-driven suspend/resume reconnect, multi-window surface detach | post-M3, opportunistic |

**Deliberate non-goals:** the custom confirm/prompt modals and two-step arm
patterns stay (better UX than native dialogs — house style, per §6); the
canvas PDF *reading* pipeline stays; the vault crypto stays Rust-via-WASM and
is deliberately *not* optimized (byte-compat, ADR-055 D-3).

## 8. Sequencing & risk

- **Order: M0 → M1 → M2 → M3 → M4.** M0 ships in the normal release train.
  M1/M2 develop on a branch with dual-shell capability (the bridge makes the
  same frontend build run under either shell — that IS the test harness:
  parity = same actions, same hub traffic, same files, both shells).
- **Feature work continues on Tauri** until M3; the bridge keeps new
  features shell-agnostic by construction. Freeze only the week of cutover.
- **Riskiest items:** (1) the sync-engine TS port — mitigated by porting
  decision logic under fixture tests before touching HTTP, and by the
  napi-rs fallback (keep Rust engines as a native addon) if parity testing
  finds drift; (2) signing/notarization lead time — start at M1; (3) the
  keychain-compat spike — if `@napi-rs/keyring` can't read the existing
  entries, fall back to `safeStorage` + a one-time re-auth flow (hub token
  re-login, vault re-unlock via device wrap → worst case recovery code).
- **No hub, mobile, or token-pipeline changes anywhere.** The browser build
  stays green throughout (the bridge's `browser` shell kind is today's
  degrade path, unchanged).
- **Rough sizing:** M0 ~1 wk · M1 ~2 wk · M2 ~3–4 wk (sync engines dominate)
  · M3 ~2 wk elapsed (signing waits) · M4 ~1 wk. One contributor plus review.

## 9. Open questions (not blockers)

1. **Renderer origin:** `file://` vs a custom `app://` privileged scheme
   (secure context, cleaner CSP, service-worker option). Decide in M1;
   `app://` is the default candidate.
2. **Keychain service names:** exact `keyring` service/account strings must
   be confirmed against the spike before M3's migration design freezes.
3. **Dual-shell period length:** one overlap release or longer? Driven by
   install-base feedback after M3.
4. **Window chrome:** the DOM already owns a titlebar-ish header — adopt
   `titleBarStyle: hidden` + overlay for a native-feeling frameless look, or
   keep OS decorations (today's behavior)? Deferred to M4; keep decorations
   for parity at cutover.

## Related

- [ADR-055](../decisions/055-desktop-electron-shell.md) — the decision this
  plan executes; alternatives and consequences live there.
- [ADR-051](../decisions/051-desktop-client-stack.md) — D-2–D-5 (React/Query/tokens/REST)
  remain the frontend architecture; only D-1 is superseded.
- [ADR-052](../decisions/052-breakglass-ssh-and-key-vault.md) — the vault
  interop contract behind the WASM exception; the SSH/PTY semantics ported
  in M2.
- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) ·
  [figure-renderer-registry.md](figure-renderer-registry.md) — feature plans
  that continue on the Tauri shell until M3 and must stay bridge-clean.
