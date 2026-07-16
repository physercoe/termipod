import { lazy, Suspense, useRef, useState } from 'react';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { bodyToFile, extForKind, fileToBody, kindForExt, useDocuments, type Doc } from '../state/documents';
import { AgentCompanion } from '../ui/AgentCompanion';
import { docKindIcon, Icon, type IconName } from '../ui/Icon';
import { AuthorNav } from './AuthorNav';
import { DiagramEditor } from './DiagramEditor';
import { CanvasEditor } from '../ui/CanvasEditor';
import { Markdown } from '../ui/Markdown';
// The table/database grid is only pulled in when a table doc is opened.
const TableEditor = lazy(() => import('../ui/TableEditor').then((m) => ({ default: m.TableEditor })));
import type { MarkdownEditorHandle } from '../ui/MarkdownEditor';
// CodeMirror is heavy (~500 KB) and Author isn't the landing tab — split it out.
const MarkdownEditor = lazy(() => import('../ui/MarkdownEditor').then((m) => ({ default: m.MarkdownEditor })));
const WysiwygEditor = lazy(() => import('../ui/WysiwygEditor').then((m) => ({ default: m.WysiwygEditor })));
import { ResizeHandle } from '../ui/ResizeHandle';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

const clamp = (n: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, n));
function loadW(key: string, fallback: number): number {
  const v = Number(localStorage.getItem(key));
  return Number.isFinite(v) && v > 0 ? v : fallback;
}

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

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? '';
}

type ViewMode = 'wysiwyg' | 'edit' | 'split' | 'read';

