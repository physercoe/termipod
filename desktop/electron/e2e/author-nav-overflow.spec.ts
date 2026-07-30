import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

/// Regression: an OVERFLOWING Author workspace tree must SCROLL, not crush —
/// `.author-nav-sec.grow` without `overflow-y: auto` let the flex column
/// compress direct-child file rows below their content height (root-level
/// files rendered 11px tall while rows inside dir wrappers kept 26px).
/// Seed a workspace with enough files to overflow the nav section and assert
/// every row keeps its full height and the section scrolls instead.

const MAIN_ENTRY = path.resolve(__dirname, '..', 'out', 'main.cjs');
const DIST_DIR = path.resolve(__dirname, '..', '..', 'dist');
const CI_FLAGS = ['--no-sandbox', '--disable-gpu', '--password-store=basic'];

let app: ElectronApplication;
let page: Page;
let userDataDir = '';
let tmpWorkspace = '';

test.beforeAll(async () => {
  userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-e2e-authornav-'));
  tmpWorkspace = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-e2e-ws-'));
  // 60 root files + one dir with children: comfortably overflows the nav.
  for (let i = 0; i < 60; i += 1) {
    fs.writeFileSync(path.join(tmpWorkspace, `file-${String(i).padStart(2, '0')}.md`), `# file ${i}\n`);
  }
  fs.mkdirSync(path.join(tmpWorkspace, 'sub'));
  for (let i = 0; i < 5; i += 1) {
    fs.writeFileSync(path.join(tmpWorkspace, 'sub', `inner-${i}.md`), `# inner ${i}\n`);
  }
  fs.mkdirSync(path.join(userDataDir, 'migration'), { recursive: true });
  fs.writeFileSync(
    path.join(userDataDir, 'migration', 'state-v1.json'),
    JSON.stringify({
      version: 1,
      exportedAt: new Date().toISOString(),
      data: { 'termipod.author.workspace': tmpWorkspace },
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
  if (userDataDir !== '') fs.rmSync(userDataDir, { recursive: true, force: true });
  if (tmpWorkspace !== '') fs.rmSync(tmpWorkspace, { recursive: true, force: true });
});

test('an overflowing author tree scrolls; rows keep their full height', async () => {
  test.setTimeout(60_000);
  const hubDialog = page.getByRole('dialog', { name: 'Add a hub' });
  await hubDialog.waitFor({ state: 'visible', timeout: 10_000 }).catch(() => undefined);
  if (await hubDialog.isVisible().catch(() => false)) {
    await hubDialog.getByRole('button', { name: 'Close' }).click();
    await expect(hubDialog).toHaveCount(0);
  }
  await page.locator('[data-job="author"]').click();
  await expect(page.locator('.author-nav-item').first()).toBeVisible({ timeout: 20_000 });

  const r = await page.evaluate(() => {
    const sec = document.querySelector('.author-nav-sec.grow');
    const heights = [...document.querySelectorAll('.author-nav-item')].map((el) => (el as HTMLElement).offsetHeight);
    return {
      rows: heights.length,
      minH: Math.min(...heights),
      maxH: Math.max(...heights),
      scrolls: sec !== null && sec.scrollHeight > sec.clientHeight,
    };
  });
  expect(r.rows).toBeGreaterThan(50);
  // No crushed rows: every row is within a couple px of the tallest (borders).
  expect(r.maxH - r.minH).toBeLessThanOrEqual(2);
  expect(r.minH).toBeGreaterThanOrEqual(20);
  expect(r.scrolls).toBe(true);
});
