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
src/ipc/ssh.ts     direct SSH + SFTP (ssh2): ssh_connect/duplicate/exec/write/
                   resize/close + sftp_list/read/write — TOFU host-key pinning,
                   shared-connection multiplexing, connect phases (M2.2a)
src/ipc/ssh_keys.ts SSH key store (sshpk): ssh_parse_key + ssh_generate_key —
                   ed25519 gen, encrypted OpenSSH PEM, SHA256 fingerprint (M2.2b)
src/ipc/voice.ts   DashScope ASR WebSocket (ws): voice_open/send/finish/close (M2.3)
src/ipc/script.ts  one-shot child runs (child_process): script_run +
                   local_agent_run — execFile, no shell (M2.4)
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
- [x] **M2.3** voice (`ws`) — `voice_open`/`send`/`finish`/`close` +
      `voice-event`, ported from `voice.rs`. `ws` sets the DashScope
      `Authorization: bearer` header the renderer's WebSocket can't; `voice_open`
      awaits open + run-task before returning so a later `voice_send` always
      meets an open socket. Pure JS — no ABI concern.
- [x] **M2.4** script + local-agent (`child_process`) — `script_run` (temp-file
      + interpreter, 120s cap, output clamp) and `local_agent_run` (argv-safe
      `claude -p`, **no** wall-clock cap, matching `local_agent.rs`). `execFile`
      (no shell) so nothing is interpolated.
- [x] **M2.2a** SSH transport + SFTP (`ssh2`) — `ssh_connect`/`duplicate`/`exec`/
      `write`/`resize`/`close` + `sftp_list`/`read`/`write`, ported from `ssh.rs`.
      One `ssh2.Client` (one handshake) backs many shell channels (duplicate/
      exec/SFTP share it; ref-counted end). TOFU host-key pinning in the
      safeStorage store — **not** migrated from the Tauri build (russh vs ssh2
      serialize keys differently → a Tauri pin can't be compared without
      spuriously rejecting a known host, so Electron re-TOFUs once per host at
      cutover). `ssh-data` carries raw channel Buffers. Device-gated: real
      handshake/auth/SFTP against a live server.
- [x] **M2.2b** SSH key store (`sshpk`) — `ssh_parse_key` (parse/introspect an
      imported key, encrypted or not) and `ssh_generate_key` (in-app ed25519).
      Verified locally: sshpk's encrypted OpenSSH-PEM output round-trips through
      ssh2's parser (so a generated key works with M2.2a's connect flow), its
      `SHA256:` fingerprint matches OpenSSH's, and a wrong passphrase throws.
- [ ] **M2.5** sync engines (WebDAV/folder/S3, under a fixture test suite) ·
      **M2.6** vault → WASM (wasm-pack from `vault.rs`, byte-compat).

> **Native addons need an Electron-ABI rebuild for the dev shell.**
> `@napi-rs/keyring` is Node-API (ABI-stable, works as-is), but `node-pty` builds
> against the Node ABI on `npm install` and must be rebuilt for Electron before
> `electron .` will load it: `npx @electron/rebuild -f -w node-pty` (or install
> with `npm_config_runtime=electron npm_config_target=<electron ver>`). M3
> packaging does this automatically and asarUnpacks both addons.

M3 = electron-builder packaging (asarUnpack + ABI-rebuild the native addons),
updater, cutover.
