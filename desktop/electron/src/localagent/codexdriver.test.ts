/// The codex app-server driver (vision-parity L4c). Run with `node --test`.
///
/// Driven against a fake channel rather than a real app-server: the byte path
/// is L4b's and already has its own live e2e, so what is asserted here is the
/// PROTOCOL — which call opens a thread, what a director's click writes back,
/// and what the transcript says while all of it happens.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { CodexDriver } from './codexdriver.ts';
import type { CodexChannel, CodexFrame, CodexChannelHandlers } from './codexchannel.ts';
import type { DriverEvent } from './driver.ts';
import type { Family } from './families.ts';

const FAMILY: Family = {
  family: 'codex',
  bin: 'codex',
  supports: ['M2'],
  launch: { M2: { mode_args: ['app-server', '--listen', 'stdio://'] } },
  frame_profile: {
    rules: [
      {
        match: { method: 'thread/started' },
        emit: {
          kind: 'session.init',
          producer: 'agent',
          payload: { session_id: '$.params.thread.id' },
        },
      },
      {
        match: { method: 'item/completed', 'params.item.type': 'agentMessage' },
        emit: {
          kind: 'text',
          producer: 'agent',
          payload: { text: '$.params.item.text', message_id: '$.params.item.id' },
        },
      },
    ],
  },
};

interface Rig {
  driver: CodexDriver;
  events: DriverEvent[];
  /// Frames the driver wrote to the channel.
  sent: CodexFrame[];
  /// Push a frame at the driver, as the engine would.
  recv: (frame: CodexFrame) => void;
  /// Drop the channel, as a dead engine would.
  hangup: (code: number | null, reason: string) => void;
  closed: () => number;
  exits: (number | null)[];
}

function rig(opts: {
  resumeThreadId?: string;
  posture?: 'converse' | 'read_local' | 'unrestricted';
  flushIntervalMs?: number;
  mode?: 'daemon' | 'spawn';
  reason?: string;
} = {}): Rig {
  const events: DriverEvent[] = [];
  const sent: CodexFrame[] = [];
  const exits: (number | null)[] = [];
  let handlers: CodexChannelHandlers | null = null;
  let closes = 0;

  const driver = new CodexDriver({
    family: FAMILY,
    cwd: '/w',
    env: { PATH: '/bin' },
    homeDir: '/home/u',
    ...(opts.posture !== undefined ? { posture: opts.posture } : {}),
    ...(opts.resumeThreadId !== undefined ? { resumeThreadId: opts.resumeThreadId } : {}),
    ...(opts.flushIntervalMs !== undefined ? { flushIntervalMs: opts.flushIntervalMs } : {}),
    plan: { mode: 'spawn', argv: ['codex', 'app-server'], reason: 'test rung' },
    openChannel: (plan, h) => {
      handlers = h;
      const channel: CodexChannel = {
        mode: opts.mode ?? 'spawn',
        reason: opts.reason ?? plan.reason,
        send: (frame) => {
          sent.push(frame);
        },
        close: () => {
          closes += 1;
        },
      };
      return Promise.resolve(channel);
    },
    onEvent: (ev) => events.push(ev),
    onExit: (code) => exits.push(code),
  });

  return {
    driver,
    events,
    sent,
    recv: (frame) => handlers?.onFrame(frame),
    hangup: (code, reason) => handlers?.onClose({ code, reason }),
    closed: () => closes,
    exits,
  };
}

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));
const after = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/// Answer the handshake the way a real app-server does, and return the thread
/// id it reported.
async function handshake(r: Rig, threadId = 'th-1'): Promise<string> {
  r.driver.start();
  await tick();
  // initialize
  const init = r.sent.find((f) => f.method === 'initialize');
  assert.ok(init !== undefined, 'expected an initialize call');
  r.recv({ jsonrpc: '2.0', id: init.id, result: { userAgent: 'codex/0.147.0' } });
  await tick();
  const open = r.sent.find((f) => f.method === 'thread/start' || f.method === 'thread/resume');
  assert.ok(open !== undefined, 'expected thread/start or thread/resume');
  r.recv({
    jsonrpc: '2.0',
    id: open.id,
    result: { thread: { id: threadId }, model: 'gpt-5.6', cwd: '/w' },
  });
  await tick();
  await tick();
  return threadId;
}

const kinds = (events: DriverEvent[]): string[] => events.map((e) => e.kind);
const find = (events: DriverEvent[], kind: string): DriverEvent | undefined =>
  events.find((e) => e.kind === kind);

// ── Opening ──────────────────────────────────────────────────────────────────

