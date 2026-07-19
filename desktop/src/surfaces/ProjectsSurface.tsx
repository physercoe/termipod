import { useState } from 'react';
import { useProjects } from '../hub/queries';
import { str, type Entity } from '../hub/types';
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

export function ProjectsSurface(): JSX.Element {
  const t = useT();
  const projectsQ = useProjects();
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
        shown.map((p) => {
          const id = str(p, 'id') ?? '';
          const label = str(p, 'name') ?? str(p, 'title') ?? id;
          return (
            <div
              key={id}
              className={`tree-agent${projectSelected(id) ? ' selected' : ''}`}
              onClick={() => selectProject('projects', id, label)}
            >
              <span className="dot muted" />
              <span className="tree-agent-label">{label}</span>
              {str(p, 'phase') !== undefined && <span className="tree-agent-kind">{str(p, 'phase')}</span>}
            </div>
          );
        })
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
