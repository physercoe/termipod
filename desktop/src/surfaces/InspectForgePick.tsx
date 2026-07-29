import { useEffect, useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { fetchForgeTree, parseForgeUrl, resolveForgeRepo, saveForgeToken, type Forge } from '../state/forge';
import { kindForInspectFile, type ForgeRepo } from '../state/inspect';
import type { PickResult } from './InspectOpen';

/// Forge (GitHub / Hugging Face) **file pick** (#460) — the missing compare-side
/// source. `RepoResolveForm` is the URL/token form shared with the pin-root
/// dialog ([[InspectRepoAddDialog]]); `ForgeFilePicker` browses a resolved,
/// SHA-pinned snapshot's tree (flat + filter, like the hub picker — forge trees
/// arrive in one fetch); `RepoPickDialog` chains the two for the Compare menu's
/// "From GitHub / Hugging Face repo…" (pick a file WITHOUT pinning it as a root).

function baseName(p: string): string {
  const s = p.replace(/[\\/]+$/, '');
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  return i >= 0 ? s.slice(i + 1) : s;
}
function extOf(p: string): string {
  const b = baseName(p);
  const i = b.lastIndexOf('.');
  return i >= 0 ? b.slice(i + 1) : '';
}

/// The shared forge resolve form: URL/shorthand + forge selector (for the bare
/// `owner/repo` shorthand whose host can't be inferred) + optional token (stored
/// in the vault, never localStorage). Resolves the ref to an immutable commit
/// SHA, then hands the pinned snapshot to `onResolved`.
export function RepoResolveForm({
  submitLabel,
  busyLabel,
  onResolved,
}: {
  submitLabel: string;
  busyLabel: string;
  onResolved: (forge: Forge, repo: ForgeRepo, id: string) => void;
}): JSX.Element {
  const t = useT();
  const [forge, setForge] = useState<Forge>('github');
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const parsed = useMemo(() => parseForgeUrl(url, forge), [url, forge]);
  const canGo = parsed !== null && !busy;

  async function resolve(): Promise<void> {
    if (parsed === null) return;
    setBusy(true);
    setErr(null);
    try {
      if (token.trim() !== '') await saveForgeToken(parsed.forge, token.trim());
      const repo = await resolveForgeRepo(parsed.forge, parsed.id, parsed.ref);
      onResolved(parsed.forge, repo, parsed.id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="inspect-repoadd">
      <div className="inspect-repoadd-forge">
        <button className={`import-btn${forge === 'github' ? ' active' : ''}`} onClick={() => setForge('github')}>
          <Icon name="git-branch" size={13} /> GitHub
        </button>
        <button className={`import-btn${forge === 'hf' ? ' active' : ''}`} onClick={() => setForge('hf')}>
          <Icon name="sliders" size={13} /> Hugging Face
        </button>
      </div>
      <label className="inspect-repoadd-label small muted">{t('inspect.repoUrl')}</label>
      <input
        className="inspect-modal-search"
        placeholder="github.com/owner/repo  ·  huggingface.co/org/model  ·  owner/repo@ref"
        value={url}
        autoFocus
        onChange={(e) => setUrl(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && canGo && void resolve()}
      />
      {parsed !== null && (
        <div className="small muted inspect-repoadd-hint">
          <Icon name={parsed.forge === 'github' ? 'git-branch' : 'sliders'} size={12} /> {parsed.forge === 'github' ? 'GitHub' : 'Hugging Face'} · {parsed.id}
          {parsed.ref !== undefined ? ` @ ${parsed.ref}` : ` · ${t('inspect.repoDefaultBranch')}`}
        </div>
      )}
      <label className="inspect-repoadd-label small muted">{t('inspect.repoToken')}</label>
      <input className="inspect-modal-search" type="password" placeholder={t('inspect.repoTokenPlaceholder')} value={token} onChange={(e) => setToken(e.target.value)} />
      {err !== null && (
        <div className="inspect-error inspect-repoadd-err">
          <Icon name="alert" size={14} /> {err}
        </div>
      )}
      <div className="inspect-repoadd-actions">
        <button className="import-btn" disabled={!canGo} onClick={() => void resolve()}>
          {busy ? busyLabel : submitLabel}
        </button>
      </div>
    </div>
  );
}

/// Browse one resolved forge snapshot and pick a file. The whole tree arrives in
/// one fetch (`fetchForgeTree`, capped — a cap is surfaced), so browsing is a
/// filter box over the flat path list rather than per-directory expansion.
export function ForgeFilePicker({ forge, repo, onPick }: { forge: Forge; repo: ForgeRepo; onPick: (r: PickResult) => void }): JSX.Element {
  const t = useT();
  const [entries, setEntries] = useState<{ path: string; is_dir: boolean }[] | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0); // retry: bump to re-fetch after an error
  const [q, setQ] = useState('');

  useEffect(() => {
    let cancelled = false;
    setEntries(null);
    setErr(null);
    fetchForgeTree(forge, repo)
      .then((r) => {
        if (cancelled) return;
        setEntries(r.entries);
        setTruncated(r.truncated);
      })
      .catch((e: unknown) => {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [forge, repo, nonce]);

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (entries ?? [])
      .filter((e) => !e.is_dir && (needle === '' || e.path.toLowerCase().includes(needle)))
      .slice(0, 500);
  }, [entries, q]);

  return (
    <>
      <input className="inspect-modal-search" placeholder={t('inspect.filter')} value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
      <div className="inspect-modal-list">
        {err !== null ? (
          <div className="inspect-error region-pad">
            <Icon name="alert" size={16} /> {err}{' '}
            <button className="link-btn small" onClick={() => setNonce((n) => n + 1)}>
              {t('inspect.retry')}
            </button>
          </div>
        ) : entries === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : shown.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noMatches')}</div>
        ) : (
          <>
            {shown.map((e) => (
              <button
                key={e.path}
                className="inspect-modal-row"
                onClick={() => onPick({ source: forge, kind: kindForInspectFile(extOf(e.path), ''), title: baseName(e.path), path: e.path, repo })}
              >
                <Icon name="file-text" size={14} />
                <span className="inspect-modal-row-name">{e.path}</span>
              </button>
            ))}
            {truncated && <div className="muted small region-pad">{t('inspect.listingCapped')}</div>}
          </>
        )}
      </div>
    </>
  );
}

/// Compare-side picker for an arbitrary repo (not pinned as a root): resolve →
/// browse → pick. The result carries the pinned SHA snapshot, so the compare
/// re-reads exactly what was picked.
export function RepoPickDialog({ onPick, onClose }: { onPick: (r: PickResult) => void; onClose: () => void }): JSX.Element {
  const t = useT();
  const [resolved, setResolved] = useState<{ forge: Forge; repo: ForgeRepo } | null>(null);
  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div className="inspect-modal" role="dialog" aria-label={t('inspect.fromRepo')} onClick={(e) => e.stopPropagation()}>
        <div className="inspect-modal-head">
          <span className="inspect-modal-title">{t('inspect.fromRepo')}</span>
          {resolved !== null && (
            <button className="link-btn small" onClick={() => setResolved(null)}>
              {t('inspect.repoChange')}
            </button>
          )}
          <span className="spacer" />
          <button className="icon-btn" title={t('inspect.close')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        {resolved !== null ? (
          <ForgeFilePicker forge={resolved.forge} repo={resolved.repo} onPick={onPick} />
        ) : (
          <RepoResolveForm submitLabel={t('inspect.repoBrowse')} busyLabel={t('inspect.repoResolving')} onResolved={(forge, repo) => setResolved({ forge, repo })} />
        )}
      </div>
    </div>
  );
}
