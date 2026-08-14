/// Tests for the on-disk session log (vision-parity L3b).
///
/// The interesting cases here are all recovery cases, and they are the reason
/// the parser is a pure function: a torn tail, a bounded read that starts
/// mid-line, a corrupt row in the middle. Each is a real thing that happens to
/// an append-only file on a machine that gets closed with the lid.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

import {
  DurableSessionLog,
  EVENTS_FILENAME,
  newestDenseRun,
  parseLogChunk,
} from './durablelog.ts';
import { SessionLog, type LocalAgentEvent } from './log.ts';

function tmp(): string {
  return mkdtempSync(path.join(tmpdir(), 'termipod-l3b-'));
}

function row(seq: number, kind = 'text'): LocalAgentEvent {
  return {
    id: `id-${seq}`,
    seq,
    session_ordinal: seq,
    ts: '2026-08-14T00:00:00.000Z',
    kind,
    producer: 'agent',
    payload: { text: `row ${seq}` },
  };
}

function lines(...rows: LocalAgentEvent[]): string {
  return rows.map((r) => JSON.stringify(r) + '\n').join('');
}

// ── parseLogChunk: pure recovery ───────────────────────────────────────────

test('parses a clean file', () => {
  const got = parseLogChunk(lines(row(1), row(2), row(3)), false);
  assert.equal(got.events.length, 3);
  assert.equal(got.tornTail, false);
  assert.equal(got.droppedInvalid, 0);
  assert.deepEqual(got.events.map((e) => e.seq), [1, 2, 3]);
});

test('recovers from a torn tail — the write that was in flight when we died', () => {
  const text = lines(row(1), row(2)) + '{"id":"id-3","seq":3,"ts":"2026';
  const got = parseLogChunk(text, false);
  assert.deepEqual(got.events.map((e) => e.seq), [1, 2]);
  assert.equal(got.tornTail, true, 'a partial trailing line must be reported, not silently dropped');
  assert.equal(got.droppedInvalid, 0, 'a torn tail is not corruption and must not be counted as such');
});

test('a mid-file read drops its first line on POSITION, not on parse success', () => {
  // The dangerous case: the leading fragment is itself valid JSON. Trusting the
  // parser here is how half a row becomes a plausible event with a real seq.
  const fragment = '"payload":{"text":"tail of a much larger row"}}\n';
  const got = parseLogChunk(fragment + lines(row(9)), true);
  assert.equal(got.droppedLeadingPartial, true);
  assert.deepEqual(got.events.map((e) => e.seq), [9]);
});

test('a whole-file read keeps its first line', () => {
  const got = parseLogChunk(lines(row(1), row(2)), false);
  assert.equal(got.droppedLeadingPartial, false);
  assert.deepEqual(got.events.map((e) => e.seq), [1, 2]);
});

test('a corrupt complete line is counted as corruption, distinctly from a torn tail', () => {
  const text = lines(row(1)) + 'not json at all\n' + lines(row(3));
  const got = parseLogChunk(text, false);
  assert.deepEqual(got.events.map((e) => e.seq), [1, 3]);
  assert.equal(got.droppedInvalid, 1);
  assert.equal(got.tornTail, false);
});

test('rows missing the fields the renderer reads are rejected', () => {
  const cases = [
    '{"seq":1,"ts":"t","kind":"text","producer":"agent","payload":{}}',           // no id
    '{"id":"a","ts":"t","kind":"text","producer":"agent","payload":{}}',          // no seq
    '{"id":"a","seq":0,"ts":"t","kind":"text","producer":"agent","payload":{}}',  // seq below 1
    '{"id":"a","seq":1.5,"ts":"t","kind":"text","producer":"agent","payload":{}}',// non-integer seq
    '{"id":"a","seq":1,"ts":"t","kind":"text","producer":"agent"}',               // no payload
    '{"id":"a","seq":1,"ts":"t","kind":"text","producer":"agent","payload":[]}',  // payload not an object
    '{"id":"a","seq":1,"ts":"t","kind":"text","payload":{}}',                     // no producer
  ];
  const got = parseLogChunk(cases.join('\n') + '\n', false);
  assert.equal(got.events.length, 0, 'a malformed row must not reach the renderer');
  assert.equal(got.droppedInvalid, cases.length);
});

test('session_ordinal defaults to seq when absent, since the renderer reads both', () => {
  const got = parseLogChunk('{"id":"a","seq":7,"ts":"t","kind":"text","producer":"agent","payload":{}}\n', false);
  assert.equal(got.events.length, 1);
  assert.equal(got.events[0].session_ordinal, 7);
});

