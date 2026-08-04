/// Tests for the approval/question payload contract (vision-parity R1).
///
/// Three producers write `approval_request` in three different shapes. A card
/// that classifies one wrong offers the wrong buttons or posts to the wrong
/// endpoint, and neither failure is visible from the code — which is why the
/// classification lives in a pure module and is pinned here. Every fixture
/// below is the shape a named hub producer actually emits, not an invention.
///
/// Run locally: `node --test src/ui/approvalRequest.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  approvalWire,
  parseApprovalRequest,
  parseAttentionRequest,
  inlineDecidable,
  pendingAttentionFor,
  readOptions,
  type PermissionSpec,
  type QuestionSpec,
} from './approvalRequest.ts';

// ── ACP / M1 permission (driver_acp.go handlePermissionRequest) ──────

test('permission: request_id + the agent-offered options', () => {
  const spec = parseApprovalRequest({
    request_id: 'nonce-7',
    params: {
      sessionId: 'sess_1',
      toolCall: { toolCallId: 'tc_1', name: 'Bash', kind: 'execute' },
      options: [
        { optionId: 'proceed_once', name: 'Allow once' },
        { optionId: 'proceed_always_server', name: 'Always allow this server' },
        { optionId: 'cancel', name: 'Reject' },
      ],
    },
  });
  assert.equal(spec.form, 'permission');
  const p = spec as PermissionSpec;
  assert.equal(p.requestId, 'nonce-7');
  assert.equal(p.toolSummary, 'Bash'); // what the user is actually deciding about
  assert.deepEqual(
    p.options.map((o) => o.id),
    ['proceed_once', 'proceed_always_server', 'cancel'],
  );
  assert.equal(p.options[0].label, 'Allow once');
  assert.equal(p.synthesized, false);
});

test('permission: no options block → a synthesized allow/deny pair', () => {
  const spec = parseApprovalRequest({ request_id: 'r1', params: {} }) as PermissionSpec;
  assert.equal(spec.form, 'permission');
  assert.deepEqual(spec.options.map((o) => o.id), ['allow', 'deny']);
  // The flag is load-bearing, not cosmetic — it decides what goes on the wire.
  assert.equal(spec.synthesized, true);
});

test('permission without a request_id is unanswerable → unknown', () => {
  // There is nothing to key the reply on, so buttons could only 400. Falling
  // back to the payload dump at least shows the user what arrived.
  assert.equal(parseApprovalRequest({ params: { options: [{ optionId: 'x', name: 'X' }] } }).form, 'unknown');
});

// ── the wire rule ────────────────────────────────────────────────────

test('approvalWire: an agent option carries option_id, a synthesized one does not', () => {
  const offered = parseApprovalRequest({
    request_id: 'r1',
    params: { options: [{ optionId: 'proceed_always_server', name: 'Always' }] },
  }) as PermissionSpec;
  // `proceed_always_server` is NOT in the hub's semantic vocabulary, so it is
  // only accepted alongside an option_id. Sending it bare is a 400 the user
  // experiences as "the button did nothing".
  assert.deepEqual(approvalWire(offered, offered.options[0]), {
    decision: 'proceed_always_server',
    optionId: 'proceed_always_server',
  });

  const synth = parseApprovalRequest({ request_id: 'r1', params: {} }) as PermissionSpec;
  assert.deepEqual(approvalWire(synth, synth.options[0]), { decision: 'allow' });
});

// ── claude M4 AskUserQuestion (claude_code/hooks.go) ─────────────────

test('question: the hook forwards the whole tool_input under `questions`', () => {
  const spec = parseApprovalRequest({
    dialog_type: 'user_question',
    tool_use_id: 'toolu_9',
    questions: {
      questions: [
        {
          question: 'Which colour?',
          header: 'Colour',
          options: [
            { label: 'Red', description: 'warm' },
            { label: 'Blue' },
          ],
        },
      ],
    },
  });
  assert.equal(spec.form, 'question');
  const q = spec as QuestionSpec;
  // The reply is keyed on the tool_use_id — the driver wraps it as a
  // tool_result claude is blocked on.
  assert.equal(q.requestId, 'toolu_9');
  assert.equal(q.header, 'Colour');
  assert.equal(q.question, 'Which colour?');
  assert.deepEqual(q.options.map((o) => o.label), ['Red', 'Blue']);
  assert.equal(q.options[0].description, 'warm');
  assert.equal(q.moreQuestions, 0);
});

