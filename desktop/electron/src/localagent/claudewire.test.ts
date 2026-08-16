/// claude wire shapes (vision-parity L3a). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  buildInputFrame,
  buildLaunchArgs,
  ContextWindows,
  CONFIG_HOME_ENV,
  resolveConfigHome,
  toolArgs,
  TurnClock,
} from './claudewire.ts';
import { DEFAULT_TOOL_POSTURE, isToolPosture } from './driver.ts';
import type { Family } from './families.ts';

const FAMILY: Family = {
  family: 'claude-code',
  bin: 'claude',
  supports: ['M1', 'M2', 'M4'],
  launch: { M2: { mode_args: ['--print', '--output-format', 'stream-json', '--input-format', 'stream-json', '--verbose'] } },
};

function parseFrame(line: string): Record<string, unknown> {
  assert.equal(line.endsWith('\n'), true, 'input frame must end with a newline');
  return JSON.parse(line) as Record<string, unknown>;
}

function contentOf(line: string): Record<string, unknown>[] {
  const frame = parseFrame(line);
  const message = frame.message as Record<string, unknown>;
  return message.content as Record<string, unknown>[];
}

// ── config home ──────────────────────────────────────────────────────────────

test('resolveConfigHome prefers the explicit override', () => {
  assert.equal(resolveConfigHome('/spawn/root', { [CONFIG_HOME_ENV]: '/env/root' }, '/home/u'), '/spawn/root');
});

test('resolveConfigHome falls back to the env var, then to ~/.claude', () => {
  assert.equal(resolveConfigHome(undefined, { [CONFIG_HOME_ENV]: '/env/root' }, '/home/u'), '/env/root');
  assert.equal(resolveConfigHome(undefined, {}, '/home/u'), '/home/u/.claude');
});

test('resolveConfigHome treats blank as absent, not as a root', () => {
  // A "   " override is a config mistake; honouring it would point the child at
  // the process cwd and the failure would be silent.
  assert.equal(resolveConfigHome('   ', { [CONFIG_HOME_ENV]: '/env/root' }, '/home/u'), '/env/root');
  assert.equal(resolveConfigHome('   ', { [CONFIG_HOME_ENV]: '  ' }, '/home/u'), '/home/u/.claude');
});

// ── tool posture ─────────────────────────────────────────────────────────────

test('the default posture cannot write, execute, or reach the network', () => {
  // Measured, not assumed: --permission-mode (including `plan`) does not gate a
  // --print child; the tool list is the only lever. If this default ever drifts
  // to `unrestricted`, a local session silently gains Bash on the user's box.
  assert.equal(DEFAULT_TOOL_POSTURE, 'read_local');
  const args = toolArgs(DEFAULT_TOOL_POSTURE);
  assert.deepEqual(args, ['--tools', 'Read,Glob,Grep']);
  for (const forbidden of ['Bash', 'Write', 'Edit', 'NotebookEdit', 'Task', 'WebFetch', 'WebSearch']) {
    assert.equal(args[1].split(',').includes(forbidden), false, `${forbidden} must not be in the default posture`);
  }
});

test('converse disables all tools with the empty-string form', () => {
  // `--tools ""` is claude's documented "disable all tools". Dropping the empty
  // argument would silently mean "no --tools flag" — i.e. everything.
  assert.deepEqual(toolArgs('converse'), ['--tools', '']);
});

test('unrestricted adds no flag at all', () => {
  assert.deepEqual(toolArgs('unrestricted'), []);
});

test('isToolPosture rejects anything not in the set', () => {
  assert.equal(isToolPosture('read_local'), true);
  assert.equal(isToolPosture('unrestricted'), true);
  assert.equal(isToolPosture('converse'), true);
  for (const bad of ['', 'full', 'skip', 'prompt', 'plan', null, undefined, 7, {}]) {
    assert.equal(isToolPosture(bad), false, `${String(bad)} must not pass`);
  }
});

// ── launch args ──────────────────────────────────────────────────────────────

test('launch args come from the family registry, not from this file', () => {
  const args = buildLaunchArgs(FAMILY, { posture: 'converse' });
  assert.deepEqual(args.slice(0, 6), ['--print', '--output-format', 'stream-json', '--input-format', 'stream-json', '--verbose']);
  assert.deepEqual(args.slice(6), ['--tools', '']);
});

test('a model is appended, an absent one adds nothing', () => {
  assert.deepEqual(buildLaunchArgs(FAMILY, { posture: 'unrestricted', model: 'claude-opus-5' }).slice(-2), ['--model', 'claude-opus-5']);
  assert.equal(buildLaunchArgs(FAMILY, { posture: 'unrestricted' }).includes('--model'), false);
  assert.equal(buildLaunchArgs(FAMILY, { posture: 'unrestricted', model: '' }).includes('--model'), false);
});

