import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import http from 'node:http';
import protobuf from 'protobufjs';
import type { AddressInfo } from 'node:net';

// The preload injects this on the renderer's `window` (see src/preload.ts). It
// is declared in the frontend package, not here, so mirror the minimal surface
// these tests touch.
declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: {
      invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T>;
      listen<T>(name: string, cb: (e: { payload: T }) => void): Promise<() => void>;
    };
  }
}

/// Smoke suite for the Electron shell (ADR-055 §7 row 14).
///
/// Proves the harness works end-to-end and pins the invariants the M4 paydowns
/// rely on:
///   - the app boots and paints (no blank/black screen — the WebView2 render
///     bugs the guards compensated for do not exist under Chromium),
///   - the preload bridge is injected and a native command round-trips,
///   - the renderer is a secure `app://` context (so `crypto.randomUUID` is
///     available — the assumption behind §7 row 12).
///
/// Runs UNPACKAGED: launch `out/main.cjs`, which loads the frontend from
/// `desktop/dist` (`TERMIPOD_DIST`). Native addons (node-pty, keyring) are
/// lazily imported, so boot needs no ABI rebuild — a terminal/SSH suite that
/// exercises them would (add `electron-builder install-app-deps` then).

// Playwright runs test files as CommonJS (the electron package is not ESM), so
// `__dirname` is the e2e/ dir.
const MAIN_ENTRY = path.resolve(__dirname, '..', 'out', 'main.cjs');
const DIST_DIR = path.resolve(__dirname, '..', '..', 'dist');

// `--no-sandbox` (the Chromium setuid sandbox can't run in an unprivileged CI
// container) and `--disable-gpu` (xvfb has no real GPU — force SwiftShader).
const CI_FLAGS = ['--no-sandbox', '--disable-gpu'];

let app: ElectronApplication;
let page: Page;

test.beforeAll(async () => {
  app = await electron.launch({
    args: [...CI_FLAGS, MAIN_ENTRY],
    env: { ...process.env, TERMIPOD_DIST: DIST_DIR, TERMIPOD_E2E: '1' },
  });
  page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
});

test.afterAll(async () => {
  await app?.close();
});

test.afterEach(async ({}, testInfo) => {
  // A screenshot on failure is the only forensic signal for a CI-only run.
  if (testInfo.status !== testInfo.expectedStatus && page) {
    await testInfo.attach('screenshot', { body: await page.screenshot(), contentType: 'image/png' });
  }
});

// The "Add a hub" connect modal auto-opens once when `init()` settles offline
// (AppShell.tsx) — at a non-deterministic time, so it can pop up during any late
// test and its backdrop then blocks clicks. Dismiss it by CLICKING its close
// button (Escape is unreliable through the focus trap) in a `toPass` loop that
// absorbs the open/animation race; a no-op when the modal is absent (count 0).
// Same pattern as the excalidraw smoke below.
async function dismissConnectModal(): Promise<void> {
  await expect(async () => {
    const closeBtn = page.locator('.connect .connect-head button');
    if ((await closeBtn.count()) > 0) await closeBtn.click({ timeout: 2000 });
    await expect(page.locator('.connect')).toHaveCount(0);
  }).toPass({ timeout: 15_000 });
}

test('window opens with the app title', async () => {
  expect(await page.title()).toContain('TermiPod');
});

test('the preload bridge is injected and a native command round-trips', async () => {
  const hasBridge = await page.evaluate(() => typeof window.__ELECTRON_BRIDGE__ !== 'undefined');
  expect(hasBridge).toBe(true);
  // `app_version` is a native command — the round-trip through the bridge is what
  // this asserts. The VALUE is `app.getVersion()`: the CalVer only in a packaged
  // build; unpackaged (as here) it's Electron's own version. Either is semver-shaped.
  const version = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('app_version'));
  expect(version).toMatch(/^\d+\.\d+\.\d+/);
});

