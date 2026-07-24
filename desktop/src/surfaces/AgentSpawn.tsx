import { useState } from 'react';
import { useHubAction } from '../hub/action';
import { useHosts, useProjects } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';

// The engine families (CLAUDE.md / ADR-035 / ADR-054). gemini-cli is deprecated
// but still spawnable until retirement; antigravity is its successor.
// kimi-code-ts is the TypeScript Kimi Code line, alongside the Python kimi-code.
const ENGINES = ['claude-code', 'codex', 'antigravity', 'kimi-code', 'kimi-code-ts', 'gemini-cli'];

/// Spawn an agent (parity Phase 4 / F3). Direct `POST /agents/spawn`
/// (self-governing): an immediate spawn returns `{agent_id}`; a policy-gated one
/// returns `202 pending_approval` and the item lands in the Attention dock.
///
/// Assign-to-task mode (W3): when `taskId` is set the sheet spawns a worker
/// *against an existing task* — it sends `task_id` (mutually exclusive with the
/// inline `task`), locks the project to the task's project, and swaps the
/// free-text task field for the task's title as read-only context. The task's
/// status flips todo→in_progress via the hub's derivation, not a client PATCH
/// (decision §6.3 — dragging a card into In progress opens this picker).
export function AgentSpawn({
  onClose,
  taskId,
  taskTitle,
  presetProjectId,
  onSpawned,
}: {
  onClose: () => void;
  taskId?: string;
  taskTitle?: string;
  presetProjectId?: string;
  onSpawned?: () => void;
}): JSX.Element {
  const t = useT();
  const assignMode = taskId !== undefined;
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const hostsQ = useHosts();
  const projectsQ = useProjects();
  const hosts = hostsQ.data ?? [];
  const projects = projectsQ.data ?? [];

  const [handle, setHandle] = useState('');
  const [engine, setEngine] = useState('claude-code');
  const [hostId, setHostId] = useState('');
  const [projectId, setProjectId] = useState(presetProjectId ?? '');
  const [task, setTask] = useState('');
  const [pending, setPending] = useState(false);

  const effectiveHost = hostId !== '' ? hostId : (hosts[0] !== undefined ? str(hosts[0], 'id') ?? '' : '');
  const canSubmit = handle.trim() !== '' && effectiveHost !== '';

  async function submit(): Promise<void> {
    if (client === null || !canSubmit) return;
    const res = await run(
      () =>
        client.spawnAgent({
          child_handle: handle.trim(),
          kind: engine,
          host_id: effectiveHost,
          project_id: projectId !== '' ? projectId : undefined,
          // task_id and the inline task are mutually exclusive hub-side. In
          // assign mode we link the existing task; otherwise the free-text
          // field mints a new one.
          task_id: assignMode ? taskId : undefined,
          task: !assignMode && task.trim() !== '' ? { title: task.trim() } : undefined,
        }),
      { invalidate: [['agents'], ['attention']] },
    );
    if (res === undefined) return;
    if (str(res, 'status') === 'pending_approval') {
      setPending(true);
    } else {
      onSpawned?.();
      onClose();
    }
  }

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={assignMode ? t('spawn.assignTitle') : t('spawn.title')}>
        <div className="admin-tabs">
          <strong>{assignMode ? t('spawn.assignTitle') : t('spawn.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <label>
            {t('spawn.handle')}
            <input value={handle} onChange={(e) => setHandle(e.target.value)} placeholder={t('spawn.handlePlaceholder')} autoFocus />
          </label>
          <label>
            {t('spawn.engine')}
            <select value={engine} onChange={(e) => setEngine(e.target.value)}>
              {ENGINES.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('spawn.host')}
            <select value={effectiveHost} onChange={(e) => setHostId(e.target.value)}>
              {hosts.map((h) => {
                const id = str(h, 'id') ?? '';
                return (
                  <option key={id} value={id}>
                    {str(h, 'name') ?? str(h, 'hostname') ?? id}
                  </option>
                );
              })}
            </select>
          </label>
          {assignMode ? (
            /* Project is fixed to the task's project; the task is the one being
               assigned — shown read-only, not authored here. */
            <label className="wide">
              {t('spawn.assignTo')}
              <div className="spawn-task-context">{taskTitle ?? taskId}</div>
            </label>
          ) : (
            <>
              <label>
                {t('spawn.project')}
                <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
                  <option value="">{t('spawn.none')}</option>
                  {projects.map((p) => {
                    const id = str(p, 'id') ?? '';
                    return (
                      <option key={id} value={id}>
                        {str(p, 'name') ?? id}
                      </option>
                    );
                  })}
                </select>
              </label>
              <label className="wide">
                {t('spawn.task')}
                <textarea value={task} onChange={(e) => setTask(e.target.value)} placeholder={t('spawn.taskPlaceholder')} />
              </label>
            </>
          )}
          {hosts.length === 0 && <div className="muted small wide">{t('spawn.noHost')}</div>}
          {pending && <div className="wide sev sev-medium">{t('spawn.pending')}</div>}
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            {pending ? (
              <button className="primary" onClick={onClose}>
                {t('admin.close')}
              </button>
            ) : (
              <button className="primary" disabled={busy || !canSubmit} onClick={() => void submit()}>
                {t('spawn.submit')}
              </button>
            )}
          </div>
        </div>
    </Modal>
  );
}
