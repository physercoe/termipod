import type { ReactNode } from 'react';
import { useT } from '../i18n';
import { JOBS, SETTINGS_JOB, useWorkbench } from '../state/workbench';
import { JobIcon } from './JobIcon';

/// The workbench's left rail (VS Code activity-bar idiom): the hub identity /
/// connection chrome at the top (`chrome` — the profile switcher, relocated here
/// from the bottom status bar), one button per job in the `JOBS` registry, and the
/// Settings tab pinned to the bottom (the gear idiom). Icon-forward with a small
/// label so the jobs stay discoverable; the active job is highlighted and
/// switching is instant (the surface state lives in each surface, not here).
export function ActivityBar({ chrome }: { chrome?: ReactNode }): JSX.Element {
  const t = useT();
  const job = useWorkbench((s) => s.job);
  const setJob = useWorkbench((s) => s.setJob);

  return (
    <nav className="activity-bar" aria-label={t('job.rail')}>
      <div className="activity-hub" title="TermiPod — Desktop Workbench">
        {chrome ?? <span className="activity-brand-mark">TP</span>}
      </div>
      <div className="activity-jobs">
        {JOBS.map((j) => (
          <button
            key={j.id}
            className={`activity-tab${job === j.id ? ' active' : ''}`}
            aria-current={job === j.id ? 'page' : undefined}
            title={`${j.tag ? `${j.tag} · ` : ''}${t(j.hintKey)}`}
            onClick={() => setJob(j.id)}
          >
            <span className="activity-icon">
              <JobIcon id={j.id} />
            </span>
            <span className="activity-label">{t(j.labelKey)}</span>
          </button>
        ))}
      </div>
      <button
        className={`activity-tab activity-tab-pinned${job === SETTINGS_JOB.id ? ' active' : ''}`}
        aria-current={job === SETTINGS_JOB.id ? 'page' : undefined}
        title={t(SETTINGS_JOB.hintKey)}
        onClick={() => setJob(SETTINGS_JOB.id)}
      >
        <span className="activity-icon">
          <JobIcon id={SETTINGS_JOB.id} />
        </span>
        <span className="activity-label">{t(SETTINGS_JOB.labelKey)}</span>
      </button>
    </nav>
  );
}