test('renderer is a secure context — crypto.randomUUID works (§7 row 12)', async () => {
  expect(await page.evaluate(() => window.isSecureContext)).toBe(true);
  const uuid = await page.evaluate(() => crypto.randomUUID());
  expect(uuid).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test('the app shell renders (no blank screen)', async () => {
  const root = page.locator('#root');
  await expect(root).toBeAttached();
  // The React tree mounted at least one node — a blank/black-screen render
  // (the WebView2 failure class M4 pays down) would leave #root empty.
  await expect.poll(async () => root.locator('> *').count(), { timeout: 15_000 }).toBeGreaterThan(0);
});

// ── Terminal flow ──────────────────────────────────────────────────────────
// A real local shell over node-pty. This is the layer the M4 base64→bytes IPC
// paydown (§7 row 4 / §6 row 6) will change, so pin the whole PTY round-trip:
// open → start → write → the `pty-data` byte stream carries the command output.
// (Requires node-pty rebuilt for the Electron ABI — the CI job does that with
// plain node-gyp; see desktop.yml.)
test('terminal: a local PTY round-trips through the bridge', async () => {
  const output = await page.evaluate(async () => {
    const b = window.__ELECTRON_BRIDGE__!;
    const { id } = await b.invoke<{ id: string; shell: string }>('pty_open', { req: { cols: 80, rows: 24 } });
    let text = '';
    // `pty-data` carries raw bytes (a Buffer → Uint8Array over structured clone),
    // NOT base64 — PTY is already bytes end-to-end.
    const un = await b.listen<{ id: string; bytes: ArrayLike<number> }>('pty-data', (e) => {
      if (e.payload.id === id) text += new TextDecoder().decode(new Uint8Array(e.payload.bytes));
    });
    await b.invoke('pty_start', { id }); // flushes buffered output; gates the reader
    await b.invoke('pty_write', { id, data: 'echo E2E_PTY_OK_MARKER\n' });
    await new Promise((r) => setTimeout(r, 3000));
    un();
    await b.invoke('pty_close', { id });
    return text;
  });
  expect(output).toContain('E2E_PTY_OK_MARKER');
});

test('terminal UI: opening a local shell mounts an xterm screen', async () => {
  // Ctrl+8 → the Terminal surface (AppShell command hint). The panel is
  // always-mounted but CSS-hidden until active.
  await page.keyboard.press('Control+8');
  // On boot with no hub configured the "Add a hub" connect modal is open; its
  // backdrop intercepts clicks on the surface, so dismiss it first (its close
  // button lives in the modal head). Conditional — it isn't always present.
  const connectClose = page.locator('.connect-head button');
  if (await connectClose.isVisible().catch(() => false)) {
    await connectClose.click();
    await expect(page.locator('.connect')).toHaveCount(0);
  }
  await page.locator('.term-add-btn').first().click(); // the "+" new-session menu
  await page.locator('.term-add-menu button').first().click(); // "Local shell"
  // xterm mounted its screen — proves the UI PTY path renders without crashing
  // (the black-screen render class doesn't reproduce under Chromium). We assert
  // the element, not its text: xterm paints to canvas/WebGL, not the DOM.
  await expect(page.locator('.xterm').first()).toBeVisible({ timeout: 15_000 });
});

// ── draw.io embed ──────────────────────────────────────────────────────────
// The offline draw.io webapp (~50 MB) is not bundled, so in CI it is not
// installed — a full iframe-embed test would need the download + a diagram doc.
// Pin the command family the surface drives: `drawio_status` round-trips and
// reports the not-installed state (which is what DiagramEditor renders its
// download CTA from).
test('draw.io: drawio_status round-trips (not installed in CI)', async () => {
  const status = await page.evaluate(() =>
    window.__ELECTRON_BRIDGE__!.invoke<{ installed: boolean; version: string }>('drawio_status'),
  );
  expect(status.installed).toBe(false);
  expect(typeof status.version).toBe('string');
});

// ── Figure export ──────────────────────────────────────────────────────────
// PNG export (`save_image_as`) rasterizes the rendered figure SVG to a canvas in
// the renderer, then hands the bytes to a native save (a dialog — not headlessly
// drivable). Pin the Chromium-behaviour half the export depends on: SVG → <img>
// → canvas → PNG. This is exactly the rasterization WebKit mishandled (the row-3
// sizedSvg concern); Chromium does it cleanly.
test('figure export: Chromium rasterizes an SVG to a PNG via canvas', async () => {
  const png = await page.evaluate(async () => {
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64">' +
      '<rect width="64" height="64" fill="#123456"/><text x="6" y="36" fill="#fff">E2E</text></svg>';
    const img = new Image();
    await new Promise<void>((res, rej) => {
      img.onload = () => res();
      img.onerror = () => rej(new Error('svg image load failed'));
      img.src = 'data:image/svg+xml;base64,' + btoa(svg);
    });
    const canvas = document.createElement('canvas');
    canvas.width = 64;
    canvas.height = 64;
    canvas.getContext('2d')!.drawImage(img, 0, 0);
    return canvas.toDataURL('image/png'); // throws if the canvas tainted; blank if raster failed
  });
  expect(png.startsWith('data:image/png;base64,')).toBe(true);
  expect(png.length).toBeGreaterThan(200); // real pixels, not a blank 1×1
});

// ── blob-URL iframe (§6 row 2 / §7 row 2 guard-deletion) ─────────────────────
// The reader's HTML attachment viewer (`ReadSurface` → `HtmlDoc`) loads a
// same-origin `blob:` URL in an iframe and drives zoom through its
// `contentDocument`. WebView2 REFUSED `<iframe src=blob:>` outright ("此页面已被
// Microsoft Edge 阻止") — the reason the codebase carried WebView2 avoidance
// comments. Chromium loads it and keeps it same-origin scriptable; this test pins
// that so those comments can be removed and the behaviour can't silently regress.
test('blob-URL iframe loads and stays same-origin scriptable (the pattern WebView2 refused)', async () => {
  const result = await page.evaluate(async () => {
    const html = '<!doctype html><html><body><p id="marker">BLOB_IFRAME_OK</p></body></html>';
    const url = URL.createObjectURL(new Blob([html], { type: 'text/html' }));
    const iframe = document.createElement('iframe');
    document.body.appendChild(iframe);
    await new Promise<void>((res, rej) => {
      iframe.onload = () => res();
      iframe.onerror = () => rej(new Error('blob iframe failed to load'));
      iframe.src = url;
    });
    // Same-origin read (HtmlDoc reads the marker's document) …
    const text = iframe.contentDocument?.getElementById('marker')?.textContent ?? '';
    // … and same-origin WRITE (HtmlDoc applies zoom via documentElement.style.zoom).
    let zoomable = false;
    try {
      (iframe.contentDocument!.documentElement.style as CSSStyleDeclaration & { zoom: string }).zoom = '1.5';
      zoomable = true;
    } catch {
      /* cross-origin — the WebView2 failure mode */
    }
    iframe.remove();
    URL.revokeObjectURL(url);
    return { text, zoomable };
  });
  expect(result.text).toBe('BLOB_IFRAME_OK');
  expect(result.zoomable).toBe(true);
});

// ── sizedSvg WebKit shim (§6 row 3 / §7 row 3 guard-deletion) ────────────────
// mermaid/vega emit a `viewBox` but often no explicit width/height (just a CSS
// max-width). WebKit reported `naturalWidth === 0` for such an SVG and drew a
// blank PNG, so `FigureEditor.sizedSvg` injected explicit dimensions before
// rasterizing. This mirrors the SIMPLIFIED path (no injection): load the
// viewBox-only SVG and `drawImage(img, 0, 0, w, h)` with explicit dest dims.
// Passing proves the injection is unnecessary on Chromium, so it can be deleted.
test('sizedSvg: Chromium rasterizes a viewBox-only SVG (the WebKit naturalWidth=0 case)', async () => {
  const out = await page.evaluate(async () => {
    // No width/height attrs — only a viewBox + a CSS max-width, exactly what
    // mermaid/vega emit and what WebKit blanked.
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 80" style="max-width:100%">' +
      '<rect width="120" height="80" fill="#22aa66"/></svg>';
    const w = 120;
    const h = 80;
    const img = new Image();
    await new Promise<void>((res, rej) => {
      img.onload = () => res();
      img.onerror = () => rej(new Error('svg decode failed'));
      img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
    });
    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d')!;
    ctx.drawImage(img, 0, 0, w, h); // explicit dest dims — no injected width/height needed
    const px = ctx.getImageData(10, 10, 1, 1).data; // should be the green rect
    return { alpha: px[3], green: px[1] };
  });
  // A blank PNG (the WebKit failure) would be fully transparent — assert real pixels.
  expect(out.alpha).toBeGreaterThan(0);
  expect(out.green).toBeGreaterThan(100);
});

// ── bytes-over-IPC (§7 row 4 — base64→bytes) ────────────────────────────────
// The file-bytes channels (storage/attachment, localfs, sftp) and voice now pass
// raw bytes over IPC instead of base64. This pins the round-trip on the one path
// reachable without a server/dialog — attachment write→read — in BOTH directions
// (renderer→main write, main→renderer read). The high bytes (253–255) would be
// mangled by any stray text/base64 (mis)handling; exact equality proves binary
// transfer. SFTP/localfs/voice use the identical structured-clone mechanism.
test('bytes over IPC: attachment write→read round-trips raw bytes (no base64)', async () => {
  const rt = await page.evaluate(async () => {
    const b = window.__ELECTRON_BRIDGE__!;
    const root = await b.invoke<string>('attachment_default_dir');
    const original = [0, 1, 2, 66, 121, 116, 101, 115, 253, 254, 255]; // incl. high bytes
    const added = await b.invoke<{ key: string; file: string; path: string }>('attachment_write_bytes', {
      root,
      filename: 'e2e-bytes-roundtrip.bin',
      bytes: new Uint8Array(original),
    });
    const f = await b.invoke<{ bytes: ArrayLike<number>; mime: string }>('attachment_read', { path: added.path });
    const readback = Array.from(new Uint8Array(f.bytes));
    await b.invoke('attachment_delete', { path: added.path }); // clean up
    return { original, readback };
  });
  expect(rt.readback).toEqual(rt.original);
});

// ── Excalidraw sketch editor (figure-plan Phase C) ───────────────────────────
// The interactive sketch surface is a heavy lazy chunk that mounts its own React
// tree and loads fonts. Pin that it lazy-loads and mounts under the packaged
// `app://` origin (the black-screen render class doesn't reproduce), AND that its
// font loader is pointed at the SELF-HOSTED assets — never the esm.sh CDN
// fallback — which is the offline-first contract (full airplane-mode is
// device-verified: fonts degrade gracefully to system fonts if absent).
test('excalidraw: the sketch editor lazy-mounts and is configured for offline fonts', async () => {
  // The "Add a hub" modal auto-opens once on boot (no hub configured, AppShell.tsx)
  // and its backdrop blocks the activity bar. It can pop up at any point during the
  // earlier tests (`init()` resolves async), so close it deterministically here.
  // `toPass` re-runs until the modal is gone, absorbing the open/animation race;
  // it's also a no-op if the modal was never present (count 0 → the assertion holds).
  await expect(async () => {
    const closeBtn = page.locator('.connect .connect-head button');
    if ((await closeBtn.count()) > 0) await closeBtn.click({ timeout: 2000 });
    await expect(page.locator('.connect')).toHaveCount(0);
  }).toPass({ timeout: 15_000 });

  // Navigate to Author by clicking its activity-bar button — a keyboard shortcut
  // (Ctrl+4) is swallowed when a modal or the terminal xterm holds focus.
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  // Open the categorized "New ▾" menu and pick Sketch → an in-memory sketch doc
  // (no workspace folder in CI). The standalone "New X" buttons collapsed into
  // this menu in the W1 shell cleanup.
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'Sketch (Excalidraw)' }).click();

  // The Excalidraw canvas mounted — the lazy chunk resolved and its React tree
  // painted without crashing on the `app://` origin.
  await expect(page.locator('.excalidraw-host .excalidraw').first()).toBeVisible({ timeout: 20_000 });
  // The font loader was pointed at the local copy, so it will not fall back to the
  // esm.sh CDN. (Set at the ExcalidrawEditor module scope, which only evaluates
  // once the lazy chunk above has loaded.)
  const assetPath = await page.evaluate(
    () => (window as unknown as { EXCALIDRAW_ASSET_PATH?: string }).EXCALIDRAW_ASSET_PATH,
  );
  expect(assetPath).toBe('/excalidraw-assets/');
});

