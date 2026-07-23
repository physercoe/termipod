import { type Extension } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import { HighlightStyle, LanguageDescription, syntaxHighlighting } from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { tags as tk } from '@lezer/highlight';

/// Shared CodeMirror 6 theming for the Inspect (J3) surface — the syntax palette,
/// the editor theme, and the lazy language resolver. Extracted from `CodeView`
/// so the two-blob **compare** viewer (`@codemirror/merge`) highlights and themes
/// identically without pulling the whole `CodeView` component into its chunk.
/// All colors route through the semantic `--syntax-*` / `--color-*` tokens
/// defined in `01-base-shell.css` (theme-aware, on-token — no phantom vars).

// Lezer highlight tag → semantic syntax token. Restrained palette per the
// single-accent discipline.
export const codeHighlight = HighlightStyle.define([
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

export const codeTheme = EditorView.theme({
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
  // Merge-view change tinting → success/danger tokens (both themes).
  '.cm-changedLine': { backgroundColor: 'color-mix(in srgb, var(--success) 14%, transparent)' },
  '.cm-deletedChunk': { backgroundColor: 'color-mix(in srgb, var(--danger) 12%, transparent)' },
  '.cm-changedText': { backgroundColor: 'color-mix(in srgb, var(--success) 26%, transparent)' },
  '.cm-deletedChunk .cm-deletedText': { backgroundColor: 'color-mix(in srgb, var(--danger) 30%, transparent)' },
  '.cm-merge-gap': { backgroundColor: 'var(--surface)' },
});

export const highlightExtension: Extension = syntaxHighlighting(codeHighlight, { fallback: true });

/// Resolve a language mode from an explicit name (a `lang` override) or the file
/// name, loading its grammar lazily. Null when unknown (plain-text view).
export async function resolveLang(filename?: string, name?: string): Promise<{ ext: Extension; label: string } | null> {
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
