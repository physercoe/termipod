/// Tests for the webview partition allowlist + per-partition navigation policy
/// (agent-transcript-redesign P0). The matrix pinned here is enforced main-side
/// by webtab.ts at three layers: `will-attach-webview` (allowlist),
/// `onBeforeRequest` + `will-navigate` (top-frame nav), `setWindowOpenHandler`
/// (popups). Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  KIMIWEB_PARTITION,
  WEBTAB_PARTITION,
  isLoopbackHttpUrl,
  partitionPolicy,
} from './webtab_policy.ts';

test('allowlist: webtab + kimiweb are allowed, everything else is rejected', () => {
  assert.ok(partitionPolicy(WEBTAB_PARTITION) !== null);
  assert.ok(partitionPolicy(KIMIWEB_PARTITION) !== null);
  // …including the default session (where the app:// scheme handlers and the
  // hub-CORS bearer injection live) and any other persistent partition.
  assert.equal(partitionPolicy(''), null);
  assert.equal(partitionPolicy('default'), null);
  assert.equal(partitionPolicy('persist:evil'), null);
  assert.equal(partitionPolicy('webtab'), null); // missing the persist: prefix
});

test('webtab policy: any http(s) top frame, popups may stay in-tab', () => {
  const p = partitionPolicy(WEBTAB_PARTITION)!;
  assert.ok(p.allowTopFrame('https://arxiv.org/abs/2401.00001'));
  assert.ok(p.allowTopFrame('http://example.com/'));
  assert.equal(p.allowTopFrame('file:///etc/passwd'), false);
  assert.equal(p.allowTopFrame('app://termipod/index.html'), false);
  assert.equal(p.allowTopFrame('ftp://example.com/x'), false);
  assert.equal(p.windowOpen, 'inline');
});

test('kimiweb policy: loopback http(s) only, any port', () => {
  const p = partitionPolicy(KIMIWEB_PARTITION)!;
  // The embed URL itself — token in the hash — must pass.
  assert.ok(p.allowTopFrame('http://127.0.0.1:17331/#token=9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk'));
  assert.ok(p.allowTopFrame('http://127.0.0.1:1/'));
  assert.ok(p.allowTopFrame('http://127.0.0.1:65535/'));
  assert.ok(p.allowTopFrame('http://localhost:3000/chat'));
  assert.ok(p.allowTopFrame('http://[::1]:8080/'));
  assert.ok(p.allowTopFrame('https://127.0.0.1/'));
});

test('kimiweb policy: external and look-alike origins are blocked', () => {
  const p = partitionPolicy(KIMIWEB_PARTITION)!;
  assert.equal(p.allowTopFrame('https://example.com/'), false);
  assert.equal(p.allowTopFrame('http://moonshot.cn/'), false);
  // String-prefix look-alikes are NOT loopback — hostname comparison only.
  assert.equal(p.allowTopFrame('http://127.0.0.1.evil.com/'), false);
  assert.equal(p.allowTopFrame('http://localhost.evil.com/'), false);
  // Cloud metadata + unspecified-address bypasses are NOT loopback.
  assert.equal(p.allowTopFrame('http://169.254.169.254/latest/meta-data'), false);
  assert.equal(p.allowTopFrame('http://0.0.0.0:8080/'), false);
  // Scheme escapes.
  assert.equal(p.allowTopFrame('file:///etc/passwd'), false);
  assert.equal(p.allowTopFrame('app://termipod/index.html'), false);
  assert.equal(p.allowTopFrame('not a url'), false);
  // Popups never load in-tab (safe schemes go to the OS browser instead).
  assert.equal(p.windowOpen, 'external');
});

test('isLoopbackHttpUrl: direct predicate spot-checks', () => {
  assert.ok(isLoopbackHttpUrl('http://127.0.0.1/'));
  assert.ok(isLoopbackHttpUrl('http://[::1]:9/'));
  assert.equal(isLoopbackHttpUrl('http://[::ffff:127.0.0.1]/'), false);
  assert.equal(isLoopbackHttpUrl('https://192.168.1.10/'), false);
  assert.equal(isLoopbackHttpUrl('ws://127.0.0.1/'), false);
});