// ── Author shell: New ▾ menu + workspace-pane fold (W1 shell cleanup) ─────────
// The six standalone "New X" buttons collapsed into one categorized New ▾ menu,
// and the left pane is workspace-only with a fold chevron + slim re-open button.
// Pin the create-from-menu path and the fold/unfold of the pane.
test('author: the New ▾ menu creates a document and the workspace pane folds', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  // The workspace pane shows by default.
  await expect(page.locator('.author-nav')).toBeVisible();
  // Open the New ▾ menu and create a Document from it (menuitem, not the primary
  // button — this exercises the menu path).
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'New', exact: true }).click();
  await expect(page.locator('.read-tabstrip .read-tabitem').last()).toBeVisible();
  // Fold the pane via its header chevron → the tree is gone and a slim edge
  // button takes its place; clicking that restores the pane.
  await page.locator('.author-nav .author-nav-head .author-nav-icon').first().click();
  await expect(page.locator('.author-nav')).toHaveCount(0);
  await page.locator('.author-nav-show').click();
  await expect(page.locator('.author-nav')).toBeVisible();
});

// ── Author outline: right-hand heading nav + jump-to-line (W2) ───────────────
// The markdown editor gains an Obsidian-style outline on the right (the shared
// MarkdownOutline rail, extended to drive the CodeMirror source pane). Pin that
// it lists the document's headings and that a click jumps the source editor to
// the heading's line.
test('author: the markdown outline lists headings and jumps the source editor', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  // A fresh Document from the New ▾ menu.
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'New', exact: true }).click();
  // Split mode keeps both the editor and preview live.
  await page.getByRole('button', { name: 'Split', exact: true }).click();
  // Type a two-heading document into the editor. `force` skips the actionability
  // retry loop — a plain click on the CodeMirror contenteditable hangs under
  // xvfb (the pointer-stability check never settles).
  const editor = page.locator('.md-editor .cm-content').last();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('# First heading\n\nalpha\n\n## Second heading\n\nbeta\n');
  // The outline rail appears on the right listing both headings (it hides at
  // ≤ 1 heading, so its presence also proves the recompute ran).
  const outline = page.locator('.mdreader-outline.side-right');
  await expect(outline).toBeVisible({ timeout: 15_000 });
  await expect(outline.getByRole('button', { name: 'Second heading' })).toBeVisible();
  // Clicking the second heading jumps the source editor to its line — the active
  // line becomes the `## Second heading` line.
  await outline.getByRole('button', { name: 'Second heading' }).click();
  await expect(page.locator('.md-editor .cm-activeLine').last()).toContainText('Second heading');
});

