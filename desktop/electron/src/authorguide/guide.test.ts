/// `author_guide`'s lookup (coworking C2 + C3). Run with `node --test`.
///
/// Two kinds of test here, and the second is the one worth having:
///
///   - **behaviour** — an index lists what resolves, a refusal names what
///     would have worked, a filter narrows without losing the sentence that
///     says how to use what it returned;
///   - **drift pins** — the guide describes code that lives in another package
///     (`desktop/src/state/`), which nothing in the type system connects it to.
///     A seventh figure renderer, or a seventh document kind, would ship with
///     no guide entry and the only symptom would be an agent that cannot find
///     out the thing exists. Those files are read as TEXT rather than imported
///     because they are renderer modules — importing them here would drag a DOM
///     and the vendor libraries into a `node --test` run.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { GUIDE_KINDS, guideAnswer, topicNames } from './guide.ts';
import { KIND_GUIDES } from './guides.ts';
import { SHAPE_LIBRARIES } from './shapes.generated.ts';

/// `desktop/` — three up from `electron/src/authorguide/`.
const DESKTOP = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');
const readState = (f: string): string => readFileSync(join(DESKTOP, 'src', 'state', f), 'utf8');

function text(kind: string, topic: string | null = null, filter: string | null = null): string {
  const a = guideAnswer(kind, topic, filter);
  assert.ok(a.ok, `expected ok for ${kind}/${String(topic)}: ${a.ok ? '' : a.message}`);
  return a.text;
}

// ── behaviour ────────────────────────────────────────────────────────────────

test('every kind has an index, and the index lists what actually resolves', () => {
  for (const kind of GUIDE_KINDS) {
    const idx = text(kind);
    assert.ok(idx.length > 100, `${kind} index is too thin to be useful`);
    for (const topic of topicNames(kind)) {
      // A library is named in the `shapes` sub-index rather than the kind
      // index; everything else must be visible from the front door.
      const inIndex = idx.includes(`\`${topic}\``);
      const isLibrary = SHAPE_LIBRARIES.some((l) => l.name === topic);
      assert.ok(inIndex || isLibrary, `${kind} index does not mention topic '${topic}'`);
    }
  }
});

test('every advertised topic resolves to a body', () => {
  for (const kind of GUIDE_KINDS) {
    for (const topic of topicNames(kind)) {
      const body = text(kind, topic);
      assert.ok(body.length > 40, `${kind}/${topic} is empty`);
    }
  }
});

test('an unknown kind names the kinds that exist', () => {
  const a = guideAnswer('spreadsheet', null, null);
  assert.ok(!a.ok);
  assert.equal(a.code, 'UNKNOWN_KIND');
  for (const kind of GUIDE_KINDS) assert.ok(a.message.includes(kind), `refusal omits '${kind}'`);
});

test('an unknown topic names the topics that exist', () => {
  const a = guideAnswer('canvas', 'nodes', null);
  assert.ok(!a.ok);
  assert.equal(a.code, 'UNKNOWN_TOPIC');
  assert.ok(a.message.includes('schema'));
});

test('a library draw.io has but we do not is refused with the reason, not a blank miss', () => {
  // Upstream's index names `arista`/`digitalocean`/`eip` and ships no file. An
  // agent that read the upstream README elsewhere will ask for one of them.
  const a = guideAnswer('diagram', 'digitalocean', null);
  assert.ok(!a.ok);
  assert.equal(a.code, 'UNKNOWN_TOPIC');
  assert.match(a.message, /no name list/);
  assert.match(a.message, /'shapes'/);
});

test('the shapes index covers every library and warns where a list is partial', () => {
  const idx = text('diagram', 'shapes');
  for (const lib of SHAPE_LIBRARIES) assert.ok(idx.includes(`\`${lib.name}\``), `shapes index omits ${lib.name}`);
  // The partial ones are stated as "N of M", so the index itself carries the
  // caveat rather than making an agent open the library to discover it.
  const azure = SHAPE_LIBRARIES.find((l) => l.name === 'azure2');
  assert.ok(azure !== undefined && azure.claimed !== null);
  assert.ok(idx.includes(`${String(azure.listed)} of ${String(azure.claimed)} names`));
});

test('filter narrows a library and keeps the line that says how to use a name', () => {
  const out = text('diagram', 'aws4', 'lambda');
  assert.match(out, /names containing "lambda"/);
  assert.ok(out.includes('`lambda`'));
  // The Usage block travels with the matches: names without the style form
  // they compose into are not actionable.
  assert.match(out, /shape=mxgraph\.aws4/);
  // And it is genuinely smaller than the whole library, which is the point.
  const whole = text('diagram', 'aws4');
  assert.ok(out.length * 8 < whole.length, `filtered ${String(out.length)} vs whole ${String(whole.length)}`);
});

