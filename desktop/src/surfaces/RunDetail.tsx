import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Modal } from '../ui/Modal';
import { useHubAction } from '../hub/action';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { parsePoints, Sparkline } from '../ui/Sparkline';
import { useConfirm } from '../ui/ConfirmModal';
import { useReplay } from '../state/replay';
import { useWorkbench } from '../state/workbench';
import { formatCount, formatDuration, readDatasetSummary, readEpisodePage } from '../state/replayDigest';

/// Run detail (parity — mobile _RunDetailScreen ViewSwitcher: Overview / Charts /
/// Media / Outputs / Config). Charts renders scalar `/metrics` + `/system_metrics`
/// as inline sparklines; Media renders logged `/images` (blob bytes → data URL)
/// and `/histograms`; Outputs lists run artifacts; Config shows hyperparameters.
/// The header also exposes the run's editable status (`PATCH …/runs/{id}` — the
/// hub supports run edit even though mobile has no run-edit UI).
const RUN_STATUSES = ['pending', 'running', 'completed', 'failed', 'cancelled'];

function fmtConfig(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'string') return v;
  try {
    return JSON.stringify(v, null, 2) ?? String(v);
  } catch {
    return String(v);
  }
}

type View = 'overview' | 'charts' | 'media' | 'episodes' | 'outputs' | 'config';

