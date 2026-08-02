/// Tests for the `author_*` consent policy (coworking A3/A4, ADR-064) —
/// the lease's scope, the card's two answers, and the sentence an agent is
/// told on each outcome. `node --test`, and CI runs this package's suite.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyResultText,
  authorApprovalCard,
  authorDenialMessage,
  AuthorLeaseStore,
  documentIndexText,
} from './author.ts';

test('a lease is per DOCUMENT: granting one does not grant the next', () => {
  const leases = new AuthorLeaseStore();
  leases.grant('ag_1', 'doc_a');
  assert.equal(leases.has('ag_1', 'doc_a'), true);
  // The whole point of the scope. "Allow this document for this session" must
  // not become "allow anything this agent opens next".
  assert.equal(leases.has('ag_1', 'doc_b'), false);
  // …nor "allow any agent this document".
  assert.equal(leases.has('ag_2', 'doc_a'), false);
});

test('an anonymous caller never holds a lease', () => {
  const leases = new AuthorLeaseStore();
  leases.grant('', 'doc_a');
  assert.equal(leases.has('', 'doc_a'), false);
  assert.equal(leases.size(), 0);
  // Symmetrically: a grant with no document is not a blanket grant.
  leases.grant('ag_1', '');
  assert.equal(leases.has('ag_1', ''), false);
  assert.equal(leases.size(), 0);
});

test('two pairs cannot collide into one key by moving the boundary', () => {
  const leases = new AuthorLeaseStore();
  leases.grant('ag_1', 'doc:a');
  // With a printable separator (`${agent}:${doc}`) both of these render as
  // "ag_1:doc:a", and the second pair silently inherits the first pair's
  // lease. The separator is a NUL escape precisely because no id can hold one.
  assert.equal(leases.has('ag_1:doc', 'a'), false);
  assert.equal(leases.has('ag_1', 'doc:a'), true);
});

test('revokeAgent drops that agent only; clear drops everything', () => {
  const leases = new AuthorLeaseStore();
  leases.grant('ag_1', 'doc_a');
  leases.grant('ag_1', 'doc_b');
  leases.grant('ag_2', 'doc_a');
  leases.revokeAgent('ag_1');
  assert.equal(leases.has('ag_1', 'doc_a'), false);
  assert.equal(leases.has('ag_1', 'doc_b'), false);
  assert.equal(leases.has('ag_2', 'doc_a'), true);
  leases.clear();
  assert.equal(leases.size(), 0);
});

test('revokeAgent matches on the whole agent id, not a prefix of one', () => {
  const leases = new AuthorLeaseStore();
  leases.grant('ag_1', 'doc_a');
  leases.grant('ag_10', 'doc_a');
  leases.revokeAgent('ag_1');
  assert.equal(leases.has('ag_1', 'doc_a'), false);
  // Without the separator in the prefix, revoking ag_1 would silently revoke
  // ag_10 as well — a different agent losing access it was never told about.
  assert.equal(leases.has('ag_10', 'doc_a'), true);
});

test('the card names the document and declares the grant it can create', () => {
  const card = authorApprovalCard({
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    documentId: 'doc_a',
    title: 'Architecture',
    kind: 'diagram',
    mode: 'replace',
    reason: 'add the relay box',
    bytes: 1024,
  });
  assert.match(card.summary, /kimi-1/);
  assert.match(card.summary, /rewrite/);
  assert.match(card.summary, /Architecture/);
  assert.match(card.summary, /1024 bytes/);
  assert.equal(card.payload.tool, 'author_apply');
  assert.equal(card.payload.document_id, 'doc_a');
  // A client renders the second button from these two fields; a card that did
  // not declare the scope would leave "for this session" meaning whatever the
  // client guessed.
  assert.equal(card.payload.session_grant, true);
  assert.equal(card.payload.session_grant_scope, 'document');
});

test('the card says append when it is an append, and names an untitled doc', () => {
  const card = authorApprovalCard({
    agentId: 'ag_1',
    agentHandle: '',
    documentId: 'doc_a',
    title: '',
    kind: 'markdown',
    mode: 'append',
    reason: '',
    bytes: 12,
  });
  assert.match(card.summary, /append to/);
  assert.match(card.summary, /an untitled document/);
  // No handle: the id is the fallback, never a blank subject.
  assert.match(card.summary, /^ag_1 /);
});

test('an agent-authored reason is clipped on the card payload', () => {
  const card = authorApprovalCard({
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    documentId: 'doc_a',
    title: 'T',
    kind: 'markdown',
    mode: 'replace',
    reason: 'x'.repeat(500),
    bytes: 1,
  });
  const reason = card.payload.reason;
  assert.equal(typeof reason, 'string');
  assert.ok((reason as string).length <= 201, `reason not clipped: ${String((reason as string).length)}`);
});

test('applied_store_only says the user cannot see it yet; applied_live says they can', () => {
  const live = applyResultText({ documentId: 'doc_a', title: 'T', kind: 'canvas', state: 'applied_live', bytes: 10 });
  assert.match(live, /^applied_live:/);
  assert.match(live, /looking at the new version/);
  assert.match(live, /undo/i);

  const stored = applyResultText({ documentId: 'doc_a', title: 'T', kind: 'table', state: 'applied_store_only', bytes: 10 });
  assert.match(stored, /^applied_store_only:/);
  // The honesty requirement A4 exists for: the agent must be able to tell the
  // user the screen is stale, not just that the call succeeded.
  assert.match(stored, /still shows the previous version/);
  assert.match(stored, /reopen the tab/);
});

test('every denial cause has its own sentence, and none of them is "unknown"', () => {
  const causes = ['denied', 'timeout', 'unavailable', 'raise_failed'] as const;
  const seen = new Set<string>();
  for (const c of causes) {
    const msg = authorDenialMessage(c);
    assert.notEqual(msg, '');
    seen.add(msg);
  }
  assert.equal(seen.size, causes.length, 'two causes share a sentence — the agent cannot tell them apart');
  // The one distinction that changes what an agent should DO next: signing in
  // fixes `unavailable`, retrying fixes `raise_failed`.
  assert.match(authorDenialMessage('unavailable'), /not signed in/);
  assert.match(authorDenialMessage('raise_failed'), /retrying may work/);
});

test('the document index marks the active document and survives an empty list', () => {
  assert.match(documentIndexText([]), /no documents are open/);
  const text = documentIndexText([
    { id: 'doc_a', kind: 'markdown', title: 'Notes', active: false },
    { id: 'doc_b', kind: 'diagram', title: 'Arch', active: true },
  ]);
  assert.match(text, /doc_a \[markdown] Notes/);
  assert.match(text, /doc_b \[diagram] \(active\) Arch/);
  assert.equal(text.split('\n').length, 2);
});
