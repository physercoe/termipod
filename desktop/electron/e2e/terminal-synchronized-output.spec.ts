import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

// Exercise the real SSH-event → Screen → xterm renderer path. Fragmented
// redraws must remain hidden until DECSET 2026 ends, as on a slow SSH link.
let app: ElectronApplication;
let page: Page;
let userDataDir = '';

test.beforeAll(async () => {
  userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-e2e-term-sync-'));
  fs.mkdirSync(path.join(userDataDir, 'migration'));
  fs.writeFileSync(path.join(userDataDir, 'migration', 'state-v1.json'), JSON.stringify({
    version: 1,
    exportedAt: new Date().toISOString(),
    data: {
      connections: JSON.stringify([{
        id: 'sync-host', name: 'Sync host', host: 'sync.invalid', port: 22,
        username: 'tester', authMethod: 'password', keyId: null, tmuxPath: null,
        group: null, createdAt: '2026-11-04T00:00:00Z', lastConnectedAt: null, deepLinkId: null,
      }]),
    },
  }));
  app = await electron.launch({
    args: ['--no-sandbox', '--disable-gpu', '--password-store=basic', `--user-data-dir=${userDataDir}`, path.resolve(__dirname, '../out/main.cjs')],
    env: { ...process.env, TERMIPOD_DIST: path.resolve(__dirname, '../../dist'), TERMIPOD_E2E: '1' },
  });
  page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
  await app.evaluate(({ ipcMain }) => {
    const state = globalThis as unknown as { sshWrites: string[] };
    state.sshWrites = [];
    ipcMain.removeHandler('bridge:invoke');
    ipcMain.handle('bridge:invoke', async (_event, cmd: string, args: { key?: string; data?: string }) => {
      if (cmd === 'ssh_connect') return 'sync-session';
      if (cmd === 'ssh_write') { state.sshWrites.push(args.data!); return; }
      if (cmd === 'keychain_get') return args.key === 'secretstore.v1'
        ? JSON.stringify({ 'password_sync-host': 'test-password' }) : 'test-password';
      if (cmd === 'keychain_is_windows') return false;
      // Use DOM rendering, matching the macOS terminal and working without GPU.
      if (cmd === 'platform_os') return 'macos';
      return null;
    });
  });
  const dialog = page.getByRole('dialog', { name: 'Add a hub' });
  await dialog.waitFor({ state: 'visible', timeout: 10_000 }).catch(() => undefined);
  if (await dialog.isVisible()) await dialog.getByRole('button', { name: 'Close' }).click();
  await page.locator('[data-job="terminal"]').click();
  await page.locator('.term-nav-quick').click();
  await expect(page.locator('.term-main .xterm-rows')).toBeVisible();
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  // Let Screen's initial 120/450 ms font/layout settling complete before the
  // test opens a synchronized update; resizing is a separate redraw trigger.
  await page.waitForTimeout(500);
});

test.afterAll(async () => {
  await app?.close();
  if (userDataDir) fs.rmSync(userDataDir, { recursive: true, force: true });
});

async function output(data: string): Promise<void> {
  await app.evaluate(({ BrowserWindow }, chunk) => {
    BrowserWindow.getAllWindows()[0].webContents.send('bridge:event', {
      event: 'ssh-data', payload: { id: 'sync-session', bytes: Array.from(Buffer.from(chunk)) },
    });
  }, data);
}

async function expectReply(reply: string): Promise<void> {
  await expect.poll(() => app.evaluate(() => (
    globalThis as unknown as { sshWrites: string[] }
  ).sshWrites)).toContain(reply);
}

test('fragmented SSH redraws paint only when synchronized output ends', async () => {
  const screen = page.locator('.term-main .xterm-rows');
  await output('\x1b[2J\x1b[HCURRENT FRAME\x1b[?2026$p');
  await expectReply('\x1b[?2026;2$y'); // supported, currently reset
  await expect(screen).toContainText('CURRENT FRAME');

  // Split the control sequence itself across SSH chunks too.
  await output('\x1b[?20');
  await output('26h\x1b[2J\x1b[HOLD TRANSCRIPT REPLAY\x1b[?2026$p');
  await expectReply('\x1b[?2026;1$y'); // partial frame was parsed, update is open
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  }));
  await expect(screen).toContainText('CURRENT FRAME');
  await expect(screen).not.toContainText('OLD TRANSCRIPT REPLAY');

  await output('\x1b[HFINAL FRAME 中文\x1b[K\x1b[?2026l');
  await expect(screen).toContainText('FINAL FRAME 中文');
  await expect(screen).not.toContainText('OLD TRANSCRIPT REPLAY');

  // tmux/editor alternate-screen transitions must restore the shell buffer.
  await output('\x1b[?1049h\x1b[HAPP SCREEN');
  await expect(screen).toContainText('APP SCREEN');
  await output('\x1b[?1049l');
  await expect(screen).toContainText('FINAL FRAME 中文');
  await expect(screen).not.toContainText('APP SCREEN');
});
