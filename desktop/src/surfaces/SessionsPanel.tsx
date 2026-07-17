import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { arr, num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { useProjects } from '../hub/queries';
import { RunReport } from '../ui/RunReport';
import { AgentTranscript } from './AgentTranscript';

const SESSION_FILTERS = ['all', 'active', 'paused', 'archived'] as const;
type SessionFilter = (typeof SESSION_FILTERS)[number];

/// The session's own name (mobile `sessionDisplayTitle`): explicit title, then
/// the hub's `session_name_hint`. Null when unnamed — the row shows a localized
/// "Untitled session" placeholder rather than the raw id (rename via right-click).
function sessionName(s: Entity): string | null {
  return str(s, 'title') ?? str(s, 'session_name_hint') ?? null;
}

/// A session is "live" while active or paused; used to float live sessions to
/// the top within a scope group.
function isLive(s: Entity): boolean {
  const st = str(s, 'status') ?? '';
  return st === 'active' || st === 'paused';
}

/// Scope bucket key — Team (team/empty scope), a per-project bucket, or the
/// Approving (attention) bucket. Mirrors mobile's scope split.
function scopeKey(s: Entity): string {
  const kind = str(s, 'scope_kind') ?? '';
  const sid = str(s, 'scope_id') ?? '';
  if (kind === 'project' && sid !== '') return `project:${sid}`;
  if (kind === 'attention') return 'attention';
  return 'team';
}

function lastActiveMs(s: Entity): number {
  const v = str(s, 'last_active_at') ?? str(s, 'opened_at');
  const ms = v !== undefined ? Date.parse(v) : NaN;
  return Number.isNaN(ms) ? 0 : ms;
}

/// Compact relative age (now / Nm / Nh / Nd / Nw), like mobile `_shortTimestamp`.
function relTime(s: Entity): string {
  const ms = lastActiveMs(s);
  if (ms === 0) return '';
  const secs = Math.max(0, (Date.now() - ms) / 1000);
  if (secs < 60) return 'now';
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.floor(h / 24);
  if (d < 7) return `${d}d`;
  return `${Math.floor(d / 7)}w`;
}

function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id;
}

