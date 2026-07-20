import { useCallback, useState } from 'react';
import { HubClient } from '../hub/client';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { getToken, type HubProfile } from '../state/profiles';
import { Modal } from './Modal';
import { PasswordInput } from './PasswordInput';

interface Form {
  name: string;
  baseUrl: string;
  teamId: string;
  token: string;
}

/// Add / connect a hub profile (parity Phase 3a), rendered as a dismissable
/// overlay so the offline shell stays reachable behind it. Probes `/v1/_info`,
/// then saves the profile (token → OS keychain) and binds it. Editing an
/// existing profile prefills its non-secret fields; the token is re-entered.
export function ConnectPanel({ onClose, edit }: { onClose?: () => void; edit?: HubProfile }): JSX.Element {
  const t = useT();
  const connectProfile = useSession((s) => s.connectProfile);
  const [form, setForm] = useState<Form>({
    name: edit?.name ?? '',
    baseUrl: edit?.baseUrl ?? '',
    teamId: edit?.teamId ?? '',
    token: '',
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  // Stable identity for Modal's Escape/backdrop path (onClose is optional).
  const dismiss = useCallback((): void => onClose?.(), [onClose]);

  // A new profile needs a token; editing may reuse the stored one if left blank.
  const complete = form.baseUrl.trim() !== '' && form.teamId.trim() !== '' && (edit !== undefined || form.token.trim() !== '');

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const token = form.token.trim() !== '' ? form.token : edit !== undefined ? ((await getToken(edit.id)) ?? '') : '';
      if (token === '') throw new Error(t('connect.tokenRequired'));
      const cfg = { baseUrl: form.baseUrl.trim(), teamId: form.teamId.trim(), token };
      await new HubClient(cfg).probe(); // validate URL/token before committing
      await connectProfile({ id: edit?.id, name: form.name.trim() || form.baseUrl.trim(), ...cfg });
      onClose?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    // The dialog div carries `connect`; the <form> keeps submit semantics
    // (Enter submits) and melts into the card's flex layout via CSS (#313).
    <Modal onClose={dismiss} className="connect" ariaLabel={edit !== undefined ? t('connect.editTitle') : t('connect.title')}>
      <form onSubmit={submit}>
        <div className="connect-head">
          <h2>{edit !== undefined ? t('connect.editTitle') : t('connect.title')}</h2>
          <span className="spacer" />
          {onClose !== undefined && (
            <button type="button" onClick={onClose}>
              {t('admin.close')}
            </button>
          )}
        </div>
        <label>
          {t('connect.name')}
          <input value={form.name} onChange={set('name')} placeholder={t('connect.namePlaceholder')} />
        </label>
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
          <PasswordInput value={form.token} onChange={set('token')} placeholder="bearer token" />
        </label>
        {error !== null && <div className="error">{error}</div>}
        <button className="primary" type="submit" disabled={busy || !complete}>
          {busy ? t('connect.connecting') : t('connect.connect')}
        </button>
      </form>
    </Modal>
  );
}
