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
export function AgentSpawn({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const hostsQ = useHosts();
  const projectsQ = useProjects();
  const hosts = hostsQ.data ?? [];
  const projects = projectsQ.data ?? [];

  const [handle, setHandle] = useState('');
  const [engine, setEngine] = useState('claude-code');
  const [hostId, setHostId] = useState('');
  const [projectId, setProjectId] = useState('');
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
          task: task.trim() !== '' ? { title: task.trim() } : undefined,
        }),
      { invalidate: [['agents'], ['attention']] },
    );
    if (res === undefined) return;
    if (str(res, 'status') === 'pending_approval') {
      setPending(true);
    } else {
      onClose();
    }
  }

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={t('spawn.title')}>
        <div className="admin-tabs">
          <strong>{t('spawn.title')}</strong>
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