test('question: the tool_call spelling puts the array directly on `questions`', () => {
  // Both spellings are live (the hook nests, the tool_call path does not), and
  // betting on one silently loses the other's cards.
  const q = parseApprovalRequest({
    dialog_type: 'user_question',
    tool_use_id: 'toolu_1',
    questions: [{ question: 'Ship it?', options: [{ label: 'Yes' }] }],
  }) as QuestionSpec;
  assert.equal(q.form, 'question');
  assert.equal(q.question, 'Ship it?');
});

test('question: extra questions are counted, not dropped silently', () => {
  const q = parseApprovalRequest({
    dialog_type: 'user_question',
    tool_use_id: 't1',
    questions: { questions: [{ question: 'A' }, { question: 'B' }, { question: 'C' }] },
  }) as QuestionSpec;
  assert.equal(q.moreQuestions, 2);
});

test('question: a malformed questions block degrades to unknown, not an empty card', () => {
  assert.equal(
    parseApprovalRequest({ dialog_type: 'user_question', tool_use_id: 't1', questions: {} }).form,
    'unknown',
  );
  // No id to answer on → nothing a button could post.
  assert.equal(
    parseApprovalRequest({ dialog_type: 'user_question', questions: [{ question: 'A' }] }).form,
    'unknown',
  );
});

// ── claude M4 PreCompact (claude_code/hooks.go hookPreCompact) ───────

test('compaction is recognised and is deliberately not answerable inline', () => {
  const spec = parseApprovalRequest({
    dialog_type: 'compaction',
    trigger: 'auto',
    custom_instructions: 'keep the plan',
    options: ['compact', 'defer'],
  });
  assert.equal(spec.form, 'compaction');
  assert.equal(spec.form === 'compaction' && spec.trigger, 'auto');
  assert.equal(spec.form === 'compaction' && spec.customInstructions, 'keep the plan');
  // Deliberately NO options on the spec: hookPreCompact parks a real attention
  // item and blocks on it, and this event carries no attention id — so an
  // inline button would have nothing legitimate to POST to.
  assert.ok(!('options' in spec));
});

// ── option shapes ────────────────────────────────────────────────────

test('readOptions accepts both the map shape and the bare-string shape', () => {
  // ACP sends maps; hookPreCompact sends ["compact", "defer"].
  assert.deepEqual(readOptions([{ optionId: 'a', name: 'A' }]), [{ id: 'a', label: 'A' }]);
  assert.deepEqual(readOptions(['compact', 'defer']), [
    { id: 'compact', label: 'compact' },
    { id: 'defer', label: 'defer' },
  ]);
  // id/label are the documented aliases for optionId/name.
  assert.deepEqual(readOptions([{ id: 'x', label: 'X' }]), [{ id: 'x', label: 'X' }]);
});

test('readOptions drops entries with no usable id', () => {
  // A button whose id is empty posts an empty decision, which the hub rejects
  // — better to not render it than to render one that cannot work.
  assert.deepEqual(readOptions([{ name: 'no id' }, { optionId: '', name: 'blank' }, '']), []);
  assert.deepEqual(readOptions([{ optionId: 'ok', name: 'Ok' }, null, 42]), [{ id: 'ok', label: 'Ok' }]);
  assert.deepEqual(readOptions(undefined), []);
  assert.deepEqual(readOptions('not an array'), []);
});

test('readOptions falls back to the label when only a label is given', () => {
  // AskUserQuestion options carry {label, description} and no id at all; the
  // label IS the answer body, so it doubles as the id.
  assert.deepEqual(readOptions([{ label: 'Red', description: 'warm' }]), [
    { id: 'Red', label: 'Red', description: 'warm' },
  ]);
});

// ── attention_request ────────────────────────────────────────────────

