import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import type { InputAttachments } from '../hub/client';
import { Composer } from '../ui/Composer';
import { callToolId, EventCard, toFeedEvent, type FeedEvent } from '../ui/EventCard';
import { RunReport } from '../ui/RunReport';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Build the tool-pairing maps over the flat event feed: a tool_call folds its
/// matching tool_result inline (joined on `tool_use_id`), and a standalone
/// tool_result borrows its tool name back. `callIds` lets us hide the results
/// that were folded so they don't also render on their own.
function useToolMaps(feed: FeedEvent[]): {
  resultById: Map<string, Entity>;
  nameById: Map<string, string>;
  callIds: Set<string>;
} {
  return useMemo(() => {
    const resultById = new Map<string, Entity>();
    const nameById = new Map<string, string>();
    const callIds = new Set<string>();
    for (const ev of feed) {
      if (ev.kind === 'tool_result') {
        const id = str(ev.payload, 'tool_use_id');
        if (id !== undefined) resultById.set(id, ev.payload);
      } else if (ev.kind === 'tool_call') {
        const id = callToolId(ev.payload);
        if (id !== undefined) {
          callIds.add(id);
          const name = str(ev.payload, 'name');
          if (name !== undefined) nameById.set(id, name);
        }
      }
    }
    return { resultById, nameById, callIds };
  }, [feed]);
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

  const feed = useMemo(() => events.map((e, i) => toFeedEvent(e, i)), [events]);
  const { resultById, nameById, callIds } = useToolMaps(feed);

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

  async function send(body: string, att: InputAttachments): Promise<void> {
    if (client === null) return;
    await client.postAgentInput(agentId, body, att);
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
            {feed.map((ev) => {
              if (ev.kind === 'tool_result') {
                const id = str(ev.payload, 'tool_use_id');
                // Folded into its tool_call card above — don't render twice.
                if (id !== undefined && callIds.has(id)) return null;
                return <EventCard key={ev.id} ev={ev} callName={id !== undefined ? nameById.get(id) : undefined} />;
              }
              if (ev.kind === 'tool_call') {
                const id = callToolId(ev.payload);
                return <EventCard key={ev.id} ev={ev} result={id !== undefined ? resultById.get(id) : undefined} />;
              }
              return <EventCard key={ev.id} ev={ev} />;
            })}
            {feed.length === 0 && <div className="region-pad muted">{t('tx.noEvents')}</div>}
            <div ref={bottomRef} />
          </div>
          <Composer onSend={send} />
        </>
      ) : (
        <div className="region-pad digest">
          {digestQ.isLoading && <div className="muted">{t('tx.loadingDigest')}</div>}
          {digestQ.isError && <div className="error">{msg(digestQ.error)}</div>}
          {digestQ.data !== undefined ? <RunReport digest={digestQ.data} stale={digestQ.isStale} /> : null}
        </div>
      )}
    </div>
  );
}
