import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useSession } from '../state/session';
import { ConfirmButton } from '../ui/ConfirmButton';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function TeamTab(): JSX.Element {
  const client = useSession((s) => s.client);
  const policyQ = useQuery({
    queryKey: ['policy'],
    enabled: client !== null,
    queryFn: () => client!.getPolicy(),
  });
  const principalsQ = useQuery({
    queryKey: ['principals'],
    enabled: client !== null,
    queryFn: () => client!.listPrincipals(),
  });

  const policyText =
    policyQ.data === undefined
      ? ''
      : (str(policyQ.data, 'yaml') ?? JSON.stringify(policyQ.data, null, 2));

  return (
    <div className="admin-cols">
      <section>
        <h3>Members</h3>
        {principalsQ.isError ? (
          <div className="error">{msg(principalsQ.error)}</div>
        ) : (
          (principalsQ.data ?? []).map((p, i) => (
            <div key={str(p, 'id') ?? String(i)} className="admin-row">
              <span>{str(p, 'handle') ?? str(p, 'id')}</span>
              <span className="muted">{str(p, 'role') ?? str(p, 'kind')}</span>
            </div>
          ))
        )}
        {!principalsQ.isError && (principalsQ.data ?? []).length === 0 && (
          <div className="muted">No members.</div>
        )}
      </section>
      <section>
        <h3>Policy</h3>
        {policyQ.isError ? (
          <div className="error">{msg(policyQ.error)}</div>
        ) : (
          <pre className="mono">{policyText}</pre>
        )}
      </section>
    </div>
  );
}

function HostsTab(): JSX.Element {
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['admin-hosts'],
    enabled: client !== null,
    queryFn: () => client!.adminListHosts(),
  });

  async function act(host: string, action: 'ping' | 'restart' | 'shutdown' | 'update'): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      await client.adminHostAction(host, action);
      await qc.invalidateQueries({ queryKey: ['admin-hosts'] });
    } catch (e) {
      setError(msg(e));
    }
  }

  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const hosts = q.data ?? [];

  return (
    <>
      {error !== null && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr>
            <th>Host</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {hosts.map((h, i) => {
            const id = str(h, 'id') ?? str(h, 'name') ?? String(i);
            return (
              <tr key={id}>
                <td>{str(h, 'name') ?? str(h, 'hostname') ?? id}</td>
                <td>{str(h, 'status') ?? ''}</td>
                <td className="admin-actions">
                  <button onClick={() => void act(id, 'ping')}>Ping</button>
                  <ConfirmButton label="Restart" onConfirm={() => void act(id, 'restart')} />
                  <ConfirmButton label="Update" onConfirm={() => void act(id, 'update')} />
                  <ConfirmButton label="Shutdown" danger onConfirm={() => void act(id, 'shutdown')} />
                </td>
              </tr>
            );
          })}
          {hosts.length === 0 && (
            <tr>
              <td colSpan={3} className="muted">
                No hosts (or token lacks operator scope).
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

function AgentsTab(): JSX.Element {
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['admin-agents'],
    enabled: client !== null,
    queryFn: () => client!.adminListAgents(),
  });

  async function kill(agent: string): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      await client.adminKillAgent(agent);
      await qc.invalidateQueries({ queryKey: ['admin-agents'] });
    } catch (e) {
      setError(msg(e));
    }
  }

  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const agents = q.data ?? [];

  return (
    <>
      {error !== null && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr>
            <th>Agent</th>
            <th>Team</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {agents.map((a: Entity, i) => {
            const id = str(a, 'id') ?? String(i);
            return (
              <tr key={id}>
                <td>{str(a, 'handle') ?? id}</td>
                <td>{str(a, 'team_id') ?? ''}</td>
                <td>{str(a, 'status') ?? ''}</td>
                <td className="admin-actions">
                  <ConfirmButton label="Kill" danger onConfirm={() => void kill(id)} />
                </td>
              </tr>
            );
          })}
          {agents.length === 0 && (
            <tr>
              <td colSpan={4} className="muted">
                No agents (or token lacks operator scope).
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

/// WS7 — Team governance + operator Admin cockpit as an overlay. Team tab
/// (members + policy); Hosts/Agents admin tabs with confirmed destructive
/// actions. Admin endpoints 403 gracefully for non-operator tokens.
export function AdminCockpit({ onClose }: { onClose: () => void }): JSX.Element {
  const [tab, setTab] = useState<'team' | 'hosts' | 'agents'>('team');
  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="admin" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <button className={tab === 'team' ? 'tab active' : 'tab'} onClick={() => setTab('team')}>
            Team
          </button>
          <button className={tab === 'hosts' ? 'tab active' : 'tab'} onClick={() => setTab('hosts')}>
            Hosts
          </button>
          <button className={tab === 'agents' ? 'tab active' : 'tab'} onClick={() => setTab('agents')}>
            Agents
          </button>
          <span className="spacer" />
          <button onClick={onClose}>Close</button>
        </div>
        <div className="admin-body">
          {tab === 'team' && <TeamTab />}
          {tab === 'hosts' && <HostsTab />}
          {tab === 'agents' && <AgentsTab />}
        </div>
      </div>
    </div>
  );
}
