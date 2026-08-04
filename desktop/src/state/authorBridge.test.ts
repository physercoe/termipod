/// Tests for the renderer half of the `author_*` bridge (coworking A2/A4,
/// ADR-064): narrowing what main pushes, resolving which document a call
/// addresses, and the `applied_*` ladder.
///
/// Run: node --test src/state/authorBridge.test.ts  (CI does NOT run these)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyStateFor, asAuthorRequest, docLine, executeAuthorRequest, resolveTarget, type AuthorIO, type AuthorRequest } from './authorBridge.ts';
import type { ApplyOutcome } from './liveApply.ts';
import type { Doc } from './documents.ts';

const DOCS: Doc[] = [
  { id: 'doc_a', kind: 'markdown', title: 'Notes', body: '# a', updatedAt: 1_700_000_000_000 },
  { id: 'doc_b', kind: 'diagram', title: 'Arch', body: '<mxfile/>', updatedAt: 1_700_000_001_000, filePath: '/w/arch.drawio' },
];

test('a payload without a usable id or op is dropped', () => {
  assert.equal(asAuthorRequest(null), null);
  assert.equal(asAuthorRequest('nope'), null);
  assert.equal(asAuthorRequest([]), null);
  assert.equal(asAuthorRequest({ op: 'read' }), null, 'no id — there is nowhere to reply to');
  assert.equal(asAuthorRequest({ id: 'r1' }), null, 'no op');
  assert.equal(asAuthorRequest({ id: 'r1', op: 'delete' }), null, 'an op we do not implement');
});

test('narrowing coerces every field to the type the executor expects', () => {
  // Main already narrowed these; re-narrowed here because the values came from
  // an agent, and a store must take nothing on trust across an IPC boundary
  // that agent input reached.
  const req = asAuthorRequest({ id: 'r1', op: 'apply', document_id: 42, mode: 'ops', body: null, reason: {}, by: ['x'] });
  assert.notEqual(req, null);
  assert.equal(req?.documentId, null);
  // An unknown mode falls back to `replace` HERE, but never reaches here from
  // the tool: browserbridge.ts refuses anything that is not replace/append, so
  // this fallback is the second wall, not the policy.
  assert.equal(req?.mode, 'replace');
  assert.equal(req?.body, '');
  assert.equal(req?.reason, '');
  assert.equal(req?.by, '');
});

test('an empty document_id means the active document, not a document named ""', () => {
  assert.equal(asAuthorRequest({ id: 'r1', op: 'read', document_id: '' })?.documentId, null);
  assert.equal(asAuthorRequest({ id: 'r1', op: 'read', document_id: 'doc_b' })?.documentId, 'doc_b');
});

test('resolveTarget: a named document, the active one, and both absences', () => {
  const named = resolveTarget(DOCS, 'doc_a', 'doc_b');
  assert.equal(named.ok, true);
  if (named.ok) assert.equal(named.doc.id, 'doc_b');

  const active = resolveTarget(DOCS, 'doc_a', null);
  assert.equal(active.ok, true);
  if (active.ok) assert.equal(active.doc.id, 'doc_a');

  const gone = resolveTarget(DOCS, 'doc_a', 'doc_zz');
  assert.equal(gone.ok, false);
  if (!gone.ok) {
    assert.equal(gone.code, 'DOCUMENT_GONE');
    // The id is IN the message: an agent that read the index a minute ago and
    // lost a race needs to know which document went away.
    assert.match(gone.message, /doc_zz/);
  }

  const none = resolveTarget([], null, null);
  assert.equal(none.ok, false);
  if (!none.ok) assert.equal(none.code, 'NO_ACTIVE_DOCUMENT');
});

test('a stale activeId is an absence, not a crash', () => {
  // The store can hold an activeId whose document was removed; every reader
  // has to survive it.
  const out = resolveTarget(DOCS, 'doc_gone', null);
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'NO_ACTIVE_DOCUMENT');
});

