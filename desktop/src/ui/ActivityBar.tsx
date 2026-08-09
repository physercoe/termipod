import { useT } from '../i18n';
import { isSplitEligible, JOBS, SETTINGS_JOB, useWorkbench, type JobId } from '../state/workbench';
import { useContextMenu, type MenuItem } from './ContextMenu';
import { JobIcon } from './JobIcon';

/// The workbench's left rail (VS Code activity-bar idiom): one button per job in
/// the `JOBS` registry, with Settings pinned to the bottom (the gear idiom).
/// Hub identity belongs to the persistent status bar; keeping it out of this
/// narrow rail leaves the native macOS controls and job navigation visually
/// independent. Icon-forward with a small label so the jobs stay discoverable;
/// the active job is highlighted and switching is instant (surface state lives
/// in each surface, not here).
/// The assistant toggle is NOT here — it lives in the status bar as a chip (#460),
/// keeping the rail purely job navigation.
///
/// Split pane (S2): **Alt-click** or right-click → "Open beside" pins a job as the
/// secondary pane; the pinned job carries a corner dot. Plain click keeps its
/// meaning — it switches the active pane's surface, it never pins one.
export function ActivityBar(): JSX.Element {
  const t = useT();
  const job = useWorkbench((s) => s.job);
  const secondary = useWorkbench((s) => s.secondary);
  const setJob = useWorkbench((s) => s.setJob);
  const setSecondary = useWorkbench((s) => s.setSecondary);
  const { open: openMenu, node: menuNode } = useContextMenu();

  // "Open beside" is offered only where it can apply: the target must be
  // pinnable, must not already be the primary (no job in both panes), and the
  // primary itself must be able to share the row — nothing pairs with the
  // terminal or Settings. Same gating as the palette's split commands.
  function paneMenu(id: JobId): MenuItem[] {
    const items: MenuItem[] = [];
    if (isSplitEligible(id) && isSplitEligible(job) && id !== job && id !== secondary) {
      items.push({ label: t('job.openBeside'), onClick: () => setSecondary(id) });
    }
    if (id === secondary) items.push({ label: t('cmd.splitClose'), onClick: () => setSecondary(null) });
    return items;
  }

  return (
    <nav className="activity-bar" aria-label={t('job.rail')}>
      <div className="activity-jobs">
        {JOBS.map((j) => {
          // `beside` = this job IS the pinned secondary; `canOpenBeside` = it
          // could become it. Not "pinned": `.activity-tab-pinned` already means
          // "pinned to the bottom of the rail" (the Settings gear).
          const beside = secondary === j.id;
          const canOpenBeside = isSplitEligible(j.id) && isSplitEligible(job) && j.id !== job && !beside;
          return (
            <button
              key={j.id}
              // Stable identity hook for e2e. Without it a test can only reach a
              // rail item by position (the Ctrl+<n> shortcut) or by its
              // translated label, and both break when the rail gains a job —
              // which is exactly how adding J8 Replay broke the terminal spec.
              data-job={j.id}
              data-beside={beside ? '1' : undefined}
              className={`activity-tab${job === j.id ? ' active' : ''}${beside ? ' beside' : ''}`}
              aria-current={job === j.id ? 'page' : undefined}
              title={`${j.tag ? `${j.tag} · ` : ''}${t(j.hintKey)}${canOpenBeside ? ` · ${t('job.openBesideHint')}` : ''}`}
              onClick={(e) => {
                // Alt-click pins beside instead of switching. Guarded so a
                // modifier press never asks for a state the store refuses.
                if (e.altKey && canOpenBeside) setSecondary(j.id);
                else setJob(j.id);
              }}
              onContextMenu={(e) => {
                const items = paneMenu(j.id);
                if (items.length > 0) openMenu(e, items);
              }}
            >
              <span className="activity-icon">
                <JobIcon id={j.id} size={20} />
              </span>
              <span className="activity-label">{t(j.labelKey)}</span>
              {beside && <span className="activity-tab-dot" aria-hidden="true" />}
            </button>
          );
        })}
      </div>
      <button
        className={`activity-tab activity-tab-pinned${job === SETTINGS_JOB.id ? ' active' : ''}`}
        aria-current={job === SETTINGS_JOB.id ? 'page' : undefined}
        title={t(SETTINGS_JOB.hintKey)}
        onClick={() => setJob(SETTINGS_JOB.id)}
      >
        <span className="activity-icon">
          <JobIcon id={SETTINGS_JOB.id} size={20} />
        </span>
        <span className="activity-label">{t(SETTINGS_JOB.labelKey)}</span>
      </button>
      {menuNode}
    </nav>
  );
}
