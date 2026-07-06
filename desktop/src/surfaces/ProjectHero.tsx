import { useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';

/// Pluggable overview hero (parity — mobile overview_widgets/registry.dart). The
/// hub resolves a per-project/per-phase `overview_widget` slug (returned on the
/// project GET, NOT the /overview digest); we dispatch on it here. Unknown slugs
/// fall through to `null` so the caller keeps its default goal/progress header.
///
/// Eight kinds mirror the mobile registry: task_milestone_list, recent_artifacts,
/// children_status, recent_firings_list (structural), and the research phase
/// heroes idea_conversation / deliverable_focus / experiment_dash /
/// paper_acceptance (deliverable-centric). The last two additionally surface
/// runs + metric-chart / pdf artifacts (rendered as links — the desktop ships no
/// inline chart/pdf engine, matching the run Charts/Media constraint).
const PRIORITY_ORDER = ['urgent', 'high', 'med', 'low'];

function TaskMilestoneList({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['tasks', projectId],
    enabled: client !== null,
    queryFn: () => client!.listTasks(projectId),
  });
  const open = (q.data ?? []).filter((tk) => {
    const s = str(tk, 'status');
    return s !== 'done' && s !== 'cancelled';
  });
  if (open.length === 0) return <div className="muted">{t('hero.noOpenTasks')}</div>;
  const byPri = new Map<string, Entity[]>();
  for (const tk of open) {
    const p = str(tk, 'priority') ?? 'med';
    (byPri.get(p) ?? byPri.set(p, []).get(p)!).push(tk);
  }
  return (
    <>
      {PRIORITY_ORDER.filter((p) => byPri.has(p)).map((p) => (
        <div key={p} className="hero-pri-group">
          <span className="pill">{p}</span>
          <div className="hero-pri-tasks">
            {(byPri.get(p) ?? []).map((tk) => (
              <div key={str(tk, 'id')} className="hero-task">
                {str(tk, 'title') ?? str(tk, 'summary') ?? str(tk, 'id')}
              </div>
            ))}
          </div>
        </div>
      ))}
    </>
  );
}

function RecentArtifacts({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['project-artifacts', projectId],
    enabled: client !== null,
    queryFn: () => client!.listArtifacts({ project: projectId }),
  });
  const items = (q.data ?? []).slice(0, 6);
  if (items.length === 0) return <div className="muted">{t('files.noArtifacts')}</div>;
  return (
    <>
      {items.map((a, i) => (
        <div key={str(a, 'id') ?? String(i)} className="admin-row">
          <span className="pill">{str(a, 'kind') ?? '—'}</span>
          <span>{str(a, 'name') ?? str(a, 'id')}</span>
          <span className="spacer" />
          <span className="muted small">{str(a, 'created_at') ?? ''}</span>
        </div>
      ))}
    </>
  );
}

function ChildrenStatus({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const selectProject = useFocus((s) => s.selectProject);
  const projectsQ = useProjects();
  const children = (projectsQ.data ?? []).filter((p) => str(p, 'parent_project_id') === projectId);
  if (children.length === 0) return <div className="muted">{t('hero.noChildren')}</div>;
  return (
    <>
      {children.map((c) => {
        const id = str(c, 'id') ?? '';
        return (
          <button key={id} className="admin-row clickable-row" onClick={() => id && selectProject(id)}>
            <span>{str(c, 'name') ?? id}</span>
            <span className="spacer" />
            <span className="muted small">{str(c, 'kind') ?? ''}</span>
            <span className={`sev${str(c, 'status') === 'active' ? ' sev-medium' : ''}`}>{str(c, 'status') ?? ''}</span>
          </button>
        );
      })}
    </>
  );
}

function RecentFirings({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['plans', projectId],
    enabled: client !== null,
    queryFn: () => client!.listPlans(projectId),
  });
  const items = (q.data ?? []).slice(0, 6);
  if (items.length === 0) return <div className="muted">{t('proj.noPlans')}</div>;
  return (
    <>
      {items.map((p, i) => (
        <div key={str(p, 'id') ?? String(i)} className="admin-row">
          <span className={`sev${str(p, 'status') === 'completed' ? ' sev-medium' : ''}`}>{str(p, 'status') ?? ''}</span>
          <span className="mono small">{str(p, 'id')}</span>
          <span className="spacer" />
          <span className="muted small">{str(p, 'created_at') ?? ''}</span>
        </div>
      ))}
    </>
  );
}

