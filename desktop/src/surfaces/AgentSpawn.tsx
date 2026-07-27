import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { useEnvProfiles, useHosts, useProjects } from '../hub/queries';
import { arr, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';
import {
  EnvSecretError,
  inspectHostKey,
  resolveSecretRefs,
  sealEnvSecrets,
  secretRefsOf,
  trustHostKey,
  type HostKeyInfo,
} from '../vault/envSecrets';

// The engine families (CLAUDE.md / ADR-035 / ADR-054). gemini-cli is deprecated
// but still spawnable until retirement; antigravity is its successor. kimi-code-ts
// is the only kimi line (the Python kimi-cli family was removed in #378 after
// upstream discontinued it).
const ENGINES = ['claude-code', 'codex', 'antigravity', 'kimi-code-ts', 'gemini-cli'];

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
  const envProfilesQ = useEnvProfiles();
  const hosts = hostsQ.data ?? [];
  const projects = projectsQ.data ?? [];
  const envProfiles = envProfilesQ.data ?? [];

  const [handle, setHandle] = useState('');
  const [engine, setEngine] = useState('claude-code');
  const [mode, setMode] = useState('');
  const [hostId, setHostId] = useState('');
  const [projectId, setProjectId] = useState(presetProjectId ?? '');
  const [envProfileId, setEnvProfileId] = useState('');
  const [task, setTask] = useState('');
  const [pending, setPending] = useState(false);
  // Env-secret sealing (ADR-056 E3b-3): resolving refs + sealing runs before the
  // spawn POST; `sealing` covers that async work and `secretError` shows a coded
  // failure (unresolved ref / no host key / key mismatch) the run() error can't.
  const [sealing, setSealing] = useState(false);
  const [secretError, setSecretError] = useState<string | null>(null);
  // A first-sight (or re-trust) host key awaiting the operator's confirmation.
  // While set, the trust dialog is shown; confirming pins the key and continues
  // the held spawn, cancelling drops back to the sheet.
  const [trust, setTrust] = useState<{ info: HostKeyInfo; secrets: Record<string, string>; profileId: string } | null>(
    null,
  );

  const effectiveHost = hostId !== '' ? hostId : (hosts[0] !== undefined ? str(hosts[0], 'id') ?? '' : '');
  const canSubmit = handle.trim() !== '' && effectiveHost !== '';

  // Driving-mode picker (#378): the engine family's `supports` list from the
  // hub registry constrains the options; '' = Auto (the engine default mode
  // resolves hub-side). Hidden while the registry hasn't answered and for
  // single-mode engines.
  const familiesQ = useQuery({
    queryKey: ['agent-families'],
    enabled: client !== null,
    queryFn: () => client!.listAgentFamilies(),
  });
  const engineModes = (() => {
    const fam = (familiesQ.data ?? []).find((f) => str(f, 'family') === engine);
    return fam === undefined ? [] : arr(fam, 'supports').map(String).filter((s) => s !== '');
  })();
  // Snap back to Auto when the picked engine can't run the previous choice.
  const effectiveMode = engineModes.includes(mode) ? mode : '';

  // The spawn body minus the env-secret envelope, shared by the no-secret and
  // sealed paths.
  function spawnBody(envelope?: string): Parameters<NonNullable<typeof client>['spawnAgent']>[0] {
    return {
      child_handle: handle.trim(),
      kind: engine,
      host_id: effectiveHost,
      project_id: projectId !== '' ? projectId : undefined,
      mode: effectiveMode !== '' ? effectiveMode : undefined,
      env_profile_id: envProfileId !== '' ? envProfileId : undefined,
      env_secret_envelope: envelope,
      // task_id and the inline task are mutually exclusive hub-side. In assign
      // mode we link the existing task; otherwise the free-text field mints one.
      task_id: assignMode ? taskId : undefined,
      task: !assignMode && task.trim() !== '' ? { title: task.trim() } : undefined,
    };
  }

  async function doSpawn(envelope?: string): Promise<void> {
    if (client === null) return;
    const res = await run(() => client.spawnAgent(spawnBody(envelope)), { invalidate: [['agents'], ['attention']] });
    if (res === undefined) return;
    if (str(res, 'status') === 'pending_approval') {
      setPending(true);
    } else {
      onSpawned?.();
      onClose();
    }
  }

  /// Map an env-secret failure to its localized message. Codes are stable i18n
  /// suffixes (spawn.secretErr.*).
  function secretErrMessage(code: string): string {
    return t(`spawn.secretErr.${code}`);
  }

  async function submit(): Promise<void> {
    if (client === null || !canSubmit) return;
    setSecretError(null);

    // Resolve + seal the profile's secret_refs, if any, before the spawn POST.
    // We can only do this for an EXPLICITLY picked profile — an inherited one
    // (env_profile_id === '') is resolved hub-side, and if it carries secret_refs
    // the hub returns 422; pick the profile explicitly to seal secrets.
    const profile = envProfileId !== '' ? envProfiles.find((p) => str(p, 'id') === envProfileId) : undefined;
    const refs = secretRefsOf(profile);
    if (refs.length === 0) {
      await doSpawn();
      return;
    }

    setSealing(true);
    try {
      const secrets = await resolveSecretRefs(refs);
      const host = hosts.find((h) => str(h, 'id') === effectiveHost) as Entity | undefined;
      if (host === undefined) {
        setSecretError(secretErrMessage('noHostKey'));
        return;
      }
      const info = await inspectHostKey(host, effectiveHost);
      if (!info.trusted) {
        // First sight (or a re-trust): hold the spawn until the operator
        // confirms the fingerprint against the host console banner.
        setTrust({ info, secrets, profileId: envProfileId });
        return;
      }
      await sealAndSpawn(info, secrets, envProfileId);
    } catch (e) {
      if (e instanceof EnvSecretError) {
        setSecretError(secretErrMessage(e.code));
      } else {
        throw e;
      }
    } finally {
      setSealing(false);
    }
  }

  async function sealAndSpawn(info: HostKeyInfo, secrets: Record<string, string>, profileId: string): Promise<void> {
    if (client === null) return;
    const envelope = await sealEnvSecrets(secrets, info, client.transport.teamId, profileId);
    await doSpawn(envelope);
  }

  async function confirmTrust(): Promise<void> {
    if (trust === null) return;
    const held = trust;
    setTrust(null);
    setSealing(true);
    setSecretError(null);
    try {
      trustHostKey(held.info.hostId, held.info.pubkey);
      await sealAndSpawn(held.info, held.secrets, held.profileId);
    } catch (e) {
      if (e instanceof EnvSecretError) {
        setSecretError(secretErrMessage(e.code));
      } else {
        throw e;
      }
    } finally {
      setSealing(false);
    }
  }

  return (
    <>
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
            <select
              value={engine}
              onChange={(e) => {
                setEngine(e.target.value);
                setMode('');
              }}
            >
              {ENGINES.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          {engineModes.length > 1 && (
            <label>
              {t('spawn.mode')}
              <select value={effectiveMode} onChange={(e) => setMode(e.target.value)}>
                <option value="">{t('spawn.modeAuto')}</option>
                {engineModes.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </label>
          )}
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
          {envProfiles.length > 0 && (
            <label>
              {t('spawn.envProfile')}
              <select value={envProfileId} onChange={(e) => setEnvProfileId(e.target.value)}>
                <option value="">{t('spawn.envProfileInherit')}</option>
                {envProfiles.map((p) => {
                  const id = str(p, 'id') ?? '';
                  return (
                    <option key={id} value={id}>
                      {str(p, 'name') ?? id}
                    </option>
                  );
                })}
              </select>
            </label>
          )}
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
          {secretError !== null && <div className="error wide">{secretError}</div>}
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            {pending ? (
              <button className="primary" onClick={onClose}>
                {t('admin.close')}
              </button>
            ) : (
              <button className="primary" disabled={busy || sealing || !canSubmit} onClick={() => void submit()}>
                {sealing ? t('spawn.sealing') : t('spawn.submit')}
              </button>
            )}
          </div>
        </div>
    </Modal>
    {trust !== null && (
      <Modal onClose={() => setTrust(null)} className="task-detail" ariaLabel={t('spawn.trustTitle')}>
        <div className="admin-tabs">
          <strong>{t('spawn.trustTitle')}</strong>
          <span className="spacer" />
        </div>
        <div className="task-form">
          <div className="wide">{t('spawn.trustBody')}</div>
          <div className="wide spawn-fingerprint">{trust.info.fingerprint}</div>
          <div className="wide muted small">{t('spawn.trustHint')}</div>
          <div className="wide task-form-actions">
            <button onClick={() => setTrust(null)}>{t('admin.close')}</button>
            <span className="spacer" />
            <button className="primary" disabled={sealing} onClick={() => void confirmTrust()}>
              {t('spawn.trustConfirm')}
            </button>
          </div>
        </div>
      </Modal>
    )}
    </>
  );
}
