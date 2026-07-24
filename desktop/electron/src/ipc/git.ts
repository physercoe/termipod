/// Read-only git lens for the Inspect local roots (round-3 T4b). Two commands
/// over the **system** `git` (no bundled git, no libgit2): `git_info` reports the
/// branch + working-tree dirty count for a root, and `git_diff` returns the
/// working-tree diff to open in the existing patch viewer. Strictly read-only —
/// no staging, commit, checkout, or log walking (the Inspect §0 posture). The
/// feature is hidden in the UI when git is absent (`gitMissing`).
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { Handler } from './dispatch';

const pexecFile = promisify(execFile);
const INFO_MAX = 4 * 1024 * 1024;
const DIFF_MAX = 24 * 1024 * 1024; // a big working-tree diff still fits; over → typed error

export interface GitInfo {
  isRepo: boolean;
  branch: string;
  dirty: number;
  /// True when the `git` binary itself is missing — the UI hides the lens.
  gitMissing?: boolean;
}

/// Parse `git status --porcelain=v2 --branch` output: the branch from the
/// `# branch.head` header, and the dirty count = every non-header line (changed,
/// renamed, unmerged, or untracked entry). Pure, so it's unit-tested.
export function parseGitStatus(stdout: string): { branch: string; dirty: number } {
  let branch = '';
  let dirty = 0;
  for (const line of stdout.split('\n')) {
    if (line === '') continue;
    if (line.startsWith('# branch.head ')) branch = line.slice('# branch.head '.length).trim();
    else if (!line.startsWith('#')) dirty += 1;
  }
  return { branch, dirty };
}

function isEnoent(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { code?: unknown }).code === 'ENOENT';
}

export const gitHandlers: Record<string, Handler> = {
  git_info: async (args): Promise<GitInfo> => {
    const cwd = String(args.path ?? '');
    try {
      const { stdout } = await pexecFile('git', ['-C', cwd, 'status', '--porcelain=v2', '--branch'], { maxBuffer: INFO_MAX, windowsHide: true });
      const { branch, dirty } = parseGitStatus(stdout);
      return { isRepo: true, branch, dirty };
    } catch (e) {
      // ENOENT → git isn't installed; any other failure → not a git repo (git
      // exits 128 outside a work tree). Never throws: the lens degrades to hidden.
      if (isEnoent(e)) return { isRepo: false, branch: '', dirty: 0, gitMissing: true };
      return { isRepo: false, branch: '', dirty: 0 };
    }
  },

  git_diff: async (args): Promise<{ diff: string }> => {
    const cwd = String(args.path ?? '');
    try {
      const { stdout } = await pexecFile('git', ['-C', cwd, 'diff'], { maxBuffer: DIFF_MAX, windowsHide: true });
      return { diff: stdout };
    } catch (e) {
      if (isEnoent(e)) throw new Error('git is not installed');
      const code = (e as { code?: unknown }).code;
      if (code === 'ERR_CHILD_PROCESS_STDIO_MAXBUFFER') throw new Error('working-tree diff is too large to open');
      const stderr = (e as { stderr?: unknown }).stderr;
      throw new Error(typeof stderr === 'string' && stderr !== '' ? stderr : 'git diff failed');
    }
  },
};