// ── Author canvas v2: React Flow board + JSON Canvas 1.0 body (W3) ───────────
// The canvas editor is rebuilt on React Flow and its body/on-disk format is
// JSON Canvas 1.0 (Obsidian-interoperable). Pin that a board mounts (the lazy
// React Flow chunk resolves), notes add as nodes, and the persisted body is
// JSON Canvas (`nodes`), NOT the legacy `{cards,edges}` shape — the round-trip
// serialization is what makes "restores on reload" true.
test('author: a canvas board mounts on React Flow and saves as JSON Canvas', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'Board (canvas)' }).click();
  // The React Flow surface mounts (its lazy chunk resolved).
  await expect(page.locator('.canvas-flow')).toBeVisible({ timeout: 20_000 });
  // Add two notes from the toolbar → two nodes on the board.
  await page.getByRole('button', { name: 'Note', exact: true }).click();
  await page.getByRole('button', { name: 'Note', exact: true }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(2);
  // The persisted document body is JSON Canvas 1.0 (a `nodes` array of length 2),
  // never the legacy `{cards,edges}` shape — the round-trip that makes "restores
  // on reload" true. The documents store persists on a 400ms debounce, so poll.
  await expect
    .poll(
      async () =>
        page.evaluate(() => {
          const raw = localStorage.getItem('termipod.documents.v1');
          if (raw === null) return 'no-store';
          const docs = (JSON.parse(raw) as { docs: { kind: string; body: string }[] }).docs;
          const canvas = docs.find((d) => d.kind === 'canvas');
          if (canvas === undefined) return 'no-doc';
          const parsed = JSON.parse(canvas.body) as { nodes?: unknown[]; cards?: unknown[] };
          if (parsed.cards !== undefined) return 'legacy';
          return Array.isArray(parsed.nodes) ? `nodes:${parsed.nodes.length}` : 'no-nodes';
        }),
      { timeout: 10_000 },
    )
    .toBe('nodes:2');
});

