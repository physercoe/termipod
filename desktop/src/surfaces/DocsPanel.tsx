import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';

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
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="sessions-panel" onMouseDown={(e) => e.stopPropagation()}>
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
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
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
      </div>
    </div>
  );
}
