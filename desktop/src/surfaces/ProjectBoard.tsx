import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';
import { ResizeHandle, usePanelWidth } from '../ui/ResizeHandle';
import { AgentTranscript } from './AgentTranscript';
import { ActivityTab, CriteriaTab, DeliverableDetail, DocumentsTab, FilesTab } from './ProjectPanels';
import { ProjectHero } from './ProjectHero';
import { PhaseSummary } from './PhaseSummary';
import { PlanDetail } from './PlanDetail';
import { RunDetail } from './RunDetail';
import { TaskDetail, TaskDetailBody, firstLine, pipClass, relTime } from './TaskDetail';

const COLUMNS = ['todo', 'in_progress', 'blocked', 'done', 'cancelled'];
const PRIORITIES = ['low', 'med', 'high', 'urgent'];
type Tab = 'overview' | 'agents' | 'tasks' | 'runs' | 'plans' | 'criteria' | 'documents' | 'files' | 'activity';

function AgentsTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const selectAgent = useFocus((s) => s.selectAgent);
  const q = useQuery({
    queryKey: ['project-agents', projectId],
    enabled: client !== null,
    refetchInterval: 8000,
    // include_terminated so stopped/historical workers show too (mobile parity).
    queryFn: () => client!.listAgents({ project_id: projectId, include_terminated: true }),
  });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const agents = q.data ?? [];
  if (agents.length === 0) return <div className="region-pad muted">{t('proj.noAgents')}</div>;
  return (
    <table>
      <thead>
        <tr>
          <th>{t('admin.status')}</th>
          <th>{t('proj.agentHandle')}</th>
          <th>{t('proj.agentKind')}</th>
          <th>{t('proj.agentActivity')}</th>
        </tr>
      </thead>
      <tbody>
        {agents.map((a, i) => {
          const id = str(a, 'id') ?? String(i);
          return (
            <tr
              key={id}
              role="button"
              className="clickable-row"
              onClick={() => selectAgent('projects', id, str(a, 'handle') ?? undefined)}
            >
              <td>{str(a, 'status') ?? ''}</td>
              <td>{str(a, 'handle') ?? id}</td>
              <td className="muted">{str(a, 'kind') ?? ''}</td>
              <td className="muted small">{str(a, 'last_event_at') ?? str(a, 'created_at') ?? ''}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

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
    <Modal onClose={onDone} className="task-detail" ariaLabel={t('task.new')}>
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
    </Modal>
  );
}

/// Bind a domain steward to a project (the mobile "settings" step). Sets
/// `on_create_template_id` via PATCH so the project becomes startable, instead
/// of the bare Start button 422-ing. A free-text template id (mirrors the
/// mobile edit sheet) with a datalist of the team's steward agent templates.
function StewardBind({
  projectId,
  current,
  onClose,
}: {
  projectId: string;
  current: string;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [tpl, setTpl] = useState(current);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const suggestQ = useQuery({
    queryKey: ['agent-templates'],
    enabled: client !== null,
    queryFn: () => client!.listTemplates('agents'),
  });
  const suggestions = (suggestQ.data ?? [])
    .map((e) => `${str(e, 'category') ?? 'agents'}/${str(e, 'name') ?? ''}`)
    .filter((s) => /steward/i.test(s));

  async function save(): Promise<void> {
    if (client === null || tpl.trim() === '') return;
    setBusy(true);
    setErr(null);
    try {
      await client.updateProject(projectId, { on_create_template_id: tpl.trim() });
      await qc.invalidateQueries({ queryKey: ['project', projectId] });
      await qc.invalidateQueries({ queryKey: ['project-overview', projectId] });
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={t('project.bindStewardTitle')}>
        <div className="admin-tabs">
          <strong>{t('project.bindStewardTitle')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <p className="muted small wide">{t('project.bindStewardDesc')}</p>
          <label className="wide">
            {t('project.stewardTemplate')}
            <input
              list="steward-tpl-suggest"
              value={tpl}
              spellCheck={false}
              onChange={(e) => setTpl(e.target.value)}
              placeholder={t('project.bindStewardHint')}
              autoFocus
            />
            <datalist id="steward-tpl-suggest">
              {suggestions.map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
          </label>
          {err !== null && <div className="error wide">{err}</div>}
          <div className="wide task-form-actions">
            <button className="primary" disabled={busy || tpl.trim() === ''} onClick={() => void save()}>
              {t('project.bind')}
            </button>
          </div>
        </div>
    </Modal>
  );
}

function OverviewTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const [openDeliv, setOpenDeliv] = useState<string | null>(null);
  const [phaseSummary, setPhaseSummary] = useState<string | null>(null);
  const [binding, setBinding] = useState(false);
  const q = useQuery({
    queryKey: ['project-overview', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.getProjectOverview(projectId),
  });
  const projQ = useQuery({
    queryKey: ['project', projectId],
    enabled: client !== null,
    queryFn: () => client!.getProject(projectId),
  });
  const tasksQ = useQuery({
    queryKey: ['tasks', projectId],
    enabled: client !== null,
    queryFn: () => client!.listTasks(projectId),
  });

  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;

  const ov = q.data ?? {};
  const proj = projQ.data ?? {};
  const started = ov['steward_started'] === true;
  // A project can only start once a domain steward is bound
  // (`on_create_template_id`); otherwise the hub 422s. Mirror mobile: offer to
  // bind one instead of showing a Start button that fails.
  const bound = (str(proj, 'on_create_template_id') ?? '') !== '';
  const phases = Array.isArray(ov['phases']) ? (ov['phases'] as string[]) : [];
  const phase = str(ov, 'phase') ?? '';
  const phaseIndex = num(ov, 'phase_index') ?? -1;
  const deliverables = Array.isArray(ov['deliverables']) ? (ov['deliverables'] as Entity[]) : [];
  const counts = (ov['counts'] as Entity | undefined) ?? {};

  const goal = str(proj, 'goal') ?? str(ov, 'goal');
  const budgetCents = num(proj, 'budget_cents');
  const heroKind = str(proj, 'overview_widget') ?? '';
  const allTasks = tasksQ.data ?? [];
  const closedTasks = allTasks.filter((tk) => {
    const s = str(tk, 'status');
    return s === 'done' || s === 'cancelled';
  }).length;

  return (
    <div className="region-pad proj-overview">
      <section className="setting-group">
        <div className="setting-row">
          <span className={started ? 'sev sev-medium' : 'muted'}>
            {started ? t('project.started') : bound ? t('project.start') : t('project.noSteward')}
          </span>
          {!started &&
            (bound ? (
              <button
                className="primary"
                disabled={busy}
                onClick={() => void run(() => client!.startProject(projectId), { invalidate: [['project-overview', projectId], ['agents']] })}
              >
                {busy ? t('project.starting') : t('project.start')}
              </button>
            ) : (
              <button className="primary" onClick={() => setBinding(true)}>
                {t('project.bindSteward')}
              </button>
            ))}
        </div>
        {!started && !bound && <p className="muted small proj-nosteward-hint">{t('project.noStewardHint')}</p>}
        {error !== null && <div className="error">{error}</div>}
      </section>

      {(goal !== undefined || allTasks.length > 0 || budgetCents !== undefined) && (
        <section className="setting-group proj-hero">
          {goal !== undefined && goal !== '' && <p className="proj-goal">{goal}</p>}
          <div className="proj-hero-stats">
            {allTasks.length > 0 && (
              <span className="proj-hero-stat">
                <span className="proj-hero-num">
                  {closedTasks}/{allTasks.length}
                </span>{' '}
                {t('proj.tasksDone')}
              </span>
            )}
            {budgetCents !== undefined && budgetCents > 0 && (
              <span className="proj-hero-stat">
                <span className="proj-hero-num">${(budgetCents / 100).toFixed(2)}</span> {t('proj.budget')}
              </span>
            )}
          </div>
          {allTasks.length > 0 && (
            <div className="proj-progress">
              <div className="proj-progress-fill" style={{ width: `${Math.round((closedTasks / allTasks.length) * 100)}%` }} />
            </div>
          )}
        </section>
      )}

      {heroKind !== '' && <ProjectHero projectId={projectId} kind={heroKind} />}

      <section className="setting-group">
        <h3>{t('proj.phase')}</h3>
        <div className="phase-track">
          {phases.map((p, i) => (
            <button
              key={p}
              className={`phase-pip${i === phaseIndex ? ' active' : ''}${i < phaseIndex ? ' done' : ''}`}
              title={t('phase.openHint')}
              onClick={() => setPhaseSummary(p)}
            >
              {p}
            </button>
          ))}
          {phases.length === 0 && phase !== '' && (
            <button className="phase-pip active" title={t('phase.openHint')} onClick={() => setPhaseSummary(phase)}>
              {phase}
            </button>
          )}
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
          deliverables.map((d) => {
            const did = str(d, 'id') ?? '';
            const state = str(d, 'ratification_state') ?? '—';
            const ratified = state === 'ratified';
            return (
              <div key={did} className="admin-row">
                <button className="deliv-open" onClick={() => setOpenDeliv(did)} title={t('deliv.openHint')}>
                  {str(d, 'kind') ?? did}
                </button>
                <span className="spacer" />
                <span className={`sev${ratified ? ' sev-medium' : ''}`}>{state}</span>
                {ratified ? (
                  <button
                    disabled={busy}
                    title={t('deliv.unratifyHint')}
                    onClick={() =>
                      void run(() => client!.unratifyDeliverable(projectId, did), {
                        invalidate: [['project-overview', projectId]],
                      })
                    }
                  >
                    {t('deliv.unratify')}
                  </button>
                ) : (
                  <button
                    className="primary"
                    disabled={busy}
                    onClick={() =>
                      void run(() => client!.ratifyDeliverable(projectId, did), {
                        invalidate: [['project-overview', projectId]],
                      })
                    }
                  >
                    {t('deliv.ratify')}
                  </button>
                )}
              </div>
            );
          })
        )}
      </section>
      {binding && (
        <StewardBind
          projectId={projectId}
          current={str(proj, 'on_create_template_id') ?? ''}
          onClose={() => setBinding(false)}
        />
      )}
      {openDeliv !== null && (
        <DeliverableDetail projectId={projectId} deliverableId={openDeliv} onClose={() => setOpenDeliv(null)} />
      )}
      {phaseSummary !== null && (
        <PhaseSummary
          projectId={projectId}
          phase={phaseSummary}
          isCurrent={phaseSummary === phase}
          onOpenDeliverable={(id) => {
            setPhaseSummary(null);
            setOpenDeliv(id);
          }}
          onClose={() => setPhaseSummary(null)}
        />
      )}
    </div>
  );
}

function RunsTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run: act, busy, error } = useHubAction();
  const [open, setOpen] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['runs', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listRuns(projectId),
  });
  const launch = (): void =>
    void act(() => client!.createRun({ project_id: projectId }), { invalidate: [['runs', projectId]] });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const runs = q.data ?? [];
  return (
    <>
      <div className="kanban-bar">
        {error !== null && <span className="error small">{error}</span>}
        <span className="spacer" />
        <button disabled={busy} onClick={launch}>
          + {t('proj.launchRun')}
        </button>
      </div>
      {runs.length === 0 ? (
        <div className="region-pad muted">{t('proj.noRuns')}</div>
      ) : (
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
        {runs.map((r, i) => {
          const id = str(r, 'id') ?? '';
          return (
            <tr key={id || String(i)} role="button" className="clickable-row" onClick={() => id && setOpen(id)}>
              <td>{str(r, 'status') ?? ''}</td>
              <td className="mono">{id}</td>
              <td>{str(r, 'agent_id') ?? '—'}</td>
              <td>{str(r, 'created_at') ?? str(r, 'started_at') ?? ''}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
      )}
      {open !== null && <RunDetail runId={open} onClose={() => setOpen(null)} />}
    </>
  );
}

function PlansTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run: act, busy, error } = useHubAction();
  const [open, setOpen] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['plans', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listPlans(projectId),
  });
  const create = (): void =>
    void act(() => client!.createPlan({ project_id: projectId, spec_json: {} }), { invalidate: [['plans', projectId]] });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const plans = q.data ?? [];
  return (
    <>
      <div className="kanban-bar">
        {error !== null && <span className="error small">{error}</span>}
        <span className="spacer" />
        <button disabled={busy} onClick={create}>
          + {t('proj.newPlan')}
        </button>
      </div>
      {plans.length === 0 ? (
        <div className="region-pad muted">{t('proj.noPlans')}</div>
      ) : (
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
        {plans.map((p, i) => {
          const id = str(p, 'id') ?? '';
          return (
            <tr key={id || String(i)} role="button" className="clickable-row" onClick={() => id && setOpen(id)}>
              <td>{str(p, 'status') ?? ''}</td>
              <td className="mono">{id}</td>
              <td>{num(p, 'version') ?? ''}</td>
              <td>{str(p, 'created_at') ?? ''}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
      )}
      {open !== null && <PlanDetail planId={open} onClose={() => setOpen(null)} />}
    </>
  );
}

/// True while the viewport is at least `px` wide; drives the master-detail
/// breakpoint (board + inline panel ≥1100px, modal below). matchMedia is live
/// in the Electron renderer, so this reflows on window resize.
function useMinWidth(px: number): boolean {
  const [wide, setWide] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(`(min-width:${px}px)`).matches : false,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(min-width:${px}px)`);
    const on = (): void => setWide(mq.matches);
    on();
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, [px]);
  return wide;
}

/// One task card — the mobile tile's content at desktop density: priority dot,
/// title, one-line body snippet, result summary when present, live assignee
/// pip + handle, relative age. Cancelled reads struck-through; blocked is
/// flagged. `selected` highlights the card whose detail the panel is showing.
function TaskCard({
  task,
  selected,
  onOpen,
}: {
  task: Entity;
  selected: boolean;
  onOpen: () => void;
}): JSX.Element {
  const status = str(task, 'status') ?? 'todo';
  const priority = str(task, 'priority') ?? 'med';
  const assignee = str(task, 'assignee_handle');
  const snippet = firstLine(str(task, 'body_md'));
  const result = str(task, 'result_summary');
  const age = relTime(str(task, 'completed_at') ?? str(task, 'started_at') ?? str(task, 'updated_at'));
  const cls =
    'kanban-card' +
    (selected ? ' selected' : '') +
    (status === 'cancelled' ? ' cancelled' : '') +
    (status === 'blocked' ? ' blocked' : '');
  return (
    <div className={cls} role="button" onClick={onOpen}>
      <div className="kanban-card-top">
        <span className={`prio-dot prio-${priority}`} title={priority} />
        <div className="kanban-card-title">{str(task, 'title') ?? str(task, 'summary') ?? str(task, 'id')}</div>
      </div>
      {snippet !== '' && <div className="kanban-card-body">{snippet}</div>}
      {result !== undefined && result !== '' && <div className="kanban-card-result">{result}</div>}
      {(assignee !== undefined || age !== '') && (
        <div className="kanban-card-foot">
          {assignee !== undefined && (
            <span className="kanban-card-assignee">
              <span className={pipClass(str(task, 'assignee_status'))} />
              {assignee}
            </span>
          )}
          <span className="spacer" />
          {age !== '' && <span className="kanban-card-age">{age}</span>}
        </div>
      )}
    </div>
  );
}

function TasksTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [open, setOpen] = useState<Entity | null>(null);
  const [creating, setCreating] = useState(false);
  // Master-detail engages ≥1100px (panel; modal below); the tri-pane transcript
  // preview engages ≥1600px (decision §6.4). The detail panel is user-resizable,
  // mirroring MissionLayout's right-dock rail (usePanelWidth + ResizeHandle).
  const wide = useMinWidth(1100);
  const ultra = useMinWidth(1600);
  const [panelW, onPanelResize] = usePanelWidth('termipod.taskboard.panelW', 360, 280, 620, -1);
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
  const selectedId = open !== null ? str(open, 'id') : null;
  // Render the detail from the freshest copy of the task: `open` is the
  // click-time snapshot, but tasksQ polls (8s) and the persistent panel must
  // track live assignee status / result_summary like the cards do. Snapshot
  // stays as a fallback so the panel survives the task dropping off the list.
  const selected = open !== null ? tasks.find((task) => str(task, 'id') === selectedId) ?? open : null;
  const showPanel = wide && selected !== null;
  const assigneeId = selected !== null ? str(selected, 'assignee_id') : undefined;
  const showTranscript = ultra && selected !== null && assigneeId !== undefined;

  const board = (
    <div className="kanban">
      {COLUMNS.map((status) => {
        const items = inColumn(status);
        return (
          <div key={status} className="kanban-col">
            <div className="kanban-head">
              {t(`kanban.${status}`)} <span className="pill">{items.length}</span>
            </div>
            {items.map((task) => (
              <TaskCard
                key={str(task, 'id')}
                task={task}
                selected={str(task, 'id') === selectedId}
                onOpen={() => setOpen(task)}
              />
            ))}
            {items.length === 0 && <div className="kanban-empty">—</div>}
          </div>
        );
      })}
    </div>
  );

  return (
    <>
      <div className="kanban-bar">
        <span className="spacer" />
        <button onClick={() => setCreating(true)}>+ {t('task.new')}</button>
      </div>
      {showPanel ? (
        <div className="kanban-split">
          {board}
          <ResizeHandle onResize={onPanelResize} />
          <aside className="task-panel" style={{ flex: `0 0 ${panelW}px` }}>
            {/* key: the panel swaps tasks in place (no unmount, unlike the
                modal), and TaskDetailBody seeds picker state from props — a
                remount per task keeps task A's edits from saving onto task B. */}
            <TaskDetailBody
              key={selectedId ?? undefined}
              projectId={projectId}
              task={selected!}
              onClose={() => setOpen(null)}
            />
          </aside>
          {showTranscript && (
            <aside className="task-transcript">
              <AgentTranscript agentId={assigneeId!} />
            </aside>
          )}
        </div>
      ) : (
        board
      )}
      {selected !== null && !wide && <TaskDetail projectId={projectId} task={selected} onClose={() => setOpen(null)} />}
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
    { v: 'agents', label: t('proj.agents') },
    { v: 'tasks', label: t('proj.tasks') },
    { v: 'criteria', label: t('proj.criteria') },
    { v: 'runs', label: t('proj.runs') },
    { v: 'plans', label: t('proj.plans') },
    { v: 'documents', label: t('proj.documents') },
    { v: 'files', label: t('proj.files') },
    { v: 'activity', label: t('proj.activity') },
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
        {tab === 'agents' && <AgentsTab projectId={projectId} />}
        {tab === 'tasks' && <TasksTab projectId={projectId} />}
        {tab === 'criteria' && <CriteriaTab projectId={projectId} />}
        {tab === 'runs' && <RunsTab projectId={projectId} />}
        {tab === 'plans' && <PlansTab projectId={projectId} />}
        {tab === 'documents' && <DocumentsTab projectId={projectId} />}
        {tab === 'files' && <FilesTab projectId={projectId} />}
        {tab === 'activity' && <ActivityTab projectId={projectId} />}
      </div>
    </div>
  );
}