// ── Web tab: real <webview> guest (read-web-tabs plan W1) ────────────────────
// The Read surface's in-app browser tab is an Electron <webview> guest in the
// isolated `persist:webtab` partition. This pins the load-bearing invariants that
// the guest hardening in webtab.ts enforces (native menus / real logins are
// device-verified): a guest loads a real page, its title propagates, it does NOT
// carry the preload bridge, and it cannot reach the privileged `app://` scheme
// (registered on defaultSession only). Serving from an in-test http server keeps
// it offline + deterministic.
test('web tab: a <webview> guest loads, isolates the bridge, and cannot reach app://', async () => {
  const server = http.createServer((_req, res) => {
    res.setHeader('content-type', 'text/html; charset=utf-8');
    res.end('<!doctype html><html><head><title>E2E Webview OK</title></head><body>hello guest</body></html>');
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', () => r()));
  const { port } = server.address() as AddressInfo;
  const guestUrl = `http://127.0.0.1:${port}/`;
  try {
    const result = await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement & {
        getTitle(): string;
        executeJavaScript(code: string): Promise<unknown>;
      };
      wv.setAttribute('src', url);
      // The main-process `will-attach-webview` guard REJECTS any partition other
      // than persist:webtab, so setting it correctly is also what lets the guest
      // attach at all (an implicit test of that enforcement).
      wv.setAttribute('partition', 'persist:webtab');
      wv.style.width = '400px';
      wv.style.height = '300px';
      document.body.appendChild(wv);
      await new Promise<void>((resolve, reject) => {
        const to = setTimeout(() => reject(new Error('webview load timeout')), 15_000);
        wv.addEventListener('did-finish-load', () => { clearTimeout(to); resolve(); }, { once: true });
        wv.addEventListener('did-fail-load', (e) => {
          if ((e as unknown as { isMainFrame?: boolean }).isMainFrame === false) return;
          clearTimeout(to);
          reject(new Error('did-fail-load ' + String((e as unknown as { errorCode?: number }).errorCode)));
        });
      });
      const title = wv.getTitle();
      const hasBridge = await wv.executeJavaScript('typeof window.__ELECTRON_BRIDGE__');
      const appFetch = await wv.executeJavaScript(
        "fetch('app://termipod/index.html').then(r => 'reached:' + r.status).catch(() => 'blocked')",
      );
      wv.remove();
      return { title, hasBridge, appFetch };
    }, guestUrl);
    expect(result.title).toBe('E2E Webview OK');
    // No preload → the bridge (and the whole command allowlist) never exists here.
    expect(result.hasBridge).toBe('undefined');
    // The app:// scheme handler is installed on defaultSession only — the guest
    // partition can't resolve it.
    expect(result.appFetch).toBe('blocked');
  } finally {
    await new Promise<void>((r) => server.close(() => r()));
  }
});

test('inspect: New scratch opens a code tab on CodeMirror and the trace lens jumps', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // Empty state until a tab is opened.
  await expect(page.locator('.inspect-empty')).toBeVisible();
  // New scratch → a code tab + a CodeMirror editor mount.
  await page.getByRole('button', { name: 'New scratch' }).click();
  await expect(page.locator('.inspect-tab').last()).toBeVisible();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Type a Python traceback into the scratch. The editor is contenteditable —
  // force-click + focus, never a plain click (which hangs under xvfb).
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type(
    'Traceback (most recent call last):\n  File "app.py", line 7, in main\n    raise ValueError("boom")\nValueError: boom',
  );
  // The trace lens detects the traceback and lists the frame; the file chip
  // carries the base name.
  await expect(page.locator('.inspect-trace')).toBeVisible();
  await expect(page.locator('.inspect-frame .frame-file').first()).toHaveText('app.py');
  // Closing the tab returns to the empty state.
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
  await expect(page.locator('.inspect-empty')).toBeVisible();
});

