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
import { useReadTabs } from './readTabs.ts';
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
  return focusUiRefWithUndo(ref).result;
}

/// How far a focus got, plus how to put the screen back.
export interface UiRefFocusOutcome {
  result: UiRefFocusResult;
  /// Reverses everything this call changed, or null when it changed nothing.
  ///
  /// Only lane H's `desktop_open` uses it: a navigation the AGENT started needs
  /// an undo, because the user did not ask for it. A user clicking a chip is
  /// already where they wanted to go, so `focusUiRef` discards this.
  ///
  /// Best-effort by surface, and honest about it: the workbench job always
  /// reverts (that is "put me back on the tab I was on"), and per-surface state
  /// reverts where a store exposes the setter — a Read tab this call OPENED is
  /// closed again, an Inspect tab it merely focused hands focus back. What it
  /// cannot do is un-consume a handoff a surface has already acted on; those
  /// leave the entity selected and only the job returns.
  undo: (() => void) | null;
}

export function focusUiRefWithUndo(ref: UiRef): UiRefFocusOutcome {
  if (!isKnownJob(ref.surface)) return { result: 'unknown', undo: null };
  const job = ref.surface as JobId;
  const previousJob = useWorkbench.getState().job;
  const revert: (() => void)[] = [];
  // Entity focus is requested BEFORE the job switch: the Replay surface reads
  // its target as it mounts, so setting the job first would race a surface
  // that has not been told what to show yet.
  const focused = focusEntity(job, ref, revert);
  useWorkbench.getState().setJob(job);
  revert.push(() => useWorkbench.getState().setJob(previousJob));
  return {
    result: focused ? 'entity' : 'surface',
    // LIFO, by convention rather than by necessity: every revert today is a
    // write to a different store, so the order is not observable and a test
    // cannot pin it (a mutation to forward order survives, which is how this
    // comment stopped claiming otherwise). Kept because it is what the next
    // person adding an order-sensitive revert will expect — and if you add one,
    // this is the line that makes its position matter.
    undo: () => {
      for (const fn of [...revert].reverse()) fn();
    },
  };
}

/// http(s) only, for a URL an agent chose.
///
/// The webview partition enforces the same rule at the guest layer
/// (`electron/src/webtab_policy.ts`, `allowTopFrame`), which is the wall that
/// actually stops a navigation. This is the earlier one: without it a `file://`
/// or `javascript:` ref would still MINT a tab that then refuses to load, and
/// the user would be looking at a broken tab an agent opened. Two layers on
/// purpose — the guest policy cannot be imported here (different package) and
/// should not be, since it answers a different question at a different time.
export function isNavigableUrl(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

function focusEntity(job: JobId, ref: UiRef, revert: (() => void)[]): boolean {
  const p = ref.params;
  if (job === 'replay') {
    const datasetId = p.dataset_id;
    // `openRegistered` needs the project too — the surface defaults to the
    // first project's library, so without it the dataset is never found.
    const projectId = p.project_id;
    const previousSelected = useReplay.getState().selectedId;
    if (datasetId !== undefined && projectId !== undefined) {
      const episode = toIndex(p.episode ?? p.episode_id);
      useReplay.getState().openRegistered({
        datasetId,
        projectId,
        ...(episode !== null ? { episode } : {}),
      });
      // Clearing the target is only half an undo: once the surface has consumed
      // it the dataset is open, and nothing here can un-open it. The job revert
      // is what actually puts the user back.
      revert.push(() => {
        useReplay.getState().clearTarget();
        useReplay.getState().select(previousSelected);
      });
      return true;
    }
    if (datasetId !== undefined) {
      // No project: the best we can do is preselect the id and let the
      // surface's own library resolve it.
      useReplay.getState().select(datasetId);
      revert.push(() => useReplay.getState().select(previousSelected));
      return true;
    }
    return false;
  }
  if (job === 'read') {
    const tabs = useReadTabs.getState();
    const previousActive = tabs.activeId;
    const tabId = p.tab_id;
    if (tabId !== undefined) {
      if (!tabs.tabs.some((t) => t.id === tabId)) return false;
      tabs.setActive(tabId);
      revert.push(() => useReadTabs.getState().setActive(previousActive));
      return true;
    }
    const url = p.url;
    if (url === undefined || !isNavigableUrl(url)) return false;
    // Prefer a tab already showing this page. Read is the one surface where an
    // agent can drive a web tab but could not OPEN one (lane H3), and opening a
    // second copy of a page the user already has would be the wrong reading of
    // "show me this".
    const open = tabs.tabs.find((t) => t.kind === 'web' && t.url === url);
    if (open !== undefined) {
      tabs.setActive(open.id);
      revert.push(() => useReadTabs.getState().setActive(previousActive));
      return true;
    }
    const id = tabs.open({ kind: 'web', url, title: hostOfUrl(url) });
    // This one CAN be fully undone: the tab did not exist a moment ago.
    revert.push(() => {
      useReadTabs.getState().close(id);
      useReadTabs.getState().setActive(previousActive);
    });
    return true;
  }
  if (job === 'debug') {
    const path = p.file ?? p.path;
    if (path === undefined) return false;
    const tabs = useInspect.getState().tabs;
    // Prefer an already-open tab over a second one for the same file: the
    // agent is pointing at what the user has, not asking for a new view.
    const existing = tabs.find((t) => t.path === path);
    if (existing !== undefined) {
      const previousActive = useInspect.getState().activeId;
      useInspect.getState().setActive(existing.id);
      revert.push(() => useInspect.getState().setActive(previousActive));
      return true;
    }
    return false;
  }
  return false;
}

/// A web tab's strip label before the guest reports its real title. Host only —
/// a full URL in a tab strip is unreadable, and the title is replaced as soon as
/// the page loads.
function hostOfUrl(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function toIndex(raw: string | undefined): number | null {
  if (raw === undefined) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
}
