import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { markerLabel, MARKER_SEARCH, WARN_SEARCH } from '../state/ansi';
import { ansiToSpans } from '../state/ansiSpans';
import { IndexedLogModel, MemoryLogModel, type LogHit, type LogModel } from '../state/logModel';

/// Where a `LogView` reads from: a main-process line index over a big local file
/// (`index` — the headline case, never slurped over IPC), or an in-memory string
/// (`memory` — a paste scratch, or a bounded remote/hub slice already fetched as
/// text). The two back the same UI via the `LogModel` interface.
export type LogSource = { kind: 'index'; path: string } | { kind: 'memory'; text: string };

// Lines are pulled in fixed blocks so scrolling reuses adjacent reads.
const BLOCK = 500;
// A jump list of thousands of per-step markers is useless — sample down to a
// scannable set spanning the run.
const MAX_MARKERS = 60;

// Treat the search box as a regex; if it doesn't compile, match it literally so a
// half-typed `(` doesn't blow up.
function toPattern(q: string): string {
  try {
    new RegExp(q);
    return q;
  } catch {
    return q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
}

function sample<T>(xs: T[], n: number): T[] {
  if (xs.length <= n) return xs;
  const step = xs.length / n;
  const out: T[] = [];
  for (let i = 0; i < n; i += 1) out.push(xs[Math.floor(i * step)]);
  return out;
}

function LogRow({ lineNo, text, wrap, isHit }: { lineNo: number; text: string | undefined; wrap: boolean; isHit: boolean }): JSX.Element {
  return (
    <div className={`logview-row${wrap ? ' wrap' : ''}${isHit ? ' hit' : ''}`}>
      <span className="logview-ln">{lineNo + 1}</span>
      <span className="logview-txt">
        {text === undefined ? (
          <span className="logview-pending">…</span>
        ) : text === '' ? (
          ' '
        ) : (
          ansiToSpans(text).map((s, i) => (
            <span
              key={i}
              style={{
                color: s.color,
                fontWeight: s.bold ? 700 : undefined,
                fontStyle: s.italic ? 'italic' : undefined,
                textDecoration: s.underline ? 'underline' : undefined,
                opacity: s.dim ? 0.6 : undefined,
              }}
            >
              {s.text}
            </span>
          ))
        )}
      </span>
    </div>
  );
}

/// Virtualized ANSI log viewer (plan §4 W3): follow/tail, error-warn quick-filter,
/// regex search with a hit rail + prev/next, and a step/epoch marker jump list —
/// over a windowed line cache so a 100 MB log scrolls without loading whole.
export function LogView({ source }: { source: LogSource }): JSX.Element {
  const t = useT();
  const [model, setModel] = useState<LogModel | null>(null);
  const [total, setTotal] = useState(0);
  const [err, setErr] = useState<string | null>(null);
  const cache = useRef<Map<number, string>>(new Map());
  const loadedBlocks = useRef<Set<number>>(new Set());
  const [, bump] = useReducer((x: number) => x + 1, 0);
  const vRef = useRef<VirtuosoHandle>(null);

  const [warnFilter, setWarnFilter] = useState(false);
  const [rows, setRows] = useState<number[] | null>(null); // filtered real-line subset, or null = all
  const [filterTrunc, setFilterTrunc] = useState(false);

  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<LogHit[]>([]);
  const [hitIdx, setHitIdx] = useState(-1);
  const [searchTrunc, setSearchTrunc] = useState(false);

  const [markers, setMarkers] = useState<Array<{ line: number; label: string }>>([]);
  const [markerOpen, setMarkerOpen] = useState(false);

  const [wrap, setWrap] = useState(false);
  const [follow, setFollow] = useState(false);

  const rowCount = rows !== null ? rows.length : total;
  const realLine = useCallback((i: number): number => (rows !== null ? (rows[i] ?? -1) : i), [rows]);
  const displayIndex = useCallback(
    (line: number): number => (rows !== null ? rows.indexOf(line) : line),
    [rows],
  );

  // Build the model when the source changes; pre-scan a sampled marker list.
  const srcKey = source.kind === 'index' ? source.path : source.text;
  useEffect(() => {
    let cancelled = false;
    let m: LogModel | null = null;
    cache.current.clear();
    loadedBlocks.current.clear();
    setModel(null);
    setTotal(0);
    setErr(null);
    setRows(null);
    setWarnFilter(false);
    setQuery('');
    setHits([]);
    setHitIdx(-1);
    setMarkers([]);
    void (async () => {
      try {
        m = source.kind === 'index' ? await IndexedLogModel.open(source.path) : new MemoryLogModel(source.text);
        if (cancelled) {
          m.close();
          return;
        }
        setModel(m);
        setTotal(m.count());
        const mk = await m.search(MARKER_SEARCH, 'i', 2000);
        const picked = sample(mk.hits, MAX_MARKERS);
        const labelled = await Promise.all(
          picked.map(async (h) => {
            const line = (await m!.slice(h.line, 1))[0] ?? '';
            return { line: h.line, label: markerLabel(line) ?? `${t('log.line')} ${h.line + 1}` };
          }),
        );
        if (!cancelled) setMarkers(labelled);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
      m?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source.kind, srcKey]);

  // Load the blocks a display range needs (with a little overscan).
  const ensure = useCallback(
    async (startIndex: number, endIndex: number): Promise<void> => {
      if (model === null) return;
      const blocks = new Set<number>();
      for (let i = Math.max(0, startIndex - 10); i <= endIndex + 10; i += 1) {
        const ln = realLine(i);
        if (ln < 0 || ln >= total) continue;
        blocks.add(Math.floor(ln / BLOCK));
      }
      const todo = [...blocks].filter((b) => !loadedBlocks.current.has(b));
      if (todo.length === 0) return;
      todo.forEach((b) => loadedBlocks.current.add(b));
      for (const b of todo) {
        try {
          const lines = await model.slice(b * BLOCK, BLOCK);
          for (let k = 0; k < lines.length; k += 1) cache.current.set(b * BLOCK + k, lines[k]);
        } catch {
          loadedBlocks.current.delete(b);
        }
      }
      bump();
    },
    [model, realLine, total],
  );

  // Initial fill once the model / filter is ready.
  useEffect(() => {
    if (model !== null) void ensure(0, 60);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [model, rows]);

  // Error/warn quick-filter → a real-line subset the list renders over.
  useEffect(() => {
    if (model === null) return;
    if (!warnFilter) {
      setRows(null);
      setFilterTrunc(false);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const r = await model.search(WARN_SEARCH, 'i', 20000);
        if (!cancelled) {
          setRows(r.hits.map((h) => h.line));
          setFilterTrunc(r.truncated);
        }
      } catch {
        if (!cancelled) setRows([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [warnFilter, model, total]);

  const jumpToLine = useCallback(
    (line: number) => {
      const idx = displayIndex(line);
      if (idx >= 0) vRef.current?.scrollToIndex({ index: idx, align: 'center' });
    },
    [displayIndex],
  );

  // Debounced search.
  useEffect(() => {
    if (model === null) return;
    const q = query.trim();
    if (q === '') {
      setHits([]);
      setHitIdx(-1);
      setSearchTrunc(false);
      return;
    }
    let cancelled = false;
    const id = window.setTimeout(async () => {
      try {
        const r = await model.search(toPattern(q), 'i', 5000);
        if (cancelled) return;
        setHits(r.hits);
        setSearchTrunc(r.truncated);
        setHitIdx(r.hits.length > 0 ? 0 : -1);
        if (r.hits.length > 0) jumpToLine(r.hits[0].line);
      } catch {
        if (!cancelled) {
          setHits([]);
          setHitIdx(-1);
        }
      }
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, model]);

  // Follow mode (indexed local files only): poll for growth, keep the tail in view.
  useEffect(() => {
    if (!follow || model === null || source.kind !== 'index') return;
    const id = window.setInterval(async () => {
      const grew = await model.refresh();
      if (!grew) return;
      // The block that held the old tail may have had a partial final line — drop
      // it so the completed line re-reads.
      loadedBlocks.current.delete(Math.floor(Math.max(0, total - 1) / BLOCK));
      const n = model.count();
      setTotal(n);
      vRef.current?.scrollToIndex({ index: (rows !== null ? rows.length : n) - 1, align: 'end' });
    }, 1000);
    return () => window.clearInterval(id);
  }, [follow, model, total, rows, source.kind]);

  function step(delta: number): void {
    if (hits.length === 0) return;
    const next = (hitIdx + delta + hits.length) % hits.length;
    setHitIdx(next);
    jumpToLine(hits[next].line);
  }

  if (err !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {err}
      </div>
    );
  if (model === null) return <div className="muted region-pad">{t('inspect.loading')}</div>;

  const railHits = sample(hits, 400);

  return (
    <div className="logview">
      <div className="logview-bar">
        <span className="small muted logview-count">
          {total.toLocaleString()} {t('log.lines')}
          {rows !== null && <> · {rowCount.toLocaleString()} {t('log.matching')}</>}
        </span>
        <span className="spacer" />
        <div className="logview-search">
          <Icon name="search" size={13} />
          <input
            className="logview-input"
            value={query}
            placeholder={t('log.searchPlaceholder')}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') step(e.shiftKey ? -1 : 1);
            }}
          />
          {query.trim() !== '' && (
            <span className="logview-hitn small muted">
              {hits.length === 0 ? t('log.noMatches') : `${hitIdx + 1}/${hits.length}${searchTrunc ? '+' : ''}`}
            </span>
          )}
          <button className="icon-btn" title={t('log.prevMatch')} disabled={hits.length === 0} onClick={() => step(-1)}>
            <Icon name="chevron-up" size={14} />
          </button>
          <button className="icon-btn" title={t('log.nextMatch')} disabled={hits.length === 0} onClick={() => step(1)}>
            <Icon name="chevron-down" size={14} />
          </button>
        </div>
        {markers.length > 0 && (
          <div className="logview-markwrap">
            <button className="logview-btn" aria-haspopup="menu" aria-expanded={markerOpen} onClick={() => setMarkerOpen((o) => !o)}>
              <Icon name="list-ordered" size={13} /> {t('log.markers')} <Icon name="chevron-down" size={11} />
            </button>
            {markerOpen && (
              <>
                <div className="inspect-menu-scrim" onClick={() => setMarkerOpen(false)} />
                <div className="inspect-menu logview-markmenu" role="menu">
                  {markers.map((mk, i) => (
                    <button
                      key={i}
                      className="inspect-menu-item"
                      role="menuitem"
                      onClick={() => {
                        setMarkerOpen(false);
                        jumpToLine(mk.line);
                      }}
                    >
                      <span className="logview-mark-ln small muted">{mk.line + 1}</span> {mk.label}
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>
        )}
        <button className={`logview-btn${warnFilter ? ' on' : ''}`} title={t('log.warnFilter')} onClick={() => setWarnFilter((w) => !w)}>
          <Icon name="alert" size={13} /> {t('log.warnErr')}
          {filterTrunc && <span className="small">+</span>}
        </button>
        <button className={`logview-btn${wrap ? ' on' : ''}`} title={t('log.wrap')} onClick={() => setWrap((w) => !w)}>
          <Icon name="wrap" size={13} />
        </button>
        {source.kind === 'index' && (
          <button className={`logview-btn${follow ? ' on' : ''}`} title={t('log.follow')} onClick={() => setFollow((f) => !f)}>
            <Icon name="arrow-down" size={13} /> {t('log.tail')}
          </button>
        )}
      </div>
      <div className="logview-body">
        {rowCount === 0 ? (
          <div className="muted region-pad">{rows !== null ? t('log.noMatches') : t('log.empty')}</div>
        ) : (
          <>
            <Virtuoso
              ref={vRef}
              className="logview-list"
              totalCount={rowCount}
              overscan={600}
              rangeChanged={(r) => void ensure(r.startIndex, r.endIndex)}
              itemContent={(i) => {
                const ln = realLine(i);
                return <LogRow lineNo={ln} text={cache.current.get(ln)} wrap={wrap} isHit={hitIdx >= 0 && hits[hitIdx]?.line === ln} />;
              }}
            />
            {railHits.length > 0 && total > 0 && (
              <div className="logview-rail" title={t('log.hitRail')}>
                {railHits.map((h, i) => (
                  <button
                    key={i}
                    className="logview-tick"
                    style={{ top: `${(h.line / total) * 100}%` }}
                    onClick={() => {
                      const at = hits.findIndex((x) => x.line === h.line);
                      if (at >= 0) setHitIdx(at);
                      jumpToLine(h.line);
                    }}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