/// Sessions surface (parity Phase 4). The session is the conversational
/// primitive that survives respawn. Lists the team's sessions — with a text
/// filter, a status filter, and Active/Previous grouping (mobile parity) — and
/// shows a selected session's rolled-up run digest (ADR-038 §5) or its
/// transcript. Read-only for now.
export function SessionsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const projects = useProjects().data ?? [];
  const [selected, setSelected] = useState<string | null>(null);
  const [view, setView] = useState<'digest' | 'transcript'>('digest');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<SessionFilter>('all');
  const [menu, setMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; value: string } | null>(null);
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameErr, setRenameErr] = useState<string | null>(null);

  const listQ = useQuery({
    queryKey: ['sessions', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listSessions(),
  });

  const digestQ = useQuery({
    queryKey: ['session-digest', selected],
    enabled: client !== null && selected !== null,
    queryFn: () => client!.getSessionDigest(selected as string),
  });

  const sessions = listQ.data ?? [];

  const projectName = (pid: string): string => {
    const p = projects.find((x) => str(x, 'id') === pid);
    return (p !== undefined ? str(p, 'name') ?? str(p, 'title') : undefined) ?? shortId(pid);
  };

  // Text + status filter, then group by scope (Team / each Project / Approving),
  // live sessions first then newest within each group. Team first, projects next
  // (by name), Approving last.
  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const filtered = sessions.filter((s) => {
      const status = str(s, 'status') ?? '';
      if (filter !== 'all' && status !== filter) return false;
      if (q !== '') {
        const hay = [sessionName(s) ?? '', status, str(s, 'scope_kind'), str(s, 'id'), str(s, 'current_agent_id')]
          .filter((v): v is string => v !== undefined && v !== '')
          .join(' ')
          .toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
    const byKey = new Map<string, Entity[]>();
    for (const s of filtered) {
      const k = scopeKey(s);
      const bucket = byKey.get(k);
      if (bucket !== undefined) bucket.push(s);
      else byKey.set(k, [s]);
    }
    const order = (k: string): number => (k === 'team' ? 0 : k.startsWith('project:') ? 1 : 2);
    const label = (k: string): string =>
      k === 'team'
        ? t('sessions.groupTeam')
        : k === 'attention'
          ? t('sessions.groupApproving')
          : `${t('sessions.groupProject')}: ${projectName(k.slice('project:'.length))}`;
    const liveFirst = (a: Entity, b: Entity): number =>
      Number(isLive(b)) - Number(isLive(a)) || lastActiveMs(b) - lastActiveMs(a);
    return [...byKey.entries()]
      .map(([k, items]) => ({ key: k, label: label(k), items: items.sort(liveFirst) }))
      .sort((a, b) => order(a.key) - order(b.key) || a.label.localeCompare(b.label));
  }, [sessions, query, filter, projects, t]);

  function startRename(id: string): void {
    const s = sessions.find((x) => str(x, 'id') === id);
    setRenameErr(null);
    setRenaming({ id, value: (s !== undefined ? str(s, 'title') : undefined) ?? '' });
    setMenu(null);
  }
  async function saveRename(): Promise<void> {
    if (client === null || renaming === null) return;
    setRenameBusy(true);
    setRenameErr(null);
    try {
      await client.renameSession(renaming.id, renaming.value.trim());
      await qc.invalidateQueries({ queryKey: ['sessions'] });
      setRenaming(null);
    } catch (err) {
      setRenameErr(err instanceof Error ? err.message : String(err));
    } finally {
      setRenameBusy(false);
    }
  }

  /// The transcript is per-agent (there is no session-level events endpoint), so
  /// resolve the session's agent from the digest's `current_agent_id` /
  /// `agent_ids` (handlers_agent_digest.go), falling back to the list row. This
  /// is how a paused/terminated session reaches its full transcript — the
  /// `/agents/{id}/events` feed is served from stored events, not a live process.
  function resolveAgentId(): string | undefined {
    const d = digestQ.data;
    if (d !== undefined) {
      const cur = str(d, 'current_agent_id');
      if (cur !== undefined && cur !== '') return cur;
      const ids = arr(d, 'agent_ids');
      for (let i = ids.length - 1; i >= 0; i -= 1) {
        const v = ids[i];
        if (typeof v === 'string' && v !== '') return v;
      }
    }
    const s = sessions.find((x) => str(x, 'id') === selected);
    const rowCur = s !== undefined ? str(s, 'current_agent_id') : undefined;
    return rowCur !== undefined && rowCur !== '' ? rowCur : undefined;
  }
  const agentId = resolveAgentId();

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="sessions-panel" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('sessions.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="sessions-body">
          <div className="sessions-list">
            <div className="sessions-toolbar">
              <input
                className="sessions-search"
                value={query}
                placeholder={t('sessions.searchPlaceholder')}
                onChange={(e) => setQuery(e.target.value)}
              />
              <div className="seg sessions-filter">
                {SESSION_FILTERS.map((f) => (
                  <button
                    key={f}
                    className={filter === f ? 'seg-btn active' : 'seg-btn'}
                    onClick={() => setFilter(f)}
                  >
                    {t(`sessions.filter.${f}`)}
                  </button>
                ))}
              </div>
            </div>
            {listQ.isLoading && <div className="muted region-pad">{t('sessions.loading')}</div>}
            {listQ.isError && <div className="error region-pad">{(listQ.error as Error).message}</div>}
            {groups.map((g) => (
              <div key={g.key} className="sessions-group">
                <div className="sessions-group-head muted small">
                  {g.label} <span className="pill">{g.items.length}</span>
                </div>
                {g.items.map((s) => {
                  const id = str(s, 'id') ?? '';
                  const status = str(s, 'status') ?? '';
                  const scope = str(s, 'scope_kind') ?? '';
                  const cost = num(s, 'session_cost_usd_imputed');
                  const age = relTime(s);
                  const name = sessionName(s);
                  return (
                    <button
                      key={id}
                      className={id === selected ? 'session-item active' : 'session-item'}
                      onClick={() => setSelected(id)}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        setMenu({ id, x: e.clientX, y: e.clientY });
                      }}
                    >
                      <span className="session-row-top">
                        <span className={`dot ${status === 'active' ? 'running' : status === 'paused' ? 'paused' : 'muted'}`} />
                        <span className={name !== null ? 'session-name' : 'session-name muted'}>
                          {name ?? t('sessions.untitled')}
                        </span>
                        <span className="spacer" />
                        {age !== '' && <span className="muted small session-age">{age}</span>}
                      </span>
                      <span className="session-row-sub muted small">
                        {status}
                        {scope !== '' ? ` · ${scope}` : ''}
                        {cost !== undefined ? ` · $${cost.toFixed(cost >= 1 ? 2 : 4)}` : ''}
                        {id !== '' ? ' · ' : ''}
                        {id !== '' && <span className="mono">{shortId(id)}</span>}
                      </span>
                    </button>
                  );
                })}
              </div>
            ))}
            {!listQ.isLoading && groups.length === 0 && (
              <div className="muted region-pad">{sessions.length === 0 ? t('sessions.none') : t('sessions.noMatch')}</div>
            )}
          </div>
          <div className="sessions-detail">
            {selected === null ? (
              <div className="muted region-pad">{t('sessions.pick')}</div>
            ) : (
              <>
                <div className="sessions-detail-bar">
                  <div className="seg">
                    <button
                      className={view === 'digest' ? 'seg-btn active' : 'seg-btn'}
                      onClick={() => setView('digest')}
                    >
                      {t('sessions.digest')}
                    </button>
                    <button
                      className={view === 'transcript' ? 'seg-btn active' : 'seg-btn'}
                      onClick={() => setView('transcript')}
                    >
                      {t('sessions.transcript')}
                    </button>
                  </div>
                </div>
                {view === 'transcript' ? (
                  agentId !== undefined ? (
                    <AgentTranscript key={agentId} agentId={agentId} />
                  ) : (
                    <div className="muted region-pad">
                      {digestQ.isLoading ? t('tx.loadingDigest') : t('sessions.noAgent')}
                    </div>
                  )
                ) : (
                  <div className="sessions-detail-scroll">
                    {digestQ.isLoading ? (
                      <div className="muted region-pad">{t('tx.loadingDigest')}</div>
                    ) : digestQ.isError ? (
                      <div className="error region-pad">{(digestQ.error as Error).message}</div>
                    ) : digestQ.data !== undefined ? (
                      <div className="region-pad">
                        <RunReport digest={digestQ.data} stale={digestQ.isStale} />
                      </div>
                    ) : null}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
        {menu !== null && (
          <>
            <div className="context-backdrop" onMouseDown={() => setMenu(null)} />
            <div className="context-menu" style={{ left: menu.x, top: menu.y }} onMouseDown={(e) => e.stopPropagation()}>
              <button onClick={() => startRename(menu.id)}>{t('sessions.rename')}</button>
            </div>
          </>
        )}
      </div>
      {renaming !== null && (
        <div className="palette-backdrop" onMouseDown={() => setRenaming(null)}>
          <div className="rename-modal" onMouseDown={(e) => e.stopPropagation()}>
            <strong>{t('sessions.renameTitle')}</strong>
            <input
              autoFocus
              value={renaming.value}
              placeholder={t('sessions.untitled')}
              onChange={(e) => setRenaming({ id: renaming.id, value: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void saveRename();
                if (e.key === 'Escape') setRenaming(null);
              }}
            />
            {renameErr !== null && <div className="error small">{renameErr}</div>}
            <div className="rename-actions">
              <button onClick={() => setRenaming(null)}>{t('common.cancel')}</button>
              <button className="primary" disabled={renameBusy} onClick={() => void saveRename()}>
                {t('sessions.save')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
