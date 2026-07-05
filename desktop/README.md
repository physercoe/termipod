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
repo's dev host has no Rust. To run it locally where cargo is available:
`npm i -g @tauri-apps/cli && npm run tauri dev`.

## Status (WS2)

Done: app shell (3-region layout), typed hub SDK + fetch transport, fetch-based
SSE reader (auth-header capable — no `EventSource` limitation), one read-only
surface (audit console over REST + TanStack Query), ⌘K command-palette shell,
shared-token theming (WS1), and the minimal Tauri Rust core (`hub_request`
proxy). Next: fleet navigator (WS3), transcript reader (WS4), approvals dock
(WS5); Rust keychain token storage + SSE proxy (WS8).

## Notes

- The token is held in memory (browser build); `src-tauri`'s `hub_request` is the
  path to keep it out of the webview under Tauri (keychain storage is WS8).
- `src/styles/tokens.css` is generated from `../design-tokens/` — do not edit;
  run `npm run sync:tokens`.
