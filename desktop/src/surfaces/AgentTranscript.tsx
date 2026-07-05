import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';

function eventText(e: Entity): string {
  return str(e, 'text') ?? str(e, 'body') ?? str(e, 'content') ?? str(e, 'summary') ?? '';
}
function eventKind(e: Entity): string {
  return str(e, 'kind') ?? str(e, 'type') ?? 'event';
}
function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Focus region for a selected agent (WS4): a live transcript over the agent SSE
/// stream (fetch-based, auth-header) with `tail` backfill + `seq` cursor, a text
/// composer (`POST /input`), lifecycle actions (WS3), and a digest tab
/// (`GET /digest`, ADR-038).
export function AgentTranscript({ agentId }: { agentId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [events, setEvents] = useState<Entity[]>([]);
  const [tab, setTab] = useState<'transcript' | 'digest'>('transcript');
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    let handle: SseHandle | null = null;
    setEvents([]);
    setError(null);
    void (async () => {
      try {
        const initial = await client.listAgentEvents(agentId, { tail: 200 });
        if (cancelled) return;
        initial.sort((a, b) => (num(a, 'seq') ?? 0) - (num(b, 'seq') ?? 0));
        setEvents(initial);
        const last = initial.length > 0 ? num(initial[initial.length - 1], 'seq') : undefined;
        handle = client.streamAgent(agentId, {
          since: last !== undefined ? String(last) : undefined,
          onEvent: (e) => setEvents((prev) => [...prev, e as Entity]),
          onError: (err) => setError(msg(err)),
        });
      } catch (err) {
        if (!cancelled) setError(msg(err));
      }
    })();
    return () => {
      cancelled = true;
      handle?.close();
    };
  }, [agentId, client]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [events]);

  const digestQ = useQuery({
    queryKey: ['agent-digest', agentId],
    enabled: client !== null && tab === 'digest',
    queryFn: () => client!.getAgentDigest(agentId),
  });

  async function lifecycle(action: (id: string) => Promise<unknown>): Promise<void> {
    if (client === null) return;
    setBusy(true);
    try {
      await action(agentId);
      await qc.invalidateQueries({ queryKey: ['agents'] });
    } catch (err) {
      setError(msg(err));
    } finally {
      setBusy(false);
    }
  }

  async function send(): Promise<void> {
    if (client === null || draft.trim() === '') return;
    const body = draft;
    setDraft('');
    try {
      await client.postAgentInput(agentId, body);
    } catch (err) {
      setError(msg(err));
      setDraft(body);
    }
  }

  return (
    <div className="transcript">
      <div className="transcript-bar">
        <div className="tabs">
          <button className={tab === 'transcript' ? 'tab active' : 'tab'} onClick={() => setTab('transcript')}>
            {t('tx.transcript')}
          </button>
          <button className={tab === 'digest' ? 'tab active' : 'tab'} onClick={() => setTab('digest')}>
            {t('tx.digest')}
          </button>
        </div>
        <span className="spacer" />
        <div className="lifecycle">
          <button disabled={busy} onClick={() => void lifecycle((id) => client!.pauseAgent(id))}>{t('tx.pause')}</button>
          <button disabled={busy} onClick={() => void lifecycle((id) => client!.resumeAgent(id))}>{t('tx.resume')}</button>
          <button disabled={busy} onClick={() => void lifecycle((id) => client!.stopAgent(id))}>{t('tx.stop')}</button>
          <button disabled={busy} onClick={() => void lifecycle((id) => client!.terminateAgent(id))}>{t('tx.terminate')}</button>
          <button disabled={busy} onClick={() => void lifecycle((id) => client!.archiveAgent(id))}>{t('tx.archive')}</button>
        </div>
      </div>

      {error !== null && <div className="region-pad error">{error}</div>}

      {tab === 'transcript' ? (
        <>
          <div className="feed">
            {events.map((e, i) => (
              <div key={str(e, 'id') ?? String(num(e, 'seq') ?? i)} className="feed-row">
                <span className="feed-kind">{eventKind(e)}</span>
                {str(e, 'role') !== undefined ? <span className="feed-role">{str(e, 'role')}</span> : null}
                <span className="feed-text">{eventText(e)}</span>
              </div>
            ))}
            {events.length === 0 && <div className="region-pad muted">{t('tx.noEvents')}</div>}
            <div ref={bottomRef} />
          </div>
          <div className="composer">
            <input
              value={draft}
              placeholder={t('tx.sendPlaceholder')}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  void send();
                }
              }}
            />
            <button className="primary" onClick={() => void send()} disabled={draft.trim() === ''}>
              {t('tx.send')}
            </button>
          </div>
        </>
      ) : (
        <div className="region-pad digest">
          {digestQ.isLoading && <div className="muted">{t('tx.loadingDigest')}</div>}
          {digestQ.isError && <div className="error">{msg(digestQ.error)}</div>}
          {digestQ.data !== undefined ? <pre>{JSON.stringify(digestQ.data, null, 2)}</pre> : null}
        </div>
      )}
    </div>
  );
}
