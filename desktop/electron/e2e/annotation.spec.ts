import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import http from 'node:http';
import type { AddressInfo } from 'node:net';

/// D2 annotation overlay (docs/plans/desktop-ui-context-and-pointing.md §3.4,
/// plan §5): synthesize a drag and assert the crop lands as a compose-box
/// chip — and that sending posts the image to the (mock) hub. The second test
/// covers the D2.1 GLOBAL trigger: the status-bar crosshair chip arms the
/// overlay with no companion origin, and the target row still offers the
/// bound Author companion ("Send to <agent>").
///
/// The flow drives the REAL path: a mock hub serves the connect probe + the
/// agents list (+ events backfill + a parked SSE stream); the UI-context
/// sharing toggle is seeded on; the companion's "Ask agent" button arms the
/// overlay; the drag is trusted Chromium input (page.mouse); main captures
/// the real window; the target row's "Send to <agent>" hands the crop back as
/// a chip. The kimi-web target is not exercised — it needs the real kimi
/// binary. The two tests run serially in one app instance; test 2 builds on
/// test 1's connected session + bound companion.
///
/// Hermeticity (an e2e instance must never touch the user's state):
///   - a THROWAWAY --user-data-dir: the default dir holds the real profile,
///     secrets, and the single-instance posture;
///   - a pre-seeded <userData>/migration/state-v1.json (read before the Tauri
///     legacy fallback, ipc/migration.ts) — it carries the sharing toggle ON
///     and a THROWAWAY Author workspace, so the boot restore never imports
///     the user's snapshot and "New document" writes into tmp, not the user's
///     vault;
///   - TERMIPOD_E2E skips the real ~/.kimi-code/mcp.json reconcile
///     (desktopui.ts) and the cross-app keychain reads (ipc/keychain.ts).

declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: {
      invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T>;
    };
  }
}

const MAIN_ENTRY = path.resolve(__dirname, '..', 'out', 'main.cjs');
const DIST_DIR = path.resolve(__dirname, '..', '..', 'dist');
// `--no-sandbox` (the Chromium setuid sandbox can't run in an unprivileged CI
// container) and `--disable-gpu` (xvfb has no real GPU — force SwiftShader).
// `--password-store=basic`: this spec connects a hub profile, which writes the
// token via safeStorage — headless Linux has no dbus/kwallet/libsecret, and
// Electron 43 gates encryptString on IsEncryptionAvailable() =
// `OSCrypt available || (plaintext opt-in && backend == "basic_text")`
// (electron_api_safe_storage.cc). The flag pins the backend; the matching
// opt-in lives main-side in ipc/keychain.ts under TERMIPOD_E2E. macOS uses
// the Keychain either way, so the flag only bites in CI.
const CI_FLAGS = ['--no-sandbox', '--disable-gpu', '--password-store=basic'];

const AGENT = {
  id: 'ag_e2e1',
  handle: 'kimi-1',
  kind: 'kimi',
  status: 'running',
  created_at: '2026-07-30T00:00:00Z',
  updated_at: '2026-07-30T00:00:00Z',
};

interface InputPost {
  kind?: string;
  body?: string;
  images?: Array<{ mime_type?: string; data?: string }>;
}

let app: ElectronApplication;
let page: Page;
let hub: http.Server;
let hubPort = 0;
let userDataDir = '';
let tmpWorkspace = '';
const inputPosts: InputPost[] = [];

test.beforeAll(async () => {
  // The mock hub: only what the flow needs answers 200; everything else 404s
  // (React Query tolerates per-query errors and renders the empty states).
  hub = http.createServer((req, res) => {
    const url = req.url ?? '';
    const json = (code: number, body: unknown): void => {
      res.writeHead(code, { 'content-type': 'application/json' });
      res.end(JSON.stringify(body));
    };
    if (url.startsWith('/v1/_info')) return json(200, { version: '0.0.0-e2e', name: 'mock-hub' });
    if (url.startsWith('/v1/teams/t1/agents/ag_e2e1/stream')) {
      // Park the SSE stream: open, silent, held until the server closes.
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
      res.write(': ok\n\n');
      return;
    }
    if (url.startsWith('/v1/teams/t1/agents/ag_e2e1/events')) return json(200, []);
    if (url.startsWith('/v1/teams/t1/agents') && req.method === 'GET') return json(200, [AGENT]);
    if (url.startsWith('/v1/teams/t1/agents/ag_e2e1/input') && req.method === 'POST') {
      let body = '';
      req.on('data', (d: Buffer) => (body += d.toString('utf8')));
      req.on('end', () => {
        try {
          inputPosts.push(JSON.parse(body) as InputPost);
        } catch {
          /* recorded as unparseable */
        }
        json(201, { id: 'ev_1', seq: 1, ts: '2026-07-30T00:00:01Z' });
      });
      return;
    }
    return json(404, { error: 'not mocked' });
  });
  await new Promise<void>((resolve) => hub.listen(0, '127.0.0.1', resolve));
  hubPort = (hub.address() as AddressInfo).port;

  userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-e2e-annot-'));
  tmpWorkspace = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-e2e-ws-'));
  // The hermetic boot snapshot: sharing ON, workspace in tmp (see the header).
  fs.mkdirSync(path.join(userDataDir, 'migration'), { recursive: true });
  fs.writeFileSync(
    path.join(userDataDir, 'migration', 'state-v1.json'),
    JSON.stringify({
      version: 1,
      exportedAt: new Date().toISOString(),
      data: {
        'termipod.uiContext.enabled': '1',
        'termipod.author.workspace': tmpWorkspace,
      },
    }),
  );

  app = await electron.launch({
    args: [...CI_FLAGS, `--user-data-dir=${userDataDir}`, MAIN_ENTRY],
    env: { ...process.env, TERMIPOD_DIST: DIST_DIR, TERMIPOD_E2E: '1' },
  });
  page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
});