test('the lifecycle row states the boundary, not just the posture name', async () => {
  const r = rig({ posture: 'read_local' });
  await handshake(r);
  const life = find(r.events, 'lifecycle');
  assert.ok(life !== undefined);
  assert.equal(life.payload.tool_posture, 'read_local');
  // A reader should not have to know our mapping table to know whether this
  // agent can write to their disk.
  assert.equal(life.payload.sandbox, 'read-only');
  assert.equal(life.payload.approval_policy, 'never');
  assert.equal(life.payload.resumed, false);
});

test('converse records the difference it could not keep', async () => {
  const r = rig({ posture: 'converse' });
  await handshake(r);
  assert.match(String(find(r.events, 'lifecycle')?.payload.posture_note ?? ''), /no tool-disable switch/);
});

test('which rung opened is a transcript row, because the two make different promises', async () => {
  const r = rig({ mode: 'daemon', reason: 'attached to the shared codex app-server daemon' });
  await handshake(r);
  const row = r.events.find((e) => e.kind === 'system' && e.payload.kind === 'codex_channel');
  assert.ok(row !== undefined, 'expected a codex_channel row');
  assert.equal(row.payload.channel, 'daemon');
  assert.match(String(row.payload.reason), /shared codex app-server daemon/);
});

test('the handshake is initialize, then the initialized notification, then thread/start', async () => {
  const r = rig();
  await handshake(r);
  const methods = r.sent.map((f) => f.method);
  assert.deepEqual(methods, ['initialize', 'initialized', 'thread/start']);
  // The notification carries no id — a request would block waiting for a
  // response app-server never sends.
  assert.equal(r.sent[1].id, undefined);
  assert.equal(r.driver.threadId, 'th-1');
});

test('a resume calls thread/resume and mints the session.init the engine will not', async () => {
  const r = rig({ resumeThreadId: 'th-old' });
  await handshake(r, 'th-old');
  const resume = r.sent.find((f) => f.method === 'thread/resume');
  assert.ok(resume !== undefined);
  assert.equal((resume.params as Record<string, unknown>).threadId, 'th-old');
  // Measured: thread/resume emits no `thread/started`, so the profile's
  // session.init rule never fires and the row has to come from here — without
  // it a reattached session has no init row at all.
  const init = find(r.events, 'session.init');
  assert.ok(init !== undefined, 'expected a synthesized session.init on resume');
  assert.equal(init.payload.session_id, 'th-old');
  assert.equal(init.payload.resumed, true);
  assert.equal(find(r.events, 'lifecycle')?.payload.resumed, true);
});

test('a fresh start does NOT synthesize session.init — the profile owns that row', async () => {
  const r = rig();
  await handshake(r);
  assert.equal(find(r.events, 'session.init'), undefined);
  // It arrives when the engine says so, through the shared frame profile.
  r.recv({ jsonrpc: '2.0', method: 'thread/started', params: { thread: { id: 'th-1' } } });
  assert.equal(find(r.events, 'session.init')?.payload.session_id, 'th-1');
});

test('a failed handshake reports an error row and stops, rather than throwing into nowhere', async () => {
  const r = rig();
  r.driver.start();
  await tick();
  const init = r.sent.find((f) => f.method === 'initialize');
  r.recv({ jsonrpc: '2.0', id: init?.id, error: { code: -32600, message: 'bad client' } });
  await tick();
  await tick();
  assert.match(String(find(r.events, 'error')?.payload.text ?? ''), /bad client/);
  const stopped = r.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped');
  assert.equal(stopped.length, 1);
  assert.equal(stopped[0].payload.expected, false);
  assert.equal(r.driver.running, false);
});

// ── Turns ────────────────────────────────────────────────────────────────────

test('text before the thread exists is QUEUED, not lost', async () => {
  const r = rig();
  r.driver.start();
  await tick();
  r.driver.input('text', { body: 'early' });
  assert.equal(r.sent.some((f) => f.method === 'turn/start'), false);

  await handshake(r);
  const turn = r.sent.find((f) => f.method === 'turn/start');
  assert.ok(turn !== undefined, 'the queued turn should be sent once the thread opens');
  assert.deepEqual((turn.params as Record<string, unknown>).input, [{ type: 'text', text: 'early' }]);
});

test('a payload codex cannot carry throws at the call site, even while queued', async () => {
  const r = rig();
  r.driver.start();
  await tick();
  // Built before the queue, so the caller learns immediately rather than
  // having the failure surface later against a turn they think is running.
  assert.throws(
    () => r.driver.input('text', { body: 'x', pdfs: [{ mime: 'application/pdf', data: 'JVBER' }] }),
    /no file attachments/,
  );
});

