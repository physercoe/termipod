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

// Lezer highlight tag → semantic syntax token. Inspect is deliberately richer
// than the monochrome app chrome: colour communicates grammar here, not brand.
export const codeHighlight = HighlightStyle.define([
  { tag: [tk.comment, tk.lineComment, tk.blockComment, tk.docComment], color: 'var(--syntax-comment)' },
  { tag: [tk.keyword, tk.controlKeyword, tk.operatorKeyword, tk.moduleKeyword, tk.definitionKeyword, tk.modifier, tk.self], color: 'var(--syntax-keyword)', fontWeight: '500' },
  { tag: [tk.string, tk.docString, tk.character, tk.attributeValue], color: 'var(--syntax-string)' },
  { tag: [tk.regexp, tk.escape, tk.special(tk.string)], color: 'var(--syntax-regexp)' },
  { tag: [tk.number, tk.integer, tk.float], color: 'var(--syntax-number)' },
  { tag: [tk.bool, tk.null, tk.atom, tk.constant(tk.variableName)], color: 'var(--syntax-constant)' },
  { tag: [tk.typeName, tk.className, tk.namespace], color: 'var(--syntax-type)' },
  { tag: tk.tagName, color: 'var(--syntax-tag)' },
  { tag: [tk.function(tk.variableName), tk.function(tk.propertyName), tk.macroName], color: 'var(--syntax-func)' },
  { tag: [tk.propertyName, tk.definition(tk.propertyName)], color: 'var(--syntax-property)' },
  { tag: tk.attributeName, color: 'var(--syntax-attribute)' },
  { tag: [tk.variableName, tk.definition(tk.variableName), tk.labelName], color: 'var(--syntax-name)' },
  { tag: [tk.operator, tk.punctuation, tk.bracket, tk.separator], color: 'var(--syntax-punctuation)' },
  { tag: [tk.meta, tk.annotation, tk.processingInstruction], color: 'var(--syntax-meta)' },
  { tag: tk.heading, color: 'var(--text)', fontWeight: '700' },
  { tag: tk.strong, fontWeight: '700' },
  { tag: tk.emphasis, fontStyle: 'italic' },
  { tag: [tk.link, tk.url], color: 'var(--accent-text)' },
  { tag: tk.invalid, color: 'var(--danger)' },
]);

export const codeTheme = EditorView.theme({
  '&': { color: 'var(--text)', backgroundColor: 'var(--bg)', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    overflow: 'auto',
    fontFamily: 'var(--font-mono, ui-monospace, monospace)',
    fontFeatureSettings: '"calt" 1, "liga" 1',
    lineHeight: '1.62',
  },
  '.cm-content': { fontSize: '13.5px', padding: '10px 0 24px', caretColor: 'var(--text)' },
  '.cm-line': { padding: '0 20px 0 10px' },
  '.cm-gutters': {
    backgroundColor: 'color-mix(in srgb, var(--canvas) 48%, var(--bg))',
    color: 'color-mix(in srgb, var(--text-muted) 80%, transparent)',
    borderRight: 'var(--hairline) solid var(--border)',
  },
  '.cm-lineNumbers .cm-gutterElement': { minWidth: '3.25em', padding: '0 10px 0 8px' },
  '.cm-foldGutter .cm-gutterElement': { padding: '0 6px 0 2px', color: 'var(--text-muted)' },
  '.cm-activeLineGutter': {
    backgroundColor: 'color-mix(in srgb, var(--text) 5%, transparent)',
    color: 'var(--text-secondary)',
    boxShadow: 'inset 2px 0 0 color-mix(in srgb, var(--text) 35%, transparent)',
  },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--text) 4%, transparent)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--accent)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--syntax-property) 28%, transparent)',
  },
  '.cm-matchingBracket': {
    backgroundColor: 'color-mix(in srgb, var(--syntax-type) 18%, transparent)',
    outline: '1px solid color-mix(in srgb, var(--syntax-type) 48%, transparent)',
    borderRadius: '2px',
  },
  '.cm-nonmatchingBracket': { color: 'var(--danger)', outline: '1px solid var(--danger)', borderRadius: '2px' },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--warn) 26%, transparent)', outline: '1px solid color-mix(in srgb, var(--warn) 48%, transparent)', borderRadius: '2px' },
  '.cm-searchMatch-selected': { backgroundColor: 'color-mix(in srgb, var(--syntax-property) 38%, transparent)' },
  '.cm-selectionMatch': { backgroundColor: 'color-mix(in srgb, var(--syntax-property) 13%, transparent)', borderRadius: '2px' },
  '.cm-panels': { backgroundColor: 'var(--surface)', color: 'var(--text)', borderColor: 'var(--border)', boxShadow: 'var(--sh-2)' },
  '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-gotoLine input': {
    fontFamily: 'inherit',
    backgroundColor: 'var(--input)',
    color: 'var(--text)',
    border: '1px solid var(--border)',
    borderRadius: '4px',
  },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--raised)', color: 'var(--text-muted)', border: '1px solid var(--border)', borderRadius: '3px', padding: '0 4px' },
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
