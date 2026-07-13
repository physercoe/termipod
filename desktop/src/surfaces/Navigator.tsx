import { useState } from 'react';
import { useAgents, useHosts, useProjects } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { AgentSpawn } from './AgentSpawn';
import { ProjectCreate } from './ProjectCreate';

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
/// Projects · Stewards · Agents · Hosts. Selection drives the Focus region
/// (agent transcript / project board).
export function Navigator(): JSX.Element {
  const t = useT();
  const agentsQ = useAgents();
  const hostsQ = useHosts();
  const projectsQ = useProjects();
  const selection = useFocus((s) => s.selection);
  const selectAgent = useFocus((s) => s.selectAgent);
  const selectProject = useFocus((s) => s.selectProject);
  const selectHost = useFocus((s) => s.selectHost);
  const connected = useSession((s) => s.client) !== null;
  const [creating, setCreating] = useState(false);
  const [spawning, setSpawning] = useState(false);
  const [open, setOpen] = useState({ projects: true, stewards: true, agents: true, hosts: true });
  const toggle = (k: keyof typeof open): void => setOpen((o) => ({ ...o, [k]: !o[k] }));

  const agents = agentsQ.data ?? [];
  const hosts = hostsQ.data ?? [];
  const projects = projectsQ.data ?? [];

  const stewards = agents.filter(isSteward);
  const workers = agents.filter((a) => !isSteward(a));

  const hostLabel = (id: string): string => {
    const h = hosts.find((x) => str(x, 'id') === id);
    return (h && (str(h, 'name') ?? str(h, 'hostname'))) ?? (id || t('nav.unassigned'));
  };

  const agentSelected = (id: string): boolean =>
    selection?.type === 'agent' && selection.id === id;
  const projectSelected = (id: string): boolean =>
    selection?.type === 'project' && selection.id === id;
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
        className={`tree-agent${agentSelected(id) ? ' selected' : ''}`}
        onClick={() => selectAgent(id)}
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
      {/* Projects — the units of directed work. */}
      <KindSection
        title={t('nav.projects')}
        count={projects.length}
        open={open.projects}
        onToggle={() => toggle('projects')}
        onAdd={connected ? () => setCreating(true) : undefined}
        addTitle={t('project.new')}
      >
        {projects.length === 0
          ? loadingOrEmpty(projectsQ.isLoading, t('nav.noProjects'))
          : projects.map((p) => {
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
                  {str(p, 'phase') !== undefined && (
                    <span className="tree-agent-kind">{str(p, 'phase')}</span>
                  )}
                </div>
              );
            })}
      </KindSection>

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
        onAdd={connected ? () => setSpawning(true) : undefined}
        addTitle={t('spawn.title')}
      >
        {!agentsQ.isError &&
          (workers.length === 0
            ? loadingOrEmpty(agentsQ.isLoading, t('nav.noAgents'))
            : workers.map((a) => agentRow(a, { showHost: true })))}
      </KindSection>

      {/* Hosts — the machines the fleet runs on. */}
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
                  className={`tree-agent tree-host-row${hostSelected(id) ? ' selected' : ''}`}
                  title={str(h, 'hostname') ?? label}
                  onClick={() => selectHost(id)}
                >
                  <span className={`dot ${statusClass(str(h, 'status'))}`} />
                  <span className="tree-agent-label">{label}</span>
                  <span className="tree-agent-kind">{t('nav.hostAgents').replace('{n}', String(count))}</span>
                </div>
              );
            })}
      </KindSection>

      {creating && <ProjectCreate onClose={() => setCreating(false)} />}
      {spawning && <AgentSpawn onClose={() => setSpawning(false)} />}
    </div>
  );
}
