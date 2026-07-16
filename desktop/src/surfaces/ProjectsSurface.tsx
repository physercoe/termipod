import { useState } from 'react';
import { useProjects } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { AttentionDock } from './AttentionDock';
import { FocusRegion } from './FocusRegion';
import { ProjectCreate } from './ProjectCreate';

/// The **Projects** tab — the units of directed work, split out of the fleet
/// tree into their own surface (director request). Same three-region frame as
/// the fleet (list · focus · attention) so the app reads consistently, but the
/// left nav is *only* the projects list; selecting one drives the shared
/// `FocusRegion` to that project's board. The fleet tab, meanwhile, keeps just
/// the ops roster (stewards · agents · hosts).
export function ProjectsSurface(): JSX.Element {
  const t = useT();
  const projectsQ = useProjects();
  const selection = useFocus((s) => s.selection);
  const selectProject = useFocus((s) => s.selectProject);
  const connected = useSession((s) => s.client) !== null;
  const [creating, setCreating] = useState(false);

  const projects = projectsQ.data ?? [];
  const projectSelected = (id: string): boolean => selection?.type === 'project' && selection.id === id;

  return (
    <>
      <div className="fleet-toolbar">
        <span className="fleet-toolbar-label">{t('nav.projects')}</span>
        <span className="fleet-toolbar-sep" />
        <button disabled={!connected} onClick={() => setCreating(true)}>
          {t('project.new')}
        </button>
      </div>
      <div className="shell-body">
        <div className="region navigator">
          <div className="region-header">{t('nav.projects')}</div>
          <div className="tree">
            {projects.length === 0 ? (
              <div className="region-pad muted">
                {projectsQ.isLoading ? t('common.loading') : t('nav.noProjects')}
              </div>
            ) : (
              projects.map((p) => {
                const id = str(p, 'id') ?? '';
                const label = str(p, 'name') ?? str(p, 'title') ?? id;
                return (
                  <div
                    key={id}
                    className={`tree-agent${projectSelected(id) ? ' selected' : ''}`}
                    onClick={() => selectProject(id)}
                  >
                    <span className="dot muted" />
                    <span className="tree-agent-label">{label}</span>
                    {str(p, 'phase') !== undefined && <span className="tree-agent-kind">{str(p, 'phase')}</span>}
                  </div>
                );
              })
            )}
          </div>
        </div>

        <FocusRegion />

        <div className="region dock">
          <div className="region-header">{t('region.attention')}</div>
          <AttentionDock />
        </div>
      </div>

      {creating && <ProjectCreate onClose={() => setCreating(false)} />}
    </>
  );
}