test('inspect: the Open menu launches the source picker modal', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // The Open ▾ menu lists the source affordances.
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(page.locator('.inspect-menu')).toBeVisible();
  // From workspace… opens the picker modal (its contents depend on whether a
  // workspace folder is set — assert the modal itself, backend-free).
  await page.getByRole('menuitem', { name: 'From workspace…' }).click();
  await expect(page.locator('.inspect-modal')).toBeVisible();
  // The × in the modal header closes it.
  await page.locator('.inspect-modal .inspect-modal-head .icon-btn').click();
  await expect(page.locator('.inspect-modal')).toHaveCount(0);
});

test('inspect: the tree-sitter symbol outline lists symbols and jumps the editor', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Choose JavaScript (brace-based → immune to auto-indent) so the outline
  // activates and the WASM grammar loads on demand.
  await page.locator('.inspect-runbar .surface-select').selectOption('javascript');
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type('function alpha(){ return 1; }\nfunction beta(){ return 2; }\nclass Gamma { m(){ return 3; } }\n');
  // The outline rail appears with the extracted symbols (grammar wasm fetched
  // from app:// on demand — allow generous time).
  const outline = page.locator('.code-outline');
  await expect(outline).toBeVisible({ timeout: 15000 });
  await expect(outline.locator('.code-outline-name', { hasText: 'alpha' })).toBeVisible();
  await expect(outline.locator('.code-outline-name', { hasText: 'Gamma' })).toBeVisible();
  // Clicking a symbol jumps the editor caret to its line.
  await outline.locator('.code-outline-item', { hasText: 'beta' }).click();
  await expect(page.locator('.inspect-code .cm-activeLine')).toContainText('beta');
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted patch renders the multi-file diff viewer (W2)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // A two-file git patch. CRITICAL: no line may start with whitespace — a plain
  // scratch has no language, so CM's newline command copies the previous line's
  // leading indent; a ` context` line would then indent every following line and
  // break `^@@`/`^diff --git` matching. Pure delete + add hunks have no
  // leading-space lines, so the typed text round-trips verbatim.
  await page.keyboard.type(
    [
      'diff --git a/del.txt b/del.txt',
      'deleted file mode 100644',
      '--- a/del.txt',
      '+++ /dev/null',
      '@@ -1,2 +0,0 @@',
      '-alpha',
      '-beta',
      'diff --git a/add.txt b/add.txt',
      'new file mode 100644',
      '--- /dev/null',
      '+++ b/add.txt',
      '@@ -0,0 +1,2 @@',
      '+gamma',
      '+delta',
    ].join('\n'),
  );
  // The content sniffs as a patch → the "View as diff" affordance appears.
  await page.getByRole('button', { name: 'View as diff' }).click();
  // The patch viewer renders one card per file (git-diff-view lazy chunk).
  await expect(page.locator('.patch-file')).toHaveCount(2, { timeout: 15000 });
  await expect(page.locator('.patch-file-path').first()).toContainText('del.txt');
  // Both a delete (A→D) and an add badge render.
  await expect(page.locator('.patch-status.k-add')).toBeVisible();
  await expect(page.locator('.patch-status.k-delete')).toBeVisible();
  // "View source" flips the tab back to the editor.
  await page.getByRole('button', { name: 'View source' }).click();
  await expect(page.locator('.inspect-code .cm-content')).toBeVisible();
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: comparing two open tabs mounts the merge view (W2)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // Tab A.
  await page.getByRole('button', { name: 'New scratch' }).click();
  let editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type('alpha\nbeta\ngamma');
  // Tab B (becomes active).
  await page.getByRole('button', { name: 'New scratch' }).click();
  editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type('alpha\nBETA\ngamma');
  // Compare ▾ → the first "open tab" entry is the other scratch. Scope to the
  // surface (`main`): "Compare" also names the J5 activity-bar tab in the nav,
  // so an unscoped role query is a strict-mode collision.
  await page.getByRole('main').getByRole('button', { name: 'Compare', exact: true }).click();
  await expect(page.locator('.inspect-menu')).toBeVisible();
  await page.locator('.inspect-menu-item').first().click();
  // The @codemirror/merge view mounts (its own lazy chunk).
  await expect(page.locator('.compare-host .cm-mergeView')).toBeVisible({ timeout: 15000 });
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted log renders the virtualized log viewer, filters and searches (W3)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // A log-shaped paste (level words + step/epoch markers) so `looksLikeLog` fires
  // and "View as log" appears. NO line starts with whitespace — a plain scratch's
  // newline command copies the previous line's indent (the W2 auto-indent trap).
  await page.keyboard.type(
    [
      '2026-07-23 10:00:00 INFO starting run',
      'epoch 1 step 100 loss=2.30',
      'epoch 1 step 200 loss=1.90',
      'WARN gpu memory high',
      'epoch 2 step 300 loss=1.20',
      'ERROR nan encountered',
      'done',
    ].join('\n'),
  );
  // The content sniffs as a log → the "View as log" affordance appears.
  await page.getByRole('button', { name: 'View as log' }).click();
  // The virtualized viewer mounts (its own lazy chunk — react-virtuoso + anser).
  await expect(page.locator('.logview')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('.logview-row').first()).toBeVisible();
  await expect(page.locator('.logview-count')).toContainText('7');

  // Regex search over the whole log: three lines carry "epoch".
  await page.locator('.logview-input').fill('epoch');
  await expect(page.locator('.logview-hitn')).toContainText('1/3');
  await page.locator('.logview-input').fill('');

  // Error/warn quick-filter narrows the view to the WARN + ERROR lines (2).
  await page.locator('.logview-btn', { hasText: 'Warn/Err' }).click();
  await expect(page.locator('.logview-count')).toContainText('matching');
  await expect(page.locator('.logview-count')).toContainText('2');

  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted DOT graph renders to SVG via the wasm engine (graph)', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // DOT graph — line 1 is unindented so `looksLikeDot` fires; inner lines may be
  // auto-indented (harmless — DOT is whitespace-insensitive, semicolons terminate).
  await page.keyboard.type(['digraph G {', 'rankdir=LR;', 'alpha -> beta;', 'beta -> gamma;', '}'].join('\n'));
  await page.getByRole('button', { name: 'View as graph' }).click();
  // The lazy DotGraphView mounts and the wasm Graphviz engine renders an SVG.
  await expect(page.locator('.dotgraph')).toBeVisible({ timeout: 20000 });
  await expect(page.locator('.dotgraph-svg svg')).toBeVisible({ timeout: 20000 });
  // Graphviz emits node labels as SVG <text>; our nodes are alpha/beta/gamma.
  await expect(page.locator('.dotgraph-svg svg')).toContainText('alpha');
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the Trace model graph form opens and Detect round-trips the interpreter', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Make it a Python tab so the "Trace model graph" affordance appears.
  await page.locator('.inspect-runbar .surface-select').selectOption('python');
  await editor.click({ force: true });
  await page.keyboard.type('class Model:\n    pass');
  // Open the trace form.
  await page.getByRole('button', { name: 'Trace model graph' }).click();
  await expect(page.locator('.trace-modal')).toBeVisible();
  // The interpreter defaults to python3; Detect probes it for torch/torchview.
  // The runner has python3 but not torch → the probe round-trips to an error;
  // either outcome (ok/err) proves the trace_run IPC path works end-to-end.
  await page.getByRole('button', { name: 'Detect', exact: true }).click();
  await expect(page.locator('.trace-ok, .trace-err')).toBeVisible({ timeout: 25000 });
  // Close the modal (backdrop click) and the tab.
  await page.locator('.inspect-modal-backdrop').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.trace-modal')).toHaveCount(0);
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the Call graph form opens and Detect round-trips the interpreter', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // A Python tab surfaces the "Call graph" affordance (code2flow: py/js/rb/php).
  await page.locator('.inspect-runbar .surface-select').selectOption('python');
  await editor.click({ force: true });
  await page.keyboard.type('def a():\n    b()\n\ndef b():\n    pass');
  await page.getByRole('button', { name: 'Call graph' }).click();
  await expect(page.locator('.trace-modal')).toBeVisible();
  // The runner has python3 but not code2flow → the probe round-trips to an error;
  // either outcome (ok/err) proves the reused trace_run IPC path works end-to-end.
  await page.getByRole('button', { name: 'Detect', exact: true }).click();
  await expect(page.locator('.trace-ok, .trace-err')).toBeVisible({ timeout: 25000 });
  await page.locator('.inspect-modal-backdrop').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.trace-modal')).toHaveCount(0);
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the log index commands slice + search a file without slurping it (W3)', async () => {
  // Exercise the main-process line index directly through the bridge — the
  // no-whole-file-read path LogView's IndexedLogModel drives.
  const p = path.join(os.tmpdir(), `tp-w3-${process.pid}.log`);
  fs.writeFileSync(p, 'boot\nWARN low disk\ninfo tick\nERROR boom\nbye\n');
  try {
    const opened = await page.evaluate(
      (fp) => window.__ELECTRON_BRIDGE__!.invoke<{ id: string; size: number; lines: number }>('log_open', { path: fp }),
      p,
    );
    expect(opened.lines).toBe(5);
    expect(opened.id).toMatch(/^log\d+$/);

    const sl = await page.evaluate(
      (id) => window.__ELECTRON_BRIDGE__!.invoke<{ lines: string[] }>('log_slice', { id, from: 1, count: 1 }),
      opened.id,
    );
    expect(sl.lines).toEqual(['WARN low disk']);

    const se = await page.evaluate(
      (id) => window.__ELECTRON_BRIDGE__!.invoke<{ hits: Array<{ line: number }> }>('log_search', { id, pattern: 'WARN|ERROR', flags: 'i', max: 10 }),
      opened.id,
    );
    expect(se.hits.map((h) => h.line)).toEqual([1, 3]);

    await page.evaluate((id) => window.__ELECTRON_BRIDGE__!.invoke('log_close', { id }), opened.id);
  } finally {
    fs.rmSync(p, { force: true });
  }
});

