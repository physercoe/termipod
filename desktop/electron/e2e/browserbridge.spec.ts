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
  /// W2: action-scoped bearer, handed to opted-in spawns only.
  action_token: string;
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

interface AuditRow {
  tool: string;
  agent_id: string;
  args: Record<string, unknown>;
  ok: boolean;
  error: string | null;
  hub?: string;
}

/// W2 (docs/plans/desktop-agent-browser-bridge.md): the action tools behind
/// the spawn opt-in's ACTION bearer — navigate/click/type/eval against a live
/// guest, the read bearer's refusal, and the audit ring (redacted args,
/// hub:'skipped' with no signed-in hub).
test('browser bridge W2: action scope drives a live guest; read scope refuses; audit recorded', async () => {
  const fixture = http.createServer((_req, res) => {
    res.setHeader('content-type', 'text/html; charset=utf-8');
    res.end(`<!doctype html><html><head><title>E2E Action Fixture</title></head><body>
<button id="go">Go</button>
<span id="count">count:0</span>
<input id="q" type="text" placeholder="query">
<div id="echo"></div>
<script>
let n = 0;
document.getElementById('go').addEventListener('click', () => { n++; document.getElementById('count').textContent = 'count:' + n; });
document.getElementById('q').addEventListener('input', (e) => { document.getElementById('echo').textContent = 'echo:' + e.target.value; });
</script>
</body></html>`);
  });
  await new Promise<void>((r) => fixture.listen(0, '127.0.0.1', () => r()));
  const { port: fixturePort } = fixture.address() as AddressInfo;
  const guestUrl = `http://127.0.0.1:${fixturePort}/`;

  let mcp: StdioMcp | null = null;
  let mcpRead: StdioMcp | null = null;
  try {
    // Re-enable (test 1 ends toggled off) and read the W2 discovery shape.
    const on = await page.evaluate(() =>
      window.__ELECTRON_BRIDGE__?.invoke<{ enabled: boolean; running: boolean }>('browserbridge_set_enabled', { enabled: true }),
    );
    expect(on?.running).toBe(true);
    const discovery = JSON.parse(fs.readFileSync(discoveryPath(), 'utf8')) as Discovery;
    expect(typeof discovery.action_token).toBe('string');
    expect(discovery.action_token).not.toBe(discovery.token);

    // The action-scoped relay: what an opted-in spawn (browser_bridge: true)
    // gets injected, incl. the agent id the audit attributes calls to.
    mcp = new StdioMcp(discovery.bridge_path, {
      TP_BROWSER_URL: discovery.url,
      TP_BROWSER_TOKEN: discovery.action_token,
      TP_BROWSER_SCOPE: 'action',
      TP_BROWSER_AGENT_ID: 'e2e-agent-1',
    });
    await mcp.request('initialize', {});
    mcp.notify('notifications/initialized');

    const list = (await mcp.request('tools/list')) as { tools: Array<{ name: string }> };
    const names = list.tools.map((t) => t.name).sort();
    for (const wanted of ['browser_navigate', 'browser_click', 'browser_type', 'browser_eval', 'browser_find_tab', 'browser_scroll']) {
      expect(names).toContain(wanted);
    }

    // The read bearer stays read-only: no action tools listed, calls refused.
    mcpRead = new StdioMcp(discovery.bridge_path, {
      TP_BROWSER_URL: discovery.url,
      TP_BROWSER_TOKEN: discovery.token,
      TP_BROWSER_SCOPE: 'read',
    });
    await mcpRead.request('initialize', {});
    const readList = (await mcpRead.request('tools/list')) as { tools: Array<{ name: string }> };
    expect(readList.tools.map((t) => t.name).sort()).toEqual([
      'browser_list_tabs',
      'browser_read_text',
      'browser_screenshot',
      'browser_snapshot',
    ]);
    const refused = (await mcpRead.request('tools/call', { name: 'browser_click', arguments: { tabId: 1, selector: '#go' } })) as {
      isError?: boolean;
      content: Array<{ text: string }>;
    };
    expect(refused.isError).toBe(true);
    expect(refused.content[0]?.text).toContain('SCOPE_READ_ONLY');

    // A live guest on the fixture page.
    const wcId = await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement & { getWebContentsId(): number };
      wv.setAttribute('src', url);
      wv.setAttribute('partition', 'persist:webtab');
      wv.style.width = '480px';
      wv.style.height = '320px';
      document.body.appendChild(wv);
      await new Promise<void>((resolve, reject) => {
        const to = setTimeout(() => reject(new Error('webview load timeout')), 15_000);
        wv.addEventListener('did-finish-load', () => { clearTimeout(to); resolve(); }, { once: true });
        wv.addEventListener('did-fail-load', () => { clearTimeout(to); reject(new Error('did-fail-load')); });
      });
      return wv.getWebContentsId();
    }, guestUrl);

    // browser_find_tab {active:true} after the app reports the visible guest.
    await page.evaluate((id) => window.__ELECTRON_BRIDGE__?.invoke('browserbridge_set_active_guest', { id }), wcId);
    const found = (await mcp.request('tools/call', { name: 'browser_find_tab', arguments: { active: true } })) as {
      content: Array<{ text: string }>;
    };
    expect(found.content[0]?.text).toContain(`"tabId": ${String(wcId)}`);

    // Navigate policy: javascript: is refused even by an action-scoped agent.
    const denied = (await mcp.request('tools/call', { name: 'browser_navigate', arguments: { tabId: wcId, url: 'javascript:alert(1)' } })) as {
      isError?: boolean;
      content: Array<{ text: string }>;
    };
    expect(denied.isError).toBe(true);
    expect(denied.content[0]?.text).toContain('NAVIGATION_DENIED');

    // Snapshot → refs for the button and the input.
    const snap = (await mcp.request('tools/call', { name: 'browser_snapshot', arguments: { tabId: wcId } })) as {
      content: Array<{ text: string }>;
    };
    const snapText = snap.content[0]?.text ?? '';
    const goRef = /button "Go" \[ref=(@e\d+)\]/.exec(snapText)?.[1];
    const inputRef = /textbox[^\n]*\[ref=(@e\d+)\]/.exec(snapText)?.[1];
    expect(goRef, `button ref in snapshot:\n${snapText}`).toBeDefined();
    expect(inputRef, `textbox ref in snapshot:\n${snapText}`).toBeDefined();

    // Click the button via its ref — the counter increments in-page.
    const click = (await mcp.request('tools/call', { name: 'browser_click', arguments: { tabId: wcId, ref: goRef } })) as {
      isError?: boolean;
      content: Array<{ text: string }>;
    };
    expect(click.isError).not.toBe(true);

    // Type into the input — the echo div mirrors it. The reply must not echo text.
    const typed = (await mcp.request('tools/call', { name: 'browser_type', arguments: { tabId: wcId, ref: inputRef, text: 'hello' } })) as {
      isError?: boolean;
      content: Array<{ text: string }>;
    };
    expect(typed.isError).not.toBe(true);
    expect(typed.content[0]?.text).toBe('typed 5 chars');

    const readBack = (await mcp.request('tools/call', { name: 'browser_read_text', arguments: { tabId: wcId } })) as {
      content: Array<{ text: string }>;
    };
    expect(readBack.content[0]?.text).toContain('count:1');
    expect(readBack.content[0]?.text).toContain('echo:hello');

    // browser_eval returns JSON.
    const evaluated = (await mcp.request('tools/call', { name: 'browser_eval', arguments: { tabId: wcId, expression: 'document.title' } })) as {
      content: Array<{ text: string }>;
    };
    expect(evaluated.content[0]?.text).toBe('"E2E Action Fixture"');

    // The audit ring: every action call recorded, typed text redacted, hub
    // mirror 'skipped' (no hub is signed in inside the e2e instance).
    const audit = await page.evaluate(() =>
      window.__ELECTRON_BRIDGE__?.invoke<{ entries: AuditRow[] }>('browserbridge_audit_tail'),
    );
    const tools = (audit?.entries ?? []).map((e) => e.tool);
    for (const wanted of ['browser_navigate', 'browser_find_tab', 'browser_click', 'browser_type', 'browser_eval']) {
      expect(tools).toContain(wanted);
    }
    const typeEntry = audit?.entries.find((e) => e.tool === 'browser_type');
    expect(typeEntry?.args.text).toBe('<redacted 5 chars>');
    expect(JSON.stringify(audit?.entries ?? [])).not.toContain('hello');
    expect(typeEntry?.agent_id).toBe('e2e-agent-1');
    expect(typeEntry?.ok).toBe(true);
    expect(typeEntry?.hub).toBe('skipped');
    // The scope-refused call from the read relay never reached the audit hook.
    expect(audit?.entries.every((e) => e.agent_id === 'e2e-agent-1')).toBe(true);
  } finally {
    mcp?.close();
    mcpRead?.close();
    await new Promise<void>((r) => fixture.close(() => r()));
    try {
      await page.evaluate(() => window.__ELECTRON_BRIDGE__?.invoke('browserbridge_set_enabled', { enabled: false }));
    } catch {
      /* window already gone */
    }
  }
});
