import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { ConfirmButton } from '../ui/ConfirmButton';
import { useConfirm } from '../ui/ConfirmModal';
import { Modal } from '../ui/Modal';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Small "shown once" secret row — a fresh token / recovery string the operator
/// must copy now (the hub never returns it again).
function SecretReveal({ label, value }: { label: string; value: string }): JSX.Element {
  const t = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div className="secret-reveal">
      <div className="muted">{label}</div>
      <div className="admin-row">
        <code className="mono">{value}</code>
        <button
          onClick={() => {
            void navigator.clipboard?.writeText(value);
            setCopied(true);
          }}
        >
          {copied ? t('admin.copied') : t('admin.copy')}
        </button>
      </div>
    </div>
  );
}

function TeamTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const principalsQ = useQuery({
    queryKey: ['principals'],
    enabled: client !== null,
    queryFn: () => client!.listPrincipals(),
  });
  const policyQ = useQuery({
    queryKey: ['policy-text'],
    enabled: client !== null,
    queryFn: () => client!.getPolicyText(),
  });

  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Seed the editor once the policy loads; keep the user's edits after that.
  useEffect(() => {
    if (policyQ.data !== undefined && draft === null) setDraft(policyQ.data);
  }, [policyQ.data, draft]);

  async function save(): Promise<void> {
    if (client === null || draft === null) return;
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await client.putPolicy(draft);
      setSaved(true);
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="admin-cols">
      <section>
        <h3>{t('admin.members')}</h3>
        {principalsQ.isError ? (
          <div className="error">{msg(principalsQ.error)}</div>
        ) : (
          (principalsQ.data ?? []).map((p, i) => (
            <div key={str(p, 'id') ?? String(i)} className="admin-row">
              <span>{str(p, 'handle') ?? str(p, 'id')}</span>
              <span className="muted">{str(p, 'role') ?? str(p, 'kind')}</span>
            </div>
          ))
        )}
        {!principalsQ.isError && (principalsQ.data ?? []).length === 0 && (
          <div className="muted">{t('admin.noMembers')}</div>
        )}
      </section>
      <section>
        <h3>{t('admin.editPolicy')}</h3>
        {policyQ.isError ? (
          <div className="error">{msg(policyQ.error)}</div>
        ) : (
          <>
            <textarea
              className="policy-edit mono"
              spellCheck={false}
              value={draft ?? ''}
              onChange={(e) => {
                setDraft(e.target.value);
                setSaved(false);
              }}
            />
            {error !== null && <div className="error">{error}</div>}
            <div className="admin-row">
              {saved ? <span className="muted">{t('admin.saved')}</span> : <span />}
              <button className="primary" disabled={busy || draft === null} onClick={() => void save()}>
                {t('admin.savePolicy')}
              </button>
            </div>
          </>
        )}
      </section>
    </div>
  );
}

function HostsTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['admin-hosts'],
    enabled: client !== null,
    queryFn: () => client!.adminListHosts(),
  });

  async function act(host: string, action: 'ping' | 'restart' | 'shutdown' | 'update'): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      await client.adminHostAction(host, action);
      await qc.invalidateQueries({ queryKey: ['admin-hosts'] });
    } catch (e) {
      setError(msg(e));
    }
  }

  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const hosts = q.data ?? [];

  return (
    <>
      {error !== null && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr>
            <th>{t('admin.host')}</th>
            <th>{t('admin.status')}</th>
            <th>{t('admin.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {hosts.map((h, i) => {
            const id = str(h, 'id') ?? str(h, 'name') ?? String(i);
            return (
              <tr key={id}>
                <td>{str(h, 'name') ?? str(h, 'hostname') ?? id}</td>
                <td>{str(h, 'status') ?? ''}</td>
                <td className="admin-actions">
                  <button onClick={() => void act(id, 'ping')}>{t('admin.ping')}</button>
                  <ConfirmButton label={t('admin.restart')} onConfirm={() => void act(id, 'restart')} />
                  <ConfirmButton label={t('admin.update')} onConfirm={() => void act(id, 'update')} />
                  <ConfirmButton label={t('admin.shutdown')} danger onConfirm={() => void act(id, 'shutdown')} />
                </td>
              </tr>
            );
          })}
          {hosts.length === 0 && (
            <tr>
              <td colSpan={3} className="muted">
                {t('admin.noHosts')}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

function AgentsTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['admin-agents'],
    enabled: client !== null,
    queryFn: () => client!.adminListAgents(),
  });

  async function kill(agent: string): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      await client.adminKillAgent(agent);
      await qc.invalidateQueries({ queryKey: ['admin-agents'] });
    } catch (e) {
      setError(msg(e));
    }
  }

  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const agents = q.data ?? [];

  return (
    <>
      {error !== null && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr>
            <th>{t('admin.agents')}</th>
            <th>{t('admin.team')}</th>
            <th>{t('admin.status')}</th>
            <th>{t('admin.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {agents.map((a: Entity, i) => {
            const id = str(a, 'id') ?? String(i);
            return (
              <tr key={id}>
                <td>{str(a, 'handle') ?? id}</td>
                <td>{str(a, 'team_id') ?? ''}</td>
                <td>{str(a, 'status') ?? ''}</td>
                <td className="admin-actions">
                  <ConfirmButton label={t('admin.kill')} danger onConfirm={() => void kill(id)} />
                </td>
              </tr>
            );
          })}
          {agents.length === 0 && (
            <tr>
              <td colSpan={4} className="muted">
                {t('admin.noAgents')}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

function TeamsTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [error, setError] = useState<string | null>(null);
  const [freshToken, setFreshToken] = useState<{ team: string; token: string } | null>(null);
  const q = useQuery({
    queryKey: ['admin-teams'],
    enabled: client !== null,
    queryFn: () => client!.adminListTeams(),
  });

  async function rotate(team: string): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      const res = await client.adminRotateTeamToken(team);
      const tok = str(res, 'token') ?? str(res, 'value');
      if (tok !== undefined) setFreshToken({ team, token: tok });
    } catch (e) {
      setError(msg(e));
    }
  }

  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const teams = q.data ?? [];

  return (
    <>
      {error !== null && <div className="error">{error}</div>}
      {freshToken !== null && (
        <SecretReveal label={`${t('admin.newToken')} · ${freshToken.team}`} value={freshToken.token} />
      )}
      <table>
        <thead>
          <tr>
            <th>{t('admin.team')}</th>
            <th>{t('admin.name')}</th>
            <th>{t('admin.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {teams.map((tm, i) => {
            const id = str(tm, 'id') ?? String(i);
            return (
              <tr key={id}>
                <td className="mono">{id}</td>
                <td>{str(tm, 'name') ?? ''}</td>
                <td className="admin-actions">
                  <ConfirmButton label={t('admin.rotateToken')} danger onConfirm={() => void rotate(id)} />
                </td>
              </tr>
            );
          })}
          {teams.length === 0 && (
            <tr>
              <td colSpan={3} className="muted">
                {t('admin.noTeams')}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}

function UpkeepTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [error, setError] = useState<string | null>(null);
  const [vacuum, setVacuum] = useState<Entity | null>(null);
  const [hostToken, setHostToken] = useState<string | null>(null);

  async function doVacuum(): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      setVacuum(await client.adminDBVacuum());
    } catch (e) {
      setError(msg(e));
    }
  }
  async function rotateHost(): Promise<void> {
    if (client === null) return;
    setError(null);
    try {
      const res = await client.adminRotateHostTokens('desktop-upkeep');
      const tok = str(res, 'token') ?? str(res, 'value');
      if (tok !== undefined) setHostToken(tok);
    } catch (e) {
      setError(msg(e));
    }
  }

  return (
    <div className="upkeep">
      <p className="muted">{t('admin.upkeepNote')}</p>
      {error !== null && <div className="error">{error}</div>}

      <div className="admin-row">
        <div>
          <div>{t('admin.dbVacuum')}</div>
          {vacuum !== null && (
            <div className="muted">
              {t('admin.reclaimed')}: {num(vacuum, 'reclaimed') ?? 0} B
            </div>
          )}
        </div>
        <ConfirmButton label={t('admin.dbVacuum')} onConfirm={() => void doVacuum()} />
      </div>

      <div className="admin-row">
        <div>{t('admin.rotateHostTokens')}</div>
        <ConfirmButton label={t('admin.rotateHostTokens')} danger onConfirm={() => void rotateHost()} />
      </div>
      {hostToken !== null && <SecretReveal label={t('admin.newToken')} value={hostToken} />}
    </div>
  );
}

