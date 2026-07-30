import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { disposeRerun, rerunStart, rerunStatus, rerunStop } from './rerun.ts';

/// rerun_spawn.test.ts — the first test that actually LAUNCHES something
/// against the Rerun manager (J8 Replay W4a/W4b-1).
///
/// `rerun_policy.test.ts` covers the pure decisions. This covers the spawn: that
/// the manager finds a launcher, hands it the argv it built, waits for the right
/// line on the right stream, derives the viewer URL, and can stop it again. Real
/// rerun is not installed here (nor on CI), so the launcher is a stub — but the
/// stub is a real child process, and the argv asserted below is the argv rerun
/// would have received.
///
/// The load-bearing assertion is `--bind 127.0.0.1`. Rerun's
/// `WebViewerConfig.bind_ip` defaults to 0.0.0.0, so a `--serve-web` without it
/// publishes the robot's episodes to the whole network and looks fine while
/// doing so. rerun_policy.test.ts asserts the argv *builder* emits it; this
/// asserts it survives all the way into a process's real argv.
///
/// Still NOT covered anywhere: whether the rerun viewer renders the recording.
/// That needs real rerun, a real .rrd and a display.

function stubRerun(t: { after: (fn: () => void) => void }, body: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'rerun-stub-'));
  const bin = path.join(dir, 'rerun');
  fs.writeFileSync(bin, `#!/bin/sh\n${body}`, { mode: 0o755 });
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return bin;
}

function stubRecording(t: { after: (fn: () => void) => void }): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'rerun-rec-'));
  const rrd = path.join(dir, 'lerobot_pusht_episode_0.rrd');
  fs.writeFileSync(rrd, 'RRD');
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return rrd;
}

test('the manager launches a server, and --bind 127.0.0.1 reaches its real argv', async (t) => {
  if (process.platform === 'win32') {
    t.skip('the stub launcher is a POSIX shell script');
    return;
  }
  const argvFile = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'rerun-argv-')), 'argv');
  t.after(() => fs.rmSync(path.dirname(argvFile), { recursive: true, force: true }));

  // Record argv, print what rerun prints on stderr (re_log writes there), then
  // stay alive like a server does.
  const bin = stubRerun(
    t,
    `for a in "$@"; do printf '%s\\n' "$a" >> ${JSON.stringify(argvFile)}; done
printf 'INFO Serving web viewer at http://127.0.0.1:9999?url=rerun%%2Bhttp://127.0.0.1:9876/proxy\\n' >&2
while true; do sleep 1; done
`,
  );
  process.env.TERMIPOD_RERUN_BIN = bin;
  t.after(() => {
    delete process.env.TERMIPOD_RERUN_BIN;
    disposeRerun();
  });

  const rec = stubRecording(t);
  const { url } = await rerunStart(rec);

  assert.match(url, /^http:\/\/127\.0\.0\.1:\d+\?url=rerun%2Bhttp/, `viewer url = ${url}`);
  assert.equal(rerunStatus().running, true);
  assert.equal(rerunStatus().recording, rec);

  const argv = fs.readFileSync(argvFile, 'utf8').trim().split('\n');
  assert.ok(argv.includes('--serve-web'), `argv = ${argv.join(' ')}`);
  const bindAt = argv.indexOf('--bind');
  assert.notEqual(bindAt, -1, 'no --bind in the real argv: rerun would bind 0.0.0.0');
  assert.equal(
    argv[bindAt + 1],
    '127.0.0.1',
    'the bind address that actually reached the process is not loopback',
  );
  assert.equal(argv[argv.length - 1], rec, 'the recording must be the final positional');

  // The two ports must differ or rerun refuses to start.
  const webPort = argv[argv.indexOf('--web-viewer-port') + 1];
  const grpcPort = argv[argv.indexOf('--port') + 1];
  assert.notEqual(webPort, grpcPort);

  await rerunStop();
  assert.equal(rerunStatus().running, false);
});

test('a launcher that exits before serving fails with its own output', async (t) => {
  if (process.platform === 'win32') {
    t.skip('the stub launcher is a POSIX shell script');
    return;
  }
  const bin = stubRerun(t, `printf 'error: could not open recording\\n' >&2\nexit 3\n`);
  process.env.TERMIPOD_RERUN_BIN = bin;
  t.after(() => {
    delete process.env.TERMIPOD_RERUN_BIN;
    disposeRerun();
  });

  await assert.rejects(rerunStart(stubRecording(t)), (e: Error) => {
    // The exit code and the launcher's own words both have to survive, or the
    // director sees "it didn't work" with nothing to act on.
    assert.match(e.message, /exited/);
    assert.match(e.message, /could not open recording/);
    return true;
  });
  assert.equal(rerunStatus().running, false);
});

test('a missing launcher is named rather than waited out', async (t) => {
  process.env.TERMIPOD_RERUN_BIN = path.join(os.tmpdir(), 'definitely-not-here', 'rerun');
  t.after(() => {
    delete process.env.TERMIPOD_RERUN_BIN;
    disposeRerun();
  });
  await assert.rejects(rerunStart(stubRecording(t)), /rerun not found/);
});

test('a path that is not an absolute .rrd never reaches a spawn', async (t) => {
  // No TERMIPOD_RERUN_BIN at all: if the guard failed, this would report a
  // missing launcher instead of a bad path, which is how the distinction shows.
  delete process.env.TERMIPOD_RERUN_BIN;
  t.after(disposeRerun);
  for (const bad of ['relative/x.rrd', '/tmp/x.txt', '', '   ']) {
    await assert.rejects(rerunStart(bad), /recording/i, `accepted ${JSON.stringify(bad)}`);
  }
});
