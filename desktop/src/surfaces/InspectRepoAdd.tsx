import { useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { parseForgeUrl, resolveForgeRepo, saveForgeToken } from '../state/forge';
import type { PinRoot } from './InspectOpen';

/// Add a **forge** root to the Inspect tree (round-3 T3): paste a GitHub (T3a) or
/// Hugging Face (T3b) repo URL / shorthand, resolve its ref to an immutable
/// commit SHA, and pin it. An optional token is stored in the vault (never
/// `localStorage`) keyed to the forge host, so all its repos pick it up.
///
/// The forge is auto-detected from the URL host; a bare `owner/repo` shorthand
/// defaults to GitHub (the T3b HF selector will disambiguate it).
export function InspectRepoAddDialog({ onClose, onAdd }: { onClose: () => void; onAdd: (root: PinRoot) => void }): JSX.Element {
  const t = useT();
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const parsed = useMemo(() => parseForgeUrl(url), [url]);
  // HF resolution lands in T3b; keep the dialog honest until then.
  const hfNotYet = parsed !== null && parsed.forge === 'hf';
  const canAdd = parsed !== null && !hfNotYet && !busy;

  async function add(): Promise<void> {
    if (parsed === null || hfNotYet) return;
    setBusy(true);
    setErr(null);
    try {
      if (token.trim() !== '') await saveForgeToken(parsed.forge, token.trim());
      const repo = await resolveForgeRepo(parsed.forge, parsed.id, parsed.ref);
      onAdd({ source: parsed.forge, repo, label: `${parsed.id}@${repo.ref}` });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div className="inspect-modal" role="dialog" aria-label={t('inspect.fromRepo')} onClick={(e) => e.stopPropagation()}>
        <div className="inspect-modal-head">
          <span className="inspect-modal-title">{t('inspect.fromRepo')}</span>
          <span className="spacer" />
          <button className="icon-btn" title={t('inspect.close')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        <div className="inspect-repoadd">
          <label className="inspect-repoadd-label small muted">{t('inspect.repoUrl')}</label>
          <input
            className="inspect-modal-search"
            placeholder="github.com/owner/repo  ·  owner/repo@ref"
            value={url}
            autoFocus
            onChange={(e) => setUrl(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && canAdd && void add()}
          />
          {parsed !== null && !hfNotYet && (
            <div className="small muted inspect-repoadd-hint">
              <Icon name="git-branch" size={12} /> {parsed.forge === 'github' ? 'GitHub' : 'Hugging Face'} · {parsed.id}
              {parsed.ref !== undefined ? ` @ ${parsed.ref}` : ` · ${t('inspect.repoDefaultBranch')}`}
            </div>
          )}
          {hfNotYet && <div className="small muted inspect-repoadd-hint">{t('inspect.repoHfSoon')}</div>}
          <label className="inspect-repoadd-label small muted">{t('inspect.repoToken')}</label>
          <input className="inspect-modal-search" type="password" placeholder={t('inspect.repoTokenPlaceholder')} value={token} onChange={(e) => setToken(e.target.value)} />
          {err !== null && (
            <div className="inspect-error inspect-repoadd-err">
              <Icon name="alert" size={14} /> {err}
            </div>
          )}
          <div className="inspect-repoadd-actions">
            <button className="import-btn" disabled={!canAdd} onClick={() => void add()}>
              {busy ? t('inspect.repoResolving') : t('inspect.repoAdd')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
