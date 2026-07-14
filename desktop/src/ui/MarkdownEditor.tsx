import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react';
import { EditorState, EditorSelection, RangeSetBuilder } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  keymap,
  drawSelection,
  highlightActiveLine,
  placeholder as cmPlaceholder,
  ViewPlugin,
  type ViewUpdate,
  WidgetType,
} from '@codemirror/view';
import { history, historyKeymap, defaultKeymap, indentWithTab } from '@codemirror/commands';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { HighlightStyle, syntaxHighlighting, indentOnInput, syntaxTree } from '@codemirror/language';
import { tags as tk } from '@lezer/highlight';
import { loadNoteImage, NOTE_ATT_SCHEME } from '../state/attachments';

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

// ---- Obsidian-style inline image preview ----------------------------------
//
// Collapse an `![alt](url)` image node into the rendered image, EXCEPT when the
// selection is inside it (so you can still edit the markdown). `termipod-att://`
// refs (de-inlined note images, Layer 1) resolve to a blob object URL; data/http
// srcs paint directly. This is the "source mode" counterpart to the WYSIWYG
// editor — you see the picture, not a base64 blob or a bare ref.
class ImageWidget extends WidgetType {
  private revoke: (() => void) | null = null;
  constructor(
    readonly url: string,
    readonly alt: string,
  ) {
    super();
  }
  eq(o: ImageWidget): boolean {
    return o.url === this.url && o.alt === this.alt;
  }
  toDOM(): HTMLElement {
    const wrap = document.createElement('span');
    wrap.className = 'cm-md-img';
    const img = document.createElement('img');
    img.alt = this.alt;
    if (this.url.startsWith(NOTE_ATT_SCHEME)) {
      let dead = false;
      void loadNoteImage(this.url).then((b) => {
        if (dead || b === null) return;
        const obj = URL.createObjectURL(b);
        img.src = obj;
        this.revoke = () => URL.revokeObjectURL(obj);
      });
      // If destroyed before the blob resolves, cancel the src assignment.
      const prev = this.revoke;
      this.revoke = () => {
        dead = true;
        if (prev !== null) prev();
      };
    } else {
      img.src = this.url;
    }
    wrap.appendChild(img);
    return wrap;
  }
  destroy(): void {
    if (this.revoke !== null) this.revoke();
  }
  ignoreEvent(): boolean {
    return false;
  }
}

function buildImageDecorations(view: EditorView): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>();
  const sel = view.state.selection.main;
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({
      from,
      to,
      enter: (node) => {
        if (node.name !== 'Image') return;
        // Keep the markdown visible while the cursor/selection is on it.
        if (sel.from <= node.to && sel.to >= node.from) return;
        const text = view.state.sliceDoc(node.from, node.to);
        const m = /^!\[([^\]]*)\]\(\s*(\S+?)(?:\s+["'][^)]*["'])?\s*\)$/.exec(text);
        if (m === null) return;
        builder.add(node.from, node.to, Decoration.replace({ widget: new ImageWidget(m[2], m[1]) }));
      },
    });
  }
  return builder.finish();
}

const imagePreview = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    constructor(view: EditorView) {
      this.decorations = buildImageDecorations(view);
    }
    update(u: ViewUpdate): void {
      if (u.docChanged || u.viewportChanged || u.selectionSet) {
        this.decorations = buildImageDecorations(u.view);
      }
    }
  },
  { decorations: (v) => v.decorations },
);

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
          imagePreview,
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
