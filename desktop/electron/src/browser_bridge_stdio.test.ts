/// Tests for the stdio relay's discovery-file fallback (D1 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.5): the matrix is
///   - env-set wins (the hostrunner-injected path never touches the file),
///   - discovery-present → url + READ token only — a sentinel action_token
///     in the fixture proves the fallback never resolves it,
///   - discovery-absent/malformed → clean exit (code 0, no stdout noise) so
///     kimi-code marks the server down, not failed,
///   - partial env (URL without token) keeps the old loud exit-2.
/// Each case spawns the real relay as a child process against a fake MCP
/// HTTP endpoint. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { AddressInfo } from 'node:net';

const RELAY = fileURLToPath(new URL('../resources/browser_bridge_stdio.mjs', import.meta.url));
const PING = '{"jsonrpc":"2.0","id":1,"method":"ping"}\n';

interface Captured {
  authorization: string | null;
  body: string;
  /// Plan lane B2: the relay stamps Mcp-Method/Mcp-Name so the desktop can
  /// classify and meter a call without parsing the body.
  headers: http.IncomingHttpHeaders;
}

/// A fake MCP endpoint: captures requests, answers every POST with a pong.
function fakeMcp(): Promise<{ url: string; requests: Captured[]; close: () => Promise<void> }> {
  const requests: Captured[] = [];
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (d: Buffer) => {
      body += d.toString('utf8');
    });
    req.on('end', () => {
      requests.push({ authorization: req.headers.authorization ?? null, body, headers: req.headers });
      let id: unknown = null;
      try {
        id = (JSON.parse(body) as { id?: unknown }).id ?? null;
      } catch {
        /* keep null */
      }
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ jsonrpc: '2.0', id, result: {} }));
    });
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo;
      resolve({
        url: `http://127.0.0.1:${String(port)}/mcp`,
        requests,
        close: () =>
          new Promise<void>((r) => {
            server.close(() => r());
          }),
      });
    });
  });
}

interface RelayRun {
  code: number | null;
  stdout: string;
  stderr: string;
}

/// Spawn the relay with a from-scratch env (no inherited TP_BROWSER_*). When
/// `ping` is set, one ping frame is written and stdin stays open until the
/// relayed answer arrives on stdout (ending stdin earlier would exit the
/// relay mid-request — rl close is process exit by design).
function runRelay(env: Record<string, string>, opts: { ping?: boolean; timeoutMs?: number; frame?: string } = {}): Promise<RelayRun> {
  const timeoutMs = opts.timeoutMs ?? 8000;
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [RELAY], {
      env: { PATH: process.env.PATH ?? '', ...env },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    let done = false;
    const killer = setTimeout(() => child.kill('SIGKILL'), timeoutMs);
    // The process may exit before we hang up (fallback paths) — an EPIPE on
    // stdin is expected there, not a test failure.
    child.stdin.on('error', () => undefined);
    child.stdout.on('data', (d: Buffer) => {
      stdout += d.toString('utf8');
      if (opts.ping === true && stdout.includes('\n')) child.stdin.end();
    });
    child.stderr.on('data', (d: Buffer) => {
      stderr += d.toString('utf8');
    });
    child.on('error', () => {
      if (!done) {
        done = true;
        clearTimeout(killer);
        resolve({ code: null, stdout, stderr });
      }
    });
    child.on('close', (code) => {
      if (!done) {
        done = true;
        clearTimeout(killer);
        resolve({ code, stdout, stderr });
      }
    });
    if (opts.ping === true) {
      child.stdin.write(opts.frame ?? PING);
    } else {
      child.stdin.end();
    }
  });
}

function writeDiscovery(dir: string, fields: Record<string, unknown>): string {
  const p = path.join(dir, 'browser-bridge.json');
  fs.writeFileSync(p, JSON.stringify(fields), { mode: 0o600 });
  return p;
}