/// Shared research-phase hero: the project's deliverables (from /overview) plus
/// an optional extra region (runs, chart/pdf artifacts) for experiment/paper.
function PhaseHero({ projectId, extraKind }: { projectId: string; extraKind?: 'experiment' | 'paper' }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const ovQ = useQuery({
    queryKey: ['project-overview', projectId],
    enabled: client !== null,
    queryFn: () => client!.getProjectOverview(projectId),
  });
  const runsQ = useQuery({
    queryKey: ['runs', projectId],
    enabled: client !== null && extraKind === 'experiment',
    queryFn: () => client!.listRuns(projectId),
  });
  const artKind = extraKind === 'paper' ? 'pdf' : extraKind === 'experiment' ? 'metric-chart' : undefined;
  const artQ = useQuery({
    queryKey: ['project-artifacts', projectId, artKind],
    enabled: client !== null && artKind !== undefined,
    queryFn: () => client!.listArtifacts({ project: projectId, kind: artKind }),
  });

  const ov = ovQ.data ?? {};
  const deliverables = Array.isArray(ov['deliverables']) ? (ov['deliverables'] as Entity[]) : [];
  const runs = (runsQ.data ?? []).slice(0, 4);
  const artifacts = artQ.data ?? [];

  return (
    <>
      {deliverables.length === 0 ? (
        <div className="muted">{t('proj.noDeliverables')}</div>
      ) : (
        deliverables.map((d, i) => (
          <div key={str(d, 'id') ?? String(i)} className="admin-row">
            <span>{str(d, 'kind') ?? '—'}</span>
            <span className="spacer" />
            <span className={`sev${str(d, 'ratification_state') === 'ratified' ? ' sev-medium' : ''}`}>
              {str(d, 'ratification_state') ?? '—'}
            </span>
          </div>
        ))
      )}
      {extraKind === 'experiment' && runs.length > 0 && (
        <div className="hero-sub">
          <h4>{t('proj.runs')}</h4>
          {runs.map((r, i) => (
            <div key={str(r, 'id') ?? String(i)} className="admin-row">
              <span className="mono small">{str(r, 'id')}</span>
              <span className="spacer" />
              <span className="muted small">{str(r, 'status') ?? ''}</span>
            </div>
          ))}
        </div>
      )}
      {artKind !== undefined && artifacts.length > 0 && (
        <div className="hero-sub">
          <h4>{artKind}</h4>
          {artifacts.slice(0, 4).map((a, i) => (
            <div key={str(a, 'id') ?? String(i)} className="admin-row">
              <span>{str(a, 'name') ?? str(a, 'id')}</span>
              <span className="spacer" />
              <span className="muted small">{num(a, 'size') !== undefined ? `${num(a, 'size')} B` : ''}</span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}

const HERO_TITLES: Record<string, string> = {
  task_milestone_list: 'hero.tasks',
  recent_artifacts: 'hero.artifacts',
  children_status: 'hero.children',
  recent_firings_list: 'hero.firings',
  idea_conversation: 'hero.idea',
  deliverable_focus: 'hero.deliverable',
  experiment_dash: 'hero.experiment',
  paper_acceptance: 'hero.paper',
};

/// Returns the hero body for a slug, or null for unknown/default slugs (caller
/// keeps its own header). Exported title lookup drives the section heading.
export function ProjectHero({ projectId, kind }: { projectId: string; kind: string }): JSX.Element | null {
  const t = useT();
  let body: JSX.Element | null = null;
  switch (kind) {
    case 'task_milestone_list':
      body = <TaskMilestoneList projectId={projectId} />;
      break;
    case 'recent_artifacts':
      body = <RecentArtifacts projectId={projectId} />;
      break;
    case 'children_status':
      body = <ChildrenStatus projectId={projectId} />;
      break;
    case 'recent_firings_list':
      body = <RecentFirings projectId={projectId} />;
      break;
    case 'idea_conversation':
    case 'deliverable_focus':
      body = <PhaseHero projectId={projectId} />;
      break;
    case 'experiment_dash':
      body = <PhaseHero projectId={projectId} extraKind="experiment" />;
      break;
    case 'paper_acceptance':
      body = <PhaseHero projectId={projectId} extraKind="paper" />;
      break;
    default:
      return null;
  }
  return (
    <section className="setting-group hero-widget">
      <h3>{t(HERO_TITLES[kind] ?? 'proj.overview')}</h3>
      {body}
    </section>
  );
}