test('cancel interrupts the tracked turn with BOTH ids codex requires', async () => {
  const r = rig();
  await handshake(r);
  r.driver.input('text', { body: 'go' });
  await tick();
  const turn = r.sent.find((f) => f.method === 'turn/start');
  r.recv({ jsonrpc: '2.0', id: turn?.id, result: { turn: { id: 'tu-1' } } });
  await tick();

  r.driver.input('cancel', { reason: 'stop' });
  await tick();
  const interrupt = r.sent.find((f) => f.method === 'turn/interrupt');
  assert.ok(interrupt !== undefined);
  // Without either id codex answers -32600 "missing field".
  assert.deepEqual(interrupt.params, { threadId: 'th-1', turnId: 'tu-1' });
});

// ── Approvals ────────────────────────────────────────────────────────────────

test('an approval is parked as an R1 card and the click becomes the JSON-RPC response', async () => {
  const r = rig();
  await handshake(r);
  r.recv({
    jsonrpc: '2.0',
    id: 7,
    method: 'item/commandExecution/requestApproval',
    params: { command: 'ls', availableDecisions: ['accept', 'decline'] },
  });

  const card = find(r.events, 'approval_request');
  assert.ok(card !== undefined, 'expected an approval_request event');
  const requestId = String(card.payload.request_id);
  assert.match(requestId, /^codex-1-7$/);

  r.driver.input('approval', { request_id: requestId, decision: 'accept' });
  const answer = r.sent.find((f) => f.id === 7);
  assert.ok(answer !== undefined, 'expected a response on the parked id');
  assert.deepEqual(answer.result, { decision: 'accept' });
});

test('a question is answered with the option label, on the question-id map', async () => {
  const r = rig();
  await handshake(r);
  r.recv({
    jsonrpc: '2.0',
    id: 9,
    method: 'item/tool/requestUserInput',
    params: { questions: [{ id: 'q1', question: 'which?', options: [{ label: 'staging' }] }] },
  });
  const card = find(r.events, 'approval_request');
  assert.equal(card?.payload.dialog_type, 'user_question');

  r.driver.input('answer', { request_id: String(card?.payload.tool_use_id), body: 'staging' });
  const answer = r.sent.find((f) => f.id === 9);
  assert.deepEqual(answer?.result, { answers: { q1: { answers: ['staging'] } } });
});

test('a request we cannot present is refused AT ONCE, not parked forever', async () => {
  const r = rig();
  await handshake(r);
  r.recv({
    jsonrpc: '2.0',
    id: 11,
    method: 'mcpServer/elicitation/request',
    params: { serverName: 'docs', mode: 'form', message: 'branch?', requestedSchema: { properties: { b: {} } } },
  });
  // No card — a card nobody can answer holds the engine open forever.
  assert.equal(find(r.events, 'approval_request'), undefined);
  const answer = r.sent.find((f) => f.id === 11);
  assert.deepEqual(answer?.result, { action: 'decline', content: null, _meta: null });
  const row = r.events.find((e) => e.kind === 'system' && e.payload.kind === 'codex_request_refused');
  assert.match(String(row?.payload.reason ?? ''), /structured fields/);
});

test('cancel drains parked gates, so the next turn is not stuck behind one', async () => {
  const r = rig();
  await handshake(r);
  r.recv({ jsonrpc: '2.0', id: 5, method: 'item/fileChange/requestApproval', params: {} });
  assert.ok(find(r.events, 'approval_request') !== undefined);

  r.driver.input('cancel', { reason: 'stop' });
  await tick();
  // turn/interrupt aborts in-flight tool calls, but a parked JSON-RPC id stays
  // open until we write a response.
  assert.deepEqual(r.sent.find((f) => f.id === 5)?.result, { decision: 'decline' });
});

test('stop() answers parked gates before closing the socket', async () => {
  const r = rig({ mode: 'daemon' });
  await handshake(r);
  r.recv({ jsonrpc: '2.0', id: 3, method: 'item/commandExecution/requestApproval', params: { command: 'ls' } });

  r.driver.stop();
  assert.deepEqual(r.sent.find((f) => f.id === 3)?.result, { decision: 'decline' });
  assert.equal(r.closed(), 1);
  assert.equal(r.driver.running, false);
  const stopped = r.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped');
  assert.equal(stopped[0].payload.expected, true);
});

test('answering a card from a previous connection is reported, not misrouted', async () => {
  const r = rig();
  await handshake(r);
  // `codex-0-7` names epoch 0; this connection is epoch 1. The id counter
  // restarts with every connection, so without the epoch a stale card could
  // answer a DIFFERENT request that reached the same number.
  r.driver.input('approval', { request_id: 'codex-0-7', decision: 'accept' });
  const row = r.events.find((e) => e.kind === 'system' && e.payload.kind === 'codex_answer_unmatched');
  assert.ok(row !== undefined, 'expected an unmatched-answer row');
  assert.equal(r.sent.some((f) => f.result !== undefined), false);
});