test('an assigned session id is passed through as --session-id', () => {
  // Probed on claude 2.1.220: the flag is honoured, and both the init and
  // result frames report the id we passed. Assigning it is what gives a session
  // a resume handle before its first frame — see service.ts.
  const args = buildLaunchArgs(FAMILY, { posture: 'converse', sessionId: 'a-uuid' });
  assert.deepEqual(args.slice(-2), ['--session-id', 'a-uuid']);
});

test('resume tokens are appended verbatim from the recipe table', () => {
  const args = buildLaunchArgs(FAMILY, { posture: 'converse', resumeTokens: ['--resume', 'eng-1'] });
  assert.deepEqual(args.slice(-2), ['--resume', 'eng-1']);
  assert.equal(args.includes('--session-id'), false);
});

test('resuming and assigning at once is refused', () => {
  // `--resume` names an existing conversation and `--session-id` names a new
  // one. Passing both asks the engine to be two sessions, and whichever it
  // picks is a coin flip we would not see the result of until the transcript
  // turned out to be the wrong one.
  assert.throws(
    () => buildLaunchArgs(FAMILY, { posture: 'converse', sessionId: 'a-uuid', resumeTokens: ['--resume', 'eng-1'] }),
    /cannot both resume/,
  );
});

test('an empty resume token list is a fresh launch, not a broken one', () => {
  const args = buildLaunchArgs(FAMILY, { posture: 'converse', resumeTokens: [] });
  assert.equal(args.includes('--resume'), false);
  // ...and it does not block assigning an id.
  assert.deepEqual(
    buildLaunchArgs(FAMILY, { posture: 'converse', resumeTokens: [], sessionId: 'a-uuid' }).slice(-2),
    ['--session-id', 'a-uuid'],
  );
});

test('a family with no M2 launch contract refuses to launch', () => {
  // Silently launching without the mode args gives an interactive child on a
  // pipe: it never speaks stream-json and the session just hangs.
  assert.throws(() => buildLaunchArgs({ family: 'x', bin: 'x' }, { posture: 'converse' }), /no launch.M2.mode_args/);
  assert.throws(
    () => buildLaunchArgs({ family: 'x', bin: 'x', launch: { M2: { mode_args: [] } } }, { posture: 'converse' }),
    /no launch.M2.mode_args/,
  );
});

// ── input frames ─────────────────────────────────────────────────────────────

test('a text input becomes a user frame with one text block', () => {
  const content = contentOf(buildInputFrame('text', { body: 'hello' }));
  assert.deepEqual(content, [{ type: 'text', text: 'hello' }]);
});

test('attachments lead and the caption follows', () => {
  const content = contentOf(
    buildInputFrame('text', {
      body: 'what is this',
      images: [{ mime: 'image/png', data: 'AAAA' }],
      pdfs: [{ mime: 'application/pdf', data: 'BBBB', filename: 'spec.pdf' }],
    }),
  );
  assert.equal(content.length, 3);
  assert.deepEqual(content[0], { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'AAAA' } });
  assert.deepEqual(content[1], {
    type: 'document',
    source: { type: 'base64', media_type: 'application/pdf', data: 'BBBB' },
    title: 'spec.pdf',
  });
  assert.deepEqual(content[2], { type: 'text', text: 'what is this' });
});

test('a pdf without a filename carries no title key', () => {
  const content = contentOf(buildInputFrame('text', { pdfs: [{ mime: 'application/pdf', data: 'B' }] }));
  assert.equal('title' in content[0], false);
});

test('an attachment with no caption is still a valid turn', () => {
  const content = contentOf(buildInputFrame('text', { images: [{ mime: 'image/png', data: 'A' }] }));
  assert.equal(content.length, 1);
  assert.equal(content[0].type, 'image');
});

test('a text input with neither body nor attachment is refused', () => {
  assert.throws(() => buildInputFrame('text', {}), /no body and no attachments/);
  assert.throws(() => buildInputFrame('text', { body: '', images: [], pdfs: [] }), /no body and no attachments/);
});

test('an approval becomes a tool_result and a denial marks is_error', () => {
  const ok = contentOf(buildInputFrame('approval', { request_id: 'tu_1', decision: 'allow' }))[0];
  assert.deepEqual(ok, { type: 'tool_result', tool_use_id: 'tu_1', content: 'allow', is_error: false });

  const denied = contentOf(buildInputFrame('approval', { request_id: 'tu_1', decision: 'deny', note: 'not that path' }))[0];
  assert.equal(denied.is_error, true);
  assert.equal(denied.content, 'deny: not that path');
});

