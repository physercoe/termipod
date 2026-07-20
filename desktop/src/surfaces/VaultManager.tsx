import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { isTauri, openExternal } from '../platform';
import { copySecret } from '../state/clipboard';
import { DEFAULT_GEN, generatePassword, passwordStrength } from '../state/password';
import { parseSeed, secondsRemaining, totpCode, type TotpParams } from '../state/totp';
import { Icon, type IconName } from '../ui/Icon';
import { PasswordInput } from '../ui/PasswordInput';
import { listAppIntegrations, type AppIntegration } from '../state/appIntegrations';
import { listConnections } from '../state/connections';
import { secretDelete, secretGet, secretSet } from '../state/persist';
import { useAutolock, useVaultLock } from '../state/vaultLock';
import { runScript, type ScriptResult } from '../state/scriptRun';
import { useWorkspace } from '../state/workspace';
import {
  deleteItem,
  getItemSecret,
  listItems,
  saveItem,
  toggleFavorite,
  type VaultItemMeta,
  type VaultItemType,
} from '../state/vaultItems';
import { SshKeysSettings } from './SshKeys';

/// The vault item manager — a compact, 1Password/Bitwarden-style credential
/// store layered over the app's keychain-backed secret layer. Everything the
/// vault protects is visible and manageable in one place: generic Logins, API
/// tokens and Secure notes (this store), plus read views into the SSH keys and
/// saved connections that already seal into the same synced vault.
///
/// Layout follows the well-worn pattern: a category tab strip, a searchable item
/// list, and a detail/editor pane. Secret fields are masked with reveal + copy
/// affordances and are only read from the keychain on demand.

type Tab =
  | 'all'
  | 'favorites'
  | 'login'
  | 'api'
  | 'note'
  | 'env'
  | 'script'
  | 'termipod'
  | 'sshkeys'
  | 'connections';
const GENERIC: readonly VaultItemType[] = ['login', 'api', 'note', 'env', 'script'];

// Informational shape of an env/config blob, and the interpreters a script item
// can be run with (mirrors the Rust allowlist in script.rs).
const ENV_FORMATS = ['dotenv', 'shell', 'json', 'yaml', 'plain'] as const;
const INTERPRETERS = ['bash', 'sh', 'zsh', 'python', 'node', 'pwsh', 'ruby'] as const;

// Soft ceiling per item: not enforced (the item still saves), just a nudge. Every
// vault secret shares one keychain document that loads whole into memory and
// re-seals into each sync bundle, so multi-MB bodies tax every launch and sync.
const SOFT_MAX_BYTES = 64 * 1024;
const byteLen = (s: string): number => new TextEncoder().encode(s).length;

function typeIcon(type: VaultItemType): IconName {
  switch (type) {
    case 'login':
      return 'key';
    case 'api':
      return 'code';
    case 'env':
      return 'sliders';
    case 'script':
      return 'terminal';
    default:
      return 'note';
  }
}

function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// ── Field renderers ─────────────────────────────────────────────────────────

function PlainRow({ label, value, link }: { label: string; value: string; link?: boolean }): JSX.Element | null {
  const t = useT();
  const [copied, setCopied] = useState(false);
  if (value === '') return null;
  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard blocked */
    }
  }
  return (
    <div className="vault-field">
      <span className="vault-field-label">{label}</span>
      {link ? (
        <button className="vault-field-val vault-link" onClick={() => openExternal(value)} title={value}>
          {value}
        </button>
      ) : (
        <span className="vault-field-val">{value}</span>
      )}
      <button className="icon-btn" title={copied ? t('vault.copied') : t('vault.copy')} onClick={() => void copy()}>
        <Icon name={copied ? 'check' : 'copy'} size={15} />
      </button>
    </div>
  );
}

