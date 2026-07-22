import { useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { EditorState } from '@codemirror/state';
import { EditorView, keymap, drawSelection, highlightActiveLine, placeholder as cmPlaceholder } from '@codemirror/view';
import { history, historyKeymap, defaultKeymap, indentWithTab } from '@codemirror/commands';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { toast } from '../state/toast';
import { useDocuments, type Doc } from '../state/documents';
import { figureBySpec, FigureRenderError, renderFigure } from '../state/figures';
import { Icon } from '../ui/Icon';

/// The J2 Author **figure** editor — a split source ↔ live-preview surface over
/// the `state/figures` renderer registry (plan §A3). The left pane is a plain
/// CodeMirror source editor; the right pane debounce-renders the source to SVG
/// through the doc's spec renderer (mermaid / graphviz / vega-lite). A render
/// error surfaces inline (message + source line where the tool reports one) and
/// the last good SVG is kept, so a mid-edit syntax error never blanks the view.
///
/// Export SVG / PNG mirror the affordance every figure spec shares: SVG is the
/// renderer's own output; PNG rasterizes it at 2× via an offscreen canvas.

/// A trailing-debounced mirror (same pattern as the Markdown preview, #311): a
/// typing burst coalesces into one render instead of re-parsing per keystroke.
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(id);
  }, [value, ms]);
  return v;
}

const cmTheme = EditorView.theme({
  '&': { color: 'var(--text)', backgroundColor: 'transparent', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--font-mono, ui-monospace, monospace)', lineHeight: '1.6' },
  '.cm-content': { fontSize: '13px', padding: '14px 16px', caretColor: 'var(--accent)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--accent)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent) 26%, transparent)',
  },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
  '.cm-placeholder': { color: 'var(--text-muted)' },
});

/// A minimal plain-text CodeMirror source pane (no language mode — the figure
/// specs are mermaid/dot/JSON, none of which is markdown). Controlled: an
/// external body change reconciles via a minimal diff, so undo stays granular
/// and the cursor survives (the pattern proven in MarkdownEditor, #322).
function SourceEditor({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          history(),
          drawSelection(),
          highlightActiveLine(),
          EditorView.lineWrapping,
          cmPlaceholder(placeholder ?? ''),
          keymap.of([indentWithTab, ...historyKeymap, ...defaultKeymap]),
          cmTheme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current(u.state.doc.toString());
          }),
        ],
      }),
    });
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const view = viewRef.current;
    if (view === null) return;
    const cur = view.state.doc.toString();
    if (value === cur) return;
    let from = 0;
    const minLen = Math.min(cur.length, value.length);
    while (from < minLen && cur[from] === value[from]) from++;
    let toCur = cur.length;
    let toNew = value.length;
    while (toCur > from && toNew > from && cur[toCur - 1] === value[toNew - 1]) {
      toCur--;
      toNew--;
    }
    view.dispatch({ changes: { from, to: toCur, insert: value.slice(from, toNew) } });
  }, [value]);

  return <div className="md-editor figure-source" ref={hostRef} />;
}

/// Extract the intended pixel size of a rendered figure SVG — its explicit
/// `width`/`height`, else the `viewBox`, else a default — so `svgToPngBase64` can
/// size the output canvas. (It formerly also injected explicit width/height into
/// the `<svg>` to work around WebKit reporting `naturalWidth === 0` for
/// viewBox-only SVGs; the Electron shell's Chromium rasterizes those via
/// `drawImage(img, 0, 0, w, h)` with no injection — §7 row 3, pinned in
/// electron/e2e/app.spec.ts.)
function svgSize(svg: string): { w: number; h: number } {
  const vb = /viewBox\s*=\s*["']\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*["']/.exec(svg);
  const wAttr = /<svg[^>]*\bwidth\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const hAttr = /<svg[^>]*\bheight\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const w = wAttr !== null ? Number(wAttr[1]) : vb !== null ? Number(vb[3]) : 800;
  const h = hAttr !== null ? Number(hAttr[1]) : vb !== null ? Number(vb[4]) : 600;
  return { w, h };
}

/// Rasterize an SVG string to a base64 PNG at `scale`× via an offscreen canvas.
async function svgToPngBase64(svg: string, scale = 2): Promise<string> {
  const { w, h } = svgSize(svg);
  const url = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  const img = new Image();
  await new Promise<void>((resolve, reject) => {
    img.onload = () => resolve();
    img.onerror = () => reject(new Error('svg decode failed'));
    img.src = url;
  });
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(w * scale));
  canvas.height = Math.max(1, Math.round(h * scale));
  const ctx = canvas.getContext('2d');
  if (ctx === null) throw new Error('no 2d context');
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0, w, h);
  return canvas.toDataURL('image/png').split(',')[1] ?? '';
}

