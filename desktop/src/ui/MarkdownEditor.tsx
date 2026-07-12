import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react';
import { EditorState, EditorSelection } from '@codemirror/state';
import {
  EditorView,
  keymap,
  drawSelection,
  highlightActiveLine,
  placeholder as cmPlaceholder,
} from '@codemirror/view';
import { history, historyKeymap, defaultKeymap, indentWithTab } from '@codemirror/commands';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { HighlightStyle, syntaxHighlighting, indentOnInput } from '@codemirror/language';
import { tags as tk } from '@lezer/highlight';

/// A real Markdown editor built on CodeMirror 6 — the source pane of the Author
/// workspace, replacing the plain <textarea>. It gives live syntax highlighting
/// (headings scale up, bold/italic/links/code styled), soft line-wrapping, undo
/// history, and Obsidian-style shortcuts (Cmd/Ctrl-B, -I). The theme is expressed
/// in our CSS tokens (var(--…)) so it follows light/dark automatically.
///
/// The parent renders the formatting toolbar + view-mode switch and drives
/// wrapping/prefixing through the imperative handle below (so the buttons act on
/// the real selection). Value is controlled: an external change (switching docs,
/// an agent insertion) is reconciled into the document without a feedback loop.

export interface MarkdownEditorHandle {
  wrap: (before: string, after?: string) => void;
  linePrefix: (prefix: string) => void;
  focus: () => void;
}

// Markdown token → style, in CSS-token colors so it's theme-aware.
const mdHighlight = HighlightStyle.define([
  { tag: tk.heading1, fontSize: '1.5em', fontWeight: '700', color: 'var(--text)' },
  { tag: tk.heading2, fontSize: '1.3em', fontWeight: '700', color: 'var(--text)' },
  { tag: tk.heading3, fontSize: '1.15em', fontWeight: '600', color: 'var(--text)' },
  { tag: [tk.heading4, tk.heading5, tk.heading6], fontWeight: '600', color: 'var(--text)' },
  { tag: tk.strong, fontWeight: '700', color: 'var(--text)' },
  { tag: tk.emphasis, fontStyle: 'italic' },
  { tag: tk.strikethrough, textDecoration: 'line-through' },
  { tag: [tk.link, tk.url], color: 'var(--accent)' },
  { tag: tk.monospace, fontFamily: 'var(--font-mono, monospace)', color: 'var(--accent-strong, var(--accent))' },
  { tag: tk.quote, color: 'var(--text-secondary)', fontStyle: 'italic' },
  { tag: [tk.list, tk.processingInstruction], color: 'var(--accent)' },
  { tag: tk.meta, color: 'var(--text-muted)' },
]);

const cmTheme = EditorView.theme({
  '&': { color: 'var(--text)', backgroundColor: 'transparent', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'inherit', lineHeight: '1.7' },
  '.cm-content': {
    fontFamily: 'var(--font-mono, ui-monospace, monospace)',
    fontSize: '14px',
    padding: '16px 18px',
    caretColor: 'var(--accent)',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--accent)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent) 26%, transparent)',
  },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
  '.cm-placeholder': { color: 'var(--text-muted)' },
});

function wrapSel(view: EditorView, before: string, after: string): void {
  const { state } = view;
  const tr = state.changeByRange((range) => {
    const text = state.sliceDoc(range.from, range.to);
    return {
      changes: { from: range.from, to: range.to, insert: `${before}${text}${after}` },
      range: EditorSelection.range(range.from + before.length, range.from + before.length + text.length),
    };
  });
  view.dispatch(state.update(tr, { scrollIntoView: true, userEvent: 'input' }));
}

function prefixLines(view: EditorView, prefix: string): void {
  const { state } = view;
  const changes: { from: number; insert: string }[] = [];
  const seen = new Set<number>();
  for (const range of state.selection.ranges) {
    let pos = range.from;
    for (;;) {
      const line = state.doc.lineAt(pos);
      if (!seen.has(line.number)) {
        seen.add(line.number);
        changes.push({ from: line.from, insert: prefix });
      }
      if (line.to >= range.to) break;
      pos = line.to + 1;
    }
  }
  view.dispatch(state.update({ changes, userEvent: 'input' }));
}

export const MarkdownEditor = forwardRef<MarkdownEditorHandle, {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}>(function MarkdownEditor({ value, onChange, placeholder }, ref) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useImperativeHandle(
    ref,
    () => ({
      wrap: (before, after) => {
        const v = viewRef.current;
        if (v !== null) {
          wrapSel(v, before, after ?? before);
          v.focus();
        }
      },
      linePrefix: (prefix) => {
        const v = viewRef.current;
        if (v !== null) {
          prefixLines(v, prefix);
          v.focus();
        }
      },
      focus: () => viewRef.current?.focus(),
    }),
    [],
  );

  // Create the editor once (the parent remounts per document via key={doc.id}).
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
          indentOnInput(),
          EditorView.lineWrapping,
          markdown({ base: markdownLanguage }),
          syntaxHighlighting(mdHighlight),
          cmPlaceholder(placeholder ?? ''),
          keymap.of([
            { key: 'Mod-b', run: (v) => (wrapSel(v, '**', '**'), true) },
            { key: 'Mod-i', run: (v) => (wrapSel(v, '*', '*'), true) },
            { key: 'Mod-e', run: (v) => (wrapSel(v, '`', '`'), true) },
            indentWithTab,
            ...historyKeymap,
            ...defaultKeymap,
          ]),
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

  // Reconcile an external value change (agent insert, undo from elsewhere) into
  // the doc. The editor's own edits set value === current, so this is a no-op then.
  useEffect(() => {
    const view = viewRef.current;
    if (view === null) return;
    const cur = view.state.doc.toString();
    if (value !== cur) {
      view.dispatch({ changes: { from: 0, to: cur.length, insert: value } });
    }
  }, [value]);

  return <div className="md-editor" ref={hostRef} />;
});
