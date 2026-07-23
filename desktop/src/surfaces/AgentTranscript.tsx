import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
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
import { agentIsBusy, errorLabel, eventIsError, FEED_LENSES, isHiddenInFeed, matchesLens, type FeedLens } from '../ui/feedLens';
import { collapseStreamingPartials } from '../ui/streamingPartials';
import { groupToolCalls, toolCallUpdateParentId, type FeedRow } from '../ui/toolGroups';
import { ToolGroupCard } from '../ui/ToolGroupCard';
import { deriveStateDock } from '../ui/stateDock';
// NB explicit .tsx: the pure module `stateDock.ts` differs from this file
// name only in casing, so on macOS's case-insensitive filesystem a bare
// '../ui/StateDock' import resolves to stateDock.ts (TS1149). The extension
// pins the component (allowImportingTsExtensions covers .tsx here).
import { StateDock } from '../ui/StateDock.tsx';
import { RunReport } from '../ui/RunReport';
import { AgentInfo, latestStatusLine, mergeSessionInit } from '../ui/AgentInfo';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function fmtTok(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

function fmtElapsed(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

// The newest window the events endpoint serves on cold open (tail = last N). The
// virtual list renders only the visible slice — no render storm — and the
// measured, bottom-anchored scroller keeps the view pinned to the latest message
// with no jump as cards hydrate (markdown/KaTeX/images settling), which one-shot
// JS pinning couldn't do on WebKit. Older history is NOT loaded here: scrolling
// to the head pages it in by the `before_ordinal` cursor (`loadOlder`, #332
// parity — mobile LiveFeed `_maybeLoadOlder`).
const LOAD_TAIL = 500;
// One page of older history, fetched when the user scrolls to the head (mobile
// `_pageSize`). A short page (< this) proves the start of the transcript.
const OLDER_PAGE = 200;

/// A stable, cross-agent-unique key for one event. The globally-unique row `id`
/// is the true identity — `seq` is only per-agent, so it COLLIDES across the
/// agents a resumed session spans (ADR-042). Dedup MUST key on `id`, or a
/// session-scoped merge would silently drop one agent's event whenever another
/// agent reused the same seq.
function eventKey(e: Entity): string {
  return str(e, 'id') ?? `seq:${num(e, 'seq') ?? 0}`;
}

/// Session-dense order key. In a session-scoped feed the hub stamps every row
/// with `session_ordinal` — a gap-free, session-unique coordinate that orders
/// correctly across a resume where `seq` collides. Per-agent feeds have no
/// ordinal (0), so we fall back to `seq`; `ts` is the final tiebreak.
function orderRank(e: Entity): [number, number, number] {
  const ord = num(e, 'session_ordinal') ?? 0;
  const seq = num(e, 'seq') ?? 0;
  const ts = e['ts'] !== undefined ? Date.parse(String(e['ts'])) : 0;
  return [ord, seq, Number.isNaN(ts) ? 0 : ts];
}

/// Union two event batches, deduped on the stable `id` and sorted by the
/// session-dense order key. Idempotent, so a streamed event that also appears in
/// the history backfill (overlap around the cursor) collapses to one, and the
/// background history merges under the already-streamed tail without duplicates.
function mergeEvents(a: Entity[], b: Entity[]): Entity[] {
  const byId = new Map<string, Entity>();
  for (const e of a) byId.set(eventKey(e), e);
  for (const e of b) byId.set(eventKey(e), e);
  return [...byId.values()].sort((x, y) => {
    const rx = orderRank(x);
    const ry = orderRank(y);
    return rx[0] - ry[0] || rx[1] - ry[1] || rx[2] - ry[2];
  });
}

/// The navigation coordinate of a rendered row: the dense `session_ordinal` when
/// present (session-scoped feed), else the per-agent `seq`. Within one feed this
/// space is homogeneous, so a "nearest at-or-after" search is well-defined —
/// whereas raw `seq` collides across a resumed session's agents and mis-targets.
function rowCoord(ev: FeedEvent): number {
  return ev.ord > 0 ? ev.ord : ev.seq;
}

/// Build the tool-pairing maps over the flat event feed: a tool_call folds its
/// matching tool_result inline (joined on `tool_use_id`) and its latest
/// tool_call_update (joined on `toolCallId`, mobile FoldMaps), and a standalone
/// tool_result borrows its tool name back. `callIds` lets us hide the results
/// that were folded so they don't also render on their own; `nameById` doubles
/// as the mobile `toolNames` map the feed-noise model reads (feedLens.ts).
function useToolMaps(feed: FeedEvent[]): {
  resultById: Map<string, Entity>;
  updateById: Map<string, Entity>;
  nameById: Map<string, string>;
  callIds: Set<string>;
} {
  return useMemo(() => {
    const resultById = new Map<string, Entity>();
    const updateById = new Map<string, Entity>();
    const nameById = new Map<string, string>();
    const callIds = new Set<string>();
    for (const ev of feed) {
      if (ev.kind === 'tool_result') {
        const id = str(ev.payload, 'tool_use_id');
        if (id !== undefined) resultById.set(id, ev.payload);
      } else if (ev.kind === 'tool_call_update') {
        // Latest update wins (forward scan overwrites) — the folded status
        // pill / group row reads only the end state (mobile fold_maps.dart:55).
        const id = toolCallUpdateParentId(ev.payload);
        if (id !== undefined) updateById.set(id, ev.payload);
      } else if (ev.kind === 'tool_call') {
        const id = callToolId(ev.payload);
        if (id !== undefined) {
          callIds.add(id);
          const name = str(ev.payload, 'name');
          if (name !== undefined) nameById.set(id, name);
        }
      }
    }
    return { resultById, updateById, nameById, callIds };
  }, [feed]);
}

type Mode = 'live' | 'insight' | 'digest' | 'info';

/// Focus region for a selected agent (WS4 + parity transcript work): a live
/// transcript over the agent SSE stream (fetch-based, auth-header) with `tail`
/// backfill + `seq` cursor, a text composer (`POST /input`), lifecycle actions
/// (WS3), and three modes mirroring mobile:
/// - **Live** — the streaming feed + a `FeedLens` filter (all/text/turns/tools/
///   errors) with a match stepper (mobile live_feed.dart funnel/stepper).
/// - **Insight** — a Turns/Errors navigator that jumps the full feed to a
///   turn's `start_seq` or an error's `seq` (mobile InsightTranscript, ADR-041).
/// - **Digest** — the `RunReport` dashboard (`GET /digest`, ADR-038).
export function AgentTranscript({ agentId, sessionId }: { agentId: string; sessionId?: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [events, setEvents] = useState<Entity[]>([]);
  // The session this transcript is scoped to. Explicit when opened from the
  // Sessions panel; otherwise resolved from the feed's newest `session_id` after
  // the first (agent-scoped) load, then adopted so the view spans the whole
  // session across respawns — the same session-anchoring mobile does
  // (handlers_agent_events.go: "resolves an agent's run session from the newest
  // event's session_id"). Scoping to the session is what makes a resumed
  // transcript show its full history instead of just the current agent's slice.
  const [scope, setScope] = useState<string | undefined>(sessionId);
  useEffect(() => {
    setScope(sessionId);
  }, [sessionId, agentId]);
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
  // The event id to flash briefly after a jump (Insight jump / live stepper). An
  // off-screen row in a virtual list isn't in the DOM to take a class, so we track
  // it in state and apply it when the row renders. Keyed on the stable `id`, not
  // `seq` — seq collides across a resumed session's agents, so a seq flash could
  // light the wrong row.
  const [flashId, setFlashId] = useState<string | null>(null);
  // A jump target (session_ordinal) whose content isn't loaded yet: we window-load
  // around it, and the effect below re-runs the jump once the window merges in.
  const [pendingJump, setPendingJump] = useState<number | null>(null);
  const jumpLoadingRef = useRef(false);
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  // Scroll-to-latest pill (#332): while the user is scrolled up, count events
  // that arrive off-screen and offer a jump back to the live tail.
  const [atBottom, setAtBottom] = useState(true);
  const [unread, setUnread] = useState(0);
  const prevLiveLenRef = useRef(0);

  // Load-older paging (#332 parity — mobile LiveFeed `_maybeLoadOlder`). The tail
  // load holds only the newest LOAD_TAIL events; scrolling to the head pages the
  // previous window in by the dense `session_ordinal` cursor. `atHead` latches
  // once a short page proves the transcript's start is loaded. `prependAnchorRef`
  // carries the pre-merge top row id so the reconcile effect can re-anchor the
  // view to it (new rows grow upward without a jump).
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [atHead, setAtHead] = useState(false);
  const loadingOlderRef = useRef(false);
  const prependAnchorRef = useRef<string | null>(null);

  // Context-jump target (mobile ContextJumpButton → `_jumpToContext`): a filtered
  // match the user asked to see in the full log. We clear the lens to `all`, then
  // this fires the jump once the unfiltered list has rebuilt.
  const [pendingContext, setPendingContext] = useState<number | null>(null);

  // Reset the feed only when the AGENT changes (a genuinely different transcript)
  // — not when `scope` resolves from undefined → session, which reloads the same
  // transcript as a superset. Clearing there would flash the feed empty between
  // the agent-scoped first paint and the session-scoped reload.
  useEffect(() => {
    setEvents([]);
    setError(null);
    setLoaded(false);
    setPendingJump(null);
    setAtHead(false);
    setLoadingOlder(false);
    loadingOlderRef.current = false;
    prependAnchorRef.current = null;
    setPendingContext(null);
  }, [agentId]);

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    let handle: SseHandle | null = null;
    void (async () => {
      try {
        // One load of the deepest tail; the virtual list renders only the visible
        // slice, so holding the full window is cheap. `session` scopes the query
        // across the session's respawned agents (empty per-agent slice after a
        // resume otherwise).
        const history = await client.listAgentEvents(agentId, { tail: LOAD_TAIL, session: scope });
        if (cancelled) return;
        const sorted = mergeEvents(history, []);
        setEvents(sorted);
        setLoaded(true);
        // Adopt the session from the newest event when we weren't told one, so a
        // live agent's transcript re-scopes to its whole session (surviving a
        // future resume). Guarded to fire once (scope stays set), and only when
        // the event actually carries a session — reloads the effect session-scoped.
        if (scope === undefined) {
          for (let i = sorted.length - 1; i >= 0; i -= 1) {
            const sid = str(sorted[i], 'session_id');
            if (sid !== undefined && sid !== '') {
              setScope(sid);
              return; // effect re-runs with the resolved scope; skip streaming here
            }
          }
        }
        // Live stream from the CURRENT agent's own max seq — the stream backfill
        // is `agent_id = <this agent> AND seq > since`, so the cursor must be this
        // agent's seq, not the session-wide tail (which may belong to an older
        // agent with a higher, unrelated seq → live events silently skipped).
        let cursor: number | undefined;
        for (const e of sorted) {
          if (str(e, 'agent_id') !== agentId) continue;
          const s = num(e, 'seq');
          if (s !== undefined && (cursor === undefined || s > cursor)) cursor = s;
        }
        handle = client.streamAgent(agentId, {
          since: cursor !== undefined ? String(cursor) : undefined,
          query: scope !== undefined ? { session: scope } : undefined,
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
  }, [agentId, client, scope]);

  // Session-scoped digest when we know the session (the ts-ordered rollup across
  // the session's respawned agents), else per-agent. Mobile ALWAYS reads the
  // session digest (session_digest_provider.dart) — a resumed session's current
  // agent has a sparse/empty digest on its own, which is why the desktop's
  // agent-scoped digest read looked empty.
  const digestQ = useQuery({
    queryKey: ['digest', scope ?? `agent:${agentId}`],
    enabled: client !== null && mode === 'digest',
    queryFn: () => (scope !== undefined ? client!.getSessionDigest(scope) : client!.getAgentDigest(agentId)),
  });

  // The turn index backing the Insight navigator. Session-scoped when we know the
  // session (the ts-ordered UNION of the session's agents' turns), so a resumed
  // session lists EVERY turn — the per-agent list only sees the current agent's,
  // which is the other half of the "insight nav is broken after a resume" bug.
  const turnsQ = useQuery({
    queryKey: ['turns', scope ?? `agent:${agentId}`],
    enabled: client !== null && mode === 'insight',
    refetchInterval: 10000,
    queryFn: () =>
      scope !== undefined
        ? client!.listSessionTurns(scope, { limit: 500 })
        : client!.listAgentTurns(agentId, { limit: 500 }),
  });

  // The agent's lifecycle status — for the running indicator + the composer's
  // Stop-while-running action (#332). Polled; it changes out-of-band of the feed.
  const agentQ = useQuery({
    queryKey: ['agent', agentId],
    enabled: client !== null,
    refetchInterval: 5000,
    queryFn: () => client!.getAgent(agentId),
  });
  const agentStatus = agentQ.data !== undefined ? str(agentQ.data, 'status') : undefined;
  const running = agentStatus === 'running' || agentStatus === 'pending';
  const paused = agentStatus === 'paused';

  // Lifecycle overflow menu (#332): one contextual primary action in the bar, the
  // rest behind an overflow toggle.
  const [lifeMenuOpen, setLifeMenuOpen] = useState(false);
  const lifeMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!lifeMenuOpen) return;
    const onDown = (e: MouseEvent): void => {
      if (lifeMenuRef.current !== null && !lifeMenuRef.current.contains(e.target as Node)) setLifeMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setLifeMenuOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [lifeMenuOpen]);

  // Quote-into-composer (#332): a message's text, blockquote-prefixed, pushed to
  // the composer as an injection signal (id bump so the same text re-injects).
  const [quoteSignal, setQuoteSignal] = useState<{ text: string; id: number } | null>(null);
  const quoteIdRef = useRef(0);
  // Stable identity so it doesn't defeat EventCard's memo (#311).
  const quoteToComposer = useCallback((text: string): void => {
    quoteIdRef.current += 1;
    const quoted = `${text
      .split('\n')
      .map((l) => `> ${l}`)
      .join('\n')}\n\n`;
    setQuoteSignal({ text: quoted, id: quoteIdRef.current });
  }, []);

  const feed = useMemo(() => events.map((e, i) => toFeedEvent(e, i)), [events]);
  const { resultById, updateById, nameById, callIds } = useToolMaps(feed);
  // P2 state dock (kimi-web ChatDock parity — plan §6 P2): session-state
  // chips + detail panel above the composer. Derived from the FULL feed —
  // NOT the lens-filtered list — so a lens change never moves the counts
  // (chips are session state, not feed filters; the lens system below stays
  // untouched).
  const stateDock = useMemo(
    () => deriveStateDock(feed, { nameById, resultById, updateById }),
    [feed, nameById, resultById, updateById],
  );
  // P1 tool-group collapse state (kimi-web parity — plan §7 decision 3):
  // groups are EXPANDED by default and never auto-collapse; the header click
  // is the only toggle, opt-in per group instance. Keyed by the group's
  // stable row key (`grp:<first-call-id>`), which survives the group growing
  // as new calls join its run AND virtual-list recycling (unlike per-card
  // component state, which unmounts off-screen).
  const [collapsedGroups, setCollapsedGroups] = useState<ReadonlySet<string>>(new Set());
  const toggleGroup = useCallback((key: string): void => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);
  // Feed-derived session config for the Info tab: the merged `session.init`
  // frame (engine, model, workdir, tools, …) + the latest `status_line` (live
  // effort / thinking / fast-mode). Computed here because these live in the
  // event stream, not the agent record. Session-scoped, so a resumed session's
  // config survives across the respawn boundary the same way its transcript does.
  const sessionInit = useMemo(() => mergeSessionInit(events), [events]);
  const statusLine = useMemo(() => latestStatusLine(events), [events]);
  // The composer's Stop-vs-Send signal is whether the agent is mid-turn (derived
  // from the feed), NOT the lifecycle status (a live-but-idle agent is still
  // `running`, which would show Stop almost always — director-reported).
  const generating = useMemo(() => agentIsBusy(feed), [feed]);

  // Persistent status line (#332): model, turn count, latest token snapshot, and
  // elapsed wall-time — all reliably present in the session.init / usage / turn
  // events and the row timestamps. (Live running-state + a composer Stop swap
  // want the agent's lifecycle status, not just the feed — deferred.)
  const stats = useMemo(() => {
    let model: string | undefined;
    let inTok = 0;
    let outTok = 0;
    let turns = 0;
    let firstTs: number | undefined;
    let lastTs: number | undefined;
    for (const ev of feed) {
      if (ev.ts !== undefined) {
        const ts = Date.parse(ev.ts);
        if (!Number.isNaN(ts)) {
          if (firstTs === undefined) firstTs = ts;
          lastTs = ts;
        }
      }
      if (ev.kind === 'session.init') model = str(ev.payload, 'model') ?? model;
      else if (ev.kind === 'usage') {
        model = str(ev.payload, 'model') ?? model;
        inTok = num(ev.payload, 'input_tokens') ?? inTok;
        outTok = num(ev.payload, 'output_tokens') ?? outTok;
      } else if (ev.kind === 'turn.result') turns += 1;
    }
    const elapsed = firstTs !== undefined && lastTs !== undefined && lastTs > firstTs ? lastTs - firstTs : undefined;
    return { model, inTok, outTok, turns, elapsed };
  }, [feed]);

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
    // Quote-into-composer only where the composer lives (live mode).
    return <EventCard ev={ev} onQuote={mode === 'live' ? quoteToComposer : undefined} />;
  }

  // The item wrapper for the virtual list: carries the transient jump flash and
  // the row spacing.
  function feedItem(ev: FeedEvent): JSX.Element {
    // On a filtered live feed, each match carries a jump chip to its spot in the
    // full log (mobile ContextJumpButton). Hidden at lens `all` (already the full
    // log) and outside live mode.
    const showJump = mode === 'live' && lens !== 'all';
    return (
      <div className={ev.id === flashId ? 'feed-item ev-flash' : 'feed-item'}>
        <div className="feed-measure">
          {showJump && (
            <button
              className="feed-jump"
              title={t('tx.viewInContext')}
              aria-label={t('tx.viewInContext')}
              onClick={() => jumpToContext(rowCoord(ev))}
            >
              <Icon name="crosshair" size={13} />
            </button>
          )}
          {renderCard(ev)}
        </div>
      </div>
    );
  }

  // One virtual-list ROW in the live feed: a standalone event renders exactly
  // like before; a group row (≥2 consecutive tool_calls) renders the P1 group
  // card. The row key is stable (`grp:<first-call-id>`) so toggling collapse
  // only re-renders this item — and Virtuoso's per-item ResizeObserver picks
  // up the height change and re-measures it (firing totalListHeightChanged,
  // which the settle/pin logic already handles).
  function feedRow(row: FeedRow): JSX.Element {
    if (row.events.length === 1) return feedItem(row.events[0]);
    const first = row.events[0];
    const showJump = mode === 'live' && lens !== 'all';
    return (
      <div className={first.id === flashId ? 'feed-item ev-flash' : 'feed-item'}>
        <div className="feed-measure">
          {showJump && (
            <button
              className="feed-jump"
              title={t('tx.viewInContext')}
              aria-label={t('tx.viewInContext')}
              onClick={() => jumpToContext(rowCoord(first))}
            >
              <Icon name="crosshair" size={13} />
            </button>
          )}
          <ToolGroupCard
            events={row.events}
            resultById={resultById}
            updateById={updateById}
            collapsed={collapsedGroups.has(row.key)}
            onToggle={() => toggleGroup(row.key)}
          />
        </div>
      </div>
    );
  }

  // Live-mode filtering, in mobile's exact call order (live_feed.dart:1029-1046):
  // drop feed noise (hide), then collapse streaming partials by message_id
  // (P1 port — codex/gemini text/thought chains and the hub-stamped plan chain
  // become one row that updates in place), then the lens, then drop folded
  // tool_results so the list data is exactly the rows that render. Tool
  // folding still runs over the FULL feed above, so a hidden telemetry row
  // never orphans a paired result.
  const visible = useMemo(
    () => feed.filter((ev) => !isHiddenInFeed(ev, verbose, nameById)),
    [feed, verbose, nameById],
  );
  const foldedStream = useMemo(() => collapseStreamingPartials(visible), [visible]);
  const shown = useMemo(
    () => (lens === 'all' ? foldedStream : foldedStream.filter((ev) => matchesLens(ev, lens, resultById))),
    [foldedStream, lens, resultById],
  );
  const liveData = useMemo(() => shown.filter((ev) => !isFolded(ev)), [shown, callIds]);
  // P1 tool-group cards (kimi-web tool-stack rule): consecutive tool_call rows
  // in the visible list render as ONE group card. Render-layer only — the
  // virtual list gets ROWS (single event or group); every reducer above
  // (busy, lens, matchCoords, unread) keeps the raw events.
  const liveRows = useMemo(() => groupToolCalls(liveData), [liveData]);
  // Insight applies the SAME feed-noise filter as live (mobile parity —
  // insight_transcript.dart:1377): turn.start / turn.result / usage / lifecycle
  // ("started M2") etc. are telemetry, hidden unless Details (verbose) is on.
  // Without this the sealed view showed rows the live feed hides. It also runs
  // the same streaming-partial fold (insight_transcript.dart:1378). Grouping
  // stays live-only — the sealed view keeps flat rows for exact seq targeting.
  const insightData = useMemo(
    () => collapseStreamingPartials(feed.filter((ev) => !isFolded(ev) && !isHiddenInFeed(ev, verbose, nameById))),
    [feed, callIds, verbose, nameById],
  );
  const matchCoords = useMemo(() => liveData.map((ev) => rowCoord(ev)), [liveData]);

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
  // Row count, not event count: the live virtual list's data is `liveRows`
  // (P1 groups), so tail-pinning scrolls to the last ROW.
  liveLenRef.current = liveRows.length;
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
    // Reset the scroll-to-latest tracking so a backfill / agent switch doesn't
    // arrive counted as unread (we open at the tail, i.e. atBottom).
    setAtBottom(true);
    setUnread(0);
    prevLiveLenRef.current = 0;
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
  // Count events that arrive while scrolled up as unread; clear when the user
  // returns to the tail (`atBottomStateChange`, below).
  useEffect(() => {
    const delta = liveData.length - prevLiveLenRef.current;
    prevLiveLenRef.current = liveData.length;
    if (delta > 0 && !atBottom) setUnread((u) => u + delta);
  }, [liveData.length, atBottom]);
  function jumpToLatest(): void {
    stickBottomRef.current = true;
    setUnread(0);
    const n = liveRows.length;
    if (n > 0) virtuosoRef.current?.scrollToIndex({ index: n - 1, align: 'end', behavior: 'smooth' });
  }
  // How many low-signal rows the verbose toggle would reveal (for its badge).
  const verboseHidden = useMemo(
    () => (verbose ? 0 : feed.filter((ev) => isHiddenInFeed(ev, false, nameById) && !isHiddenInFeed(ev, true, nameById)).length),
    [feed, verbose, nameById],
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

  // The loaded ordinal window [min,max] over ALL loaded events (incl. hidden
  // telemetry). session_ordinal is dense (gap-free, ADR-042), so a target inside
  // this range is guaranteed loaded; one outside needs a window fetch first.
  function loadedOrdRange(): { min: number; max: number } {
    let min = Infinity;
    let max = 0;
    for (const ev of feed) {
      if (ev.ord > 0) {
        if (ev.ord < min) min = ev.ord;
        if (ev.ord > max) max = ev.ord;
      }
    }
    return { min, max };
  }

  // Random-access window fetch around a jump target (mobile _resetWindowAround):
  // a backward half (session_ordinal < target, DESC) + a forward half (>= target,
  // ASC), merged into the feed. Only meaningful with a session scope (ordinal
  // cursors are session-keyed). The tail load only holds the newest window, so a
  // jump to an older turn has no row to land on until this pulls its block in.
  async function loadWindowAround(target: number): Promise<void> {
    if (client === null || scope === undefined || jumpLoadingRef.current) return;
    jumpLoadingRef.current = true;
    try {
      const half = 150;
      const [back, fwd] = await Promise.all([
        client.listAgentEvents(agentId, { session: scope, beforeOrdinal: target, limit: half }),
        client.listAgentEvents(agentId, { session: scope, afterOrdinal: target - 1, limit: half }),
      ]);
      const rows = [...back, ...fwd];
      if (rows.length === 0) {
        // Anchor out of the real range — abandon the jump rather than leave it
        // pending forever.
        setPendingJump(null);
        return;
      }
      setEvents((prev) => mergeEvents(prev, rows));
    } catch (err) {
      setError(msg(err));
      setPendingJump(null);
    } finally {
      jumpLoadingRef.current = false;
    }
  }

  // Page the previous window of history in when the user scrolls to the head
  // (#332 parity — mobile LiveFeed `_maybeLoadOlder`). Session-scoped by the dense
  // `session_ordinal` cursor (survives a resume — per-agent seq collides). We
  // stash the current top row's id, merge the older page (mergeEvents sorts it to
  // the front), then the reconcile effect re-anchors the view to that row so the
  // new rows appear above without a jump. `atHead` latches on a short/empty page.
  async function loadOlder(): Promise<void> {
    if (client === null || scope === undefined) return; // ordinal cursor is session-keyed
    if (loadingOlderRef.current || atHead || !settledRef.current) return;
    const { min } = loadedOrdRange();
    if (!Number.isFinite(min) || min <= 1) {
      setAtHead(true); // ordinal 1 is the session's first event — nothing older
      return;
    }
    loadingOlderRef.current = true;
    setLoadingOlder(true);
    prependAnchorRef.current = liveData.length > 0 ? liveData[0].id : null;
    try {
      const older = await client.listAgentEvents(agentId, { session: scope, beforeOrdinal: min, limit: OLDER_PAGE });
      if (older.length < OLDER_PAGE) setAtHead(true);
      if (older.length === 0) {
        loadingOlderRef.current = false;
        setLoadingOlder(false);
        prependAnchorRef.current = null;
        return;
      }
      // The reconcile effect clears the loading flags + re-anchors once the merged
      // rows land in liveData.
      setEvents((prev) => mergeEvents(prev, older));
    } catch (err) {
      setError(msg(err));
      loadingOlderRef.current = false;
      setLoadingOlder(false);
      prependAnchorRef.current = null;
    }
  }

  // Jump the virtual list to a navigation coordinate (Insight turn/error jump,
  // live stepper). The coordinate is the dense `session_ordinal` in a session-
  // scoped feed, else the per-agent `seq` — see `rowCoord`. Off-screen rows aren't
  // in the DOM, so we resolve the coordinate to an index in the active list, let
  // Virtuoso scroll to it, then flash it once it's mounted.
  function scrollToCoord(target: number): void {
    // Content not loaded (target outside the loaded ordinal window): window-load
    // around it, then the pendingJump effect re-runs this once it's merged in —
    // mobile _jumpToOrdinal → _resetWindowAround. Without this the nearest-at-or-
    // after fallback below lands on the loaded edge, not the requested turn.
    if (scope !== undefined && target > 0) {
      const { min, max } = loadedOrdRange();
      if (target < min || target > max) {
        setPendingJump(target);
        void loadWindowAround(target);
        return;
      }
    }
    // Resolve the coordinate to a row index in the active list. Live mode's
    // list is `liveRows` (P1): a group row SPANS the coords of its calls, so a
    // target inside a group lands on the group; insight keeps flat events.
    let index = -1;
    let landedId: string | undefined;
    if (mode === 'insight') {
      index = insightData.findIndex((ev) => rowCoord(ev) === target);
      if (index < 0) {
        // No exact row — a turn's start anchor is a turn.start marker
        // (ALWAYS_HIDDEN, so never a visible row) or an event outside the filtered
        // set. Land on the nearest visible row AT OR AFTER the target, i.e. the
        // turn's first rendered event (mobile parity — insight_transcript.dart:1404).
        // Without this fallback every turn resolved to -1 and the view never moved
        // ("jumps to the same position").
        let best = Infinity;
        for (let i = 0; i < insightData.length; i++) {
          const c = rowCoord(insightData[i]);
          if (c >= target && c < best) {
            best = c;
            index = i;
          }
        }
        // Nothing at or after (target past the tail) — fall back to the last row.
        if (index < 0 && insightData.length > 0) index = insightData.length - 1;
      }
      if (index >= 0) landedId = insightData[index].id;
    } else {
      const rowStart = (row: FeedRow): number => rowCoord(row.events[0]);
      const rowEnd = (row: FeedRow): number => rowCoord(row.events[row.events.length - 1]);
      index = liveRows.findIndex((row) => target >= rowStart(row) && target <= rowEnd(row));
      if (index < 0) {
        // Same nearest-at-or-after fallback as insight, over row STARTS.
        let best = Infinity;
        for (let i = 0; i < liveRows.length; i++) {
          const c = rowStart(liveRows[i]);
          if (c >= target && c < best) {
            best = c;
            index = i;
          }
        }
        if (index < 0 && liveRows.length > 0) index = liveRows.length - 1;
      }
      if (index >= 0) landedId = liveRows[index].events[0].id;
    }
    if (index < 0 || landedId === undefined) return;
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
    // Flash the row we actually landed on (may differ from the requested target),
    // keyed on its stable id (seq collides across a resumed session). For a
    // group row this is the group's first call — the flash class lights the
    // whole card.
    setFlashId(landedId);
    window.setTimeout(() => setFlashId((s) => (s === landedId ? null : s)), 1400);
  }

  function step(delta: number): void {
    if (matchCoords.length === 0) return;
    const next = Math.max(0, Math.min(matchCoords.length - 1, matchIndex + delta));
    setMatchIndex(next);
    scrollToCoord(matchCoords[next]);
  }

  // Once a window-load for a pending jump has merged the target's ordinal into the
  // feed, re-run the jump (now it lands on the real row). Re-checks on every feed
  // change so it fires exactly when the window arrives.
  useEffect(() => {
    if (pendingJump === null) return;
    const { min, max } = loadedOrdRange();
    if (pendingJump >= min && pendingJump <= max) {
      const target = pendingJump;
      setPendingJump(null);
      scrollToCoord(target);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [feed, pendingJump]);

  // After a load-older merge, re-anchor the viewport to the row that was at the
  // top before the prepend, so the newly-inserted older rows grow upward without
  // yanking the view (mobile does a post-frame `jumpTo` height-delta; here the
  // row's stable index + `align:'start'` is the Virtuoso-idiomatic equivalent).
  // useLayoutEffect so the re-anchor lands before paint. Runs on `liveRows` change
  // so it fires exactly when the page merges in.
  useLayoutEffect(() => {
    const anchorId = prependAnchorRef.current;
    if (anchorId === null) return;
    // The anchor id is an EVENT id (captured from liveData[0]); the list
    // renders ROWS, so find the row containing it (a group contains its calls).
    const idx = liveRows.findIndex((row) => row.events.some((ev) => ev.id === anchorId));
    if (idx > 0) virtuosoRef.current?.scrollToIndex({ index: idx, align: 'start' });
    prependAnchorRef.current = null;
    loadingOlderRef.current = false;
    setLoadingOlder(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [liveRows]);

  // Jump from a filtered match to its position in the FULL log (mobile
  // ContextJumpButton → `_jumpToContext`): clear the lens to `all`, then once the
  // unfiltered list rebuilds, scroll+flash the event at its own coordinate.
  // scrollToCoord window-loads around a target outside the loaded range.
  function jumpToContext(coord: number): void {
    if (lens === 'all') {
      scrollToCoord(coord);
      return;
    }
    setLensReset('all');
    setPendingContext(coord);
  }
  useEffect(() => {
    if (pendingContext === null || lens !== 'all') return;
    const target = pendingContext;
    setPendingContext(null);
    scrollToCoord(target);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingContext, lens, liveData]);

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
      await qc.invalidateQueries({ queryKey: ['agent', agentId] });
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

  /// Interrupt the in-flight turn (composer Stop) — a `cancel` INPUT the driver
  /// acts on, NOT the `/stop` lifecycle (that pauses the session and drops the
  /// agent from the live list, which the director reads as "archived"). Parity
  /// with mobile agent_compose `_cancel`. The cancel event flows back through the
  /// feed; no manual refetch.
  async function cancelTurn(): Promise<void> {
    if (client === null) return;
    try {
      await client.cancelAgentInput(agentId, 'user requested cancel');
    } catch (err) {
      setError(msg(err));
    }
  }

  const modes: { v: Mode; label: string }[] = [
    { v: 'live', label: t('tx.live') },
    { v: 'insight', label: t('tx.insight') },
    { v: 'digest', label: t('tx.digest') },
    { v: 'info', label: t('tx.info') },
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
          {running && (
            <button className="primary" disabled={busy} onClick={() => void lifecycle((id) => client!.stopAgent(id))}>
              {t('tx.stop')}
            </button>
          )}
          {paused && (
            <button disabled={busy} onClick={() => void lifecycle((id) => client!.resumeAgent(id))}>
              {t('tx.resume')}
            </button>
          )}
          <div className="life-overflow" ref={lifeMenuRef}>
            <button
              className="icon-btn"
              title={t('tx.more')}
              aria-label={t('tx.more')}
              aria-haspopup="menu"
              aria-expanded={lifeMenuOpen}
              onClick={() => setLifeMenuOpen((o) => !o)}
            >
              <Icon name="menu" size={16} />
            </button>
            {lifeMenuOpen && (
              <div className="life-menu" role="menu">
                <button
                  disabled={busy}
                  onClick={() => {
                    setLifeMenuOpen(false);
                    void lifecycle((id) => client!.pauseAgent(id));
                  }}
                >
                  {t('tx.pause')}
                </button>
                <button
                  disabled={busy}
                  onClick={() => {
                    setLifeMenuOpen(false);
                    void lifecycle((id) => client!.resumeAgent(id));
                  }}
                >
                  {t('tx.resume')}
                </button>
                <button
                  disabled={busy}
                  onClick={() => {
                    setLifeMenuOpen(false);
                    void lifecycle((id) => client!.stopAgent(id));
                  }}
                >
                  {t('tx.stop')}
                </button>
                <ConfirmButton
                  label={t('tx.terminate')}
                  danger
                  disabled={busy}
                  onConfirm={() => {
                    setLifeMenuOpen(false);
                    void lifecycle((id) => client!.terminateAgent(id));
                  }}
                />
                <ConfirmButton
                  label={t('tx.archive')}
                  disabled={busy}
                  onConfirm={() => {
                    setLifeMenuOpen(false);
                    void lifecycle((id) => client!.archiveAgent(id));
                  }}
                />
              </div>
            )}
          </div>
        </div>
      </div>

      {mode !== 'digest' && mode !== 'info' && (agentStatus !== undefined || stats.model !== undefined) && (
        <div className="transcript-status">
          {agentStatus !== undefined && (
            <span className="ts-state">
              <span
                className={`dot ${running ? 'running' : agentStatus === 'failed' || agentStatus === 'crashed' ? 'stopped' : 'muted'}`}
              />
              {agentStatus}
            </span>
          )}
          {stats.model !== undefined && <span className="ts-model">{stats.model}</span>}
          {stats.turns > 0 && (
            <>
              <span className="ts-sep">·</span>
              <span>
                {stats.turns} {t('tx.turnsLabel')}
              </span>
            </>
          )}
          {(stats.inTok > 0 || stats.outTok > 0) && (
            <>
              <span className="ts-sep">·</span>
              <span className="ts-tok">
                {t('tx.tokIn')} {fmtTok(stats.inTok)} · {t('tx.tokOut')} {fmtTok(stats.outTok)}
              </span>
            </>
          )}
          {stats.elapsed !== undefined && (
            <>
              <span className="ts-sep">·</span>
              <span>{fmtElapsed(stats.elapsed)}</span>
            </>
          )}
        </div>
      )}

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
                  {matchCoords.length === 0 ? '0' : `${matchIndex + 1}/${matchCoords.length}`} {t('tx.matched')}
                </span>
                <button disabled={matchCoords.length === 0} title={t('tx.prev')} onClick={() => step(-1)}>
                  <Icon name="chevron-up" size={14} />
                </button>
                <button disabled={matchCoords.length === 0} title={t('tx.next')} onClick={() => step(1)}>
                  <Icon name="chevron-down" size={14} />
                </button>
                <button title={t('tx.clear')} onClick={() => setLensReset('all')}>
                  <Icon name="close" size={14} />
                </button>
              </span>
            )}
          </div>
          {loaded ? (
            <div className="feed-wrap">
              <Virtuoso
                key={agentId}
                ref={virtuosoRef}
                className="feed-virt"
                style={settled ? undefined : { visibility: 'hidden' }}
                data={liveRows}
                scrollerRef={(el) => setFeedScroller(el instanceof HTMLElement ? el : null)}
                // One ROW per item (P1): a standalone event keys on its own id,
                // a tool-call group on `grp:<first-call-id>` — stable as the
                // group grows, so expand/collapse re-renders a single item and
                // Virtuoso's ResizeObserver re-measures it.
                computeItemKey={(_i, row) => row.key}
                initialTopMostItemIndex={{ index: Math.max(0, liveRows.length - 1), align: 'end' }}
                defaultItemHeight={120}
                alignToBottom
                followOutput={(bottom) => (bottom ? 'auto' : false)}
                atBottomStateChange={(bottom) => {
                  setAtBottom(bottom);
                  if (bottom) {
                    stickBottomRef.current = true;
                    setUnread(0);
                  }
                }}
                startReached={() => void loadOlder()}
                totalListHeightChanged={pinBottom}
                itemContent={(_i, row) => feedRow(row)}
                components={{
                  Header: () =>
                    loadingOlder ? (
                      <div className="feed-older muted" aria-busy="true">
                        {t('tx.loadingOlder')}
                      </div>
                    ) : atHead ? (
                      <div className="feed-older muted">{t('tx.historyStart')}</div>
                    ) : null,
                  EmptyPlaceholder: () => (
                    <div className="region-pad muted">{lens === 'all' ? t('tx.noEvents') : t('tx.noMatches')}</div>
                  ),
                }}
              />
              {settled && !atBottom && (
                <button className="scroll-latest" onClick={jumpToLatest} aria-label={t('tx.latest')}>
                  <Icon name="arrow-down" size={14} />
                  {unread > 0 ? `${unread} ${t('tx.new')}` : t('tx.latest')}
                </button>
              )}
            </div>
          ) : (
            <div className="feed-skeleton" aria-busy="true" aria-label={t('common.loading')}>
              {[0, 1, 2, 3].map((i) => (
                <div key={i} className="feed-item">
                  <div className="feed-measure">
                    <div className="sk-card">
                      <div className="sk-line w60" />
                      <div className="sk-line w90" />
                      <div className="sk-line w40" />
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {/* P2 state dock — live mode only, outside the virtual list,
              directly above the composer. */}
          <StateDock model={stateDock} />
          <Composer
            onSend={send}
            generating={generating}
            onStop={() => void cancelTurn()}
            inject={quoteSignal}
          />
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
                <>
                  <div className="tabs">
                    <button className={navTab === 'turns' ? 'tab active' : 'tab'} onClick={() => setNavTab('turns')}>
                      {t('insight.turns')} <span className="pill">{turns.length}</span>
                    </button>
                    <button className={navTab === 'errors' ? 'tab active' : 'tab'} onClick={() => setNavTab('errors')}>
                      {t('insight.errors')} <span className="pill">{errorRows.length}</span>
                    </button>
                  </div>
                  <span className="spacer" />
                  <button
                    className={verbose ? 'feed-verbose active' : 'feed-verbose'}
                    title={t('tx.verboseHint')}
                    onClick={() => setVerbose((v) => !v)}
                  >
                    {verbose ? t('tx.hideNoise') : t('tx.showNoise')}
                    {!verbose && verboseHidden > 0 && <span className="pill">{verboseHidden}</span>}
                  </button>
                </>
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
                    // Jump anchor: the dense `start_ordinal` (session_ordinal
                    // space, ADR-042), falling back to the per-agent `start_seq`
                    // for pre-migration digests (mobile insight_transcript.dart:753).
                    const startOrd = num(tn, 'start_ordinal') ?? 0;
                    const startSeq = num(tn, 'start_seq');
                    const anchor = startOrd > 0 ? startOrd : startSeq;
                    const status = str(tn, 'status') ?? (tn['open'] === true ? 'open' : 'done');
                    const errs = num(tn, 'error_count') ?? 0;
                    const toolN = num(tn, 'tool_count') ?? 0;
                    const toolF = num(tn, 'tool_failed') ?? 0;
                    const dur = num(tn, 'duration_ms');
                    return (
                      <button
                        key={str(tn, 'turn_id') ?? String(i)}
                        className="insight-row"
                        disabled={anchor === undefined}
                        onClick={() => anchor !== undefined && scrollToCoord(anchor)}
                      >
                        <span className={`dot ${errs > 0 ? 'stopped' : status === 'open' ? 'running' : 'muted'}`} />
                        <span className="insight-row-title">
                          {t('insight.turn')} {i + 1}
                        </span>
                        <span className="spacer" />
                        <span className="muted small">
                          {toolN > 0 && (
                            <span className="ir-stat">
                              <Icon name="wrench" size={12} /> {toolN - toolF}/{toolN}
                            </span>
                          )}
                          {errs > 0 && (
                            <span className="ir-stat err">
                              <Icon name="close" size={12} /> {errs}
                            </span>
                          )}
                          {dur !== undefined && <span className="ir-stat">{Math.round(dur / 100) / 10}s</span>}
                        </span>
                      </button>
                    );
                  })
                )
              ) : errorRows.length === 0 ? (
                <div className="muted region-pad">{t('insight.noErrors')}</div>
              ) : (
                errorRows.map((ev) => (
                  <button key={ev.id} className="insight-row" onClick={() => scrollToCoord(rowCoord(ev))}>
                    <span className="dot stopped" />
                    <span className="insight-row-title">{errorLabel(ev, nameById)}</span>
                    <span className="spacer" />
                    <span className="muted small mono">#{ev.ord > 0 ? ev.ord : ev.seq}</span>
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

      {mode === 'info' && (
        <div className="region-pad info-scroll">
          {agentQ.isLoading && agentQ.data === undefined && <div className="muted">{t('common.loading')}</div>}
          {agentQ.isError && <div className="error">{msg(agentQ.error)}</div>}
          {agentQ.data !== undefined ? (
            <AgentInfo agent={agentQ.data} init={sessionInit} status={statusLine} t={t} />
          ) : null}
        </div>
      )}
    </div>
  );
}
