/// codex app-server wire shapes (vision-parity L4c). Run with `node --test`.
///
/// Every shape asserted here was checked against a LIVE codex-cli 0.147.0 —
/// either by sending it and reading the reply, or by reading the vendor's own
/// `codex app-server generate-ts` output. The comments name which.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  askEventPayload,
  askResult,
  buildTurnInput,
  codexPosture,
  isDeltaMethod,
  parseServerAsk,
  refuseResult,
  threadResumeParams,
  threadStartParams,
  trimToTail,
} from './codexwire.ts';

// ── Posture ──────────────────────────────────────────────────────────────────

test('read_local lowers to the sandbox that was MEASURED to refuse a write', () => {
  const p = codexPosture('read_local');
  assert.equal(p.sandbox, 'read-only');
  // `never` and not `untrusted`: with `untrusted` codex asks to retry outside
  // the sandbox when a command fails (observed: `reason: "command failed;
  // retry without sandbox?"`), which would make the boundary clickable.
  assert.equal(p.approvalPolicy, 'never');
  assert.equal(p.note, undefined);
});

test('unrestricted is the only posture that leaves the sandbox', () => {
  assert.equal(codexPosture('unrestricted').sandbox, 'danger-full-access');
  for (const posture of ['converse', 'read_local'] as const) {
    assert.equal(codexPosture(posture).sandbox, 'read-only');
  }
});

test('converse says out loud that codex cannot disable its tools', () => {
  const p = codexPosture('converse');
  // The posture cannot be kept exactly, so the transcript carries the
  // difference rather than the name alone (D-4).
  assert.match(p.note ?? '', /no tool-disable switch/);
  assert.match(p.note ?? '', /can still read files/);
});

// ── thread/start + thread/resume ─────────────────────────────────────────────

test('thread/start carries cwd and the lowered posture, and no config file', () => {
  const params = threadStartParams({ cwd: '/w', posture: 'read_local', model: 'gpt-5.6' });
  assert.deepEqual(params, {
    cwd: '/w',
    sandbox: 'read-only',
    approvalPolicy: 'never',
    model: 'gpt-5.6',
  });
});

test('config overrides ride the RPC instead of a written .codex/config.toml', () => {
  const params = threadStartParams({
    cwd: '/w',
    posture: 'read_local',
    config: { model_reasoning_effort: 'low' },
  });
  assert.deepEqual(params.config, { model_reasoning_effort: 'low' });
  // An empty map is not sent at all — an empty override object would be a
  // claim that overrides were configured.
  assert.equal(threadStartParams({ cwd: '/w', posture: 'read_local', config: {} }).config, undefined);
});

test('resume re-sends the overrides, because a resumed thread takes what it is given', () => {
  const params = threadResumeParams('th-1', { cwd: '/w', posture: 'read_local' });
  assert.equal(params.threadId, 'th-1');
  assert.equal(params.sandbox, 'read-only');
  assert.equal(params.approvalPolicy, 'never');
});

// ── turn/start input ─────────────────────────────────────────────────────────

test('an image rides the variant codex actually accepts', () => {
  const input = buildTurnInput({ body: 'what is this', images: [{ mime: 'image/png', data: 'AAA' }] });
  // Measured: `{type:"input_image", image_url}` — the shape the hub's Go driver
  // sends — is answered `-32600 unknown variant 'input_image', expected one of
  // 'text', 'image', 'localImage', 'audio', 'localAudio', 'skill', 'mention'`.
  assert.deepEqual(input, [
    { type: 'image', url: 'data:image/png;base64,AAA' },
    { type: 'text', text: 'what is this' },
  ]);
});

test('text carries no text_elements, which the server fills itself', () => {
  const input = buildTurnInput({ body: 'hi' });
  assert.deepEqual(input, [{ type: 'text', text: 'hi' }]);
  // The generated `UserInput` marks `text_elements` required; the live server
  // accepts its absence and echoes back `text_elements: []`. Sending one would
  // be inventing UI spans we do not have.
  assert.equal('text_elements' in (input[0] as object), false);
});

test('a PDF throws rather than being dropped', () => {
  // codex 0.147.0 has no file variant at all — `input_file` is rejected with
  // the same "unknown variant" error. A silent drop would send the agent a
  // question about a document it never received.
  assert.throws(
    () => buildTurnInput({ body: 'summarise', pdfs: [{ mime: 'application/pdf', data: 'JVBER' }] }),
    /no file attachments/,
  );
});

test('an empty message is refused', () => {
  assert.throws(() => buildTurnInput({}), /no body and no attachments/);
});

