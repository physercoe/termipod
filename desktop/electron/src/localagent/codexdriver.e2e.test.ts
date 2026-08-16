/// End-to-end against a REAL codex app-server (vision-parity L4c).
///
/// The unit tests fake the channel, which proves the driver's bookkeeping and
/// proves nothing about the PROTOCOL: whether `thread/start` takes the params
/// we send it, whether `turn/start.input` accepts the blocks we build, whether
/// `thread/resume` finds the thread again. Every one of those is a claim about
/// a program we did not write, and this file is where they are measured.
///
/// It has already earned itself. The shapes the hub's Go driver has been
/// sending — `{type:"input_image", image_url}` and `{type:"input_file",
/// file_data}` — are answered by codex-cli 0.147.0 with `-32600 unknown
/// variant`, which fails the whole `turn/start`. A fake server accepts anything
/// and had pinned the wrong shape for as long as it shipped.
///
/// **Opt-in, because this one costs tokens** — it runs real model turns against
/// the operator's codex account:
///
///     TERMIPOD_CODEX_DRIVER_E2E=1 npm test
///
/// Skipped without the variable, and skipped with a clear reason when codex is
/// absent or unauthenticated (neither is a failure of this code).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { codexBinary, codexHome } from './codexattach.ts';
import { CodexDriver } from './codexdriver.ts';
import type { DriverEvent } from './driver.ts';
import { parseFamilies, familyByName, type Family } from './families.ts';

const ENABLED = process.env['TERMIPOD_CODEX_DRIVER_E2E'] === '1';
const HOME = os.homedir();

function codexPresent(): boolean {
  const bin = codexBinary(HOME, process.env);
  return path.isAbsolute(bin) && fs.existsSync(bin);
}

function authPresent(): boolean {
  return fs.existsSync(path.join(codexHome(HOME, process.env), 'auth.json'));
}

/// The SHIPPED registry, not a fixture — the whole point is that the profile
/// the Companion translates codex frames with is the hub's own.
function codexFamily(): Family {
  const artifact = fileURLToPath(new URL('../../resources/agent_families.generated.json', import.meta.url));
  const fam = familyByName(parseFamilies(fs.readFileSync(artifact, 'utf-8')), 'codex');
  assert.ok(fam !== undefined, 'the generated registry has no codex family');
  return fam;
}

interface Live {
  driver: CodexDriver;
  events: DriverEvent[];
  /// Resolve when an event satisfying `pred` arrives, or reject on timeout.
  wait: (pred: (ev: DriverEvent) => boolean, what: string, ms?: number) => Promise<DriverEvent>;
}

function live(cwd: string, resumeThreadId?: string): Live {
  const events: DriverEvent[] = [];
  const waiters: Array<(ev: DriverEvent) => void> = [];
  const driver = new CodexDriver({
    family: codexFamily(),
    cwd,
    posture: 'read_local',
    env: process.env,
    homeDir: HOME,
    ...(resumeThreadId !== undefined ? { resumeThreadId } : {}),
    onEvent: (ev) => {
      events.push(ev);
      for (const w of [...waiters]) w(ev);
    },
  });
  return {
    driver,
    events,
    wait: (pred, what, ms = 90_000) =>
      new Promise<DriverEvent>((resolve, reject) => {
        const hit = events.find(pred);
        if (hit !== undefined) {
          resolve(hit);
          return;
        }
        const timer = setTimeout(() => reject(new Error(`timed out waiting for ${what}`)), ms);
        waiters.push((ev) => {
          if (!pred(ev)) return;
          clearTimeout(timer);
          resolve(ev);
        });
      }),
  };
}

const skip = (): string | false => {
  if (!ENABLED) return 'set TERMIPOD_CODEX_DRIVER_E2E=1 to run (spends tokens on a real turn)';
  if (!codexPresent()) return 'no codex binary on this host';
  if (!authPresent()) return 'codex is not authenticated on this host';
  return false;
};

