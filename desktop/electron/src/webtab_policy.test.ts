/// Tests for the webview partition allowlist + per-partition navigation policy
/// (agent-transcript-redesign P0). The matrix pinned here is enforced main-side
/// by webtab.ts at three layers: `will-attach-webview` (allowlist),
/// `onBeforeRequest` + `will-navigate` (top-frame nav), `setWindowOpenHandler`
/// (popups). Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildGuestMenuTemplate,
  KIMIWEB_PARTITION,
  RERUNWEB_PARTITION,
  WEBTAB_PARTITION,
  isLoopbackHttpUrl,
  partitionPolicy,
  type GuestMenuContext,
} from './webtab_policy.ts';

test('allowlist: webtab + kimiweb + rerunweb are allowed, everything else is rejected', () => {
  assert.ok(partitionPolicy(WEBTAB_PARTITION) !== null);
  assert.ok(partitionPolicy(KIMIWEB_PARTITION) !== null);
  assert.ok(partitionPolicy(RERUNWEB_PARTITION) !== null);
  // …including the default session (where the app:// scheme handlers and the
  // hub-CORS bearer injection live) and any other persistent partition.
  assert.equal(partitionPolicy(''), null);
  assert.equal(partitionPolicy('default'), null);
  assert.equal(partitionPolicy('persist:evil'), null);
  assert.equal(partitionPolicy('webtab'), null); // missing the persist: prefix
});

test('rerunweb policy: a new partition must not relax the existing ones', () => {
  // The plan's partition-discipline anchor, as an assertion. The Rerun viewer
  // is served from the same machine that holds the recording, so it never needs
  // a remote origin — and the whole promise of "another web UI is one registry
  // row" only holds if a new row cannot quietly widen the policy.
  const p = partitionPolicy(RERUNWEB_PARTITION)!;
  const kimi = partitionPolicy(KIMIWEB_PARTITION)!;
  assert.equal(p.windowOpen, kimi.windowOpen);
  assert.equal(p.windowOpen, 'external');
  // The URL the companion actually loads: loopback, our chosen port, the
  // recording named in the query string.
  assert.ok(p.allowTopFrame('http://127.0.0.1:9090?url=rerun%2Bhttp://127.0.0.1:9876/proxy'));
  assert.ok(p.allowTopFrame('http://localhost:9090/'));
  // Everything the kimiweb row refuses, this one refuses identically —
  // including rerun's own hosted viewer, which would ship the recording's URL
  // to a third party.
  for (const url of [
    'https://app.rerun.io/',
    'https://example.com/',
    'http://127.0.0.1.evil.com/',
    'http://169.254.169.254/latest/meta-data',
    'http://0.0.0.0:9090/',
    'file:///etc/passwd',
  ]) {
    assert.equal(p.allowTopFrame(url), false, url);
    assert.equal(kimi.allowTopFrame(url), false, url);
  }
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

// ── Guest context-menu template ──────────────────────────────────────────────
// This is why kimiweb/webtab now HAVE a right-click menu at all: the guest's
// `context-menu` is handled main-side (webtab.ts), building a native menu from
// this descriptor. The blank base = "nothing under the cursor" (which must
// yield NO menu, not an empty one).

const NONE: GuestMenuContext = {
  linkURL: '',
  isImage: false,
  isEditable: false,
  selectionText: '',
  canCut: false,
  canCopy: false,
  canPaste: false,
  canSelectAll: false,
};
const actions = (items: ReturnType<typeof buildGuestMenuTemplate>): string[] =>
  items.map((it) => (it === 'separator' ? '|' : it.action));

test('guest menu: nothing useful under the cursor ⇒ no menu', () => {
  assert.deepEqual(buildGuestMenuTemplate(NONE), []);
});

test('guest menu: a plain text selection offers copy + select-all', () => {
  const items = buildGuestMenuTemplate({ ...NONE, selectionText: 'hello' });
  assert.deepEqual(actions(items), ['copy', '|', 'selectAll']);
});

test('guest menu: an editable field offers the full edit set, enabled per editFlags', () => {
  const items = buildGuestMenuTemplate({
    ...NONE,
    isEditable: true,
    selectionText: 'sel',
    canCopy: true,
    canCut: true,
    canPaste: true,
    canSelectAll: true,
  });
  assert.deepEqual(actions(items), ['cut', 'copy', 'paste', '|', 'selectAll']);
  // editFlags drive enabled state (an empty clipboard ⇒ paste disabled, no
  // selection ⇒ cut/copy disabled) — Chromium's own accounting.
  const empty = buildGuestMenuTemplate({ ...NONE, isEditable: true });
  const byAction = Object.fromEntries(empty.filter((it) => it !== 'separator').map((it) => [it.action, it.enabled]));
  assert.equal(byAction.cut, false);
  assert.equal(byAction.copy, false);
  assert.equal(byAction.paste, false);
  assert.equal(byAction.selectAll, false);
});

test('guest menu: a link leads, then image, then text (browser ordering)', () => {
  const items = buildGuestMenuTemplate({
    ...NONE,
    linkURL: 'https://example.com/',
    isImage: true,
    selectionText: 'sel',
  });
  assert.deepEqual(actions(items), ['openLink', 'copyLink', '|', 'copyImage', '|', 'copy', '|', 'selectAll']);
});

test('guest menu: an image alone offers copy image only', () => {
  assert.deepEqual(actions(buildGuestMenuTemplate({ ...NONE, isImage: true })), ['copyImage']);
});