export function FigureEditor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const update = useDocuments((s) => s.update);
  const [svg, setSvg] = useState<string | null>(null);
  const [err, setErr] = useState<{ msg: string; line?: number } | null>(null);
  const [rendering, setRendering] = useState(false);
  const body = useDebounced(doc.body, 300);
  const row = doc.spec !== undefined ? figureBySpec(doc.spec) : undefined;
  const baseName = (doc.title !== '' ? doc.title : 'figure').replace(/\.[^.]+$/, '').replace(/[^\w.-]+/g, '-');

  useEffect(() => {
    if (doc.spec === undefined) return;
    let alive = true;
    setRendering(true);
    void renderFigure(doc.spec, body).then(
      (out) => {
        if (!alive) return;
        setSvg(out);
        setErr(null);
        setRendering(false);
      },
      (e: unknown) => {
        if (!alive) return;
        // Keep the last good SVG; only swap in the error strip.
        setErr({ msg: e instanceof Error ? e.message : String(e), line: e instanceof FigureRenderError ? e.line : undefined });
        setRendering(false);
      },
    );
    return () => {
      alive = false;
    };
  }, [doc.spec, body]);

  async function exportSvg(): Promise<void> {
    if (svg === null || !isShell()) return;
    try {
      // `doc_save` returns null when the user cancels the dialog — no toast then.
      const path = await invoke<string | null>('doc_save', { content: svg, defaultName: `${baseName}.svg` });
      if (path !== null) toast.success(t('figure.exported'));
    } catch (e) {
      toast.error(`${t('figure.exportFailed')}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function exportPng(): Promise<void> {
    if (svg === null || !isShell()) return;
    try {
      const base64 = await svgToPngBase64(svg, 2);
      const path = await invoke<string | null>('save_image_as', { defaultName: `${baseName}.png`, base64 });
      if (path !== null) toast.success(t('figure.exported'));
    } catch (e) {
      toast.error(`${t('figure.exportFailed')}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  return (
    <div className="figure-editor">
      <div className="figure-bar">
        <span className="figure-badge">
          <Icon name="figure" size={13} />
          {row !== undefined ? t(row.labelKey) : (doc.spec ?? '')}
        </span>
        <span className="spacer" />
        {rendering && <span className="muted small">{t('figure.rendering')}</span>}
        {isShell() && (
          <>
            <button className="import-btn" disabled={svg === null} onClick={() => void exportSvg()}>
              {t('figure.exportSvg')}
            </button>
            <button className="import-btn" disabled={svg === null} onClick={() => void exportPng()}>
              {t('figure.exportPng')}
            </button>
          </>
        )}
      </div>
      <div className="figure-body">
        <div className="figure-source-pane">
          <SourceEditor
            value={doc.body}
            onChange={(v) => update(doc.id, { body: v })}
            placeholder={row?.sample ?? ''}
          />
        </div>
        <div className="figure-preview-pane">
          {svg !== null ? (
            <div className="figure-preview" dangerouslySetInnerHTML={{ __html: svg }} />
          ) : (
            !rendering && err === null && <div className="muted region-pad">{t('figure.empty')}</div>
          )}
          {err !== null && (
            <div className="figure-error">
              <Icon name="alert" size={14} />
              <span>
                {err.line !== undefined ? `${t('figure.renderError')} (line ${err.line}): ` : `${t('figure.renderError')}: `}
                {err.msg}
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
