import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useHubAction } from '../hub/action';
import { useSession } from '../state/session';
import { useFocus } from '../state/focus';
import { Icon } from '../ui/Icon';
import { Modal } from '../ui/Modal';

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
  const selectAgent = useFocus((s) => s.selectAgent);
  // Event search returns each hit's `from_id`; for agent-emitted events that IS
  // the agent id (hostrunner sets from_id = agent_id), so a hit whose from_id
  // matches a known agent can jump straight to that agent's transcript.
  //
  // Search runs over the *historical* event stream, so most hits come from agents
  // that have since terminated. The default agent list hides
  // terminated/failed/crashed/archived agents (handlers_agents.go), which would
  // make almost every result un-clickable — so this jump-map explicitly includes
  // them. The `/agents/{id}/events` feed is served from stored events, so a
  // terminated agent's transcript still opens.
  const agentsQ = useQuery({
    queryKey: ['agents', 'search-jump', client?.transport.teamId],
    enabled: client !== null,
    queryFn: () => client!.listAgents({ include_terminated: true, include_archived: true }),
  });
  const agentIds = useMemo(
    () => new Set((agentsQ.data ?? []).map((a) => str(a, 'id')).filter((v): v is string => v !== undefined && v !== '')),
    [agentsQ.data],
  );
  const [q, setQ] = useState('');
  const [results, setResults] = useState<Entity[] | null>(null);

  async function submit(): Promise<void> {
    if (client === null || q.trim() === '') return;
    const r = await run(() => client.searchEvents(q.trim(), 100));
    if (r !== undefined) setResults(r as Entity[]);
  }

  function openAgent(id: string): void {
    selectAgent('fleet', id);
    onClose();
  }

  return (
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('search.title')}>
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
          {results?.map((item, i) => {
            const from = str(item, 'from_id') ?? '';
            const jumpable = from !== '' && agentIds.has(from);
            const meta = (
              <div className="search-result-meta muted small">
                <span className="pill">{str(item, 'type') ?? 'event'}</span>
                <span className="mono">{from}</span>
                <span>{str(item, 'received_ts') ?? ''}</span>
                {jumpable && <Icon name="chevron-right" size={13} />}
              </div>
            );
            const key = str(item, 'id') ?? String(i);
            return jumpable ? (
              <button
                key={key}
                className="search-result search-result-link"
                title={t('search.openAgent')}
                onClick={() => openAgent(from)}
              >
                {meta}
                <div className="search-result-text">{partsText(item)}</div>
              </button>
            ) : (
              <div key={key} className="search-result">
                {meta}
                <div className="search-result-text">{partsText(item)}</div>
              </div>
            );
          })}
        </div>
    </Modal>
  );
}