function Editor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const update = useDocuments((s) => s.update);
  const edRef = useRef<MarkdownEditorHandle>(null);
  const [mode, setMode] = useState<ViewMode>(() => {
    const v = localStorage.getItem('termipod.author.viewMode');
    return v === 'wysiwyg' || v === 'edit' || v === 'split' || v === 'read' ? v : 'split';
  });
  function pickMode(m: ViewMode): void {
    setMode(m);
    try {
      localStorage.setItem('termipod.author.viewMode', m);
    } catch {
      /* ignore */
    }
  }
  const words = doc.body.trim() ? doc.body.trim().split(/\s+/).length : 0;

  // Formatting actions act on the live CodeMirror selection (mousedown-preventDefault
  // keeps that selection alive through the button click).
  const fmt: { icon: IconName; title: string; run: () => void }[] = [
    { icon: 'bold', title: t('author.fmtBold'), run: () => edRef.current?.wrap('**') },
    { icon: 'italic', title: t('author.fmtItalic'), run: () => edRef.current?.wrap('*') },
    { icon: 'code', title: t('author.fmtCode'), run: () => edRef.current?.wrap('`') },
    { icon: 'heading', title: t('author.fmtHeading'), run: () => edRef.current?.linePrefix('## ') },
    { icon: 'list', title: t('author.fmtList'), run: () => edRef.current?.linePrefix('- ') },
    { icon: 'list-ordered', title: t('author.fmtOList'), run: () => edRef.current?.linePrefix('1. ') },
    { icon: 'quote', title: t('author.fmtQuote'), run: () => edRef.current?.linePrefix('> ') },
    { icon: 'link', title: t('author.fmtLink'), run: () => edRef.current?.wrap('[', '](url)') },
  ];

  return (
    <div className="author-doc">
      <div className="author-doc-bar">
        <div className="seg author-viewmode">
          {(['wysiwyg', 'edit', 'split', 'read'] as ViewMode[]).map((m) => (
            <button key={m} className={mode === m ? 'seg-btn active' : 'seg-btn'} onClick={() => pickMode(m)}>
              {t(`author.mode_${m}`)}
            </button>
          ))}
        </div>
        {(mode === 'edit' || mode === 'split') && (
          <div className="author-fmt">
            {fmt.map((f) => (
              <button
                key={f.title}
                className="author-fmt-btn"
                title={f.title}
                onMouseDown={(e) => e.preventDefault()}
                onClick={f.run}
              >
                <Icon name={f.icon} size={15} />
              </button>
            ))}
          </div>
        )}
        <span className="spacer" />
        <span className="author-doc-meta muted small">
          {t('author.words').replace('{n}', String(words))}
          {doc.filePath !== undefined ? (
            <span title={doc.filePath}>
              {' · '}
              {doc.dirty === true ? '● ' : ''}
              {t('author.savedFile').replace('{f}', baseName(doc.filePath))}
            </span>
          ) : (
            <span title={t('author.savedLocalHint')}>{' · '}{t('author.savedLocal')}</span>
          )}
        </span>
      </div>
      <div className={`author-body mode-${mode}`}>
        {mode === 'wysiwyg' ? (
          <Suspense fallback={<div className="milkdown-host muted region-pad">{t('author.loadingEditor')}</div>}>
            <WysiwygEditor
              key={doc.id}
              value={doc.body}
              onChange={(v) => update(doc.id, { body: v })}
              placeholder={t('author.placeholder')}
            />
          </Suspense>
        ) : (
          <>
            {mode !== 'read' && (
              <Suspense fallback={<div className="md-editor muted region-pad">{t('author.loadingEditor')}</div>}>
                <MarkdownEditor
                  ref={edRef}
                  value={doc.body}
                  onChange={(v) => update(doc.id, { body: v })}
                  placeholder={t('author.placeholder')}
                />
              </Suspense>
            )}
            {mode !== 'edit' && (
              <div className="preview-pane">
                {doc.body.trim() ? (
                  <Markdown text={doc.body} />
                ) : (
                  <div className="muted region-pad">{t('author.empty')}</div>
                )}
              </div>
            )}
          </>
        )}
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
  const update = useDocuments((s) => s.update);
  const [busy, setBusy] = useState(false);
  // Left file/workspace tree. On by default; width persisted.
  const [showNav, setShowNav] = useState(() => localStorage.getItem('termipod.author.showNav') !== '0');
  const [navW, setNavW] = useState(() => loadW('termipod.author.navW', 240));
  // Agent-assist side panel (hub-attached). Off by default; width persisted.
  const [showAgent, setShowAgent] = useState(false);
  const [agentW, setAgentW] = useState(() => loadW('termipod.author.agentW', 380));
  // Two-step close arm — `window.confirm` is unreliable in the Tauri webview, so
  // the first × click arms (turns into a confirm ×), the second closes.
  const [confirmClose, setConfirmClose] = useState<string | null>(null);

  const active = docs.find((d) => d.id === activeId);
  const tauri = isTauri();

  function closeTab(id: string): void {
    const d = docs.find((x) => x.id === id);
    const hasContent = d !== undefined && d.body.trim() !== '' && d.body.trim() !== '#';
    if (hasContent && confirmClose !== id) {
      setConfirmClose(id);
      return;
    }
    setConfirmClose(null);
    remove(id);
  }

  async function onSave(): Promise<void> {
    if (active === undefined || !tauri) return;
    setBusy(true);
    try {
      const content = bodyToFile(active.kind, active.body, t('table.colName'));
      if (active.filePath !== undefined) {
        await invoke('doc_write', { path: active.filePath, content });
        markSaved(active.id, active.filePath);
      } else {
        const name = (active.title !== '' ? active.title : 'document').replace(/[^\w.-]+/g, '-');
        const path = await invoke<string | null>('doc_save', {
          content,
          defaultName: `${name}.${extForKind(active.kind)}`,
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
        const kind = kindForExt(extOf(res.path));
        create(kind, {
          title: baseName(res.path),
          body: fileToBody(kind, res.content, t('table.colName')),
          filePath: res.path,
        });
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
          <button
            className={showNav ? 'import-btn attn' : 'import-btn'}
            title={t('author.filesHint')}
            onClick={() =>
              setShowNav((v) => {
                const n = !v;
                try {
                  localStorage.setItem('termipod.author.showNav', n ? '1' : '0');
                } catch {
                  /* ignore */
                }
                return n;
              })
            }
          >
            {t('author.files')}
          </button>
          <button className="import-btn" onClick={() => create('markdown')}>
            <Icon name="plus" size={14} />
            {t('author.newDoc')}
          </button>
          <button className="import-btn" onClick={() => create('diagram')}>
            <Icon name="diagram" size={14} />
            {t('author.newDiagram')}
          </button>
          <button className="import-btn" onClick={() => create('canvas')}>
            <Icon name="canvas" size={14} />
            {t('author.newCanvas')}
          </button>
          <button className="import-btn" onClick={() => create('table')}>
            <Icon name="table" size={14} />
            {t('author.newTable')}
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
          <button
            className={showAgent ? 'import-btn attn' : 'import-btn'}
            title={t('author.assistantHint')}
            onClick={() => setShowAgent((v) => !v)}
          >
            {t('author.assistant')}
          </button>
        </>
      }
    >
      <div className="author-layout">
      {showNav && (
        <>
          <div className="author-nav-col" style={{ width: navW }}>
            <AuthorNav />
          </div>
          <ResizeHandle
            onResize={(dx) =>
              setNavW((w) => {
                const n = clamp(w + dx, 180, 480);
                try {
                  localStorage.setItem('termipod.author.navW', String(n));
                } catch {
                  /* ignore */
                }
                return n;
              })
            }
          />
        </>
      )}
      <div className="author-main">
      {docs.length > 0 && (
        <div className="read-tabstrip">
          {docs.map((d) => (
            <span key={d.id} className={`read-tabitem${activeId === d.id ? ' active' : ''}`}>
              <button className="read-tabitem-label" title={d.filePath ?? d.title} onClick={() => setActive(d.id)}>
                <Icon name={docKindIcon(d.kind)} size={13} className="read-tabitem-kind" />
                {d.dirty === true ? '● ' : ''}
                {d.title !== '' ? d.title : t('author.untitled')}
              </button>
              <button
                className={confirmClose === d.id ? 'read-tabitem-x danger' : 'read-tabitem-x'}
                title={confirmClose === d.id ? t('author.confirmClose') : t('read.closeTab')}
                onClick={() => closeTab(d.id)}
              >
                {confirmClose === d.id ? '✓×' : '×'}
              </button>
            </span>
          ))}
        </div>
      )}
      {active !== undefined ? (
        <div className="author-split">
          {active.kind === 'diagram' ? (
            <DiagramEditor key={active.id} doc={active} />
          ) : active.kind === 'canvas' ? (
            <CanvasEditor key={active.id} value={active.body} onChange={(v) => update(active.id, { body: v })} />
          ) : active.kind === 'table' ? (
            <Suspense fallback={<div className="muted region-pad">{t('author.loadingEditor')}</div>}>
              <TableEditor key={active.id} value={active.body} onChange={(v) => update(active.id, { body: v })} />
            </Suspense>
          ) : (
            <Editor key={active.id} doc={active} />
          )}
          {/* Agent-assist inserts prose into the body — only meaningful for a
              markdown doc; a diagram/canvas/table body is structured
              (XML/JSON) and would be corrupted by an append. */}
          {showAgent && active.kind === 'markdown' && (
            <>
              <ResizeHandle
                onResize={(dx) =>
                  setAgentW((w) => {
                    const n = clamp(w - dx, 280, 720);
                    try {
                      localStorage.setItem('termipod.author.agentW', String(n));
                    } catch {
                      /* ignore */
                    }
                    return n;
                  })
                }
              />
              <div className="author-agent" style={{ width: agentW }}>
                <AgentCompanion
                  storageKey="termipod.author.agent"
                  context={{
                    label: active.title !== '' ? active.title : t('author.untitled'),
                    build: () =>
                      `I'm writing a document titled "${active.title}". Current draft:\n\n${active.body}`,
                  }}
                  onInsert={(text) =>
                    update(active.id, { body: active.body.trimEnd() === '' ? text : `${active.body.trimEnd()}\n\n${text}` })
                  }
                />
              </div>
            </>
          )}
        </div>
      ) : (
        <div className="author-empty muted">
          <p>{t('author.noDocs')}</p>
          <button className="primary" onClick={() => create('markdown')}>
            + {t('author.newDoc')}
          </button>
        </div>
      )}
      </div>
      </div>
    </WorkbenchSurface>
  );
}
