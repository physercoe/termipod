/// Reading-paper themes for the EPUB reader (#321). These are content-presentation
/// palettes injected INTO each EPUB section's own document (a separate iframe doc),
/// so they can't be design-system CSS variables — the host's custom properties
/// don't cascade across the document boundary, and they aren't app-chrome colours.
/// Kept here as literal values in a `.ts` module deliberately: they belong to the
/// rendered book, not the shell.
///
/// `default` leaves the EPUB's own colours untouched (most books are dark-on-white).
export type EpubTheme = 'default' | 'sepia' | 'night';

export const EPUB_THEMES: EpubTheme[] = ['default', 'sepia', 'night'];

/// CSS to inject into a section's <head> for the chosen theme. Uses `!important`
/// to beat the author stylesheet, mirroring the width-override approach. Returns
/// '' for `default` so nothing is forced.
export function epubThemeCss(theme: EpubTheme): string {
  if (theme === 'sepia') {
    return [
      'html, body { background: #f4ecd8 !important; color: #5b4636 !important; }',
      'a { color: #7a5230 !important; }',
    ].join('\n');
  }
  if (theme === 'night') {
    return [
      'html, body { background: #1b1b1d !important; color: #c9c7c2 !important; }',
      'a { color: #7db1d6 !important; }',
    ].join('\n');
  }
  return '';
}
