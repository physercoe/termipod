/// End-to-end against a REAL codex app-server (vision-parity L4b).
///
/// The unit tests fake both transports, which proves the plumbing and proves
/// nothing about the handshake — and the handshake is exactly where L4a went
/// wrong. It shipped `codex app-server proxy --sock <path>` as the daemon rung
/// on the strength of that subcommand's help text; against a live daemon the
/// proxy relays raw bytes without the WebSocket upgrade the control socket
/// requires, so the socket closes and the proxy exits **0 with no output**. A
/// dead channel that looks exactly like a quiet agent.
///
/// So this file exists to ask the one question no fixture can answer: does a
/// frame we send come back answered?
///
/// **Opt-in.** It brings up a shared background daemon on the operator's
/// machine — cheap (no model turn, no tokens) but a real side effect. So:
///
///     TERMIPOD_CODEX_ATTACH_E2E=1 npm test
///
/// If the daemon was not already running, the test stops it again afterwards
/// and leaves the box as it found it. Skipped without the variable, and skipped
/// with a clear reason when codex is absent or was not installed by the
/// official installer script (no managed standalone → no daemon rung at all,
/// which is the common case and not a failure).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';

import {
  codexBinary,
  managedCodexPath,
  codexHome,
  planCodexAttach,
} from './codexattach.ts';
import { openCodexChannel, type CodexFrame } from './codexchannel.ts';

const ENABLED = process.env['TERMIPOD_CODEX_ATTACH_E2E'] === '1';

const HOME = os.homedir();
const CHOME = codexHome(HOME, process.env);
const BIN = codexBinary(HOME, process.env);

function managedInstallPresent(): boolean {
  return fs.existsSync(managedCodexPath(CHOME));
}

/// Is the daemon up right now? `daemon version` is the vendor's own client for
/// the control socket, so this is their answer, not ours.
function daemonRunning(): boolean {
  try {
    const out = execFileSync(BIN, ['app-server', 'daemon', 'version'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
      timeout: 30_000,
    });
    return (JSON.parse(out) as { status?: string }).status === 'running';
  } catch {
    return false;
  }
}

function skipReason(): string | null {
  if (!ENABLED) return 'set TERMIPOD_CODEX_ATTACH_E2E=1 to run the live codex attach test';
  if (BIN === 'codex') {
    try {
      execFileSync('codex', ['--version'], { stdio: 'ignore', timeout: 30_000 });
    } catch {
      return 'codex is not installed';
    }
  }
  if (!managedInstallPresent()) {
    return `no installer-managed codex at ${managedCodexPath(CHOME)} — this box has no daemon rung`;
  }
  return null;
}

test('the daemon rung carries a real JSON-RPC round trip', async (t) => {
  const skip = skipReason();
  if (skip !== null) {
    t.skip(skip);
    return;
  }

  const wasRunning = daemonRunning();
  const plan = planCodexAttach(HOME, { managedInstallPresent: true, bin: BIN }, process.env);
  assert.equal(plan.mode, 'daemon', 'a managed install must take the daemon rung');

  const frames: CodexFrame[] = [];
  let resolveInit: (() => void) | undefined;
  const initialized = new Promise<void>((r) => {
    resolveInit = r;
  });

  const channel = await openCodexChannel(
    plan,
    {
      onFrame: (f) => {
        frames.push(f);
        if (f['id'] === 1 && f['result'] !== undefined) resolveInit?.();
      },
      onClose: () => {},
    },
    { cwd: process.cwd(), connectTimeoutMs: 30_000 },
  );

  try {
    // If this reports 'spawn' we fell back, which means the socket handshake
    // failed — the precise failure L4a shipped and could not see.
    assert.equal(channel.mode, 'daemon', `expected the daemon rung, got ${channel.mode}: ${channel.reason}`);

    channel.send({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: { clientInfo: { name: 'termipod', title: 'TermiPod', version: '0.1.0' } },
    });

    await Promise.race([
      initialized,
      new Promise((_, reject) => setTimeout(() => reject(new Error('no initialize result within 30s')), 30_000)),
    ]);

    const result = frames.find((f) => f['id'] === 1)?.['result'] as Record<string, unknown> | undefined;
    assert.ok(result !== undefined, 'initialize must be answered');
    // The server's own view of where it lives — proof the answer came from a
    // real app-server and not from an echo.
    assert.equal(result['codexHome'], CHOME);
  } finally {
    channel.close();
    if (!wasRunning) {
      try {
        execFileSync(BIN, ['app-server', 'daemon', 'stop'], { stdio: 'ignore', timeout: 30_000 });
      } catch {
        /* best effort — leaving a daemon up is untidy, not broken */
      }
    }
  }
});

test('closing our channel leaves the shared daemon running', async (t) => {
  const skip = skipReason();
  if (skip !== null) {
    t.skip(skip);
    return;
  }

  const wasRunning = daemonRunning();
  const plan = planCodexAttach(HOME, { managedInstallPresent: true, bin: BIN }, process.env);
  const channel = await openCodexChannel(
    plan,
    { onFrame: () => {}, onClose: () => {} },
    { cwd: process.cwd(), connectTimeoutMs: 30_000 },
  );
  channel.close();

  // The rung's entire promise is that the session outlives the app. If closing
  // a client took the daemon down, every "your session survives" claim in the
  // UI would be false.
  assert.equal(daemonRunning(), true, 'the daemon must survive a client disconnect');

  if (!wasRunning) {
    try {
      execFileSync(BIN, ['app-server', 'daemon', 'stop'], { stdio: 'ignore', timeout: 30_000 });
    } catch {
      /* best effort */
    }
  }
});
