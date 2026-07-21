/// The `app://` privileged scheme (ADR-055 M1, plan §9 OQ1).
///
/// The renderer is served from a custom `app://termipod/` origin rather than
/// `file://`. Reasons (plan §7 rows 12, §9): a standard, **secure** origin gives
/// the renderer a proper secure context — `crypto.randomUUID`, subtle crypto,
/// service workers all work, deleting the `tauri://` non-secure-context
/// fallbacks — and a single stable origin makes the CSP and future
/// cross-origin rules clean. The scheme serves the same Vite `dist/` the Tauri
/// shell embeds; nothing about the frontend build changes.
///
/// `registerSchemesAsPrivileged` MUST run before `app` is ready, so it fires at
/// module load (this file is imported at the top of main.ts). The file handler
/// is attached per-session after ready via `registerAppScheme`.
import { protocol, net } from 'electron';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { stat } from 'node:fs/promises';

export const APP_SCHEME = 'app';
export const APP_HOST = 'termipod';
export const APP_ORIGIN = `${APP_SCHEME}://${APP_HOST}`;

// Carried over from tauri.conf.json's CSP, with two deliberate changes for the
// Electron shell: `script-src`/`style-src`/etc. are unchanged, but `connect-src`
// drops the Tauri `ipc:`/`http://ipc.localhost` origins and instead allows
// http(s)/ws(s) so the renderer can talk to the (user-configured, arbitrary
// host) hub directly — the renderer-direct transport that replaces the Rust
// `hub_request*`/`hub_sse_*` proxies (plan §7 rows 1–2). `frame-src … drawio:`
// is preserved for the embedded draw.io editor.
const CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline' blob:",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data: blob:",
  "media-src 'self' blob: https:",
  "worker-src 'self' blob:",
  "connect-src 'self' https: http: ws: wss: data: blob:",
  "frame-src 'self' https: http: data: blob: drawio:",
  "object-src 'none'",
  "base-uri 'self'",
].join('; ');

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.wasm': 'application/wasm',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.map': 'application/json',
  '.txt': 'text/plain; charset=utf-8',
};

protocol.registerSchemesAsPrivileged([
  {
    scheme: APP_SCHEME,
    privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true },
  },
]);

async function isFile(p: string): Promise<boolean> {
  try {
    return (await stat(p)).isFile();
  } catch {
    return false;
  }
}

/// Attach the `app://` file handler to a session, serving `distDir`. Resolves
/// requests within `distDir` only (traversal guard); an extension-less miss
/// falls back to `index.html` so the client-side router owns deep links (the
/// app doesn't use URL paths today, but this keeps history routing safe).
export function registerAppScheme(sess: Electron.Session, distDir: string): void {
  const root = path.resolve(distDir);

  sess.protocol.handle(APP_SCHEME, async (req): Promise<Response> => {
    const url = new URL(req.url);
    let rel = decodeURIComponent(url.pathname);
    if (rel === '' || rel === '/') rel = '/index.html';

    let target = path.resolve(root, '.' + rel);
    // Traversal guard: the resolved path must stay inside dist.
    if (target !== root && !target.startsWith(root + path.sep)) {
      return new Response('forbidden', { status: 403 });
    }

    if (!(await isFile(target))) {
      // SPA fallback only for route-shaped paths (no file extension); genuine
      // asset misses stay 404 so a broken bundle path is visible, not masked.
      if (path.extname(target) === '') {
        target = path.join(root, 'index.html');
      } else {
        return new Response('not found', { status: 404 });
      }
    }

    const res = await net.fetch(pathToFileURL(target).toString());
    const headers = new Headers(res.headers);
    const type = MIME[path.extname(target).toLowerCase()];
    if (type !== undefined) headers.set('Content-Type', type);
    headers.set('Content-Security-Policy', CSP);
    return new Response(res.body, { status: 200, headers });
  });
}
