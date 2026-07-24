import { invoke } from '../bridge';

/// Renderer bridge to the read-only git lens (Inspect T4b). Desktop-only —
/// there is no system git in the plain-browser build, so callers gate on
/// `isShell()`.

export interface GitInfo {
  isRepo: boolean;
  branch: string;
  dirty: number;
  gitMissing?: boolean;
}

/** Branch + working-tree dirty count for a local folder (never throws server-side). */
export function gitInfo(path: string): Promise<GitInfo> {
  return invoke<GitInfo>('git_info', { path });
}
/** The working-tree diff of a local repo (for the patch viewer). */
export function gitDiff(path: string): Promise<{ diff: string }> {
  return invoke<{ diff: string }>('git_diff', { path });
}
