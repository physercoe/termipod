import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { toast } from '../state/toast';
import {
  bodyToFile,
  extForDoc,
  extForKind,
  fileToBody,
  kindForFile,
  seedBody,
  useDocuments,
  type Doc,
  type DocKind,
} from '../state/documents';
import { figureBySpec, FIGURES, type FigureSpec } from '../state/figures';
import { useWorkspace } from '../state/workspace';
import { NEW_BASE, uniqueWorkspacePath } from '../state/workspaceFiles';
import { AgentCompanion } from '../ui/AgentCompanion';
import { docKindIcon, Icon, type IconName } from '../ui/Icon';
import { AuthorNav } from './AuthorNav';
import { DiagramEditor } from './DiagramEditor';
import { CanvasEditor } from '../ui/CanvasEditor';
import { Markdown } from '../ui/Markdown';
// The table/database grid is only pulled in when a table doc is opened.
const TableEditor = lazy(() => import('../ui/TableEditor').then((m) => ({ default: m.TableEditor })));
// The figure editor pulls in a renderer library (mermaid/graphviz/vega) on first
// use of a spec — split it (and them) out of the entry chunk.
const FigureEditor = lazy(() => import('./FigureEditor').then((m) => ({ default: m.FigureEditor })));
// Excalidraw is a heavy interactive editor (its own React tree + fonts) — split
// it out so it loads only when a sketch doc is opened, never at app boot.
const ExcalidrawEditor = lazy(() => import('./ExcalidrawEditor').then((m) => ({ default: m.ExcalidrawEditor })));
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

/// A trailing-debounced mirror of a value. The split/read Markdown preview
/// re-parses the WHOLE document (react-markdown + rehype-highlight + KaTeX) on
/// every editor keystroke — a main-thread stall on large docs (#311). Debouncing
/// the preview's input coalesces a typing burst into one parse (~250ms, in step
/// with the documents store's 400ms trailing persistence) while the editor
/// itself keeps the live value.
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(id);
  }, [value, ms]);
  return v;
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

function baseName(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? '';
}

