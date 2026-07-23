/// Checkpoint-inspection command for the Inspect (J3) model viewer (plan §5, W4).
/// Thin IPC wrapper over the pure `checkpoint.ts` parsers — the renderer's
/// `ModelView` calls `checkpoint_inspect {path}` and receives the small JSON
/// summary (never tensor bytes).
import type { Handler } from './dispatch';
import { inspectCheckpoint, type CheckpointInfo } from './checkpoint';

export const checkpointHandlers: Record<string, Handler> = {
  checkpoint_inspect: async (args): Promise<CheckpointInfo> => {
    const p = String(args.path ?? '');
    if (p === '') throw new Error('checkpoint_inspect: empty path');
    return inspectCheckpoint(p);
  },
};
