import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import type { InputAttachments } from '../hub/client';
import { Composer } from '../ui/Composer';
import { Icon } from '../ui/Icon';
import { callToolId, EventCard, toFeedEvent, type FeedEvent } from '../ui/EventCard';
import { errorLabel, eventIsError, FEED_LENSES, isHiddenInFeed, matchesLens, type FeedLens } from '../ui/feedLens';
import { RunReport } from '../ui/RunReport';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// How many events to render on the FIRST paint — just enough to overfill the
// viewport so the view lands on the latest message and only a handful of cards
// hydrate. The deeper HISTORY tail is *prefetched* in the background but held
// out of the DOM until the user scrolls up (see revealHistory): rendering all of
// it on open makes hundreds of markdown/KaTeX/image cards lay out at once, and
// each one growing as it hydrates churns the scroll — the "scrolls a lot on
// open" bug. Lazy reveal keeps open cheap; scrollback is instant since the bytes
// are already fetched.
const INITIAL_TAIL = 40;
const HISTORY_TAIL = 500;

/// Union two event batches by `seq` (unique + monotonic per agent), sorted
/// ascending. Idempotent, so a streamed event that also appears in the history
/// backfill (overlap around the cursor) collapses to one, and the background
/// history merges under the already-streamed tail without duplicates.
function mergeEvents(a: Entity[], b: Entity[]): Entity[] {
  const bySeq = new Map<number, Entity>();
  const noSeq: Entity[] = [];
  for (const e of a) {
    const s = num(e, 'seq');
    if (s === undefined) noSeq.push(e);
    else bySeq.set(s, e);
  }
  for (const e of b) {
    const s = num(e, 'seq');
    if (s === undefined) noSeq.push(e);
    else bySeq.set(s, e);
  }
  const merged = [...bySeq.values()].sort((x, y) => (num(x, 'seq') ?? 0) - (num(y, 'seq') ?? 0));
  return noSeq.length > 0 ? [...merged, ...noSeq] : merged;
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
  const [verbose, setVerbose] = useState(false);
  const [matchIndex, setMatchIndex] = useState(0);
  const [navTab, setNavTab] = useState<'turns' | 'errors'>('turns');
  const [insightNavOpen, setInsightNavOpen] = useState(() => {
    try {
      return localStorage.getItem('termipod.insight.navOpen') !== '0';
    } catch {
      return true;
    }
  });
  function toggleInsightNav(): void {
    setInsightNavOpen((v) => {
      const n = !v;
      try {
        localStorage.setItem('termipod.insight.navOpen', n ? '1' : '0');
      } catch {
        /* ignore */
      }
      return n;
    });
  }
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Gates the live feed visible until it has settled at the bottom. The tail's
  // cards pin + reflow (fonts, late layout) over the first frames after open;
  // holding the content invisible until then means the user sees it already at
  // the latest message instead of watching it scroll into place. (This is what
  // chat UIs do — position at the bottom before the first painted frame.)
  const [feedReady, setFeedReady] = useState(false);
  const feedRef = useRef<HTMLDivElement>(null);
  const feedContentRef = useRef<HTMLDivElement>(null);
  // Whether the feed is pinned to the tail. True while the user is at the
  // bottom; a manual scroll-up releases it so a streamed event (or the history
  // backfill) doesn't yank them back down.
  const stickRef = useRef(true);
  // Deeper history, prefetched in the background but held out of the DOM until
  // the user scrolls up (revealHistory merges it once). Keeps the open path to
  // just the tail's worth of cards.
  const historyRef = useRef<Entity[] | null>(null);
  const historyMergedRef = useRef(false);
  // Current mode, readable from the (stale-closure) load effect so it can force
  // the history reveal if the user is already in Insight mode when it lands.
  const modeRef = useRef(mode);
  modeRef.current = mode;

  // Merge the prefetched deeper history into the feed exactly once — when the
  // user scrolls up to the top, or right away if the tail didn't fill the
  // viewport. We compensate scrollTop by the height the older rows add above the
  // viewport so the rows being read stay put; the browser's scroll anchoring
  // absorbs the rest as those rows hydrate. A no-op until the prefetch lands or
  // if the user is still down at the tail. `force` reveals unconditionally —
  // Insight mode needs the full window in the DOM so jump-to-turn can find any
  // row, so it doesn't wait for a scroll-up.
  const revealHistory = useCallback((force = false): void => {
    const el = feedRef.current;
    if (historyRef.current === null || historyMergedRef.current) return;
    // Still reading near the tail (and there's room to scroll) — wait for scroll-up.
    if (!force && el !== null && el.scrollTop >= 200 && el.scrollHeight > el.clientHeight) return;
    historyMergedRef.current = true;
    const extra = historyRef.current;
    historyRef.current = null;
    const prevHeight = el?.scrollHeight ?? 0;
    const prevTop = el?.scrollTop ?? 0;
    setEvents((prev) => mergeEvents(prev, extra));
    // Keep the viewport where it was after the older rows prepend above it.
    requestAnimationFrame(() => {
      const el2 = feedRef.current;
      if (el2 !== null) el2.scrollTop = prevTop + (el2.scrollHeight - prevHeight);
    });
  }, []);

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    let handle: SseHandle | null = null;
    setEvents([]);
    setError(null);
    setFeedReady(false);
    stickRef.current = true; // a freshly-opened transcript starts pinned to the latest
    historyRef.current = null;
    historyMergedRef.current = false;
    void (async () => {
      try {
        // 1) Small tail → instant first paint, already scrolled to the latest.
        const initial = await client.listAgentEvents(agentId, { tail: INITIAL_TAIL });
        if (cancelled) return;
        const sorted = mergeEvents(initial, []);
        setEvents(sorted);
        // Reveal once the tail has committed + pinned (two frames covers the
        // layout-effect pin and the immediate reflow), so the settle is off-screen.
        requestAnimationFrame(() => requestAnimationFrame(() => !cancelled && setFeedReady(true)));
        const last = sorted.length > 0 ? num(sorted[sorted.length - 1], 'seq') : undefined;
        // 2) Live stream from the tail cursor (merge dedupes any overlap).
        handle = client.streamAgent(agentId, {
          since: last !== undefined ? String(last) : undefined,
          onEvent: (e) => setEvents((prev) => mergeEvents(prev, [e as Entity])),
          onError: (err) => setError(msg(err)),
        });
        // 3) Prefetch deeper history in the background but DON'T render it yet —
        //    hold it in historyRef so scrollback is instant, while the open path
        //    stays limited to the tail's cards. revealHistory merges it the
        //    moment the user scrolls up (or now, if the tail didn't fill the
        //    viewport and they're already at the top).
        if (HISTORY_TAIL > INITIAL_TAIL) {
          const history = await client.listAgentEvents(agentId, { tail: HISTORY_TAIL });
          if (cancelled) return;
          historyRef.current = history;
          revealHistory(modeRef.current === 'insight');
        }
      } catch (err) {
        if (!cancelled) setError(msg(err));
      }
    })();
    return () => {
      cancelled = true;
      handle?.close();
    };
  }, [agentId, client]);

  // Pin the feed to the tail (instantly, no animation) after a render — but only
  // in Live/all mode and only while the user is at the bottom. A filter, an
  // Insight jump, or a manual scroll-up all leave the view where it is.
  useLayoutEffect(() => {
    if (mode !== 'live' || lens !== 'all' || !stickRef.current) return;
    const el = feedRef.current;
    if (el !== null) el.scrollTop = el.scrollHeight;
  }, [events, mode, lens, verbose]);

  // Hold the tail as cards *grow after mount*. EventCard content settles async —
  // markdown lays out, KaTeX/highlight.js restyle, images load with no reserved
  // height — so the one-shot pin above lands before the real height is known and
  // the view then drifts as content hydrates (the "keeps scrolling" bug). A
  // ResizeObserver on the feed content re-pins to the bottom on every height
  // change while the user is at the tail, so the latest message stays in view as
  // the backfill fills in above and cards finish rendering. Releasing on a manual
  // scroll-up (stickRef) means reading history is never yanked.
  useLayoutEffect(() => {
    const content = feedContentRef.current;
    const el = feedRef.current;
    if (content === null || el === null) return;
    const ro = new ResizeObserver(() => {
      if (mode !== 'live' || lens !== 'all' || !stickRef.current) return;
      el.scrollTop = el.scrollHeight;
    });
    ro.observe(content);
    return () => ro.disconnect();
  }, [mode, lens]);

  // Insight mode navigates the FULL feed (jump-to-turn / jump-to-error), so it
  // needs the deeper history in the DOM even without a scroll-up — force it in.
  useEffect(() => {
    if (mode === 'insight') revealHistory(true);
  }, [mode, revealHistory]);

  // Track whether the user is at the bottom, so streamed events follow the tail
  // only when they haven't scrolled up to read history.
  function onFeedScroll(): void {
    const el = feedRef.current;
    if (el === null) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (el.scrollTop < 200) revealHistory(); // reached the top → fill in older history
  }

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

  // Live-mode filtering: first drop feed noise (mobile verbose model), then
  // apply the lens (mobile `lensed`). Tool folding still runs over the FULL
  // feed above, so a hidden telemetry row never orphans a paired result.
  const visible = useMemo(() => feed.filter((ev) => !isHiddenInFeed(ev, verbose)), [feed, verbose]);
  const shown = useMemo(
    () => (lens === 'all' ? visible : visible.filter((ev) => matchesLens(ev, lens, resultById))),
    [visible, lens, resultById],
  );
  const matchSeqs = useMemo(() => shown.map((ev) => ev.seq), [shown]);
  // How many low-signal rows the verbose toggle would reveal (for its badge).
  const verboseHidden = useMemo(
    () => (verbose ? 0 : feed.filter((ev) => isHiddenInFeed(ev, false) && !isHiddenInFeed(ev, true)).length),
    [feed, verbose],
  );

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
            <button
              className={verbose ? 'feed-verbose active' : 'feed-verbose'}
              title={t('tx.verboseHint')}
              onClick={() => setVerbose((v) => !v)}
            >
              {verbose ? t('tx.hideNoise') : t('tx.showNoise')}
              {!verbose && verboseHidden > 0 && <span className="pill">{verboseHidden}</span>}
            </button>
            {lens !== 'all' && (
              <span className="feed-stepper">
                <span className="muted small">
                  {matchSeqs.length === 0 ? '0' : `${matchIndex + 1}/${matchSeqs.length}`} {t('tx.matched')}
                </span>
                <button disabled={matchSeqs.length === 0} title={t('tx.prev')} onClick={() => step(-1)}>
                  <Icon name="chevron-up" size={14} />
                </button>
                <button disabled={matchSeqs.length === 0} title={t('tx.next')} onClick={() => step(1)}>
                  <Icon name="chevron-down" size={14} />
                </button>
                <button title={t('tx.clear')} onClick={() => setLensReset('all')}>
                  <Icon name="close" size={14} />
                </button>
              </span>
            )}
          </div>
          <div className="feed" ref={feedRef} onScroll={onFeedScroll}>
            <div className="feed-content" ref={feedContentRef} style={{ opacity: feedReady ? 1 : 0 }}>
              {shown.map(renderCard)}
              {shown.length === 0 && <div className="region-pad muted">{lens === 'all' ? t('tx.noEvents') : t('tx.noMatches')}</div>}
            </div>
          </div>
          <Composer onSend={send} />
        </>
      )}

      {mode === 'insight' && (
        <div className="insight-body">
          <div className={insightNavOpen ? 'insight-nav' : 'insight-nav collapsed'}>
            <div className="insight-nav-head">
              <button
                className="nav-fold-btn"
                title={insightNavOpen ? t('nav.collapse') : t('nav.expand')}
                onClick={toggleInsightNav}
              >
                <Icon name="sidebar" size={15} />
              </button>
              {insightNavOpen && (
                <div className="tabs">
                  <button className={navTab === 'turns' ? 'tab active' : 'tab'} onClick={() => setNavTab('turns')}>
                    {t('insight.turns')} <span className="pill">{turns.length}</span>
                  </button>
                  <button className={navTab === 'errors' ? 'tab active' : 'tab'} onClick={() => setNavTab('errors')}>
                    {t('insight.errors')} <span className="pill">{errorRows.length}</span>
                  </button>
                </div>
              )}
            </div>
            {insightNavOpen && (
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
            )}
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
