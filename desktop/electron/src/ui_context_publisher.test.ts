/// The focus publisher's subscription invariant (coworking lane G).
///
/// `uiContext.ts` states the rule in a comment: "EVERY store `sourcesNow()`
/// reads must be listed [as a `subscribe(tick)`] — one missing subscription is
/// a field that publishes only when some other store happens to move, which is
/// worse than not publishing it at all." Nothing enforced it, and the failure
/// is invisible in every other test: the field assembles correctly, projects
/// correctly, and reaches an agent late or never depending on unrelated UI
/// activity. G4 added the sixth such store (`useCompareWall`), which is a good
/// moment to make the rule executable.
///
/// A source scan rather than an import: `uiContext.ts` pulls in zustand
/// stores that read localStorage at module scope plus the platform bridge, so
/// this suite (electron- and renderer-free, `node --test`) cannot load it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const SOURCE = fileURLToPath(new URL('../../src/state/uiContext.ts', import.meta.url));

/// The body of `sourcesNow()` — scoped deliberately. `getState()` is also
/// called elsewhere in the file (the toggle clears agent highlights), and
/// those stores are not focus sources: demanding a subscription for them would
/// make the test wrong rather than strict.
function sourcesNowBody(src: string): string {
  const start = src.indexOf('function sourcesNow(');
  assert.ok(start > 0, 'sourcesNow() not found — did the publisher move?');
  const end = src.indexOf('\n}', start);
  assert.ok(end > start, 'sourcesNow() has no closing brace at column 0');
  return src.slice(start, end);
}

function matchAll(src: string, re: RegExp): string[] {
  return [...src.matchAll(re)].map((m) => m[1]);
}

test('uiContext: every store the focus assembly reads is subscribed to the publisher', () => {
  const src = readFileSync(SOURCE, 'utf8');
  const read = new Set(matchAll(sourcesNowBody(src), /\b(use[A-Za-z0-9_]+)\.getState\(\)/g));
  const subscribed = new Set(matchAll(src, /\b(use[A-Za-z0-9_]+)\.subscribe\(tick\)/g));

  // Non-vacuity: the publisher reads half a dozen stores. If this scan ever
  // sees almost none, the shape it greps for has changed and the test is
  // silently checking nothing.
  assert.ok(read.size >= 5, `only ${read.size} store reads found in sourcesNow() — the scan is likely stale`);

  const missing = [...read].filter((s) => !subscribed.has(s));
  assert.deepEqual(
    missing,
    [],
    `sourcesNow() reads ${missing.join(', ')} but nothing subscribes them to tick(). ` +
      'Those fields would publish only when an unrelated store moves — add `<store>.subscribe(tick)`.',
  );
});
