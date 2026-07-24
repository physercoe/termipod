/// Pure-helper tests for the Inspect roots store (plan §3): the path-boundary
/// containment check and the innermost-root selection that feeds the stack-trace
/// resolver and the W4 trace form's repo-root default. Frontend has no CI runner —
/// run locally: `node --test src/state/inspectRoots.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { containsPath, innermostLocalRoot, type InspectRoot } from './inspectRoots.ts';

test('containsPath: whole-segment containment, not a raw prefix', () => {
  assert.equal(containsPath('/a/proj', '/a/proj'), true); // the root itself
  assert.equal(containsPath('/a/proj', '/a/proj/src/main.py'), true);
  assert.equal(containsPath('/a/proj', '/a/proj2/x'), false); // sibling with a shared prefix
  assert.equal(containsPath('/a/proj/', '/a/proj/src'), true); // trailing slash on root
  assert.equal(containsPath('/a/proj', '/b/other'), false);
  assert.equal(containsPath('C:\\repo', 'C:\\repo\\src\\a.ts'), true); // backslash sep
});

function local(path: string): InspectRoot {
  return { id: path, source: 'local', label: path, path };
}

test('innermostLocalRoot: picks the deepest containing local root', () => {
  const roots = [local('/a'), local('/a/proj'), local('/other')];
  assert.equal(innermostLocalRoot(roots, '/a/proj/src/main.py'), '/a/proj');
  assert.equal(innermostLocalRoot(roots, '/a/lib/x.py'), '/a');
  assert.equal(innermostLocalRoot(roots, '/nowhere/x.py'), undefined);
});

test('innermostLocalRoot: ignores non-local and path-less roots', () => {
  const roots: InspectRoot[] = [
    { id: 'r1', source: 'remote', label: 'host', path: '/a/proj', hostId: 'h1' },
    { id: 'r2', source: 'hub', label: 'proj', projectId: 'p1' },
    local('/a'),
  ];
  // The remote root would be "deeper" but is not a local checkout — skip it.
  assert.equal(innermostLocalRoot(roots, '/a/proj/x.py'), '/a');
});
