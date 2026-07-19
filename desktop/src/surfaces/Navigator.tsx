import { useState } from 'react';
import { useAgents, useHosts } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { activateOnKey } from '../ui/a11y';
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

/// A steward is a coordinating agent — its `kind` starts with `steward.`
/// (blueprint / CLAUDE.md domain model). Everything else is a worker agent.
function isSteward(e: Entity): boolean {
  return (str(e, 'kind') ?? '').startsWith('steward.');
}

/// A collapsible, kind-scoped section header. One per entity kind so the tree
/// never intermixes projects, stewards, agents, and hosts.
function KindSection(props: {
  title: string;
  count: number;
  open: boolean;
  onToggle: () => void;
  onAdd?: () => void;
  addTitle?: string;
  children: React.ReactNode;
}): JSX.Element {
  const { title, count, open, onToggle, onAdd, addTitle, children } = props;
  return (
    <div className="tree-kind">
      <div className="tree-section tree-section-row">
        <button className="tree-kind-toggle" onClick={onToggle} aria-expanded={open}>
          <span className={`tree-caret${open ? ' open' : ''}`} aria-hidden>
            <Icon name="chevron-right" size={13} />
          </span>
          <span>{title}</span>
          <span className="tree-count">{count}</span>
        </button>
        {onAdd && (
          <button className="tree-add" title={addTitle} onClick={onAdd}>
            +
          </button>
        )}
      </div>
      {open && children}
    </div>
  );
}

