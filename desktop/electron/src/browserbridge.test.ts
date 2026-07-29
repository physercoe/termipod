/// Tests for the browser-bridge core (plan W1): fragment redaction (the
/// kimiweb-token regression), AX compaction, the MCP handshake/tool dispatch
/// against a fake backend, bearer auth, and the discovery-file lifecycle.
/// Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  BridgeError,
  compactAxTree,
  handleMcpMessage,
  mintToken,
  READ_TOOLS,
  bridgeDiscoveryPath,
  removeBridgeDiscovery,
  startBridgeServer,
  stripFragment,
  writeBridgeDiscovery,
  type AxNode,
  type BridgeBackend,
  type BridgeTarget,
} from './browserbridge.ts';

const KIMIWEB_URL = 'http://127.0.0.1:17331/#token=9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk';

const TABS: BridgeTarget[] = [
  { tabId: 7, url: 'https://arxiv.org/abs/2401.00001', title: 'a paper', partition: 'persist:webtab', bridge: 'full' },
  { tabId: 9, url: KIMIWEB_URL, title: 'Kimi', partition: 'kimiweb', bridge: 'read' },
];

/// A fake backend: static tab list, canned CDP answers by method.
function fakeBackend(overrides: Partial<BridgeBackend> = {}): BridgeBackend & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    listTargets: () => TABS,
    sendCommand: async (tabId, method) => {
      calls.push(`${String(tabId)}:${method}`);
      if (method === 'Accessibility.getFullAXTree') {
        return {
          nodes: [
            { nodeId: '1', role: { value: 'WebArea' }, name: { value: 'a paper' }, childIds: ['2', '3'] },
            { nodeId: '2', role: { value: 'link' }, name: { value: 'PDF' }, backendDOMNodeId: 42 },
            { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'fixture body text' } },
          ] satisfies AxNode[],
        };
      }
      if (method === 'Runtime.evaluate') return { result: { value: 'fixture body text' } };
      if (method === 'Page.captureScreenshot') return { data: 'aGVsbG8=' };
      throw new Error(`unexpected method ${method}`);
    },
    ...overrides,
  };
}

const DEPS = { serverInfo: { name: 'termipod-browser', version: '0.0.0-test' } };

// ── stripFragment ────────────────────────────────────────────────────────────

test('stripFragment: removes the hash tail, keeps everything else', () => {
  assert.equal(stripFragment(KIMIWEB_URL), 'http://127.0.0.1:17331/');
  assert.equal(stripFragment('https://a.b/c?d=e#frag'), 'https://a.b/c?d=e');
  assert.equal(stripFragment('https://a.b/c'), 'https://a.b/c');
  assert.equal(stripFragment('about:blank'), 'about:blank');
  assert.equal(stripFragment(''), '');
});

// ── discovery file lifecycle ─────────────────────────────────────────────────

