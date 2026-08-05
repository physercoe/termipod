/// The renderer CSP. Run with `node --test`.
///
/// These exist because the media scheme shipped registered, handled, wired to a
/// `<video>` — and absent from the CSP, so nothing it served could ever load.
/// Nothing failed: not a build, not a test, not a lint. The element simply drew
/// nothing. That is the failure mode this file is here to make loud.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildCsp,
  cspAllows,
  cspDirectives,
  CSP_DRAWIO_SCHEME,
  CSP_MEDIA_SCHEME,
  MEDIA_DIRECTIVES,
} from './csp.ts';

test('every scheme the app serves is reachable under the CSP', () => {
  const csp = buildCsp();
  // The invariant itself, stated once: a privileged scheme with a handler is
  // useless if the document may not load it. Adding a scheme to schemes.ts and
  // forgetting this list is the exact bug this pins.
  for (const directive of MEDIA_DIRECTIVES) {
    assert.equal(
      cspAllows(csp, directive, `${CSP_MEDIA_SCHEME}:`),
      true,
      `${directive} must admit ${CSP_MEDIA_SCHEME}: — a <video>/<img>/<iframe> against it is refused before the handler runs`,
    );
  }
  assert.equal(cspAllows(csp, 'frame-src', `${CSP_DRAWIO_SCHEME}:`), true);
});

test('the duplicated scheme names match their authorities', async () => {
  // `csp.ts` cannot import `schemes.ts` (it registers privileges at module load
  // and needs electron), so it restates the names. A rename there with no
  // rename here produces a CSP that lists a scheme nobody serves and omits the
  // one that exists — silently, since neither side would fail to compile.
  const { MEDIA_SCHEME } = await import('./media_policy.ts');
  assert.equal(CSP_MEDIA_SCHEME, MEDIA_SCHEME);
  // schemes.ts imports electron, so its literal is checked by reading the file
  // rather than importing it.
  const fs = await import('node:fs/promises');
  const url = await import('node:url');
  const path = await import('node:path');
  const here = path.dirname(url.fileURLToPath(import.meta.url));
  const src = await fs.readFile(path.join(here, 'schemes.ts'), 'utf8');
  assert.match(src, new RegExp(`DRAWIO_SCHEME\\s*=\\s*'${CSP_DRAWIO_SCHEME}'`));
});

test('the media scheme gets no reach it does not need', () => {
  const csp = buildCsp();
  // It serves decodable bytes. It must never be a script origin or a fetch()
  // target: those would turn "the renderer named a path" into "a file the user
  // pointed at runs in the app's own secure origin".
  for (const directive of ['script-src', 'connect-src', 'style-src', 'worker-src', 'default-src']) {
    assert.equal(
      cspAllows(csp, directive, `${CSP_MEDIA_SCHEME}:`),
      false,
      `${directive} must NOT admit ${CSP_MEDIA_SCHEME}:`,
    );
  }
});

test('the directive parser does not pass on substrings', () => {
  // The naive check is `csp.includes('termipod-media:')`, which is true as soon
  // as ANY directive lists it — so a policy that allowed it only for images
  // would still "pass" a media-src assertion. Parse, then look in the right
  // bucket.
  const partial = "default-src 'self'; img-src 'self' termipod-media:; media-src 'self'";
  assert.equal(partial.includes('termipod-media:'), true); // the bad check
  assert.equal(cspAllows(partial, 'img-src', 'termipod-media:'), true);
  assert.equal(cspAllows(partial, 'media-src', 'termipod-media:'), false); // the good one
});

test('an absent fetch directive falls back to default-src, as a browser does', () => {
  const csp = "default-src 'self' blob:; script-src 'self'";
  assert.equal(cspAllows(csp, 'img-src', 'blob:'), true); // inherited
  assert.equal(cspAllows(csp, 'script-src', 'blob:'), false); // overridden, narrower
});

test('the policy keeps the directives the app already depends on', () => {
  const d = cspDirectives(buildCsp());
  // Guarding against a careless edit to a shared string: each of these is load
  // bearing for a shipped feature and none is obvious from reading the line.
  assert.ok(d['script-src'].includes("'wasm-unsafe-eval'"), 'graphviz wasm');
  assert.ok(d['script-src'].includes("'unsafe-eval'"), 'vega expression compiler');
  assert.ok(d['connect-src'].includes('https:') && d['connect-src'].includes('wss:'), 'hub transport + SSE');
  assert.deepEqual(d['object-src'], ["'none'"]);
  assert.deepEqual(d['base-uri'], ["'self'"]);
});