test('attention_request surfaces the reason and the hub-computed remediation', () => {
  const spec = parseAttentionRequest({
    kind: 'auth_required',
    reason: 'no cached credentials',
    engine_kind: 'gemini-cli',
    available_methods: [],
    remediation: 'Run `gemini auth` on the host',
  });
  assert.equal(spec.kind, 'auth_required');
  assert.equal(spec.reason, 'no cached credentials');
  // The remediation is a command on the HOST, which is why this card has no
  // buttons: the desktop cannot perform the fix it would be promising.
  assert.equal(spec.remediation, 'Run `gemini auth` on the host');
  assert.deepEqual(spec.methods, []);
});

test('attention_request carries the offered auth methods when there are any', () => {
  const spec = parseAttentionRequest({
    kind: 'auth_required',
    available_methods: [{ id: 'oauth-personal', label: 'Google login', interactive: true }],
  });
  assert.deepEqual(spec.methods, [{ id: 'oauth-personal', label: 'Google login' }]);
});

// ── pending attention scoping ────────────────────────────────────────
//
// Fixture statuses are the hub's REAL vocabulary — 'open' / 'resolved'
// (`handleListAttention` defaults to status=open; nothing ever writes
// 'pending'). The first cut of these tests invented a 'pending' status and
// the filter matched the fixtures instead of the wire, so the inline cards
// never rendered against a live hub. Same equivalence blind spot as the
// ui_policy coverage invariant: hand-written fixtures verify the halves
// agree with each other, not with the producer.

test('pendingAttentionFor matches open rows on pending_payload.agent_id', () => {
  const items = [
    { id: 'a1', status: 'open', pending_payload: { agent_id: 'ag_1', tool_name: 'Bash' } },
    { id: 'a2', status: 'open', pending_payload: { agent_id: 'ag_2' } },
  ];
  assert.deepEqual(pendingAttentionFor(items, 'ag_1').map((i) => i.id), ['a1']);
});

test('pendingAttentionFor excludes resolved rows', () => {
  // The card renders what still needs the user; a settled decision is already
  // a matter of record in the transcript.
  const items = [
    { id: 'a1', status: 'resolved', pending_payload: { agent_id: 'ag_1' } },
    { id: 'a2', status: 'open', pending_payload: { agent_id: 'ag_1' } },
  ];
  assert.deepEqual(pendingAttentionFor(items, 'ag_1').map((i) => i.id), ['a2']);
});

test('pendingAttentionFor falls back to session_id only when a session is in scope', () => {
  const items = [{ id: 'a1', status: 'open', session_id: 'sess_1' }];
  assert.deepEqual(pendingAttentionFor(items, 'ag_1', 'sess_1').map((i) => i.id), ['a1']);
  // Without a session in scope the row must NOT match — otherwise every
  // system-raised row with an empty session would land in this agent's feed.
  assert.deepEqual(pendingAttentionFor(items, 'ag_1'), []);
  assert.deepEqual(pendingAttentionFor([{ id: 'a2', status: 'open' }], 'ag_1', ''), []);
});

test('pendingAttentionFor with no bound agent matches nothing', () => {
  assert.deepEqual(pendingAttentionFor([{ id: 'a1', status: 'open' }], ''), []);
});

// ── inline decidability ──────────────────────────────────────────────

test('inlineDecidable: binary kinds only — select and help_request defer to the dock', () => {
  // Binary by construction (request_approval) or allow-once semantics
  // (the bridge approval kinds): the approve/reject pair is complete.
  for (const kind of ['permission_prompt', 'approval_request', 'browser_action', 'desktop_action']) {
    assert.equal(inlineDecidable(kind), true, kind);
  }
  // select needs an option_id — an optionless approve resolves the row while
  // naming no choice; help_request needs a body — the hub 400s without one.
  // Unknown kinds have an unknown decide contract, so they defer too.
  for (const kind of ['select', 'help_request', 'elicit', 'propose', 'template_proposal', 'idle', '']) {
    assert.equal(inlineDecidable(kind), false, kind === '' ? '(empty)' : kind);
  }
});
