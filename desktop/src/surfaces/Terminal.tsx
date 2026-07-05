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

/// Breakglass SSH terminal overlay (ADR-052, personal direct SSH). A connect
/// form (host / user / password or private key), then a live xterm screen.
/// Desktop-only: direct SSH needs the Tauri native core.
export function Terminal({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const [host, setHost] = useState('');
  const [port, setPort] = useState('22');
  const [user, setUser] = useState('');
  const [auth, setAuth] = useState<Auth>('password');
  const [password, setPassword] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const tauri = isTauri();

  async function connect(): Promise<void> {
    setBusy(true);
    setError(null);
    const req: SshConnectReq = {
      host: host.trim(),
      port: Number(port) || 22,
      user: user.trim(),
      cols: 80,
      rows: 24,
      ...(auth === 'password' ? { password } : { private_key: privateKey, passphrase }),
    };
    try {
      const id = await sshConnect(req);
      setSessionId(id);
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  const canConnect =
    host.trim() !== '' && user.trim() !== '' && (auth === 'password' ? password !== '' : privateKey.trim() !== '');

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
          <span className="spacer" />
          {sessionId !== null && <button onClick={() => setSessionId(null)}>{t('term.disconnect')}</button>}
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>

        {!tauri ? (
          <div className="term-banner">{t('term.desktopOnly')}</div>
        ) : sessionId !== null ? (
          <div className="term-body">
            <Screen sessionId={sessionId} onExit={() => {}} />
          </div>
        ) : (
          <div className="term-body">
            <div className="term-form">
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
              {error !== null && <div className="error wide">{error}</div>}
              <div className="wide">
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
