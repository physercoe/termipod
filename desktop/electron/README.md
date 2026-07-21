# TermiPod Electron shell (ADR-055 M1)

The Electron shell that will replace the Tauri shell (see
[`docs/plans/desktop-electron-migration.md`](../../docs/plans/desktop-electron-migration.md)).
It wraps the **same** Vite build the Tauri shell ships (`../dist`); the frontend
talks to it through the runtime-agnostic `../src/bridge/`. Injecting
`window.__ELECTRON_BRIDGE__` from the preload is what flips that bridge from its
browser degrade path onto the Electron path — no frontend call site changes.

This is a **self-contained package** (its own `package.json` / `node_modules`)
so the Tauri release build's `npm ci` in `desktop/` never installs Electron.

## Layout

```
src/main.ts        single BrowserWindow, app:// scheme, IPC dispatch, lifecycle
src/preload.ts     sandboxed bridge → window.__ELECTRON_BRIDGE__ {invoke, listen}
src/appscheme.ts   app:// privileged scheme serving ../dist (secure origin + CSP)
src/events.ts      main→renderer event fan-out + subscribe-gate bookkeeping
src/ipc/dispatch.ts  command handler map = the allowlist (successor of capabilities/default.json)
assets/icon.png    app icon for the dev dock/window (copy of src-tauri/icons/icon.png)
src/ipc/platform.ts  platform_os / os_build_number / open_external / reveal_path / …
src/ipc/migration.ts migration_read (own userData, falling back to the Tauri
                   app-data dir for the one-time cross-install handoff, #353) /
                   migration_export (state-v1.json)
src/ipc/pty.ts     local PTY (node-pty): pty_open/start/write/resize/close —
                   JS-buffered subscribe-gate + login-shell PATH recovery (M2.1)
esbuild.mjs        bundles main + preload → out/*.cjs
```

## Run

```bash
# from desktop/: build the frontend once so ../dist exists
npm ci && npm run build

# from desktop/electron/: install, build the shell, launch
npm install
npm start          # esbuild → out/, then `electron .`
```

`npm run typecheck` type-checks without the Electron binary
(`ELECTRON_SKIP_BINARY_DOWNLOAD=1 npm install` is enough for typecheck/build).

## Status — M1 slices

- [x] **M1.1** shell scaffold: window, `app://`, preload bridge, IPC allowlist,
      platform + migration command families.
- [x] **M1.2** hub reachability — `session.webRequest` CORS bridging so the
      `app://` renderer's direct hub `fetch`/SSE works (bearer stays renderer-set
      for now; moving it into `onBeforeSendHeaders` is a plan §7 row-2 follow-up).
- [x] **M1.4** files + dialogs — docfile / localfs / workspace(+fs) / storage +
      attachments (26 commands), ported 1:1 from the Rust modules with matching
      arg keys (camelCase) and return-field casing (`is_dir`, `folderName`,
      `contentType`).
- [x] **M1.5** `drawio://` privileged scheme + `drawio_status`/`download`/
      `install_file` (extract-zip; version-keyed app-data root; traversal guard).
- [x] **M1.3** keychain — `safeStorage`-encrypted file store as the engine;
      `@napi-rs/keyring` reads the Tauri-written `secretstore.v1` once on first
      boot, and a store miss lazily migrates the matching per-item Tauri
      account (pre-consolidation secrets such as `hub_token_<id>`, #353) —
      both device-gated on macOS (whether a cross-app OS-keychain read succeeds).

**M1 is feature-complete (device-passed on macOS).**

## Status — M2 slices (heavy natives + vault WASM)

- [x] **M2.1** local PTY (`node-pty`) — `pty_open`/`start`/`write`/`resize`/
      `close` + `pty-data`/`pty-exit` events, ported from `pty.rs`. The
      subscribe-gate that stops the shell banner racing the renderer is kept by
      **buffering `onData` chunks in JS until `pty_start`** (node-pty auto-flows,
      so there is no reader-thread to withhold as portable-pty had); a pre-start
      exit is deferred to the same flush. Login-shell PATH recovery (`$SHELL
      -ilc`, async, cached) so GUI-launched agent CLIs resolve. Windows `.cmd`
      shims run through `cmd.exe /C`.
- [ ] **M2.2** SSH/SFTP (`ssh2`) · **M2.3** voice (`ws`) · **M2.4** script +
      local-agent (`child_process`) · **M2.5** sync engines (WebDAV/folder/S3,
      under a fixture test suite) · **M2.6** vault → WASM (wasm-pack from
      `vault.rs`, byte-compat).

> **Native addons need an Electron-ABI rebuild for the dev shell.**
> `@napi-rs/keyring` is Node-API (ABI-stable, works as-is), but `node-pty` builds
> against the Node ABI on `npm install` and must be rebuilt for Electron before
> `electron .` will load it: `npx @electron/rebuild -f -w node-pty` (or install
> with `npm_config_runtime=electron npm_config_target=<electron ver>`). M3
> packaging does this automatically and asarUnpacks both addons.

M3 = electron-builder packaging (asarUnpack + ABI-rebuild the native addons),
updater, cutover.
