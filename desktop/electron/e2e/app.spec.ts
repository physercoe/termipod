import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';

// The preload injects this on the renderer's `window` (see src/preload.ts). It
// is declared in the frontend package, not here, so mirror the minimal surface
// these tests touch.
declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: { invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> };
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
  // `app_version` is a native command; it returns the packaged CalVer version.
  const version = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('app_version'));
  expect(version).toMatch(/^\d{4}\.\d+\.\d+/);
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