test('relay: env-set wins — the discovery file is never consulted', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-relay-'));
  const envServer = await fakeMcp();
  const discServer = await fakeMcp();
  try {
    const discovery = writeDiscovery(dir, { url: discServer.url, token: 'disc-tok', action_token: 'disc-action' });
    const run = await runRelay(
      { TP_BROWSER_URL: envServer.url, TP_BROWSER_TOKEN: 'env-tok', TP_BROWSER_DISCOVERY: discovery },
      { ping: true },
    );
    assert.equal(run.code, 0, run.stderr);
    assert.ok(run.stdout.includes('"id":1'), `expected a relayed pong, got: ${run.stdout}`);
    assert.deepEqual(envServer.requests.map((r) => r.authorization), ['Bearer env-tok']);
    assert.equal(discServer.requests.length, 0, 'the env-injected path must not touch the discovery file');
  } finally {
    await envServer.close();
    await discServer.close();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('relay: discovery fallback resolves url + READ token only (sentinel action_token)', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-relay-'));
  const server = await fakeMcp();
  try {
    const discovery = writeDiscovery(dir, {
      url: server.url,
      token: 'read-tok',
      action_token: 'ACTION_SENTINEL',
      pid: process.pid,
      started_at: new Date().toISOString(),
      app_version: '0.0.0-test',
      bridge_path: RELAY,
    });
    const run = await runRelay({ TP_BROWSER_DISCOVERY: discovery }, { ping: true });
    assert.equal(run.code, 0, run.stderr);
    assert.ok(run.stdout.includes('"id":1'), `expected a relayed pong, got: ${run.stdout}`);
    assert.equal(server.requests.length, 1);
    assert.equal(server.requests[0]?.authorization, 'Bearer read-tok');
    const everywhere = JSON.stringify(server.requests) + run.stdout + run.stderr;
    assert.ok(!everywhere.includes('ACTION_SENTINEL'), 'the fallback must never resolve action_token');
  } finally {
    await server.close();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('relay: discovery absent → clean exit 0, no stdout noise', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-relay-'));
  try {
    const run = await runRelay({ TP_BROWSER_DISCOVERY: path.join(dir, 'missing.json') });
    assert.equal(run.code, 0, `a down bridge must be a quiet exit, got code ${String(run.code)}: ${run.stderr}`);
    assert.equal(run.stdout, '', 'stdout must stay clean — it is the JSON-RPC channel');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('relay: discovery malformed → clean exit 0', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-relay-'));
  try {
    for (const bad of ['garbage{', '{"url":"http://x/"}', '{"url":42,"token":"t"}']) {
      const discovery = path.join(dir, 'browser-bridge.json');
      fs.writeFileSync(discovery, bad);
      const run = await runRelay({ TP_BROWSER_DISCOVERY: discovery });
      assert.equal(run.code, 0, `malformed discovery (${bad}) must exit quietly: ${run.stderr}`);
      assert.equal(run.stdout, '');
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('relay: partial env (URL without token) keeps the loud exit-2', async () => {
  const run = await runRelay({ TP_BROWSER_URL: 'http://127.0.0.1:1/mcp' });
  assert.equal(run.code, 2);
  assert.match(run.stderr, /TP_BROWSER_URL and TP_BROWSER_TOKEN are required/);
});

// ── Lane B2: standard MCP request headers on the relay's HTTP leg ────────────

test('relay: stamps Mcp-Method, and Mcp-Name only on a tools/call', async () => {
  const server = await fakeMcp();
  try {
    // A method that names no tool must not carry Mcp-Name — a classifier
    // reading it would attribute the frame to some unrelated tool.
    const pinged = await runRelay({ TP_BROWSER_URL: server.url, TP_BROWSER_TOKEN: 'tok' }, { ping: true });
    assert.equal(pinged.code, 0, pinged.stderr);
    assert.equal(server.requests[0]?.headers['mcp-method'], 'ping');
    assert.equal(server.requests[0]?.headers['mcp-name'], undefined);

    const called = await runRelay(
      { TP_BROWSER_URL: server.url, TP_BROWSER_TOKEN: 'tok' },
      { ping: true, frame: `${JSON.stringify({ jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'browser_click', arguments: {} } })}\n` },
    );
    assert.equal(called.code, 0, called.stderr);
    assert.equal(server.requests[1]?.headers['mcp-method'], 'tools/call');
    assert.equal(server.requests[1]?.headers['mcp-name'], 'browser_click');
  } finally {
    await server.close();
  }
});

// The relay is a byte pump, not a validator: the desktop is the authority on
// what is valid JSON-RPC. A frame we cannot parse still goes through, just
// unlabelled — rejecting it here would put a second, weaker parser in the path.
test('relay: an unparseable frame is forwarded verbatim and unstamped', async () => {
  const server = await fakeMcp();
  try {
    const run = await runRelay({ TP_BROWSER_URL: server.url, TP_BROWSER_TOKEN: 'tok' }, { ping: true, frame: '{not json\n' });
    assert.equal(run.code, 0, run.stderr);
    assert.equal(server.requests[0]?.body, '{not json');
    assert.equal(server.requests[0]?.headers['mcp-method'], undefined);
  } finally {
    await server.close();
  }
});

// A PARSEABLE frame whose tool name cannot live in an HTTP header value must
// still reach the desktop: Node's client throws on control bytes at send
// time, so stamping such a name verbatim would kill the frame at the relay
// with a transport error, when the desktop would have answered it with a
// proper JSON-RPC error. The valid method is still stamped; only the
// unstampable name is skipped.
test('relay: a header-hostile tool name is skipped, not fatal', async () => {
  const server = await fakeMcp();
  try {
    const frame = `${JSON.stringify({ jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'evil\r\nX-Inject: 1', arguments: {} } })}\n`;
    const run = await runRelay({ TP_BROWSER_URL: server.url, TP_BROWSER_TOKEN: 'tok' }, { ping: true, frame });
    assert.equal(run.code, 0, run.stderr);
    assert.equal(server.requests[0]?.headers['mcp-method'], 'tools/call');
    assert.equal(server.requests[0]?.headers['mcp-name'], undefined);
    assert.match(run.stdout, /"id":3/, 'the frame must be answered by the server, not die at the relay');
  } finally {
    await server.close();
  }
});
