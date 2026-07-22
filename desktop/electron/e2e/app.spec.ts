import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';

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
