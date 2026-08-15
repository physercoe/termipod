import type { ReactNode } from 'react';
import { useT } from '../i18n';
import { JOBS, type JobId } from '../state/workbench';
import { Icon } from './Icon';
import { JobIcon } from './JobIcon';

/// One persistent, icon-only control for a surface side pane. Keeping the same
/// button in the header while the pane opens and closes avoids the old pattern
/// where the affordance jumped between an in-pane collapse button and a narrow
/// body-edge reveal rail.
export function HeaderPaneToggle({
  side,
  open,
  showLabel,
  hideLabel,
  onToggle,
}: {
  side: 'left' | 'right';
  open: boolean;
  showLabel: string;
  hideLabel: string;
  onToggle: () => void;
}): JSX.Element {
  const label = open ? hideLabel : showLabel;
  return (
    <button
      className={`pane-toggle header-pane-toggle ${side}`}
      title={label}
      aria-label={label}
      aria-pressed={open}
      onClick={onToggle}
    >
      <Icon name="sidebar" size={16} className={side === 'right' ? 'mirror-x' : undefined} />
    </button>
  );
}

/// Shared chrome for a workbench job surface: a titled header with a stable
/// leading slot for left-pane controls, a trailing actions slot, and a scrolling
/// body. Keeps every job surface visually consistent with the fleet regions
/// while giving each its own centre-stage layout.
export function WorkbenchSurface({
  job,
  leadingActions,
  actions,
  children,
}: {
  job: JobId;
  leadingActions?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}): JSX.Element {
  const t = useT();
  const def = JOBS.find((j) => j.id === job);
  return (
    <section className={`surface surface-${job}`} aria-label={def ? t(def.labelKey) : job}>
      <header className="surface-head">
        {leadingActions !== undefined && <div className="surface-leading-actions">{leadingActions}</div>}
        <span className="surface-icon">{def && <JobIcon id={def.id} size={20} />}</span>
        <div className="surface-titles">
          <div className="surface-title">
            {def ? t(def.labelKey) : job}
          </div>
          <div className="surface-hint">{def ? t(def.hintKey) : ''}</div>
        </div>
        <span className="spacer" />
        {actions !== undefined && <div className="surface-actions">{actions}</div>}
      </header>
      <div className="surface-body scroll">{children}</div>
    </section>
  );
}

/// A short "what this tab will become" placard for jobs whose primary component
/// is still an unshipped EMBED (e.g. J4's tldraw canvas). Honest about the
/// posture from `research-tooling-landscape.md` rather than faking a surface.
export function SurfacePlaceholder({
  posture,
  lines,
}: {
  posture: string;
  lines: string[];
}): JSX.Element {
  return (
    <div className="surface-placeholder region-pad">
      <div className="surface-posture">{posture}</div>
      <ul className="surface-todo">
        {lines.map((l, i) => (
          <li key={i}>{l}</li>
        ))}
      </ul>
    </div>
  );
}
