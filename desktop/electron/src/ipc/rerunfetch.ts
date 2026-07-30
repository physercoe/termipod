/// `rerun_fetch` (J8 Replay W4b-2) — pull a remote host's exported `.rrd` down
/// to this machine over the director's own live SSH session.
///
/// The three things this file is, and nothing else: the SFTP channel
/// (`openSftpFile`), the cache root (`app.getPath('userData')`), and the
/// progress event. Everything that can be decided without electron or a
/// session — the digest gate, the streaming, the verification, the eviction —
/// is in `rerun_cache.ts` and is tested there.
///
/// **Zero bytes cross the hub.** ADR-058 §4 leaves the transport to this wedge
/// and names two: teleport's chunked manifest through the blob store, or a
/// channel over the SSH primitives. The session wins because the bytes never
/// touch hub disk at all — a `.rrd` is tens of megabytes and an episodes table
/// is *browsed*, so the blob route would push hundreds of megabytes of derived
/// bytes through the hub for a file that can be re-exported at will.
///
/// Progress reuses the existing `sftp-progress` event rather than minting one:
/// the renderer already has `onSftpProgress(transferId)`, and this is an SFTP
/// transfer by any reading.
import { app } from 'electron';
import path from 'node:path';

import { emit } from '../events';
import { fetchRecording } from '../rerun_cache';
import { isRecordingPath } from '../rerun_policy';
import { openSftpFile } from './ssh';
import type { Handler } from './dispatch';

/// Where fetched recordings live. Under `userData` beside the app's other
/// caches (drawio, storage, migration) — not the OS temp dir, which is swept
/// out from under a running viewer on some platforms.
export function recordingCacheRoot(): string {
  return path.join(app.getPath('userData'), 'rerun-recordings');
}

export const rerunFetchHandlers: Record<string, Handler> = {
  /// Fetch `path` from the host behind ssh session `sessionId`, verify it
  /// against `sha256`, and return the local path the Rerun manager can open.
  ///
  /// The digest is required, not optional. It is what names the local file and
  /// what proves the bytes are the ones the host exported; a host-runner too old
  /// to report one is refused here rather than trusted.
  rerun_fetch: async (args, ctx): Promise<{ path: string; bytes: number; cached: boolean }> => {
    const sessionId = String(args.sessionId ?? '');
    const remotePath = String(args.path ?? '');
    const sha256 = String(args.sha256 ?? '');
    const transferId = String(args.transferId ?? '');

    if (!isRecordingPath(remotePath)) {
      throw new Error(`not a Rerun recording: ${remotePath === '' ? '(empty path)' : remotePath}`);
    }
    const source = await openSftpFile(sessionId, remotePath);
    if (source === null) {
      // Both halves are the same answer to the director — the session they
      // picked cannot produce this file — so they are one message rather than
      // two guesses at which it was.
      throw new Error(`no live SSH session, or '${remotePath}' is not a file on that host`);
    }
    return await fetchRecording({
      root: recordingCacheRoot(),
      sha256,
      source,
      onProgress: (done) => {
        if (transferId !== '') emit(ctx.sender, 'sftp-progress', { transfer_id: transferId, done });
      },
    });
  },
};
