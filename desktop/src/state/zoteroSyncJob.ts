import { create } from 'zustand';
import { loadZoteroBackend, syncWebdav, type SyncReport } from './webdav';
import type { SyncProgress } from './syncProgress';
import type { SyncBackend } from './workspaceSync';
import { toast } from './toast';

/// A single background Zotero-attachment-sync job (the Read surface's library
/// storage). The transfer (WebDAV/S3, `webdav.rs` / `s3.rs` `s3_zotero_sync`) can
/// take a long time for a large library, so — like the Author workspace sync
/// ([[syncJob]]) — it runs OFF the modal: `start` kicks `syncWebdav` and THIS
/// store owns the in-flight promise, so closing the sync dialog (or switching
/// tabs) doesn't cancel it or drop the result. Any surface can show the running
/// state; the StatusBar renders a shared spinner across both sync jobs. Serial: a
/// second `start` while one is running is ignored. `syncWebdav` already re-indexes
/// the linked folder on completion, so downloaded files show without extra work.

interface ZoteroSyncJobState {
  running: boolean;
  backend: SyncBackend | null;
  /// When the current/last run began (epoch ms).
  startedAt: number | null;
  /// Live N/M progress while running (null before the first tick).
  progress: SyncProgress | null;
  report: SyncReport | null;
  error: string | null;
  start: () => void;
  dismiss: () => void;
}

export const useZoteroSyncJob = create<ZoteroSyncJobState>((set, get) => ({
  running: false,
  backend: null,
  startedAt: null,
  progress: null,
  report: null,
  error: null,
  start: () => {
    if (get().running) return;
    set({
      running: true,
      backend: loadZoteroBackend(),
      startedAt: Date.now(),
      progress: null,
      report: null,
      error: null,
    });
    void (async () => {
      try {
        const report = await syncWebdav((p) => set({ progress: p }));
        set({ running: false, progress: null, report, error: null });
        toast.success(`Zotero synced — ${report.downloaded} down, ${report.uploaded} up`);
      } catch (e) {
        const err = e instanceof Error ? e.message : String(e);
        set({ running: false, progress: null, error: err });
        toast.error(`Zotero sync failed: ${err}`);
      }
    })();
  },
  dismiss: () => set({ report: null, error: null }),
}));
