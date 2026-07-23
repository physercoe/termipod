/// Graph-substrate checks (plan §5): the DOT content sniff, and that the wasm
/// Graphviz engine actually renders DOT → SVG. Frontend has no CI runner, so run
/// locally: `node --test src/state/dotGraph.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { looksLikeDot, renderDot } from './dotGraph.ts';

test('looksLikeDot: recognises digraph / graph / strict, past comments', () => {
  assert.ok(looksLikeDot('digraph G { a -> b; }'));
  assert.ok(looksLikeDot('graph { a -- b; }'));
  assert.ok(looksLikeDot('strict digraph { a -> b; }'));
  assert.ok(looksLikeDot('  \n digraph "my model" {\n a\n}'));
  assert.ok(looksLikeDot('// a saved dvc dag\ndigraph {\n stage1 -> stage2\n}'));
});

test('looksLikeDot: rejects code that merely mentions digraph, and non-DOT', () => {
  assert.ok(!looksLikeDot('const digraph = buildGraph();'));
  assert.ok(!looksLikeDot('function graph() { return 1; }')); // `graph(` not `graph {`
  assert.ok(!looksLikeDot('{ "nodes": [] }'));
  assert.ok(!looksLikeDot('INFO step 1 loss=0.5'));
  assert.ok(!looksLikeDot(''));
});

test('renderDot: the wasm engine produces an SVG with the node labels', async () => {
  const svg = await renderDot('digraph G { rankdir=LR; a [label="main"]; b [label="helper"]; a -> b; }');
  assert.match(svg.trim(), /^<(\?xml|svg)/);
  assert.ok(svg.includes('main') && svg.includes('helper'));
  assert.match(svg, /<(path|polygon|ellipse)/); // real layout geometry
});

test('renderDot: a DOT syntax error rejects (surfaced in the error pane)', async () => {
  await assert.rejects(() => renderDot('digraph { a -> ; }'));
});