// ── Server-initiated requests ────────────────────────────────────────────────

const EXEC_APPROVAL = 'item/commandExecution/requestApproval';

test('a command approval offers exactly the decisions codex advertised', () => {
  // Verbatim from the wire: codex advertised `cancel`, NOT `decline`, plus an
  // object-shaped amendment.
  const ask = parseServerAsk(
    EXEC_APPROVAL,
    {
      command: "/bin/bash -lc 'echo hi'",
      reason: 'command failed; retry without sandbox?',
      availableDecisions: ['accept', { acceptWithExecpolicyAmendment: { execpolicy_amendment: ['echo'] } }, 'cancel'],
    },
    'req-1',
  );
  assert.equal(ask.form, 'approval');
  assert.deepEqual(ask.options.map((o) => o.id), ['accept', 'cancel']);
  assert.match(ask.summary, /Run: \/bin\/bash -lc 'echo hi'/);
  assert.match(ask.summary, /retry without sandbox/);
});

test('an object-shaped decision is never offered as a button', () => {
  const ask = parseServerAsk(
    EXEC_APPROVAL,
    { command: 'rm -rf /', availableDecisions: [{ applyNetworkPolicyAmendment: {} }, 'accept', 'decline'] },
    'req-1',
  );
  // Both amendment variants mint a STANDING policy from one click — the same
  // class R1's `inlineDecidable` already refuses to put on an inline card.
  assert.deepEqual(ask.options.map((o) => o.id), ['accept', 'decline']);
});

test('no advertised decisions still yields an answerable card', () => {
  const ask = parseServerAsk('item/fileChange/requestApproval', { grantRoot: '/w' }, 'req-2');
  assert.equal(ask.form, 'approval');
  assert.deepEqual(ask.options.map((o) => o.id), ['accept', 'decline']);
  assert.match(ask.summary, /under \/w/);
});

test('an MCP tool-call gate is an approval; a real form fill is refused', () => {
  const gate = parseServerAsk(
    'mcpServer/elicitation/request',
    { serverName: 'docs', message: 'Run search?', mode: 'form', requestedSchema: { properties: {} } },
    'req-3',
  );
  assert.equal(gate.form, 'approval');
  assert.deepEqual(gate.options.map((o) => o.id), ['accept', 'decline']);

  const form = parseServerAsk(
    'mcpServer/elicitation/request',
    {
      serverName: 'docs',
      message: 'Which branch?',
      mode: 'form',
      requestedSchema: { properties: { branch: { type: 'string' } } },
    },
    'req-4',
  );
  // A card with no free-text input could never answer this, and a card that
  // can never be answered parks the engine forever.
  assert.equal(form.form, 'unsupported');
  assert.match(form.note ?? '', /structured fields/);
});

test('a url-mode elicitation is refused rather than silently opening a browser', () => {
  const ask = parseServerAsk(
    'mcpServer/elicitation/request',
    { serverName: 'auth', mode: 'url', message: 'Sign in', url: 'https://example.test' },
    'req-5',
  );
  assert.equal(ask.form, 'unsupported');
  assert.match(ask.note ?? '', /opening a browser/);
});

test('requestUserInput becomes a question card keyed on the question id', () => {
  const ask = parseServerAsk(
    'item/tool/requestUserInput',
    {
      questions: [
        {
          id: 'q1',
          header: 'Deploy',
          question: 'Which environment?',
          isSecret: false,
          options: [
            { label: 'staging', description: 'safe' },
            { label: 'prod', description: 'not safe' },
          ],
        },
      ],
      isBlocking: true,
    },
    'req-6',
  );
  assert.equal(ask.form, 'question');
  assert.equal(ask.questionId, 'q1');
  assert.deepEqual(ask.options.map((o) => o.label), ['staging', 'prod']);
});

test('a SECRET question is never rendered as a card', () => {
  const ask = parseServerAsk(
    'item/tool/requestUserInput',
    { questions: [{ id: 'q1', question: 'API key?', isSecret: true, options: [{ label: 'x' }] }] },
    'req-7',
  );
  // `service.input()` records every input into the durable on-disk transcript
  // BEFORE sending it, so answering here would write the secret to disk.
  assert.equal(ask.form, 'unsupported');
  assert.match(ask.note ?? '', /transcript on disk/);
});

test('an option-less question is refused, because the card answers with an option', () => {
  const ask = parseServerAsk(
    'item/tool/requestUserInput',
    { questions: [{ id: 'q1', question: 'Describe the bug', options: [] }] },
    'req-8',
  );
  assert.equal(ask.form, 'unsupported');
});

