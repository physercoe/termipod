import { useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useT } from '../i18n';
import {
  isTauri,
  onSshData,
  onSshExit,
  sshClose,
  sshConnect,
  sshResize,
  sshWrite,
  type SshConnectReq,
} from '../ssh/tauri';
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
import { TmuxPanel } from './TmuxPanel';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

type Auth = 'password' | 'key';

/// Live xterm.js screen bound to an open SSH session (ADR-052). Owns the
/// terminal instance, streams core `ssh-data` bytes in, and pipes keystrokes +
/// resizes back out. Unmount tears the session down.
function Screen({ sessionId, onExit }: { sessionId: string; onExit: () => void }): JSX.Element {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (el === null) return;
    let disposed = false;
    const term = new XTerm({
      cursorBlink: true,
      fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 13,
      theme: { background: '#000000', foreground: '#e6edf3' },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    void sshResize(sessionId, term.cols, term.rows);

    const onData = term.onData((s) => void sshWrite(sessionId, s));
    const onResize = (): void => {
      fit.fit();
      void sshResize(sessionId, term.cols, term.rows);
    };
    window.addEventListener('resize', onResize);

    const unlistenP = onSshData(sessionId, (b) => term.write(b));
    const exitP = onSshExit(sessionId, () => {
      if (!disposed) term.write('\r\n\x1b[2m[connection closed]\x1b[0m\r\n');
      onExit();
    });
    term.focus();

    return () => {
      disposed = true;
      window.removeEventListener('resize', onResize);
      onData.dispose();
      void unlistenP.then((u) => u());
      void exitP.then((u) => u());
      void sshClose(sessionId);
      term.dispose();
    };
  }, [sessionId, onExit]);

  return <div className="term-screen" ref={ref} />;
}

/// Key store manager (parity Phase 2a): import a pasted private key (validated
/// + introspected by the Rust core) and list/remove saved keys. Secrets live in
/// the OS keychain, not here.
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

/// Breakglass SSH terminal overlay (ADR-052, personal direct SSH). Saved
/// connections + key store (Phase 2a) over a connect form, then a live xterm
/// screen. Desktop-only: direct SSH needs the Tauri native core.
export function Terminal({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const [id, setId] = useState<string | null>(null); // selected saved-connection id
  const [name, setName] = useState('');
  const [host, setHost] = useState('');
  const [port, setPort] = useState('22');
  const [user, setUser] = useState('');
  const [auth, setAuth] = useState<Auth>('password');
  const [password, setPassword] = useState('');
  const [keyId, setKeyId] = useState(''); // selected saved key ('' = paste)
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [connView, setConnView] = useState<'term' | 'tmux'>('term');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conns, setConns] = useState<Connection[]>([]);
  const [keys, setKeys] = useState<SshKeyMeta[]>([]);

  const tauri = isTauri();

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
    if (c.authMethod === 'password') {
      setPassword((await getConnectionPassword(c.id)) ?? '');
    } else {
      setPassword('');
    }
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
      setSessionId(sid);
      if (id !== null) {
        touchConnection(id);
        setConns(listConnections());
      }
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
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="term" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('term.title')}</strong>
          {sessionId !== null && (
            <span className="pill">
              {user}@{host} · {t('term.connected')}
            </span>
          )}
          {sessionId !== null && (
            <div className="tabs">
              <button className={connView === 'term' ? 'tab active' : 'tab'} onClick={() => setConnView('term')}>
                {t('term.terminal')}
              </button>
              <button className={connView === 'tmux' ? 'tab active' : 'tab'} onClick={() => setConnView('tmux')}>
                {t('term.tmux')}
              </button>
            </div>
          )}
          <span className="spacer" />
          {sessionId !== null && <button onClick={() => setSessionId(null)}>{t('term.disconnect')}</button>}
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>

        {!tauri ? (
          <div className="term-banner">{t('term.desktopOnly')}</div>
        ) : sessionId !== null ? (
          <div className="term-body">
            {/* Screen stays mounted across view switches — unmounting it closes
                the SSH session. Hide it (not unmount) when browsing tmux. */}
            <div className={connView === 'term' ? 'term-view' : 'term-view hidden'}>
              <Screen sessionId={sessionId} onExit={() => {}} />
            </div>
            {connView === 'tmux' && <TmuxPanel sessionId={sessionId} />}
          </div>
        ) : (
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
                <span className="spacer" />
                <button className="primary" disabled={!canConnect || busy} onClick={() => void connect()}>
                  {busy ? t('term.connecting') : t('term.connect')}
                </button>
              </div>
              <div className="term-note wide">{t('term.personalNote')}</div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
