/// Tests for the git-status porcelain parser (Inspect T4b git lens): branch from
/// the `# branch.head` header, dirty = every non-header line (changed / renamed /
/// unmerged / untracked). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseGitStatus } from './git.ts';

test('parseGitStatus: branch head + dirty count over mixed entries', () => {
  const out = [
    '# branch.oid abc123',
    '# branch.head main',
    '# branch.upstream origin/main',
    '# branch.ab +1 -0',
    '1 .M N... 100644 100644 100644 aaa bbb src/a.ts', // modified
    '2 R. N... 100644 100644 100644 ccc ddd R100 new.ts\told.ts', // renamed
    'u UU N... ... x.ts', // unmerged
    '? untracked.txt', // untracked
    '',
  ].join('\n');
  assert.deepEqual(parseGitStatus(out), { branch: 'main', dirty: 4 });
});

test('parseGitStatus: clean tree = zero dirty', () => {
  const out = '# branch.oid abc\n# branch.head feature/x\n# branch.ab +0 -0\n';
  assert.deepEqual(parseGitStatus(out), { branch: 'feature/x', dirty: 0 });
});

test('parseGitStatus: detached head + empty output', () => {
  assert.equal(parseGitStatus('# branch.oid abc\n# branch.head (detached)\n').branch, '(detached)');
  assert.deepEqual(parseGitStatus(''), { branch: '', dirty: 0 });
});