test('an answer carries the body verbatim, with no decision prefix', () => {
  // Carved off `approval` upstream precisely so the agent does not have to peel
  // a "decision: " prefix off the user's words.
  const block = contentOf(buildInputFrame('answer', { request_id: 'tu_2', body: 'the second one' }))[0];
  assert.deepEqual(block, { type: 'tool_result', tool_use_id: 'tu_2', content: 'the second one', is_error: false });
});

test('approval and answer refuse a missing correlation id', () => {
  assert.throws(() => buildInputFrame('approval', { decision: 'allow' }), /request_id/);
  assert.throws(() => buildInputFrame('approval', { request_id: 'x' }), /decision/);
  assert.throws(() => buildInputFrame('answer', { body: 'hi' }), /request_id/);
  assert.throws(() => buildInputFrame('answer', { request_id: 'x' }), /body/);
});

test('cancel has a default reason', () => {
  assert.deepEqual(contentOf(buildInputFrame('cancel', {}))[0], { type: 'text', text: 'cancel: user requested cancel' });
  assert.deepEqual(contentOf(buildInputFrame('cancel', { reason: 'changed my mind' }))[0], {
    type: 'text',
    text: 'cancel: changed my mind',
  });
});

test('every frame is a single line', () => {
  // Two newlines in one write would be two frames to the engine, and the
  // second would be a parse error on its side.
  const line = buildInputFrame('text', { body: 'multi\nline\nbody' });
  assert.equal(line.split('\n').length, 2, 'body newlines must be JSON-escaped, not literal');
});

// ── turn clock ───────────────────────────────────────────────────────────────

test('turn ids are stable and increment', () => {
  const clock = new TurnClock();
  assert.equal(clock.next(), 't-1');
  assert.equal(clock.next(), 't-2');
  assert.equal(clock.issued, 2);
});

// ── context windows ──────────────────────────────────────────────────────────

test('a window learned from turn.result stamps later usage', () => {
  const cw = new ContextWindows();
  cw.apply('turn.result', { by_model: { 'claude-opus-5': { context_window: 200000 } } });
  const usage: Record<string, unknown> = { model: 'claude-opus-5', input_tokens: 10 };
  cw.apply('usage', usage);
  assert.equal(usage.context_window, 200000);
});

test('the engine outranks our memory of the engine', () => {
  const cw = new ContextWindows();
  cw.apply('turn.result', { by_model: { m: { context_window: 200000 } } });
  const usage: Record<string, unknown> = { model: 'm', context_window: 1000000 };
  cw.apply('usage', usage);
  assert.equal(usage.context_window, 1000000);
});

test('an unknown model leaves the field absent rather than zero', () => {
  // The clients suppress the ring on a missing window and would render a wrong
  // percentage on a zero one. Absent beats wrong.
  const cw = new ContextWindows();
  const usage: Record<string, unknown> = { model: 'never-seen', input_tokens: 5 };
  cw.apply('usage', usage);
  assert.equal('context_window' in usage, false);
});

test('the first turn has no window and that is the documented cost', () => {
  // No static table is ported, so nothing has reported a window yet. Pinned so
  // the narrowing is a decision on record, not a bug someone later "fixes" by
  // copying a heuristic table in.
  const cw = new ContextWindows();
  const usage: Record<string, unknown> = { model: 'claude-opus-5', input_tokens: 5 };
  cw.apply('usage', usage);
  assert.equal('context_window' in usage, false);
});

test('malformed by_model blocks are skipped, not thrown on', () => {
  const cw = new ContextWindows();
  for (const bad of [null, 'x', 42, [], { m: null }, { m: 'x' }, { m: { context_window: 0 } }, { m: { context_window: -1 } }, { m: { context_window: 'big' } }]) {
    cw.apply('turn.result', { by_model: bad });
  }
  const usage: Record<string, unknown> = { model: 'm' };
  cw.apply('usage', usage);
  assert.equal('context_window' in usage, false);
});

test('usage with no model is left alone', () => {
  const cw = new ContextWindows();
  cw.apply('turn.result', { by_model: { m: { context_window: 100 } } });
  const usage: Record<string, unknown> = { input_tokens: 5 };
  cw.apply('usage', usage);
  assert.equal('context_window' in usage, false);
});

test('apply routes only the two kinds it owns', () => {
  // A `text` payload carrying a stray `by_model` must not teach the memo
  // anything — learning is turn.result's job alone.
  const cw = new ContextWindows();
  cw.apply('text', { by_model: { m: { context_window: 999 } } });
  const usage: Record<string, unknown> = { model: 'm' };
  cw.apply('usage', usage);
  assert.equal('context_window' in usage, false);
});
