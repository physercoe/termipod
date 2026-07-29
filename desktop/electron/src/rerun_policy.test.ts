import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import {
  RERUN_BIND,
  extractViewerUrl,
  isRecordingPath,
  rerunArgs,
  resolveRerunBinary,
  viewerUrl,
} from './rerun_policy.ts';

const PORTS = { webPort: 9090, grpcPort: 9876 };

test('the launch always pins the bind address to loopback', () => {
  // The single most important assertion in this file. Rerun's own
  // WebViewerConfig.bind_ip defaults to 0.0.0.0 ("Defaults to 0.0.0.0",
  // re_sdk/src/web_viewer.rs), so a --serve-web started WITHOUT this flag
  // publishes the robot's episodes, video and all, to every machine on the
  // network — and would look perfectly fine to whoever launched it.
  const args = rerunArgs('/data/ep.rrd', PORTS);
  const bindAt = args.indexOf('--bind');
  assert.ok(bindAt >= 0, 'no --bind in the argv');
  assert.equal(args[bindAt + 1], '127.0.0.1');
  assert.equal(RERUN_BIND, '127.0.0.1');
});

test('the argv is the form rerun documents for serving a file', () => {
  assert.deepEqual(rerunArgs('/data/ep.rrd', PORTS), [
    '--serve-web',
    '--bind',
    '127.0.0.1',
    '--port',
    '9876',
    '--web-viewer-port',
    '9090',
    '/data/ep.rrd',
  ]);
  // The recording is the trailing positional, so a path that starts with a
  // dash cannot be read as a flag ahead of the ones we set.
  assert.equal(rerunArgs('/data/--weird.rrd', PORTS).at(-1), '/data/--weird.rrd');
});

test('the two ports must differ, because rerun refuses the collision', () => {
  // entrypoint.rs compares them and bails; caught here instead, where the
  // message says which two ports rather than surfacing as an early exit.
  assert.throws(() => rerunArgs('/data/ep.rrd', { webPort: 9090, grpcPort: 9090 }), /must differ/);
});

test('only an absolute .rrd is handed to the process', () => {
  assert.ok(isRecordingPath('/data/lerobot/ep_000001.rrd'));
  assert.ok(isRecordingPath('/data/EP.RRD'));
  assert.ok(isRecordingPath('C:\\data\\ep.rrd'));
  // A relative path resolves against the APP's working directory, not the
  // user's, so it would name a different file than whoever passed it meant.
  assert.ok(!isRecordingPath('ep.rrd'));
  assert.ok(!isRecordingPath('./ep.rrd'));
  // Anything that is not a recording is a way to turn a path we were given
  // into a process argument.
  assert.ok(!isRecordingPath('/etc/passwd'));
  assert.ok(!isRecordingPath('/data/ep.rrd.sh'));
  assert.ok(!isRecordingPath(''));
  assert.ok(!isRecordingPath('   '));
});

test('the constructed viewer URL matches the shape rerun builds', () => {
  // re_sdk/src/web_viewer.rs formats `"{server_url}?url=rerun%2Bhttp://{addr}/proxy"`,
  // and server_url() is `http://<addr>` with NO trailing slash — hence the `?`
  // directly after the port. A stray slash makes a URL that loads the viewer
  // with no recording, which reads as an empty dataset rather than a bad link.
  assert.equal(viewerUrl(PORTS), 'http://127.0.0.1:9090?url=rerun%2Bhttp://127.0.0.1:9876/proxy');
  assert.ok(!viewerUrl(PORTS).includes(':9090/?'), 'no slash before the query');
});

test('the viewer URL is recovered from the startup log', () => {
  // The real line, from re_sdk/src/web_viewer.rs. Note the BOUND url comes
  // first and has no query string — the parse must not stop on it.
  const log =
    '[2026-07-29T10:00:00Z INFO  re_sdk] Hosting a web-viewer at http://127.0.0.1:9090 ' +
    '- connect at http://127.0.0.1:9090?url=rerun%2Bhttp://127.0.0.1:9876/proxy\n';
  assert.equal(extractViewerUrl(log), 'http://127.0.0.1:9090?url=rerun%2Bhttp://127.0.0.1:9876/proxy');
  // Nothing yet, or a line that only mentions the bound address.
  assert.equal(extractViewerUrl(''), null);
  assert.equal(extractViewerUrl('Hosting a web-viewer at http://127.0.0.1:9090\n'), null);
});

test('an explicit binary override wins, and a missing one is not silently ignored', () => {
  // rerun is usually installed into a virtualenv that is on nobody's global
  // PATH, which is not something an app can discover by guessing.
  const exists = (p: string): boolean => p === '/venv/bin/rerun' || p === path.join('/usr/bin', 'rerun');
  assert.equal(resolveRerunBinary({ TERMIPOD_RERUN_BIN: '/venv/bin/rerun', PATH: '/usr/bin' }, exists), '/venv/bin/rerun');
  // A set-but-wrong override returns null rather than quietly falling through
  // to PATH: the user pointed somewhere specific and deserves to be told it is
  // not there, not to get a different rerun than they asked for.
  assert.equal(resolveRerunBinary({ TERMIPOD_RERUN_BIN: '/venv/bin/nope', PATH: '/usr/bin' }, exists), null);
  assert.equal(resolveRerunBinary({ TERMIPOD_RERUN_BIN: '   ', PATH: '/usr/bin' }, exists), path.join('/usr/bin', 'rerun'));
  assert.equal(resolveRerunBinary({ PATH: '/nowhere' }, exists), null);
  assert.equal(resolveRerunBinary({}, exists), null);
});
