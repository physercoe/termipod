import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import http from 'node:http';
import { spawn, type ChildProcess } from 'node:child_process';
import type { AddressInfo } from 'node:net';

declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: {
      invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T>;
    };
  }
}

/// E2E for the agent browser bridge (plan W1 — docs/plans/desktop-agent-browser-bridge.md).
///
/// Drives the real stack end to end: enable the bridge in the app (the
/// Settings toggle's IPC), read the discovery file the hostrunner would read,
/// run the shipped stdio relay as a child (the agent-side shape), and speak
/// MCP over it: initialize → tools/list → browser_list_tabs/browser_snapshot
/// against a live <webview> guest serving fixture text. Then the negative
/// halves: no bearer → 401, toggle off → server refuses + discovery gone.
///
/// Same harness as app.spec.ts: unpackaged out/main.cjs + desktop/dist.

const MAIN_ENTRY = path.resolve(__dirname, '..', 'out', 'main.cjs');
const DIST_DIR = path.resolve(__dirname, '..', '..', 'dist');
const CI_FLAGS = ['--no-sandbox', '--disable-gpu'];

let app: ElectronApplication;
let page: Page;
/// Minimal newline-delimited JSON-RPC client over the stdio relay — the same
/// framing an engine's MCP client uses.
class StdioMcp {
  private child: ChildProcess;
  private buf = '';
  private seq = 0;
  private pending = new Map<number, (msg: { result?: unknown; error?: { message: string } }) => void>();

  constructor(scriptPath: string, env: NodeJS.ProcessEnv) {
    this.child = spawn(process.execPath, [scriptPath], { env, stdio: ['pipe', 'pipe', 'pipe'] });
    this.child.stdout?.on('data', (d: Buffer) => {
      this.buf += d.toString('utf8');
      let i: number;
      while ((i = this.buf.indexOf('\n')) >= 0) {
        const line = this.buf.slice(0, i).trim();
        this.buf = this.buf.slice(i + 1);
        if (line === '') continue;
        const msg = JSON.parse(line) as { id?: number; result?: unknown; error?: { message: string } };
        if (msg.id !== undefined) this.pending.get(msg.id)?.(msg);
        if (msg.id !== undefined) this.pending.delete(msg.id);
      }
    });
  }