test('a real turn round-trips through the shipped frame profile', async (t) => {
  const why = skip();
  if (why !== false) {
    t.skip(why);
    return;
  }
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), 'termipod-l4c-'));
  const l = live(cwd);
  try {
    l.driver.start();
    // `session.init` comes from the profile's `thread/started` rule — so this
    // assertion covers the handshake AND the translation in one.
    const init = await l.wait((e) => e.kind === 'session.init', 'session.init');
    const threadId = String(init.payload.session_id);
    assert.notEqual(threadId, '');

    // Long enough to outlive the 200 ms flush window. A short reply
    // legitimately produces NO partial — item/completed cancels the pending
    // flush, which is the designed behaviour, so asserting a partial on a
    // one-token answer would be asserting a race.
    l.driver.input('text', {
      body: 'Write four sentences about why streaming output matters, then end with exactly: L4C-OK',
    });
    const text = await l.wait(
      (e) => e.kind === 'text' && e.payload.partial !== true && String(e.payload.text).includes('L4C-OK'),
      'the final text event',
    );
    assert.match(String(text.payload.text), /L4C-OK/);

    // Deltas arrived and were throttled into partials, which is E3's whole
    // point: a unit test can prove the buffer, only this can prove codex
    // actually sends them.
    assert.ok(
      l.events.some((e) => e.kind === 'text' && e.payload.partial === true),
      'expected at least one streamed partial before the final',
    );

    // ── The measurement that matters most: resume finds the thread again ──
    l.driver.stop();
    const again = live(cwd, threadId);
    try {
      again.driver.start();
      const reinit = await again.wait((e) => e.kind === 'session.init', 'session.init after resume');
      assert.equal(reinit.payload.session_id, threadId);
      assert.equal(reinit.payload.resumed, true);

      again.driver.input('text', { body: 'What exact token did I ask you to reply with earlier? Answer with just that token.' });
      const memory = await again.wait(
        (e) => e.kind === 'text' && e.payload.partial !== true && String(e.payload.text).includes('L4C-OK'),
        'the resumed turn to remember the token',
      );
      // The engine remembers across a fresh app-server process — the same
      // property claude's `--resume` has, reached by a completely different
      // mechanism.
      assert.match(String(memory.payload.text), /L4C-OK/);
    } finally {
      again.driver.stop();
    }
  } finally {
    l.driver.stop();
    fs.rmSync(cwd, { recursive: true, force: true });
  }
});

test('an image reaches the model in the variant this build accepts', async (t) => {
  const why = skip();
  if (why !== false) {
    t.skip(why);
    return;
  }
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), 'termipod-l4c-img-'));
  const l = live(cwd);
  try {
    l.driver.start();
    await l.wait((e) => e.kind === 'session.init', 'session.init');
    // A 1×1 png of one OPAQUE pure-green pixel (colour type 2, no alpha
    // channel). Both halves of that are deliberate. Opaque, because the
    // obvious sample to reach for — the widely-copied 1×1 "red dot" — is
    // RGBA(255,0,0,127), and a half-transparent pixel gets described
    // differently depending on what the reader composites it against: the
    // same bytes drew "Light blue." from one turn and "red" from another.
    // Green, because red and blue are what a model guesses when it cannot see
    // the image at all, so an answer of "green" is evidence the bytes
    // ARRIVED rather than evidence the model is agreeable.
    l.driver.input('text', {
      body: 'Name the single colour of this 1x1 image in one word.',
      images: [
        {
          mime: 'image/png',
          data: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNg+M8AAAICAQB7CYF4AAAAAElFTkSuQmCC',
        },
      ],
    });
    const text = await l.wait(
      (e) => e.kind === 'text' && e.payload.partial !== true,
      'an answer about the image',
    );
    // If the block shape were wrong, `turn/start` would have failed with
    // `-32600 unknown variant` and this would be an `error` row instead.
    assert.equal(
      l.events.some((e) => e.kind === 'error' && /unknown variant/.test(String(e.payload.text))),
      false,
      'turn/start rejected the image block',
    );
    assert.match(String(text.payload.text).toLowerCase(), /green/);
  } finally {
    l.driver.stop();
    fs.rmSync(cwd, { recursive: true, force: true });
  }
});
