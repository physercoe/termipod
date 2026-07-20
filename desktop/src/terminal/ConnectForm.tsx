import { useEffect, useRef, useState } from 'react';
import type { UnlistenFn } from '@tauri-apps/api/event';
import { useT } from '../i18n';
import {
  onSshConnectPhase,
  sshClose,
  sshConnect,
  type SshConnectPhase,
  type SshConnectReq,
} from '../ssh/tauri';
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
import { useConfirm } from '../ui/ConfirmModal';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

type Auth = 'password' | 'key';

/// Button labels for the handshake stages the core reports on
/// `ssh-connect-progress` (ssh.rs) — the connect button follows them while an
/// attempt is in flight so a slow handshake never looks frozen (#319).
const PHASE_KEY: Record<SshConnectPhase, string> = {
  tcp: 'term.phaseTcp',
  auth: 'term.phaseAuth',
  channel: 'term.phaseChannel',
};

/// Ceiling on one connect attempt (#319): a firewall that silently drops the
/// SYN (or a stuck auth handshake) would otherwise leave the form busy
/// forever. Past this the attempt is abandoned; if the core connects anyway,
/// its orphaned session is closed (see connect()).
const CONNECT_TIMEOUT_MS = 20_000;

/// Attempt ids for `ssh-connect-progress` subscription, minted per click.
let connectSeq = 0;

// Snapshot of the form fields, for the dirty-close guard (#313): cancelling
// with unsaved edits used to drop them silently.
interface FormSnapshot {
  name: string;
  group: string;
  host: string;
  port: string;
  user: string;
  auth: Auth;
  password: string;
  keyId: string;
  privateKey: string;
  passphrase: string;
}
const BLANK_FORM: FormSnapshot = {
  name: '',
  group: DEFAULT_GROUP,
  host: '',
  port: '22',
  user: '',
  auth: 'password',
  password: '',
  keyId: '',
  privateKey: '',
  passphrase: '',
};

/// The SSH connect surface (saved connections + key store over a connect form).
/// Extracted from the old breakglass modal so the terminal dock can present it as
/// a tab; on a successful connect it hands the new session id back up to the dock,
/// which opens a live terminal tab for it (ADR-052 personal direct SSH).
export function ConnectForm({
  onConnected,
  onCancel,
  initialConnId,
}: {
  onConnected: (sessionId: string, title: string, connId?: string) => void;
  onCancel?: () => void;
  /** Preselect this saved connection on mount (nav click in the terminal). */
  initialConnId?: string;
}): JSX.Element {
  const t = useT();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
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
  const [phase, setPhase] = useState<SshConnectPhase | null>(null);
  // The in-flight attempt, flagged by Cancel/timeout — the invoke itself can't
  // be cancelled, so a flagged attempt's late resolution is torn down instead.
  const attemptRef = useRef<{ cancelled: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [keys, setKeys] = useState<SshKeyMeta[]>([]);
  const [base, setBase] = useState<FormSnapshot>(BLANK_FORM);

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
    setBase(BLANK_FORM);
  }

  async function applyConnection(c: Connection): Promise<void> {
    const pw = c.authMethod === 'password' ? ((await getConnectionPassword(c.id)) ?? '') : '';
    const grp = (c.group ?? '').trim() || DEFAULT_GROUP;
    setId(c.id);
    setName(c.name);
    setGroup(grp);
    setHost(c.host);
    setPort(String(c.port));
    setUser(c.username);
    setAuth(c.authMethod);
    setKeyId(c.keyId ?? '');
    setPrivateKey('');
    setPassphrase('');
    setError(null);
    setPassword(pw);
    setBase({
      name: c.name,
      group: grp,
      host: c.host,
      port: String(c.port),
      user: c.username,
      auth: c.authMethod,
      password: pw,
      keyId: c.keyId ?? '',
      privateKey: '',
      passphrase: '',
    });
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
    setPhase(null);
    const attempt = { cancelled: false };
    attemptRef.current = attempt;
    const timer = setTimeout(() => {
      if (attempt.cancelled) return;
      attempt.cancelled = true;
      attemptRef.current = null;
      setBusy(false);
      setPhase(null);
      setError(t('term.connectTimeout'));
    }, CONNECT_TIMEOUT_MS);
    // Minted per attempt and echoed on the core's phase ticks, so a stale
    // attempt's ticks never update this attempt's display.
    const attemptId = `c${++connectSeq}`;
    let unlisten: UnlistenFn | null = null;
    try {
      // Attached before the invoke so no phase tick is missed.
      unlisten = await onSshConnectPhase(attemptId, setPhase);
      const base = {
        host: host.trim(),
        port: Number(port) || 22,
        user: user.trim(),
        cols: 80,
        rows: 24,
        connect_id: attemptId,
      };
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
      if (attempt.cancelled) {
        // Abandoned (Cancel/timeout) but the core connected anyway — close the
        // orphaned session rather than leak a remote shell nobody renders.
        void sshClose(sid);
        return;
      }
      if (id !== null) touchConnection(id);
      onConnected(sid, `${user.trim()}@${host.trim()}`, id ?? undefined);
    } catch (e) {
      if (!attempt.cancelled) setError(msg(e));
    } finally {
      clearTimeout(timer);
      unlisten?.();
      // A newer attempt may already be running (this one timed out and the user
      // retried) — only reset the UI when this attempt is still the current one.
      if (attemptRef.current === attempt) {
        attemptRef.current = null;
        setBusy(false);
        setPhase(null);
      }
    }
  }

  /// Abandon the in-flight attempt (#319). The core-side handshake can't be
  /// cancelled mid-await, so this flags it: connect() tears down a late
  /// success instead of opening a tab for it.
  function cancelConnect(): void {
    const attempt = attemptRef.current;
    if (attempt === null) return;
    attempt.cancelled = true;
    attemptRef.current = null;
    setBusy(false);
    setPhase(null);
  }

  const canConnect =
    host.trim() !== '' &&
    user.trim() !== '' &&
    (auth === 'password' ? password !== '' : keyId !== '' || privateKey.trim() !== '');

  // Dirty-close guard (#313): cancelling with unsaved edits used to drop them
  // silently — confirm before dropping them.
  const dirty =
    name !== base.name ||
    group !== base.group ||
    host !== base.host ||
    port !== base.port ||
    user !== base.user ||
    auth !== base.auth ||
    password !== base.password ||
    keyId !== base.keyId ||
    privateKey !== base.privateKey ||
    passphrase !== base.passphrase;
  async function attemptCancel(): Promise<void> {
    if (!dirty || (await confirmAsk({ message: t('confirm.discardChanges'), danger: true }))) onCancel?.();
  }

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
          {/* One Cancel button at all times: idle it closes the form (guarded
              against unsaved edits, #313); while connecting it abandons the
              attempt (the form stays open for a retry) — the connect button
              shows the handshake phase (#319). */}
          {onCancel !== undefined && !busy && <button onClick={() => void attemptCancel()}>{t('common.cancel')}</button>}
          {busy && <button onClick={cancelConnect}>{t('common.cancel')}</button>}
          <span className="spacer" />
          <button className="primary" disabled={!canConnect || busy} onClick={() => void connect()}>
            {busy ? (phase !== null ? t(PHASE_KEY[phase]) : t('term.connecting')) : t('term.connect')}
          </button>
        </div>
        <div className="term-note wide">{t('term.personalNote')}</div>
        {confirmNode}
      </div>
    </div>
  );
}