test('docLine carries metadata and never the body', () => {
  const line = docLine(DOCS[1], 'doc_b');
  assert.deepEqual(line, {
    id: 'doc_b',
    kind: 'diagram',
    title: 'Arch',
    file_path: '/w/arch.drawio',
    updated_at: new Date(1_700_000_001_000).toISOString(),
    active: true,
  });
  // The pre-flight `resolve` puts this on an approval card. Naming a document
  // must not disclose it, so the absence of `body` is the point of the shape.
  assert.equal(Object.prototype.hasOwnProperty.call(line, 'body'), false);
  assert.equal(docLine(DOCS[0], 'doc_b').active, false);
  assert.equal(docLine(DOCS[0], 'doc_b').file_path, null);
});

test('the ladder: a live target wins, otherwise the kind decides', () => {
  // A registered adapter took it — the strongest evidence there is.
  assert.equal(applyStateFor('applied_live', 'canvas'), 'applied_live');
  // No adapter: markdown/figure re-render from `body`, so the store write IS
  // what the user sees.
  assert.equal(applyStateFor('no_target', 'markdown'), 'applied_live');
  assert.equal(applyStateFor('no_target', 'figure'), 'applied_live');
  // …and the kinds whose editor owns its state do not, which is the whole
  // point of reporting the rung instead of "ok".
  assert.equal(applyStateFor('no_target', 'table'), 'applied_store_only');
  assert.equal(applyStateFor('no_target', 'excalidraw'), 'applied_store_only');
  assert.equal(applyStateFor('no_target', 'diagram'), 'applied_store_only');
});

test('the ladder never reports a rejection as a landing', () => {
  // `rejected` must not reach the ladder — the executor turns it into a
  // refusal before this point. If it ever does, the answer must still be the
  // conservative rung for the kind, never `applied_live` by default.
  assert.equal(applyStateFor('rejected', 'table'), 'applied_store_only');
  assert.equal(applyStateFor('rejected', 'diagram'), 'applied_store_only');
});

// ── The executor: the ORDER is the safety property ───────────────────────────

interface Spy {
  io: AuthorIO;
  log: string[];
  docs: Doc[];
}

function spy(docs: Doc[], outcome: ApplyOutcome | 'no_target' = 'applied_live'): Spy {
  const log: string[] = [];
  const live = [...docs];
  return {
    docs: live,
    log,
    io: {
      docs: live,
      activeId: live[0]?.id ?? null,
      liveApply: (id, body) => {
        log.push(`live:${id}:${body.length}`);
        return outcome;
      },
      record: (id, edit) => log.push(`record:${id}:${edit.before.length}:${edit.by}${edit.reason === undefined ? '' : `:${edit.reason}`}`),
      update: (id, body) => {
        log.push(`update:${id}:${body.length}`);
        const d = live.find((x) => x.id === id);
        if (d !== undefined) d.body = body;
      },
      now: () => 1_700_000_099_000,
    },
  };
}

function req(over: Partial<AuthorRequest> = {}): AuthorRequest {
  return { id: 'r1', op: 'apply', documentId: null, mode: 'replace', body: '', reason: '', by: 'kimi-1', ...over };
}

test('a body that does not parse changes NOTHING — no live call, no record, no write', () => {
  const s = spy([{ id: 'doc_t', kind: 'table', title: 'T', body: '{"columns":[],"rows":[]}', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ body: '{ oops' }), s.io);
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'INVALID_TABLE');
  // The review anchor, executable: "malformed apply leaves the doc
  // byte-identical". Not even the editor was asked.
  assert.deepEqual(s.log, []);
  assert.equal(s.docs[0].body, '{"columns":[],"rows":[]}');
});

test('an editor that REFUSES leaves the document byte-identical too', () => {
  // The subtle one. Validation passed, so a naive implementation would have
  // written the store first and asked the editor after — leaving a document
  // whose body no editor ever accepted.
  const s = spy([{ id: 'doc_c', kind: 'canvas', title: 'C', body: '{"nodes":[],"edges":[]}', updatedAt: 1 }], 'rejected');
  const out = executeAuthorRequest(req({ body: '{"nodes":[{"id":"n1"}],"edges":[]}' }), s.io);
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'APPLY_REJECTED');
  assert.deepEqual(s.log.map((l) => l.split(':')[0]), ['live'], 'the editor was asked, and nothing else ran');
  assert.equal(s.docs[0].body, '{"nodes":[],"edges":[]}');
});

