/// End-to-end against a REAL claude binary (vision-parity L3a).
///
/// Every other test in this directory fakes the process, which proves the
/// plumbing and proves nothing about the engine. This one spawns the actual
/// CLI and asserts that a session started by the service produces a usable
/// transcript — the question no fixture can answer, and the one the pane-state
/// lane could never ask because no engine was installed on that box.
///
/// **Opt-in, and deliberately so.** It runs a real model turn, which costs the
/// operator tokens and takes tens of seconds. A suite that quietly spent
/// someone's quota on every `npm test` would be a bad trade for a signal they
/// did not ask for. So:
///
///     TERMIPOD_LOCAL_ENGINE_E2E=1 npm test
///
/// Skipped without that variable, and skipped with a clear reason when `claude`
/// is not on PATH.
///
/// The posture is `converse` — no tools at all. The engine is being asked
/// whether it can hold a session, not to touch the machine running CI.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import os from 'node:os';
import path from 'node:path';

import { parseFamilies } from './families.ts';
import { LocalAgentService } from './service.ts';
import type { LocalAgentEvent } from './log.ts';

const ENABLED = process.env.TERMIPOD_LOCAL_ENGINE_E2E === '1';

function claudeAvailable(): boolean {
  try {
    execFileSync('claude', ['--version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

const ARTIFACT = path.join(import.meta.dirname, '..', '..', 'resources', 'agent_families.generated.json');

test('a real claude session runs end to end through the service', { skip: skipReason() }, async () => {
  const service = new LocalAgentService({
    families: parseFamilies(readFileSync(ARTIFACT, 'utf-8')),
    env: process.env,
    homeDir: os.homedir(),
  });

  const seen: LocalAgentEvent[] = [];
  service.subscribe((_id, ev) => seen.push(ev));

  const session = service.create({
    cwd: mkdtempSync(path.join(tmpdir(), 'termipod-l3a-')),
    posture: 'converse',
  });

  const done = new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('no turn.result within 180s')), 180_000);
    service.subscribe((_id, ev) => {
      if (ev.kind === 'turn.result') {
        clearTimeout(timer);
        resolve();
      }
    });
  });

  service.input(session.id, 'text', { body: 'Reply with exactly: PONG' });
  try {
    await done;
  } finally {
    service.disposeAll();
  }

  const kinds = seen.map((e) => e.kind);

  // The engine identified itself. Without this the profile never matched a
  // frame and everything below could be passing on `raw` rows.
  assert.equal(kinds.includes('session.init'), true, `no session.init in ${JSON.stringify(kinds)}`);

  // The service captured the engine's own session id — the handle L3b's
  // native resume looks a recipe up by.
  const engineId = service.get(session.id)?.engine_session_id;
  assert.equal(typeof engineId, 'string');
  assert.notEqual(engineId, '');

  // The model actually answered, through the profile, as typed text.
  const text = seen.filter((e) => e.kind === 'text').map((e) => String(e.payload.text ?? '')).join(' ');
  assert.match(text, /PONG/i, `model reply missing from ${JSON.stringify(text)}`);

  // Our own turn boundary bracketed it.
  assert.equal(kinds.includes('turn.start'), true);
  assert.equal(kinds.includes('turn.result'), true);

  // seq is dense and ordered — the cursor contract the renderer resumes on.
  assert.deepEqual(
    seen.map((e) => e.seq),
    seen.map((_e, i) => i + 1),
  );

  // Nothing fell through to the raw fallback. A `raw` row here means the
  // engine emitted a frame shape the vendored profile does not model — which
  // is exactly the drift this would otherwise discover in production.
  const raw = seen.filter((e) => e.kind === 'raw');
  assert.deepEqual(raw, [], `unmodelled frames reached the transcript: ${JSON.stringify(raw.slice(0, 3))}`);
});

function skipReason(): string | false {
  if (!ENABLED) return 'set TERMIPOD_LOCAL_ENGINE_E2E=1 to run (spends real tokens)';
  if (!claudeAvailable()) return 'claude is not on PATH';
  return false;
}
