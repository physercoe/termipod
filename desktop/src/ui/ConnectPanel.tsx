import { useState } from 'react';
import { HubClient } from '../hub/client';
import { configComplete, type HubConfig } from '../hub/config';
import { useT } from '../i18n';
import { useSession } from '../state/session';

/// Connect form (hub URL + team + token) rendered as a dismissable overlay so
/// the offline shell stays reachable behind it. Probes `/v1/_info`, then
/// commits. The token is held in memory only. Under Tauri the probe goes
/// through the Rust core (see HubTransport) — a webview fetch would be a
/// cross-origin/no-CORS "Failed to fetch".
export function ConnectPanel({ onClose }: { onClose?: () => void }): JSX.Element {
  const t = useT();
  const persisted = useSession((s) => s.config);
  const connect = useSession((s) => s.connect);
  const [form, setForm] = useState<HubConfig>(persisted);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (k: keyof HubConfig) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await new HubClient(form).probe(); // validate URL/token before committing
      connect(form);
      onClose?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={() => onClose?.()}>
      <form className="connect" onMouseDown={(e) => e.stopPropagation()} onSubmit={submit}>
        <div className="connect-head">
          <h2>{t('connect.title')}</h2>
          <span className="spacer" />
          {onClose !== undefined && (
            <button type="button" onClick={onClose}>
              {t('admin.close')}
            </button>
          )}
        </div>
        <label>
          {t('connect.url')}
          <input value={form.baseUrl} onChange={set('baseUrl')} placeholder="https://hub.example.com" />
        </label>
        <label>
          {t('connect.team')}
          <input value={form.teamId} onChange={set('teamId')} placeholder="team id" />
        </label>
        <label>
          {t('connect.token')}
          <input value={form.token} onChange={set('token')} type="password" placeholder="bearer token" />
        </label>
        {error !== null && <div className="error">{error}</div>}
        <button className="primary" type="submit" disabled={busy || !configComplete(form)}>
          {busy ? t('connect.connecting') : t('connect.connect')}
        </button>
      </form>
    </div>
  );
}
