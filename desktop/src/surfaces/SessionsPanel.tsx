import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { RunReport } from '../ui/RunReport';

/// Sessions surface (parity Phase 4). The session is the conversational
/// primitive that survives respawn; `listSessions` already existed with no UI.
/// This lists the team's sessions and shows a selected session's rolled-up run
/// digest (ADR-038 §5) via the shared RunReport. Read-only for now.
export function SessionsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [selected, setSelected] = useState<string | null>(null);

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
            ) : digestQ.isLoading ? (
              <div className="muted region-pad">{t('tx.loadingDigest')}</div>
            ) : digestQ.isError ? (
              <div className="error region-pad">{(digestQ.error as Error).message}</div>
            ) : digestQ.data !== undefined ? (
              <div className="region-pad">
                <RunReport digest={digestQ.data} stale={digestQ.isStale} />
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}