/// A masked secret with reveal + copy. The plaintext is fetched from the keychain
/// lazily (first reveal/copy) and cached for the lifetime of the row. `block`
/// renders multi-line values (note bodies) as a pre block.
function SecretRow({
  itemId,
  slot,
  label,
  block,
}: {
  itemId: string;
  slot: string;
  label: string;
  block?: boolean;
}): JSX.Element {
  const t = useT();
  const [val, setVal] = useState<string | null>(null);
  const [show, setShow] = useState(false);
  const [copied, setCopied] = useState(false);
  const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Auto-hide a revealed secret after 20s so it doesn't linger on-screen (#320).
  useEffect(() => {
    if (!show) return;
    hideTimer.current = setTimeout(() => setShow(false), 20_000);
    return () => {
      if (hideTimer.current !== undefined) clearTimeout(hideTimer.current);
    };
  }, [show]);

  async function load(): Promise<string> {
    if (val !== null) return val;
    const v = await getItemSecret(itemId, slot);
    setVal(v);
    return v;
  }
  async function reveal(): Promise<void> {
    await load();
    setShow((s) => !s);
  }
  async function copy(): Promise<void> {
    const v = await load();
    if (await copySecret(v)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  }

  const actions = (
    <>
      <button className="icon-btn" title={show ? t('vault.hide') : t('vault.reveal')} onClick={() => void reveal()}>
        <Icon name={show ? 'eye-off' : 'eye'} size={15} />
      </button>
      <button className="icon-btn" title={copied ? t('vault.copied') : t('vault.copy')} onClick={() => void copy()}>
        <Icon name={copied ? 'check' : 'copy'} size={15} />
      </button>
    </>
  );

  if (block === true) {
    return (
      <div className="vault-field vault-field-block">
        <div className="vault-field-blockhead">
          <span className="vault-field-label">{label}</span>
          <span className="spacer" />
          {actions}
        </div>
        {show && <pre className="vault-note mono">{val ?? ''}</pre>}
      </div>
    );
  }
  return (
    <div className="vault-field">
      <span className="vault-field-label">{label}</span>
      <span className="vault-field-val mono">{show ? (val ?? '') : '••••••••••'}</span>
      {actions}
    </div>
  );
}

/// A live RFC 6238 TOTP code for a login's stored seed (#320): recomputed on a
/// 1s tick with a countdown ring sweeping the seed's period, and Copy routes
/// through `copySecret` so the clipboard auto-clears like every other vault
/// secret. The seed is fetched from the keychain once on mount — never shown,
/// only its rolling code.
function TotpRow({ itemId, label }: { itemId: string; label: string }): JSX.Element {
  const t = useT();
  const [params, setParams] = useState<TotpParams | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [code, setCode] = useState('');
  const [remain, setRemain] = useState(0);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let alive = true;
    void getItemSecret(itemId, 'totp').then((v) => {
      if (alive) {
        setParams(parseSeed(v));
        setLoaded(true);
      }
    });
    return () => {
      alive = false;
    };
  }, [itemId]);

  useEffect(() => {
    if (params === null) return;
    const p = params; // const alias so the narrowing holds inside the tick closure
    let alive = true;
    async function tick(): Promise<void> {
      const c = await totpCode(p);
      if (alive) {
        setCode(c);
        setRemain(secondsRemaining(p.period));
      }
    }
    void tick();
    const iv = setInterval(() => void tick(), 1000);
    return () => {
      alive = false;
      clearInterval(iv);
    };
  }, [params]);

  async function copy(): Promise<void> {
    if (code !== '' && (await copySecret(code))) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  }

  // Countdown ring: dash-offset sweeps with the seconds left in the period.
  const R = 7;
  const C = 2 * Math.PI * R;
  return (
    <div className="vault-field">
      <span className="vault-field-label">{label}</span>
      {loaded && params === null ? (
        <span className="vault-field-val muted small">{t('vault.totpInvalid')}</span>
      ) : (
        <span className="vault-field-val mono vault-totp-code">{code}</span>
      )}
      {params !== null && (
        <svg
          className="vault-totp-ring"
          width="18"
          height="18"
          viewBox="0 0 18 18"
          role="img"
          aria-label={t('vault.totpNext').replace('{n}', String(remain))}
        >
          <circle className="vault-totp-ring-bg" cx="9" cy="9" r={R} />
          <circle
            className="vault-totp-ring-fg"
            cx="9"
            cy="9"
            r={R}
            strokeDasharray={C}
            strokeDashoffset={C * (1 - remain / params.period)}
            transform="rotate(-90 9 9)"
          />
        </svg>
      )}
      <button className="icon-btn" title={copied ? t('vault.copied') : t('vault.copy')} onClick={() => void copy()}>
        <Icon name={copied ? 'check' : 'copy'} size={15} />
      </button>
    </div>
  );
}

