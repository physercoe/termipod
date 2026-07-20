import { useEffect, useMemo, useRef, useState } from 'react';
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import type { InputAttachments } from '../hub/client';
import { Composer } from '../ui/Composer';
import { ConfirmButton } from '../ui/ConfirmButton';
import { Icon } from '../ui/Icon';
import { callToolId, EventCard, toFeedEvent, type FeedEvent } from '../ui/EventCard';
import { errorLabel, eventIsError, FEED_LENSES, isHiddenInFeed, matchesLens, type FeedLens } from '../ui/feedLens';
import { RunReport } from '../ui/RunReport';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// The deepest history the events endpoint serves in one call (tail = last N; it
// has no older-than cursor). We load it once and let the virtual list render only
// the visible slice — so there's no render storm, and the measured, bottom-
// anchored scroller keeps the view pinned to the latest message with no jump as
// cards hydrate (markdown/KaTeX/images settling), which one-shot JS pinning
// couldn't do on WebKit.
const LOAD_TAIL = 500;

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
  const [loaded, setLoaded] = useState(false);
  // Seq to flash briefly after a jump (Insight jump / live stepper). An off-screen
  // row in a virtual list isn't in the DOM to take a class, so we track it in
  // state and apply it when the row renders.
  const [flashSeq, setFlashSeq] = useState<number | null>(null);
  const virtuosoRef = useRef<VirtuosoHandle>(null);

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    let handle: SseHandle | null = null;
    setEvents([]);
    setError(null);
    setLoaded(false);
    void (async () => {
      try {
        // One load of the deepest tail; the virtual list renders only the visible
        // slice, so holding the full window is cheap.
        const history = await client.listAgentEvents(agentId, { tail: LOAD_TAIL });
        if (cancelled) return;
        const sorted = mergeEvents(history, []);
        setEvents(sorted);
        setLoaded(true);
        const last = sorted.length > 0 ? num(sorted[sorted.length - 1], 'seq') : undefined;
        // Live stream from the tail cursor (merge dedupes any overlap); appended
        // events stick to the bottom via the list's followOutput when the user is
        // there, and don't yank them if they've scrolled up.
        handle = client.streamAgent(agentId, {
          since: last !== undefined ? String(last) : undefined,
          onEvent: (e) => setEvents((prev) => mergeEvents(prev, [e as Entity])),
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

  // A tool_result folded into its matching tool_call — not rendered on its own.
  const isFolded = (ev: FeedEvent): boolean => {
    if (ev.kind !== 'tool_result') return false;
    const id = str(ev.payload, 'tool_use_id');
    return id !== undefined && callIds.has(id);
  };

  // Render helper — one card per event with tool folding applied. Folded
  // tool_results are dropped from the virtual list's data (below), so this only
  // sees rows that actually render.
  function renderCard(ev: FeedEvent): JSX.Element {
    if (ev.kind === 'tool_result') {
      const id = str(ev.payload, 'tool_use_id');
      return <EventCard ev={ev} callName={id !== undefined ? nameById.get(id) : undefined} />;
    }
    if (ev.kind === 'tool_call') {
      const id = callToolId(ev.payload);
      return <EventCard ev={ev} result={id !== undefined ? resultById.get(id) : undefined} />;
    }
    return <EventCard ev={ev} />;
  }

  // The item wrapper for the virtual list: carries the transient jump flash and
  // the row spacing.
  function feedItem(ev: FeedEvent): JSX.Element {
    return <div className={ev.seq === flashSeq ? 'feed-item ev-flash' : 'feed-item'}>{renderCard(ev)}</div>;
  }

  // Live-mode filtering: first drop feed noise (mobile verbose model), then
  // apply the lens (mobile `lensed`), then drop folded tool_results so the list
  // data is exactly the rows that render. Tool folding still runs over the FULL
  // feed above, so a hidden telemetry row never orphans a paired result.
  const visible = useMemo(() => feed.filter((ev) => !isHiddenInFeed(ev, verbose)), [feed, verbose]);
  const shown = useMemo(
    () => (lens === 'all' ? visible : visible.filter((ev) => matchesLens(ev, lens, resultById))),
    [visible, lens, resultById],
  );
  const liveData = useMemo(() => shown.filter((ev) => !isFolded(ev)), [shown, callIds]);
  const insightData = useMemo(() => feed.filter((ev) => !isFolded(ev)), [feed, callIds]);
  const matchSeqs = useMemo(() => liveData.map((ev) => ev.seq), [liveData]);

  // Keep the live feed pinned to the last message while it settles. On a tab
  // switch this view remounts and Virtuoso re-measures the whole log over many
  // frames (markdown/KaTeX/images hydrating). As rows above the fold grow,
  // Virtuoso holds the *topmost* visible row fixed, so the bottom drifts up and
  // the last message leaves the viewport (director report: "the scrollbar creeps
  // up"). A fixed re-pin window can't cover an arbitrarily long hydration, so we
  // re-assert the end on EVERY measured height change (`totalListHeightChanged`)
  // until the feed reveals. `liveLenRef` feeds the current tail length in
  // without making the pin logic a render dependency.
  const liveLenRef = useRef(0);
  liveLenRef.current = liveData.length;
  const stickBottomRef = useRef(true);
  /// Open at the last page with ZERO visible scrolling (#331). The re-pin loop
  /// lands the bottom, but on WebKit each correction during hydration (markdown,
  /// KaTeX, images) is a visible jump/creep. So the feed stays `visibility:hidden`
  /// (layout preserved → Virtuoso still measures) until heights go quiet, then
  /// reveals in one frame already at the bottom. The reveal is QUIESCENCE-based,
  /// not capped: every measured height change pushes the reveal out by another
  /// quiet window, so a heavy transcript stays hidden for its whole hydration
  /// storm instead of popping in mid-storm (the old flat 500ms cap did exactly
  /// that on long logs). Two guards bound the design: a generous backstop timer
  /// so a pathological transcript never stays hidden forever, and `settledRef`
  /// retiring the re-pin loop at reveal, so a LATE height change (font swap,
  /// async image pop) can no longer scroll the view — post-reveal, sticking to
  /// the bottom is covered by `followOutput` / `atBottomStateChange` alone.
  const [settled, setSettled] = useState(false);
  const settledRef = useRef(false);
  const settleTimer = useRef<number | null>(null);
  const backstopTimer = useRef<number | null>(null);
  const clearSettleTimer = (): void => {
    if (settleTimer.current !== null) window.clearTimeout(settleTimer.current);
    settleTimer.current = null;
  };
  const clearBackstopTimer = (): void => {
    if (backstopTimer.current !== null) window.clearTimeout(backstopTimer.current);
    backstopTimer.current = null;
  };
  /// Reveal is one-way and idempotent (#331): flip the ref first (synchronously
  /// retires pinBottom before the state render lands), then drop both timers so
  /// nothing pending can fire afterwards.
  const reveal = (): void => {
    settledRef.current = true;
    setSettled(true);
    clearSettleTimer();
    clearBackstopTimer();
  };
  // Re-arm the pin + re-hide on (re)mount, agent change, or return to the live tab.
  useEffect(() => {
    stickBottomRef.current = true;
    settledRef.current = false;
    setSettled(false);
    clearSettleTimer();
    clearBackstopTimer();
    // Backstop only: reveal no later than ~1.8s after (re)mount even if the
    // height-change storm never goes quiet (#331). The quiet timer in pinBottom
    // is the normal reveal path and fires long before this on a healthy log.
    backstopTimer.current = window.setTimeout(reveal, 1800);
    return () => {
      clearSettleTimer();
      clearBackstopTimer();
    };
  }, [agentId, loaded, mode]);
  function pinBottom(): void {
    // Retired once revealed (#331): a late height change after the reveal must
    // neither scroll the view (a visible jump) nor re-arm a settled reveal.
    if (settledRef.current) return;
    const n = liveLenRef.current;
    if (stickBottomRef.current && n > 0) {
      virtuosoRef.current?.scrollToIndex({ index: n - 1, align: 'end' });
    }
    // Quiescence reveal: un-hide only after the height-change storm has been
    // quiet for a full window; each new change pushes the reveal out again.
    clearSettleTimer();
    settleTimer.current = window.setTimeout(reveal, 150);
  }
  // A real scroll gesture releases the pin so we never fight the user mid-settle;
  // returning to the bottom (`atBottomStateChange`) re-arms it. Listeners go on
  // Virtuoso's own scroller element (via `scrollerRef`).
  const [feedScroller, setFeedScroller] = useState<HTMLElement | null>(null);
  useEffect(() => {
    if (feedScroller === null) return;
    const release = (): void => {
      stickBottomRef.current = false;
    };
    feedScroller.addEventListener('wheel', release, { passive: true });
    feedScroller.addEventListener('touchmove', release, { passive: true });
    feedScroller.addEventListener('keydown', release);
    return () => {
      feedScroller.removeEventListener('wheel', release);
      feedScroller.removeEventListener('touchmove', release);
      feedScroller.removeEventListener('keydown', release);
    };
  }, [feedScroller]);
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

  // Jump the virtual list to a seq (Insight jump / live stepper). Off-screen rows
  // aren't in the DOM, so we resolve the seq to an index in the active list and
  // let Virtuoso scroll to it, then flash it once it's mounted.
  function scrollToSeq(seq: number): void {
    const list = mode === 'insight' ? insightData : liveData;
    const index = list.findIndex((ev) => ev.seq === seq);
    if (index < 0) return;
    // An explicit jump means "hold here" — release the bottom pin so a late
    // height change doesn't yank the view back down off the target.
    stickBottomRef.current = false;
    /// behavior 'auto', not 'smooth': rows off-screen are unmeasured, so
    /// Virtuoso computes the target offset from *estimated* heights (running
    /// average of measured rows). Transcript cards vary wildly in height, so
    /// a smooth fling toward a distant index overshoots and the browser clamps
    /// it at the bottom — different targets then land on the same position.
    /// Worse, Virtuoso's smooth retry chain is hard-cut by a 1200ms cleanup
    /// timer, leaving the list wherever the last fling stopped. 'auto'
    /// re-asserts scrollToIndex on each list change until sizes stabilize,
    /// converging on the true offset; the 1.4s flash below gives the user the
    /// orientation the smooth animation was providing.
    virtuosoRef.current?.scrollToIndex({ index, align: 'center', behavior: 'auto' });
    setFlashSeq(seq);
    window.setTimeout(() => setFlashSeq((s) => (s === seq ? null : s)), 1400);
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
          <ConfirmButton
            label={t('tx.terminate')}
            danger
            disabled={busy}
            onConfirm={() => void lifecycle((id) => client!.terminateAgent(id))}
          />
          <ConfirmButton
            label={t('tx.archive')}
            disabled={busy}
            onConfirm={() => void lifecycle((id) => client!.archiveAgent(id))}
          />
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
          {loaded ? (
            <Virtuoso
              key={agentId}
              ref={virtuosoRef}
              className="feed-virt"
              style={settled ? undefined : { visibility: 'hidden' }}
              data={liveData}
              scrollerRef={(el) => setFeedScroller(el instanceof HTMLElement ? el : null)}
              computeItemKey={(_i, ev) => ev.id}
              initialTopMostItemIndex={{ index: Math.max(0, liveData.length - 1), align: 'end' }}
              defaultItemHeight={120}
              alignToBottom
              followOutput={(atBottom) => (atBottom ? 'auto' : false)}
              atBottomStateChange={(atBottom) => {
                if (atBottom) stickBottomRef.current = true;
              }}
              totalListHeightChanged={pinBottom}
              itemContent={(_i, ev) => feedItem(ev)}
              components={{
                EmptyPlaceholder: () => (
                  <div className="region-pad muted">{lens === 'all' ? t('tx.noEvents') : t('tx.noMatches')}</div>
                ),
              }}
            />
          ) : (
            <div className="feed muted region-pad">{t('common.loading')}</div>
          )}
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
          <Virtuoso
            ref={virtuosoRef}
            className="feed-virt insight-feed"
            data={insightData}
            computeItemKey={(_i, ev) => ev.id}
            itemContent={(_i, ev) => feedItem(ev)}
            components={{ EmptyPlaceholder: () => <div className="region-pad muted">{t('tx.noEvents')}</div> }}
            /// Parity with the live list: a sane height estimate keeps
            /// estimate-based scrollToIndex offsets from skewing toward the
            /// first-measured rows, so jump retries converge faster.
            defaultItemHeight={120}
          />
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
