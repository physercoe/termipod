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
src/ipc/sync/core.ts  shared sync decision core (no HTTP): decideBoth (never-
                   delete rule) + willTransfer + enumerateLocalTree + XML/date/
                   encoding helpers. Tested by core.test.ts (node:test) (M2.5a)
src/ipc/sync/webdav.ts folder-tree WebDAV backend (fetch): folder_webdav_verify
                   + folder_webdav_sync — PROPFIND/MKCOL/PUT/GET (M2.5b).
                   webdav_url.ts holds the pure, tested URL helpers
src/ipc/sync/zotero.ts  shared Zotero content-addressing (jszip + md5): zip/unzip,
                   buildProp/parseProp, KEY enumeration. Tested (M2.5c)
src/ipc/sync/webdav_zotero.ts Zotero-flat WebDAV backend: webdav_verify +
                   webdav_sync (zotero/<KEY>.zip + .prop, MD5-addressed) (M2.5c)
src/ipc/sync/sigv4.ts  pure AWS SigV4 signer — validated vs aws4 (M2.5d)
src/ipc/sync/s3.ts  S3 backend: s3_sync/verify (tree) + s3_zotero_sync —
                   ListObjectsV2, path-style, SigV4-signed fetch (M2.5d)
src/ipc/vault.ts   nine vault_* commands → the WASM build of vault-core
                   (../../vault-wasm/pkg), lazy computed-path import (M2.6b)
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
- [x] **M2.5a** shared sync core + fixture tests (`sync/core.ts` +
      `core.test.ts`) — the plan makes the decision logic, not the HTTP, the risk
      of the sync port, so it lands first, isolated and tested: `decideBoth` (the
      never-delete direction rule) + `willTransfer` + `enumerateLocalTree` + the
      dependency-free XML scanners (`elementBlocks`/`extractAll`) + date parsing
      (`daysFromCivil`/`parseHttpDateMs`/`iso8601ToMs`, checked vs `Date.UTC`).
      16 `node:test` cases (Node 22 strips TS natively); `npm test` in CI.
- [x] **M2.5b** folder-tree WebDAV backend (`sync/webdav.ts`, `fetch`) —
      `folder_webdav_verify` + `folder_webdav_sync`, the Obsidian-vault tree
      mirror from `foldersync.rs`. Consumes the tested core; PROPFIND/MKCOL/PUT/
      GET, Basic auth, BFS remote walk, MKCOL-parents, path-traversal-guarded
      download. Pure URL helpers split to `webdav_url.ts` + tested (percent-
      encoding is interop-critical). Proxy arg accepted but not yet applied
      (Node fetch has no per-request proxy — deferred, as M1.5 drawio).
- [x] **M2.5c** Zotero-flat WebDAV backend (`webdav.rs` → `webdav_zotero.ts`) —
      `webdav_verify` + `webdav_sync`, the `zotero/<KEY>.zip` + `.prop` layout,
      MD5-content-addressed (equal hash ⇒ skip; else newest mtime; same
      mtime/diff hash ⇒ conflict). Shared Zotero helpers (`zotero.ts`, `jszip` +
      node `crypto` md5) tested: MD5 vs RFC-1321 vectors, zip round-trip,
      basename-only extraction (no traversal), KEY enumeration.
- [x] **M2.5d** S3 backend (`s3.rs` → `s3.ts`) — `s3_sync` + `s3_sync_verify`
      (tree) and `s3_zotero_sync`, ListObjectsV2 (paginated), path-style. The
      hand-rolled **SigV4 signer** (`sigv4.ts`) is validated in `sigv4.test.ts`
      against **`aws4`** (the canonical Node SigV4 lib, proven against live AWS) —
      matching GET / encoded-path / query / body-hash cases on the minimal signed
      header set. Completes M2.5.
- [x] **M2.6** vault → WASM (completes M2). **M2.6a:** `desktop/vault-core` (pure
      Rust, byte-identical to `vault.rs`) + `desktop/vault-wasm` (wasm-bindgen); a
      `vault-wasm` CI job runs the native crypto tests (incl. NIST AES-GCM KATs
      confirmed vs Node crypto) and builds + round-trip-smoke-tests the WASM —
      **green on the first run**. **M2.6b:** `src/ipc/vault.ts` registers the nine
      `vault_*` commands, lazy-loading the wasm-pack module via a computed-path
      import (opaque to esbuild; no build-time dependency on the artifact).
      Remaining for M3: build the WASM in packaging + copy `pkg/` into the app
      resources (or set `TERMIPOD_VAULT_WASM`); device-test seal/open/wrap.

**M2 COMPLETE.**

## Status — M3 slices (packaging, updater, cutover)

- [x] **M3.1** electron-builder packaging (`electron-builder.yml` +
      `desktop-electron-release.yml`). `npx electron-builder --<os>` produces
      dmg+zip / nsis / AppImage+deb. The native addons (`node-pty`,
      `@napi-rs/keyring`) are ABI-rebuilt for Electron by the default
      `npmRebuild` and **asarUnpacked** (a `.node` can't load from inside the
      asar). The frontend `dist` and the vault-crypto `vault-wasm/pkg` ship as
      **extraResources** (unpacked on disk under `Resources/`), because the
      `app://` handler serves `dist` via `net.fetch(file://)` — Chromium's
      `file://` stack can't read asar virtual paths — and the vault loader
      `import()`s the wasm module by path. Packaged builds resolve both from
      `process.resourcesPath` (`main.ts` `DIST` + `TERMIPOD_VAULT_WASM`,
      `app.isPackaged`-gated). The vault WASM is built once (nodejs target, same
      bytes on every OS) by the workflow's `wasm` job and shared to the three
      `bundle` jobs as an artifact. Signing/notarization consume repo secrets
      when present; absent, the build is unsigned (enough to gate the pipeline).
- [ ] **M3.2** electron-updater — client wiring + the release feed (latest*.yml).
- [ ] **M3.3** first-boot migration cutover — state-v1.json import + keychain
      reader + draw.io re-fetch, verified against a packaged build.
- [ ] **M3.4** signing/notarization certs (maintainer-supplied secrets) + the
      handoff release; retire the Tauri lane after one overlap.

> **Native addons need an Electron-ABI rebuild for the *dev* shell.**
> `@napi-rs/keyring` is Node-API (ABI-stable, works as-is), but `node-pty` builds
> against the Node ABI on `npm install` and must be rebuilt for Electron before
> `electron .` will load it: `npx @electron/rebuild -f -w node-pty` (or install
> with `npm_config_runtime=electron npm_config_target=<electron ver>`).
> Packaging (M3.1) does this automatically and asarUnpacks both addons.
