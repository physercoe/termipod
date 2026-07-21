/// Offline draw.io editor (ADR-055 M1.5) — port of `src-tauri/src/drawio.rs` +
/// the `drawio://` scheme registered in lib.rs.
///
/// draw.io is Apache-2.0, fully client-side, ~50 MB — not bundled. The user
/// downloads the official `draw.war` (a ZIP whose root IS the static webapp)
/// once; we extract it into a **version-keyed** app-data dir (survives updates,
/// never re-downloaded) and serve it to an in-app iframe via the privileged
/// `drawio://` scheme so relative asset URLs resolve offline. Path traversal is
/// guarded on serve. Commands: `drawio_status` / `drawio_download` /
/// `drawio_install_file`.
import { app, net } from 'electron';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { mkdir, realpath, rename, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import extract from 'extract-zip';
import type { Ctx, Handler } from './ipc/dispatch';
import { openDialog } from './ipc/dialogs';
import { DRAWIO_SCHEME } from './schemes';

const DRAWIO_VERSION = 'v30.3.6';
const DRAWIO_WAR_URL = 'https://github.com/jgraph/drawio/releases/download/v30.3.6/draw.war';

interface DrawioStatus {
  installed: boolean;
  version: string;
}

function drawioRoot(): string {
  // Version-keyed so a new pinned version never forces a re-download.
  return path.join(app.getPath('userData'), 'drawio', DRAWIO_VERSION);
}

async function isFile(p: string): Promise<boolean> {
  return (await stat(p).catch(() => null))?.isFile() ?? false;
}

async function statusOf(root: string): Promise<DrawioStatus> {
  return { installed: await isFile(path.join(root, 'index.html')), version: DRAWIO_VERSION };
}

/// Extract a draw.war (ZIP) at `warPath` into the version-keyed root. Staging +
/// rename so a partial extract never leaves a root that `status` calls installed.
/// The `.war` root is the webapp; the Java `WEB-INF/`/`META-INF/` dirs are left
/// in place but never served (the traversal-guarded scheme only reads what the
/// iframe requests). Mirrors `install_war_bytes`.
async function installWar(root: string, warPath: string): Promise<DrawioStatus> {
  const staging = `${root}.part`;
  await rm(staging, { recursive: true, force: true });
  await mkdir(staging, { recursive: true });
  try {
    await extract(warPath, { dir: staging });
  } catch (e) {
    await rm(staging, { recursive: true, force: true });
    throw new Error(`not a valid draw.war (ZIP) file: ${e instanceof Error ? e.message : String(e)}`);
  }
  if (!(await isFile(path.join(staging, 'index.html')))) {
    await rm(staging, { recursive: true, force: true });
    throw new Error('draw.war has no index.html at its root — is this the webapp .war?');
  }
  await rm(root, { recursive: true, force: true });
  await mkdir(path.dirname(root), { recursive: true });
  await rename(staging, root);
  return statusOf(root);
}

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.htm': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json',
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
  '.eot': 'application/vnd.ms-fontobject',
  '.xml': 'application/xml',
  '.txt': 'text/plain; charset=utf-8',
  '.wasm': 'application/wasm',
};

/// Serve one extracted draw.io file for the `drawio://` scheme, traversal-guarded
/// to the version-keyed root (port of `drawio::serve`). Attach the drawio scheme
/// handler to a session (call after app ready).
export function registerDrawioScheme(sess: Electron.Session): void {
  sess.protocol.handle(DRAWIO_SCHEME, async (req): Promise<Response> => {
    try {
      const root = drawioRoot();
      const url = new URL(req.url);
      let rel = decodeURIComponent(url.pathname).replace(/^\/+/, '');
      if (rel === '') rel = 'index.html';
      const canonRoot = await realpath(root);
      const canonFull = await realpath(path.join(root, rel));
      if (canonFull !== canonRoot && !canonFull.startsWith(canonRoot + path.sep)) {
        return new Response('forbidden', { status: 403 });
      }
      const res = await net.fetch(pathToFileURL(canonFull).toString());
      const headers = new Headers(res.headers);
      const type = MIME[path.extname(canonFull).toLowerCase()];
      if (type !== undefined) headers.set('Content-Type', type);
      return new Response(res.body, { status: 200, headers });
    } catch {
      return new Response('not found', { status: 404 });
    }
  });
}

export const drawioHandlers: Record<string, Handler> = {
  drawio_status: async (): Promise<DrawioStatus> => statusOf(drawioRoot()),

  drawio_download: async (args): Promise<DrawioStatus> => {
    const root = drawioRoot();
    if (await isFile(path.join(root, 'index.html'))) return statusOf(root);
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : undefined;
    // Node's fetch follows the GitHub release-asset redirect to the CDN; identify
    // a UA so no proxy rejects a header-less request. (Proxy support: M1.2/M4 —
    // `session.resolveProxy`; the arg is accepted now for contract parity.)
    void proxy;
    let resp: Response;
    try {
      resp = await fetch(DRAWIO_WAR_URL, { headers: { 'user-agent': 'termipod-desktop' }, redirect: 'follow' });
    } catch (e) {
      throw new Error(
        `could not reach the draw.io download (${e instanceof Error ? e.message : String(e)}). Download draw.war manually and use "Install from file".`,
      );
    }
    if (!resp.ok) throw new Error(`draw.io download failed: HTTP ${resp.status}`);
    const bytes = Buffer.from(await resp.arrayBuffer());
    const tmp = path.join(tmpdir(), `termipod-drawio-${DRAWIO_VERSION}.war`);
    await writeFile(tmp, bytes);
    try {
      return await installWar(root, tmp);
    } finally {
      await rm(tmp, { force: true });
    }
  },

  drawio_install_file: async (_args, ctx: Ctx): Promise<DrawioStatus | null> => {
    const res = await openDialog(ctx.win, {
      properties: ['openFile'],
      filters: [{ name: 'draw.io webapp', extensions: ['war', 'zip'] }],
    });
    if (res.canceled || res.filePaths.length === 0) return null;
    // extract-zip needs a real path with a zip-y name; the picked .war is fine.
    return installWar(drawioRoot(), res.filePaths[0]);
  },
};
