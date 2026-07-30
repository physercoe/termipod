import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { episodeEnvRef, formatCount, formatDuration, type DatasetSummary } from '../state/replayDigest';
import {
  formatSample,
  nearestPointIndex,
  readSeriesPage,
  timeToX,
  valueAt,
  xToTime,
} from '../state/replaySeries';
import type { EpisodeRow, EpisodeVideo } from '../state/replayDigest';
import { episodeVideoSftpUrl, episodeVideoUrl, fileTimeOf, isPastEnd } from '../state/replayMedia';
import { liveConnIds, liveSessionFor } from '../state/replayRemote';
import { remoteMediaConn, setRemoteMediaConn } from '../state/replayRemoteStore';
import { listConnections } from '../state/connections';
import { useTerminals } from '../terminal/store';
import { pickPoseFeature } from '../state/robotManifest';
import { ReplayPose3D } from './ReplayPose3D';
import { RerunExportButton, RerunViewerPanel } from './RerunExport';
import { isShell } from '../platform';

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
  rootPath,
  episode,
  summary,
  onClose,
}: {
  datasetId: string;
  /// The dataset root, joined to each video's host-resolved relative path.
  rootPath: string;
  episode: EpisodeRow;
  summary: DatasetSummary;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState<number | null>(null);
  const [showPose, setShowPose] = useState(true);
  /// The loopback URL of a Rerun viewer this player started, or null. Owned here
  /// rather than inside the button so the panel can sit in the layout above the
  /// video grid instead of inside the header row.
  const [rerunUrl, setRerunUrl] = useState<string | null>(null);

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

  const envRef = episodeEnvRef(episode, summary);
  const shown = view.features.filter((f) => !hidden.has(f.key));
  const cursorIndex = cursor === null ? -1 : nearestPointIndex(view.timestamps, cursor);
  const cursorTime = cursorIndex >= 0 ? view.timestamps[cursorIndex] : null;

  // The pose panel is driven by whichever feature describes the robot's own
  // configuration — never by a hidden-feature toggle, which is about the plots.
  const poseKey = pickPoseFeature(view.features.map((f) => f.key));
  const poseFeature = poseKey === null ? null : (view.features.find((f) => f.key === poseKey) ?? null);

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
          {/* The environment this episode was recorded in (plan §2's env chip,
              environments plan E0). Shown verbatim: E0 has no registry to
              resolve the handle against, and inventing a prettier rendering
              would be parsing a string the model says is opaque. */}
          {envRef !== '' && (
            <>
              {' · '}
              {t('replay.player.env')} <span className="mono">{envRef}</span>
            </>
          )}
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
        <RerunExportButton datasetId={datasetId} episode={episode.index} onViewer={setRerunUrl} />
        {poseFeature !== null && (
          <button
            type="button"
            className={`replay-toggle${showPose ? ' is-on' : ''}`}
            onClick={() => setShowPose((s) => !s)}
            aria-pressed={showPose}
          >
            {t('replay.pose.title')}
          </button>
        )}
        <button type="button" className="link-btn small" onClick={onClose}>
          {t('replay.player.close')}
        </button>
      </div>

      {episode.tasks.length > 0 && <div className="replay-player-task small">{episode.tasks.join(' · ')}</div>}

      {rerunUrl !== null && <RerunViewerPanel url={rerunUrl} onClose={() => setRerunUrl(null)} />}

      <VideoGrid datasetId={datasetId} rootPath={rootPath} episode={episode} summary={summary} cursor={cursorTime} />

      {showPose && poseFeature !== null && (
        <ReplayPose3D robotType={summary.robotType} feature={poseFeature} cursorIndex={cursorIndex} />
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

/// The multi-camera video grid.
///
/// No transcoding and no clip extraction: the Electron main process serves the
/// file with range support and each `<video>` seeks to its episode's slice.
/// That works because a real LeRobot mp4 carries a keyframe every 0.4s, so a
/// seek costs at most one extra decoded frame — measured, not assumed.
///
/// A v3.0 file holds every episode of the dataset, so the pane has to STOP at
/// the slice end; left alone, playback rolls into the next episode and looks
/// like the robot teleporting rather than like a player that forgot to stop.
function VideoGrid({
  datasetId,
  rootPath,
  episode,
  summary,
  cursor,
}: {
  datasetId: string;
  rootPath: string;
  episode: EpisodeRow;
  summary: DatasetSummary;
  cursor: number | null;
}): JSX.Element | null {
  const t = useT();
  const tabs = useTerminals((s) => s.tabs);
  // The persisted source choice for THIS dataset: null = this machine's disk,
  // a connection id = stream over that connection's live SSH session (J8
  // remote datasets — plots already ride the hub; video bytes ride SFTP).
  const [conn, setConn] = useState<string | null>(() => remoteMediaConn(datasetId));
  const [anyFailed, setAnyFailed] = useState(false);

  // The dataset declares cameras but this episode carries no playable slice —
  // an older digest, or a template the host could not resolve. Saying which of
  // the two states this is beats an empty area that reads as a layout bug.
  if (episode.videos.length === 0) {
    if (summary.videoStreams.length === 0) return null;
    return (
      <div className="replay-player-novideo small muted">
        {t('replay.player.noVideoSlices').replace('{n}', String(summary.videoStreams.length))}
      </div>
    );
  }
  // The media scheme lives in the Electron main process; a plain-browser build
  // has no way to read a local file at all.
  if (!isShell()) {
    return <div className="replay-player-novideo small muted">{t('replay.player.videoDesktopOnly')}</div>;
  }

  const session = conn !== null ? liveSessionFor(conn, tabs) : null;
  const urlFor = (v: EpisodeVideo): string | null =>
    conn !== null && session !== null ? episodeVideoSftpUrl(session, rootPath, v) : episodeVideoUrl(rootPath, v);
  const pick = (next: string | null): void => {
    setRemoteMediaConn(datasetId, next);
    setConn(next);
    setAnyFailed(false);
  };
  // Offer the picker once local playback failed, or whenever a remote source
  // is (or could be) in play — an all-local dataset that plays never shows it.
  const options = liveConnIds(tabs);
  const conns = listConnections();
  const nameOf = (id: string): string => conns.find((c) => c.id === id)?.name ?? id;
  const showPicker = anyFailed || conn !== null;

  return (
    <>
      {showPicker && (
        <div className="replay-video-source small">
          <span className="muted">{t('replay.player.videoSource')}</span>
          <select value={conn ?? ''} onChange={(e) => pick(e.target.value === '' ? null : e.target.value)}>
            <option value="">{t('replay.player.videoSourceLocal')}</option>
            {options.map((id) => (
              <option key={id} value={id}>
                {nameOf(id)}
              </option>
            ))}
            {conn !== null && !options.includes(conn) && (
              <option value={conn}>{nameOf(conn)}</option>
            )}
          </select>
          {conn !== null && session === null && (
            <span className="replay-video-source-warn">
              {t('replay.player.videoSourceDead').replace('{name}', nameOf(conn))}
            </span>
          )}
        </div>
      )}
      <div className="replay-video-grid">
        {episode.videos.map((v) => (
          <VideoPane key={v.key} url={urlFor(v)} video={v} cursor={cursor} onFail={() => setAnyFailed(true)} />
        ))}
      </div>
    </>
  );
}

function VideoPane({
  url,
  video,
  cursor,
  onFail,
}: {
  url: string | null;
  video: EpisodeVideo;
  cursor: number | null;
  onFail: () => void;
}): JSX.Element {
  const t = useT();
  const ref = useRef<HTMLVideoElement>(null);
  const [failed, setFailed] = useState(false);

  // Park the playhead at the episode's start whenever the slice changes. A
  // shared v3.0 file opens at 0, which is some other episode entirely.
  useEffect(() => {
    setFailed(false);
    const el = ref.current;
    if (el === null) return;
    const seek = (): void => {
      el.currentTime = video.fromTS;
    };
    if (el.readyState >= 1) seek();
    else el.addEventListener('loadedmetadata', seek, { once: true });
    return () => el.removeEventListener('loadedmetadata', seek);
  }, [url, video.fromTS]);

  // Follow the plot cursor. Only while paused: during playback the video owns
  // the clock, and writing currentTime on every frame would fight the decoder.
  useEffect(() => {
    const el = ref.current;
    if (el === null || cursor === null || !el.paused) return;
    const want = fileTimeOf(video, cursor);
    // A tolerance, because assigning currentTime triggers a seek and seeks are
    // not free; a sub-frame correction is not worth one.
    if (Math.abs(el.currentTime - want) > 0.02) el.currentTime = want;
  }, [cursor, video]);

  if (url === null || failed) {
    return (
      <div className="replay-video-pane">
        <div className="replay-video-head small mono">{video.key}</div>
        <div className="replay-video-missing small muted">{t('replay.player.videoUnreadable')}</div>
      </div>
    );
  }

  return (
    <div className="replay-video-pane">
      <div className="replay-video-head small mono">{video.key}</div>
      <video
        ref={ref}
        className="replay-video"
        src={url}
        controls
        preload="metadata"
        onError={() => {
          setFailed(true);
          onFail();
        }}
        // Stop at the slice end rather than rolling into the next episode.
        onTimeUpdate={(e) => {
          const el = e.currentTarget;
          if (!el.paused && isPastEnd(video, el.currentTime)) {
            el.pause();
            el.currentTime = video.toTS;
          }
        }}
      />
    </div>
  );
}
