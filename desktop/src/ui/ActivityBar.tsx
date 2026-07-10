import { useT } from '../i18n';
import { JOBS, useWorkbench } from '../state/workbench';

/// The workbench's left rail (VS Code activity-bar idiom): one button per job in
/// the `JOBS` registry. Icon-forward with a small label so the seven jobs stay
/// discoverable; the active job is highlighted and switching is instant (the
/// surface state lives in each surface, not here). This is the affordance the
/// director asked for — "the jobs show as distinct sidebar tabs" — replacing the
/// old single control-fleet screen.
export function ActivityBar(): JSX.Element {
  const t = useT();
  const job = useWorkbench((s) => s.job);
  const setJob = useWorkbench((s) => s.setJob);

  return (
    <nav className="activity-bar" aria-label={t('job.rail')}>
      {JOBS.map((j) => (
        <button
          key={j.id}
          className={`activity-tab${job === j.id ? ' active' : ''}`}
          aria-current={job === j.id ? 'page' : undefined}
          title={`${j.tag ? `${j.tag} · ` : ''}${t(j.hintKey)}`}
          onClick={() => setJob(j.id)}
        >
          <span className="activity-icon">{j.icon}</span>
          <span className="activity-label">{t(j.labelKey)}</span>
        </button>
      ))}
    </nav>
  );
}
