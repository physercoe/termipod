import { useAgents, useHosts } from '../hub/queries';
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

/// Left region — the persistent fleet tree (Hosts ▸ agents). Selection drives
/// the Focus region (WS4 transcript). Sessions/projects branches land later.
export function Navigator(): JSX.Element {
  const agentsQ = useAgents();
  const hostsQ = useHosts();
  const selected = useFocus((s) => s.selectedAgentId);
  const select = useFocus((s) => s.select);

  const agents = agentsQ.data ?? [];
  const hosts = hostsQ.data ?? [];

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

  if (agentsQ.isLoading) return <div className="region-pad muted">Loading fleet…</div>;
  if (agentsQ.isError) {
    return <div className="region-pad error">{(agentsQ.error as Error).message}</div>;
  }

  return (
    <div className="tree">
      {hostIds.length === 0 && <div className="region-pad muted">No agents.</div>}
      {hostIds.map((hid) => (
        <div key={hid || 'unassigned'} className="tree-group">
          <div className="tree-host">{hostLabel(hid)}</div>
          {(byHost.get(hid) ?? []).map((a) => {
            const id = str(a, 'id') ?? '';
            const label = str(a, 'handle') ?? str(a, 'name') ?? id;
            const kind = str(a, 'kind') ?? '';
            const status = str(a, 'status');
            return (
              <div
                key={id}
                className={`tree-agent${id === selected ? ' selected' : ''}`}
                onClick={() => select(id)}
                title={kind}
              >
                <span className={`dot ${statusClass(status)}`} />
                <span className="tree-agent-label">{label}</span>
                <span className="tree-agent-kind">{kind.replace(/^steward\./, '★')}</span>
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}
