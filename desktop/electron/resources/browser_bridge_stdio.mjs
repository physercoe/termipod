#!/usr/bin/env node
/// termipod-browser stdio ⇄ HTTP MCP relay (plan W1, §3.4).
///
/// The agent-side half of the desktop browser bridge: engine MCP configs can
/// only speak newline-delimited JSON-RPC on stdin/stdout, so the hostrunner
/// injects this script (`node browser_bridge_stdio.mjs`, env below) and it
/// relays each frame to the Electron main process's loopback MCP endpoint.
/// Plain node, zero dependencies — runs anywhere the engine fleet runs
/// (~10 MB, not a second Electron). Mirrors hub-mcp-bridge's shape exactly
/// (mcpbridge/run.go), including the codex/rmcp newline-normalization lesson.
///
/// Env (injected by the hostrunner from ~/.termipod/browser-bridge.json):
///   TP_BROWSER_URL    — full MCP endpoint, http://127.0.0.1:<port>/mcp
///   TP_BROWSER_TOKEN  — per-app-run bearer (read token, or the action token
///                       for spawns that opted in via browser_bridge: true)
///   TP_BROWSER_SCOPE  — 'read' | 'action' (W2 spawn opt-in); informational —
///                       the server derives scope from the bearer itself.
///   TP_BROWSER_AGENT_ID — the spawn's hub agent id; forwarded as
///                       x-tp-agent-id so the desktop's audit trail attributes
///                       every action call to the calling agent.
///   TP_BROWSER_DISCOVERY — discovery-file path override (tests); defaults to
///                       ~/.termipod/browser-bridge.json.
///
/// DISCOVERY-FILE FALLBACK (D1, desktop-ui-context plan §3.5): the user-level
/// ~/.kimi-code/mcp.json entry is STATIC — it carries no env, so when
/// TP_BROWSER_URL is unset the relay reads the per-run discovery file itself
/// and survives token rotation. Two pinned constraints:
///   - READ TOKEN ONLY: the file also carries action_token, which this path
///     NEVER resolves — action scope exists only on the env-injected path,
///     where the spawn opted in via browser_bridge: true (ADR-059 D-3);
///   - an absent/invalid file (bridge off) exits cleanly (code 0, no noise)
///     so kimi-code marks the server down, never failed.
import http from 'node:http';
import readline from 'node:readline';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

let URL_ = process.env.TP_BROWSER_URL;
let TOKEN = process.env.TP_BROWSER_TOKEN;
let SCOPE = process.env.TP_BROWSER_SCOPE ?? 'read';
const AGENT_ID = process.env.TP_BROWSER_AGENT_ID ?? '';
const TIMEOUT_MS = 30_000;

// stderr is the only place we can log — stdout is the JSON-RPC channel and a
// stray line there corrupts the stream.
function log(...args) {
  console.error('browser-bridge', ...args);
}

if (!URL_) {
  // Static-entry fallback: resolve the discovery file ourselves. `token` is
  // read, `action_token` is deliberately NOT — see the header.
  const discovery = process.env.TP_BROWSER_DISCOVERY ?? path.join(os.homedir(), '.termipod', 'browser-bridge.json');
  let d = null;
  try {
    d = JSON.parse(fs.readFileSync(discovery, 'utf8'));
  } catch {
    process.exit(0); // absent/unreadable — bridge off; a quiet down, not a failure
  }
  if (d === null || typeof d !== 'object' || typeof d.url !== 'string' || typeof d.token !== 'string' || d.url === '' || d.token === '') {
    process.exit(0); // malformed — same quiet down
  }
  URL_ = d.url;
  TOKEN = d.token;
  SCOPE = 'read';
}

if (!URL_ || !TOKEN) {
  log('TP_BROWSER_URL and TP_BROWSER_TOKEN are required');
  process.exit(2);
}

/// POST one JSON-RPC frame; resolve with the response body (null for 202/empty
/// — a notification answer) or reject on transport/HTTP failure.
function forward(line) {
  return new Promise((resolve, reject) => {
    const req = http.request(
      URL_,
      {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${TOKEN}`,
          'x-tp-browser-scope': SCOPE,
          ...(AGENT_ID !== '' ? { 'x-tp-agent-id': AGENT_ID } : {}),
        },
        timeout: TIMEOUT_MS,
      },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (d) => {
          body += d;
        });
        res.on('end', () => {
          if (res.statusCode >= 400) {
            reject(new Error(`${res.statusCode}: ${body.trim()}`));
          } else {
            resolve(body.trim() === '' ? null : body);
          }
        });
      },
    );
    req.on('timeout', () => req.destroy(new Error(`request timed out after ${TIMEOUT_MS}ms`)));
    req.on('error', reject);
    req.end(line);
  });
}

/// A JSON-RPC error frame for a transport failure, echoing the request id
/// (best-effort — an unparseable request gets no id).
function transportError(line, cause) {
  let id = null;
  try {
    const parsed = JSON.parse(line);
    if (parsed && typeof parsed === 'object' && 'id' in parsed) id = parsed.id;
  } catch {
    /* unparseable — id stays null */
  }
  return `${JSON.stringify({
    jsonrpc: '2.0',
    id,
    error: { code: -32000, message: 'browser-bridge transport error', data: String(cause) },
  })}\n`;
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });
rl.on('line', (line) => {
  if (line.trim() === '') return;
  forward(line)
    .then((body) => {
      if (body === null) return; // notification answer — no stdout frame
      // Exactly one trailing newline: strict MCP clients (codex's rmcp)
      // parse every stdout line and treat an empty line as fatal.
      process.stdout.write(`${body.replace(/[\r\n]+$/, '')}\n`);
    })
    .catch((e) => {
      log('forward error:', e instanceof Error ? e.message : e);
      process.stdout.write(transportError(line, e instanceof Error ? e.message : e));
    });
});
rl.on('close', () => process.exit(0));