/// The run ↔ dataset edge, and the episodes it makes watchable (J8 W5).
///
/// Charts say what the numbers did; this says what the robot did. A run that
/// names a dataset in its config can be linked in one click — the hub sniffs
/// the field, this only confirms it — and once linked, every episode is one
/// step from the J8 player.
///
/// Linking is never automatic. A config key that merely looks like a dataset is
/// a guess, and a wrong edge sends someone to watch the wrong robot and believe
/// what they see.
function RunEpisodes({
  runId,
  run,
  onOpened,
}: {
  runId: string;
  run: Entity;
  onOpened: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run: act, busy } = useHubAction();
  const [offset, setOffset] = useState(0);

  const datasetId = str(run, 'dataset_id') ?? '';
  const projectId = str(run, 'project_id') ?? '';

  const hintQ = useQuery({
    queryKey: ['run-dataset-hint', runId],
    // Only worth asking when nothing is linked: the hint exists to propose one.
    enabled: client !== null && datasetId === '',
    queryFn: () => client!.getRunDatasetHint(runId),
  });
  const datasetQ = useQuery({
    queryKey: ['dataset', datasetId],
    enabled: client !== null && datasetId !== '',
    queryFn: () => client!.getDataset(datasetId),
  });
  const summary = readDatasetSummary(datasetQ.data);
  const episodesQ = useQuery({
    queryKey: ['dataset-episodes', datasetId, offset],
    // Episodes are proxied from the host and only exist once the dataset has
    // been read; asking earlier surfaces as an error rather than as "unread".
    enabled: client !== null && datasetId !== '' && summary.hasDigest,
    queryFn: () => client!.listDatasetEpisodes(datasetId, { offset, limit: RUN_EPISODE_PAGE }),
  });
  const page = readEpisodePage(episodesQ.data);

  function link(id: string): void {
    void act(() => client!.updateRun(runId, { dataset_id: id }), {
      invalidate: [['run', runId], ['run-dataset-hint', runId], ['runs']],
    });
  }

  /// Hand the episode to J8 Replay and switch jobs. The project travels with
  /// it: Replay defaults to the first project in the list, so without it the
  /// surface would load a different library and never find this dataset.
  function watch(episode?: number): void {
    useReplay.getState().openRegistered({ datasetId, projectId, episode });
    useWorkbench.getState().setJob('replay');
    onOpened();
  }

  if (datasetId === '') {
    const hint = hintQ.data?.['hint'] as Entity | null | undefined;
    const proposed = hint === null || hint === undefined ? null : (str(hint, 'value') ?? '');
    const registered = str(hintQ.data ?? {}, 'dataset_id') ?? '';
    return (
      <section className="setting-group">
        {hintQ.isLoading && <div className="muted">{t('common.loading')}</div>}
        {!hintQ.isLoading && proposed === null && <div className="muted">{t('run.dataset.noHint')}</div>}
        {proposed !== null && (
          <>
            <div className="admin-row">
              <span className="muted">{t('run.dataset.proposed')}</span>
              <span className="spacer" />
              <span className="mono small">{proposed}</span>
            </div>
            <div className="muted small">
              {t('run.dataset.fromKey').replace('{key}', str(hint ?? {}, 'key') ?? '')}
            </div>
            {registered !== '' ? (
              <div className="admin-row">
                <span className="muted small">{t('run.dataset.isRegistered')}</span>
                <span className="spacer" />
                <button disabled={busy} onClick={() => link(registered)}>
                  {t('run.dataset.link')}
                </button>
              </div>
            ) : (
              // No row to link to. Saying which of the two states this is beats
              // a disabled button that gives no reason.
              <div className="muted small">{t('run.dataset.notRegistered')}</div>
            )}
          </>
        )}
      </section>
    );
  }

  return (
    <section className="setting-group">
      <div className="admin-row">
        <span className="muted">{t('run.dataset')}</span>
        <span className="spacer" />
        <span className="mono small">{str(datasetQ.data ?? {}, 'name') ?? datasetId}</span>
        <button disabled={busy} onClick={() => watch()}>
          {t('run.dataset.openReplay')} →
        </button>
        <button disabled={busy} onClick={() => link('')}>
          {t('run.dataset.unlink')}
        </button>
      </div>

      {datasetQ.isLoading && <div className="muted">{t('common.loading')}</div>}
      {datasetQ.data !== undefined && !summary.hasDigest && (
        <div className="muted small">{t('run.dataset.unread')}</div>
      )}

      {summary.hasDigest && (
        <>
          {episodesQ.isLoading && <div className="muted">{t('common.loading')}</div>}
          {page.rows.map((row) => (
            <div key={row.index} className="admin-row">
              <span className="mono small">{row.index}</span>
              <span className="muted small">
                {formatCount(row.length)} · {formatDuration(row.durationSec)}
              </span>
              <span className="spacer" />
              {row.tasks.length > 0 && <span className="muted small">{row.tasks[0]}</span>}
              <button onClick={() => watch(row.index)}>{t('run.episodes.watch')}</button>
            </div>
          ))}
          {!episodesQ.isLoading && page.rows.length === 0 && <div className="muted">{t('run.episodes.none')}</div>}
          {page.total > RUN_EPISODE_PAGE && (
            <div className="admin-row">
              <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - RUN_EPISODE_PAGE))}>
                {t('replay.prev')}
              </button>
              <span className="muted small">
                {offset + 1}–{offset + page.rows.length} / {page.total}
              </span>
              <span className="spacer" />
              <button
                disabled={offset + page.rows.length >= page.total}
                onClick={() => setOffset(offset + RUN_EPISODE_PAGE)}
              >
                {t('replay.next')}
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

/// Smaller than the Replay surface's page: this list lives in a modal beside
/// four other views, not on a surface of its own.
const RUN_EPISODE_PAGE = 25;

/// One logged image — fetches blob bytes lazily (only when this tile mounts) and
/// renders them as a data URL. Mirrors mobile's _ImageSeriesTile lazy download.
function RunImageTile({ image }: { image: Entity }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const sha = str(image, 'blob_sha') ?? '';
  const imgQ = useQuery({
    queryKey: ['blob', sha],
    enabled: client !== null && sha !== '',
    staleTime: 5 * 60 * 1000,
    queryFn: () => client!.getBlobDataUrl(sha),
  });
  return (
    <div className="run-image-tile">
      <div className="muted small">
        {str(image, 'metric_name') ?? ''}
        {num(image, 'step') !== undefined ? ` · step ${num(image, 'step')}` : ''}
      </div>
      {imgQ.isLoading && <div className="muted small">{t('common.loading')}</div>}
      {imgQ.isError && <div className="muted small">{t('run.blobFailed')}</div>}
      {imgQ.data !== undefined && <img className="run-image" src={imgQ.data} alt={str(image, 'caption') ?? sha} />}
      {str(image, 'caption') !== undefined && str(image, 'caption') !== '' && (
        <div className="muted small">{str(image, 'caption')}</div>
      )}
    </div>
  );
}

export function RunDetail({ runId, onClose }: { runId: string; onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const selectAgent = useFocus((s) => s.selectAgent);
  const { run: act, busy } = useHubAction();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const [view, setView] = useState<View>('overview');

  const runQ = useQuery({
    queryKey: ['run', runId],
    enabled: client !== null,
    refetchInterval: 20000,
    queryFn: () => client!.getRun(runId),
  });
  const configQ = useQuery({
    queryKey: ['run-config', runId],
    enabled: client !== null && view === 'config',
    queryFn: () => client!.getRunConfig(runId),
  });
  const artifactsQ = useQuery({
    queryKey: ['run-artifacts', runId],
    enabled: client !== null && view === 'outputs',
    queryFn: () => client!.listArtifacts({ run: runId }),
  });
  const metricsQ = useQuery({
    queryKey: ['run-metrics', runId],
    enabled: client !== null && view === 'charts',
    refetchInterval: view === 'charts' ? 15000 : false,
    queryFn: () => client!.getRunMetrics(runId),
  });
  const sysMetricsQ = useQuery({
    queryKey: ['run-sysmetrics', runId],
    enabled: client !== null && view === 'charts',
    queryFn: () => client!.getRunSystemMetrics(runId),
  });
  const imagesQ = useQuery({
    queryKey: ['run-images', runId],
    enabled: client !== null && view === 'media',
    queryFn: () => client!.getRunImages(runId),
  });
  const histQ = useQuery({
    queryKey: ['run-histograms', runId],
    enabled: client !== null && view === 'media',
    queryFn: () => client!.getRunHistograms(runId),
  });

  const run = runQ.data ?? {};
  const status = str(run, 'status') ?? '—';
  const agentId = str(run, 'agent_id');
  const artifacts = artifactsQ.data ?? [];
  const metrics = metricsQ.data ?? [];
  const sysMetrics = sysMetricsQ.data ?? [];
  const images = imagesQ.data ?? [];
  const histograms = histQ.data ?? [];

  const views: { v: View; label: string }[] = [
    { v: 'overview', label: t('run.overview') },
    { v: 'charts', label: t('run.charts') },
    { v: 'media', label: t('run.media') },
    { v: 'episodes', label: t('run.episodes') },
    { v: 'outputs', label: t('run.outputs') },
    { v: 'config', label: t('run.config') },
  ];

  async function setStatus(next: string): Promise<void> {
    // Cancelling is a destructive terminal transition — one mis-select in the
    // status dropdown would otherwise cancel a live run with no undo.
    if (next === 'cancelled' && !(await confirmAsk({ message: t('run.confirmCancel'), danger: true }))) return;
    void act(() => client!.updateRun(runId, { status: next }), { invalidate: [['run', runId], ['runs']] });
  }

  function DetailRow({ label, value }: { label: string; value: string | undefined }): JSX.Element | null {
    if (value === undefined || value === '') return null;
    return (
      <div className="admin-row">
        <span className="muted">{label}</span>
        <span className="spacer" />
        <span className="mono small">{value}</span>
      </div>
    );
  }

  function MetricTile({ row, ordinal }: { row: Entity; ordinal?: boolean }): JSX.Element {
    const pts = parsePoints(row['points']);
    return (
      <div className="metric-tile">
        <div className="metric-head">
          <span className="metric-name">{str(row, 'name') ?? '—'}</span>
          <span className="spacer" />
          {num(row, 'last_value') !== undefined && <span className="mono small">{num(row, 'last_value')}</span>}
        </div>
        <Sparkline points={pts} sampleOrdinalX={ordinal === true} />
        <div className="muted small">
          {num(row, 'sample_count') ?? pts.length} {t('run.samples')}
        </div>
      </div>
    );
  }

  return (
    <>
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('run.title')}>
        <div className="admin-tabs">
          <strong>{t('run.title')}</strong>
          <label className="inline-select">
            <select value={status} disabled={busy} onChange={(e) => void setStatus(e.target.value)}>
              {RUN_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <div className="tabs">
            {views.map((x) => (
              <button key={x.v} className={view === x.v ? 'tab active' : 'tab'} onClick={() => setView(x.v)}>
                {x.label}
              </button>
            ))}
          </div>
          <span className="spacer" />
          {agentId !== undefined && (
            <button
              onClick={() => {
                selectAgent('projects', agentId);
                onClose();
              }}
            >
              {t('run.openAgent')} →
            </button>
          )}
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {runQ.isLoading && <div className="muted">{t('common.loading')}</div>}
          {runQ.isError && <div className="error">{(runQ.error as Error).message}</div>}

          {view === 'overview' && runQ.data !== undefined && (
            <section className="setting-group">
              <DetailRow label={t('run.id')} value={str(run, 'id')} />
              <DetailRow label={t('run.project')} value={str(run, 'project_id')} />
              <DetailRow label={t('run.agent')} value={agentId} />
              <DetailRow label={t('run.seed')} value={num(run, 'seed') !== undefined ? String(num(run, 'seed')) : undefined} />
              <DetailRow label={t('run.parent')} value={str(run, 'parent_run_id')} />
              <DetailRow label={t('run.started')} value={str(run, 'started_at')} />
              <DetailRow label={t('run.finished')} value={str(run, 'finished_at')} />
              <DetailRow label={t('run.trackio')} value={str(run, 'trackio_run_uri')} />
              {str(run, 'config_json') !== undefined && (
                <>
                  <h3>{t('run.config')}</h3>
                  <pre className="ev-mono">{fmtConfig(run['config_json'])}</pre>
                </>
              )}
            </section>
          )}

          {view === 'charts' && (
            <section className="setting-group">
              {(metricsQ.isLoading || sysMetricsQ.isLoading) && <div className="muted">{t('common.loading')}</div>}
              {!metricsQ.isLoading && metrics.length === 0 && sysMetrics.length === 0 && (
                <div className="muted">{t('run.noMetrics')}</div>
              )}
              <div className="metric-grid">
                {metrics.map((m, i) => (
                  <MetricTile key={str(m, 'name') ?? String(i)} row={m} />
                ))}
              </div>
              {sysMetrics.length > 0 && (
                <>
                  <h3>{t('run.systemMetrics')}</h3>
                  <div className="metric-grid">
                    {sysMetrics.map((m, i) => (
                      <MetricTile key={str(m, 'name') ?? String(i)} row={m} ordinal />
                    ))}
                  </div>
                </>
              )}
            </section>
          )}

          {view === 'media' && (
            <section className="setting-group">
              {(imagesQ.isLoading || histQ.isLoading) && <div className="muted">{t('common.loading')}</div>}
              {!imagesQ.isLoading && images.length === 0 && histograms.length === 0 && (
                <div className="muted">{t('run.noMedia')}</div>
              )}
              <div className="run-image-grid">
                {images.map((im, i) => (
                  <RunImageTile key={str(im, 'id') ?? String(i)} image={im} />
                ))}
              </div>
              {histograms.length > 0 && (
                <>
                  <h3>{t('run.histograms')}</h3>
                  {histograms.map((h, i) => (
                    <div key={str(h, 'name') ?? String(i)} className="admin-row">
                      <span>{str(h, 'name') ?? '—'}</span>
                      <span className="spacer" />
                      <span className="muted small">step {num(h, 'step') ?? 0}</span>
                    </div>
                  ))}
                </>
              )}
            </section>
          )}

          {view === 'episodes' && runQ.data !== undefined && (
            <RunEpisodes runId={runId} run={run} onOpened={onClose} />
          )}

          {view === 'outputs' && (
            <section className="setting-group">
              {artifactsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
              {artifacts.length === 0 && !artifactsQ.isLoading && <div className="muted">{t('run.noOutputs')}</div>}
              {artifacts.map((a, i) => (
                <div key={str(a, 'id') ?? String(i)} className="admin-row">
                  <span>{str(a, 'name') ?? str(a, 'kind') ?? str(a, 'id')}</span>
                  <span className="spacer" />
                  <span className="muted small mono">
                    {str(a, 'mime') ?? ''}
                    {num(a, 'size') !== undefined ? ` · ${num(a, 'size')} B` : ''}
                  </span>
                </div>
              ))}
            </section>
          )}

          {view === 'config' && (
            <section className="setting-group">
              {configQ.isLoading && <div className="muted">{t('common.loading')}</div>}
              {configQ.isError && <div className="muted">{t('run.noConfig')}</div>}
              {configQ.data !== undefined && (
                <pre className="ev-mono">{fmtConfig((configQ.data as Entity)['config'] ?? configQ.data)}</pre>
              )}
            </section>
          )}
        </div>
    </Modal>
    {confirmNode}
    </>
  );
}
