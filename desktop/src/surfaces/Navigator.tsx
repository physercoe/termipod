import { useAgents, useHosts, useProjects } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useFocus } from '../state/focus';

function statusClass(status: string | undefined): string {
  switch (status) {
    case 'running':
      return 'running';
    case 'paused':
      return 'paused';
    case 'pending':
      return 'pending';
    case 'terminated':
    case 'crashed':
    case 'failed':
      return 'stopped';
    default:
      return 'muted';
  }
}

/// Left region — the persistent fleet + projects tree. Selection drives the
/// Focus region (agent transcript / project board).
export function Navigator(): JSX.Element {
  const agentsQ = useAgents();
  const hostsQ = useHosts();
  const projectsQ = useProjects();
  const selection = useFocus((s) => s.selection);
  const selectAgent = useFocus((s) => s.selectAgent);
  const selectProject = useFocus((s) => s.selectProject);

  const agents = agentsQ.data ?? [];
  const hosts = hostsQ.data ?? [];
  const projects = projectsQ.data ?? [];

  const hostLabel = (id: string): string => {
    const h = hosts.find((x) => str(x, 'id') === id);
    return (h && (str(h, 'name') ?? str(h, 'hostname'))) ?? (id || 'unassigned');
  };

  const byHost = new Map<string, Entity[]>();
  for (const a of agents) {
    const hid = str(a, 'host_id') ?? '';
    const list = byHost.get(hid);
    if (list) list.push(a);
    else byHost.set(hid, [a]);
  }
  const hostIds = [...byHost.keys()].sort();

  const agentSelected = (id: string): boolean =>
    selection?.type === 'agent' && selection.id === id;
  const projectSelected = (id: string): boolean =>
    selection?.type === 'project' && selection.id === id;

  return (
    <div className="tree">
      <div className="tree-section">Fleet</div>
      {agentsQ.isError && <div className="region-pad error">{(agentsQ.error as Error).message}</div>}
      {!agentsQ.isError && hostIds.length === 0 && (
        <div className="region-pad muted">{agentsQ.isLoading ? 'Loading…' : 'No agents.'}</div>
      )}
      {hostIds.map((hid) => (
        <div key={hid || 'unassigned'} className="tree-group">
          <div className="tree-host">{hostLabel(hid)}</div>
          {(byHost.get(hid) ?? []).map((a) => {
            const id = str(a, 'id') ?? '';
            const label = str(a, 'handle') ?? str(a, 'name') ?? id;
            const kind = str(a, 'kind') ?? '';
            return (
              <div
                key={id}
                className={`tree-agent${agentSelected(id) ? ' selected' : ''}`}
                onClick={() => selectAgent(id)}
                title={kind}
              >
                <span className={`dot ${statusClass(str(a, 'status'))}`} />
                <span className="tree-agent-label">{label}</span>
                <span className="tree-agent-kind">{kind.replace(/^steward\./, '★')}</span>
              </div>
            );
          })}
        </div>
      ))}

      <div className="tree-section">Projects</div>
      {projects.length === 0 && (
        <div className="region-pad muted">{projectsQ.isLoading ? 'Loading…' : 'No projects.'}</div>
      )}
      {projects.map((p) => {
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
      })}
    </div>
  );
}
