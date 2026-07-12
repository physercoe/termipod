import { useState } from 'react';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { useDocuments, type Doc } from '../state/documents';
import { Markdown } from '../ui/Markdown';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J2 — Author reports / slides / figures. A workspace of **multiple documents
/// as tabs** (director request), each a split GFM+math+code Markdown editor
/// (source ↔ live preview) over the shared `Markdown` renderer (KaTeX +
/// highlight.js, offline).
///
/// Storage: documents are device-local (localStorage) by default and can be
/// saved to a real file on disk via the native dialog — the header shows where
/// the active document lives. The landscape doc's posture is EMBED BlockNote +
/// Quarto/Typst export for the reproducible-report path; a draw.io `diagram`
/// document kind lands once drawio is bundled offline (see the
/// author-agent-assist-and-diagrams discussion). Agent-assisted writing (the
/// side panel) is deferred pending the host-OS discussion.

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke: inv } = await import('@tauri-apps/api/core');
  return inv<T>(cmd, args);
}

function baseName(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function Editor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const update = useDocuments((s) => s.update);
  const words = doc.body.trim() ? doc.body.trim().split(/\s+/).length : 0;
  return (
    <div className="author-doc">
      <div className="author-doc-meta muted small">
        {t('author.words').replace('{n}', String(words))}
        <span className="spacer" />
        {doc.filePath !== undefined ? (
          <span title={doc.filePath}>
            {doc.dirty === true ? '● ' : ''}
            {t('author.savedFile').replace('{f}', baseName(doc.filePath))}
          </span>
        ) : (
          <span title={t('author.savedLocalHint')}>{t('author.savedLocal')}</span>
        )}
      </div>
      <div className="split-2">
        <textarea
          className="editor-pane mono"
          value={doc.body}
          onChange={(e) => update(doc.id, { body: e.target.value })}
          placeholder={t('author.placeholder')}
          spellCheck={false}
        />
        <div className="preview-pane">
          {doc.body.trim() ? (
            <Markdown text={doc.body} />
          ) : (
            <div className="muted region-pad">{t('author.empty')}</div>
          )}
        </div>
      </div>
    </div>
  );
}

export function AuthorSurface(): JSX.Element {
  const t = useT();
  const docs = useDocuments((s) => s.docs);
  const activeId = useDocuments((s) => s.activeId);
  const create = useDocuments((s) => s.create);
  const remove = useDocuments((s) => s.remove);
  const setActive = useDocuments((s) => s.setActive);
  const markSaved = useDocuments((s) => s.markSaved);
  const [busy, setBusy] = useState(false);

  const active = docs.find((d) => d.id === activeId);
  const tauri = isTauri();

  function closeTab(id: string): void {
    const d = docs.find((x) => x.id === id);
    if (d !== undefined && d.body.trim() !== '' && d.body.trim() !== '#') {
      if (!window.confirm(t('author.confirmClose'))) return;
    }
    remove(id);
  }

  async function onSave(): Promise<void> {
    if (active === undefined || !tauri) return;
    setBusy(true);
    try {
      if (active.filePath !== undefined) {
        await invoke('doc_write', { path: active.filePath, content: active.body });
        markSaved(active.id, active.filePath);
      } else {
        const name = (active.title !== '' ? active.title : 'document').replace(/[^\w.-]+/g, '-');
        const path = await invoke<string | null>('doc_save', {
          content: active.body,
          defaultName: `${name}.md`,
        });
        if (path !== null) markSaved(active.id, path, baseName(path));
      }
    } catch {
      /* ignore — user can retry */
    } finally {
      setBusy(false);
    }
  }

  async function onOpen(): Promise<void> {
    if (!tauri) return;
    setBusy(true);
    try {
      const res = await invoke<{ path: string; content: string } | null>('doc_open');
      if (res !== null) {
        create('markdown', { title: baseName(res.path), body: res.content, filePath: res.path });
      }
    } catch {
      /* ignore */
    } finally {
      setBusy(false);
    }
  }

  return (
    <WorkbenchSurface
      job="author"
      actions={
        <>
          <button className="import-btn" onClick={() => create('markdown')}>
            + {t('author.newDoc')}
          </button>
          {tauri && (
            <>
              <button className="import-btn" disabled={busy} onClick={() => void onOpen()}>
                {t('author.openFile')}
              </button>
              <button className="import-btn" disabled={busy || active === undefined} onClick={() => void onSave()}>
                {t('author.saveFile')}
              </button>
            </>
          )}
        </>
      }
    >
      {docs.length > 0 && (
        <div className="read-tabstrip">
          {docs.map((d) => (
            <span key={d.id} className={`read-tabitem${activeId === d.id ? ' active' : ''}`}>
              <button className="read-tabitem-label" title={d.filePath ?? d.title} onClick={() => setActive(d.id)}>
                <span className="read-tabitem-kind">{d.kind === 'diagram' ? '◈' : '📝'}</span>
                {d.dirty === true ? '● ' : ''}
                {d.title !== '' ? d.title : t('author.untitled')}
              </button>
              <button className="read-tabitem-x" title={t('read.closeTab')} onClick={() => closeTab(d.id)}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {active !== undefined ? (
        <Editor key={active.id} doc={active} />
      ) : (
        <div className="author-empty muted">
          <p>{t('author.noDocs')}</p>
          <button className="primary" onClick={() => create('markdown')}>
            + {t('author.newDoc')}
          </button>
        </div>
      )}
    </WorkbenchSurface>
  );
}
