/// Plumbing tests for the model-graph tracer runner (plan §5). The torchview
/// *trace* itself needs a torch venue, but the invocation contract — helper piped
/// to stdin, params via env, cwd honoured, multi-word preset argv-split, exit code
/// + timeout surfaced — is verified here against the real `python3` on PATH (no
/// torch needed). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { traceHandlers } from './trace.ts';

const run = traceHandlers.trace_run;

test('trace_run: helper arrives on stdin and env params are visible', async () => {
  const helper = 'import sys,os\nsys.stdout.write("ENTRY=" + os.environ.get("TRACE_ENTRY","?") + "\\n")\nsys.stdout.write("STDIN=" + sys.stdin.read().count("\\n").__str__())\n';
  const r = (await run({ command: 'python3', content: helper, env: { TRACE_ENTRY: 'Model(dim=512)' } }, {} as never)) as {
    code: number | null;
    stdout: string;
    timedOut: boolean;
  };
  assert.equal(r.code, 0);
  assert.equal(r.timedOut, false);
  assert.match(r.stdout, /ENTRY=Model\(dim=512\)/);
});

test('trace_run: cwd is honoured', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'trace-'));
  try {
    await writeFile(path.join(dir, 'marker.txt'), 'hi');
    const helper = 'import os\nprint("has_marker", os.path.exists("marker.txt"))';
    const r = (await run({ command: 'python3', content: helper, cwd: dir }, {} as never)) as { stdout: string };
    assert.match(r.stdout, /has_marker True/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('trace_run: a multi-word preset splits into argv (env python3 -u ≈ python3)', async () => {
  // `env python3` exercises the whitespace argv-split with a real launcher.
  const r = (await run({ command: 'env python3', content: 'print("multiword ok")' }, {} as never)) as { code: number | null; stdout: string };
  assert.equal(r.code, 0);
  assert.match(r.stdout, /multiword ok/);
});

test('trace_run: a non-zero exit surfaces the code + stderr (not a throw)', async () => {
  const r = (await run({ command: 'python3', content: 'import sys\nsys.stderr.write("boom\\n")\nsys.exit(3)' }, {} as never)) as {
    code: number | null;
    stderr: string;
  };
  assert.equal(r.code, 3);
  assert.match(r.stderr, /boom/);
});

test('trace_run: an unlaunchable interpreter rejects', async () => {
  await assert.rejects(() => Promise.resolve(run({ command: 'definitely-not-a-real-python-xyz', content: 'x' }, {} as never)));
});

test('trace_run: the wall-clock cap kills a hung helper', async () => {
  const r = (await run({ command: 'python3', content: 'import time\ntime.sleep(30)', timeoutMs: 400 }, {} as never)) as {
    timedOut: boolean;
    code: number | null;
  };
  assert.equal(r.timedOut, true);
  assert.equal(r.code, null);
});
