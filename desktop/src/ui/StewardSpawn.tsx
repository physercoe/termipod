import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { useAgents, useEnvProfiles, useHosts } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import {
  defaultStewardName,
  isStewardHandle,
  normalizeStewardHandle,
  parseBackendKind,
  stewardTemplatePicks,
  suggestedNameFor,
  validateStewardName,
} from '../state/stewardSpawn';
import { HostKeyTrustDialog } from './HostKeyTrustDialog';
import { Modal } from './Modal';
import {
  EnvSecretError,
  inspectHostKey,
  resolveSecretRefs,
  sealEnvSecrets,
  secretRefsOf,
  trustHostKey,
  type HostKeyInfo,
} from '../vault/envSecrets';

/// Spawn a steward — desktop parity with mobile's spawn_steward_sheet.dart.
/// Deliberately NOT the worker sheet (AgentSpawn): a steward spawns from a
/// `agents/steward*.yaml` TEMPLATE, whose `default_role: team.*` escalates
/// the role hub-side — the worker sheet's engine `kind` can only ever mint
/// role=worker. The user types a bare Name (`steward`, or a domain like
/// `research`); the `-steward` suffix convention is applied on submit.
///
/// The `@steward` general concierge (steward.general* template, hub
/// ensure-endpoint singleton) is out of scope here, as on mobile's sheet.
///
/// Flow: pick template + host (+ env profile) → fetch the MERGED template
/// YAML → parse `backend.kind` for the request's engine `kind` → spawn with
/// `spawn_spec_yaml`, `permission_mode: 'skip'` (the bootstrap default) and
/// `auto_open_session` so the steward never exists agent-without-session.
/// Env profiles with secret_refs run the same resolve → host-key trust →
/// seal path as the worker sheet (ADR-056 E3b-3).
export function StewardSpawn({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const hostsQ = useHosts();
  const agentsQ = useAgents();
  const envProfilesQ = useEnvProfiles();
  const hosts = hostsQ.data ?? [];
  const envProfiles = envProfilesQ.data ?? [];

  // Live steward handles (running/pending/paused) — the collision check for
  // the Name field. Handle-convention only: an `@`-form (concierge, project
  // stewards) can never collide with valid input.
  const live = new Set(
    (agentsQ.data ?? [])
      .filter((a) => {
        const s = str(a, 'status') ?? '';
        return (s === 'running' || s === 'pending' || s === 'paused') && isStewardHandle(str(a, 'handle') ?? '');
      })
      .map((a) => str(a, 'handle') ?? ''),
  );

  const [name, setName] = useState<string | null>(null);
  const [template, setTemplate] = useState('steward.v1.yaml');
  const [hostId, setHostId] = useState('');
  const [envProfileId, setEnvProfileId] = useState('');
  const [pending, setPending] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);
  const [sealing, setSealing] = useState(false);
  const [secretError, setSecretError] = useState<string | null>(null);
  const [trust, setTrust] = useState<{ info: HostKeyInfo; secrets: Record<string, string>; profileId: string } | null>(
    null,
  );

  // Default the Name once the agents list answers: `steward` when free.
  const effectiveName = name ?? defaultStewardName(live);

  const templatesQ = useQuery({
    queryKey: ['templates', 'agents'],
    enabled: client !== null,
    queryFn: () => client!.listTemplates('agents'),
  });
  const templates = stewardTemplatePicks(
    (templatesQ.data ?? []).map((row: Entity) => str(row, 'name') ?? '').filter((n) => n !== ''),
  );
  // Snap to a listed template if the default isn't among the team's picks.
  useEffect(() => {
    if (!templates.includes(template)) setTemplate(templates[0]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templates.join('|')]);

  const effectiveHost = hostId !== '' ? hostId : (hosts[0] !== undefined ? (str(hosts[0], 'id') ?? '') : '');
  const canSubmit = effectiveName.trim() !== '' && effectiveHost !== '';

  function pickTemplate(tpl: string): void {
    setTemplate(tpl);
    // A domain template seeds the Name — but never clobbers a typed one.
    const suggested = suggestedNameFor(tpl);
    if (suggested !== '' && (name === null || name === '')) setName(suggested);
  }

  async function doSpawn(specYaml: string, kind: string, handle: string, envelope?: string): Promise<void> {
    if (client === null) return;
    const res = await run(
      () =>
        client.spawnAgent({
          child_handle: handle,
          kind,
          host_id: effectiveHost,
          env_profile_id: envProfileId !== '' ? envProfileId : undefined,
          env_secret_envelope: envelope,
          spawn_spec_yaml: specYaml,
          permission_mode: 'skip',
          auto_open_session: true,
        }),
      { invalidate: [['agents'], ['attention'], ['sessions']] },
    );
    if (res === undefined) return;
    if (str(res, 'status') === 'pending_approval') {
      setPending(true);
    } else {
      onClose();
    }
  }

  async function submit(): Promise<void> {
    if (client === null || !canSubmit) return;
    setSecretError(null);
    setNameError(null);

    const code = validateStewardName(effectiveName);
    if (code !== null) {
      setNameError(t(`steward.err.${code}`));
      return;
    }
    const handle = normalizeStewardHandle(effectiveName);
    if (live.has(handle)) {
      setNameError(t('steward.err.taken').replace('{handle}', handle));
      return;
    }

    setSealing(true);
    try {
      // The MERGED template (team overlay over the bundled default) is the
      // spawn spec; its backend.kind is the request's engine kind.
      const specYaml = await client.getTemplateText('agents', template, true);
      const kind = parseBackendKind(specYaml) ?? 'claude-code';

      const profile = envProfileId !== '' ? envProfiles.find((p) => str(p, 'id') === envProfileId) : undefined;
      const refs = secretRefsOf(profile);
      if (refs.length === 0) {
        await doSpawn(specYaml, kind, handle);
        return;
      }
      const secrets = await resolveSecretRefs(refs);
      const host = hosts.find((h) => str(h, 'id') === effectiveHost) as Entity | undefined;
      if (host === undefined) {
        setSecretError(t('spawn.secretErr.noHostKey'));
        return;
      }
      const info = await inspectHostKey(host, effectiveHost);
      if (!info.trusted) {
        setTrust({ info, secrets, profileId: envProfileId });
        return;
      }
      await sealAndSpawn(specYaml, kind, handle, info, secrets, envProfileId);
    } catch (e) {
      if (e instanceof EnvSecretError) {
        setSecretError(t(`spawn.secretErr.${e.code}`));
      } else {
        throw e;
      }
    } finally {
      setSealing(false);
    }
  }

  async function sealAndSpawn(
    specYaml: string,
    kind: string,
    handle: string,
    info: HostKeyInfo,
    secrets: Record<string, string>,
    profileId: string,
  ): Promise<void> {
    if (client === null) return;
    const envelope = await sealEnvSecrets(secrets, info, client.transport.teamId, profileId);
    await doSpawn(specYaml, kind, handle, envelope);
  }

  async function confirmTrust(): Promise<void> {
    if (trust === null) return;
    const held = trust;
    setTrust(null);
    setSealing(true);
    setSecretError(null);
    try {
      trustHostKey(held.info.hostId, held.info.pubkey);
      const specYaml = await client!.getTemplateText('agents', template, true);
      const kind = parseBackendKind(specYaml) ?? 'claude-code';
      await sealAndSpawn(specYaml, kind, normalizeStewardHandle(effectiveName), held.info, held.secrets, held.profileId);
    } catch (e) {
      if (e instanceof EnvSecretError) {
        setSecretError(t(`spawn.secretErr.${e.code}`));
      } else {
        throw e;
      }
    } finally {
      setSealing(false);
    }
  }

  return (
    <>
      <Modal onClose={onClose} className="task-detail" ariaLabel={t('steward.title')}>
        <div className="admin-tabs">
          <strong>{t('steward.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <p className="muted small wide">{t('steward.lead')}</p>
          <label>
            {t('steward.name')}
            <input
              value={effectiveName}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('steward.namePlaceholder')}
              autoFocus
            />
          </label>
          <label>
            {t('steward.template')}
            <select value={template} onChange={(e) => pickTemplate(e.target.value)}>
              {templates.map((n) => (
                <option key={n} value={n}>
                  {n}
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
          {hosts.length === 0 && <div className="muted small wide">{t('spawn.noHost')}</div>}
          {pending && <div className="wide sev sev-medium">{t('spawn.pending')}</div>}
          {nameError !== null && <div className="error wide">{nameError}</div>}
          {secretError !== null && <div className="error wide">{secretError}</div>}
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            {pending ? (
              <button className="primary" onClick={onClose}>
                {t('admin.close')}
              </button>
            ) : (
              <button className="primary" disabled={busy || sealing || !canSubmit} onClick={() => void submit()}>
                {sealing ? t('spawn.sealing') : t('steward.submit')}
              </button>
            )}
          </div>
        </div>
      </Modal>
      {trust !== null && (
        <HostKeyTrustDialog
          info={trust.info}
          sealing={sealing}
          confirmLabel={t('spawn.trustConfirm')}
          retrustConfirmLabel={t('spawn.retrustConfirm')}
          onCancel={() => setTrust(null)}
          onConfirm={() => void confirmTrust()}
        />
      )}
    </>
  );
}
