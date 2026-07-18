import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { ConfirmButton } from '../ui/ConfirmButton';
import { useSession } from '../state/session';
import {
  createVault,
  forgetLocalVault,
  restoreWithRecovery,
  syncDown,
  syncUp,
  vaultStatus,
  vaultStatusKey,
} from '../vault/service';

function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/// Locale-aware relative time ("3 minutes ago") for the last-sync line; the
/// absolute local timestamp rides along as the title tooltip. Returns null on an
/// unparseable value so the caller can fall back.
function relTime(iso: string): { rel: string; abs: string } | null {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return null;
  const abs = new Date(ms).toLocaleString();
  const secs = Math.round((Date.now() - ms) / 1000);
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, 'second'],
    [60, 'minute'],
    [24, 'hour'],
    [30, 'day'],
    [12, 'month'],
  ];
  let unit: Intl.RelativeTimeFormatUnit = 'year';
  let value = secs;
  for (const [span, u] of steps) {
    if (Math.abs(value) < span) {
      unit = u;
      break;
    }
    value = Math.round(value / span);
  }
  // `value` is seconds-elapsed collapsed into `unit`; negate so a past time reads
  // as "…ago" rather than "in …".
  return { rel: rtf.format(-value, unit), abs };
}

/// Settings → Vault (parity Phase 2b, ADR-052 D-3/D-4). Joins the
/// zero-knowledge vault so saved connections + keys sync with the phone:
/// create, sync up/down, restore via a recovery code. Desktop-only (needs the
/// Rust crypto). EXPERIMENTAL — the crypto round-trips are CI-verified but
/// cross-device interop with the mobile app is not yet confirmed.
export function VaultPanel(): JSX.Element | null {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [code, setCode] = useState<string | null>(null); // one-time recovery code to show
  const [restore, setRestore] = useState('');
  const [showRestore, setShowRestore] = useState(false);

  // Cached + prefetched (AppShell primes this on connect) so the status is
  // already resolved when Settings opens — no keychain-latency "splash".
  const stQ = useQuery({
    queryKey: vaultStatusKey(client),
    enabled: client !== null,
    staleTime: 60_000,
    queryFn: () => vaultStatus(client as NonNullable<typeof client>),
  });
  const st = stQ.data ?? null;

  if (!isTauri()) return null;

  async function run(fn: () => Promise<void>): Promise<void> {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: vaultStatusKey(client) });
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="setting-group">
      <h3>{t('vault.title')}</h3>
      <p className="muted small">{t('vault.blurb')}</p>

      {client === null ? (
        <div className="muted">{t('vault.needHub')}</div>
      ) : (
        <div className="vault-body">
          <div className="setting-row">
            <label>{t('vault.status')}</label>
            <span className="muted">
              {st === null
                ? stQ.isLoading
                  ? t('vault.checking')
                  : '—'
                : st.exists
                  ? `${t('vault.exists')} · v${st.version} · ${st.hasLocalKey ? t('vault.unlocked') : t('vault.locked')}`
                  : t('vault.none')}
            </span>
          </div>

          {st !== null && st.exists && (
            <>
              <div className="setting-row">
                <label>{t('vault.lastSynced')}</label>
                {(() => {
                  const rt = st.updatedAt !== null ? relTime(st.updatedAt) : null;
                  const from =
                    st.lastDevice !== null && st.lastDevice !== ''
                      ? ` · ${t('vault.syncedFrom').replace('{m}', st.lastDevice)}`
                      : '';
                  return (
                    <span className="muted" title={rt?.abs}>
                      {rt !== null ? `${rt.rel}${from}` : t('vault.neverSynced')}
                    </span>
                  );
                })()}
              </div>
              <div className="setting-row">
                <label>{t('vault.thisDevice')}</label>
                <span className="muted">{st.thisDevice}</span>
              </div>
            </>
          )}

          {code !== null && (
            <div className="vault-code">
              <div className="small">{t('vault.recoverySaved')}</div>
              <pre className="mono">{code}</pre>
              <button onClick={() => setCode(null)}>{t('admin.close')}</button>
            </div>
          )}

          <div className="setting-row vault-actions">
            {st !== null && !st.exists && (
              <button className="primary" disabled={busy} onClick={() => void run(async () => setCode(await createVault(client)))}>
                {t('vault.create')}
              </button>
            )}
            {st !== null && st.exists && st.hasLocalKey && (
              <>
                <button disabled={busy} onClick={() => void run(async () => { await syncUp(client); })}>
                  {t('vault.syncUp')}
                </button>
                <ConfirmButton
                  label={t('vault.syncDown')}
                  danger
                  disabled={busy}
                  onConfirm={() => void run(async () => { await syncDown(client); })}
                />
              </>
            )}
            {st !== null && st.exists && !st.hasLocalKey && (
              <button disabled={busy} onClick={() => setShowRestore((v) => !v)}>
                {t('vault.restore')}
              </button>
            )}
            {st !== null && st.hasLocalKey && (
              <ConfirmButton
                label={t('vault.forget')}
                danger
                disabled={busy}
                onConfirm={() => void run(forgetLocalVault)}
              />
            )}
          </div>

          {showRestore && st !== null && st.exists && !st.hasLocalKey && (
            <div className="vault-restore">
              <input
                placeholder={t('vault.recoveryPlaceholder')}
                value={restore}
                onChange={(e) => setRestore(e.target.value)}
                spellCheck={false}
              />
              <button
                className="primary"
                disabled={busy || restore.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await restoreWithRecovery(client, restore.trim());
                    setRestore('');
                    setShowRestore(false);
                    setNote(t('vault.restored'));
                  })
                }
              >
                {t('vault.restore')}
              </button>
            </div>
          )}

          {note !== null && <div className="setting-row"><span className="muted">{note}</span></div>}
          {err !== null && <div className="setting-row"><span className="error">{err}</span></div>}
        </div>
      )}
    </section>
  );
}
