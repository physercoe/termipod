import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { bool, num, obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';

// ---- Acceptance Criteria (parity — AcceptanceCriteriaScreen) ----------------

const CRIT_STATES = ['all', 'pending', 'met', 'failed', 'waived'];

function critText(c: Entity): string {
  const body = obj(c, 'body');
  if (body !== undefined) {
    const d = str(body, 'description') ?? str(body, 'summary') ?? str(body, 'statement') ?? str(body, 'text');
    if (d !== undefined) return d;
    // metric-kind: operator/threshold
    const metric = str(body, 'metric');
    if (metric !== undefined) return `${metric} ${str(body, 'operator') ?? ''} ${str(body, 'threshold') ?? ''}`.trim();
  }
  return str(c, 'body') ?? str(c, 'description') ?? str(c, 'id') ?? '—';
}

function critStateClass(state: string): string {
  switch (state) {
    case 'met':
      return 'sev-medium';
    case 'failed':
      return 'sev-high';
    case 'waived':
      return 'muted';
    default:
      return '';
  }
}

export function CriteriaTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy } = useHubAction();
  const [filter, setFilter] = useState('all');
  const q = useQuery({
    queryKey: ['criteria', projectId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listCriteria(projectId),
  });

  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;

  const all = q.data ?? [];
  const criteria = filter === 'all' ? all : all.filter((c) => (str(c, 'state') ?? 'pending') === filter);

  // Group by phase, sort pending → failed → met → waived then ord.
  const rank: Record<string, number> = { pending: 0, failed: 1, met: 2, waived: 3 };
  const byPhase = new Map<string, Entity[]>();
  for (const c of criteria) {
    const phase = str(c, 'phase') ?? '—';
    const list = byPhase.get(phase);
    if (list) list.push(c);
    else byPhase.set(phase, [c]);
  }
  for (const list of byPhase.values()) {
    list.sort(
      (a, b) =>
        (rank[str(a, 'state') ?? 'pending'] ?? 0) - (rank[str(b, 'state') ?? 'pending'] ?? 0) ||
        (num(a, 'ord') ?? 0) - (num(b, 'ord') ?? 0),
    );
  }

  function act(cid: string, action: 'mark-met' | 'mark-failed' | 'waive'): void {
    void run(() => client!.criterionAction(projectId, cid, action), { invalidate: [['criteria', projectId]] });
  }

  return (
    <div className="region-pad scroll">
      <div className="kanban-bar">
        {CRIT_STATES.map((s) => (
          <button key={s} className={filter === s ? 'chip active' : 'chip'} onClick={() => setFilter(s)}>
            {t(`crit.${s}`)}
          </button>
        ))}
      </div>
      {all.length === 0 && <div className="muted">{t('crit.none')}</div>}
      {[...byPhase.keys()].sort().map((phase) => (
        <section key={phase} className="setting-group">
          <h3>{phase}</h3>
          {(byPhase.get(phase) ?? []).map((c, i) => {
            const cid = str(c, 'id') ?? '';
            const state = str(c, 'state') ?? 'pending';
            const isGate = str(c, 'kind') === 'gate';
            const deliv = str(c, 'deliverable_id');
            return (
              <div key={cid || String(i)} className="crit-row">
                <span className={`sev ${critStateClass(state)}`}>{state}</span>
                <div className="crit-main">
                  <div>{critText(c)}</div>
                  <div className="muted small">
                    {str(c, 'kind') ?? 'text'}
                    {deliv !== undefined && deliv !== '' ? ` · ${t('crit.underDeliverable')}` : ` · ${t('crit.projectLevel')}`}
                    {bool(c, 'required') === true ? ` · ${t('crit.required')}` : ''}
                  </div>
                </div>
                {!isGate && state !== 'met' && (
                  <div className="crit-actions">
                    <button disabled={busy} onClick={() => act(cid, 'mark-met')}>
                      {t('crit.markMet')}
                    </button>
                    <button disabled={busy} onClick={() => act(cid, 'mark-failed')}>
                      {t('crit.markFailed')}
                    </button>
                    <button disabled={busy} onClick={() => act(cid, 'waive')}>
                      {t('crit.waive')}
                    </button>
                  </div>
                )}
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
}

// ---- Deliverable detail (parity — StructuredDeliverableViewer) --------------

export function DeliverableDetail({
  projectId,
  deliverableId,
  onClose,
}: {
  projectId: string;
  deliverableId: string;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const delivQ = useQuery({
    queryKey: ['deliverable', projectId, deliverableId],
    enabled: client !== null,
    queryFn: () => client!.getDeliverable(projectId, deliverableId),
  });
  const critQ = useQuery({
    queryKey: ['criteria', projectId, deliverableId],
    enabled: client !== null,
    queryFn: () => client!.listCriteria(projectId, { deliverable_id: deliverableId }),
  });

  const d = delivQ.data ?? {};
  const state = str(d, 'ratification_state') ?? '—';
  const ratified = state === 'ratified';
  const components = Array.isArray(d['components']) ? (d['components'] as Entity[]) : [];
  const criteria = critQ.data ?? [];

  const invalidate = [
    ['deliverable', projectId, deliverableId],
    ['project-overview', projectId],
  ];

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="task-detail" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{str(d, 'kind') ?? t('deliv.title')}</strong>
          <span className={`sev ${critStateClass(state === 'ratified' ? 'met' : 'pending')}`}>{state}</span>
          <span className="spacer" />
          {ratified ? (
            <button disabled={busy} onClick={() => void run(() => client!.unratifyDeliverable(projectId, deliverableId), { invalidate })}>
              {t('deliv.unratify')}
            </button>
          ) : (
            <button className="primary" disabled={busy} onClick={() => void run(() => client!.ratifyDeliverable(projectId, deliverableId), { invalidate })}>
              {t('deliv.ratify')}
            </button>
          )}
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {delivQ.isLoading && <div className="muted">{t('common.loading')}</div>}
          {error !== null && <div className="error">{error}</div>}
          <div className="muted small">
            {t('deliv.phase')}: {str(d, 'phase') ?? '—'}
            {str(d, 'ratified_at') !== undefined ? ` · ${t('deliv.ratifiedAt')} ${str(d, 'ratified_at')}` : ''}
          </div>

          <section className="setting-group">
            <h3>{t('deliv.components')}</h3>
            {components.length === 0 ? (
              <div className="muted">{t('deliv.noComponents')}</div>
            ) : (
              components.map((c, i) => (
                <div key={str(c, 'ref_id') ?? String(i)} className="admin-row">
                  <span className="pill">{str(c, 'kind') ?? '—'}</span>
                  <span className="mono small">{str(c, 'ref_id') ?? ''}</span>
                  <span className="spacer" />
                  {bool(c, 'required') === true && <span className="muted small">{t('crit.required')}</span>}
                </div>
              ))
            )}
          </section>

          <section className="setting-group">
            <h3>{t('proj.criteria')}</h3>
            {criteria.length === 0 ? (
              <div className="muted">{t('crit.none')}</div>
            ) : (
              criteria.map((c, i) => (
                <div key={str(c, 'id') ?? String(i)} className="crit-row">
                  <span className={`sev ${critStateClass(str(c, 'state') ?? 'pending')}`}>{str(c, 'state') ?? 'pending'}</span>
                  <div className="crit-main">
                    <div>{critText(c)}</div>
                  </div>
                </div>
              ))
            )}
          </section>
        </div>
      </div>
    </div>
  );
}

// ---- Files: docs_root tree + project artifacts (parity — DocsSection) --------

export function FilesTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [openDoc, setOpenDoc] = useState<string | null>(null);

  const docsQ = useQuery({
    queryKey: ['project-docs', projectId],
    enabled: client !== null,
    queryFn: () => client!.listProjectDocs(projectId),
  });
  const artifactsQ = useQuery({
    queryKey: ['project-artifacts', projectId],
    enabled: client !== null,
    queryFn: () => client!.listArtifacts({ project: projectId }),
  });
  const contentQ = useQuery({
    queryKey: ['project-doc', projectId, openDoc],
    enabled: client !== null && openDoc !== null,
    queryFn: () => client!.getProjectDocText(projectId, openDoc as string),
  });

  const docs = (docsQ.data ?? []).filter((d) => bool(d, 'is_dir') !== true);
  const artifacts = artifactsQ.data ?? [];

  return (
    <div className="region-pad scroll">
      <p className="muted small">{t('files.guidance')}</p>
      <section className="setting-group">
        <h3>{t('files.docsRoot')}</h3>
        {docsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
        {docsQ.isError && <div className="muted">{t('files.noDocsRoot')}</div>}
        {!docsQ.isLoading && docs.length === 0 && <div className="muted">{t('files.noDocsRoot')}</div>}
        {docs.map((d, i) => {
          const path = str(d, 'path') ?? '';
          const depth = path.split('/').length - 1;
          return (
            <button
              key={path || String(i)}
              className="file-row"
              style={{ paddingLeft: `${8 + depth * 14}px` }}
              onClick={() => setOpenDoc(path)}
            >
              <span className="file-name">{path.split('/').pop()}</span>
              <span className="spacer" />
              <span className="muted small">{num(d, 'size') !== undefined ? `${num(d, 'size')} B` : ''}</span>
            </button>
          );
        })}
      </section>

      <section className="setting-group">
        <h3>{t('files.artifacts')}</h3>
        {artifactsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
        {!artifactsQ.isLoading && artifacts.length === 0 && <div className="muted">{t('files.noArtifacts')}</div>}
        {artifacts.map((a, i) => (
          <div key={str(a, 'id') ?? String(i)} className="admin-row">
            <span>{str(a, 'name') ?? str(a, 'kind') ?? str(a, 'id')}</span>
            <span className="spacer" />
            <span className="muted small mono">
              {str(a, 'kind') ?? ''}
              {num(a, 'size') !== undefined ? ` · ${num(a, 'size')} B` : ''}
            </span>
          </div>
        ))}
      </section>

      {openDoc !== null && (
        <div className="palette-backdrop" onMouseDown={() => setOpenDoc(null)}>
          <div className="task-detail" onMouseDown={(e) => e.stopPropagation()}>
            <div className="admin-tabs">
              <strong className="mono">{openDoc}</strong>
              <span className="spacer" />
              <button onClick={() => setOpenDoc(null)}>{t('admin.close')}</button>
            </div>
            <div className="region-pad scroll doc-body">
              {contentQ.isLoading && <div className="muted">{t('common.loading')}</div>}
              {contentQ.isError && <div className="error">{(contentQ.error as Error).message}</div>}
              {contentQ.data !== undefined &&
                (openDoc.endsWith('.md') ? (
                  <Markdown text={contentQ.data} />
                ) : (
                  <pre className="ev-mono">{contentQ.data}</pre>
                ))}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ---- Activity: audit feed scoped to the project (parity — ActivityFeed) ------

export function ActivityTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const q = useQuery({
    queryKey: ['project-activity', projectId],
    enabled: client !== null,
    refetchInterval: 12000,
    queryFn: () => client!.listAudit({ project_id: projectId, limit: 100 }),
  });
  if (q.isLoading) return <div className="region-pad muted">{t('proj.loading')}</div>;
  if (q.isError) return <div className="region-pad error">{(q.error as Error).message}</div>;
  const rows = q.data ?? [];
  if (rows.length === 0) return <div className="region-pad muted">{t('activity.none')}</div>;
  return (
    <div className="region-pad scroll">
      {rows.map((r, i) => (
        <div key={str(r, 'id') ?? String(i)} className="activity-row">
          <span className="pill">{str(r, 'action') ?? str(r, 'kind') ?? 'event'}</span>
          <span className="activity-actor mono small">{str(r, 'actor') ?? str(r, 'actor_handle') ?? ''}</span>
          <span className="spacer" />
          <span className="muted small">{str(r, 'ts') ?? str(r, 'created_at') ?? ''}</span>
        </div>
      ))}
    </div>
  );
}
