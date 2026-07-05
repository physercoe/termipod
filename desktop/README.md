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
- **WS6** — Projects section in the Navigator + a tasks kanban board (ADR-029
  statuses) in the Focus region.
- **WS8 packaging** — installers via `desktop-release.yml` (see below).

Next: team/admin cockpits (WS7); project overview/runs panes + task detail; Rust
keychain + SSE proxy; multi-select bulk ops; split-pane transcripts.

## Notes

- The token is held in memory (browser build); `src-tauri`'s `hub_request` is the
  path to keep it out of the webview under Tauri (keychain storage is WS8).
- `src/styles/tokens.css` is generated from `../design-tokens/` — do not edit;
  run `npm run sync:tokens`.
