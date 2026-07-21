/// Privileged custom schemes (ADR-055 M1). `registerSchemesAsPrivileged` may be
/// called only ONCE and must run before `app` is ready, so both the renderer
/// origin (`app://`) and the draw.io asset scheme (`drawio://`) are declared here
/// in a single call at module load. The per-session file handlers are attached
/// after ready (`registerAppScheme` / `registerDrawioScheme`).
import { protocol } from 'electron';

export const APP_SCHEME = 'app';
export const APP_HOST = 'termipod';
export const APP_ORIGIN = `${APP_SCHEME}://${APP_HOST}`;

export const DRAWIO_SCHEME = 'drawio';

protocol.registerSchemesAsPrivileged([
  {
    scheme: APP_SCHEME,
    privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true },
  },
  {
    // Served to an in-app iframe; draw.io runs its own JS/WASM under this origin.
    scheme: DRAWIO_SCHEME,
    privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true },
  },
]);
