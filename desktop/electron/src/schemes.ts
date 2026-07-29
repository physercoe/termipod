/// Privileged custom schemes (ADR-055 M1). `registerSchemesAsPrivileged` may be
/// called only ONCE and must run before `app` is ready, so the renderer origin
/// (`app://`), the draw.io asset scheme (`drawio://`) and the media scheme
/// (`termipod-media://`) are all declared here in a single call at module load.
/// The per-session handlers are attached after ready (`registerAppScheme` /
/// `registerDrawioScheme` / `registerMediaScheme`).
import { protocol } from 'electron';

import { MEDIA_SCHEME } from './media_policy';

export { MEDIA_SCHEME };

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
  {
    // `stream: true` is what makes <video> issue range requests against it and
    // treat the resource as seekable; without it the player can only ever play
    // from the start. No corsEnabled: nothing needs to fetch() this
    // cross-origin, and the <video> element does not require it.
    scheme: MEDIA_SCHEME,
    privileges: { standard: true, secure: true, stream: true },
  },
]);
