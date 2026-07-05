import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { ConfirmButton } from '../ui/ConfirmButton';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function TeamTab(): JSX.Element {
  const t = useT();
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
        <h3>{t('admin.members')}</h3>
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
          <div className="muted">{t('admin.noMembers')}</div>
        )}
      </section>
      <section>
        <h3>{t('admin.policy')}</h3>
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
  const t = useT();
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
            <th>{t('admin.host')}</th>
            <th>{t('admin.status')}</th>
            <th>{t('admin.actions')}</th>
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
                  <button onClick={() => void act(id, 'ping')}>{t('admin.ping')}</button>
                  <ConfirmButton label={t('admin.restart')} onConfirm={() => void act(id, 'restart')} />
                  <ConfirmButton label={t('admin.update')} onConfirm={() => void act(id, 'update')} />
                  <ConfirmButton label={t('admin.shutdown')} danger onConfirm={() => void act(id, 'shutdown')} />
                </td>
              </tr>
            );
          })}
          {hosts.length === 0 && (
            <tr>
              <td colSpan={3} className="muted">
                {t('admin.noHosts')}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

function AgentsTab(): JSX.Element {
  const t = useT();
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
            <th>{t('admin.agents')}</th>
            <th>{t('admin.team')}</th>
            <th>{t('admin.status')}</th>
            <th>{t('admin.actions')}</th>
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
                  <ConfirmButton label={t('admin.kill')} danger onConfirm={() => void kill(id)} />
                </td>
              </tr>
            );
          })}
          {agents.length === 0 && (
            <tr>
              <td colSpan={4} className="muted">
                {t('admin.noAgents')}
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
  const t = useT();
  const [tab, setTab] = useState<'team' | 'hosts' | 'agents'>('team');
  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="admin" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <button className={tab === 'team' ? 'tab active' : 'tab'} onClick={() => setTab('team')}>
            {t('admin.team')}
          </button>
          <button className={tab === 'hosts' ? 'tab active' : 'tab'} onClick={() => setTab('hosts')}>
            {t('admin.hosts')}
          </button>
          <button className={tab === 'agents' ? 'tab active' : 'tab'} onClick={() => setTab('agents')}>
            {t('admin.agents')}
          </button>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
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
