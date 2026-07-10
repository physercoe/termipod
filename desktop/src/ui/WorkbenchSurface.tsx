import type { ReactNode } from 'react';
import { useT } from '../i18n';
import { JOBS, type JobId } from '../state/workbench';
import { JobIcon } from './JobIcon';

/// Shared chrome for a workbench job surface (J1–J6): a titled header with the
/// job's J-tag and one-line hint, an optional actions slot, and a scrolling body.
/// Keeps every job surface visually consistent with the fleet regions while
/// giving each its own centre-stage layout.
export function WorkbenchSurface({
  job,
  actions,
  children,
}: {
  job: JobId;
  actions?: ReactNode;
  children: ReactNode;
}): JSX.Element {
  const t = useT();
  const def = JOBS.find((j) => j.id === job);
  return (
    <section className="surface" aria-label={def ? t(def.labelKey) : job}>
      <header className="surface-head">
        <span className="surface-icon">{def && <JobIcon id={def.id} size={20} />}</span>
        <div className="surface-titles">
          <div className="surface-title">
            {def?.tag && <span className="surface-tag">{def.tag}</span>}
            {def ? t(def.labelKey) : job}
          </div>
          <div className="surface-hint">{def ? t(def.hintKey) : ''}</div>
        </div>
        <span className="spacer" />
        {actions}
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
