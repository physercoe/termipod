/// Pure-logic tests for the W4b module reader (plan §4b): JSON extraction from the
/// stdlib-`ast` helper's output, the model parse, the remote-command assembly, and
/// the class-graph build (composition/inheritance edges, local-vs-external). The AST
/// extraction itself runs on any python3 (stdlib) — covered end-to-end by
/// `electron/src/ipc/trace.test.ts`'s generic `trace_run`. Run:
/// `node --test src/state/moduleAstCore.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  MODULE_AST_HELPER,
  buildModuleGraph,
  extractModuleJson,
  parseModuleAst,
  remoteModuleAstCommand,
  type ModuleModel,
} from './moduleAstCore.ts';

const OUT = [
  'UserWarning: something',
  '===AST-START===',
  JSON.stringify({
    classes: [
      { name: 'MLP', bases: ['nn.Module'], lineno: 3, endLineno: 7, submodules: [{ attr: 'fc1', classes: ['nn.Linear'], lineno: 6 }] },
      {
        name: 'Block',
        bases: ['nn.Module'],
        lineno: 9,
        endLineno: 13,
        submodules: [
          { attr: 'attn', classes: ['Attention'], lineno: 11 },
          { attr: 'mlp', classes: ['MLP'], lineno: 12 },
        ],
      },
      { name: 'Model', bases: ['nn.Module'], lineno: 15, endLineno: 19, submodules: [{ attr: 'blocks', classes: ['nn.ModuleList', 'Block'], lineno: 17 }] },
    ],
  }),
  '===AST-END===',
  'done.',
].join('\n');

test('MODULE_AST_HELPER is ASCII (btoa-safe) and self-delimiting, stdlib only', () => {
  // eslint-disable-next-line no-control-regex
  assert.ok(/^[\x00-\x7F]*$/.test(MODULE_AST_HELPER));
  assert.ok(MODULE_AST_HELPER.includes('===AST-START==='));
  assert.ok(MODULE_AST_HELPER.includes('import os, sys, ast, json'));
  assert.ok(!MODULE_AST_HELPER.includes('import torch'));
});

test('extractModuleJson / parseModuleAst: pull + validate the model out of noise', () => {
  assert.equal(extractModuleJson('no sentinels here'), null);
  const m = parseModuleAst(OUT);
  assert.ok(m);
  assert.equal(m.classes.length, 3);
  assert.equal(m.classes[0].name, 'MLP');
  assert.deepEqual(m.classes[2].submodules[0].classes, ['nn.ModuleList', 'Block']);
});

test('parseModuleAst: malformed / sentinel-less input returns null', () => {
  assert.equal(parseModuleAst('Traceback ...'), null);
  assert.equal(parseModuleAst('===AST-START===\n{bad json}\n===AST-END==='), null);
});

test('remoteModuleAstCommand: base64 helper + AST_FILE env + interpreter, quoted', () => {
  const cmd = remoteModuleAstCommand('m.py', 'conda run -n rl python', '/repo', 'print(1)');
  assert.match(cmd, /^cd '\/repo' && /);
  assert.ok(cmd.includes("AST_FILE='m.py'"));
  assert.ok(cmd.trimEnd().endsWith('conda run -n rl python'));
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.equal(Buffer.from(b64, 'base64').toString('utf8'), 'print(1)');
});

test('buildModuleGraph: composition edges only to LOCAL classes; externals stay metadata', () => {
  const model = parseModuleAst(OUT) as ModuleModel;
  const g = buildModuleGraph(model);
  assert.equal(g.nodes.length, 3);
  // Block composes Attention (absent → not local) + MLP (local) → one composition edge.
  const blockEdges = g.edges.filter((e) => e.source === 'Block' && e.kind === 'composition');
  assert.deepEqual(blockEdges.map((e) => e.target).sort(), ['MLP']);
  // Model → Block via the ModuleList element class.
  assert.ok(g.edges.some((e) => e.source === 'Model' && e.target === 'Block' && e.kind === 'composition'));
  // MLP's fc1 (nn.Linear) is external → a submodule row, not an edge, flagged non-local.
  const mlp = g.nodes.find((n) => n.id === 'MLP')!;
  assert.equal(mlp.submodules[0].type, 'nn.Linear');
  assert.equal(mlp.submodules[0].local, false);
});

test('buildModuleGraph: local inheritance draws an edge; external bases are dropped', () => {
  const model: ModuleModel = {
    classes: [
      { name: 'Base', bases: ['nn.Module'], lineno: 1, endLineno: 2, submodules: [] },
      { name: 'Child', bases: ['Base'], lineno: 3, endLineno: 4, submodules: [] },
    ],
  };
  const g = buildModuleGraph(model);
  assert.deepEqual(g.edges, [{ source: 'Child', target: 'Base', kind: 'inheritance' }]);
  assert.deepEqual(g.nodes.find((n) => n.id === 'Child')!.bases, ['Base']);
  assert.deepEqual(g.nodes.find((n) => n.id === 'Base')!.bases, []); // nn.Module dropped
});

test('buildModuleGraph: a self-referential composition does not make a self-edge', () => {
  const model: ModuleModel = {
    classes: [{ name: 'Rec', bases: [], lineno: 1, endLineno: 2, submodules: [{ attr: 'inner', classes: ['Rec'], lineno: 2 }] }],
  };
  assert.deepEqual(buildModuleGraph(model).edges, []);
});

// End-to-end against the real python3 (stdlib `ast` — no torch), mirroring how the
// app runs the helper over `trace_run`. Local-only (the renderer pkg has no CI test
// runner); skipped when python3 is absent.
test('MODULE_AST_HELPER end-to-end: python3 → parse → graph', () => {
  const py = spawnSync('python3', ['--version']);
  if (py.status !== 0) return; // no python3 here
  const dir = mkdtempSync(path.join(tmpdir(), 'modast-'));
  const file = path.join(dir, 'modeling_x.py');
  writeFileSync(
    file,
    [
      'import torch.nn as nn',
      'class MLP(nn.Module):',
      '    def __init__(self, d):',
      '        super().__init__()',
      '        self.fc = nn.Linear(d, d)',
      'class Block(nn.Module):',
      '    def __init__(self, d):',
      '        super().__init__()',
      '        self.mlp = MLP(d)',
      'class Model(nn.Module):',
      '    def __init__(self, d, n):',
      '        super().__init__()',
      '        self.blocks = nn.ModuleList([Block(d) for _ in range(n)])',
      '',
    ].join('\n'),
  );
  try {
    const r = spawnSync('python3', ['-'], { input: MODULE_AST_HELPER, env: { ...process.env, AST_FILE: file }, encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    const model = parseModuleAst(r.stdout);
    assert.ok(model, 'helper output parsed');
    assert.deepEqual(model.classes.map((c) => c.name).sort(), ['Block', 'MLP', 'Model']);
    const g = buildModuleGraph(model);
    // Model → Block (via ModuleList element) and Block → MLP compose; MLP.fc is external.
    assert.ok(g.edges.some((e) => e.source === 'Model' && e.target === 'Block' && e.kind === 'composition'));
    assert.ok(g.edges.some((e) => e.source === 'Block' && e.target === 'MLP' && e.kind === 'composition'));
    const mlp = g.nodes.find((n) => n.id === 'MLP')!;
    assert.equal(mlp.submodules[0].local, false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
