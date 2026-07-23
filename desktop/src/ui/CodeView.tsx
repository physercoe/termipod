import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { Compartment, EditorSelection, EditorState, type Extension } from '@codemirror/state';
import { EditorView, keymap, drawSelection, highlightActiveLine, highlightActiveLineGutter, lineNumbers } from '@codemirror/view';
import { history, historyKeymap, defaultKeymap, indentWithTab } from '@codemirror/commands';
import {
  HighlightStyle,
  LanguageDescription,
  bracketMatching,
  codeFolding,
  foldGutter,
  foldKeymap,
  indentOnInput,
  syntaxHighlighting,
} from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { gotoLine, highlightSelectionMatches, openSearchPanel, search, searchKeymap } from '@codemirror/search';
import { tags as tk } from '@lezer/highlight';
import { Icon } from './Icon';
import { useT } from '../i18n';

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

// Lezer highlight tag → semantic syntax token (theme-aware, defined in
// 01-base-shell.css). Restrained palette per the single-accent discipline.
const codeHighlight = HighlightStyle.define([
  { tag: [tk.keyword, tk.controlKeyword, tk.operatorKeyword, tk.moduleKeyword, tk.definitionKeyword], color: 'var(--syntax-keyword)', fontWeight: '600' },
  { tag: [tk.string, tk.special(tk.string), tk.regexp], color: 'var(--syntax-string)' },
  { tag: [tk.comment, tk.lineComment, tk.blockComment, tk.docComment], color: 'var(--syntax-comment)', fontStyle: 'italic' },
  { tag: [tk.number, tk.integer, tk.float, tk.bool, tk.null], color: 'var(--syntax-number)' },
  { tag: [tk.typeName, tk.className, tk.namespace, tk.tagName], color: 'var(--syntax-type)' },
  { tag: [tk.function(tk.variableName), tk.function(tk.propertyName), tk.macroName], color: 'var(--syntax-func)' },
  { tag: [tk.variableName, tk.propertyName, tk.attributeName, tk.definition(tk.variableName)], color: 'var(--syntax-name)' },
  { tag: [tk.operator, tk.punctuation, tk.bracket, tk.separator], color: 'var(--text-secondary)' },
  { tag: [tk.meta, tk.processingInstruction], color: 'var(--text-muted)' },
  { tag: tk.heading, color: 'var(--text)', fontWeight: '700' },
  { tag: [tk.link, tk.url], color: 'var(--accent-text)' },
  { tag: tk.invalid, color: 'var(--danger)' },
]);

const codeTheme = EditorView.theme({
  '&': { color: 'var(--text)', backgroundColor: 'transparent', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--font-mono, ui-monospace, monospace)', lineHeight: '1.55' },
  '.cm-content': { fontSize: '13px', padding: '8px 0' },
  '.cm-gutters': { backgroundColor: 'transparent', color: 'var(--text-muted)', border: 'none' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--text)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--accent)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent) 24%, transparent)',
  },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--warn) 30%, transparent)', borderRadius: '2px' },
  '.cm-searchMatch-selected': { backgroundColor: 'color-mix(in srgb, var(--accent) 42%, transparent)' },
  '.cm-selectionMatch': { backgroundColor: 'color-mix(in srgb, var(--accent) 14%, transparent)' },
  '.cm-panels': { backgroundColor: 'var(--surface)', color: 'var(--text)', borderColor: 'var(--border)' },
  '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-gotoLine input': {
    fontFamily: 'inherit',
    backgroundColor: 'var(--input)',
    color: 'var(--text)',
    border: '1px solid var(--border)',
    borderRadius: '4px',
  },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--raised)', color: 'var(--text-muted)', border: '1px solid var(--border)' },
});

// Resolve a language mode from an explicit name (a `lang` override) or the file
// name, loading its grammar lazily. Null when unknown (plain-text view).
async function resolveLang(filename?: string, name?: string): Promise<{ ext: Extension; label: string } | null> {
  let desc: LanguageDescription | null = null;
  if (name !== undefined && name !== '' && name !== 'auto') desc = LanguageDescription.matchLanguageName(languages, name, true);
  if (desc === null && filename !== undefined && filename !== '') desc = LanguageDescription.matchFilename(languages, filename);
  if (desc === null) return null;
  try {
    const support = await desc.load();
    return { ext: support, label: desc.name };
  } catch {
    return null;
  }
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
          syntaxHighlighting(codeHighlight, { fallback: true }),
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