test('a successful apply records BEFORE it writes, and records the OLD body', () => {
  const s = spy([{ id: 'doc_m', kind: 'markdown', title: 'M', body: 'old', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ body: 'new body', reason: 'tidy' }), s.io);
  assert.equal(out.ok, true);
  // record before update: reversed, the ring would capture the agent's own
  // write as the thing to revert TO, and the chip would restore nothing.
  assert.deepEqual(s.log, ['live:doc_m:8', 'record:doc_m:3:kimi-1:tidy', 'update:doc_m:8']);
  assert.equal(s.docs[0].body, 'new body');
});

test('a no-op write is reported without spending an undo', () => {
  const s = spy([{ id: 'doc_m', kind: 'markdown', title: 'M', body: 'same', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ body: 'same' }), s.io);
  assert.equal(out.ok, true);
  if (out.ok && out.op === 'apply') assert.equal(out.state, 'applied_live');
  assert.deepEqual(s.log, [], 'a no-op must not push onto the revert ring');
});

test('append composes against the CURRENT body, and the ring holds the pre-append text', () => {
  const s = spy([{ id: 'doc_m', kind: 'markdown', title: 'M', body: 'intro', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ mode: 'append', body: 'tail' }), s.io);
  assert.equal(out.ok, true);
  assert.equal(s.docs[0].body, 'intro\n\ntail');
  assert.ok(s.log.includes('record:doc_m:5:kimi-1'), s.log.join(' | '));
});

test('a diagram apply stores the WRAPPED body, not the agent input', () => {
  const s = spy([{ id: 'doc_d', kind: 'diagram', title: 'D', body: '<mxfile></mxfile>', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ body: '<mxGraphModel><root><mxCell id="0"/></root></mxGraphModel>' }), s.io);
  assert.equal(out.ok, true);
  assert.ok(s.docs[0].body.startsWith('<mxfile'), s.docs[0].body.slice(0, 40));
  // The reported byte count is what was STORED — an agent comparing it against
  // what it sent should see the wrap, not a number that silently disagrees.
  if (out.ok && out.op === 'apply') assert.equal(out.bytes, s.docs[0].body.length);
});

test('an oversized body is refused before anything parses it', () => {
  const s = spy([{ id: 'doc_m', kind: 'markdown', title: 'M', body: 'x', updatedAt: 1 }]);
  const out = executeAuthorRequest(req({ body: 'y'.repeat(512 * 1024 + 1) }), s.io);
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'BODY_TOO_LARGE');
  assert.deepEqual(s.log, []);
});

test('an anonymous agent is attributed, never blank', () => {
  const s = spy([{ id: 'doc_m', kind: 'markdown', title: 'M', body: 'a', updatedAt: 1 }]);
  executeAuthorRequest(req({ body: 'b', by: '' }), s.io);
  // An unattributed chip is the failure mode the plan's risk section names.
  assert.ok(s.log.some((l) => l.startsWith('record:doc_m:1:an agent')), s.log.join(' | '));
});

test('read returns the body and the index; resolve returns neither', () => {
  const s = spy([
    { id: 'doc_m', kind: 'markdown', title: 'M', body: 'secret prose', updatedAt: 1 },
    { id: 'doc_d', kind: 'diagram', title: 'D', body: '<mxfile/>', updatedAt: 2 },
  ]);
  const read = executeAuthorRequest(req({ op: 'read' }), s.io);
  assert.equal(read.ok, true);
  if (read.ok && read.op === 'read') {
    assert.equal(read.document.body, 'secret prose');
    assert.equal(read.documents.length, 2);
  }
  const resolved = executeAuthorRequest(req({ op: 'resolve' }), s.io);
  assert.equal(resolved.ok, true);
  if (resolved.ok && resolved.op === 'resolve') {
    // The pre-flight that names the document on an approval card. Naming it
    // must not disclose it.
    assert.equal(Object.prototype.hasOwnProperty.call(resolved.document, 'body'), false);
  }
  assert.deepEqual(s.log, [], 'neither read nor resolve mutates anything');
});
