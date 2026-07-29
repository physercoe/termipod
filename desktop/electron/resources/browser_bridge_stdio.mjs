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
///   TP_BROWSER_TOKEN  — per-app-run bearer
///   TP_BROWSER_SCOPE  — 'read' (W1) | 'action' (W2 spawn opt-in); forwarded
///                       as a header, the desktop enforces the tool set.
import http from 'node:http';
import readline from 'node:readline';

const URL_ = process.env.TP_BROWSER_URL;
const TOKEN = process.env.TP_BROWSER_TOKEN;
const SCOPE = process.env.TP_BROWSER_SCOPE ?? 'read';
const TIMEOUT_MS = 30_000;

// stderr is the only place we can log — stdout is the JSON-RPC channel and a
// stray line there corrupts the stream.
function log(...args) {
  console.error('browser-bridge', ...args);
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
