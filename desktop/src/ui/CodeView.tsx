import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { Compartment, EditorSelection, EditorState } from '@codemirror/state';
import { EditorView, keymap, drawSelection, highlightActiveLine, highlightActiveLineGutter, lineNumbers } from '@codemirror/view';
import { history, historyKeymap, defaultKeymap, indentWithTab } from '@codemirror/commands';
import { bracketMatching, codeFolding, foldGutter, foldKeymap, indentOnInput } from '@codemirror/language';
import { gotoLine, highlightSelectionMatches, openSearchPanel, search, searchKeymap } from '@codemirror/search';
import { Icon } from './Icon';
import { useT } from '../i18n';
import { codeTheme, highlightExtension, resolveLang } from './codeTheme';

/// The Inspect (J3) code viewer — the workhorse the W1 shell mounts, and the base
/// that W2's two-blob diff builds on. CodeMirror 6 (already shipped for the
/// Markdown editor), read-only by default with an edit toggle; language modes are
/// pulled lazily from `@codemirror/language-data` (Vite code-splits each grammar,
/// so nothing but the requested language lands), plus a search panel, fold
/// gutter, go-to-line, soft-wrap toggle, and copy. Deliberately NOT a
/// generalization of `MarkdownEditor` (that one is md-specific by design); this
/// is a sibling that shares only the CSS-token theming approach.

export interface CodeViewHandle {
  /// Move the caret to the start of a 1-based line, center it, and focus — the
  /// trace-lens / outline jump target.
  revealLine: (line: number) => void;
}

export const CodeView = forwardRef<
  CodeViewHandle,
  {
    value: string;
    onChange?: (v: string) => void;
    filename?: string;
    lang?: string;
    /// Start in edit mode (default: read-only).
    editable?: boolean;
  }
>(function CodeView({ value, onChange, filename, lang, editable = false }, ref) {
  const t = useT();
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const langComp = useRef(new Compartment()).current;
  const editComp = useRef(new Compartment()).current;
  const wrapComp = useRef(new Compartment()).current;

  const [editing, setEditing] = useState(editable);
  const [wrap, setWrap] = useState(false);
  const [langLabel, setLangLabel] = useState<string>('text');
  const [copied, setCopied] = useState(false);

  useImperativeHandle(
    ref,
    () => ({
      revealLine: (line) => {
        const v = viewRef.current;
        if (v === null) return;
        const n = Math.max(1, Math.min(line, v.state.doc.lines));
        const info = v.state.doc.line(n);
        v.dispatch({ selection: EditorSelection.cursor(info.from), effects: EditorView.scrollIntoView(info.from, { y: 'center' }) });
        v.focus();
      },
    }),
    [],
  );

  // Build the editor once; the parent remounts per tab via key={tab.id}.
  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLineGutter(),
          foldGutter(),
          codeFolding(),
          history(),
          drawSelection(),
          highlightActiveLine(),
          bracketMatching(),
          indentOnInput(),
          search({ top: true }),
          highlightSelectionMatches(),
          highlightExtension,
          langComp.of([]),
          wrapComp.of([]),
          editComp.of(EditorState.readOnly.of(!editable)),
          keymap.of([indentWithTab, ...searchKeymap, ...foldKeymap, ...historyKeymap, ...defaultKeymap]),
          codeTheme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged && !u.state.readOnly) onChangeRef.current?.(u.state.doc.toString());
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

  // Load the language grammar (lazy) when the file/lang changes.
  useEffect(() => {
    let cancelled = false;
    void resolveLang(filename, lang).then((r) => {
      const v = viewRef.current;
      if (cancelled || v === null) return;
      v.dispatch({ effects: langComp.reconfigure(r?.ext ?? []) });
      setLangLabel(r?.label ?? 'text');
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filename, lang]);

  // Reconcile edit-mode + wrap toggles into the live editor.
  useEffect(() => {
    viewRef.current?.dispatch({ effects: editComp.reconfigure(EditorState.readOnly.of(!editing)) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);
  useEffect(() => {
    viewRef.current?.dispatch({ effects: wrapComp.reconfigure(wrap ? EditorView.lineWrapping : []) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wrap]);

  // Reconcile an external value change (a re-read, an agent write) into the doc,
  // dispatching only the changed span so selection/undo stay sane (the
  // MarkdownEditor precedent, #322).
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

  async function copyAll(): Promise<void> {
    try {
      await navigator.clipboard.writeText(viewRef.current?.state.doc.toString() ?? value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard denied — no-op */
    }
  }

  return (
    <div className="code-view-root">
      <div className="code-view-bar">
        <span className="code-view-lang muted small">{langLabel}</span>
        <span className="spacer" />
        <button className="icon-btn" title={t('inspect.search')} onClick={() => viewRef.current && openSearchPanel(viewRef.current)}>
          <Icon name="search" size={15} />
        </button>
        <button className="icon-btn" title={t('inspect.gotoLine')} onClick={() => viewRef.current && gotoLine(viewRef.current)}>
          <Icon name="crosshair" size={15} />
        </button>
        <button className={`icon-btn${wrap ? ' active' : ''}`} title={t('inspect.wrap')} onClick={() => setWrap((w) => !w)}>
          <Icon name="wrap" size={15} />
        </button>
        {onChange !== undefined && (
          <button className={`icon-btn${editing ? ' active' : ''}`} title={editing ? t('inspect.viewOnly') : t('inspect.edit')} onClick={() => setEditing((e) => !e)}>
            <Icon name={editing ? 'eye' : 'pen'} size={15} />
          </button>
        )}
        <button className="icon-btn" title={t('inspect.copy')} onClick={() => void copyAll()}>
          <Icon name={copied ? 'check' : 'copy'} size={15} />
        </button>
      </div>
      <div className="code-view-host" ref={hostRef} />
    </div>
  );
});
