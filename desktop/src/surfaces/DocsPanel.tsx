import { useCallback, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { useProjects } from '../hub/queries';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { useConfirm } from '../ui/ConfirmModal';
import { Markdown } from '../ui/Markdown';
import { Modal } from '../ui/Modal';

const DOC_KINDS = ['memo', 'draft', 'report', 'review'];

/// Compose a new document, or a new version of an existing one (parity — mobile
/// DocumentCreateSheet). Documents are versioned: editing POSTs a fresh version
/// with `prev_version_id` pointing at the current row (there is no whole-document
/// PATCH). `base` pre-fills the form for an edit.
function DocumentCompose({
  projectId,
  base,
  onDone,
}: {
  projectId: string;
  base?: Entity;
  onDone: (createdId?: string) => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const editing = base !== undefined;
  const initKind = base !== undefined ? str(base, 'kind') ?? 'memo' : 'memo';
  const initTitle = base !== undefined ? str(base, 'title') ?? '' : '';
  const initBody = base !== undefined ? str(base, 'content_inline') ?? '' : '';
  const [kind, setKind] = useState(initKind);
  const [title, setTitle] = useState(initTitle);
  const [body, setBody] = useState(initBody);

  async function submit(): Promise<void> {
    if (client === null || title.trim() === '') return;
    const created = await run(
      () =>
        client.createDocument({
          project_id: projectId,
          kind,
          title: title.trim(),
          content_inline: body !== '' ? body : '(no content)',
          prev_version_id: editing ? str(base, 'id') : undefined,
        }),
      { invalidate: [['documents', projectId]] },
    );
    if (created !== undefined) onDone(str(created, 'id'));
  }

  // Dirty-close guard (#313): backdrop / Escape / Close used to discard an
  // unsaved draft silently — confirm before dropping it. useCallback keeps
  // Modal's keydown effect from re-registering every render.
  const dirty = kind !== initKind || title !== initTitle || body !== initBody;
  const attemptClose = useCallback(async (): Promise<void> => {
    if (!dirty || (await confirmAsk({ message: t('confirm.discardChanges'), danger: true }))) onDone();
  }, [dirty, confirmAsk, onDone, t]);

  return (
    <>
    <Modal onClose={attemptClose} className="task-detail" ariaLabel={editing ? t('docs.newVersion') : t('docs.new')}>
        <div className="admin-tabs">
          <strong>{editing ? t('docs.newVersion') : t('docs.new')}</strong>
          <span className="spacer" />
          <button onClick={() => void attemptClose()}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <label className="wide">
            {t('docs.kind')}
            <div className="seg">
              {DOC_KINDS.map((k) => (
                <button key={k} className={kind === k ? 'seg-btn active' : 'seg-btn'} disabled={editing} onClick={() => setKind(k)}>
                  {k}
                </button>
              ))}
            </div>
          </label>
          <label className="wide">
            {t('docs.docTitle')}
            <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
          </label>
          <label className="wide">
            {t('docs.body')}
            <textarea className="doc-compose-body" value={body} spellCheck onChange={(e) => setBody(e.target.value)} placeholder={t('docs.bodyPlaceholder')} />
          </label>
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            <button className="primary" disabled={busy || title.trim() === ''} onClick={() => void submit()}>
              {editing ? t('docs.saveVersion') : t('docs.create')}
            </button>
          </div>
        </div>
    </Modal>
    {confirmNode}
    </>
  );
}

/// Documents surface (parity Phase 4). Lists a project's DB-row documents
/// (`GET …/documents?project=`, `handleListDocuments` — list rows omit the body)
/// and, on select, fetches the full document (`GET …/documents/{id}`) whose
/// `content_inline` markdown is rendered via the F1 Markdown primitive. When a
/// document is artifact-backed (no inline body) we show its artifact ref rather
/// than fetching the blob bytes. Read-only.
export function DocsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];
  const [projectId, setProjectId] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);
  const [editing, setEditing] = useState(false);

  const effectiveProject = projectId !== '' ? projectId : (projects[0] !== undefined ? str(projects[0], 'id') ?? '' : '');

  const listQ = useQuery({
    queryKey: ['documents', effectiveProject],
    enabled: client !== null && effectiveProject !== '',
    refetchInterval: 20000,
    queryFn: () => client!.listDocuments(effectiveProject),
  });

  const docQ = useQuery({
    queryKey: ['document', selected],
    enabled: client !== null && selected !== null,
    queryFn: () => client!.getDocument(selected as string),
  });

  const docs = listQ.data ?? [];

  function docLabel(d: Entity): string {
    return str(d, 'title') ?? `${str(d, 'kind') ?? 'doc'} · ${str(d, 'id')}`;
  }

  const doc = docQ.data;
  const inline = doc !== undefined ? str(doc, 'content_inline') : undefined;
  const artifactId = doc !== undefined ? str(doc, 'artifact_id') : undefined;

  return (
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('docs.title')}>
        <div className="admin-tabs">
          <strong>{t('docs.title')}</strong>
          <select
            value={effectiveProject}
            onChange={(e) => {
              setProjectId(e.target.value);
              setSelected(null);
            }}
          >
            {projects.map((p) => {
              const id = str(p, 'id') ?? '';
              return (
                <option key={id} value={id}>
                  {str(p, 'name') ?? id}
                </option>
              );
            })}
          </select>
          <button disabled={effectiveProject === ''} onClick={() => setComposing(true)}>
            + {t('docs.new')}
          </button>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        {composing && effectiveProject !== '' && (
          <DocumentCompose
            projectId={effectiveProject}
            onDone={(id) => {
              setComposing(false);
              if (id !== undefined) setSelected(id);
            }}
          />
        )}
        {editing && doc !== undefined && effectiveProject !== '' && (
          <DocumentCompose
            projectId={effectiveProject}
            base={doc}
            onDone={(id) => {
              setEditing(false);
              if (id !== undefined) setSelected(id);
            }}
          />
        )}
        <div className="sessions-body">
          <div className="sessions-list">
            {listQ.isLoading && <div className="muted region-pad">{t('common.loading')}</div>}
            {listQ.isError && <div className="error region-pad">{(listQ.error as Error).message}</div>}
            {docs.map((d) => {
              const id = str(d, 'id') ?? '';
              return (
                <button
                  key={id}
                  className={id === selected ? 'session-item active' : 'session-item'}
                  onClick={() => setSelected(id)}
                >
                  <span className="session-name">{docLabel(d)}</span>
                  <span className="muted small">
                    {str(d, 'kind') ?? ''}
                    {num(d, 'version') !== undefined ? ` · v${num(d, 'version')}` : ''}
                  </span>
                </button>
              );
            })}
            {!listQ.isLoading && docs.length === 0 && <div className="muted region-pad">{t('docs.none')}</div>}
          </div>
          <div className="sessions-detail scroll">
            {selected === null ? (
              <div className="muted region-pad">{t('docs.pick')}</div>
            ) : docQ.isLoading ? (
              <div className="muted region-pad">{t('common.loading')}</div>
            ) : docQ.isError ? (
              <div className="error region-pad">{(docQ.error as Error).message}</div>
            ) : inline !== undefined && inline !== '' ? (
              <div className="region-pad doc-body">
                <div className="doc-toolbar">
                  <span className="spacer" />
                  <button onClick={() => setEditing(true)}>{t('docs.editNewVersion')}</button>
                </div>
                <Markdown text={inline} />
              </div>
            ) : artifactId !== undefined ? (
              <div className="region-pad muted">
                {t('docs.artifactBacked')} <span className="mono">{artifactId}</span>
              </div>
            ) : (
              <div className="region-pad muted">{t('docs.empty')}</div>
            )}
          </div>
        </div>
    </Modal>
  );
}
