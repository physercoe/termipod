import { create } from 'zustand';

/// What the J8 Replay surface is showing, plus the one-shot handoff another
/// surface uses to send it somewhere.
///
/// The selection lives here rather than in `ReplaySurface`'s own `useState`
/// because a cross-surface jump has to survive the surface being unmounted:
/// Inspect sets the target *before* the shell switches jobs, so by the time
/// ReplaySurface mounts, its local state would already have been discarded.
/// This is the `useInspect.getState().open(...)` → `setJob('debug')` pattern
/// that AssistantSettings already uses to hand a config file to Inspect.

/// A dataset another surface wants opened, consumed exactly once.
///
/// Deliberately NOT a dataset id: the sender knows a filesystem location, not a
/// hub row, and whether that location is registered is a question only the
/// Replay surface (which has the project's library loaded) can answer.
///
/// It carries no host either, and that is not an omission. A dataset's host is
/// a hub `hosts` row; what Inspect has is an SSH connection (breakglass
/// credentials, no hub identity) or a folder on this desktop, which the hub
/// cannot map to a host at all. Sending either as `host_id` would write a
/// dangling foreign key, so the host stays a human choice in the register form.
export interface ReplayHandoff {
  /// The dataset root — the directory containing `meta/`.
  rootPath: string;
}

interface ReplayState {
  /// '' means "no explicit choice yet"; the surface falls back to the first
  /// dataset in the library rather than showing nothing.
  selectedId: string;
  handoff: ReplayHandoff | null;
  select: (id: string) => void;
  /// Ask the Replay surface to open a dataset by location. The caller switches
  /// the job itself — this store owns what Replay shows, not where the shell is.
  openDataset: (h: ReplayHandoff) => void;
  /// Clear the handoff once acted on. Explicit rather than automatic: the
  /// surface can only decide "already registered" vs "offer to register" after
  /// its library query settles, and a handoff cleared before then would be lost.
  clearHandoff: () => void;
}

export const useReplay = create<ReplayState>((set) => ({
  selectedId: '',
  handoff: null,
  select: (selectedId) => set({ selectedId }),
  // A new handoff drops any prior selection: the surface must not flash the
  // previously-open dataset while it works out what the incoming one is.
  openDataset: (handoff) => set({ handoff, selectedId: '' }),
  clearHandoff: () => set({ handoff: null }),
}));
