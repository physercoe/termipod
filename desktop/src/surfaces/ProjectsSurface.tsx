import { useState } from 'react';
import { useProjectInsights, useProjects } from '../hub/queries';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { MissionLayout } from '../ui/MissionLayout';
import { ProjectCreate } from './ProjectCreate';

/// The **Projects** tab — the units of directed work, split out of the fleet
/// tree (director request). Its left nav has two subtabs so the two hub project
/// *kinds* no longer intermix: **Projects** (`kind:'goal'`, phased directed work)
/// and **Workspaces** (`kind:'standing'`, long-lived spaces with no goal
/// lifecycle). Selecting either drives the shared FocusRegion to its board; "New"
/// pre-selects the active subtab's kind.
type Sub = 'goal' | 'standing';

function loadSub(): Sub {
  try {
    return localStorage.getItem('termipod.projects.navSub') === 'standing' ? 'standing' : 'goal';
  } catch {
    return 'goal';
  }
}

/// Fold the team-insights `by_project[]` aggregate into two lookups the nav
/// rows read: per-project stats (progress / phase / open-AC / own attention),
/// and a parent→Σ(children open_attention) rollup so a parent row surfaces work
/// waiting on its children (mobile `_ProjectListCard` parity). Pure derivation
/// over data already fetched — no extra requests.
function foldInsights(
  insights: Entity | undefined,
  projects: Entity[],
): { byProject: Map<string, Entity>; childAttention: Map<string, number> } {
  const byProject = new Map<string, Entity>();
  const rows = insights !== undefined && Array.isArray(insights['by_project']) ? (insights['by_project'] as Entity[]) : [];
  for (const r of rows) {
    const id = str(r, 'project_id');
    if (id !== undefined) byProject.set(id, r);
  }
  const childAttention = new Map<string, number>();
  for (const p of projects) {
    const parent = str(p, 'parent_project_id');
    if (parent === undefined || parent === '') continue;
    const own = num(byProject.get(str(p, 'id') ?? '') ?? {}, 'open_attention') ?? 0;
    if (own > 0) childAttention.set(parent, (childAttention.get(parent) ?? 0) + own);
  }
  return { byProject, childAttention };
}

export function ProjectsSurface(): JSX.Element {
  const t = useT();
  const projectsQ = useProjects();
  const insightsQ = useProjectInsights();
  const selection = useFocus((s) => s.projects.selection);
  const selectProject = useFocus((s) => s.selectProject);
  const connected = useSession((s) => s.client) !== null;
  const [sub, setSub] = useState<Sub>(loadSub);
  const [creating, setCreating] = useState(false);

  function pick(s: Sub): void {
    setSub(s);
    try {
      localStorage.setItem('termipod.projects.navSub', s);
    } catch {
      /* ignore */
    }
  }

  const all = projectsQ.data ?? [];
  // A project with no `kind` predates the goal/standing split — treat it as a
  // goal project (the original meaning) so nothing vanishes from the list.
  const kindOf = (p: Entity): Sub => (str(p, 'kind') === 'standing' ? 'standing' : 'goal');
  const shown = all.filter((p) => kindOf(p) === sub);
  const projectSelected = (id: string): boolean => selection?.type === 'project' && selection.id === id;
  const { byProject, childAttention } = foldInsights(insightsQ.data, all);

  /// One project nav row. Goal projects with insights data render the mobile
  /// 3-line card (name+status+attention / phase+open-ACs / progress); workspaces
  /// and pre-insights rows fall back to the flat dot+name+phase row.
  function renderRow(p: Entity): JSX.Element {
    const id = str(p, 'id') ?? '';
    const label = str(p, 'name') ?? str(p, 'title') ?? id;
    const selected = projectSelected(id);
    const agg = byProject.get(id);
    const started = p['steward_started'] === true;
    const dotCls = `dot${started ? ' running' : ' muted'}`;
    // Attention badge = this project's own open items + those waiting on its
    // children (rollup), so a parent never hides a child that needs the director.
    const attn = (num(agg ?? {}, 'open_attention') ?? 0) + (childAttention.get(id) ?? 0);
    if (agg === undefined) {
      // Workspace / no-insights → the flat row (unchanged).
      return (
        <div
          key={id}
          className={`tree-agent${selected ? ' selected' : ''}`}
          onClick={() => selectProject('projects', id, label)}
        >
          <span className={dotCls} />
          <span className="tree-agent-label">{label}</span>
          {attn > 0 && <span className="attn-badge">{attn}</span>}
          {str(p, 'phase') !== undefined && <span className="tree-agent-kind">{str(p, 'phase')}</span>}
        </div>
      );
    }
    const phase = str(agg, 'current_phase') ?? str(p, 'phase') ?? '';
    const phaseIndex = num(agg, 'phase_index') ?? 0;
    const phasesTotal = num(agg, 'phases_total') ?? 0;
    const openCriteria = num(agg, 'open_criteria') ?? 0;
    const progress = Math.max(0, Math.min(1, num(agg, 'progress') ?? 0));
    return (
      <div
        key={id}
        className={`tree-agent project-card${selected ? ' selected' : ''}`}
        onClick={() => selectProject('projects', id, label)}
      >
        <div className="project-card-line">
          <span className={dotCls} />
          <span className="tree-agent-label">{label}</span>
          {attn > 0 && <span className="attn-badge" title={t('proj.openAttention')}>{attn}</span>}
        </div>
        <div className="project-card-line project-card-meta">
          {phase !== '' && (
            <span className="phase-pill">
              {phase}
              {phasesTotal > 0 && <span className="muted"> {phaseIndex}/{phasesTotal}</span>}
            </span>
          )}
          {openCriteria > 0 && <span className="ac-chip" title={t('proj.openCriteria')}>{openCriteria} AC</span>}
          <span className="spacer" />
          <span className="project-card-pct muted small">{Math.round(progress * 100)}%</span>
        </div>
        <div className="proj-progress">
          <div className="proj-progress-fill" style={{ width: `${Math.round(progress * 100)}%` }} />
        </div>
      </div>
    );
  }

  const toolbar = (
    <>
      <span className="fleet-toolbar-label">{t('nav.projects')}</span>
      <span className="fleet-toolbar-sep" />
      <button disabled={!connected} onClick={() => setCreating(true)}>
        {sub === 'standing' ? t('project.newWorkspace') : t('project.new')}
      </button>
    </>
  );

  const nav = (
    <div className="tree">
      <div className="nav-subtabs seg">
        <button className={sub === 'goal' ? 'seg-btn active' : 'seg-btn'} onClick={() => pick('goal')}>
          {t('nav.projects')}
        </button>
        <button className={sub === 'standing' ? 'seg-btn active' : 'seg-btn'} onClick={() => pick('standing')}>
          {t('nav.workspaces')}
        </button>
      </div>
      {shown.length === 0 ? (
        <div className="region-pad muted">
          {projectsQ.isLoading
            ? t('common.loading')
            : sub === 'standing'
              ? t('nav.noWorkspaces')
              : t('nav.noProjects')}
        </div>
      ) : (
        shown.map((p) => renderRow(p))
      )}
    </div>
  );

  return (
    <>
      <MissionLayout storageKey="projects" toolbar={toolbar} nav={nav} />
      {creating && <ProjectCreate initialKind={sub} onClose={() => setCreating(false)} />}
    </>
  );
}
