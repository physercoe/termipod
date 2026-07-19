import { create } from 'zustand';
import { loadSyncBackend, syncWorkspace, type FolderSyncReport, type SyncBackend } from './workspaceSync';
import type { SyncProgress } from './syncProgress';
import { useWorkspace } from './workspace';
import { toast } from './toast';

/// A single background workspace-sync job. The transfer (WebDAV/S3, `foldersync.rs`
/// / `s3.rs`) can take a long time for a large vault, so it runs OFF the modal:
/// `start` kicks the Rust command and this store — not a component's local state —
/// owns the in-flight promise, so closing the sync dialog (or leaving the Author
/// tab) doesn't cancel it or drop the result. Any surface can show the running
/// state (the AuthorNav cloud button spins) and the last report. Serial: a second
/// `start` while one is running is ignored.

interface SyncJobState {
  running: boolean;
  /// The folder being synced (or last synced).
  root: string | null;
  backend: SyncBackend | null;
  /// When the current/last run began (epoch ms) — for a "syncing…" elapsed hint.
  startedAt: number | null;
  /// Live N/M transfer progress while running (null before the first tick).
  progress: SyncProgress | null;
  report: FolderSyncReport | null;
  error: string | null;
  start: (root: string) => void;
  dismiss: () => void;
}

export const useSyncJob = create<SyncJobState>((set, get) => ({
  running: false,
  root: null,
  backend: null,
  startedAt: null,
  progress: null,
  report: null,
  error: null,
  start: (root) => {
    if (get().running) return;
    set({
      running: true,
      root,
      backend: loadSyncBackend(),
      startedAt: Date.now(),
      progress: null,
      report: null,
      error: null,
    });
    void (async () => {
      try {
        const report = await syncWorkspace(root, (p) => set({ progress: p }));
        set({ running: false, progress: null, report, error: null });
        // Pulled files down → refresh the file tree wherever it's mounted.
        if (report.downloaded > 0) useWorkspace.getState().touch();
        toast.success(`Workspace synced — ${report.uploaded} up, ${report.downloaded} down`);
      } catch (e) {
        const err = e instanceof Error ? e.message : String(e);
        set({ running: false, progress: null, error: err });
        toast.error(`Workspace sync failed: ${err}`);
      }
    })();
  },
  dismiss: () => set({ report: null, error: null }),
}));
