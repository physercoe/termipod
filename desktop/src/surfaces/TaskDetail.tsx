import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';

const STATUSES = ['todo', 'in_progress', 'blocked', 'done', 'cancelled'];
const PRIORITIES = ['low', 'med', 'high', 'urgent'];

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Task detail (WS6, deepened) — inspect a task and change its status /
/// priority via `PATCH /projects/{project}/tasks/{task}` (`handlePatchTask`).
/// The kanban re-renders off the invalidated `['tasks', project]` query.
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
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const taskId = str(task, 'id') ?? '';
  const [status, setStatus] = useState(str(task, 'status') ?? 'todo');
  const [priority, setPriority] = useState(str(task, 'priority') ?? 'med');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = status !== (str(task, 'status') ?? 'todo') || priority !== (str(task, 'priority') ?? 'med');

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

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="settings" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('task.detail')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="admin-body">
          <div className="task-title">{str(task, 'title') ?? str(task, 'summary') ?? taskId}</div>
          {str(task, 'body_md') !== undefined && str(task, 'body_md') !== '' && (
            <div className="task-body">
              <Markdown text={str(task, 'body_md') ?? ''} />
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

          <div className="setting-row">
            <label>{t('task.assignee')}</label>
            <span className="muted">{str(task, 'assignee_handle') ?? str(task, 'assignee_id') ?? t('task.unassigned')}</span>
          </div>

          {error !== null && <div className="error">{error}</div>}

          <div className="setting-row">
            <span className="spacer" />
            <button className="primary" disabled={!dirty || busy} onClick={() => void save()}>
              {busy ? t('task.saving') : t('task.save')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
