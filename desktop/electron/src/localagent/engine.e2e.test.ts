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
import { parseResumeTable } from './resumerecipes.ts';
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

const RESOURCES = path.join(import.meta.dirname, '..', '..', 'resources');
const ARTIFACT = path.join(RESOURCES, 'agent_families.generated.json');
const RESUME_ARTIFACT = path.join(RESOURCES, 'resume_recipes.generated.json');

/// A service over a given data directory. Two of these over the SAME directory
/// is what an app restart looks like from here.
function realService(dataDir: string): LocalAgentService {
  return new LocalAgentService({
    families: parseFamilies(readFileSync(ARTIFACT, 'utf-8')),
    resumeTable: parseResumeTable(readFileSync(RESUME_ARTIFACT, 'utf-8')),
    env: process.env,
    homeDir: os.homedir(),
    dataDir,
  });
}

/// Resolve when a session emits `turn.result`.
function turnDone(service: LocalAgentService, timeoutMs = 180_000): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`no turn.result within ${timeoutMs}ms`)), timeoutMs);
    service.subscribe((_id, ev) => {
      if (ev.kind === 'turn.result') {
        clearTimeout(timer);
        resolve();
      }
    });
  });
}

test('a real claude session runs end to end through the service', { skip: skipReason() }, async () => {
  const service = realService(mkdtempSync(path.join(tmpdir(), 'termipod-l3a-data-')));

  const seen: LocalAgentEvent[] = [];
  service.subscribe((_id, ev) => seen.push(ev));

  const session = service.create({
    cwd: mkdtempSync(path.join(tmpdir(), 'termipod-l3a-')),
    posture: 'converse',
  });

  const done = turnDone(service);

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

test('a session survives a restart: the transcript reloads and the engine remembers', { skip: skipReason() }, async () => {
  // The whole of L3b, against the real binary. Two independent services over
  // one data directory, exactly as two runs of the app would be.
  //
  // The two halves are asserted separately on purpose, because they fail
  // separately: the engine can remember while the transcript is lost (native
  // resume alone — which emits no replay), and the transcript can reload while
  // the engine forgets (a rebind that quietly cold-starts, which is the silent
  // failure recipes.yaml warns about).
  const dataDir = mkdtempSync(path.join(tmpdir(), 'termipod-l3b-data-'));
  const cwd = mkdtempSync(path.join(tmpdir(), 'termipod-l3b-cwd-'));
  const codeword = 'ZEPPELIN-42';

  // ── run one ──
  const first = realService(dataDir);
  const session = first.create({ cwd, posture: 'converse' });
  const firstTurn = turnDone(first);
  first.input(session.id, 'text', { body: `Remember this codeword: ${codeword}. Reply with just the word OK.` });
  try {
    await firstTurn;
  } finally {
    first.disposeAll();
  }

  const engineId = first.get(session.id)?.engine_session_id;
  assert.equal(typeof engineId, 'string');
  const beforeCursor = first.history(session.id).cursor;
  assert.ok(beforeCursor > 0);

  // ── run two ──
  const second = realService(dataDir);
  const report = second.reload();
  assert.deepEqual(report.restored, [session.id], `reload did not restore the session: ${JSON.stringify(report)}`);
  assert.deepEqual(report.unreadable, []);

  const restored = second.get(session.id);
  assert.equal(restored?.status, 'stopped');
  assert.equal(restored?.restored, true);
  assert.equal(restored?.engine_session_id, engineId, 'the resume handle must survive the restart');

  // Half one: the VIEW. What was said before is readable, with its numbering.
  // `text ?? body` because both sides of the conversation are asserted here and
  // they use different keys — the agent's rows carry `text`, the director's own
  // `input.text` row carries `body`, which is the hub's field name. EventCard
  // reads exactly this pair.
  const reloaded = second.history(session.id);
  const said = (e: LocalAgentEvent): string => String(e.payload.text ?? e.payload.body ?? '');
  const saidBefore = reloaded.events.map(said).join(' ');
  assert.match(saidBefore, new RegExp(codeword),
    'the transcript from before the restart is gone; native resume would not have restored it');
  assert.ok(reloaded.events.some((e) => e.kind === 'input.text'),
    "the director's own message is missing from the reloaded transcript");
  assert.equal(reloaded.cursor, beforeCursor, 'numbering must continue, not restart');

  // Half two: the MEMORY. Ask for something only the prior conversation knows.
  const secondTurn = turnDone(second);
  second.input(session.id, 'text', { body: 'What codeword did I ask you to remember? Reply with just the codeword.' });
  try {
    await secondTurn;
  } finally {
    second.disposeAll();
  }

  const after = second.history(session.id);
  // The AGENT's rows only — `kind === 'text'`, which excludes the `input.text`
  // row carrying the question. Without that filter this would pass on the
  // codeword we just typed rather than the one the engine recalled.
  const answered = after.events
    .filter((e) => e.seq > beforeCursor && e.kind === 'text')
    .map((e) => String(e.payload.text ?? ''))
    .join(' ');
  assert.match(answered, new RegExp(codeword),
    `the rebound engine did not recall the conversation — it cold-started. Reply: ${JSON.stringify(answered)}`);

  // And the rebind said so in the transcript.
  const starts = after.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'started');
  assert.equal(starts.length, 2);
  assert.equal(starts[1].payload.resumed, true);

  // Still nothing unmodelled, on the resume path too.
  const raw = after.events.filter((e) => e.kind === 'raw');
  assert.deepEqual(raw, [], `unmodelled frames on the resume path: ${JSON.stringify(raw.slice(0, 3))}`);
});

function skipReason(): string | false {
  if (!ENABLED) return 'set TERMIPOD_LOCAL_ENGINE_E2E=1 to run (spends real tokens)';
  if (!claudeAvailable()) return 'claude is not on PATH';
  return false;
}