test('a permissions request grants nothing rather than guessing a profile', () => {
  const ask = parseServerAsk('item/permissions/requestApproval', { reason: 'needs network' }, 'req-9');
  assert.equal(ask.form, 'unsupported');
  assert.deepEqual(refuseResult('item/permissions/requestApproval'), { permissions: {}, scope: 'turn' });
});

// ── Answering ────────────────────────────────────────────────────────────────

test('each method is answered in ITS OWN response shape', () => {
  const exec = parseServerAsk(EXEC_APPROVAL, {}, 'r');
  assert.deepEqual(askResult(exec, 'accept'), { decision: 'accept' });

  const elicit = parseServerAsk('mcpServer/elicitation/request', { requestedSchema: { properties: {} } }, 'r');
  assert.deepEqual(askResult(elicit, 'accept'), { action: 'accept', content: {}, _meta: null });
  assert.deepEqual(askResult(elicit, 'decline'), { action: 'decline', content: null, _meta: null });

  const question = parseServerAsk(
    'item/tool/requestUserInput',
    { questions: [{ id: 'q1', question: 'which?', options: [{ label: 'a' }] }] },
    'r',
  );
  assert.deepEqual(askResult(question, 'a'), { answers: { q1: { answers: ['a'] } } });
});

test('the refusal shape differs per method — an empty object fails every one', () => {
  assert.deepEqual(refuseResult('mcpServer/elicitation/request'), {
    action: 'decline',
    content: null,
    _meta: null,
  });
  assert.deepEqual(refuseResult(EXEC_APPROVAL), { decision: 'decline' });
  assert.deepEqual(refuseResult('item/tool/requestUserInput'), { answers: {} });
  // The legacy v1 `ReviewDecision` refusal is an OBJECT carrying its reason.
  assert.deepEqual(refuseResult('execCommandApproval'), {
    decision: { denied: { rejection: 'the Companion cannot present this request' } },
  });
});

// ── The R1 card shapes ───────────────────────────────────────────────────────

test('an approval renders through R1s existing permission card', () => {
  const ask = parseServerAsk(EXEC_APPROVAL, { command: 'ls', availableDecisions: ['accept', 'decline'] }, 'req-1');
  const payload = askEventPayload(ask);
  // The ACP-permission shape `parseApprovalRequest` reads: a `request_id`, a
  // `params.toolCall` naming what is gated, and `params.options[].optionId`.
  assert.equal(payload.request_id, 'req-1');
  const params = payload.params as Record<string, unknown>;
  assert.equal((params.toolCall as Record<string, unknown>).name, EXEC_APPROVAL);
  assert.deepEqual((params.options as Record<string, unknown>[]).map((o) => o.optionId), ['accept', 'decline']);
});

test('a question renders through R1s existing question card', () => {
  const ask = parseServerAsk(
    'item/tool/requestUserInput',
    { questions: [{ id: 'q1', question: 'Which env?', options: [{ label: 'staging' }] }] },
    'req-6',
  );
  const payload = askEventPayload(ask);
  // `parseApprovalRequest` discriminates on `dialog_type` and keys the reply on
  // `tool_use_id`; the card answers with the option's LABEL.
  assert.equal(payload.dialog_type, 'user_question');
  assert.equal(payload.tool_use_id, 'req-6');
  const q = (payload.questions as Record<string, unknown>[])[0];
  assert.equal(q.question, 'Which env?');
  assert.deepEqual((q.options as Record<string, unknown>[]).map((o) => o.label), ['staging']);
});

// ── Deltas ───────────────────────────────────────────────────────────────────

test('both delta spellings observed on the wire are recognised', () => {
  assert.equal(isDeltaMethod('item/agentMessage/delta'), true);
  assert.equal(isDeltaMethod('item/commandExecution/outputDelta'), true);
  assert.equal(isDeltaMethod('item/agentReasoningRawContentDelta'), true);
  assert.equal(isDeltaMethod('item/completed'), false);
  assert.equal(isDeltaMethod(''), false);
});

test('trimToTail keeps the end and never leaves half a line', () => {
  assert.equal(trimToTail('abc', 10), 'abc');
  // Cut lands mid-line, so it moves forward to the next boundary.
  assert.equal(trimToTail('aaaa\nbbbb\ncccc', 9), 'cccc');
  // No newline in the kept window: the raw tail is the honest answer.
  assert.equal(trimToTail('abcdefghij', 4), 'ghij');
});
