# TermiPod desktop control plane

The unified web-tech client shell — WS2 of
[`../docs/plans/desktop-control-plane.md`](../docs/plans/desktop-control-plane.md)
([ADR-051](../docs/decisions/051-desktop-client-stack.md)). A **React +
TypeScript** frontend that ships in an **Electron** shell
([`electron/`](electron/README.md), [ADR-055](../docs/decisions/055-desktop-electron-shell.md))
and also runs as a plain-browser build. The original Tauri v2 shell was retired
at the M3.4 cutover; the frontend is shell-agnostic (every native call funnels
through `src/bridge/`).

## Layout

```
desktop/
  src/               React + TS frontend (browser-target, fully buildable here)
    bridge/          runtime shell bridge — invoke/listen/updater (Electron | browser)
    hub/             typed hub SDK — transport, sse, client facade (mirrors hub_client.dart)
    state/           zustand session store
    surfaces/        work surfaces
    ui/              AppShell (3-region mission control), ConnectPanel, CommandPalette
    styles/          app.css (+ generated tokens.css from design-tokens/, WS1)
  electron/          Electron shell — main/preload, native IPC ports, packaging (see electron/README.md)
  vault-core/        Rust vault crypto (native tests)
  vault-wasm/        vault-core compiled to WASM for the Electron shell
```

## Develop

```bash
cd desktop
npm ci
npm run dev        # Vite dev server (browser); http://localhost:5173
npm run build      # sync tokens + typecheck + production build
npm run typecheck  # tsc --noEmit
```

The frontend builds fully here. The Electron shell (native binary, packaging)
is built in CI (`.github/workflows/desktop.yml` gates it; there is no local
Electron/Rust toolchain on this dev host). To run the shell locally where the
Electron binary is available, see [`electron/README.md`](electron/README.md).

## Installers

Bundled installers (Linux `.AppImage`/`.deb`, macOS `.dmg`, Windows `.exe`) are
produced by `.github/workflows/desktop-electron-release.yml`:

- **On demand:** GitHub → Actions → *Desktop Electron Release* → *Run workflow*.
  The installers appear as run artifacts.
- **Tagged:** push an `electron-v<version>` tag to build all OSes and create the
  matching prerelease with installers + `latest*.yml`.
- **Go live:** *Run workflow* with `promote=<version>` copies that release's
  assets onto the rolling `electron-latest` generic feed (the auto-update
  source). Promotion is the go-live switch; it is gated on signing.

## Status (WS2–WS8)

Done:
- **WS2** — app shell (3-region layout), typed hub SDK + fetch transport,
  fetch-based SSE reader (auth-header capable — no `EventSource` limitation), the
  audit console, ⌘K command-palette shell, shared-token theming (WS1).
- **WS3** — fleet Navigator (hosts ▸ agents tree + status dots), persistent
  status bar, single-agent lifecycle (pause/resume/stop/terminate/archive).
- **WS4** — agent transcript over the SSE stream (`tail` backfill + `seq` cursor)
  with a composer (`POST /input`) and a digest tab.
- **WS5** — always-visible approvals dock: per-kind attention cards
  (permission_prompt / propose+override / help_request / generic) driving
  `POST /attention/{id}/decide`.
- **WS6** — Projects section in the Navigator + a tabbed project surface:
  **Overview** (phase track + deliverables/counts), **Tasks** kanban (ADR-029
  statuses) with a **task-detail modal that patches status/priority**
  (`PATCH …/tasks/{task}`), **Runs**, and **Plans**.
- **WS7** — Admin & Governance overlay: Team members + **editable policy**
  (`PUT /policy`, raw YAML) · admin **Hosts** (ping/restart/update/shutdown) ·
  admin **Agents** (kill) · **Teams** (rotate-token) · **Upkeep** (DB vacuum,
  host-token rotation), destructive actions gated by a two-click confirm and
  freshly-minted tokens shown once for copy.
- **Terminal** — breakglass **SSH terminal** ([ADR-052](../docs/decisions/052-breakglass-ssh-and-key-vault.md),
  personal direct-SSH path): xterm.js in the renderer + a `russh` PTY transport
  in the Electron main process (`electron/src/ipc/ssh.ts`). Password or
  private-key auth; keys held in memory for the session only, never sent to the
  hub. Desktop-only (the browser build shows a "desktop app only" notice).
- **WS8 packaging** — Electron installers via `desktop-electron-release.yml`
  (see above), with `electron-updater` auto-update and a first-boot migration
  that imports state + secrets from a previous Tauri install.
- **Shell** — Settings overlay (titlebar + ⌘K): **light / dark / system themes**
  (semantic CSS vars over the shared light+dark tokens, persisted) and **English /
  中文** i18n (`src/i18n/`, persisted, English fallback) across all UI strings.

## Updating

The app self-updates via **electron-updater**: **Settings → Software update →
Check for updates** polls the rolling `electron-latest` generic feed
(`releases/download/electron-latest/latest*.yml`), then downloads, installs, and
relaunches. macOS uses the `.zip` (Squirrel.Mac) alongside the `.dmg`; Windows
uses the NSIS `Setup.exe` + `latest.yml`.

**Signing.** Auto-update is gated on code-signing: macOS Squirrel rejects
unsigned updates and Windows SmartScreen flags unsigned installers, so until the
certs land the current build is **manual-install** (download the installer from
the GitHub release page). CI signs when the secrets are present:

```bash
# macOS Developer ID cert (base64 .p12) + password; notarization via APPLE_* :
gh secret set CSC_LINK              --repo <owner>/<repo> < cert.p12.base64
gh secret set CSC_KEY_PASSWORD      --repo <owner>/<repo> < cert-password.txt
# Windows Authenticode cert (base64 .pfx) + password:
gh secret set WIN_CSC_LINK          --repo <owner>/<repo> < win-cert.pfx.base64
gh secret set WIN_CSC_KEY_PASSWORD  --repo <owner>/<repo> < win-cert-password.txt
```

**Behind a corporate proxy.** The updater and the sync/download transports honour
a system proxy: the Electron main resolves it (`system_proxy` — env vars, then
`session.resolveProxy`, which reads the Windows registry / PAC), and the frontend
passes it to `checkUpdate({ proxy })` and the sync backends (undici `ProxyAgent`).
**Settings → Software update → Proxy settings** shows what was detected and lets
you override it. The first build carrying an update fix must be installed manually
once; in-app updates work from then on (once signing is live).

## Notes

- **Hub calls fetch directly under Electron.** The Electron main injects the
  bearer token via `session.webRequest` and applies any system proxy, so
  `HubTransport` and `streamSse` `fetch` the hub directly from the renderer (no
  Rust proxy). The plain-browser build also uses `fetch`. (Native command calls
  route through the preload bridge `window.__ELECTRON_BRIDGE__`; see `src/bridge/`.)
- **The shell renders without a connection.** The connect form is a dismissable
  overlay; the SSH terminal and Settings work offline, and hub-backed surfaces
  show empty states until you connect.
- `src/styles/tokens.css` is generated from `../design-tokens/` — do not edit;
  run `npm run sync:tokens`.
