import { isShell } from '../platform';
import { invoke, listen } from '../bridge';

/// Live progress for a running sync — N of M files. For the workspace backends
/// (WebDAV/S3 folder) M is the count of files that will actually transfer (skips
/// excluded); for the Zotero backends M is the total keys processed (the per-key
/// decision needs a network fetch, so the transfer count isn't known up front).
export interface SyncProgress {
  done: number;
  total: number;
}

let seq = 0;

/// Run a Tauri sync command that emits `sync:progress` events, forwarding each
/// tick to `onProgress`. A unique id scopes the listener to this run so two
/// concurrent syncs (workspace + library) never cross-talk, and it's torn down
/// when the command settles. With no callback (or off-desktop) it's a plain invoke.
export async function invokeWithProgress<T>(
  cmd: string,
  args: Record<string, unknown>,
  onProgress?: (p: SyncProgress) => void,
): Promise<T> {
  // Always send progressId (null when unwatched) so the Rust `Option<String>` arg
  // is present — mirrors the sibling `proxy: … ?? null` convention.
  if (onProgress === undefined || !isShell()) return invoke<T>(cmd, { ...args, progressId: null });
  seq += 1;
  const id = `${cmd}#${seq}`;
  const un = await listen<{ id: string; done: number; total: number }>('sync:progress', (e) => {
    if (e.payload.id === id) onProgress({ done: e.payload.done, total: e.payload.total });
  });
  try {
    // `progressId` (camelCase) reaches the Rust command as `progress_id`.
    return await invoke<T>(cmd, { ...args, progressId: id });
  } finally {
    un();
  }
}
