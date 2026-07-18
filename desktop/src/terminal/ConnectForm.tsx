import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { sshConnect, type SshConnectReq } from '../ssh/tauri';
import {
  DEFAULT_GROUP,
  deleteConnection,
  getConnectionPassword,
  listConnections,
  navGroups,
  setConnectionPassword,
  touchConnection,
  upsertConnection,
  type Connection,
} from '../state/connections';
import { getKeyMaterial, listKeys, type SshKeyMeta } from '../state/keys';
import { ConfirmButton } from '../ui/ConfirmButton';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

type Auth = 'password' | 'key';

/// The SSH connect surface (saved connections + key store over a connect form).
/// Extracted from the old breakglass modal so the terminal dock can present it as
/// a tab; on a successful connect it hands the new session id back up to the dock,
/// which opens a live terminal tab for it (ADR-052 personal direct SSH).
export function ConnectForm({
  onConnected,
  onCancel,
  initialConnId,
}: {
  onConnected: (sessionId: string, title: string) => void;
  onCancel?: () => void;
  /** Preselect this saved connection on mount (nav click in the terminal). */
  initialConnId?: string;
}): JSX.Element {
  const t = useT();
  const [id, setId] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [group, setGroup] = useState(DEFAULT_GROUP);
  const [host, setHost] = useState('');
  const [port, setPort] = useState('22');
  const [user, setUser] = useState('');
  const [auth, setAuth] = useState<Auth>('password');
  const [password, setPassword] = useState('');
  const [keyId, setKeyId] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [keys, setKeys] = useState<SshKeyMeta[]>([]);

  useEffect(() => {
    setKeys(listKeys());
    if (initialConnId !== undefined) {
      const c = listConnections().find((x) => x.id === initialConnId);
      if (c !== undefined) void applyConnection(c);
    }
    // Prefill runs once on mount (the form remounts per open — keyed on the
    // selected connection in the terminal nav).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function resetForm(): void {
    setId(null);
    setName('');
    setGroup(DEFAULT_GROUP);
    setHost('');
    setPort('22');
    setUser('');
    setAuth('password');
    setPassword('');
    setKeyId('');
    setPrivateKey('');
    setPassphrase('');
    setError(null);
  }

  async function applyConnection(c: Connection): Promise<void> {
    setId(c.id);
    setName(c.name);
    setGroup((c.group ?? '').trim() || DEFAULT_GROUP);
    setHost(c.host);
    setPort(String(c.port));
    setUser(c.username);
    setAuth(c.authMethod);
    setKeyId(c.keyId ?? '');
    setPrivateKey('');
    setPassphrase('');
    setError(null);
    setPassword(c.authMethod === 'password' ? ((await getConnectionPassword(c.id)) ?? '') : '');
  }

  async function saveCurrent(): Promise<void> {
    setError(null);
    try {
      const conn = upsertConnection({
        id: id ?? undefined,
        name: name.trim() || host.trim(),
        group: group.trim() || DEFAULT_GROUP,
        host: host.trim(),
        port: Number(port) || 22,
        username: user.trim(),
        authMethod: auth,
        keyId: auth === 'key' && keyId !== '' ? keyId : null,
      });
      if (auth === 'password' && password !== '') await setConnectionPassword(conn.id, password);
      setId(conn.id);
    } catch (e) {
      setError(msg(e));
    }
  }

  // Delete the connection being edited, then close (the terminal nav — the single
  // source of truth for the saved list — refreshes when this form unmounts).
  async function removeCurrent(): Promise<void> {
    if (id === null) return;
    try {
      await deleteConnection(id);
      onCancel?.();
    } catch (e) {
      setError(msg(e));
    }
  }

  async function connect(): Promise<void> {
    setBusy(true);
    setError(null);
    try {
      const base = { host: host.trim(), port: Number(port) || 22, user: user.trim(), cols: 80, rows: 24 };
      let req: SshConnectReq;
      if (auth === 'password') {
        req = { ...base, password };
      } else if (keyId !== '') {
        const { pem, passphrase: pass } = await getKeyMaterial(keyId);
        if (pem === null) throw new Error(t('term.keyMissing'));
        req = { ...base, private_key: pem, passphrase: pass ?? undefined };
      } else {
        req = { ...base, private_key: privateKey, passphrase };
      }
      const sid = await sshConnect(req);
      if (id !== null) touchConnection(id);
      onConnected(sid, `${user.trim()}@${host.trim()}`);
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  const canConnect =
    host.trim() !== '' &&
    user.trim() !== '' &&
    (auth === 'password' ? password !== '' : keyId !== '' || privateKey.trim() !== '');

  return (
    <div className="term-body term-connect">
      <div className="term-form">
        <div className="term-form-head">
          <strong>{id !== null ? t('term.editConnection') : t('term.newConnection')}</strong>
          {id !== null && (
            <button onClick={resetForm}>{t('term.newConnection')}</button>
          )}
        </div>
        <label className="wide">
          {t('term.name')}
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('term.namePlaceholder')} />
        </label>
        <label className="wide">
          {t('term.group')}
          {/* Free-text with a datalist of existing groups: pick one or type a new
              name (it materialises on save). */}
          <input
            value={group}
            list="term-group-list"
            onChange={(e) => setGroup(e.target.value)}
            placeholder={DEFAULT_GROUP}
          />
          <datalist id="term-group-list">
            {navGroups().map((g) => (
              <option key={g} value={g} />
            ))}
          </datalist>
        </label>
        <label className="wide">
          {t('term.host')}
          <input value={host} onChange={(e) => setHost(e.target.value)} placeholder="host.example.com" />
        </label>
        <label>
          {t('term.port')}
          <input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" />
        </label>
        <label>
          {t('term.user')}
          <input value={user} onChange={(e) => setUser(e.target.value)} />
        </label>
        <label className="wide">
          {t('term.auth')}
          <div className="seg">
            <button className={auth === 'password' ? 'seg-btn active' : 'seg-btn'} onClick={() => setAuth('password')}>
              {t('term.password')}
            </button>
            <button className={auth === 'key' ? 'seg-btn active' : 'seg-btn'} onClick={() => setAuth('key')}>
              {t('term.privateKey')}
            </button>
          </div>
        </label>
        {auth === 'password' ? (
          <label className="wide">
            {t('term.password')}
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </label>
        ) : (
          <>
            <label className="wide">
              {t('term.useKey')}
              <select value={keyId} onChange={(e) => setKeyId(e.target.value)}>
                <option value="">{t('term.pasteKey')}</option>
                {keys.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name} ({k.type})
                  </option>
                ))}
              </select>
            </label>
            {keyId === '' && (
              <>
                <label className="wide">
                  {t('term.privateKey')}
                  <textarea
                    value={privateKey}
                    spellCheck={false}
                    placeholder={t('term.keyPlaceholder')}
                    onChange={(e) => setPrivateKey(e.target.value)}
                  />
                </label>
                <label className="wide">
                  {t('term.passphrase')}
                  <input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
                </label>
              </>
            )}
          </>
        )}
        {error !== null && <div className="error wide">{error}</div>}
        <div className="wide term-actions">
          <button disabled={host.trim() === '' || user.trim() === ''} onClick={() => void saveCurrent()}>
            {id !== null ? t('term.update') : t('term.save')}
          </button>
          {id !== null && <ConfirmButton label={t('term.delete')} danger onConfirm={() => void removeCurrent()} />}
          {onCancel !== undefined && <button onClick={onCancel}>{t('common.cancel')}</button>}
          <span className="spacer" />
          <button className="primary" disabled={!canConnect || busy} onClick={() => void connect()}>
            {busy ? t('term.connecting') : t('term.connect')}
          </button>
        </div>
        <div className="term-note wide">{t('term.personalNote')}</div>
      </div>
    </div>
  );
}
