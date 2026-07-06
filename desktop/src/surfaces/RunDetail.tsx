import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';

/// Run detail (parity — mobile RunDetailScreen). Mobile's ViewSwitcher has
/// Overview/Charts/Media/Outputs/Config; the desktop client has no ML
/// charts/media infra, so we ship the applicable views: Overview (status +
/// identity + open-agent), Config (hyperparameters), Outputs (run artifacts).
/// Backed by GET …/runs/{id} (+ /config) and GET …/artifacts?run=.
function fmtConfig(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'string') return v;
  try {
    return JSON.stringify(v, null, 2) ?? String(v);
  } catch {
    return String(v);
  }
}

type View = 'overview' | 'config' | 'outputs';

export function RunDetail({ runId, onClose }: { runId: string; onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const selectAgent = useFocus((s) => s.selectAgent);
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

  const run = runQ.data ?? {};
  const agentId = str(run, 'agent_id');
  const artifacts = artifactsQ.data ?? [];

  const views: { v: View; label: string }[] = [
    { v: 'overview', label: t('run.overview') },
    { v: 'config', label: t('run.config') },
    { v: 'outputs', label: t('run.outputs') },
  ];

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

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="sessions-panel" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('run.title')}</strong>
          <span className={`sev${str(run, 'status') === 'succeeded' ? ' sev-medium' : ''}`}>{str(run, 'status') ?? '—'}</span>
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
                selectAgent(agentId);
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

          {view === 'config' && (
            <section className="setting-group">
              {configQ.isLoading && <div className="muted">{t('common.loading')}</div>}
              {configQ.isError && <div className="muted">{t('run.noConfig')}</div>}
              {configQ.data !== undefined && (
                <pre className="ev-mono">{fmtConfig((configQ.data as Entity)['config'] ?? configQ.data)}</pre>
              )}
            </section>
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
        </div>
      </div>
    </div>
  );
}
