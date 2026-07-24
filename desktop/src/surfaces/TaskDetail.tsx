import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';
import { Modal } from '../ui/Modal';

const STATUSES = ['todo', 'in_progress', 'blocked', 'in_review', 'done', 'cancelled'];
const PRIORITIES = ['low', 'med', 'high', 'urgent'];

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Compact relative age ("3h", "2d") from an ISO timestamp; '' when absent or
/// unparseable. Units are language-neutral (s/m/h/d) — no i18n plumbing needed
/// for the card/foot density this feeds. Shared by the board cards and panel.
export function relTime(iso: string | undefined): string {
  if (iso === undefined || iso === '') return '';
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return '';
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/// Live-assignee status → pip colour class. Maps the agent lifecycle
/// (pending → running → terminated/crashed/failed) onto the four pip states so
/// a card can show whether its worker is alive at a glance (mobile parity).
export function pipClass(status: string | undefined): string {
  switch (status) {
    case 'running':
      return 'pip pip-live';
    case 'crashed':
    case 'failed':
      return 'pip pip-bad';
    case 'pending':
      return 'pip pip-wait';
    default:
      return 'pip pip-idle';
  }
}

/// First non-empty line of a markdown body, trimmed of leading list/heading
/// markers, for the one-line card snippet. Returns '' when there's no body.
export function firstLine(bodyMd: string | undefined): string {
  if (bodyMd === undefined) return '';
  for (const raw of bodyMd.split('\n')) {
    const line = raw.replace(/^\s*([#>*-]+\s*)?/, '').trim();
    if (line !== '') return line;
  }
  return '';
}

/// The rich task detail — shared by the modal (narrow) and the master-detail
/// panel (wide, ≥1100px). Reaches mobile parity: status/priority pickers, live
/// assignee pip + Open-transcript, result summary, start/complete timestamps,
/// and the markdown body. Changes patch via `PATCH /projects/{p}/tasks/{t}`
/// (`handlePatchTask`); the kanban re-renders off the invalidated
/// `['tasks', project]` query.
export function TaskDetailBody({
  projectId,
  task,
  onClose,
  onAssign,
}: {
  projectId: string;
  task: Entity;
  onClose: () => void;
  // W3: opens the assign-agent picker for this task (keyboard/non-DnD path to
  // the same spawn the drag-into-In-progress drop triggers). Omitted by the
  // modal (narrow viewport) where DnD isn't the entry point anyway.
  onAssign?: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const selectAgent = useFocus((s) => s.selectAgent);
  const qc = useQueryClient();
  const taskId = str(task, 'id') ?? '';
  const [status, setStatus] = useState(str(task, 'status') ?? 'todo');
  const [priority, setPriority] = useState(str(task, 'priority') ?? 'med');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = status !== (str(task, 'status') ?? 'todo') || priority !== (str(task, 'priority') ?? 'med');

  const assignee = str(task, 'assignee_handle') ?? str(task, 'assignee_id');
  const assigneeId = str(task, 'assignee_id');
  const assigneeStatus = str(task, 'assignee_status');
  const resultSummary = str(task, 'result_summary');
  const startedAt = str(task, 'started_at');
  const completedAt = str(task, 'completed_at');
  const bodyMd = str(task, 'body_md');

  async function save(): Promise<void> {
    if (client === null || !dirty) return;
    setBusy(true);
    setError(null);
    try {
      await client.patchTask(projectId, taskId, { status, priority });
      await qc.invalidateQueries({ queryKey: ['tasks', projectId] });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  // W2 review lifecycle: from in_review a human accepts (→done) or sends the
  // work back to the assignee (→in_progress to re-engage). A one-click patch —
  // no Save round-trip. (The send-back note into the assignee session is W5.)
  async function review(target: string): Promise<void> {
    if (client === null) return;
    setBusy(true);
    setError(null);
    try {
      await client.patchTask(projectId, taskId, { status: target });
      await qc.invalidateQueries({ queryKey: ['tasks', projectId] });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="admin-tabs">
        <strong>{t('task.detail')}</strong>
        <span className="spacer" />
        <button onClick={onClose}>{t('admin.close')}</button>
      </div>
      <div className="admin-body">
        <div className="task-title">{str(task, 'title') ?? str(task, 'summary') ?? taskId}</div>

        {(str(task, 'status') ?? 'todo') === 'in_review' && (
          <div className="task-review-actions">
            <button className="primary" disabled={busy} onClick={() => void review('done')}>
              {t('task.accept')}
            </button>
            <button disabled={busy} onClick={() => void review('in_progress')}>
              {t('task.sendBack')}
            </button>
          </div>
        )}

        <div className="setting-row">
          <label>{t('task.assignee')}</label>
          {assignee !== undefined ? (
            <span className="task-assignee">
              <span className={pipClass(assigneeStatus)} />
              {assignee}
              {assigneeId !== undefined && (
                <button
                  className="link-btn"
                  onClick={() => selectAgent('projects', assigneeId, str(task, 'assignee_handle') ?? undefined)}
                >
                  {t('task.openTranscript')}
                </button>
              )}
            </span>
          ) : (
            <span className="task-assignee">
              <span className="muted">{t('task.unassigned')}</span>
              {onAssign !== undefined && (
                <button className="link-btn" onClick={onAssign}>
                  {t('task.assign')}
                </button>
              )}
            </span>
          )}
        </div>

        {resultSummary !== undefined && resultSummary !== '' && (
          <div className="setting-row task-result-row">
            <label>{t('task.result')}</label>
            <span>{resultSummary}</span>
          </div>
        )}

        {(startedAt !== undefined || completedAt !== undefined) && (
          <div className="setting-row task-times">
            {startedAt !== undefined && (
              <span className="muted small">
                {t('task.started')} {relTime(startedAt)}
              </span>
            )}
            {completedAt !== undefined && (
              <span className="muted small">
                {t('task.completed')} {relTime(completedAt)}
              </span>
            )}
          </div>
        )}

        <section className="setting-group">
          <h3>{t('task.status')}</h3>
          <div className="seg seg-wrap">
            {STATUSES.map((s) => (
              <button key={s} className={status === s ? 'seg-btn active' : 'seg-btn'} onClick={() => setStatus(s)}>
                {t(`kanban.${s}`)}
              </button>
            ))}
          </div>
        </section>

        <section className="setting-group">
          <h3>{t('task.priority')}</h3>
          <div className="seg seg-wrap">
            {PRIORITIES.map((p) => (
              <button key={p} className={priority === p ? 'seg-btn active' : 'seg-btn'} onClick={() => setPriority(p)}>
                {p}
              </button>
            ))}
          </div>
        </section>

        {bodyMd !== undefined && bodyMd !== '' && (
          <div className="task-body">
            <Markdown text={bodyMd} />
          </div>
        )}

        {error !== null && <div className="error">{error}</div>}

        <div className="setting-row">
          <span className="spacer" />
          <button className="primary" disabled={!dirty || busy} onClick={() => void save()}>
            {busy ? t('task.saving') : t('task.save')}
          </button>
        </div>
      </div>
    </>
  );
}

/// Modal wrapper over `TaskDetailBody` — the narrow-viewport (<1100px) form.
/// On wide viewports `TasksTab` renders `TaskDetailBody` inline as a
/// master-detail panel instead (no modal).
export function TaskDetail({
  projectId,
  task,
  onClose,
}: {
  projectId: string;
  task: Entity;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  return (
    <Modal onClose={onClose} className="settings" ariaLabel={t('task.detail')}>
      <TaskDetailBody projectId={projectId} task={task} onClose={onClose} />
    </Modal>
  );
}