test('inspect: checkpoint_inspect parses a safetensors header (W4)', async () => {
  // A safetensors file = u64 LE header length + JSON header + tensor bytes. The
  // parser reads only the header, so zero-padded data suffices.
  const header = {
    __metadata__: { format: 'pt' },
    'model.layers.0.attn.weight': { dtype: 'F16', shape: [4, 4], data_offsets: [0, 32] },
    'lm_head.weight': { dtype: 'F32', shape: [8, 4], data_offsets: [32, 160] },
  };
  const json = Buffer.from(JSON.stringify(header), 'utf8');
  const len = Buffer.alloc(8);
  len.writeBigUInt64LE(BigInt(json.length));
  const p = path.join(os.tmpdir(), `tp-w4-${process.pid}.safetensors`);
  fs.writeFileSync(p, Buffer.concat([len, json, Buffer.alloc(160)]));
  try {
    const info = await page.evaluate(
      (fp) =>
        window.__ELECTRON_BRIDGE__!.invoke<{ format: string; totalParams: number; tensorCount: number; tensors: Array<{ name: string }> }>(
          'checkpoint_inspect',
          { path: fp },
        ),
      p,
    );
    expect(info.format).toBe('safetensors');
    expect(info.tensorCount).toBe(2);
    expect(info.totalParams).toBe(16 + 32);
    expect(info.tensors.map((x) => x.name)).toContain('lm_head.weight');
  } finally {
    fs.rmSync(p, { force: true });
  }
});