/// Quick-run a script item: a two-step armed Run (executing is consequential and
/// `window.confirm` is unreliable in WebView2), then invoke the Rust runner and
/// show stdout/stderr/exit inline. Runs in the open workspace folder when there
/// is one, so a bootstrap script sees the director's files.
function ScriptRunner({ item }: { item: VaultItemMeta }): JSX.Element {
  const t = useT();
  const cwd = useWorkspace((s) => s.folder);
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<ScriptResult | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function run(): Promise<void> {
    setArmed(false);
    setBusy(true);
    setErr(null);
    setRes(null);
    try {
      const content = await getItemSecret(item.id, 'content');
      if (content.trim() === '') {
        setErr(t('vault.scriptEmpty'));
        return;
      }
      setRes(await runScript(item.interpreter || 'bash', content, cwd ?? undefined));
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="vault-run">
      <div className="vault-run-bar">
        {armed ? (
          <button className="primary" disabled={busy} onClick={() => void run()}>
            {t('vault.runConfirm')}
          </button>
        ) : (
          <button disabled={busy} onClick={() => setArmed(true)}>
            <Icon name="terminal" size={14} /> {busy ? t('vault.running') : t('vault.run')}
          </button>
        )}
        {armed && !busy && <span className="muted small">{t('vault.runArmedHint')}</span>}
        {cwd !== null && <span className="muted small mono vault-run-cwd">{t('vault.runIn')}</span>}
      </div>
      {err !== null && <div className="error small">{err}</div>}
      {res !== null && (
        <div className="vault-run-out">
          <div className={`vault-run-code${(res.code ?? 1) === 0 ? ' ok' : ' bad'}`}>
            {res.timedOut ? t('vault.runTimeout') : t('vault.exitCode').replace('{n}', String(res.code ?? '?'))}
          </div>
          {res.stdout !== '' && <pre className="vault-run-pre mono">{res.stdout}</pre>}
          {res.stderr !== '' && <pre className="vault-run-pre mono err">{res.stderr}</pre>}
        </div>
      )}
    </div>
  );
}

// ── Detail (read) view ──────────────────────────────────────────────────────

function ItemDetail({
  item,
  onEdit,
  onDelete,
}: {
  item: VaultItemMeta;
  onEdit: () => void;
  onDelete: () => void;
}): JSX.Element {
  const t = useT();
  const hasNotes = item.secretSlots.includes('notes');
  // Two-step delete — `window.confirm` is unreliable in the Tauri webview
  // (WebView2), so the trash button arms first, then a second click confirms.
  const [armed, setArmed] = useState(false);
  return (
    <div className="vault-detail">
      <div className="vault-detail-head">
        <Icon name={typeIcon(item.type)} size={18} className="vault-detail-icon" />
        <span className="vault-detail-title">{item.title}</span>
        <span className="spacer" />
        <button className="icon-btn" title={t('vault.edit')} onClick={onEdit}>
          <Icon name="pen" size={15} />
        </button>
        {armed ? (
          <button className="link-btn danger" onClick={onDelete}>
            {t('vault.confirmDeleteShort')}
          </button>
        ) : (
          <button
            className="icon-btn danger"
            title={t('vault.delete')}
            onClick={() => {
              setArmed(true);
              setTimeout(() => setArmed(false), 3000);
            }}
          >
            <Icon name="trash" size={15} />
          </button>
        )}
      </div>

      {item.type === 'login' && (
        <>
          <PlainRow label={t('vault.fUsername')} value={item.username} />
          <PlainRow label={t('vault.fUrl')} value={item.url} link />
          <SecretRow itemId={item.id} slot="password" label={t('vault.fPassword')} />
          {item.secretSlots.includes('totp') && <TotpRow itemId={item.id} label={t('vault.fTotp')} />}
        </>
      )}
      {item.type === 'api' && (
        <>
          <PlainRow label={t('vault.fEndpoint')} value={item.endpoint} />
          <SecretRow itemId={item.id} slot="token" label={t('vault.fToken')} />
        </>
      )}
      {item.type === 'note' && <SecretRow itemId={item.id} slot="content" label={t('vault.fContent')} block />}
      {item.type === 'env' && (
        <>
          <PlainRow label={t('vault.fFormat')} value={item.format} />
          <SecretRow itemId={item.id} slot="content" label={t('vault.fEnv')} block />
        </>
      )}
      {item.type === 'script' && (
        <>
          <PlainRow label={t('vault.fInterpreter')} value={item.interpreter} />
          <SecretRow itemId={item.id} slot="content" label={t('vault.fScript')} block />
          {isTauri() && <ScriptRunner item={item} />}
        </>
      )}

      {hasNotes && <SecretRow itemId={item.id} slot="notes" label={t('vault.fNotes')} block />}
    </div>
  );
}

/// Password input with a reveal toggle, a one-click generator, and a live
/// strength meter (#320).
function PasswordField({ value, onChange }: { value: string; onChange: (v: string) => void }): JSX.Element {
  const t = useT();
  const [show, setShow] = useState(false);
  const s = passwordStrength(value);
  return (
    <div className="vault-pw">
      <div className="vault-pw-row">
        <input
          type={show ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
        <button type="button" className="icon-btn" title={show ? t('vault.hide') : t('vault.reveal')} onClick={() => setShow((v) => !v)}>
          <Icon name={show ? 'eye-off' : 'eye'} size={15} />
        </button>
        <button type="button" className="icon-btn" title={t('vault.generate')} onClick={() => { onChange(generatePassword(DEFAULT_GEN)); setShow(true); }}>
          <Icon name="refresh" size={15} />
        </button>
      </div>
      {value !== '' && (
        <div className="vault-pw-meter" title={`${s.bits} bits`}>
          <div className={`vault-pw-bar strength-${s.label}`} style={{ width: `${Math.round(s.fraction * 100)}%` }} />
          <span className="vault-pw-label muted small">{t(`vault.strength.${s.label}`)}</span>
        </div>
      )}
    </div>
  );
}

// ── Editor ──────────────────────────────────────────────────────────────────

function ItemEditor({
  item,
  type,
  onSaved,
  onCancel,
}: {
  item: VaultItemMeta | null;
  type: VaultItemType;
  onSaved: () => void;
  onCancel: () => void;
}): JSX.Element {
  const t = useT();
  const [title, setTitle] = useState(item?.title ?? '');
  const [favorite, setFavorite] = useState(item?.favorite ?? false);
  const [username, setUsername] = useState(item?.username ?? '');
  const [url, setUrl] = useState(item?.url ?? '');
  const [endpoint, setEndpoint] = useState(item?.endpoint ?? '');
  const [format, setFormat] = useState(item?.format !== undefined && item.format !== '' ? item.format : 'dotenv');
  const [interpreter, setInterpreter] = useState(
    item?.interpreter !== undefined && item.interpreter !== '' ? item.interpreter : 'bash',
  );
  const [password, setPassword] = useState('');
  const [totp, setTotp] = useState('');
  const [token, setToken] = useState('');
  const [content, setContent] = useState('');
  const [notes, setNotes] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Gate Save until existing secrets have loaded — otherwise saving before the
  // async preload resolves would write empty strings and wipe the real values.
  const [loading, setLoading] = useState(item !== null);

  // Preload existing secrets for edit (blank for a brand-new item).
  useEffect(() => {
    if (item === null) return;
    void (async () => {
      if (item.secretSlots.includes('password')) setPassword(await getItemSecret(item.id, 'password'));
      if (item.secretSlots.includes('totp')) setTotp(await getItemSecret(item.id, 'totp'));
      if (item.secretSlots.includes('token')) setToken(await getItemSecret(item.id, 'token'));
      if (item.secretSlots.includes('content')) setContent(await getItemSecret(item.id, 'content'));
      if (item.secretSlots.includes('notes')) setNotes(await getItemSecret(item.id, 'notes'));
      setLoading(false);
    })();
    // Preload runs once — the editor is keyed per open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function save(): Promise<void> {
    if (title.trim() === '') {
      setErr(t('vault.titleRequired'));
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const secrets: Record<string, string> = { notes };
      if (type === 'login') {
        secrets.password = password;
        secrets.totp = totp;
      }
      if (type === 'api') secrets.token = token;
      if (type === 'note' || type === 'env' || type === 'script') secrets.content = content;
      await saveItem({
        id: item?.id,
        type,
        title: title.trim(),
        favorite,
        username,
        url,
        endpoint,
        format,
        interpreter,
        secrets,
      });
      onSaved();
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="vault-editor">
      <div className="vault-detail-head">
        <Icon name={typeIcon(type)} size={18} className="vault-detail-icon" />
        <span className="vault-detail-title">
          {item === null ? t(`vault.new_${type}`) : t('vault.edit')}
        </span>
        <span className="spacer" />
        <button
          className={favorite ? 'icon-btn fav-on' : 'icon-btn'}
          title={t('vault.favorite')}
          onClick={() => setFavorite((v) => !v)}
        >
          <Icon name="star" size={16} />
        </button>
      </div>

      <label className="vault-lbl">
        {t('vault.fTitle')}
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t('vault.fTitle')} autoFocus />
      </label>

      {type === 'login' && (
        <>
          <label className="vault-lbl">
            {t('vault.fUsername')}
            <input value={username} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="vault-lbl">
            {t('vault.fUrl')}
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" />
          </label>
          <label className="vault-lbl">
            {t('vault.fPassword')}
            <PasswordField value={password} onChange={setPassword} />
          </label>
          <label className="vault-lbl">
            {t('vault.fTotp')}
            <PasswordInput
              value={totp}
              onChange={(e) => setTotp(e.target.value)}
              placeholder={t('vault.totpPlaceholder')}
              autoComplete="off"
              spellCheck={false}
            />
          </label>
        </>
      )}
      {type === 'api' && (
        <>
          <label className="vault-lbl">
            {t('vault.fEndpoint')}
            <input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} placeholder="https://api.example.com" />
          </label>
          <label className="vault-lbl">
            {t('vault.fToken')}
            <textarea className="vault-secret-area" spellCheck={false} value={token} onChange={(e) => setToken(e.target.value)} />
          </label>
        </>
      )}
      {type === 'note' && (
        <label className="vault-lbl">
          {t('vault.fContent')}
          <textarea className="vault-note-area" spellCheck={false} value={content} onChange={(e) => setContent(e.target.value)} />
        </label>
      )}
      {type === 'env' && (
        <>
          <label className="vault-lbl">
            {t('vault.fFormat')}
            <select value={format} onChange={(e) => setFormat(e.target.value)}>
              {ENV_FORMATS.map((f) => (
                <option key={f} value={f}>
                  {f}
                </option>
              ))}
            </select>
          </label>
          <label className="vault-lbl">
            {t('vault.fEnv')}
            <textarea
              className="vault-note-area mono"
              spellCheck={false}
              value={content}
              placeholder={'KEY=value\nAPI_TOKEN=…'}
              onChange={(e) => setContent(e.target.value)}
            />
          </label>
        </>
      )}
      {type === 'script' && (
        <>
          <label className="vault-lbl">
            {t('vault.fInterpreter')}
            <select value={interpreter} onChange={(e) => setInterpreter(e.target.value)}>
              {INTERPRETERS.map((i) => (
                <option key={i} value={i}>
                  {i}
                </option>
              ))}
            </select>
          </label>
          <label className="vault-lbl">
            {t('vault.fScript')}
            <textarea
              className="vault-note-area mono"
              spellCheck={false}
              value={content}
              placeholder={'#!/usr/bin/env bash\nset -euo pipefail\n…'}
              onChange={(e) => setContent(e.target.value)}
            />
          </label>
        </>
      )}

      <label className="vault-lbl">
        {t('vault.fNotes')}
        <textarea className="vault-note-area" spellCheck={false} value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>

      {err !== null && <div className="error">{err}</div>}
      {(() => {
        // Sum the item's secret-bearing fields — that's its footprint in the
        // shared keychain document. Non-blocking; big items still save.
        const bytes = byteLen(password) + byteLen(totp) + byteLen(token) + byteLen(content) + byteLen(notes);
        if (bytes <= SOFT_MAX_BYTES) return null;
        return (
          <div className="vault-size-warn small">
            {`${Math.round(bytes / 1024)} KB · ${t('vault.sizeWarn')}`}
          </div>
        );
      })()}
      <div className="vault-editor-actions">
        <button className="primary" disabled={busy || loading} onClick={() => void save()}>
          {t('vault.save')}
        </button>
        <button onClick={onCancel}>{t('vault.cancel')}</button>
      </div>
    </div>
  );
}

// ── TermiPod tab: the app's own integrations (WebDAV/S3 sync + voice key) ─────

/// A raw keychain-slot secret (not a vault item) with reveal / copy / inline edit.
/// Used for the app-integration secrets, which live under fixed keychain keys.
function AppSecretRow({ slot, label }: { slot: string; label: string }): JSX.Element {
  const t = useT();
  const [val, setVal] = useState<string | null>(null);
  const [show, setShow] = useState(false);
  const [copied, setCopied] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);

  // Load once on mount so "set" vs "not set" renders correctly before a reveal.
  // Cheap: all app secrets live in the one cached keychain document.
  useEffect(() => {
    void secretGet(slot).then((v) => setVal(v ?? ''));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function load(): Promise<string> {
    if (val !== null) return val;
    const v = (await secretGet(slot)) ?? '';
    setVal(v);
    return v;
  }
  async function reveal(): Promise<void> {
    await load();
    setShow((s) => !s);
  }
  async function copy(): Promise<void> {
    const v = await load();
    if (await copySecret(v)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  }
  async function startEdit(): Promise<void> {
    setDraft(await load());
    setEditing(true);
  }
  async function saveEdit(): Promise<void> {
    setBusy(true);
    try {
      if (draft === '') await secretDelete(slot);
      else await secretSet(slot, draft);
      setVal(draft);
      setEditing(false);
    } finally {
      setBusy(false);
    }
  }

  const hasValue = val !== null ? val !== '' : true; // assume set until loaded
  if (editing) {
    return (
      <div className="vault-field vault-field-block">
        <div className="vault-field-blockhead">
          <span className="vault-field-label">{label}</span>
        </div>
        <PasswordInput
          className="vault-app-edit"
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <div className="vault-editor-actions">
          <button className="primary" disabled={busy} onClick={() => void saveEdit()}>
            {t('vault.save')}
          </button>
          <button disabled={busy} onClick={() => setEditing(false)}>
            {t('vault.cancel')}
          </button>
        </div>
      </div>
    );
  }
  return (
    <div className="vault-field">
      <span className="vault-field-label">{label}</span>
      <span className="vault-field-val mono">
        {show ? (val ?? '') : hasValue ? '••••••••••' : <span className="muted">{t('vault.tpNotSet')}</span>}
      </span>
      <button className="icon-btn" title={show ? t('vault.hide') : t('vault.reveal')} onClick={() => void reveal()}>
        <Icon name={show ? 'eye-off' : 'eye'} size={15} />
      </button>
      <button className="icon-btn" title={copied ? t('vault.copied') : t('vault.copy')} onClick={() => void copy()}>
        <Icon name={copied ? 'check' : 'copy'} size={15} />
      </button>
      <button className="icon-btn" title={t('vault.edit')} onClick={() => void startEdit()}>
        <Icon name="pen" size={15} />
      </button>
    </div>
  );
}

function IntegrationCard({ it }: { it: AppIntegration }): JSX.Element {
  const t = useT();
  return (
    <div className="vault-app-card">
      <div className="vault-app-head">
        <Icon name={it.icon} size={16} className="vault-detail-icon" />
        <span className="vault-detail-title">{t(it.titleKey)}</span>
      </div>
      {it.info.map((row) => (
        <PlainRow key={row.labelKey} label={t(row.labelKey)} value={row.value} />
      ))}
      {it.secrets.map((s) => (
        <AppSecretRow key={s.slot} slot={s.slot} label={t(s.labelKey)} />
      ))}
    </div>
  );
}

function VaultTermipod(): JSX.Element {
  const t = useT();
  const integrations = listAppIntegrations();
  return (
    <div className="vault-conns">
      <p className="muted small">{t('vault.tpHint')}</p>
      {integrations.map((it) => (
        <IntegrationCard key={it.id} it={it} />
      ))}
    </div>
  );
}

// ── Read view: saved connections (edited in the terminal; synced here) ────────

function VaultConnections(): JSX.Element {
  const t = useT();
  const conns = listConnections();
  return (
    <div className="vault-conns">
      <p className="muted small">{t('vault.connsHint')}</p>
      {conns.length === 0 && <div className="muted small">{t('term.noSaved')}</div>}
      {conns.map((c) => (
        <div key={c.id} className="vault-conn-row">
          <span className="term-tab-kind ssh" />
          <span className="vault-conn-name">{c.name}</span>
          <span className="spacer" />
          <span className="muted small mono">
            {c.username}@{c.host}
            {c.port !== 22 ? `:${c.port}` : ''}
          </span>
          <span className="pill">{c.authMethod === 'key' ? t('term.privateKey') : t('term.password')}</span>
        </div>
      ))}
    </div>
  );
}

// ── Manager ──────────────────────────────────────────────────────────────────

export function VaultManager(): JSX.Element {
  const t = useT();
  useAutolock();
  const locked = useVaultLock((s) => s.locked);
  const lock = useVaultLock((s) => s.lock);
  const unlock = useVaultLock((s) => s.unlock);
  const [items, setItems] = useState<VaultItemMeta[]>(() => listItems());
  const [tab, setTab] = useState<Tab>('all');
  const [q, setQ] = useState('');
  const [sel, setSel] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ item: VaultItemMeta | null; type: VaultItemType } | null>(null);
  const [newMenu, setNewMenu] = useState(false);

  const reload = (): void => setItems(listItems());

  const tabs: { id: Tab; label: string }[] = [
    { id: 'all', label: t('vault.tabAll') },
    { id: 'favorites', label: t('vault.tabFav') },
    { id: 'login', label: t('vault.tabLogins') },
    { id: 'api', label: t('vault.tabTokens') },
    { id: 'note', label: t('vault.tabNotes') },
    { id: 'env', label: t('vault.tabEnv') },
    { id: 'script', label: t('vault.tabScripts') },
    { id: 'sshkeys', label: t('vault.tabKeys') },
    { id: 'connections', label: t('vault.tabConns') },
    { id: 'termipod', label: t('vault.tabTermipod') },
  ];

  const query = q.trim().toLowerCase();
  const listed = items
    .filter((i) => {
      if (tab === 'favorites') return i.favorite;
      if ((GENERIC as readonly string[]).includes(tab)) return i.type === tab;
      return tab === 'all';
    })
    .filter((i) =>
      query === '' ? true : [i.title, i.username, i.url, i.endpoint].some((x) => x.toLowerCase().includes(query)),
    )
    .sort((a, b) => Number(b.favorite) - Number(a.favorite) || a.title.localeCompare(b.title));

  const selItem = items.find((i) => i.id === sel) ?? null;

  function openNew(type: VaultItemType): void {
    setEditing({ item: null, type });
    setSel(null);
    setNewMenu(false);
  }
  function onSaved(): void {
    reload();
    setEditing(null);
  }
  async function remove(id: string): Promise<void> {
    await deleteItem(id);
    reload();
    if (sel === id) setSel(null);
  }
  function fav(id: string): void {
    setItems(toggleFavorite(id));
  }

  const showItems = tab !== 'sshkeys' && tab !== 'connections' && tab !== 'termipod';

  if (locked) {
    return (
      <div className="vault-mgr">
        <div className="vault-locked">
          <Icon name="lock" size={28} />
          <div className="vault-locked-title">{t('vault.sessionLocked')}</div>
          <div className="muted small">{t('vault.lockedHint')}</div>
          <button className="primary" onClick={unlock}>
            <Icon name="unlock" size={14} /> {t('vault.unlock')}
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="vault-mgr">
      <div className="vault-tabs">
        {tabs.map((tb) => (
          <button
            key={tb.id}
            className={`vault-tab${tb.id === tab ? ' active' : ''}`}
            onClick={() => {
              setTab(tb.id);
              setEditing(null);
            }}
          >
            {tb.label}
          </button>
        ))}
        <span className="spacer" />
        <button className="vault-lock-btn" title={t('vault.lockNow')} onClick={lock}>
          <Icon name="lock" size={14} /> {t('vault.lockNow')}
        </button>
      </div>

      {tab === 'sshkeys' && <SshKeysSettings />}
      {tab === 'connections' && <VaultConnections />}
      {tab === 'termipod' && <VaultTermipod />}

      {showItems && (
        <>
          <div className="vault-toolbar">
            <div className="vault-search">
              <Icon name="search" size={15} className="vault-search-icon" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('vault.search')} />
            </div>
            <div className="vault-new">
              <button className="primary" onClick={() => setNewMenu((v) => !v)}>
                <Icon name="plus" size={14} /> {t('vault.new')}
              </button>
              {newMenu && (
                <>
                  <div className="vault-new-backdrop" onClick={() => setNewMenu(false)} />
                  <div className="vault-new-menu">
                    <button onClick={() => openNew('login')}>
                      <Icon name="key" size={15} /> {t('vault.new_login')}
                    </button>
                    <button onClick={() => openNew('api')}>
                      <Icon name="code" size={15} /> {t('vault.new_api')}
                    </button>
                    <button onClick={() => openNew('note')}>
                      <Icon name="note" size={15} /> {t('vault.new_note')}
                    </button>
                    <button onClick={() => openNew('env')}>
                      <Icon name="sliders" size={15} /> {t('vault.new_env')}
                    </button>
                    <button onClick={() => openNew('script')}>
                      <Icon name="terminal" size={15} /> {t('vault.new_script')}
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>

          <div className="vault-split">
            <div className="vault-list">
              {listed.length === 0 && <div className="muted small vault-empty">{t('vault.empty')}</div>}
              {listed.map((i) => (
                <div key={i.id} className={`vault-row${i.id === sel && editing === null ? ' active' : ''}`}>
                  <button
                    className="vault-row-pick"
                    onClick={() => {
                      setSel(i.id);
                      setEditing(null);
                    }}
                  >
                    <Icon name={typeIcon(i.type)} size={16} className="vault-row-icon" />
                    <span className="vault-row-main">
                      <span className="vault-row-title">{i.title}</span>
                      <span className="vault-row-sub muted small">
                        {i.type === 'login'
                          ? i.username
                          : i.type === 'api'
                            ? i.endpoint
                            : i.type === 'env'
                              ? i.format || t('vault.typeEnv')
                              : i.type === 'script'
                                ? i.interpreter || t('vault.typeScript')
                                : t('vault.typeNote')}
                      </span>
                    </span>
                  </button>
                  <button
                    className={i.favorite ? 'icon-btn fav-on' : 'icon-btn vault-row-fav'}
                    title={t('vault.favorite')}
                    onClick={() => fav(i.id)}
                  >
                    <Icon name="star" size={15} />
                  </button>
                </div>
              ))}
            </div>

            <div className="vault-pane">
              {editing !== null ? (
                <ItemEditor
                  key={editing.item?.id ?? `new-${editing.type}`}
                  item={editing.item}
                  type={editing.type}
                  onSaved={onSaved}
                  onCancel={() => setEditing(null)}
                />
              ) : selItem !== null ? (
                <ItemDetail
                  item={selItem}
                  onEdit={() => setEditing({ item: selItem, type: selItem.type })}
                  onDelete={() => void remove(selItem.id)}
                />
              ) : (
                <div className="vault-pane-empty muted small">
                  <Icon name="lock" size={28} />
                  <p>{t('vault.pickHint')}</p>
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
