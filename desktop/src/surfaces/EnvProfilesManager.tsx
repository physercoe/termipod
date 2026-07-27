import { useState } from 'react';
import { useHubAction } from '../hub/action';
import { useEnvProfiles } from '../hub/queries';
import { obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';

// EnvProfilesManager — desktop management UI for team environment profiles
// (env-profiles plan, E2b-2). CRUD over the hub REST surface
// (/v1/teams/{team}/env-profiles); the spawn sheet's picker (E2b-1) lists what
// is created here. Rendered as a Settings category.
//
// E2 scope: name / description / env_vars / setup_script / failure policy /
// network mode. secret_refs are shown as a read-only count with a note — they
// are stored but not applied at spawn until E3 (host-key envelopes), so editing
// them here would imply a capability that doesn't exist yet.

interface KV {
  key: string;
  value: string;
}

interface Draft {
  id: string | null; // null → creating a new profile
  name: string;
  description: string;
  setupScript: string;
  failurePolicy: string; // fail | continue
  netMode: string; // open | allowlist | offline
  envVars: KV[];
}

function draftFrom(p: Entity): Draft {
  const evObj = obj(p, 'env_vars') ?? {};
  const envVars: KV[] = Object.entries(evObj).map(([key, value]) => ({ key, value: String(value) }));
  return {
    id: str(p, 'id') ?? null,
    name: str(p, 'name') ?? '',
    description: str(p, 'description') ?? '',
    setupScript: str(p, 'setup_script') ?? '',
    failurePolicy: str(p, 'setup_failure_policy') ?? 'fail',
    netMode: str(obj(p, 'network_policy') ?? {}, 'mode') ?? 'open',
    envVars,
  };
}

function emptyDraft(): Draft {
  return { id: null, name: '', description: '', setupScript: '', failurePolicy: 'fail', netMode: 'open', envVars: [] };
}

export function EnvProfilesManager(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const profilesQ = useEnvProfiles();
  const { run, busy, error } = useHubAction();
  const profiles = profilesQ.data ?? [];

  const [draft, setDraft] = useState<Draft | null>(null);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const nameOk = draft !== null && draft.name.trim() !== '';

  async function save(): Promise<void> {
    if (draft === null || client === null || !nameOk) return;
    const env_vars: Record<string, string> = {};
    for (const kv of draft.envVars) {
      const k = kv.key.trim();
      if (k !== '') env_vars[k] = kv.value;
    }
    const body = {
      name: draft.name.trim(),
      description: draft.description,
      setup_script: draft.setupScript,
      setup_failure_policy: draft.failurePolicy,
      env_vars,
      network_policy: { mode: draft.netMode },
    };
    const res = await run(
      () => (draft.id === null ? client.createEnvProfile(body) : client.updateEnvProfile(draft.id, body)),
      { invalidate: [['env-profiles']] },
    );
    if (res !== undefined) setDraft(null);
  }

  async function remove(id: string): Promise<void> {
    if (client === null) return;
    const res = await run(() => client.deleteEnvProfile(id), { invalidate: [['env-profiles']] });
    if (res !== undefined) setConfirmId(null);
  }

  function setVar(i: number, patch: Partial<KV>): void {
    if (draft === null) return;
    const next = draft.envVars.map((kv, j) => (j === i ? { ...kv, ...patch } : kv));
    setDraft({ ...draft, envVars: next });
  }

  if (draft !== null) {
    return (
      <div className="envprof">
        <p className="muted small settings-lead">
          {draft.id === null ? t('envprof.newTitle') : t('envprof.editTitle')}
        </p>
        <div className="task-form">
          <label>
            {t('envprof.name')}
            <input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} autoFocus />
          </label>
          <label>
            {t('envprof.failurePolicy')}
            <select value={draft.failurePolicy} onChange={(e) => setDraft({ ...draft, failurePolicy: e.target.value })}>
              <option value="fail">{t('envprof.policyFail')}</option>
              <option value="continue">{t('envprof.policyContinue')}</option>
            </select>
          </label>
          <label className="wide">
            {t('envprof.description')}
            <input value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
          </label>
          <label>
            {t('envprof.network')}
            <select value={draft.netMode} onChange={(e) => setDraft({ ...draft, netMode: e.target.value })}>
              <option value="open">{t('envprof.netOpen')}</option>
              <option value="allowlist">{t('envprof.netAllowlist')}</option>
              <option value="offline">{t('envprof.netOffline')}</option>
            </select>
          </label>
          <div className="wide">
            <div className="envprof-kv-head">
              <span>{t('envprof.envVars')}</span>
              <span className="spacer" />
              <button onClick={() => setDraft({ ...draft, envVars: [...draft.envVars, { key: '', value: '' }] })}>
                {t('envprof.addVar')}
              </button>
            </div>
            {draft.envVars.length === 0 && <div className="muted small">{t('envprof.noVars')}</div>}
            {draft.envVars.map((kv, i) => (
              <div className="envprof-kv" key={i}>
                <input
                  className="mono"
                  placeholder={t('envprof.key')}
                  value={kv.key}
                  onChange={(e) => setVar(i, { key: e.target.value })}
                />
                <input
                  className="mono"
                  placeholder={t('envprof.value')}
                  value={kv.value}
                  onChange={(e) => setVar(i, { value: e.target.value })}
                />
                <button onClick={() => setDraft({ ...draft, envVars: draft.envVars.filter((_, j) => j !== i) })}>
                  {t('envprof.removeVar')}
                </button>
              </div>
            ))}
          </div>
          <label className="wide">
            {t('envprof.setupScript')}
            <textarea
              className="mono"
              value={draft.setupScript}
              onChange={(e) => setDraft({ ...draft, setupScript: e.target.value })}
              placeholder={t('envprof.setupPlaceholder')}
            />
          </label>
          <p className="muted small wide">{t('envprof.secretNote')}</p>
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions envprof-actions">
            <button onClick={() => setDraft(null)}>{t('envprof.cancel')}</button>
            <button className="primary" disabled={busy || !nameOk} onClick={() => void save()}>
              {t('envprof.save')}
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="envprof">
      <p className="muted small settings-lead">{t('envprof.lead')}</p>
      <div className="envprof-toolbar">
        <button className="primary" onClick={() => setDraft(emptyDraft())}>
          {t('envprof.new')}
        </button>
      </div>
      {profiles.length === 0 && <div className="muted small">{t('envprof.empty')}</div>}
      {profiles.map((p) => {
        const id = str(p, 'id') ?? '';
        const vars = obj(p, 'env_vars') ?? {};
        const varCount = Object.keys(vars).length;
        return (
          <div className="envprof-row" key={id}>
            <div className="envprof-row-main">
              <strong>{str(p, 'name') ?? id}</strong>
              {str(p, 'description') !== undefined && str(p, 'description') !== '' && (
                <span className="muted small">{str(p, 'description')}</span>
              )}
              <span className="muted small mono">{t('envprof.varCount').replace('{n}', String(varCount))}</span>
            </div>
            <span className="spacer" />
            <button onClick={() => setDraft(draftFrom(p))}>{t('envprof.edit')}</button>
            {confirmId === id ? (
              <button className="danger" disabled={busy} onClick={() => void remove(id)}>
                {t('envprof.confirmDelete')}
              </button>
            ) : (
              <button onClick={() => setConfirmId(id)}>{t('envprof.delete')}</button>
            )}
          </div>
        );
      })}
    </div>
  );
}
