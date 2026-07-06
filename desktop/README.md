# TermiPod desktop control plane

The unified web-tech client shell — WS2 of
[`../docs/plans/desktop-control-plane.md`](../docs/plans/desktop-control-plane.md)
([ADR-051](../docs/decisions/051-desktop-client-stack.md)). A **Tauri v2 + React +
TypeScript** app; the same frontend also runs as a plain-browser build.

## Layout

```
desktop/
  src/               React + TS frontend (browser-target, fully buildable here)
    hub/             typed hub SDK — transport, sse, client facade (mirrors hub_client.dart)
    state/           zustand session store
    surfaces/        work surfaces (WS2: AuditConsole)
    ui/              AppShell (3-region mission control), ConnectPanel, CommandPalette
    styles/          app.css (+ generated tokens.css from design-tokens/, WS1)
  src-tauri/         Tauri v2 Rust core (shell + hub_request proxy); compiled in CI
```

## Develop

```bash
cd desktop
npm ci
npm run dev        # Vite dev server (browser); http://localhost:5173
npm run build      # sync tokens + typecheck + production build
npm run typecheck  # tsc --noEmit
```

The desktop (Tauri) shell needs a Rust toolchain + the platform webview
libraries; it is compiled in CI (`.github/workflows/desktop.yml`) since this
repo's dev host has no Rust. To run/bundle it locally where cargo is available:
`npm run tauri dev` / `npm run tauri build`.

## Installers

Bundled installers (Linux `.deb`/`.rpm`/`.AppImage`, macOS universal `.dmg`,
Windows `.msi`/`.exe`) are produced by `.github/workflows/desktop-release.yml`:

- **On demand:** GitHub → Actions → *Desktop Release* → *Run workflow*. The
  installers appear as run artifacts.
- **Tagged:** push a `desktop-v*` tag to also attach them to a draft GitHub
  release.

Builds are unsigned (fine for internal testing).

## Status (WS2–WS4)

Done:
- **WS2** — app shell (3-region layout), typed hub SDK + fetch transport,
  fetch-based SSE reader (auth-header capable — no `EventSource` limitation), the
  audit console, ⌘K command-palette shell, shared-token theming (WS1), and the
  minimal Tauri Rust core (`hub_request` proxy).
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
  personal direct-SSH path): xterm.js in the webview + a `russh` PTY transport
  in the Tauri Rust core (`src-tauri/src/ssh.rs`). Password or private-key auth;
  keys held in memory for the session only, never sent to the hub. Desktop-only
  (the browser build shows a "desktop app only" notice). The managed-host
  hub-brokered PTY (D-2 Path 1 / D-6) and the zero-knowledge key vault (D-4)
  remain future workstreams.
- **WS8 packaging** — installers via `desktop-release.yml` (see below).
- **Shell** — Settings overlay (titlebar + ⌘K): **light / dark / system themes**
  (semantic CSS vars over the shared light+dark tokens, persisted) and **English /
  中文** i18n (`src/i18n/`, persisted, English fallback) across all UI strings.

Next: Rust keychain + SSE proxy; multi-select bulk ops; split-pane transcripts;
managed-host hub-brokered PTY (ADR-052 D-6) + the zero-knowledge key vault
(D-4); host-key pinning for the personal SSH path.

## Updating

The app self-updates via the Tauri updater plugin: **Settings → Software update →
Check for updates** queries the signed `latest.json` on the latest GitHub release,
then downloads, installs, and relaunches. Updates are **code-signed** — the public
key is in `src-tauri/tauri.conf.json` (`plugins.updater.pubkey`); CI signs each
bundle with the matching private key.

**One-time signing setup** (required before the next release tag, else bundling
fails on signing):

```bash
# Set the two repo secrets from the generated keypair (values piped from files):
gh secret set TAURI_SIGNING_PRIVATE_KEY          --repo <owner>/<repo> < private.key
gh secret set TAURI_SIGNING_PRIVATE_KEY_PASSWORD --repo <owner>/<repo> < password.txt
```

Releases are now **published** (not draft) so `releases/latest/download/latest.json`
resolves. Rotating the key = regenerate (`npm run tauri signer generate`), replace
the `pubkey` in `tauri.conf.json`, and reset both secrets.

**Behind a corporate proxy.** The updater fetches from GitHub via reqwest, which
honours proxy *environment variables* but not the Windows *system-proxy registry*
— so on an intranet the check fails with `error sending request for url …`. The
`system_proxy` Rust command resolves a proxy (env vars, then the Windows
`Internet Settings` registry) and the frontend passes it to `check({ proxy })`,
which the plugin applies to both the check and the download. **Settings → Software
update → Proxy settings** shows what was detected and lets you override it (needed
for PAC/auto-config scripts, which can't be read). Because the *updater itself* is
what this fixes, the first build carrying it must be installed manually once
(download the installer from the GitHub release page in a browser, which uses the
system proxy); in-app updates work from then on.

## Notes

- **Hub calls route through the Rust core under Tauri.** The webview origin is
  `tauri://localhost`, so a direct `fetch` to the hub is cross-origin and the hub
  sends no CORS headers ("Failed to fetch"). `HubTransport` therefore proxies REST
  through `hub_request`, and `streamSse` proxies the live SSE streams through
  `hub_sse_open`/`hub_sse_close` (the core pipes bytes back as `hub-sse` events).
  The plain-browser build still uses `fetch` directly. This also keeps the bearer
  token out of the webview (keychain storage is a later WS8 item).
- **The shell renders without a connection.** The connect form is a dismissable
  overlay; the SSH terminal and Settings work offline, and hub-backed surfaces
  show empty states until you connect.
- `src/styles/tokens.css` is generated from `../design-tokens/` — do not edit;
  run `npm run sync:tokens`.