test('inspect: checkpoint_inspect parses an ONNX graph (W4 remainder)', async () => {
  // Proves the BUNDLED main.cjs decodes ONNX: encode a ModelProto (incl. a
  // raw_data blob the parser must skip), then round-trip it through the real IPC.
  const enc = `
    syntax = "proto3"; package onnx;
    message OperatorSetIdProto { string domain = 1; int64 version = 2; }
    message TensorProto { repeated int64 dims = 1; int32 data_type = 2; string name = 8; bytes raw_data = 9; }
    message ValueInfoProto { string name = 1; }
    message NodeProto { repeated string input = 1; repeated string output = 2; string name = 3; string op_type = 4; }
    message GraphProto {
      repeated NodeProto node = 1; string name = 2; repeated TensorProto initializer = 5;
      repeated ValueInfoProto input = 11; repeated ValueInfoProto output = 12;
    }
    message ModelProto { int64 ir_version = 1; string producer_name = 2; GraphProto graph = 7; repeated OperatorSetIdProto opset_import = 8; }
  `;
  const Model = protobuf.parse(enc).root.lookupType('onnx.ModelProto');
  const bytes = Model.encode(
    Model.create({
      irVersion: 9,
      producerName: 'pytorch',
      opsetImport: [{ version: 18 }],
      graph: {
        name: 'g',
        input: [{ name: 'x' }],
        output: [{ name: 'y' }],
        node: [
          { opType: 'MatMul', name: 'mm', input: ['x', 'model.layers.0.weight'], output: ['h'] },
          { opType: 'Relu', name: 'act', input: ['h'], output: ['y'] },
        ],
        initializer: [
          { name: 'model.layers.0.weight', dataType: 1, dims: [4, 4], rawData: Buffer.alloc(1024, 3) },
          { name: 'model.layers.1.weight', dataType: 10, dims: [4, 8] },
        ],
      },
    }),
  ).finish();
  const p = path.join(os.tmpdir(), `tp-w4onnx-${process.pid}.onnx`);
  fs.writeFileSync(p, Buffer.from(bytes));
  try {
    const info = await page.evaluate(
      (fp) =>
        window.__ELECTRON_BRIDGE__!.invoke<{
          format: string;
          totalParams: number;
          tensorCount: number;
          ops?: Record<string, number>;
          graph?: { nodes: { opType: string; inputs: string[]; outputs: string[] }[]; inputs: string[]; outputs: string[] };
        }>('checkpoint_inspect', { path: fp }),
      p,
    );
    expect(info.format).toBe('onnx');
    expect(info.tensorCount).toBe(2);
    expect(info.totalParams).toBe(16 + 32);
    expect(info.ops).toEqual({ MatMul: 1, Relu: 1 });
    // The bundled main.cjs retains the wired operator graph (for "View as graph").
    expect(info.graph?.nodes.length).toBe(2);
    expect(info.graph?.nodes[0]).toEqual({ name: 'mm', opType: 'MatMul', inputs: ['x', 'model.layers.0.weight'], outputs: ['h'] });
    expect(info.graph?.outputs).toEqual(['y']);
  } finally {
    fs.rmSync(p, { force: true });
  }
});