test.afterAll(async () => {
  await app?.close();
  hub?.closeAllConnections?.();
  await new Promise((r) => hub?.close(r));
  if (userDataDir !== '') fs.rmSync(userDataDir, { recursive: true, force: true });
  if (tmpWorkspace !== '') fs.rmSync(tmpWorkspace, { recursive: true, force: true });
});

test('D2: drag → target row → companion chip → postAgentInput carries the image', async () => {
  test.setTimeout(90_000);

  // Connect through the real ConnectPanel (auto-opened on boot): the probe +
  // bind hit the mock hub; the token lands in the throwaway safeStorage file.
  const connect = page.locator('.connect');
  await expect(connect).toBeVisible({ timeout: 20_000 });
  await connect.locator('input').nth(1).fill(`http://127.0.0.1:${String(hubPort)}`);
  await connect.locator('input').nth(2).fill('t1');
  await connect.locator('input').nth(3).fill('e2e-token');
  await connect.getByRole('button', { name: /^connect$/i }).click();
  await expect(connect).toHaveCount(0, { timeout: 20_000 });

  // Author surface: a fresh markdown doc (into the tmp workspace), then open
  // the assistant pane — the companion auto-selects the mock agent.
  await page.locator('[data-job="author"]').click();
  await page.getByRole('button', { name: 'New', exact: true }).click();
  await page.getByRole('button', { name: '✦ Assistant' }).click();
  const composer = page.locator('.companion .composer textarea');
  await expect(composer).toBeVisible({ timeout: 20_000 });

  // "Ask agent" shows only because the toggle is on; clicking arms the overlay.
  const ask = page.locator('.companion button[aria-label*="Ask agent"]');
  await expect(ask).toBeVisible();
  await ask.click();
  await expect(page.locator('.annot-overlay')).toBeVisible();

  // Drag a rect with trusted mouse input; main captures the real window.
  await page.mouse.move(220, 220);
  await page.mouse.down();
  await page.mouse.move(520, 420, { steps: 6 });
  await page.mouse.up();

  // The target row: thumbnail + the companion target (kimi web isn't running
  // in e2e, so only the agent target is offered).
  const bar = page.locator('.annot-target');
  await expect(bar).toBeVisible({ timeout: 20_000 });
  await expect(bar.locator('.annot-thumb')).toBeVisible();
  await bar.locator('.annot-note').fill('what is this?');
  await bar.getByRole('button', { name: /Send to kimi-1/ }).click();

  // The crop is a delete-able chip in the compose box; the note is the draft.
  const chip = page.locator('.companion .att-chip');
  await expect(chip).toBeVisible();
  await expect(chip.locator('.att-thumb')).toBeVisible();
  await expect(composer).toHaveValue('what is this?');

  // Send → the hub receives kind:text with the image as raw-base64 payload.
  await composer.press('Enter');
  await expect.poll(() => inputPosts.length, { timeout: 20_000 }).toBe(1);
  const post = inputPosts[0];
  expect(post?.kind).toBe('text');
  expect(post?.body ?? '').toContain('what is this?');
  expect(post?.images?.length).toBe(1);
  expect(post?.images?.[0]?.mime_type).toBe('image/png');
  expect((post?.images?.[0]?.data ?? '').length).toBeGreaterThan(100);
  expect(post?.images?.[0]?.data?.startsWith('data:')).toBe(false);
});

test('D2.1: status-bar chip arms GLOBALLY — the bound companion is still offered', async () => {
  test.setTimeout(90_000);

  // Builds on the first test's state (serial file, one app instance): the hub
  // is connected and the Author companion is mounted + bound to ag_e2e1, so
  // it sits in the annotation store's companion registry. kimi web isn't
  // running in e2e, so the companion row is the only target offered.
  const chip = page.locator('.statusbar-annotate');
  await expect(chip).toBeVisible();
  await chip.click();
  await expect(page.locator('.annot-overlay')).toBeVisible();
  // The chip reads active while the overlay is armed (the dock-chip idiom).
  await expect(chip).toHaveClass(/active/);

  // Same trusted drag; main captures the real window.
  await page.mouse.move(240, 240);
  await page.mouse.down();
  await page.mouse.move(560, 460, { steps: 6 });
  await page.mouse.up();

  // No companion armed the overlay, yet the bound Author companion's session
  // is offered — the D2.1 registry resolution, not an origin.
  const bar = page.locator('.annot-target');
  await expect(bar).toBeVisible({ timeout: 20_000 });
  await expect(bar.locator('.annot-thumb')).toBeVisible();
  await bar.locator('.annot-note').fill('from the chip');
  await bar.getByRole('button', { name: /Send to kimi-1/ }).click();

  // The crop lands as a chip in that companion's compose box, note as draft.
  const att = page.locator('.companion .att-chip');
  await expect(att).toBeVisible();
  await expect(att.locator('.att-thumb')).toBeVisible();
  const composer = page.locator('.companion .composer textarea');
  await expect(composer).toHaveValue('from the chip');

  // Send → the hub receives a second input post, again carrying the image.
  await composer.press('Enter');
  await expect.poll(() => inputPosts.length, { timeout: 20_000 }).toBe(2);
  expect(inputPosts[1]?.kind).toBe('text');
  expect(inputPosts[1]?.body ?? '').toContain('from the chip');
  expect(inputPosts[1]?.images?.length).toBe(1);
  expect(inputPosts[1]?.images?.[0]?.mime_type).toBe('image/png');
});