/// Strip a single enclosing ```` ``` ```` fence from an agent reply so a figure
/// body stays raw source. Agents answer "a sequence diagram of X" with a fenced
/// ```` ```mermaid … ``` ```` block; the figure renderer wants the inside only.
/// A reply that isn't a lone fence passes through untouched.
function unfence(text: string): string {
  const m = /^\s*```[\w-]*\n([\s\S]*?)\n```\s*$/.exec(text.trim());
  return m !== null ? m[1] : text;
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
  // The preview renders the debounced body, not the per-keystroke one (#311).
  const previewBody = useDebounced(doc.body, 250);

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
          {t.plural('author.words', words)}
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
                {previewBody.trim() ? (
                  <Markdown text={previewBody} />
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
  const folder = useWorkspace((s) => s.folder);
  const touchWs = useWorkspace((s) => s.touch);
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
  // The New-figure spec dropdown (Mermaid · Graphviz · Vega-Lite, driven by the
  // registry). Open/closed only; picking a spec creates the doc and closes it.
  const [figMenu, setFigMenu] = useState(false);

  const active = docs.find((d) => d.id === activeId);
  const tauri = isShell();

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
      // The disk format follows the target file's extension (a table re-saves as
      // CSV if linked to a .csv, else the lossless .json default). A figure saves
      // as its spec's extension (`extForDoc`).
      const ext = active.filePath !== undefined ? extOf(active.filePath) : extForDoc(active);
      const content = bodyToFile(active.kind, active.body, ext, t('table.colName'));
      if (active.filePath !== undefined) {
        await invoke('doc_write', { path: active.filePath, content });
        markSaved(active.id, active.filePath);
      } else {
        const name = (active.title !== '' ? active.title : 'document').replace(/[^\w.-]+/g, '-');
        const path = await invoke<string | null>('doc_save', {
          content,
          defaultName: `${name}.${extForDoc(active)}`,
        });
        if (path !== null) markSaved(active.id, path, baseName(path));
      }
      toast.success(t('author.saved'));
    } catch (e) {
      // Was silently swallowed — a failed disk write left the ● dirty dot with no
      // explanation. Surface it (#312/#315); the doc stays dirty so a retry works.
      toast.error(`${t('author.saveFailed')}: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  }

  // Cmd/Ctrl+S saves the active document to its linked file (browser build / no
  // active doc: a no-op, guarded inside onSave). A ref keeps the handler pointed
  // at the latest closure without re-subscribing the listener each keystroke.
  const saveRef = useRef(onSave);
  saveRef.current = onSave;
  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === 's') {
        e.preventDefault();
        void saveRef.current();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Create a new document. With a workspace folder open (desktop), it's
  // materialized as a real file IN that folder — so it appears in the tree and
  // round-trips on Save — instead of a device-local draft disconnected from the
  // workspace (director report: "the file is not added to the workspace"). With
  // no folder open, or in the browser build, it stays an in-memory draft.
  // A figure carries a `spec` (which renderer): its seed body and on-disk
  // extension come from the registry row, not the bare kind.
  async function createDoc(kind: DocKind, spec?: FigureSpec): Promise<void> {
    const row = spec !== undefined ? figureBySpec(spec) : undefined;
    const seed: Partial<Doc> = spec !== undefined ? { spec, body: row?.sample ?? '' } : {};
    if (!tauri || folder === null) {
      create(kind, seed);
      return;
    }
    setBusy(true);
    try {
      const ext = row?.ext ?? extForKind(kind);
      const body = seed.body ?? seedBody(kind);
      const path = await uniqueWorkspacePath(folder, NEW_BASE[kind], ext);
      await invoke('doc_write', { path, content: bodyToFile(kind, body, ext, t('table.colName')) });
      create(kind, { ...seed, title: baseName(path), body, filePath: path });
      touchWs();
    } catch {
      // Permissions / race — still give the user a document, just in-memory.
      create(kind, seed);
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
        const ext = extOf(res.path);
        const { kind, spec } = kindForFile(ext, res.content);
        create(kind, {
          title: baseName(res.path),
          body: fileToBody(kind, res.content, ext, t('table.colName')),
          filePath: res.path,
          spec,
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
          <button className="import-btn" disabled={busy} onClick={() => void createDoc('markdown')}>
            <Icon name="plus" size={14} />
            {t('author.newDoc')}
          </button>
          <button className="import-btn" disabled={busy} onClick={() => void createDoc('diagram')}>
            <Icon name="diagram" size={14} />
            {t('author.newDiagram')}
          </button>
          <button className="import-btn" disabled={busy} onClick={() => void createDoc('canvas')}>
            <Icon name="canvas" size={14} />
            {t('author.newCanvas')}
          </button>
          <button className="import-btn" disabled={busy} onClick={() => void createDoc('table')}>
            <Icon name="table" size={14} />
            {t('author.newTable')}
          </button>
          <button className="import-btn" disabled={busy} onClick={() => void createDoc('excalidraw')}>
            <Icon name="sketch" size={14} />
            {t('author.newExcalidraw')}
          </button>
          <div className="author-figbtn">
            <button
              className="import-btn"
              disabled={busy}
              aria-haspopup="menu"
              aria-expanded={figMenu}
              onClick={() => setFigMenu((v) => !v)}
            >
              <Icon name="figure" size={14} />
              {t('author.newFigure')}
              <Icon name="chevron-down" size={12} />
            </button>
            {figMenu && (
              <>
                <div className="author-figmenu-scrim" onClick={() => setFigMenu(false)} />
                <div className="author-figmenu" role="menu">
                  {FIGURES.map((f) => (
                    <button
                      key={f.spec}
                      role="menuitem"
                      className="author-figmenu-item"
                      onClick={() => {
                        setFigMenu(false);
                        void createDoc('figure', f.spec);
                      }}
                    >
                      {t(f.labelKey)}
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>
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
        <div className="read-tabstrip" role="tablist" aria-label={t('author.docTabs')}>
          {docs.map((d) => (
            <span key={d.id} role="presentation" className={`read-tabitem${activeId === d.id ? ' active' : ''}`}>
              <button
                role="tab"
                aria-selected={activeId === d.id}
                tabIndex={activeId === d.id ? 0 : -1}
                className="read-tabitem-label"
                title={d.filePath ?? d.title}
                onClick={() => setActive(d.id)}
              >
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
          ) : active.kind === 'figure' ? (
            <Suspense fallback={<div className="muted region-pad">{t('author.loadingEditor')}</div>}>
              <FigureEditor key={active.id} doc={active} />
            </Suspense>
          ) : active.kind === 'excalidraw' ? (
            <Suspense fallback={<div className="muted region-pad">{t('author.loadingEditor')}</div>}>
              <ExcalidrawEditor key={active.id} doc={active} />
            </Suspense>
          ) : (
            <Editor key={active.id} doc={active} />
          )}
          {/* The agent panel is available for every document kind. Its
              "insert reply" affordance appends prose to a markdown body and
              REPLACES a figure body (a figure is a single spec source, so an
              append would corrupt it — the agent answers with the whole diagram).
              A diagram/canvas/table body is structured (XML/JSON) and has no safe
              text insert, so for those the panel is read/assist-only. */}
          {showAgent && (
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
                      active.kind === 'markdown'
                        ? `I'm writing a document titled "${active.title}". Current draft:\n\n${active.body}`
                        : active.kind === 'figure'
                          ? `I'm authoring a ${active.spec ?? 'figure'} diagram titled "${active.title}", rendered from the \`\`\`${active.spec ?? ''}\`\`\` fenced syntax. Reply with ONLY the ${active.spec ?? 'figure'} source (optionally in a fenced block). Current source:\n\n${active.body}`
                          : `I'm editing a ${active.kind} document titled "${active.title}"${
                              active.filePath !== undefined ? ` (file: ${active.filePath})` : ''
                            }.`,
                  }}
                  onInsert={
                    active.kind === 'markdown'
                      ? (text) =>
                          update(active.id, {
                            body: active.body.trimEnd() === '' ? text : `${active.body.trimEnd()}\n\n${text}`,
                          })
                      : active.kind === 'figure'
                        ? (text) => update(active.id, { body: unfence(text) })
                        : undefined
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
