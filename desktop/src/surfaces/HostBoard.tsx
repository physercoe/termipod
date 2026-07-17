import { useAgents, useHosts } from '../hub/queries';
import { num, obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';

/// Focus-region detail for a single host (the machine a host-runner is on).
/// There's no single-host endpoint, so this reads the host out of the cached
/// team `listHosts` result and cross-references the cached fleet for the agents
/// running on it — no extra request, updates as those polls refresh.

function statusClass(status: string | undefined): string {
  switch (status) {
    case 'running':
    case 'online':
    case 'ready':
      return 'running';
    case 'stale':
    case 'degraded':
      return 'paused';
    case 'offline':
    case 'down':
      return 'stopped';
    default:
      return 'muted';
  }
}

function agentStatusClass(status: string | undefined): string {
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

function fmtBytes(n: number | undefined): string | undefined {
  if (n === undefined || n <= 0) return undefined;
  const gib = n / 1024 ** 3;
  if (gib >= 1) return `${gib.toFixed(gib >= 10 ? 0 : 1)} GiB`;
  return `${Math.round(n / 1024 ** 2)} MiB`;
}

// Best-effort relative age from an ISO timestamp; falls back to the raw string.
function ago(iso: string | undefined, t: (k: string) => string): string | undefined {
  if (iso === undefined || iso === '') return undefined;
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return t('host.justNow');
  if (s < 3600) return t('host.minsAgo').replace('{n}', String(Math.floor(s / 60)));
  if (s < 86400) return t('host.hoursAgo').replace('{n}', String(Math.floor(s / 3600)));
  return t('host.daysAgo').replace('{n}', String(Math.floor(s / 86400)));
}

function Field({ label, value }: { label: string; value: string }): JSX.Element {
  return (
    <div className="host-field">
      <div className="host-field-label muted small">{label}</div>
      <div className="host-field-value">{value}</div>
    </div>
  );
}

export function HostBoard({ hostId }: { hostId: string }): JSX.Element {
  const t = useT();
  const hostsQ = useHosts();
  const agentsQ = useAgents();
  const selectAgent = useFocus((s) => s.selectAgent);

  const host = (hostsQ.data ?? []).find((h) => str(h, 'id') === hostId);
  if (host === undefined) {
    return (
      <div className="region-pad muted">
        {hostsQ.isLoading ? t('common.loading') : t('host.gone')}
      </div>
    );
  }

  const name = str(host, 'name') ?? str(host, 'hostname') ?? hostId;
  const status = str(host, 'status');
  const agents = (agentsQ.data ?? []).filter((a) => (str(a, 'host_id') ?? '') === hostId);

  // capabilities.host — static box facts; capabilities.agents — installed engines.
  const caps = obj(host, 'capabilities');
  const hostInfo = caps !== undefined ? obj(caps, 'host') : undefined;
  const engines = caps !== undefined ? obj(caps, 'agents') : undefined;
  const installed =
    engines !== undefined
      ? Object.entries(engines)
          .filter(([, v]) => v !== null && typeof v === 'object' && (v as Entity).installed === true)
          .map(([k, v]) => {
            const ver = str(v as Entity, 'version');
            return ver !== undefined ? `${k} ${ver}` : k;
          })
      : [];

  // ssh_hint_json — a JSON string holding hostname/port/username (non-secret).
  let ssh: Entity | undefined;
  const rawHint = str(host, 'ssh_hint_json');
  if (rawHint !== undefined && rawHint !== '') {
    try {
      const p = JSON.parse(rawHint) as unknown;
      if (p !== null && typeof p === 'object') ssh = p as Entity;
    } catch {
      /* malformed hint — skip */
    }
  }

  const facts: { label: string; value: string }[] = [];
  const push = (label: string, value: string | undefined): void => {
    if (value !== undefined && value !== '') facts.push({ label, value });
  };
  push(t('host.id'), hostId);
  if (hostInfo !== undefined) {
    const osArch = [str(hostInfo, 'os'), str(hostInfo, 'arch')].filter(Boolean).join(' / ');
    push(t('host.os'), osArch);
    const cpu = num(hostInfo, 'cpu_count');
    push(t('host.cpu'), cpu !== undefined ? `${cpu} cores` : undefined);
    push(t('host.mem'), fmtBytes(num(hostInfo, 'mem_bytes')));
    push(t('host.kernel'), str(hostInfo, 'kernel'));
  }
  push(t('host.lastSeen'), ago(str(host, 'last_seen_at'), t));
  push(t('host.created'), ago(str(host, 'created_at'), t));
  if (ssh !== undefined) {
    push(t('host.sshHost'), str(ssh, 'hostname'));
    const port = num(ssh, 'port');
    push(t('host.sshPort'), port !== undefined ? String(port) : undefined);
    push(t('host.sshUser'), str(ssh, 'username'));
  }
  const commit = str(host, 'runner_commit');
  if (commit !== undefined) {
    const modified = host['runner_modified'] === true ? ' (modified)' : '';
    push(t('host.runner'), `${commit.slice(0, 10)}${modified}`);
  }
  push(t('host.runnerBuilt'), ago(str(host, 'runner_build_time'), t));

  return (
    <div className="host-board scroll">
      <div className="host-board-head">
        <span className={`dot ${statusClass(status)}`} />
        <h2 className="host-board-name">{name}</h2>
        <span className={`pill ${statusClass(status)} small`}>{status ?? t('host.unknown')}</span>
      </div>

      <div className="host-facts">
        {facts.map((f) => (
          <Field key={f.label} label={f.label} value={f.value} />
        ))}
      </div>

      {installed.length > 0 && (
        <section className="host-section">
          <div className="host-section-label">{t('host.engines')}</div>
          <div className="host-chips">
            {installed.map((e) => (
              <span key={e} className="host-chip">
                {e}
              </span>
            ))}
          </div>
        </section>
      )}

      <section className="host-section">
        <div className="host-section-label">
          {t('host.agentsHere')} <span className="muted">({agents.length})</span>
        </div>
        {agents.length === 0 ? (
          <div className="muted small region-pad">{t('host.noAgentsHere')}</div>
        ) : (
          <div className="host-agent-list">
            {agents.map((a) => {
              const id = str(a, 'id') ?? '';
              const label = str(a, 'handle') ?? str(a, 'name') ?? id;
              const kind = str(a, 'kind') ?? '';
              return (
                <button key={id} className="host-agent-row" onClick={() => selectAgent('fleet', id)}>
                  <span className={`dot ${agentStatusClass(str(a, 'status'))}`} />
                  <span className="host-agent-label">{label}</span>
                  <span className="host-agent-kind">{kind.replace(/^steward\./, '★')}</span>
                </button>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