test('an empty file yields nothing and reports nothing wrong', () => {
  const got = parseLogChunk('', false);
  assert.equal(got.events.length, 0);
  assert.equal(got.tornTail, false);
  assert.equal(got.droppedInvalid, 0);
});

// ── newestDenseRun ─────────────────────────────────────────────────────────

test('a gap truncates to the newest dense run rather than serving across it', () => {
  // 1,2 then 7,8: the renderer sorts by seq and would draw these as one
  // continuous conversation, inventing a jump the director cannot see.
  const got = newestDenseRun([row(1), row(2), row(7), row(8)]);
  assert.deepEqual(got.map((e) => e.seq), [7, 8]);
});

test('no gap keeps everything', () => {
  const got = newestDenseRun([row(4), row(5), row(6)]);
  assert.deepEqual(got.map((e) => e.seq), [4, 5, 6]);
});

test('unsorted input is sorted before the run is taken', () => {
  const got = newestDenseRun([row(3), row(1), row(2)]);
  assert.deepEqual(got.map((e) => e.seq), [1, 2, 3]);
});

test('an empty set is not an error', () => {
  assert.equal(newestDenseRun([]).length, 0);
});

// ── SessionLog.restore ─────────────────────────────────────────────────────

test('restore adopts seq instead of renumbering', () => {
  const log = SessionLog.restore([row(101), row(102), row(103)]);
  assert.equal(log.highWater, 103);
  const next = log.append({ id: 'x', ts: 't', kind: 'text', producer: 'agent', payload: {} });
  assert.equal(next.seq, 104, 'a reloaded log must continue its numbering, not restart it');
  assert.equal(next.session_ordinal, 104);
});

test('restore keeps a cursor from before the restart meaningful', () => {
  const log = SessionLog.restore([row(101), row(102), row(103)]);
  const page = log.since(101);
  assert.deepEqual(page.events.map((e) => e.seq), [102, 103]);
  assert.equal(page.resyncRequired, false);
});

test('restore past capacity keeps the newest and resyncs an evicted cursor', () => {
  const log = SessionLog.restore([row(1), row(2), row(3), row(4), row(5)], 2);
  assert.equal(log.size, 2);
  assert.equal(log.highWater, 5);
  const page = log.since(1);
  assert.equal(page.resyncRequired, true, 'a cursor older than what survived must not silently skip events');
});

test('restore of nothing is an empty log, not a broken one', () => {
  const log = SessionLog.restore([]);
  assert.equal(log.highWater, 0);
  assert.equal(log.append({ id: 'x', ts: 't', kind: 'text', producer: 'agent', payload: {} }).seq, 1);
});

// ── DurableSessionLog: the round trip ──────────────────────────────────────

