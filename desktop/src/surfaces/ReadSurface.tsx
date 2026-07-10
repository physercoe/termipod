import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { useDraft } from '../state/draft';
import { Markdown } from '../ui/Markdown';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J1 — Read papers/reports in depth. The desktop-shaped reading job: long-form
/// content on the left, notes alongside on the right (the dual-pane the phone
/// can't do). Round-1 source is a hub project Document (`content_inline`
/// rendered via the shared Markdown primitive) or pasted text; the landscape
/// doc's posture is EMBED Semantic Reader / PaperCraft for real PDF/HTML paper
/// reading, which supersedes the paste path in a later round. Notes are
/// device-local scratch keyed by the source, ready to graduate to hub-backed
/// incubation notes (`research-reading-and-ideation-ui.md`).
export function ReadSurface(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];
  const [projectId, setProjectId] = useState('');
  const [docId, setDocId] = useState('');
  const [pasted, setPasted] = useState('');

  const effectiveProject = projectId !== '' ? projectId : str(projects[0] ?? {}, 'id') ?? '';

  const listQ = useQuery({
    queryKey: ['documents', effectiveProject],
    enabled: client !== null && effectiveProject !== '',
    refetchInterval: 30000,
    queryFn: () => client!.listDocuments(effectiveProject),
  });
  const docQ = useQuery({
    queryKey: ['document', docId],
    enabled: client !== null && docId !== '',
    queryFn: () => client!.getDocument(docId),
  });

  const docs = listQ.data ?? [];
  const doc = docQ.data;
  const docBody = doc !== undefined ? str(doc, 'content_inline') ?? '' : '';
  const reading = docId !== '' ? docBody : pasted;
  // Notes are keyed by the source so switching docs swaps the note-pad with it.
  const [notes, setNotes] = useDraft(`read.${docId !== '' ? docId : 'paste'}`);

  function docLabel(d: Entity): string {
    return str(d, 'title') ?? `${str(d, 'kind') ?? 'doc'} · ${str(d, 'id')}`;
  }

  return (
    <WorkbenchSurface
      job="read"
      actions={
        <>
          <select
            className="surface-select"
            value={effectiveProject}
            onChange={(e) => {
              setProjectId(e.target.value);
              setDocId('');
            }}
          >
            <option value="">{t('read.pickProject')}</option>
            {projects.map((p) => {
              const id = str(p, 'id') ?? '';
              return (
                <option key={id} value={id}>
                  {str(p, 'name') ?? id}
                </option>
              );
            })}
          </select>
          <select className="surface-select" value={docId} onChange={(e) => setDocId(e.target.value)}>
            <option value="">{t('read.pasteMode')}</option>
            {docs.map((d) => {
              const id = str(d, 'id') ?? '';
              return (
                <option key={id} value={id}>
                  {docLabel(d)}
                </option>
              );
            })}
          </select>
        </>
      }
    >
      <div className="split-2">
        <div className="reading-pane">
          {docId === '' && reading.trim() === '' ? (
            <textarea
              className="editor-pane mono"
              value={pasted}
              onChange={(e) => setPasted(e.target.value)}
              placeholder={t('read.pastePlaceholder')}
              spellCheck={false}
            />
          ) : reading.trim() === '' ? (
            <div className="muted region-pad">{docQ.isLoading ? t('common.loading') : t('read.empty')}</div>
          ) : (
            <div className="region-pad doc-body">
              {docId === '' && (
                <div className="doc-toolbar">
                  <button onClick={() => setPasted('')}>{t('read.clearPaste')}</button>
                </div>
              )}
              <Markdown text={reading} />
            </div>
          )}
        </div>
        <div className="notes-pane">
          <div className="notes-head muted small">{t('read.notes')}</div>
          <textarea
            className="editor-pane"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder={t('read.notesPlaceholder')}
          />
        </div>
      </div>
    </WorkbenchSurface>
  );
}
