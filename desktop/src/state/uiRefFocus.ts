/// UIRef → focus dispatch (D6 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4b). The other half of a ref-chip: what happens when the user CLICKS it.
///
/// The agent directs attention; **the click is the user's** (ADR-062 D-5). So
/// this module exists only behind a click handler — nothing here ever runs on
/// a ref merely being rendered, and there is deliberately no path from an
/// agent message to it. That is the whole no-actuation rule in one sentence:
/// an agent-emitted ref never focuses, scrolls, clicks or types.
///
/// Resolution is honest about its own depth. Switching to the right surface
/// always works; entity-level focus works where a store already exposes a
/// setter for it (Replay's dataset/episode, Inspect's tabs by path), and where
/// one does not, the chip lands the user on the surface and stops. That beats
/// pretending, and it beats not shipping the chip at all.
// `.ts` extensions so `node --test` resolves the module graph — all three
// stores are pure zustand and this file is on the plan's §5 test list.
import { isKnownJob, useWorkbench, type JobId } from './workbench.ts';
import { useReplay } from './replay.ts';
import { useInspect } from './inspect.ts';
import type { UiRef } from './uiRef.ts';

/// How far a click got. Returned so the caller can say something honest when
/// a ref points at a surface this build cannot open.
export type UiRefFocusResult = 'entity' | 'surface' | 'unknown';

/// Whether a ref can be focused at all — the chip renders either way (a
/// reference is worth reading), but an unfocusable one is not clickable.
export function canFocusUiRef(ref: UiRef): boolean {
  return isKnownJob(ref.surface);
}

export function focusUiRef(ref: UiRef): UiRefFocusResult {
  if (!isKnownJob(ref.surface)) return 'unknown';
  const job = ref.surface as JobId;
  // Entity focus is requested BEFORE the job switch: the Replay surface reads
  // its target as it mounts, so setting the job first would race a surface
  // that has not been told what to show yet.
  const focused = focusEntity(job, ref);
  useWorkbench.getState().setJob(job);
  return focused ? 'entity' : 'surface';
}

function focusEntity(job: JobId, ref: UiRef): boolean {
  const p = ref.params;
  if (job === 'replay') {
    const datasetId = p.dataset_id;
    // `openRegistered` needs the project too — the surface defaults to the
    // first project's library, so without it the dataset is never found.
    const projectId = p.project_id;
    if (datasetId !== undefined && projectId !== undefined) {
      const episode = toIndex(p.episode ?? p.episode_id);
      useReplay.getState().openRegistered({
        datasetId,
        projectId,
        ...(episode !== null ? { episode } : {}),
      });
      return true;
    }
    if (datasetId !== undefined) {
      // No project: the best we can do is preselect the id and let the
      // surface's own library resolve it.
      useReplay.getState().select(datasetId);
      return true;
    }
    return false;
  }
  if (job === 'debug') {
    const path = p.file ?? p.path;
    if (path === undefined) return false;
    const tabs = useInspect.getState().tabs;
    // Prefer an already-open tab over a second one for the same file: the
    // agent is pointing at what the user has, not asking for a new view.
    const existing = tabs.find((t) => t.path === path);
    if (existing !== undefined) {
      useInspect.getState().setActive(existing.id);
      return true;
    }
    return false;
  }
  return false;
}

function toIndex(raw: string | undefined): number | null {
  if (raw === undefined) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
}
