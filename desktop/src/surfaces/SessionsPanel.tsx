import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { arr, num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { RunReport } from '../ui/RunReport';
import { AgentTranscript } from './AgentTranscript';

/// Sessions surface (parity Phase 4). The session is the conversational
/// primitive that survives respawn; `listSessions` already existed with no UI.
/// This lists the team's sessions and shows a selected session's rolled-up run
/// digest (ADR-038 §5) via the shared RunReport. Read-only for now.
export function SessionsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [selected, setSelected] = useState<string | null>(null);
  const [view, setView] = useState<'digest' | 'transcript'>('digest');

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

  function sessionLabel(s: Entity): string {
    return str(s, 'title') ?? str(s, 'label') ?? str(s, 'name') ?? str(s, 'id') ?? '—';
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
            {listQ.isLoading && <div className="muted region-pad">{t('sessions.loading')}</div>}
            {listQ.isError && <div className="error region-pad">{(listQ.error as Error).message}</div>}
            {sessions.map((s) => {
              const id = str(s, 'id') ?? '';
              const cost = num(s, 'session_cost_usd_imputed');
              return (
                <button
                  key={id}
                  className={id === selected ? 'session-item active' : 'session-item'}
                  onClick={() => setSelected(id)}
                >
                  <span className="session-name">{sessionLabel(s)}</span>
                  <span className="muted small">
                    {str(s, 'status') ?? ''}
                    {cost !== undefined ? ` · $${cost.toFixed(cost >= 1 ? 2 : 4)}` : ''}
                  </span>
                </button>
              );
            })}
            {!listQ.isLoading && sessions.length === 0 && <div className="muted region-pad">{t('sessions.none')}</div>}
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
      </div>
    </div>
  );
}