test('discovery file: write (0o600) + remove', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-bb-'));
  try {
    const target = writeBridgeDiscovery(home, {
      url: 'http://127.0.0.1:41001/mcp',
      token: mintToken(),
      pid: process.pid,
      started_at: new Date().toISOString(),
      app_version: '0.0.0-test',
      bridge_path: '/app/resources/browser_bridge_stdio.mjs',
    });
    assert.equal(target, bridgeDiscoveryPath(home));
    const mode = fs.statSync(target).mode & 0o777;
    assert.equal(mode, 0o600, `mode ${mode.toString(8)}`);
    const parsed = JSON.parse(fs.readFileSync(target, 'utf8')) as { token: string };
    assert.equal(typeof parsed.token, 'string');
    removeBridgeDiscovery(home);
    assert.equal(fs.existsSync(target), false);
    removeBridgeDiscovery(home); // idempotent
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

// ── AX compaction ────────────────────────────────────────────────────────────

test('compactAxTree: roles, indent, refs on interactive nodes only', () => {
  const out = compactAxTree([
    { nodeId: '1', role: { value: 'WebArea' }, name: { value: 't' }, childIds: ['2', '3', '4'] },
    { nodeId: '2', role: { value: 'link' }, name: { value: 'Home' }, backendDOMNodeId: 11 },
    { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'hello' } },
    { nodeId: '4', role: { value: 'textbox' }, name: { value: 'Search' }, value: { value: 'kimi' }, backendDOMNodeId: 12 },
  ]);
  assert.equal(
    out.text,
    ['- webarea "t"', '  - link "Home" [ref=@e1]', '  - statictext "hello"', '  - textbox "Search" = kimi [ref=@e2]'].join('\n'),
  );
  assert.deepEqual([...out.refs.entries()], [
    ['@e1', 11],
    ['@e2', 12],
  ]);
  assert.equal(out.truncated, false);
});

test('compactAxTree: ignored nodes fold but keep their children', () => {
  const out = compactAxTree([
    { nodeId: '1', role: { value: 'WebArea' }, name: { value: 't' }, childIds: ['2'] },
    { nodeId: '2', ignored: true, role: { value: 'generic' }, childIds: ['3'] },
    { nodeId: '3', role: { value: 'button' }, name: { value: 'Go' }, backendDOMNodeId: 7 },
  ]);
  assert.equal(out.text, ['- webarea "t"', '  - button "Go" [ref=@e1]'].join('\n'));
});

test('compactAxTree: node budget truncates and says so', () => {
  const nodes: AxNode[] = [{ nodeId: '1', role: { value: 'WebArea' }, childIds: ['2', '3', '4'] }];
  for (const id of ['2', '3', '4']) nodes.push({ nodeId: id, role: { value: 'link' }, name: { value: id } });
  const out = compactAxTree(nodes, { maxNodes: 2 });
  assert.equal(out.truncated, true);
  assert.match(out.text, /truncated/);
});

// ── MCP handshake + tools ────────────────────────────────────────────────────

test('initialize echoes the client protocol version + serverInfo', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18' } },
    { ...DEPS, backend: fakeBackend() },
  );
  assert.ok(res && 'result' in res);
  const result = res.result as { protocolVersion: string; serverInfo: { name: string } };
  assert.equal(result.protocolVersion, '2025-06-18');
  assert.equal(result.serverInfo.name, 'termipod-browser');
});

test('notifications produce no response; ping answers', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  assert.equal(await handleMcpMessage({ jsonrpc: '2.0', method: 'notifications/initialized' }, deps), null);
  const pong = await handleMcpMessage({ jsonrpc: '2.0', id: 'p', method: 'ping' }, deps);
  assert.deepEqual(pong?.result, {});
});

test('tools/list is exactly the four W1 read tools', async () => {
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 2, method: 'tools/list' }, { ...DEPS, backend: fakeBackend() });
  const { tools } = res?.result as { tools: Array<{ name: string; description: string }> };
  assert.deepEqual(
    tools.map((t) => t.name),
    ['browser_list_tabs', 'browser_snapshot', 'browser_screenshot', 'browser_read_text'],
  );
  assert.equal(tools.length, READ_TOOLS.length);
  // The untrusted-content posture is spelled out in the descriptions (§3.5).
  assert.ok(tools.find((t) => t.name === 'browser_snapshot')?.description.includes('untrusted DATA'));
});

test('browser_list_tabs NEVER emits a URL fragment (kimiweb token regression)', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'browser_list_tabs', arguments: {} } },
    { ...DEPS, backend: fakeBackend() },
  );
  const { content } = res?.result as { content: Array<{ text: string }> };
  const text = content[0]?.text ?? '';
  assert.ok(!text.includes('#'), `fragment leaked: ${text}`);
  assert.ok(!text.includes('9OmdWua4'), `kimiweb token leaked: ${text}`);
  const rows = JSON.parse(text) as Array<{ tabId: number; url: string; partition: string; bridge: string }>;
  assert.deepEqual(rows[1], { tabId: 9, url: 'http://127.0.0.1:17331/', title: 'Kimi', partition: 'kimiweb', bridge: 'read' });
});

