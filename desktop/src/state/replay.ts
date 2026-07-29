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

/// A dataset another surface wants opened **by hub id** (W5).
///
/// The counterpart to `ReplayHandoff`, and deliberately a different type. That
/// one carries a location because Inspect only knows a location; this one comes
/// from a run row that already holds `dataset_id`, so there is nothing to
/// resolve and no register form to fall back to.
///
/// The project travels with it because the Replay surface defaults to the FIRST
/// project in the list: without it the surface would load some other project's
/// library, never find the dataset, and silently show the wrong one.
export interface ReplayTarget {
  datasetId: string;
  projectId: string;
  /// Episode index to open in the player. The surface pages the episodes
  /// table, so it has to seek to the page holding this index first.
  episode?: number;
}

interface ReplayState {
  /// '' means "no explicit choice yet"; the surface falls back to the first
  /// dataset in the library rather than showing nothing.
  selectedId: string;
  handoff: ReplayHandoff | null;
  target: ReplayTarget | null;
  select: (id: string) => void;
  /// Ask the Replay surface to open a dataset by location. The caller switches
  /// the job itself — this store owns what Replay shows, not where the shell is.
  openDataset: (h: ReplayHandoff) => void;
  /// Clear the handoff once acted on. Explicit rather than automatic: the
  /// surface can only decide "already registered" vs "offer to register" after
  /// its library query settles, and a handoff cleared before then would be lost.
  clearHandoff: () => void;
  /// Ask Replay to open an already-registered dataset, optionally at an
  /// episode. Used by RunDetail's Episodes view.
  openRegistered: (t: ReplayTarget) => void;
  clearTarget: () => void;
}

export const useReplay = create<ReplayState>((set) => ({
  selectedId: '',
  handoff: null,
  target: null,
  select: (selectedId) => set({ selectedId }),
  // A new handoff drops any prior selection: the surface must not flash the
  // previously-open dataset while it works out what the incoming one is.
  openDataset: (handoff) => set({ handoff, selectedId: '' }),
  clearHandoff: () => set({ handoff: null }),
  // The two are mutually exclusive: a target names a row outright, so any
  // pending location handoff is stale the moment one arrives.
  openRegistered: (target) => set({ target, handoff: null }),
  clearTarget: () => set({ target: null }),
}));

/// The page offset that contains an episode index.
///
/// The episodes table is windowed, and the player renders from the CURRENT
/// page's rows. Landing on episode 1 400 with the table still at offset 0 shows
/// an empty player over a table that does not contain the episode — the jump
/// looks broken rather than out of range.
export function pageOffsetFor(episode: number, pageSize: number): number {
  if (!Number.isFinite(episode) || episode <= 0 || pageSize <= 0) return 0;
  return Math.floor(episode / pageSize) * pageSize;
}
