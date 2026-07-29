import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { formatCount, formatDuration, type DatasetSummary } from '../state/replayDigest';
import {
  formatSample,
  nearestPointIndex,
  readSeriesPage,
  timeToX,
  valueAt,
  xToTime,
} from '../state/replaySeries';
import type { EpisodeRow } from '../state/replayDigest';

/// The episode player's **plots** half (J8 W2c): one episode's channels against
/// a shared time axis, with a cursor that reads out every channel at once.
///
/// The video grid is W2d — it needs a capped range-request media protocol in
/// the Electron main process, which is a wedge of its own. Rather than mock a
/// video pane, this says plainly that video is not here yet: an empty black
/// rectangle would read as a broken player rather than an unbuilt one.
///
/// All plot geometry lives in `state/replaySeries.ts` so it can be asserted
/// headlessly. What is left here is markup, pointer events, and one query.

/// Plot box geometry. The width is a viewBox unit, not pixels — the SVG scales
/// to its container, so the paths are resolution-independent and the cursor
/// maths works in the same space the tests use.
const PLOT_W = 1000;
const PLOT_H = 88;

/// Point budget per channel. A plot lane is a few hundred pixels wide, so more
/// than this is bytes over the wire that no display can resolve.
const MAX_POINTS = 1200;

export function EpisodePlayer({
  datasetId,
  episode,
  summary,
  onClose,
}: {
  datasetId: string;
  episode: EpisodeRow;
  summary: DatasetSummary;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState<number | null>(null);

  const seriesQ = useQuery({
    queryKey: ['dataset-series', datasetId, episode.index],
    enabled: client !== null && datasetId !== '',
    // Deliberately unfiltered: the host decides which features are numeric, and
    // asking for a subset would make the toggles below a network round-trip
    // each. An episode's decimated series is kilobytes.
    queryFn: () => client!.getEpisodeSeries(datasetId, episode.index, { maxPoints: MAX_POINTS }),
  });

  const view = useMemo(
    () => readSeriesPage(seriesQ.data, PLOT_W, PLOT_H),
    [seriesQ.data],
  );

  // A new episode starts with no cursor: carrying one over would point at a
  // moment that may not exist in a shorter episode.
  useEffect(() => {
    setCursor(null);
  }, [episode.index]);

  const shown = view.features.filter((f) => !hidden.has(f.key));
  const cursorIndex = cursor === null ? -1 : nearestPointIndex(view.timestamps, cursor);
  const cursorTime = cursorIndex >= 0 ? view.timestamps[cursorIndex] : null;

  /// Map a pointer position onto the episode clock. The SVG is scaled by CSS,
  /// so the ratio through the bounding box is what converts — reading clientX
  /// as a viewBox coordinate would put the cursor wherever the window happened
  /// to be sized.
  function seek(clientX: number, el: SVGSVGElement | null): void {
    if (el === null) return;
    const box = el.getBoundingClientRect();
    if (box.width <= 0) return;
    const ratio = (clientX - box.left) / box.width;
    setCursor(xToTime(ratio * PLOT_W, view.durationSec, PLOT_W));
  }

  function toggle(key: string): void {
    setHidden((s) => {
      const n = new Set(s);
      if (n.has(key)) n.delete(key);
      else n.add(key);
      return n;
    });
  }

  return (
    <div className="replay-player">
      <div className="replay-player-head">
        <div className="replay-player-title">
          {t('replay.player.episode').replace('{n}', String(episode.index))}
        </div>
        <div className="replay-sub muted small">
          {formatCount(episode.length)} {t('replay.player.frames')}
          {' · '}
          {formatDuration(episode.durationSec)}
          {view.downsampled && (
            <>
              {' · '}
              <span className="replay-cap">
                {t('replay.player.decimated').replace('{n}', String(view.stride))}
              </span>
            </>
          )}
        </div>
        <span className="spacer" />
        <button type="button" className="link-btn small" onClick={onClose}>
          {t('replay.player.close')}
        </button>
      </div>

      {episode.tasks.length > 0 && <div className="replay-player-task small">{episode.tasks.join(' · ')}</div>}

      {/* The honest placeholder. Video is W2d; an empty black rectangle would
          read as a broken player rather than an unbuilt one. */}
      {summary.videoStreams.length > 0 && (
        <div className="replay-player-novideo small muted">
          {t('replay.player.videoLater').replace('{n}', String(summary.videoStreams.length))}
        </div>
      )}

      {seriesQ.isPending && <div className="muted small region-pad">{t('replay.player.loading')}</div>}
      {seriesQ.isError && (
        <div className="replay-error small">
          {seriesQ.error instanceof Error ? seriesQ.error.message : String(seriesQ.error)}
        </div>
      )}

      {!seriesQ.isPending && !seriesQ.isError && !view.hasSeries && (
        <div className="muted small region-pad">{t('replay.player.noSeries')}</div>
      )}

      {view.hasSeries && (
        <>
          <div className="replay-player-toggles small">
            {view.features.map((f) => (
              <button
                key={f.key}
                type="button"
                className={`replay-toggle${hidden.has(f.key) ? '' : ' is-on'}`}
                onClick={() => toggle(f.key)}
                aria-pressed={!hidden.has(f.key)}
              >
                <span className="mono">{f.key}</span>
                <span className="muted"> {f.channels.length}</span>
              </button>
            ))}
          </div>

          {/* onPointerLeave belongs to the CONTAINER, not to each plot: dragging
              the cursor from one lane to the next crosses a gap between two
              SVGs, and per-plot leave handlers would blank the readout on every
              such crossing. */}
          <div className="replay-plots" onPointerLeave={() => setCursor(null)}>
            {shown.map((f) => (
              <div key={f.key} className="replay-plot">
                <div className="replay-plot-head small">
                  <span className="mono">{f.key}</span>
                  <span className="muted"> {formatSample(f.min)} … {formatSample(f.max)}</span>
                </div>
                <svg
                  className="replay-plot-svg"
                  viewBox={`0 0 ${PLOT_W} ${PLOT_H}`}
                  preserveAspectRatio="none"
                  role="img"
                  aria-label={f.key}
                  onPointerMove={(e) => seek(e.clientX, e.currentTarget)}
                >
                  {f.channels.map((c, i) => (
                    <path
                      key={c.name !== '' ? c.name : i}
                      d={c.path}
                      className="replay-trace"
                      style={{ stroke: traceColor(i) }}
                    />
                  ))}
                  {cursorTime !== null && (
                    <line
                      className="replay-cursor"
                      x1={timeToX(cursorTime, view.durationSec, PLOT_W)}
                      x2={timeToX(cursorTime, view.durationSec, PLOT_W)}
                      y1={0}
                      y2={PLOT_H}
                    />
                  )}
                </svg>
                <ul className="replay-legend small">
                  {f.channels.map((c, i) => (
                    <li key={c.name !== '' ? c.name : i} className="replay-legend-item">
                      <span className="replay-swatch" style={{ background: traceColor(i) }} />
                      <span className="mono">{c.name !== '' ? c.name : String(i)}</span>
                      {cursorIndex >= 0 && (
                        <span className="mono muted"> {formatSample(valueAt(c, cursorIndex))}</span>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
            {shown.length === 0 && <div className="muted small region-pad">{t('replay.player.allHidden')}</div>}
          </div>

          <div className="replay-timeline small">
            <span className="mono">{formatDuration(cursorTime ?? 0)}</span>
            <span className="muted"> / {formatDuration(view.durationSec)}</span>
            {cursorIndex >= 0 && (
              <span className="muted">
                {' · '}
                {t('replay.player.frame').replace('{n}', String(cursorIndex * view.stride))}
              </span>
            )}
          </div>

          {view.warnings.map((w) => (
            <div key={w} className="replay-cap small">
              {w}
            </div>
          ))}
        </>
      )}
    </div>
  );
}

/// Trace colours.
///
/// Not design tokens: a plot needs N *distinguishable* colours where N is the
/// dataset's channel count, and the token palette is a fixed set of semantic
/// roles — there is no `--channel-4`. Generated by golden-angle rotation so any
/// number of channels stays separable, at a lightness that survives both
/// themes.
function traceColor(i: number): string {
  return `hsl(${(i * 137.508) % 360} 65% 55%)`;
}
