import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { sshConnect, type SshConnectReq } from '../ssh/tauri';
import {
  deleteConnection,
  getConnectionPassword,
  listConnections,
  setConnectionPassword,
  touchConnection,
  upsertConnection,
  type Connection,
} from '../state/connections';
import { deleteKey, getKeyMaterial, importKey, listKeys, type SshKeyMeta } from '../state/keys';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

type Auth = 'password' | 'key';

/// Key store manager (parity Phase 2a): import a pasted private key (validated +
/// introspected by the Rust core) and list/remove saved keys. Secrets live in the
/// OS keychain, not here.
function KeyManager({ keys, onChange }: { keys: SshKeyMeta[]; onChange: () => void }): JSX.Element {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [pem, setPem] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function doImport(): Promise<void> {
    setBusy(true);
    setErr(null);
    try {
      await importKey({ name: name.trim() || 'key', pem, passphrase });
      setName('');
      setPem('');
      setPassphrase('');
      setOpen(false);
      onChange();
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="term-keys">
      <div className="term-saved-head">
        <strong>{t('term.keys')}</strong>
        <button onClick={() => setOpen((v) => !v)}>{open ? t('admin.close') : t('term.importKey')}</button>
      </div>
      {open && (
        <div className="key-import">
          <input placeholder={t('term.keyName')} value={name} onChange={(e) => setName(e.target.value)} />
          <textarea
            placeholder={t('term.keyPlaceholder')}
            spellCheck={false}
            value={pem}
            onChange={(e) => setPem(e.target.value)}
          />
          <input
            type="password"
            placeholder={t('term.passphrase')}
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
          />
          {err !== null && <div className="error">{err}</div>}
          <button className="primary" disabled={busy || pem.trim() === ''} onClick={() => void doImport()}>
            {t('term.importKey')}
          </button>
        </div>
      )}
      {keys.map((k) => (
        <div key={k.id} className="key-item">
          <span className="key-name">{k.name}</span>
          <span className="muted small">{k.type}</span>
          <span className="spacer" />
          <button className="link-btn" onClick={() => void deleteKey(k.id).then(onChange)}>
            {t('term.delete')}
          </button>
        </div>
      ))}
      {keys.length === 0 && !open && <div className="muted small">{t('term.noKeys')}</div>}
    </div>
  );
}

/// The SSH connect surface (saved connections + key store over a connect form).
/// Extracted from the old breakglass modal so the terminal dock can present it as
/// a tab; on a successful connect it hands the new session id back up to the dock,
/// which opens a live terminal tab for it (ADR-052 personal direct SSH).
export function ConnectForm({
  onConnected,
  onCancel,
}: {
  onConnected: (sessionId: string, title: string) => void;
  onCancel?: () => void;
}): JSX.Element {
  const t = useT();
  const [id, setId] = useState<string | null>(null);
  const [name, setName] = useState('');
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
  const [conns, setConns] = useState<Connection[]>([]);
  const [keys, setKeys] = useState<SshKeyMeta[]>([]);

  useEffect(() => {
    setConns(listConnections());
    setKeys(listKeys());
  }, []);

  function resetForm(): void {
    setId(null);
    setName('');
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
        host: host.trim(),
        port: Number(port) || 22,
        username: user.trim(),
        authMethod: auth,
        keyId: auth === 'key' && keyId !== '' ? keyId : null,
      });
      if (auth === 'password' && password !== '') await setConnectionPassword(conn.id, password);
      setId(conn.id);
      setConns(listConnections());
    } catch (e) {
      setError(msg(e));
    }
  }

  async function removeConnection(cid: string): Promise<void> {
    await deleteConnection(cid);
    if (id === cid) resetForm();
    setConns(listConnections());
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
      <div className="term-sidebar">
        <div className="term-saved-head">
          <strong>{t('term.saved')}</strong>
          <button onClick={resetForm}>{t('term.newConnection')}</button>
        </div>
        {conns.map((c) => (
          <div key={c.id} className={c.id === id ? 'conn-item active' : 'conn-item'}>
            <button className="conn-pick" onClick={() => void applyConnection(c)}>
              <span className="conn-name">{c.name}</span>
              <span className="muted small">
                {c.username}@{c.host}
                {c.port !== 22 ? `:${c.port}` : ''}
              </span>
            </button>
            <button className="link-btn" onClick={() => void removeConnection(c.id)}>
              {t('term.delete')}
            </button>
          </div>
        ))}
        {conns.length === 0 && <div className="muted small">{t('term.noSaved')}</div>}
        <KeyManager keys={keys} onChange={() => setKeys(listKeys())} />
      </div>

      <div className="term-form">
        <label className="wide">
          {t('term.name')}
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('term.namePlaceholder')} />
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
