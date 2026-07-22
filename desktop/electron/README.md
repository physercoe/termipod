# TermiPod Electron shell (ADR-055 M1)

The Electron shell — the sole desktop shell since the M3.4 cutover retired Tauri
(see [`docs/plans/desktop-electron-migration.md`](../../docs/plans/desktop-electron-migration.md)).
It wraps the **same** Vite build produced under `../dist`; the frontend talks to
it through the runtime-agnostic `../src/bridge/`. Injecting
`window.__ELECTRON_BRIDGE__` from the preload is what flips that bridge from its
browser degrade path onto the Electron path — no frontend call site changes.

This is a **self-contained package** (its own `package.json` / `node_modules`)
so the frontend build's `npm ci` in `desktop/` never installs Electron.

## Layout

```
src/main.ts        single BrowserWindow, app:// scheme, IPC dispatch, lifecycle
src/preload.ts     sandboxed bridge → window.__ELECTRON_BRIDGE__ {invoke, listen}
src/appscheme.ts   app:// privileged scheme serving ../dist (secure origin + CSP)
src/events.ts      main→renderer event fan-out + subscribe-gate bookkeeping
src/ipc/dispatch.ts  command handler map = the allowlist (successor of capabilities/default.json)
assets/icon.{png,icns,ico}  app icon — png for the dev dock/window, icns/ico wired into electron-builder.yml for the packaged bundles
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

## Tests

- **`npm test`** — the sync-core fixture suite (`node --test 'src/**/*.test.ts'`).
- **`npm run test:e2e`** — Playwright drives the **real Electron app** (ADR-055
  §7 row 14; specs in `e2e/`, config in `playwright.config.ts`). Needs the
  Electron binary + a display, so it runs in CI under xvfb (`.github/workflows/desktop.yml`
  → the `e2e` job); there is no local Electron binary on the dev host. It launches
  `out/main.cjs` unpackaged (loading the frontend from `../dist` via
  `TERMIPOD_DIST`) and covers:
  - **Smoke** — the window boots + paints, the preload bridge is injected and
    `app_version` round-trips, and the renderer is a secure `app://` context
    (`crypto.randomUUID` available).
  - **Terminal** — a real local shell over node-pty: `pty_open`→`start`→`write`
    and the `pty-data` byte stream carries the command output (the layer the
    base64→bytes IPC paydown will change), plus a UI check that opening a local
    shell mounts an xterm screen.
  - **draw.io** — `drawio_status` round-trips (not-installed in CI; the full
    ~50 MB iframe embed isn't downloaded).
  - **Figure export** — Chromium rasterizes an SVG to PNG via canvas (the half of
    `save_image_as` that isn't the native save dialog).

  The terminal test spawns node-pty, a native addon whose ABI must match Electron
  (not system Node), so the CI job **rebuilds it with plain node-gyp** (NOT
  `@electron/rebuild` — it deadlocks, issue #358; same recipe as the release
  workflow). `@napi-rs/keyring` is N-API prebuilt and needs no rebuild.

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
- [x] **M3.2** electron-updater — main-process `updater_check`/`_download`/
      `_install` + `app_version` (`ipc/updater.ts`); `bridge/updater.ts`
      synthesizes the Tauri `Update` shape (its `downloadAndInstall` translates
      `updater:progress` events into the plugin's callback) so
      `UpdateSection.tsx` is untouched. Feed = the electron-builder `publish`
      config baked into `app-update.yml`; the workflow stamps the real product
      version before packaging. No-ops off a packaged build.
- [x] **M3.3** first-boot migration cutover. The state + secret handoffs were
      pulled forward and are packaged-safe (they resolve via `app.getPath`):
      `migration_read` falls back to the Tauri app-data dir (#353) and
      `keychain_get` lazily reads Tauri keychain items (device-passed, M1.3).
      This slice adds the draw.io leg — `drawio_status`/`_download` adopt the
      Tauri install's already-extracted `drawio/<version>` (one-time
      staging+rename copy) instead of forcing a ~50 MB re-download at cutover.
- [~] **M3.4** cutover mechanics wired; execution is maintainer-gated (certs +
      an explicit release + promote). This repo hosts three release lanes, and
      GitHub's `releases/latest` belongs to the **mobile** lane (its in-app
      checker reads it), so the desktop feed avoids it entirely:
      `desktop-electron-release.yml` builds on an `electron-v*` tag and creates
      a **prerelease** on that same tag (never a new `v*` tag — that's the
      mobile workflows' trigger namespace); packaged apps poll a `generic`
      electron-updater feed at the rolling `electron-latest` release, which the
      workflow's `promote` dispatch points at a verified version. The M0.3
      `handoff.json` mechanism was dropped by decision (2026-07-21): its URL is
      baked to `releases/latest`, which mobile owns, so it can never resolve —
      and the Tauri install base is small enough to download manually from the
      releases page (`checkHandoff()` in shipped Tauri builds stays dormant).
      Signing/notarization consume repo secrets (below); unsigned builds still
      package. Not done here: supplying the certs, cutting + promoting the
      release, and retiring the Tauri lane — see the runbook.

## Cutover runbook (M3.4)

Signing/notarization needs certs only the maintainer holds, and cutting a
release is an explicit action, so this is a procedure, not an automated step.

**1. Configure signing secrets** (once). electron-builder reads them from the
release workflow's env:

| Secret | For | Notes |
|---|---|---|
| `CSC_LINK` | macOS | base64 (or URL) of the Developer ID `.p12` cert |
| `CSC_KEY_PASSWORD` | macOS | the cert password |
| `WIN_CSC_LINK` | Windows | base64 (or URL) of the Authenticode `.pfx` cert |
| `WIN_CSC_KEY_PASSWORD` | Windows | the cert password |
| `APPLE_ID` | macOS notarization | Apple ID email |
| `APPLE_APP_SPECIFIC_PASSWORD` | macOS notarization | app-specific password |
| `APPLE_TEAM_ID` | macOS notarization | Apple Developer team id |

`gh secret set CSC_LINK < cert.b64` etc. Absent, the workflow still builds
**unsigned** installers (auto-update won't verify — signing is the cutover
prerequisite). `CSC_IDENTITY_AUTO_DISCOVERY=false` is set so an absent cert
never hangs the macOS runner on the keychain.

**2. Release.** Bump the product version (`desktop/package.json`; the workflow
stamps it into the electron package) and push an `electron-v<version>` tag. The
matrix builds + signs, then the `release` job creates a **prerelease** GitHub
release on that tag holding the installers + `latest*.yml`. (Prerelease is
deliberate — it keeps the release out of `releases/latest`, which the mobile
in-app update checker and the still-overlapping Tauri updater both read.)

**3. Verify, then promote.** Download the release's installers and smoke-test
each OS. To go live, run the workflow with `promote=<version>`: it copies that
release's assets onto the rolling `electron-latest` release — the fixed
`generic` feed URL baked into packaged apps — replacing whatever was there.
Auto-update N→N+1 verifies as: install version N, promote N+1, check for
updates in Settings. Rollback = promote the previous version again.

**4. Retire the Tauri lane — DONE (2026-07-22).** `desktop/src-tauri/`, the
`desktop-release.yml` / `desktop-v*` lane, the `tauri` CI job, and the
`@tauri-apps/*` frontend deps are removed; `../src/bridge/` is Electron +
browser only. Tauri users migrate by downloading an installer from the releases
page (the automated `handoff.json` prompt was dropped — see M3.4 above).

> **Native addons need an Electron-ABI rebuild for the *dev* shell.**
> `@napi-rs/keyring` is Node-API (ABI-stable, works as-is), but `node-pty` builds
> against the Node ABI on `npm install` and must be rebuilt for Electron before
> `electron .` will load it: `npx @electron/rebuild -f -w node-pty` (or install
> with `npm_config_runtime=electron npm_config_target=<electron ver>`).
> Packaging (M3.1) does this automatically and asarUnpacks both addons.