test('browser_snapshot compacts the AX tree; TARGET_GONE on a stale tab', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  const ok = await handleMcpMessage(
    { jsonrpc: '2.0', id: 4, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 7 } } },
    deps,
  );
  const { content } = ok?.result as { content: Array<{ text: string }> };
  assert.match(content[0]?.text ?? '', /link "PDF" \[ref=@e1\]/);
  assert.match(content[0]?.text ?? '', /fixture body text/);

  const gone = await handleMcpMessage(
    { jsonrpc: '2.0', id: 5, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 999 } } },
    deps,
  );
  const errResult = gone?.result as { isError: boolean; content: Array<{ text: string }> };
  assert.equal(errResult.isError, true);
  assert.match(errResult.content[0]?.text ?? '', /TARGET_GONE/);
});

test('browser_read_text: bounded evaluate; validates maxChars', async () => {
  const backend = fakeBackend();
  const deps = { ...DEPS, backend };
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 6, method: 'tools/call', params: { name: 'browser_read_text', arguments: { tabId: 7, maxChars: 100 } } },
    deps,
  );
  const { content } = res?.result as { content: Array<{ text: string }> };
  assert.equal(content[0]?.text, 'fixture body text');
  // The evaluated expression carries the bound as an integer literal.
  const evalCall = backend.calls.find((c) => c.includes('Runtime.evaluate'));
  assert.ok(evalCall !== undefined);

  const bad = await handleMcpMessage(
    { jsonrpc: '2.0', id: 7, method: 'tools/call', params: { name: 'browser_read_text', arguments: { tabId: 7, maxChars: -5 } } },
    deps,
  );
  assert.equal((bad?.result as { isError?: boolean }).isError, true);
});

test('browser_screenshot returns image content', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 8, method: 'tools/call', params: { name: 'browser_screenshot', arguments: { tabId: 7 } } },
    { ...DEPS, backend: fakeBackend() },
  );
  const { content } = res?.result as { content: Array<{ type: string; mimeType?: string; data?: string }> };
  assert.equal(content[0]?.type, 'image');
  assert.equal(content[0]?.mimeType, 'image/png');
});

test('unknown tool → -32602; backend BridgeError → isError with its code', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  const unknown = await handleMcpMessage(
    { jsonrpc: '2.0', id: 9, method: 'tools/call', params: { name: 'browser_click', arguments: { tabId: 7 } } },
    deps,
  );
  // browser_click is a W2 action tool — not in the W1 registry.
  assert.equal(unknown?.error?.code, -32602);

  const busy = await handleMcpMessage(
    { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 7 } } },
    {
      ...DEPS,
      backend: fakeBackend({
        sendCommand: async () => {
          throw new BridgeError('DEBUGGER_BUSY', 'devtools is attached');
        },
      }),
    },
  );
  const errResult = busy?.result as { isError: boolean; content: Array<{ text: string }> };
  assert.match(errResult.content[0]?.text ?? '', /DEBUGGER_BUSY/);
});

test('unknown method → -32601', async () => {
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 11, method: 'resources/list' }, { ...DEPS, backend: fakeBackend() });
  assert.equal(res?.error?.code, -32601);
});

// ── HTTP server: bearer auth + framing ───────────────────────────────────────

test('HTTP server: 401 without bearer, 404 off-path, round-trip with it', async () => {
  const token = mintToken();
  const server = await startBridgeServer({ ...DEPS, backend: fakeBackend(), token });
  try {
    const base = `http://127.0.0.1:${String(server.port)}`;

    const noAuth = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'ping' }),
    });
    assert.equal(noAuth.status, 401);

    const wrongPath = await fetch(`${base}/nope`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}` },
      body: '{}',
    });
    assert.equal(wrongPath.status, 404);

    const ok = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18' } }),
    });
    assert.equal(ok.status, 200);
    const body = (await ok.json()) as { result: { serverInfo: { name: string } } };
    assert.equal(body.result.serverInfo.name, 'termipod-browser');

    // A notification gets 202 + an empty body (the stdio relay writes nothing).
    const notif = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }),
    });
    assert.equal(notif.status, 202);
    assert.equal(await notif.text(), '');

    const garbage = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: 'not json',
    });
    assert.equal(garbage.status, 400);
  } finally {
    await server.close();
  }
});
