/// The vendored shape libraries, and the rules that turn upstream's files into
/// what we serve (coworking C2). Run with `node --test`.
///
/// The first test is the load-bearing one: `shapes/` is byte-exact upstream and
/// every correction lives in the generator, which only works if the checked-in
/// module is what the generator currently produces. Editing a vendored file, or
/// changing a rule and forgetting to regenerate, fails here rather than shipping
/// a guide that disagrees with its own source.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { generate, GENERATED_PATH } from './gen-shapes.ts';
import { SHAPE_LIBRARIES, SHAPE_LIBRARIES_WITHOUT_A_FILE, SHAPE_LIBRARY_SOURCE } from './shapes.generated.ts';

const SHAPES_DIR = join(dirname(fileURLToPath(import.meta.url)), 'shapes');

test('shapes.generated.ts is what the generator currently produces', () => {
  const onDisk = readFileSync(GENERATED_PATH, 'utf8');
  assert.equal(
    onDisk,
    generate(),
    'shapes.generated.ts is stale — run `node src/authorguide/gen-shapes.ts`',
  );
});

test('every vendored file becomes a library, and nothing else does', () => {
  // A file added to `shapes/` and never wired up would be invisible: no topic,
  // no index row, and no error anywhere.
  const files = readdirSync(SHAPES_DIR)
    .filter((f) => f.endsWith('.md') && f !== 'README.md')
    .map((f) => f.slice(0, -3))
    .sort();
  assert.deepStrictEqual(
    SHAPE_LIBRARIES.map((l) => l.name).sort(),
    files,
  );
  assert.equal(files.length, 30);
});

test('the self-referential bullet is gone from every body', () => {
  // Upstream lists each library's own prefix as if it were a shape. Composed
  // into a style that is `shape=mxgraph.aws4.mxgraph.aws4`, which draw.io draws
  // as an empty box — valid XML the apply path cannot catch.
  for (const lib of SHAPE_LIBRARIES) {
    assert.ok(
      !lib.body.split('\n').some((line) => line === `- \`${lib.prefix}\``),
      `${lib.name} still lists its own prefix as a shape`,
    );
  }
});

test('`listed` counts the names the body actually carries', () => {
  // The number an agent is told, and the number it can read, are the same one.
  for (const lib of SHAPE_LIBRARIES) {
    const bullets = lib.body.split('\n').filter((l) => /^- `[^`]+`/.test(l)).length;
    assert.equal(bullets, lib.listed, `${lib.name}: body has ${String(bullets)} names, header claims ${String(lib.listed)}`);
  }
});

test('a partial library says so, and a complete one does not', () => {
  // The sentence that stops an agent concluding a shape does not exist because
  // this list omitted it. Eight libraries enumerate less than draw.io ships.
  const partial = SHAPE_LIBRARIES.filter((l) => l.claimed !== null && l.claimed > l.listed);
  assert.ok(partial.length > 0, 'expected at least one partial library in the corpus');
  for (const lib of partial) {
    assert.match(lib.body, /THIS LIST IS PARTIAL/, `${lib.name} is partial but does not say so`);
  }
  for (const lib of SHAPE_LIBRARIES.filter((l) => !partial.includes(l))) {
    assert.doesNotMatch(lib.body, /THIS LIST IS PARTIAL/, `${lib.name} is complete but warns anyway`);
  }
});

test('every body carries its provenance', () => {
  for (const lib of SHAPE_LIBRARIES) {
    assert.match(lib.body, /Vendored from next-ai-draw-io/);
    assert.ok(lib.body.includes(SHAPE_LIBRARY_SOURCE.commit.slice(0, 7)));
    assert.ok(lib.body.includes(SHAPE_LIBRARY_SOURCE.license));
  }
});

test('the index rows upstream ships no file for are recorded, not invented', () => {
  // Upstream's README links three libraries whose files do not exist. Copying
  // the README verbatim would have sent an agent to three topics that refuse.
  assert.deepStrictEqual([...SHAPE_LIBRARIES_WITHOUT_A_FILE], ['arista', 'digitalocean', 'eip']);
  const present = new Set(SHAPE_LIBRARIES.map((l) => l.name));
  for (const name of SHAPE_LIBRARIES_WITHOUT_A_FILE) assert.ok(!present.has(name));
});

test('material_design is indexed even though upstream never indexed it', () => {
  // The mirror of the above: a file with no README row. Serving upstream's
  // index verbatim would have hidden a 300-icon library completely.
  const md = SHAPE_LIBRARIES.find((l) => l.name === 'material_design');
  assert.ok(md !== undefined);
  assert.notEqual(md.category, '');
  assert.notEqual(md.description, '');
  assert.ok(md.listed > 0);
});

test('every library says what a style pastes', () => {
  // Three delivery mechanisms — an mxgraph prefix, a bundled image path, a
  // remote URL — and an agent that does not know which one this library uses
  // cannot write a working cell.
  for (const lib of SHAPE_LIBRARIES) {
    assert.notEqual(lib.prefix, '', `${lib.name} has no prefix/path/URL`);
    assert.notEqual(lib.description, '', `${lib.name} has no description`);
    assert.notEqual(lib.category, '', `${lib.name} has no category`);
  }
});

test('the vendored files are ASCII and unmodified in shape', () => {
  // The repo is English-only, and these came in over an API. A stray non-ASCII
  // byte here would land in an agent's context and in every lint that scans
  // for one.
  for (const f of readdirSync(SHAPES_DIR).filter((n) => n.endsWith('.md'))) {
    const text = readFileSync(join(SHAPES_DIR, f), 'utf8');
    // eslint-disable-next-line no-control-regex
    assert.ok(!/[^\x00-\x7F]/.test(text), `${f} carries a non-ASCII byte`);
    assert.ok(!text.includes('\0'), `${f} carries a NUL`);
  }
});
