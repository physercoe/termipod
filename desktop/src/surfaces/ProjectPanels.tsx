import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { bool, num, obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';
import { Modal } from '../ui/Modal';
import { ArtifactViewer } from './ArtifactViewer';

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

/// Compose a new acceptance criterion (parity — the mobile add-criterion sheet).
/// `phase` + `kind` are required; text criteria carry a `{description}` body,
/// metric criteria a `{metric, operator, threshold}` body.
function NewCriterionForm({ projectId, phases, onDone }: { projectId: string; phases: string[]; onDone: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const [phase, setPhase] = useState(phases[0] ?? '');
  const [kind, setKind] = useState<'text' | 'metric' | 'gate'>('text');
  const [description, setDescription] = useState('');
  const [metric, setMetric] = useState('');
  const [operator, setOperator] = useState('>=');
  const [threshold, setThreshold] = useState('');
  const [required, setRequired] = useState(true);

  const ready = phase.trim() !== '' && (kind === 'metric' ? metric.trim() !== '' : description.trim() !== '');

  async function submit(): Promise<void> {
    if (client === null || !ready) return;
    const body: Record<string, unknown> =
      kind === 'metric'
        ? { metric: metric.trim(), operator, threshold: threshold.trim() }
        : { description: description.trim() };
    const created = await run(
      () => client.createCriterion(projectId, { phase: phase.trim(), kind, body, required }),
      { invalidate: [['criteria', projectId]] },
    );
    if (created !== undefined) onDone();
  }

  return (
    <Modal onClose={onDone} className="task-detail" ariaLabel={t('crit.new')}>
        <div className="admin-tabs">
          <strong>{t('crit.new')}</strong>
          <span className="spacer" />
          <button onClick={onDone}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <label>
            {t('crit.phase')}
            {phases.length > 0 ? (
              <select value={phase} onChange={(e) => setPhase(e.target.value)}>
                {phases.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            ) : (
              <input value={phase} onChange={(e) => setPhase(e.target.value)} placeholder={t('crit.phasePlaceholder')} />
            )}
          </label>
          <label>
            {t('crit.kind')}
            <div className="seg">
              {(['text', 'metric', 'gate'] as const).map((k) => (
                <button key={k} className={kind === k ? 'seg-btn active' : 'seg-btn'} onClick={() => setKind(k)}>
                  {k}
                </button>
              ))}
            </div>
          </label>
          {kind === 'metric' ? (
            <div className="crit-metric-row wide">
              <input value={metric} onChange={(e) => setMetric(e.target.value)} placeholder={t('crit.metricName')} />
              <select value={operator} onChange={(e) => setOperator(e.target.value)}>
                {['>=', '>', '<=', '<', '=='].map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
              <input value={threshold} onChange={(e) => setThreshold(e.target.value)} placeholder={t('crit.threshold')} />
            </div>
          ) : (
            <label className="wide">
              {t('crit.statement')}
              <textarea value={description} onChange={(e) => setDescription(e.target.value)} autoFocus />
            </label>
          )}
          <label className="checkbox-row">
            <input type="checkbox" checked={required} onChange={(e) => setRequired(e.target.checked)} />
            {t('crit.required')}
          </label>
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            <button className="primary" disabled={busy || !ready} onClick={() => void submit()}>
              {t('crit.create')}
            </button>
          </div>
        </div>
    </Modal>
  );
}

export function CriteriaTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy } = useHubAction();
  const [filter, setFilter] = useState('all');
  const [creating, setCreating] = useState(false);
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

  const knownPhases = [...new Set(all.map((c) => str(c, 'phase') ?? '').filter((p) => p !== ''))].sort();

  return (
    <div className="region-pad scroll">
      <div className="kanban-bar">
        {CRIT_STATES.map((s) => (
          <button key={s} className={filter === s ? 'chip active' : 'chip'} onClick={() => setFilter(s)}>
            {t(`crit.${s}`)}
          </button>
        ))}
        <span className="spacer" />
        <button onClick={() => setCreating(true)}>+ {t('crit.new')}</button>
      </div>
      {creating && <NewCriterionForm projectId={projectId} phases={knownPhases} onDone={() => setCreating(false)} />}
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
  const [sendingBack, setSendingBack] = useState(false);
  const [note, setNote] = useState('');
  const [openComp, setOpenComp] = useState<{ kind: string; refId: string } | null>(null);
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
    ['attention'],
  ];

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={str(d, 'kind') ?? t('deliv.title')}>
        <div className="admin-tabs">
          <strong>{str(d, 'kind') ?? t('deliv.title')}</strong>
          <span className={`sev ${critStateClass(state === 'ratified' ? 'met' : 'pending')}`}>{state}</span>
          <span className="spacer" />
          {ratified ? (
            <button disabled={busy} onClick={() => void run(() => client!.unratifyDeliverable(projectId, deliverableId), { invalidate })}>
              {t('deliv.unratify')}
            </button>
          ) : (
            <>
              <button disabled={busy} title={t('deliv.sendBackHint')} onClick={() => setSendingBack((v) => !v)}>
                {t('deliv.sendBack')}
              </button>
              <button className="primary" disabled={busy} onClick={() => void run(() => client!.ratifyDeliverable(projectId, deliverableId), { invalidate })}>
                {t('deliv.ratify')}
              </button>
            </>
          )}
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {delivQ.isLoading && <div className="muted">{t('common.loading')}</div>}
          {error !== null && <div className="error">{error}</div>}
          {sendingBack && !ratified && (
            <section className="setting-group send-back-box">
              <h3>{t('deliv.sendBack')}</h3>
              <p className="muted small">{t('deliv.sendBackGuidance')}</p>
              <textarea value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('deliv.sendBackNote')} autoFocus />
              <div className="task-form-actions">
                <button onClick={() => setSendingBack(false)}>{t('admin.close')}</button>
                <button
                  className="primary"
                  disabled={busy || note.trim() === ''}
                  onClick={() =>
                    void (async () => {
                      const ok = await run(() => client!.sendBackDeliverable(projectId, deliverableId, { note: note.trim() }), { invalidate });
                      if (ok !== undefined) {
                        setNote('');
                        setSendingBack(false);
                      }
                    })()
                  }
                >
                  {t('deliv.sendBackSubmit')}
                </button>
              </div>
            </section>
          )}
          <div className="muted small">
            {t('deliv.phase')}: {str(d, 'phase') ?? '—'}
            {str(d, 'ratified_at') !== undefined ? ` · ${t('deliv.ratifiedAt')} ${str(d, 'ratified_at')}` : ''}
          </div>

          <section className="setting-group">
            <h3>{t('deliv.components')}</h3>
            {components.length === 0 ? (
              <div className="muted">{t('deliv.noComponents')}</div>
            ) : (
              components.map((c, i) => {
                const refId = str(c, 'ref_id') ?? '';
                const ckind = str(c, 'kind') ?? '';
                // Documents and artifacts have viewable content; a task/criteria
                // component is just a reference, so its row stays inert.
                const viewable = refId !== '' && isViewableComponent(ckind);
                return (
                  <button
                    key={refId || String(i)}
                    className="file-row"
                    disabled={!viewable}
                    title={viewable ? t('deliv.viewComponent') : undefined}
                    onClick={() => viewable && setOpenComp({ kind: ckind, refId })}
                  >
                    <span className="pill">{ckind || '—'}</span>
                    <span className="mono small">{refId}</span>
                    <span className="spacer" />
                    {bool(c, 'required') === true && <span className="muted small">{t('crit.required')}</span>}
                  </button>
                );
              })
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
      {openComp !== null && (
        <ComponentView
          projectId={projectId}
          kind={openComp.kind}
          refId={openComp.refId}
          onClose={() => setOpenComp(null)}
        />
      )}
    </Modal>
  );
}

// Deliverable component kinds whose content we can render inline: authored
// documents (markdown) and blob-backed artifacts. Everything else (task,
// criteria, run refs) is a pointer, not a document.
const DOC_KINDS = new Set(['document', 'doc', 'report', 'memo', 'draft', 'review', 'sample']);
function isViewableComponent(kind: string): boolean {
  return DOC_KINDS.has(kind) || kind === 'artifact';
}

/// The blob sha for an artifact row — the handle lives in `uri` as
/// `blob:sha256/<hex>` (or `hub-blob://<hex>`); `sha256` is rarely populated.
function shaFromArtifact(a: Entity): string | undefined {
  const uri = str(a, 'uri') ?? '';
  return (
    (str(a, 'sha256') ?? '') ||
    (uri.startsWith('blob:sha256/')
      ? uri.slice('blob:sha256/'.length)
      : uri.startsWith('hub-blob://')
        ? uri.slice('hub-blob://'.length)
        : undefined)
  );
}

/// View a deliverable component's actual content (director asked "how do I see
/// the output, not just its id"). A `document` resolves via `getDocument` and
/// renders its inline markdown; an `artifact` (or an artifact-backed document)
/// resolves to a blob via the project's artifact list and defers to the full
/// `ArtifactViewer`. A component with no viewable body shows a clear note.
function ComponentView({
  projectId,
  kind,
  refId,
  onClose,
}: {
  projectId: string;
  kind: string;
  refId: string;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const isDoc = DOC_KINDS.has(kind);

  const docQ = useQuery({
    queryKey: ['document', refId],
    enabled: client !== null && isDoc,
    queryFn: () => client!.getDocument(refId),
  });
  const artsQ = useQuery({
    queryKey: ['project-artifacts', projectId],
    enabled: client !== null,
    queryFn: () => client!.listArtifacts({ project: projectId }),
  });

  const doc = docQ.data;
  const inline = doc !== undefined ? str(doc, 'content_inline') : undefined;
  // The artifact to resolve: an artifact component's ref directly, or a
  // document's backing artifact when it has no inline body.
  const backingArtId = isDoc ? (doc !== undefined ? str(doc, 'artifact_id') : undefined) : refId;
  const art =
    backingArtId !== undefined && backingArtId !== ''
      ? (artsQ.data ?? []).find((a) => str(a, 'id') === backingArtId)
      : undefined;
  const sha = art !== undefined ? shaFromArtifact(art) : undefined;

  // A blob-backed component → hand off to the dedicated artifact viewer.
  if (art !== undefined && sha !== undefined && sha !== '') {
    return (
      <ArtifactViewer
        sha={sha}
        name={str(art, 'name') ?? (doc !== undefined ? str(doc, 'title') : undefined) ?? refId}
        mime={str(art, 'mime')}
        kind={str(art, 'kind')}
        onClose={onClose}
      />
    );
  }

  const loading = (isDoc && docQ.isLoading) || (backingArtId !== undefined && artsQ.isLoading);
  const title = (doc !== undefined ? str(doc, 'title') : undefined) ?? kind ?? t('deliv.component');

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={title}>
        <div className="admin-tabs">
          <strong>{title}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll doc-body">
          {loading && <div className="muted">{t('common.loading')}</div>}
          {isDoc && docQ.isError && <div className="error">{(docQ.error as Error).message}</div>}
          {!loading && inline !== undefined && inline !== '' ? (
            <Markdown text={inline} />
          ) : !loading ? (
            <div className="muted">
              {t('deliv.componentUnviewable')} <span className="mono small">{kind} · {refId}</span>
            </div>
          ) : null}
        </div>
    </Modal>
  );
}

// ---- Documents: the project's hub Documents (authored reports/memos) --------
// Parity with the mobile project "Documents" tile. These are the hub Document
// entities (not the docs_root files under Files) — the authored outputs a
// steward produces; clicking one renders its content inline via ComponentView.

export function DocumentsTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [open, setOpen] = useState<{ kind: string; refId: string } | null>(null);

  const q = useQuery({
    queryKey: ['documents', projectId],
    enabled: client !== null,
    refetchInterval: 20000,
    queryFn: () => client!.listDocuments(projectId),
  });
  const docs = q.data ?? [];

  return (
    <div className="region-pad scroll">
      <p className="muted small">{t('docs.projectGuidance')}</p>
      {q.isLoading && <div className="muted">{t('common.loading')}</div>}
      {q.isError && <div className="error">{(q.error as Error).message}</div>}
      {!q.isLoading && docs.length === 0 && <div className="muted">{t('docs.none')}</div>}
      {docs.map((d, i) => {
        const id = str(d, 'id') ?? '';
        const kind = str(d, 'kind') ?? 'document';
        return (
          <button
            key={id || String(i)}
            className="file-row"
            disabled={id === ''}
            onClick={() => id !== '' && setOpen({ kind: 'document', refId: id })}
          >
            <span className="file-name">{str(d, 'title') ?? `${kind} · ${id}`}</span>
            <span className="spacer" />
            <span className="muted small mono">
              {kind}
              {num(d, 'version') !== undefined ? ` · v${num(d, 'version')}` : ''}
            </span>
          </button>
        );
      })}
      {open !== null && (
        <ComponentView projectId={projectId} kind={open.kind} refId={open.refId} onClose={() => setOpen(null)} />
      )}
    </div>
  );
}

// ---- Files: docs_root tree + project artifacts (parity — DocsSection) --------

export function FilesTab({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [openDoc, setOpenDoc] = useState<string | null>(null);
  const [openArt, setOpenArt] = useState<{ sha: string; name: string; mime?: string; kind?: string } | null>(null);

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
        {artifacts.map((a, i) => {
          // The blob handle lives in `uri` as `blob:sha256/<hex>` (or
          // `hub-blob://<hex>`); the `sha256` field is emitted with omitempty and
          // is almost never populated (hub stores the sha inside the URI —
          // shaFromBlobURI, mcp_more.go). Read the URI first, fall back to
          // sha256. Non-blob URIs (mock/external) yield undefined → row stays
          // disabled, which is correct.
          const uri = str(a, 'uri') ?? '';
          const sha =
            (str(a, 'sha256') ?? '') ||
            (uri.startsWith('blob:sha256/')
              ? uri.slice('blob:sha256/'.length)
              : uri.startsWith('hub-blob://')
                ? uri.slice('hub-blob://'.length)
                : undefined);
          const name = str(a, 'name') ?? str(a, 'kind') ?? str(a, 'id') ?? '';
          return (
            <button
              key={str(a, 'id') ?? String(i)}
              className="file-row"
              disabled={sha === undefined || sha === ''}
              title={sha === undefined ? t('files.noBlob') : name}
              onClick={() =>
                sha !== undefined && setOpenArt({ sha, name, mime: str(a, 'mime'), kind: str(a, 'kind') })
              }
            >
              <span className="file-name">{name}</span>
              <span className="spacer" />
              <span className="muted small mono">
                {str(a, 'kind') ?? ''}
                {num(a, 'size') !== undefined ? ` · ${num(a, 'size')} B` : ''}
              </span>
            </button>
          );
        })}
      </section>

      {openDoc !== null && (
        <Modal onClose={() => setOpenDoc(null)} className="task-detail" ariaLabel={openDoc}>
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
        </Modal>
      )}

      {openArt !== null && (
        <ArtifactViewer
          sha={openArt.sha}
          name={openArt.name}
          mime={openArt.mime}
          kind={openArt.kind}
          onClose={() => setOpenArt(null)}
        />
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