  request(method: string, params?: unknown): Promise<unknown> {
    const id = (this.seq += 1);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`timeout waiting for ${method}`)), 15_000);
      this.pending.set(id, (msg) => {
        clearTimeout(timer);
        if (msg.error !== undefined) reject(new Error(msg.error.message));
        else resolve(msg.result);
      });
      this.child.stdin?.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} })}\n`);
    });
  }

  notify(method: string): void {
    this.child.stdin?.write(`${JSON.stringify({ jsonrpc: '2.0', method })}\n`);
  }

  close(): void {
    this.child.kill();
  }
}

interface Discovery {
  url: string;
  token: string;
  pid: number;
  bridge_path: string;
}

function discoveryPath(): string {
  return path.join(os.homedir(), '.termipod', 'browser-bridge.json');
}

test.beforeAll(async () => {
  // A dedicated user-data dir: the app's single-instance lock lives there,
  // so a dev host with the installed TermiPod running (same default dir)
  // would have this launch quit on the lock and firstWindow() hang forever.
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-bb-e2e-'));
  app = await electron.launch({
    args: [...CI_FLAGS, `--user-data-dir=${userData}`, MAIN_ENTRY],
    env: { ...process.env, TERMIPOD_DIST: DIST_DIR, TERMIPOD_E2E: '1' },
  });
  page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
});

test.afterAll(async () => {
  // Leave no bridge running / discovery file behind on the CI host.
  try {
    await page.evaluate(() => window.__ELECTRON_BRIDGE__?.invoke('browserbridge_set_enabled', { enabled: false }));
  } catch {
    /* window already gone */
  }
  await app?.close();
});

test('browser bridge: MCP round-trip over the stdio relay against a live guest', async () => {
  // Fixture page for the guest tab.
  const fixture = http.createServer((_req, res) => {
    res.setHeader('content-type', 'text/html; charset=utf-8');
    res.end('<!doctype html><html><head><title>E2E Bridge Fixture</title></head><body>bridge fixture body text</body></html>');
  });
  await new Promise<void>((r) => fixture.listen(0, '127.0.0.1', () => r()));
  const { port: fixturePort } = fixture.address() as AddressInfo;
  const guestUrl = `http://127.0.0.1:${fixturePort}/`;

  let mcp: StdioMcp | null = null;
  try {
    // Default state: the bridge is OFF — no discovery file, nothing injected.
    expect(fs.existsSync(discoveryPath())).toBe(false);

    // The Settings toggle's reach: enable in main, discovery file appears.
    const on = await page.evaluate(() =>
      window.__ELECTRON_BRIDGE__?.invoke<{ enabled: boolean; running: boolean }>('browserbridge_set_enabled', { enabled: true }),
    );
    expect(on?.running).toBe(true);
    const discovery = JSON.parse(fs.readFileSync(discoveryPath(), 'utf8')) as Discovery;
    expect(discovery.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/mcp$/);
    expect(fs.existsSync(discovery.bridge_path)).toBe(true);
    expect(discovery.pid).toBeGreaterThan(0);

    // No bearer → 401 (the loopback server is not an open door).
    const noAuth = await fetch(discovery.url, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'ping' }),
    });
    expect(noAuth.status).toBe(401);

    // The agent-side shape: the stdio relay with the injected env.
    mcp = new StdioMcp(discovery.bridge_path, {
      TP_BROWSER_URL: discovery.url,
      TP_BROWSER_TOKEN: discovery.token,
      TP_BROWSER_SCOPE: 'read',
    });
    const init = (await mcp.request('initialize', { protocolVersion: '2025-06-18' })) as {
      serverInfo: { name: string };
    };
    expect(init.serverInfo.name).toBe('termipod-browser');
    mcp.notify('notifications/initialized');

    const list = (await mcp.request('tools/list')) as { tools: Array<{ name: string }> };
    expect(list.tools.map((t) => t.name).sort()).toEqual([
      'browser_list_tabs',
      'browser_read_text',
      'browser_screenshot',
      'browser_snapshot',
    ]);

    // No guest yet → an empty tab list.
    const empty = (await mcp.request('tools/call', { name: 'browser_list_tabs', arguments: {} })) as {
      content: Array<{ text: string }>;
    };
    expect(JSON.parse(empty.content[0]?.text ?? '[]')).toEqual([]);

    // Open a real <webview> guest on the fixture page (the app.spec.ts pattern).
    await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement;
      wv.setAttribute('src', url);
      wv.setAttribute('partition', 'persist:webtab');
      wv.style.width = '400px';
      wv.style.height = '300px';
      document.body.appendChild(wv);
      await new Promise<void>((resolve, reject) => {
        const to = setTimeout(() => reject(new Error('webview load timeout')), 15_000);
        wv.addEventListener('did-finish-load', () => { clearTimeout(to); resolve(); }, { once: true });
        wv.addEventListener('did-fail-load', () => { clearTimeout(to); reject(new Error('did-fail-load')); });
      });
    }, guestUrl);

    // browser_list_tabs sees the guest — and never a URL fragment.
    const tabs = (await mcp.request('tools/call', { name: 'browser_list_tabs', arguments: {} })) as {
      content: Array<{ text: string }>;
    };
    expect(tabs.content[0]?.text).not.toContain('#');
    const rows = JSON.parse(tabs.content[0]?.text ?? '[]') as Array<{ tabId: number; url: string; title: string; partition: string }>;
    const row = rows.find((r) => r.url === guestUrl);
    expect(row, `guest ${guestUrl} in ${tabs.content[0]?.text ?? ''}`).toBeDefined();
    expect(row?.title).toBe('E2E Bridge Fixture');
    expect(row?.partition).toBe('persist:webtab');

    // browser_snapshot reads the page's AX tree — fixture text present.
    const snap = (await mcp.request('tools/call', { name: 'browser_snapshot', arguments: { tabId: row?.tabId } })) as {
      content: Array<{ text: string }>;
    };
    expect(snap.content[0]?.text).toContain('bridge fixture body text');

    // A stale tab id fails cleanly.
    const gone = (await mcp.request('tools/call', { name: 'browser_snapshot', arguments: { tabId: 999999 } })) as {
      isError?: boolean;
      content: Array<{ text: string }>;
    };
    expect(gone.isError).toBe(true);
    expect(gone.content[0]?.text).toContain('TARGET_GONE');

    // Toggle off → discovery file gone, the server refuses connections.
    const off = await page.evaluate(() =>
      window.__ELECTRON_BRIDGE__?.invoke<{ enabled: boolean; running: boolean }>('browserbridge_set_enabled', { enabled: false }),
    );
    expect(off?.running).toBe(false);
    expect(fs.existsSync(discoveryPath())).toBe(false);
    await expect(
      fetch(discovery.url, {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${discovery.token}` },
        body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'ping' }),
      }),
    ).rejects.toThrow();
  } finally {
    mcp?.close();
    await new Promise<void>((r) => fixture.close(() => r()));
  }
});
