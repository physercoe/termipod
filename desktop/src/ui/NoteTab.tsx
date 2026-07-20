import { lazy, Suspense, useMemo, useRef } from 'react';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { useLibrary } from '../state/library';
import { Icon } from './Icon';
import { Markdown } from './Markdown';
import { MarkdownEditor } from './MarkdownEditor';
import { extractHeadings, MarkdownOutline } from './MarkdownOutline';
import { useNotesMode } from './useNotesMode';

const WysiwygEditor = lazy(() => import('./WysiwygEditor').then((m) => ({ default: m.WysiwygEditor })));

/// A reference's note (`ref.notes`) opened full-width in its own reader tab, with
/// a left outline/nav rail built from the note's headings — the roomy editing
/// surface the cramped Inspector "Notes" tab can't be. Same three edit modes
/// (Rich / Source / Preview) as the Inspector, sharing the persisted
/// `termipod.read.notesMode` so the choice follows the reader everywhere.
///
/// The outline scroll-to targets stamped heading ids, which only Preview renders
/// (`headingIds`); in Rich/Source it is a structural map (click is a best-effort
/// no-op there). The rail is the shared `MarkdownOutline` (#322), so it's
/// resizable and collapsible like the md/epub readers.

export function NoteTab({ refId }: { refId: string }): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const update = useLibrary((s) => s.updateReference);
  const [mode, pick] = useNotesMode();
  const bodyRef = useRef<HTMLDivElement | null>(null);

  const notes = ref?.notes ?? '';
  const headings = useMemo(() => extractHeadings(notes), [notes]);

  async function exportNotes(): Promise<void> {
    if (ref === undefined || !isTauri()) return;
    const base = (ref.title !== '' ? ref.title : 'note').slice(0, 60).replace(/[^\w.-]+/g, '-');
    try {
      const { invoke } = await import('@tauri-apps/api/core');
      await invoke('doc_save', { content: notes, defaultName: `${base}.md` });
    } catch {
      /* cancelled / unavailable */
    }
  }

  if (ref === undefined) {
    return <div className="muted region-pad">{t('read.noteGone')}</div>;
  }

  return (
    <div className="notetab">
      <MarkdownOutline headings={headings} bodyRef={bodyRef} widthKey="termipod.note.outlineW" />
      <div className="notetab-main">
        <div className="ref-notes-bar">
          <div className="seg">
            <button className={mode === 'wysiwyg' ? 'seg-btn active' : 'seg-btn'} onClick={() => pick('wysiwyg')}>
              {t('read.notesWysiwyg')}
            </button>
            <button className={mode === 'source' ? 'seg-btn active' : 'seg-btn'} onClick={() => pick('source')}>
              {t('read.notesSource')}
            </button>
            <button className={mode === 'preview' ? 'seg-btn active' : 'seg-btn'} onClick={() => pick('preview')}>
              {t('read.notesPreview')}
            </button>
          </div>
          <span className="spacer" />
          {isTauri() && (
            <button className="link-btn" title={t('read.notesExport')} onClick={() => void exportNotes()}>
              <Icon name="download" size={14} /> {t('read.notesExport')}
            </button>
          )}
        </div>
        <div className="notetab-body" ref={bodyRef}>
          {mode === 'preview' ? (
            <div className="ref-notes-preview doc-body region-pad">
              <Markdown text={notes} singleDollarMath headingIds />
            </div>
          ) : mode === 'source' ? (
            <MarkdownEditor
              key={`note-src-${ref.id}`}
              value={notes}
              onChange={(v) => update(ref.id, { notes: v })}
              placeholder={t('read.notesPlaceholder')}
            />
          ) : (
            <Suspense fallback={<div className="muted region-pad">{t('read.loadingFile')}</div>}>
              <WysiwygEditor
                key={`note-wys-${ref.id}`}
                value={notes}
                onChange={(v) => update(ref.id, { notes: v })}
                placeholder={t('read.notesPlaceholder')}
              />
            </Suspense>
          )}
        </div>
      </div>
    </div>
  );
}
