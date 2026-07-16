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
export type JobId =
  | 'fleet'
  | 'projects'
  | 'read'
  | 'author'
  | 'debug'
  | 'compare'
  | 'record'
  | 'terminal'
  | 'settings';

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
  // Projects (the units of directed work) are their own tab — pulled out of the
  // fleet tree so the fleet stays an ops roster (hosts · agents · attention),
  // mirroring the mobile Projects tab being separate from Me/Hosts.
  { id: 'projects', tag: '', labelKey: 'job.projects', hintKey: 'job.projects.hint' },
  { id: 'read', tag: 'J1', labelKey: 'job.read', hintKey: 'job.read.hint' },
  // Author (J2) now also hosts the spatial **canvas** and **table/database** as
  // document kinds — the standalone J4 Canvas surface was folded in.
  { id: 'author', tag: 'J2', labelKey: 'job.author', hintKey: 'job.author.hint' },
  { id: 'debug', tag: 'J3', labelKey: 'job.debug', hintKey: 'job.debug.hint' },
  { id: 'compare', tag: 'J5', labelKey: 'job.compare', hintKey: 'job.compare.hint' },
  { id: 'record', tag: 'J6', labelKey: 'job.record', hintKey: 'job.record.hint' },
  { id: 'terminal', tag: '', labelKey: 'job.terminal', hintKey: 'job.terminal.hint' },
];

/// Settings is a job too, but pinned to the *bottom* of the activity bar (the VS
/// Code gear idiom) rather than listed with the working jobs — so it lives out of
/// `JOBS`. The ActivityBar renders it separately; the shell still switches on it.
export const SETTINGS_JOB: JobDef = {
  id: 'settings',
  tag: '',
  labelKey: 'job.settings',
  hintKey: 'job.settings.hint',
};

// Every id the shell can restore to on launch — the rail jobs plus the pinned
// settings tab.
const KNOWN_JOBS: JobId[] = [...JOBS.map((j) => j.id), SETTINGS_JOB.id];

const LS_KEY = 'termipod.workbench.job';

function initialJob(): JobId {
  try {
    const v = localStorage.getItem(LS_KEY);
    if (v === 'canvas') return 'author'; // Canvas folded into Author
    if (v !== null && KNOWN_JOBS.includes(v as JobId)) return v as JobId;
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
