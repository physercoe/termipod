import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { TaskDetail } from './TaskDetail';

const COLUMNS = ['todo', 'in_progress', 'blocked', 'done', 'cancelled'];
const PRIORITIES = ['low', 'med', 'high', 'urgent'];
type Tab = 'overview' | 'tasks' | 'runs' | 'plans';

function NewTaskForm({ projectId, onDone }: { projectId: string; onDone: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [status, setStatus] = useState('todo');
  const [priority, setPriority] = useState('med');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(): Promise<void> {
    if (client === null || title.trim() === '') return;
    setBusy(true);
    setErr(null);
    try {
      await client.createTask(projectId, { title: title.trim(), body_md: body, status, priority });
      await qc.invalidateQueries({ queryKey: ['tasks', projectId] });
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={onDone}>
      <div className="task-detail" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('task.new')}</strong>
          <span className="spacer" />
          <button onClick={onDone}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <label className="wide">
            {t('task.title')}
            <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
          </label>
          <label className="wide">
            {t('task.body')}
            <textarea value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          <label>
            {t('task.status')}
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              {COLUMNS.map((s) => (
                <option key={s} value={s}>
                  {t(`kanban.${s}`)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('task.priority')}
            <select value={priority} onChange={(e) => setPriority(e.target.value)}>
              {PRIORITIES.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </label>
          {err !== null && <div className="error wide">{err}</div>}
          <div className="wide task-form-actions">
            <button className="primary" disabled={busy || title.trim() === ''} onClick={() => void submit()}>
              {t('task.create')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function OverviewTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['project-overview', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.getProjectOverview(projectId),
  });

  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;

  const ov = q.data ?? {};
  const phases = Array.isArray(ov['phases']) ? (ov['phases'] as string[]) : [];
  const phase = str(ov, 'phase') ?? '';
  const phaseIndex = num(ov, 'phase_index') ?? -1;
  const deliverables = Array.isArray(ov['deliverables']) ? (ov['deliverables'] as Entity[]) : [];
  const counts = (ov['counts'] as Entity | undefined) ?? {};

  return (
    <div className="region-pad proj-overview">
      <section className="setting-group">
        <h3>{t('proj.phase')}</h3>
        <div className="phase-track">
          {phases.map((p, i) => (
            <span
              key={p}
              className={`phase-pip${i === phaseIndex ? ' active' : ''}${i < phaseIndex ? ' done' : ''}`}
              title={p}
            >
              {p}
            </span>
          ))}
          {phases.length === 0 && phase !== '' && <span className="phase-pip active">{phase}</span>}
        </div>
      </section>

      <section className="setting-group">
        <h3>
          {t('proj.deliverables')}{' '}
          <span className="pill">
            {num(counts, 'deliverables_ratified') ?? 0}/{num(counts, 'deliverables_total') ?? deliverables.length}{' '}
            {t('proj.ratified')}
          </span>
        </h3>
        {deliverables.length === 0 ? (
          <div className="muted">{t('proj.noDeliverables')}</div>
        ) : (
          deliverables.map((d) => (
            <div key={str(d, 'id')} className="admin-row">
              <span>{str(d, 'kind') ?? str(d, 'id')}</span>
              <span className={`sev${str(d, 'ratification_state') === 'ratified' ? ' sev-medium' : ''}`}>
                {str(d, 'ratification_state') ?? '—'}
              </span>
            </div>
          ))
        )}
      </section>
    </div>
  );
}

function RunsTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['runs', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listRuns(projectId),
  });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const runs = q.data ?? [];
  if (runs.length === 0) return <div className="region-pad muted">{t('proj.noRuns')}</div>;
  return (
    <table>
      <thead>
        <tr>
          <th>{t('admin.status')}</th>
          <th>Run</th>
          <th>{t('admin.agents')}</th>
          <th>{t('admin.created')}</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r, i) => (
          <tr key={str(r, 'id') ?? String(i)}>
            <td>{str(r, 'status') ?? ''}</td>
            <td className="mono">{str(r, 'id')}</td>
            <td>{str(r, 'agent_id') ?? '—'}</td>
            <td>{str(r, 'created_at') ?? str(r, 'started_at') ?? ''}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PlansTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['plans', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listPlans(projectId),
  });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const plans = q.data ?? [];
  if (plans.length === 0) return <div className="region-pad muted">{t('proj.noPlans')}</div>;
  return (
    <table>
      <thead>
        <tr>
          <th>{t('admin.status')}</th>
          <th>Plan</th>
          <th>{t('proj.version')}</th>
          <th>{t('admin.created')}</th>
        </tr>
      </thead>
      <tbody>
        {plans.map((p, i) => (
          <tr key={str(p, 'id') ?? String(i)}>
            <td>{str(p, 'status') ?? ''}</td>
            <td className="mono">{str(p, 'id')}</td>
            <td>{num(p, 'version') ?? ''}</td>
            <td>{str(p, 'created_at') ?? ''}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TasksTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [open, setOpen] = useState<Entity | null>(null);
  const [creating, setCreating] = useState(false);
  const tasksQ = useQuery({
    queryKey: ['tasks', projectId],
    enabled: client !== null,
    refetchInterval: 8000,
    queryFn: () => client!.listTasks(projectId),
  });

  if (tasksQ.isLoading) return <div className="region-pad muted">{t('kanban.loading')}</div>;
  if (tasksQ.isError) return <div className="region-pad error">{(tasksQ.error as Error).message}</div>;

  const tasks = tasksQ.data ?? [];
  const inColumn = (status: string): Entity[] => tasks.filter((task) => (str(task, 'status') ?? 'todo') === status);

  return (
    <>
      <div className="kanban-bar">
        <span className="spacer" />
        <button onClick={() => setCreating(true)}>+ {t('task.new')}</button>
      </div>
      <div className="kanban">
        {COLUMNS.map((status) => {
          const items = inColumn(status);
          return (
            <div key={status} className="kanban-col">
              <div className="kanban-head">
                {t(`kanban.${status}`)} <span className="pill">{items.length}</span>
              </div>
              {items.map((task) => (
                <div
                  key={str(task, 'id')}
                  className="kanban-card"
                  role="button"
                  onClick={() => setOpen(task)}
                >
                  <div className="kanban-card-title">
                    {str(task, 'title') ?? str(task, 'summary') ?? str(task, 'id')}
                  </div>
                  {str(task, 'assignee_handle') !== undefined && (
                    <div className="kanban-card-meta">{str(task, 'assignee_handle')}</div>
                  )}
                </div>
              ))}
              {items.length === 0 && <div className="kanban-empty">—</div>}
            </div>
          );
        })}
      </div>
      {open !== null && <TaskDetail projectId={projectId} task={open} onClose={() => setOpen(null)} />}
      {creating && <NewTaskForm projectId={projectId} onDone={() => setCreating(false)} />}
    </>
  );
}

/// Focus region for a selected project (WS6, deepened): tabbed Overview /
/// Tasks (kanban + status change) / Runs / Plans. The tasks kanban uses the
/// ADR-029 statuses; task cards open a detail modal that patches status.
export function ProjectBoard({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const [tab, setTab] = useState<Tab>('overview');
  const tabs: { v: Tab; label: string }[] = [
    { v: 'overview', label: t('proj.overview') },
    { v: 'tasks', label: t('proj.tasks') },
    { v: 'runs', label: t('proj.runs') },
    { v: 'plans', label: t('proj.plans') },
  ];
  return (
    <div className="transcript">
      <div className="transcript-bar">
        <div className="tabs">
          {tabs.map((x) => (
            <button key={x.v} className={tab === x.v ? 'tab active' : 'tab'} onClick={() => setTab(x.v)}>
              {x.label}
            </button>
          ))}
        </div>
      </div>
      <div className="scroll">
        {tab === 'overview' && <OverviewTab projectId={projectId} />}
        {tab === 'tasks' && <TasksTab projectId={projectId} />}
        {tab === 'runs' && <RunsTab projectId={projectId} />}
        {tab === 'plans' && <PlansTab projectId={projectId} />}
      </div>
    </div>
  );
}