test('a filtered category library keeps the category, because the path contains it', () => {
  // `azure2/{category}/{shape}.svg` — a flat list of matching names would be
  // unusable for exactly the libraries where filtering matters most.
  const out = text('diagram', 'azure2', 'machine_learning');
  assert.match(out, /^## /m);
  assert.match(out, /img\/lib\/azure2/);
});

test('a filter that matches nothing says so rather than returning an empty list', () => {
  const a = guideAnswer('diagram', 'aws4', 'zzzznotashape');
  assert.ok(!a.ok);
  assert.equal(a.code, 'NO_MATCH');
  assert.match(a.message, /drop filter/);
});

test('filter is refused where it means nothing, rather than silently ignored', () => {
  // An agent that believes it filtered and got everything back learns the
  // wrong lesson about both the argument and the result.
  for (const [kind, topic] of [
    ['diagram', 'xml'],
    ['canvas', 'schema'],
    ['figure', 'mermaid'],
  ] as const) {
    const a = guideAnswer(kind, topic, 'x');
    assert.ok(!a.ok, `${kind}/${topic} accepted a meaningless filter`);
    assert.equal(a.code, 'INVALID_PARAMS');
  }
  const onIndex = guideAnswer('diagram', null, 'x');
  assert.ok(!onIndex.ok);
  const onShapes = guideAnswer('diagram', 'shapes', 'x');
  assert.ok(!onShapes.ok);
});

// ── drift pins ───────────────────────────────────────────────────────────────

test('every document kind has a guide', () => {
  // `DocKind` is the set of kinds an agent can be asked to write. One without
  // a guide is a kind whose format is undiscoverable.
  const src = readState('documents.ts');
  const decl = /export type DocKind =([^;]+);/.exec(src);
  assert.ok(decl !== null, 'DocKind declaration moved — this pin needs updating');
  const kinds = [...decl[1].matchAll(/'([a-z]+)'/g)].map((m) => m[1]).sort();
  assert.deepStrictEqual([...GUIDE_KINDS].sort(), kinds);
});

test('every figure renderer has a topic, with its real extension and fence', () => {
  // The registry is a list of rows in the renderer; the guide is the only way
  // an agent learns a spec exists. A seventh renderer must land in both.
  const src = readState('figures.ts');
  const registry = /export const FIGURES: FigureRenderer\[\] = \[([\s\S]*?)\n\];/.exec(src);
  assert.ok(registry !== null, 'the FIGURES registry moved — this pin needs updating');
  const rows = [...registry[1].matchAll(/spec: '([^']+)',[\s\S]*?ext: '([^']+)',([\s\S]*?)fence: \[([^\]]+)\]/g)];
  assert.ok(rows.length >= 6, `parsed only ${String(rows.length)} renderer rows`);

  const figure = KIND_GUIDES.find((g) => g.kind === 'figure');
  assert.ok(figure !== undefined);
  assert.deepStrictEqual(
    figure.topics.map((t) => t.name).sort(),
    rows.map((r) => r[1]).sort(),
  );
  for (const [, spec, ext, mid, fences] of rows) {
    const body = text('figure', spec);
    assert.ok(body.includes(`\`.${ext}\``), `${spec} guide omits its extension .${ext}`);
    for (const f of [...fences.matchAll(/'([^']+)'/g)].map((m) => m[1])) {
      assert.ok(body.includes(f), `${spec} guide omits fence language '${f}'`);
    }
    for (const e of [...mid.matchAll(/openExts: \[([^\]]+)\]/g)].flatMap((m) => [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1]))) {
      assert.ok(body.includes(`\`.${e}\``), `${spec} guide omits open extension .${e}`);
    }
  }
});

test('the canvas guide names the real node, edge and colour vocabularies', () => {
  const src = readState('canvas.ts');
  const body = text('canvas', 'schema');
  const nodeTypes = [...(/export type NodeType =([^;]+);/.exec(src)?.[1] ?? '').matchAll(/'([a-z]+)'/g)].map((m) => m[1]);
  for (const t of nodeTypes) {
    // `unknown` is what the parser calls a node it could not classify — an
    // internal state, never something to write.
    if (t === 'unknown') continue;
    assert.ok(body.includes(`\`${t}\``), `canvas guide omits node type '${t}'`);
  }
  const sides = [...(/export type Side =([^;]+);/.exec(src)?.[1] ?? '').matchAll(/'([a-z]+)'/g)].map((m) => m[1]);
  for (const s of sides) assert.ok(body.includes(`\`${s}\``), `canvas guide omits side '${s}'`);
  const edgeTypes = [...(/export type EdgeType =([^;]+);/.exec(src)?.[1] ?? '').matchAll(/'([a-z]+)'/g)].map((m) => m[1]);
  for (const e of edgeTypes) assert.ok(body.includes(`\`${e}\``), `canvas guide omits x-termipod edge type '${e}'`);
});

test('the table guide names the real column types', () => {
  const src = readState('table.ts');
  const body = text('table', 'schema');
  const cols = [...(/export type ColType =([^;]+);/.exec(src)?.[1] ?? '').matchAll(/'([a-z]+)'/g)].map((m) => m[1]);
  assert.ok(cols.length >= 5);
  for (const c of cols) assert.ok(body.includes(`\`${c}\``), `table guide omits column type '${c}'`);
});

test('the excalidraw guide states the discriminator the parser actually uses', () => {
  // `type: 'excalidraw'` plus an elements array. A guide that described a
  // looser rule would have agents writing bodies we refuse.
  const src = readState('excalidrawScene.ts');
  assert.match(src, /parsed\.type !== 'excalidraw' \|\| !Array\.isArray\(parsed\.elements\)/);
  const body = text('excalidraw', 'schema');
  assert.match(body, /"type": "excalidraw"/);
  assert.match(body, /elements/);
});