/// Left region — the persistent fleet tree, one section per entity kind:
/// Stewards · Agents · Hosts (the ops roster). Projects moved to their own tab
/// (`ProjectsSurface`); the fleet is now hosts + agents + attention, mirroring
/// the mobile Me/Hosts view. Selection drives the shared Focus region.
export function Navigator(): JSX.Element {
  const t = useT();
  const agentsQ = useAgents();
  const hostsQ = useHosts();
  const selection = useFocus((s) => s.fleet.selection);
  const selectAgent = useFocus((s) => s.selectAgent);
  const selectHost = useFocus((s) => s.selectHost);
  const [open, setOpen] = useState({ stewards: true, agents: true, hosts: true });
  const toggle = (k: keyof typeof open): void => setOpen((o) => ({ ...o, [k]: !o[k] }));
  // Agents and Hosts are separate subtabs so the roster isn't one long mixed
  // tree (director request). The Agents subtab groups stewards + workers; Hosts
  // is the machines. Selection persists across launches.
  const [sub, setSub] = useState<'agents' | 'hosts'>(() => {
    try {
      return localStorage.getItem('termipod.fleet.navSub') === 'hosts' ? 'hosts' : 'agents';
    } catch {
      return 'agents';
    }
  });
  const pickSub = (s: 'agents' | 'hosts'): void => {
    setSub(s);
    try {
      localStorage.setItem('termipod.fleet.navSub', s);
    } catch {
      /* ignore */
    }
  };

  const agents = agentsQ.data ?? [];
  const hosts = hostsQ.data ?? [];

  const stewards = agents.filter(isSteward);
  const workers = agents.filter((a) => !isSteward(a));

  const hostLabel = (id: string): string => {
    const h = hosts.find((x) => str(x, 'id') === id);
    return (h && (str(h, 'name') ?? str(h, 'hostname'))) ?? (id || t('nav.unassigned'));
  };

  const agentSelected = (id: string): boolean =>
    selection?.type === 'agent' && selection.id === id;
  const hostSelected = (id: string): boolean =>
    selection?.type === 'host' && selection.id === id;

  // One agent row — engine kind on the left dot, host affiliation as a trailing
  // chip so the host is legible without turning hosts into grouping headers.
  const agentRow = (a: Entity, opts?: { showHost?: boolean }): JSX.Element => {
    const id = str(a, 'id') ?? '';
    const kind = str(a, 'kind') ?? '';
    const label = str(a, 'handle') ?? str(a, 'name') ?? id;
    const hid = str(a, 'host_id') ?? '';
    return (
      <div
        key={id}
        role="button"
        tabIndex={0}
        aria-pressed={agentSelected(id)}
        className={`tree-agent${agentSelected(id) ? ' selected' : ''}`}
        onClick={() => selectAgent('fleet', id)}
        onKeyDown={activateOnKey(() => selectAgent('fleet', id))}
        title={kind}
      >
        <span className={`dot ${statusClass(str(a, 'status'))}`} />
        <span className="tree-agent-label">{label}</span>
        {opts?.showHost && hid && <span className="tree-agent-host">{hostLabel(hid)}</span>}
        <span className="tree-agent-kind">{kind.replace(/^steward\./, '★')}</span>
      </div>
    );
  };

  const loadingOrEmpty = (loading: boolean, empty: string): JSX.Element => (
    <div className="region-pad muted">{loading ? t('common.loading') : empty}</div>
  );

  return (
    <div className="tree">
      <div className="nav-subtabs seg">
        <button className={sub === 'agents' ? 'seg-btn active' : 'seg-btn'} onClick={() => pickSub('agents')}>
          {t('nav.agents')}
          <span className="nav-subtab-count">{stewards.length + workers.length}</span>
        </button>
        <button className={sub === 'hosts' ? 'seg-btn active' : 'seg-btn'} onClick={() => pickSub('hosts')}>
          {t('nav.hosts')}
          <span className="nav-subtab-count">{hosts.length}</span>
        </button>
      </div>

      {sub === 'agents' ? (
        <>
          {/* Stewards — coordinating agents (kind steward.*). */}
          <KindSection
            title={t('nav.stewards')}
            count={stewards.length}
            open={open.stewards}
            onToggle={() => toggle('stewards')}
          >
            {agentsQ.isError && (
              <div className="region-pad error">{(agentsQ.error as Error).message}</div>
            )}
            {!agentsQ.isError &&
              (stewards.length === 0
                ? loadingOrEmpty(agentsQ.isLoading, t('nav.noStewards'))
                : stewards.map((a) => agentRow(a, { showHost: true })))}
          </KindSection>

          {/* Agents — the worker executors (non-steward). */}
          <KindSection
            title={t('nav.agents')}
            count={workers.length}
            open={open.agents}
            onToggle={() => toggle('agents')}
          >
            {!agentsQ.isError &&
              (workers.length === 0
                ? loadingOrEmpty(agentsQ.isLoading, t('nav.noAgents'))
                : workers.map((a) => agentRow(a, { showHost: true })))}
          </KindSection>
        </>
      ) : (
        /* Hosts — the machines the fleet runs on. */
        <KindSection
          title={t('nav.hosts')}
          count={hosts.length}
          open={open.hosts}
          onToggle={() => toggle('hosts')}
        >
          {hosts.length === 0
            ? loadingOrEmpty(hostsQ.isLoading, t('nav.noHosts'))
            : hosts.map((h) => {
                const id = str(h, 'id') ?? '';
                const label = str(h, 'name') ?? str(h, 'hostname') ?? id;
                const count = agents.filter((a) => (str(a, 'host_id') ?? '') === id).length;
                return (
                  <div
                    key={id}
                    role="button"
                    tabIndex={0}
                    aria-pressed={hostSelected(id)}
                    className={`tree-agent tree-host-row${hostSelected(id) ? ' selected' : ''}`}
                    title={str(h, 'hostname') ?? label}
                    onClick={() => selectHost('fleet', id)}
                    onKeyDown={activateOnKey(() => selectHost('fleet', id))}
                  >
                    <span className={`dot ${statusClass(str(h, 'status'))}`} />
                    <span className="tree-agent-label">{label}</span>
                    <span className="tree-agent-kind">{t('nav.hostAgents').replace('{n}', String(count))}</span>
                  </div>
                );
              })}
        </KindSection>
      )}
    </div>
  );
}
