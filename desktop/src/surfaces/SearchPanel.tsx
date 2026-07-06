import { useState } from 'react';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useHubAction } from '../hub/action';
import { useSession } from '../state/session';

/// Search surface (parity Phase 4). The hub has no cross-entity search
/// (grounded); `GET /v1/search?q=` is FTS5 over event **text parts** only
/// (`handleSearch`, token-scoped). This is a scoped free-text search over the
/// conversation stream — query box → result rows `{type, from_id, channel_id,
/// received_ts, parts[]}`. Not a global object search; labelled as such.
function partsText(item: Entity): string {
  const parts = Array.isArray(item['parts']) ? (item['parts'] as Entity[]) : [];
  return parts
    .map((p) => str(p, 'text') ?? '')
    .filter((s) => s !== '')
    .join(' ');
}

export function SearchPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const [q, setQ] = useState('');
  const [results, setResults] = useState<Entity[] | null>(null);

  async function submit(): Promise<void> {
    if (client === null || q.trim() === '') return;
    const r = await run(() => client.searchEvents(q.trim(), 100));
    if (r !== undefined) setResults(r as Entity[]);
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="sessions-panel" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('search.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad">
          <div className="search-bar">
            <input
              value={q}
              autoFocus
              placeholder={t('search.placeholder')}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void submit();
              }}
            />
            <button className="primary" disabled={busy || q.trim() === ''} onClick={() => void submit()}>
              {t('search.go')}
            </button>
          </div>
          <div className="muted small">{t('search.scopeNote')}</div>
        </div>
        <div className="region-pad scroll">
          {error !== null && <div className="error">{error}</div>}
          {results !== null && results.length === 0 && <div className="muted">{t('search.noResults')}</div>}
          {results?.map((item, i) => (
            <div key={str(item, 'id') ?? String(i)} className="search-result">
              <div className="search-result-meta muted small">
                <span className="pill">{str(item, 'type') ?? 'event'}</span>
                <span className="mono">{str(item, 'from_id') ?? ''}</span>
                <span>{str(item, 'received_ts') ?? ''}</span>
              </div>
              <div className="search-result-text">{partsText(item)}</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
