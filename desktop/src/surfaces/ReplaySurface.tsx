import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHosts, useProjects } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useHubAction } from '../hub/action';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { useReplay } from '../state/replay';
import {
  formatCount,
  formatDuration,
  formatResolution,
  pageRangeLabel,
  readDatasetSummary,
  readEpisodePage,
  resolveHandoff,
  type DatasetSummary,
} from '../state/replayDigest';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';
import { EpisodePlayer } from './EpisodePlayer';

/// J8 — Replay. The destination surface for episode data: a dataset library on
/// the left, the selected dataset's digest and episodes table in the centre.
///
/// W1 is the entity plus the navigation skeleton, and it is deliberately useful
/// on its own — before this, nothing in the product could show that a dataset
/// exists at all. The player (W2) replaces the table's row action; the 3D pose
/// panel (W3) and the Rerun companion (W4) are panes beside it.
///
/// Two honesty rules the plan makes non-negotiable, both visible here:
///   - A dataset that has never been read shows a "not read yet" state, not a
///     row of zeroes. Those are different facts.
///   - Every cap the host applied is surfaced (partial stats, truncated task
///     lists, clamped pages). No silent truncation.

const PAGE_SIZE = 100;

function DigestCard({ summary }: { summary: DatasetSummary }): JSX.Element {
  const t = useT();
  const s = summary;
  return (
    <div className="replay-digest">
      <div className="replay-stats">
        <Stat label={t('replay.stat.episodes')} value={formatCount(s.episodes)} />
        <Stat label={t('replay.stat.frames')} value={formatCount(s.frames)} />
        <Stat label={t('replay.stat.duration')} value={formatDuration(s.durationSec)} />
        <Stat label={t('replay.stat.fps')} value={s.fps > 0 ? String(s.fps) : '—'} />
        <Stat label={t('replay.stat.tasks')} value={formatCount(s.tasksTotal)} />
      </div>

      <div className="replay-facets">
        <div className="replay-facet">
          <div className="replay-facet-head muted small">{t('replay.streams')}</div>
          {s.videoStreams.length === 0 ? (
            <div className="muted small">{t('replay.streams.none')}</div>
          ) : (
            <ul className="replay-chiplist">
              {s.videoStreams.map((v) => (
                <li key={v.key} className="replay-chip" title={v.key}>
                  <span className="mono">{v.name}</span>
                  {formatResolution(v) !== '' && <span className="muted small"> {formatResolution(v)}</span>}
                  {v.codec !== undefined && <span className="muted small"> {v.codec}</span>}
                  {v.isDepth === true && <span className="muted small"> {t('replay.stream.depth')}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="replay-facet">
          <div className="replay-facet-head muted small">{t('replay.features')}</div>
          <ul className="replay-chiplist">
            {s.features
              // Index bookkeeping columns are not what anyone means by "the
              // feature space"; they would crowd out action/state entirely.
              .filter((f) => !['index', 'episode_index', 'frame_index', 'task_index', 'timestamp'].includes(f.key))
              .map((f) => (
                <li key={f.key} className="replay-chip" title={f.names?.join(', ')}>
                  <span className="mono">{f.key}</span>
                  {f.dim !== undefined && <span className="muted small"> {f.dim}</span>}
                </li>
              ))}
          </ul>
        </div>

        {s.histogram.length > 0 && (
          <div className="replay-facet">
            <div className="replay-facet-head muted small">{t('replay.lengths')}</div>
            <div className="replay-histogram" role="img" aria-label={t('replay.lengths')}>
              {s.histogram.map((b, i) => (
                <div
                  key={i}
                  className="replay-bar"
                  style={{ height: `${Math.max(2, Math.round(b.height * 100))}%` }}
                  title={`${b.from}–${b.to}: ${formatCount(b.count)}`}
                />
              ))}
            </div>
          </div>
        )}
      </div>

      {s.tasks.length > 0 && (
        <div className="replay-facet">
          <div className="replay-facet-head muted small">
            {t('replay.tasksList')}
            {s.tasksTruncated && <span className="replay-cap"> {t('replay.cap.tasks')}</span>}
          </div>
          <ul className="replay-chiplist">
            {s.tasks.map((task) => (
              <li key={task} className="replay-chip">
                {task}
              </li>
            ))}
          </ul>
        </div>
      )}

      {(s.statsPartial || s.episodesTruncated || s.warnings.length > 0) && (
        <ul className="replay-notes small">
          {s.statsPartial && <li className="replay-cap">{t('replay.cap.stats')}</li>}
          {s.episodesTruncated && <li className="replay-cap">{t('replay.cap.episodes')}</li>}
          {s.warnings.map((w) => (
            <li key={w} className="muted">
              {w}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }): JSX.Element {
  return (
    <div className="replay-stat">
      <div className="replay-stat-value">{value}</div>
      <div className="replay-stat-label muted small">{label}</div>
    </div>
  );
}

export function ReplaySurface(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];
  const [projectId, setProjectId] = useState('');
  // Selection lives in the store, not here, so an Inspect handoff can set it
  // before this surface mounts (see state/replay.ts).
  const selectedId = useReplay((s) => s.selectedId);
  const setSelectedId = useReplay((s) => s.select);
  const handoff = useReplay((s) => s.handoff);
  const clearHandoff = useReplay((s) => s.clearHandoff);
  const [offset, setOffset] = useState(0);
  /// The episode open in the player, by index. Surface-local: unlike the
  /// dataset selection it is never handed over from another surface, and it
  /// must not outlive the dataset it belongs to.
  const [openEpisode, setOpenEpisode] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [newPath, setNewPath] = useState('');
  const [newHost, setNewHost] = useState('');
  /// True when the form was filled in by a handoff rather than typed. The rail
  /// is easy to miss when the shell has just switched jobs under you, so the
  /// arrival says why the form is open instead of leaving a mystery prefill.
  const [prefilled, setPrefilled] = useState(false);
  const { run: act, busy, error } = useHubAction();
  const hosts = useHosts().data ?? [];

  const effectiveProject = projectId !== '' ? projectId : (str(projects[0] ?? {}, 'id') ?? '');

  const datasetsQ = useQuery({
    queryKey: ['datasets', effectiveProject],
    enabled: client !== null && effectiveProject !== '',
    queryFn: () => client!.listDatasets({ project: effectiveProject }),
  });
  const datasets = datasetsQ.data ?? [];
  const selected: Entity | undefined = datasets.find((d) => str(d, 'id') === selectedId) ?? datasets[0];
  const datasetId = str(selected ?? {}, 'id') ?? '';

  // A new selection starts at the top of the table; leaving the old offset in
  // place would open an unrelated dataset scrolled to page 7 — and the open
  // player would be showing an episode of the dataset you just left.
  useEffect(() => {
    setOffset(0);
    setOpenEpisode(null);
  }, [datasetId]);

  /// Consume an Inspect handoff (W1d). The decision itself lives in
  /// `resolveHandoff` so it can be asserted headlessly; this only dispatches it.
  ///
  /// The wait is the subtle part: a disabled TanStack query never leaves
  /// `isFetched`, so "no hub / no project" has to count as settled — otherwise
  /// the handoff sits unconsumed forever with nothing on screen to explain why.
  /// And acting before the library loads is worse than waiting: an unsettled
  /// query looks exactly like an empty one, so it would offer to register a
  /// dataset that is already there.
  const libraryLoaded = datasetsQ.isFetched || client === null || effectiveProject === '';
  useEffect(() => {
    if (handoff === null || !libraryLoaded) return;
    const next = resolveHandoff(handoff, datasets);
    if (next.action === 'select') {
      setSelectedId(next.datasetId);
      setAdding(false);
      setPrefilled(false);
    } else {
      setNewPath(next.rootPath);
      setAdding(true);
      setPrefilled(true);
    }
    clearHandoff();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [handoff, libraryLoaded, datasets]);

  const summary = readDatasetSummary(selected);

  const episodesQ = useQuery({
    queryKey: ['dataset-episodes', datasetId, offset],
    // Episodes are proxied from the host, so this is never asked for until a
    // dataset has actually been read — an unread dataset has no episodes table
    // to serve and the request would only surface as an error.
    enabled: client !== null && datasetId !== '' && summary.hasDigest,
    queryFn: () => client!.listDatasetEpisodes(datasetId, { offset, limit: PAGE_SIZE }),
  });
  const page = readEpisodePage(episodesQ.data);
  const range = pageRangeLabel(page);
  // The player needs the row, not just its index — length, duration and task
  // are already on screen and asking the host for them again would be a second
  // round-trip for data in hand. An episode scrolled off the current page
  // closes the player rather than showing a stale header.
  const playerEpisode = openEpisode === null ? undefined : page.rows.find((r) => r.index === openEpisode);

  /// Registration is explicit — nothing crawls a host looking for datasets
  /// (the no-surprise-scans posture). W1d moves the common case into the
  /// Inspect tree ("Open in Replay" on a meta/info.json row); this form is what
  /// keeps the surface usable on its own, and stays as the escape hatch for a
  /// root that is not open in a tree.
  async function register(): Promise<void> {
    const path = newPath.trim();
    if (path === '' || effectiveProject === '') return;
    const created = await act(
      () =>
        client!.createDataset({
          project_id: effectiveProject,
          root_path: path,
          ...(newHost !== '' ? { host_id: newHost } : {}),
        }),
      { invalidate: [['datasets', effectiveProject]] },
    );
    if (created !== undefined) {
      const id = str(created, 'id') ?? '';
      if (id !== '') setSelectedId(id);
      setNewPath('');
      setAdding(false);
      setPrefilled(false);
    }
  }

  async function refresh(): Promise<void> {
    if (datasetId === '') return;
    await act(() => client!.refreshDataset(datasetId), {
      invalidate: [['datasets', effectiveProject], ['dataset-episodes', datasetId]],
    });
  }

  return (
    <WorkbenchSurface
      job="replay"
      actions={
        <>
          <select
            className="surface-select"
            value={effectiveProject}
            onChange={(e) => {
              setProjectId(e.target.value);
              setSelectedId('');
            }}
            aria-label={t('replay.project')}
          >
            {projects.map((p) => (
              <option key={str(p, 'id')} value={str(p, 'id')}>
                {str(p, 'name') ?? str(p, 'id')}
              </option>
            ))}
          </select>
          {datasetId !== '' && (
            <button type="button" className="import-btn" onClick={() => void refresh()} disabled={busy}>
              {busy ? t('replay.refreshing') : t('replay.refresh')}
            </button>
          )}
        </>
      }
    >
      {error !== null && <div className="replay-error small">{error}</div>}
      <div className="replay-layout">
        <aside className="replay-rail scroll" aria-label={t('replay.library')}>
          <div className="replay-rail-head muted small">
            {t('replay.library')}
            <span className="spacer" />
            <button
              type="button"
              className="link-btn small"
              onClick={() => {
                setAdding((v) => !v);
                setPrefilled(false);
              }}
            >
              {adding ? t('replay.register.cancel') : t('replay.register')}
            </button>
          </div>
          {adding && (
            <div className="replay-register">
              {prefilled && <div className="replay-handoff small">{t('replay.register.fromInspect')}</div>}
              <input
                className="replay-input mono"
                value={newPath}
                placeholder={t('replay.register.path')}
                aria-label={t('replay.register.path')}
                onChange={(e) => setNewPath(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void register();
                }}
              />
              <select
                className="surface-select"
                value={newHost}
                aria-label={t('replay.register.host')}
                onChange={(e) => setNewHost(e.target.value)}
              >
                <option value="">{t('replay.register.host')}</option>
                {hosts.map((h) => (
                  <option key={str(h, 'id')} value={str(h, 'id')}>
                    {str(h, 'name') ?? str(h, 'id')}
                  </option>
                ))}
              </select>
              <button type="button" className="import-btn" disabled={busy || newPath.trim() === ''} onClick={() => void register()}>
                {t('replay.register.add')}
              </button>
              <div className="muted small">{t('replay.register.hint')}</div>
            </div>
          )}
          {datasets.length === 0 ? (
            <div className="muted small region-pad">{t('replay.empty')}</div>
          ) : (
            <ul className="replay-list">
              {datasets.map((d) => {
                const id = str(d, 'id') ?? '';
                const ds = readDatasetSummary(d);
                return (
                  <li key={id}>
                    <button
                      type="button"
                      className={`replay-list-row${id === datasetId ? ' is-selected' : ''}`}
                      onClick={() => setSelectedId(id)}
                    >
                      <span className="replay-list-name">{str(d, 'name') ?? id}</span>
                      <span className="replay-list-meta muted small">
                        {ds.hasDigest
                          ? `${formatCount(ds.episodes)} ${t('replay.episodesShort')}`
                          : t('replay.unread')}
                      </span>
                      <span className="replay-list-path muted small mono">{str(d, 'root_path')}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </aside>

        <div className="replay-main scroll">
          {selected === undefined ? (
            <div className="muted region-pad">{t('replay.pick')}</div>
          ) : !summary.hasDigest ? (
            // Never read is NOT the same as empty. Saying so, and offering the
            // one action that changes it, beats rendering a row of zeroes that
            // looks like a finished answer.
            <div className="replay-unread region-pad">
              <div>{t('replay.unread.title')}</div>
              <div className="muted small">{t('replay.unread.hint')}</div>
            </div>
          ) : (
            <>
              <div className="replay-head">
                <div className="replay-title">{str(selected, 'name') ?? datasetId}</div>
                <div className="replay-sub muted small mono">{str(selected, 'root_path')}</div>
                <div className="replay-sub muted small">
                  {summary.format}
                  {summary.robotType !== '' && summary.robotType !== 'unknown' && ` · ${summary.robotType}`}
                  {/* The plan's honesty pattern, borrowed from RunReportCard: say
                      when the fold happened rather than implying it is live. */}
                  {summary.digestTS !== '' && ` · ${t('replay.asOf')} ${summary.digestTS}`}
                </div>
              </div>

              <DigestCard summary={summary} />

              {/* The player sits above the table rather than replacing it: an
                  episode is chosen by comparing it against its neighbours, and
                  swapping the table out loses the row you were reading. */}
              {playerEpisode !== undefined && (
                <EpisodePlayer
                  datasetId={datasetId}
                  rootPath={str(selected, 'root_path') ?? ''}
                  episode={playerEpisode}
                  summary={summary}
                  onClose={() => setOpenEpisode(null)}
                />
              )}

              <div className="replay-table-wrap">
                <table className="replay-table">
                  <thead>
                    <tr>
                      <th>{t('replay.col.index')}</th>
                      <th>{t('replay.col.length')}</th>
                      <th>{t('replay.col.duration')}</th>
                      <th>{t('replay.col.task')}</th>
                      <th>{t('replay.col.rows')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {page.rows.map((row) => (
                      // `role="button"` + `clickable-row` is what the other
                      // clickable tables here use (ProjectBoard, AdminCockpit);
                      // a new one inventing its own affordance would look and
                      // behave subtly differently for no reason.
                      <tr
                        key={row.index}
                        role="button"
                        className={`clickable-row replay-row${row.index === openEpisode ? ' is-open' : ''}`}
                        onClick={() => setOpenEpisode(row.index === openEpisode ? null : row.index)}
                      >
                        <td className="mono">{row.index}</td>
                        <td className="mono">{formatCount(row.length)}</td>
                        <td className="mono">{formatDuration(row.durationSec)}</td>
                        <td>{row.tasks.join(', ')}</td>
                        <td className="mono muted small">
                          {row.fromIndex !== undefined && row.toIndex !== undefined
                            ? `${formatCount(row.fromIndex)}–${formatCount(row.toIndex)}`
                            : '—'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {episodesQ.isPending && <div className="muted small region-pad">{t('replay.loading')}</div>}
                {!episodesQ.isPending && page.rows.length === 0 && (
                  <div className="muted small region-pad">{t('replay.noEpisodes')}</div>
                )}
              </div>

              <div className="replay-pager small">
                <button
                  type="button"
                  className="import-btn"
                  disabled={offset === 0}
                  onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                >
                  {t('replay.prev')}
                </button>
                <span className="muted">
                  {range !== null
                    ? t('replay.range')
                        .replace('{from}', formatCount(range.from))
                        .replace('{to}', formatCount(range.to))
                        .replace('{total}', formatCount(range.total))
                    : ''}
                </span>
                <button
                  type="button"
                  className="import-btn"
                  disabled={range === null || range.to >= range.total}
                  onClick={() => setOffset(offset + PAGE_SIZE)}
                >
                  {t('replay.next')}
                </button>
                {page.truncated && <span className="replay-cap">{t('replay.cap.page')}</span>}
              </div>
            </>
          )}
        </div>
      </div>
    </WorkbenchSurface>
  );
}
