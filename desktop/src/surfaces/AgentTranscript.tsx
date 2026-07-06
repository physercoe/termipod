import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import type { InputAttachments } from '../hub/client';
import { Composer } from '../ui/Composer';
import { callToolId, EventCard, toFeedEvent, type FeedEvent } from '../ui/EventCard';
import { errorLabel, eventIsError, FEED_LENSES, matchesLens, type FeedLens } from '../ui/feedLens';
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

type Mode = 'live' | 'insight' | 'digest';

/// Focus region for a selected agent (WS4 + parity transcript work): a live
/// transcript over the agent SSE stream (fetch-based, auth-header) with `tail`
/// backfill + `seq` cursor, a text composer (`POST /input`), lifecycle actions
/// (WS3), and three modes mirroring mobile:
/// - **Live** — the streaming feed + a `FeedLens` filter (all/text/turns/tools/
///   errors) with a match stepper (mobile live_feed.dart funnel/stepper).
/// - **Insight** — a Turns/Errors navigator that jumps the full feed to a
///   turn's `start_seq` or an error's `seq` (mobile InsightTranscript, ADR-041).
/// - **Digest** — the `RunReport` dashboard (`GET /digest`, ADR-038).
export function AgentTranscript({ agentId }: { agentId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [events, setEvents] = useState<Entity[]>([]);
  const [mode, setMode] = useState<Mode>('live');
  const [lens, setLens] = useState<FeedLens>('all');
  const [matchIndex, setMatchIndex] = useState(0);
  const [navTab, setNavTab] = useState<'turns' | 'errors'>('turns');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const feedRef = useRef<HTMLDivElement>(null);

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

  // Only autoscroll to the tail in Live/all mode — a filter or an Insight jump
  // must not be yanked back to the bottom by a new streamed event.
  useEffect(() => {
    if (mode === 'live' && lens === 'all') bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [events, mode, lens]);

  const digestQ = useQuery({
    queryKey: ['agent-digest', agentId],
    enabled: client !== null && mode === 'digest',
    queryFn: () => client!.getAgentDigest(agentId),
  });

  const turnsQ = useQuery({
    queryKey: ['agent-turns', agentId],
    enabled: client !== null && mode === 'insight',
    refetchInterval: 10000,
    queryFn: () => client!.listAgentTurns(agentId, { limit: 500 }),
  });

  const feed = useMemo(() => events.map((e, i) => toFeedEvent(e, i)), [events]);
  const { resultById, nameById, callIds } = useToolMaps(feed);

  // Render helper — one card per event with tool folding applied.
  function renderCard(ev: FeedEvent): JSX.Element | null {
    if (ev.kind === 'tool_result') {
      const id = str(ev.payload, 'tool_use_id');
      if (id !== undefined && callIds.has(id)) return null; // folded into its call
      return <EventCard key={ev.id} ev={ev} callName={id !== undefined ? nameById.get(id) : undefined} />;
    }
    if (ev.kind === 'tool_call') {
      const id = callToolId(ev.payload);
      return <EventCard key={ev.id} ev={ev} result={id !== undefined ? resultById.get(id) : undefined} />;
    }
    return <EventCard key={ev.id} ev={ev} />;
  }

  // Live-mode filtering: hide non-matching events (mobile `lensed`).
  const shown = useMemo(
    () => (lens === 'all' ? feed : feed.filter((ev) => matchesLens(ev, lens, resultById))),
    [feed, lens, resultById],
  );
  const matchSeqs = useMemo(() => shown.map((ev) => ev.seq), [shown]);

  // Insight error list — dedupe folded tool_results so an error appears once.
  const errorRows = useMemo(
    () =>
      feed.filter((ev) => {
        if (!eventIsError(ev, resultById)) return false;
        if (ev.kind === 'tool_result') {
          const id = str(ev.payload, 'tool_use_id');
          if (id !== undefined && callIds.has(id)) return false;
        }
        return true;
      }),
    [feed, resultById, callIds],
  );

  const turns = turnsQ.data ?? [];

  function scrollToSeq(seq: number): void {
    const el = feedRef.current?.querySelector(`[data-seq="${seq}"]`);
    if (el === null || el === undefined) return;
    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    el.classList.add('ev-flash');
    window.setTimeout(() => el.classList.remove('ev-flash'), 1400);
  }

  function step(delta: number): void {
    if (matchSeqs.length === 0) return;
    const next = Math.max(0, Math.min(matchSeqs.length - 1, matchIndex + delta));
    setMatchIndex(next);
    scrollToSeq(matchSeqs[next]);
  }

  function setLensReset(l: FeedLens): void {
    setLens(l);
    setMatchIndex(0);
  }

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

  const modes: { v: Mode; label: string }[] = [
    { v: 'live', label: t('tx.live') },
    { v: 'insight', label: t('tx.insight') },
    { v: 'digest', label: t('tx.digest') },
  ];

  return (
    <div className="transcript">
      <div className="transcript-bar">
        <div className="tabs">
          {modes.map((m) => (
            <button key={m.v} className={mode === m.v ? 'tab active' : 'tab'} onClick={() => setMode(m.v)}>
              {m.label}
            </button>
          ))}
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

      {mode === 'live' && (
        <>
          <div className="feed-filter">
            <select value={lens} onChange={(e) => setLensReset(e.target.value as FeedLens)}>
              {FEED_LENSES.map((l) => (
                <option key={l} value={l}>
                  {t(`lens.${l}`)}
                </option>
              ))}
            </select>
            {lens !== 'all' && (
              <span className="feed-stepper">
                <span className="muted small">
                  {matchSeqs.length === 0 ? '0' : `${matchIndex + 1}/${matchSeqs.length}`} {t('tx.matched')}
                </span>
                <button disabled={matchSeqs.length === 0} title={t('tx.prev')} onClick={() => step(-1)}>
                  ▲
                </button>
                <button disabled={matchSeqs.length === 0} title={t('tx.next')} onClick={() => step(1)}>
                  ▼
                </button>
                <button title={t('tx.clear')} onClick={() => setLensReset('all')}>
                  ✕
                </button>
              </span>
            )}
          </div>
          <div className="feed" ref={feedRef}>
            {shown.map(renderCard)}
            {shown.length === 0 && <div className="region-pad muted">{lens === 'all' ? t('tx.noEvents') : t('tx.noMatches')}</div>}
            <div ref={bottomRef} />
          </div>
          <Composer onSend={send} />
        </>
      )}

      {mode === 'insight' && (
        <div className="insight-body">
          <div className="insight-nav">
            <div className="tabs">
              <button className={navTab === 'turns' ? 'tab active' : 'tab'} onClick={() => setNavTab('turns')}>
                {t('insight.turns')} <span className="pill">{turns.length}</span>
              </button>
              <button className={navTab === 'errors' ? 'tab active' : 'tab'} onClick={() => setNavTab('errors')}>
                {t('insight.errors')} <span className="pill">{errorRows.length}</span>
              </button>
            </div>
            <div className="insight-list">
              {navTab === 'turns' ? (
                turnsQ.isLoading ? (
                  <div className="muted region-pad">{t('common.loading')}</div>
                ) : turns.length === 0 ? (
                  <div className="muted region-pad">{t('insight.noTurns')}</div>
                ) : (
                  turns.map((tn, i) => {
                    const startSeq = num(tn, 'start_seq');
                    const status = str(tn, 'status') ?? (tn['open'] === true ? 'open' : 'done');
                    const errs = num(tn, 'error_count') ?? 0;
                    const toolN = num(tn, 'tool_count') ?? 0;
                    const toolF = num(tn, 'tool_failed') ?? 0;
                    const dur = num(tn, 'duration_ms');
                    return (
                      <button
                        key={str(tn, 'turn_id') ?? String(i)}
                        className="insight-row"
                        disabled={startSeq === undefined}
                        onClick={() => startSeq !== undefined && scrollToSeq(startSeq)}
                      >
                        <span className={`dot ${errs > 0 ? 'stopped' : status === 'open' ? 'running' : 'muted'}`} />
                        <span className="insight-row-title">
                          {t('insight.turn')} {i + 1}
                        </span>
                        <span className="spacer" />
                        <span className="muted small">
                          {toolN > 0 ? `⚒ ${toolN - toolF}/${toolN}` : ''}
                          {errs > 0 ? ` · ✕${errs}` : ''}
                          {dur !== undefined ? ` · ${Math.round(dur / 100) / 10}s` : ''}
                        </span>
                      </button>
                    );
                  })
                )
              ) : errorRows.length === 0 ? (
                <div className="muted region-pad">{t('insight.noErrors')}</div>
              ) : (
                errorRows.map((ev) => (
                  <button key={ev.id} className="insight-row" onClick={() => scrollToSeq(ev.seq)}>
                    <span className="dot stopped" />
                    <span className="insight-row-title">{errorLabel(ev, nameById)}</span>
                    <span className="spacer" />
                    <span className="muted small mono">#{ev.seq}</span>
                  </button>
                ))
              )}
            </div>
          </div>
          <div className="feed insight-feed" ref={feedRef}>
            {feed.map(renderCard)}
            {feed.length === 0 && <div className="region-pad muted">{t('tx.noEvents')}</div>}
          </div>
        </div>
      )}

      {mode === 'digest' && (
        <div className="region-pad digest">
          {digestQ.isLoading && <div className="muted">{t('tx.loadingDigest')}</div>}
          {digestQ.isError && <div className="error">{msg(digestQ.error)}</div>}
          {digestQ.data !== undefined ? <RunReport digest={digestQ.data} stale={digestQ.isStale} /> : null}
        </div>
      )}
    </div>
  );
}