// ── Streaming ────────────────────────────────────────────────────────────────

test('agentMessage deltas are throttled into one growing partial', async () => {
  const r = rig({ flushIntervalMs: 5 });
  await handshake(r);
  for (const delta of ['Hel', 'lo ', 'there']) {
    r.recv({ jsonrpc: '2.0', method: 'item/agentMessage/delta', params: { itemId: 'msg-1', delta } });
  }
  // A throttle, not a debounce: one flush covers all three.
  await after(25);
  const partials = r.events.filter((e) => e.kind === 'text' && e.payload.partial === true);
  assert.equal(partials.length, 1);
  assert.equal(partials[0].payload.text, 'Hello there');
  assert.equal(partials[0].payload.message_id, 'msg-1');
});

test('command output streams as a tool_call_update with no status', async () => {
  const r = rig({ flushIntervalMs: 5 });
  await handshake(r);
  r.recv({
    jsonrpc: '2.0',
    method: 'item/commandExecution/outputDelta',
    params: { itemId: 'exec-1', delta: 'line one\n' },
  });
  await after(25);
  const update = find(r.events, 'tool_call_update');
  assert.ok(update !== undefined);
  // `toolCallId` is the key BOTH clients fold a running tool card on — not the
  // `tool_use_id` a tool_result carries.
  assert.equal(update.payload.toolCallId, 'exec-1');
  // No `status`: the latest update's status wins over the tool_result's, so a
  // trailing "in_progress" would pin the card at running forever.
  assert.equal('status' in update.payload, false);
});

test('item/completed cancels the pending flush so no partial lands after the final', async () => {
  const r = rig({ flushIntervalMs: 20 });
  await handshake(r);
  r.recv({ jsonrpc: '2.0', method: 'item/agentMessage/delta', params: { itemId: 'msg-1', delta: 'Hel' } });
  r.recv({
    jsonrpc: '2.0',
    method: 'item/completed',
    params: { item: { id: 'msg-1', type: 'agentMessage', text: 'Hello there' } },
  });
  await after(50);
  const partials = r.events.filter((e) => e.kind === 'text' && e.payload.partial === true);
  assert.equal(partials.length, 0, 'a stale partial would supersede the authoritative final');
  assert.equal(find(r.events, 'text')?.payload.text, 'Hello there');
});

test('streaming can be turned off entirely', async () => {
  const r = rig({ flushIntervalMs: -1 });
  await handshake(r);
  r.recv({ jsonrpc: '2.0', method: 'item/agentMessage/delta', params: { itemId: 'msg-1', delta: 'x' } });
  await after(20);
  assert.equal(r.events.some((e) => e.payload.partial === true), false);
});

test('reasoning deltas stay dropped — they are not in the vocabulary', async () => {
  const r = rig({ flushIntervalMs: 5 });
  await handshake(r);
  r.recv({ jsonrpc: '2.0', method: 'item/reasoning/textDelta', params: { itemId: 'rs-1', delta: 'hmm' } });
  await after(20);
  assert.equal(r.events.some((e) => e.payload.partial === true), false);
});

// ── Losing the channel ───────────────────────────────────────────────────────

test('a hangup ends the session as UNexpected and fails anything in flight', async () => {
  const r = rig();
  await handshake(r);
  r.driver.input('text', { body: 'go' });
  await tick();

  r.hangup(1, 'codex app-server exited (code 1)');
  await tick();
  const stopped = r.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped');
  assert.equal(stopped.length, 1);
  assert.equal(stopped[0].payload.expected, false);
  assert.equal(stopped[0].payload.exit_code, 1);
  // The turn that was waiting reports rather than hanging until the app quits.
  assert.match(String(find(r.events, 'error')?.payload.text ?? ''), /exited \(code 1\)/);
  assert.deepEqual(r.exits, [1]);
});

test('stop() after a hangup does not emit a second lifecycle row', async () => {
  const r = rig();
  await handshake(r);
  r.hangup(null, 'socket closed');
  await tick();
  r.driver.stop();
  const stopped = r.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped');
  assert.equal(stopped.length, 1);
});

test('notifications translate through the SHARED frame profile', async () => {
  const r = rig();
  await handshake(r);
  r.recv({
    jsonrpc: '2.0',
    method: 'item/completed',
    params: { item: { id: 'msg-2', type: 'agentMessage', text: 'done' } },
  });
  // No second vocabulary: this is the hub's own codex profile, shipped in
  // agent_families.generated.json.
  assert.deepEqual(kinds(r.events).filter((k) => k === 'text'), ['text']);
  assert.equal(find(r.events, 'text')?.payload.message_id, 'msg-2');
});