/// Raw text/YAML editor overlay for a template or an agent family. Loads the raw
/// body on mount (getTemplateText / getAgentFamilyText), then PUTs it verbatim —
/// the hub reads templates/families as raw bytes and writes a per-team overlay.
/// A `name === null` opens the composer for a brand-new record.
function YamlEditor({
  target,
  onClose,
}: {
  target:
    | { kind: 'template'; category: string; name: string | null }
    | { kind: 'family'; name: string | null };
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const creating = target.name === null;
  const [name, setName] = useState(target.name ?? '');
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(creating);
  // The text as last loaded — the baseline for the dirty-close guard.
  const [savedText, setSavedText] = useState('');

  useEffect(() => {
    if (creating || client === null) return;
    let live = true;
    void (async () => {
      try {
        const raw =
          target.kind === 'template'
            ? await client.getTemplateText(target.category, target.name as string)
            : await client.getAgentFamilyText(target.name as string);
        if (live) {
          setText(raw);
          setSavedText(raw);
          setLoaded(true);
        }
      } catch (e) {
        if (live) {
          setErr(msg(e));
          setLoaded(true);
        }
      }
    })();
    return () => {
      live = false;
    };
  }, [client, creating, target]);

  const queryKey = target.kind === 'template' ? ['templates'] : ['agent-families'];

  async function save(): Promise<void> {
    if (client === null || name.trim() === '') return;
    setBusy(true);
    setErr(null);
    try {
      if (target.kind === 'template') {
        await client.putTemplate(target.category, name.trim(), text);
      } else {
        await client.putAgentFamily(name.trim(), text);
      }
      await qc.invalidateQueries({ queryKey });
      onClose();
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(): Promise<void> {
    if (client === null || creating) return;
    setBusy(true);
    setErr(null);
    try {
      if (target.kind === 'template') await client.deleteTemplate(target.category, target.name as string);
      else await client.deleteAgentFamily(target.name as string);
      await qc.invalidateQueries({ queryKey });
      onClose();
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  const heading =
    target.kind === 'template'
      ? `${t('admin.template')} · ${target.category}`
      : t('admin.family');

  // Dirty-close guard (#313): backdrop / Escape / Close used to discard unsaved
  // edits silently — confirm before dropping them.
  const dirty = name !== (target.name ?? '') || text !== savedText;
  async function attemptClose(): Promise<void> {
    if (!dirty || (await confirmAsk({ message: t('confirm.discardChanges'), danger: true }))) onClose();
  }

  return (
    <>
    <Modal onClose={() => void attemptClose()} className="sessions-panel" ariaLabel={heading}>
        <div className="admin-tabs">
          <strong>{heading}</strong>
          <span className="spacer" />
          {!creating && <ConfirmButton label={t('admin.delete')} danger onConfirm={() => void remove()} />}
          <button className="primary" disabled={busy || name.trim() === '' || !loaded} onClick={() => void save()}>
            {t('admin.save')}
          </button>
          <button onClick={() => void attemptClose()}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          <label className="wide">
            {t('admin.name')}
            <input className="mono" value={name} disabled={!creating} onChange={(e) => setName(e.target.value)} />
          </label>
          {!loaded ? (
            <div className="muted">{t('common.loading')}</div>
          ) : (
            <textarea
              className="yaml-editor mono"
              value={text}
              spellCheck={false}
              onChange={(e) => setText(e.target.value)}
              placeholder={target.kind === 'family' ? t('admin.familyYamlHint') : t('admin.templateYamlHint')}
            />
          )}
          {err !== null && <div className="error wide">{err}</div>}
        </div>
    </Modal>
    {confirmNode}
    </>
  );
}

function TemplatesTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [edit, setEdit] = useState<{ category: string; name: string | null } | null>(null);
  const q = useQuery({
    queryKey: ['templates'],
    enabled: client !== null,
    queryFn: () => client!.listTemplates(),
  });
  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const templates = q.data ?? [];
  const byCategory = new Map<string, Entity[]>();
  for (const tpl of templates) {
    const cat = str(tpl, 'category') ?? 'other';
    const list = byCategory.get(cat);
    if (list) list.push(tpl);
    else byCategory.set(cat, [tpl]);
  }
  const categories = [...byCategory.keys()].sort();
  return (
    <div className="scroll">
      <p className="muted">{t('admin.templatesNote')}</p>
      {categories.map((cat) => (
        <section key={cat} className="setting-group">
          <div className="setting-row">
            <h3>{cat}</h3>
            <span className="spacer" />
            <button onClick={() => setEdit({ category: cat, name: null })}>+ {t('admin.new')}</button>
          </div>
          {(byCategory.get(cat) ?? []).map((tpl, i) => (
            <button
              key={str(tpl, 'name') ?? String(i)}
              className="admin-row clickable-row"
              onClick={() => setEdit({ category: cat, name: str(tpl, 'name') ?? '' })}
            >
              <span className="mono">{str(tpl, 'name') ?? '—'}</span>
              <span className="spacer" />
              <span className="muted small">{str(tpl, 'source') ?? ''}</span>
            </button>
          ))}
        </section>
      ))}
      {templates.length === 0 && <div className="muted">{t('admin.noTemplates')}</div>}
      {edit !== null && (
        <YamlEditor target={{ kind: 'template', category: edit.category, name: edit.name }} onClose={() => setEdit(null)} />
      )}
    </div>
  );
}

function FamiliesTab(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [edit, setEdit] = useState<{ name: string | null } | null>(null);
  const q = useQuery({
    queryKey: ['agent-families'],
    enabled: client !== null,
    queryFn: () => client!.listAgentFamilies(),
  });
  if (q.isError) return <div className="error">{msg(q.error)}</div>;
  const families = q.data ?? [];
  return (
    <div className="scroll">
      <div className="setting-row">
        <p className="muted">{t('admin.familiesNote')}</p>
        <span className="spacer" />
        <button onClick={() => setEdit({ name: null })}>+ {t('admin.new')}</button>
      </div>
      <table>
        <thead>
          <tr>
            <th>{t('admin.family')}</th>
            <th>{t('admin.bin')}</th>
            <th>{t('admin.source')}</th>
            <th>{t('admin.supports')}</th>
          </tr>
        </thead>
        <tbody>
          {families.map((f, i) => {
            const supports = f['supports'];
            const supText =
              supports !== null && typeof supports === 'object'
                ? Object.entries(supports as Record<string, unknown>)
                    .filter(([, v]) => v === true)
                    .map(([k]) => k)
                    .join(', ')
                : '';
            const fam = str(f, 'family') ?? '';
            return (
              <tr key={fam || String(i)} role="button" className="clickable-row" onClick={() => setEdit({ name: fam })}>
                <td className="mono">{fam || '—'}</td>
                <td className="mono">{str(f, 'bin') ?? ''}</td>
                <td className="muted small">{str(f, 'source') ?? ''}</td>
                <td className="small">{supText}</td>
              </tr>
            );
          })}
          {families.length === 0 && (
            <tr>
              <td colSpan={4} className="muted">
                {t('admin.noFamilies')}
              </td>
            </tr>
          )}
        </tbody>
      </table>
      {edit !== null && <YamlEditor target={{ kind: 'family', name: edit.name }} onClose={() => setEdit(null)} />}
    </div>
  );
}

type AdminTab = 'team' | 'hosts' | 'agents' | 'teams' | 'templates' | 'families' | 'upkeep';

/// WS7 — Team governance + operator Admin cockpit as an overlay. Team tab
/// (members + editable policy); Hosts/Agents admin tabs with confirmed
/// destructive actions; Teams (token rotation) and Upkeep (DB vacuum, host-
/// token rotation). Admin endpoints 403 gracefully for non-operator tokens.
export function AdminCockpit({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const [tab, setTab] = useState<AdminTab>('team');
  const tabs: { v: AdminTab; label: string }[] = [
    { v: 'team', label: t('admin.team') },
    { v: 'hosts', label: t('admin.hosts') },
    { v: 'agents', label: t('admin.agents') },
    { v: 'teams', label: t('admin.teams') },
    { v: 'templates', label: t('admin.templates') },
    { v: 'families', label: t('admin.engines') },
    { v: 'upkeep', label: t('admin.upkeep') },
  ];
  return (
    <Modal onClose={onClose} className="admin" ariaLabel={t('admin.cockpit')}>
        <div className="admin-tabs">
          {tabs.map((x) => (
            <button key={x.v} className={tab === x.v ? 'tab active' : 'tab'} onClick={() => setTab(x.v)}>
              {x.label}
            </button>
          ))}
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="admin-body">
          {tab === 'team' && <TeamTab />}
          {tab === 'hosts' && <HostsTab />}
          {tab === 'agents' && <AgentsTab />}
          {tab === 'teams' && <TeamsTab />}
          {tab === 'templates' && <TemplatesTab />}
          {tab === 'families' && <FamiliesTab />}
          {tab === 'upkeep' && <UpkeepTab />}
        </div>
    </Modal>
  );
}