test('events written in one process are readable in the next', () => {
  const dir = tmp();
  try {
    const a = DurableSessionLog.create(path.join(dir, 's1'));
    a.append({ id: 'e1', ts: 't1', kind: 'session.init', producer: 'agent', payload: { session_id: 'abc' } });
    a.append({ id: 'e2', ts: 't2', kind: 'text', producer: 'agent', payload: { text: 'hello' } });
    a.close();

    const { log: b, report } = DurableSessionLog.open(path.join(dir, 's1'));
    assert.equal(report.events, 2);
    assert.equal(report.tornTail, false);
    assert.equal(report.droppedInvalid, 0);
    const page = b.tail();
    assert.deepEqual(page.events.map((e) => e.kind), ['session.init', 'text']);
    assert.equal(page.events[1].payload.text, 'hello');
    assert.equal(b.highWater, 2);
    b.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('appending after a reopen continues the numbering', () => {
  const dir = tmp();
  try {
    const a = DurableSessionLog.create(path.join(dir, 's1'));
    a.append({ id: 'e1', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    a.append({ id: 'e2', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    a.close();

    const { log: b } = DurableSessionLog.open(path.join(dir, 's1'));
    const row3 = b.append({ id: 'e3', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    assert.equal(row3.seq, 3);
    b.close();

    const { log: c } = DurableSessionLog.open(path.join(dir, 's1'));
    assert.deepEqual(c.tail().events.map((e) => e.seq), [1, 2, 3]);
    c.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('opening a session whose event file is gone is REFUSED, not restarted at seq 1', () => {
  // This refusal is what makes the missing epoch safe — see durablelog.ts. If
  // this ever returns an empty log instead of throwing, every stale cursor
  // starts pointing at a real-looking row that is the wrong event, and the
  // cursor needs an epoch to detect it.
  const dir = tmp();
  try {
    mkdirSync(path.join(dir, 'ghost'), { recursive: true });
    assert.throws(() => DurableSessionLog.open(path.join(dir, 'ghost')), /missing/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a file torn by a hard kill reopens with the whole rows and says so', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    mkdirSync(sdir, { recursive: true });
    writeFileSync(path.join(sdir, EVENTS_FILENAME),
      lines(row(1), row(2)) + '{"id":"id-3","seq":3,"ts":"2026-08', 'utf-8');

    const { log, report } = DurableSessionLog.open(sdir);
    assert.deepEqual(log.tail().events.map((e) => e.seq), [1, 2]);
    assert.equal(report.tornTail, true);
    assert.equal(log.append({ id: 'e', ts: 't', kind: 'text', producer: 'agent', payload: {} }).seq, 3,
      'the seq of the row that never finished writing is free to reuse');
    log.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a gap in the middle of the file truncates to the newest run and reports the loss', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    mkdirSync(sdir, { recursive: true });
    writeFileSync(path.join(sdir, EVENTS_FILENAME), lines(row(1), row(2), row(9), row(10)), 'utf-8');

    const { log, report } = DurableSessionLog.open(sdir);
    assert.deepEqual(log.tail().events.map((e) => e.seq), [9, 10]);
    assert.equal(report.droppedBeforeGap, 2);
    log.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a bounded tail read starts mid-file and still returns whole rows', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    mkdirSync(sdir, { recursive: true });
    const all = Array.from({ length: 50 }, (_, i) => row(i + 1));
    writeFileSync(path.join(sdir, EVENTS_FILENAME), lines(...all), 'utf-8');

    // Small enough to land in the middle of a line.
    const { log, report } = DurableSessionLog.open(sdir, { tailBytes: 600 });
    const got = log.tail().events;
    assert.ok(got.length > 0 && got.length < 50, `expected a partial tail, got ${got.length}`);
    assert.equal(got[got.length - 1].seq, 50, 'the newest event must always survive');
    // Dense and ascending: no half-row slipped through.
    for (let i = 1; i < got.length; i += 1) {
      assert.equal(got[i].seq, got[i - 1].seq + 1);
    }
    assert.equal(report.events, got.length);
    log.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('compaction rewrites the file to the retained window and keeps seq', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir, { capacity: 3 });
    for (let i = 0; i < 10; i += 1) {
      log.append({ id: `e${i}`, ts: 't', kind: 'text', producer: 'agent', payload: { text: 'x' } });
    }
    log.compact();
    log.close();

    const text = readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8');
    const parsed = parseLogChunk(text, false);
    assert.deepEqual(parsed.events.map((e) => e.seq), [8, 9, 10],
      'compaction keeps the window, and the window keeps its original numbering');

    const { log: reopened } = DurableSessionLog.open(sdir, { capacity: 3 });
    assert.equal(reopened.highWater, 10);
    assert.equal(reopened.append({ id: 'x', ts: 't', kind: 'text', producer: 'agent', payload: {} }).seq, 11);
    reopened.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('the file compacts itself once it passes its byte cap', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    // The cap comfortably holds the 5-row window (~650 bytes): the normal
    // regime, where the cap alone is the trigger. The oversized-window regime
    // has its own test below.
    const log = DurableSessionLog.create(sdir, { capacity: 5, maxFileBytes: 1000 });
    for (let i = 0; i < 40; i += 1) {
      log.append({ id: `e${i}`, ts: '2026-08-14T00:00:00.000Z', kind: 'text', producer: 'agent', payload: { text: 'padding padding' } });
    }
    assert.ok(log.fileBytes <= 1000 + 200,
      `file should have been compacted, is ${log.fileBytes} bytes`);
    log.close();

    const { log: reopened } = DurableSessionLog.open(sdir, { capacity: 5 });
    assert.equal(reopened.highWater, 40, 'compaction must not lose the numbering');
    assert.ok(reopened.size <= 5);
    reopened.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a window too big for the byte cap does not rewrite the file on every append', () => {
  // The pathological regime: the retained window itself serializes past
  // `maxFileBytes` (think a 5000-event window holding a few 1 MB tool
  // results), so no compaction can get under the cap. Re-triggering on the cap
  // would then mean a full synchronous rewrite per append. The trigger must
  // instead wait for the file to grow past double its compacted size — which
  // is observable from outside: rows the window has already evicted stay in
  // the file until the NEXT compaction, whereas a rewrite-per-append would
  // leave the file holding exactly the window and nothing older.
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    // A cap no row can fit under — the regime where triggering on the cap
    // alone would rewrite the whole file on every single append.
    const log = DurableSessionLog.create(sdir, { capacity: 3, maxFileBytes: 1 });
    let next = 1;
    const add = (): void => {
      log.append({ id: `e${next}`, ts: 't', kind: 'text', producer: 'agent', payload: { text: 'sixteen bytes xx' } });
      next += 1;
    };
    const fileSeqs = (): number[] => {
      log.flush();
      return parseLogChunk(readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8'), false).events.map((e) => e.seq);
    };

    // Append until a compaction has happened and then one row has left the
    // window: the file still holding that evicted row is what proves the next
    // appends did NOT each rewrite the file down to the window.
    for (let i = 0; i < 4; i += 1) add();
    const seqs = fileSeqs();
    const evicted = seqs.filter((s) => s < log.highWater - 2);
    assert.ok(evicted.length > 0,
      `the file was rewritten on an append past the cap: holds only ${JSON.stringify(seqs)}`);

    // The rewrite is amortised, not abandoned: within a bounded number of
    // further appends the file doubles, compacts, and the evicted rows go.
    let trimmed: number[] = [];
    for (let i = 0; i < 20; i += 1) {
      add();
      trimmed = fileSeqs();
      if (!trimmed.some((s) => s < log.highWater - 2)) break;
    }
    assert.ok(!trimmed.some((s) => s < log.highWater - 2),
      `no compaction within 20 appends past the cap: file holds ${JSON.stringify(trimmed)}`);
    log.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a compaction interrupted before the rename leaves the previous file intact', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir);
    log.append({ id: 'e1', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    log.close();
    // Simulate the crash: the temp file exists, the real one is untouched.
    writeFileSync(path.join(sdir, EVENTS_FILENAME + '.compact'), 'half a rewrite', 'utf-8');

    const { log: reopened, report } = DurableSessionLog.open(sdir);
    assert.equal(report.events, 1);
    assert.deepEqual(reopened.tail().events.map((e) => e.id), ['e1']);
    reopened.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('flush persists without closing, so a live session is durable mid-turn', () => {
  // The batching is invisible to callers only if a flush actually reaches disk
  // on its own. Without this, "durable" would mean "durable if you quit".
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir);
    log.append({ id: 'e1', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    log.flush();

    const parsed = parseLogChunk(readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8'), false);
    assert.deepEqual(parsed.events.map((e) => e.id), ['e1']);

    // And the log stays usable afterwards.
    assert.equal(log.append({ id: 'e2', ts: 't', kind: 'text', producer: 'agent', payload: {} }).seq, 2);
    log.close();
    const after = parseLogChunk(readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8'), false);
    assert.deepEqual(after.events.map((e) => e.id), ['e1', 'e2']);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a flushed row is not written twice by the next flush', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir);
    log.append({ id: 'e1', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    log.flush();
    log.flush();
    log.close();
    const parsed = parseLogChunk(readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8'), false);
    assert.equal(parsed.events.length, 1, 'a duplicated row would break the dense-run check on reload');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('appends after close stay in memory and do not resurrect the file', () => {
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir);
    log.append({ id: 'e1', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    log.close();
    log.append({ id: 'e2', ts: 't', kind: 'text', producer: 'agent', payload: {} });
    assert.equal(log.tail().events.length, 2, 'the window still answers');

    const parsed = parseLogChunk(readFileSync(path.join(sdir, EVENTS_FILENAME), 'utf-8'), false);
    assert.deepEqual(parsed.events.map((e) => e.id), ['e1'], 'only what was written before close is on disk');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('reads are served from the window, so an evicted cursor still resyncs', () => {
  // The disk holds more than the window does; `since()` must not quietly start
  // answering from the file, because the renderer's contract is that a page
  // either continues the cursor or says it could not.
  const dir = tmp();
  try {
    const sdir = path.join(dir, 's1');
    const log = DurableSessionLog.create(sdir, { capacity: 3 });
    for (let i = 0; i < 10; i += 1) {
      log.append({ id: `e${i}`, ts: 't', kind: 'text', producer: 'agent', payload: {} });
    }
    const page = log.since(2);
    assert.equal(page.resyncRequired, true);
    assert.deepEqual(page.events.map((e) => e.seq), [8, 9, 10]);
    log.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
