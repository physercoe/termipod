import { create } from 'zustand';

/// The desktop workbench's top-level navigation model. The desktop app is no
/// longer a single control-fleet screen (`desktop-research-surface.md` §6): it is
/// a left **activity-bar** of distinct jobs, each a full-height surface in the
/// centre. `fleet` (J7) is the original three-region mission-control; J1–J6 are
/// the research-workbench jobs derived in that discussion and mapped to a
/// build/embed posture by `research-tooling-landscape.md`.
///
/// The registry below is the single source of truth for the rail order, icons,
/// and i18n keys — the ActivityBar renders it and the shell switches on the id,
/// so adding a job is one entry here plus a surface component (never a scattered
/// change).
export type JobId = 'fleet' | 'read' | 'author' | 'debug' | 'canvas' | 'compare' | 'record';

export interface JobDef {
  id: JobId;
  /** J-number from `desktop-research-surface.md` §3 (empty for the fleet home). */
  tag: string;
  /** i18n key for the short rail label. */
  labelKey: string;
  /** i18n key for the tooltip / surface subtitle. */
  hintKey: string;
}

export const JOBS: JobDef[] = [
  { id: 'fleet', tag: '', labelKey: 'job.fleet', hintKey: 'job.fleet.hint' },
  { id: 'read', tag: 'J1', labelKey: 'job.read', hintKey: 'job.read.hint' },
  { id: 'author', tag: 'J2', labelKey: 'job.author', hintKey: 'job.author.hint' },
  { id: 'debug', tag: 'J3', labelKey: 'job.debug', hintKey: 'job.debug.hint' },
  { id: 'canvas', tag: 'J4', labelKey: 'job.canvas', hintKey: 'job.canvas.hint' },
  { id: 'compare', tag: 'J5', labelKey: 'job.compare', hintKey: 'job.compare.hint' },
  { id: 'record', tag: 'J6', labelKey: 'job.record', hintKey: 'job.record.hint' },
];

const LS_KEY = 'termipod.workbench.job';

function initialJob(): JobId {
  try {
    const v = localStorage.getItem(LS_KEY);
    if (v !== null && JOBS.some((j) => j.id === v)) return v as JobId;
  } catch {
    /* ignore */
  }
  return 'fleet';
}

interface WorkbenchState {
  job: JobId;
  setJob: (job: JobId) => void;
}

export const useWorkbench = create<WorkbenchState>((set) => ({
  job: initialJob(),
  setJob: (job) => {
    try {
      localStorage.setItem(LS_KEY, job);
    } catch {
      /* ignore */
    }
    set({ job });
  },
}));
