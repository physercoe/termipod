/// ANSI-SGR → styled spans for the Inspect (J3) log viewer (plan §4 W3).
///
/// **Kept separate from `state/ansi.ts` on purpose.** This is the only module
/// that imports `anser`; it is imported *only* by `ui/LogView.tsx`, which is a
/// lazy chunk — so anser never reaches the boot bundle (plan §7 bundle
/// discipline). The regex-only heuristics (`looksLikeLog`, `markerLabel`, the
/// search patterns) live in `state/ansi.ts` and are safe to import eagerly from
/// `DebugSurface`.
///
/// The app's theme discipline is preserved by re-mapping anser's six default
/// hues onto our `--color-terminal-*` tokens (so a log's INFO/WARN/ERROR colours
/// track the active theme); 256-palette and 24-bit truecolour codes — rare in
/// training logs — pass through as their literal rgb. No colour literal is
/// written in source (only token `var(...)` refs), so the token linter stays
/// clean.
import Anser from 'anser';

export interface AnsiSpan {
  text: string;
  /// A CSS colour: a `--color-terminal-*` token ref for the standard hues, an
  /// `rgb(...)` string for palette/truecolour, or undefined for default text.
  color?: string;
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  dim?: boolean;
}

// anser's default 16-colour palette (normal + bright), keyed by the "r, g, b"
// string it emits, mapped to our theme tokens. Black/white/grey are intentionally
// absent — they pass through so they read against either theme's log surface.
const RGB_TOKEN: Record<string, string> = {
  '187, 0, 0': 'var(--color-terminal-red)',
  '255, 85, 85': 'var(--color-terminal-red)',
  '0, 187, 0': 'var(--color-terminal-green)',
  '85, 255, 85': 'var(--color-terminal-green)',
  '187, 187, 0': 'var(--color-terminal-yellow)',
  '255, 255, 85': 'var(--color-terminal-yellow)',
  '0, 0, 187': 'var(--color-terminal-blue)',
  '85, 85, 255': 'var(--color-terminal-blue)',
  '187, 0, 187': 'var(--color-terminal-magenta)',
  '255, 85, 255': 'var(--color-terminal-magenta)',
  '0, 187, 187': 'var(--color-terminal-cyan)',
  '85, 255, 255': 'var(--color-terminal-cyan)',
};

/// Parse one log line into styled spans. A line with no escapes returns a single
/// plain span (`ansiToSpans('x') === [{ text: 'x' }]`).
export function ansiToSpans(line: string): AnsiSpan[] {
  const parts = Anser.ansiToJson(line, { remove_empty: true, use_classes: false });
  return parts.map((p) => {
    const span: AnsiSpan = { text: p.content };
    if (p.fg) span.color = RGB_TOKEN[p.fg] ?? `rgb(${p.fg})`;
    const d = p.decoration ?? '';
    if (d.includes('bold')) span.bold = true;
    if (d.includes('italic')) span.italic = true;
    if (d.includes('underline')) span.underline = true;
    if (d.includes('dim')) span.dim = true;
    return span;
  });
}
