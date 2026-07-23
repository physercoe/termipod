/// Regex-only log heuristics for the Inspect (J3) log viewer (plan §4 W3):
/// "does this paste look like a log", step/epoch marker extraction, and the
/// `log_search` patterns behind the quick-filter and marker pre-scan.
///
/// **No `anser` import here.** This module is imported eagerly by `DebugSurface`
/// (for `looksLikeLog`), so pulling anser in would drag it into the boot bundle
/// (plan §7 bundle discipline). The one anser consumer, `ansiToSpans`, lives in
/// `state/ansiSpans.ts`, imported only by the lazy `ui/LogView.tsx`.

// eslint-disable-next-line no-control-regex
const ANSI_RE = /\[[0-9;]*m/;

/// Whether text carries SGR escape sequences (a strong "this is a log" signal).
export function hasAnsi(text: string): boolean {
  return ANSI_RE.test(text);
}

// Log-level words and step/epoch/iteration markers, plus a leading ISO-ish
// timestamp — any of which, seen a few times, marks a paste as a log.
const LOG_LINE_RE = /\b(INFO|WARN|WARNING|ERROR|ERR|DEBUG|TRACE|FATAL|CRITICAL)\b|\b(step|epoch|iter|iteration)\b[\s:=]*\d|^\s*\[?\d{4}-\d\d-\d\d[ T]\d\d:\d\d/i;

/// Heuristic: does this pasted text look like a log (worth offering "View as
/// log")? True on any ANSI content, or 3+ level/marker/timestamp lines in a
/// multi-line blob. Deliberately conservative — a stray "ERROR" in prose stays a
/// code tab.
export function looksLikeLog(text: string): boolean {
  if (hasAnsi(text)) return true;
  const lines = text.split('\n');
  if (lines.length < 4) return false;
  let hits = 0;
  for (const l of lines.slice(0, 200)) {
    if (LOG_LINE_RE.test(l)) {
      hits += 1;
      if (hits >= 3) return true;
    }
  }
  return false;
}

// Step/epoch/iteration markers -> a jump list ("go to the loss spike around step
// 40k" in one click). The captured group is the label shown in the dropdown.
const MARKER_RE = /\b((?:step|epoch|iter(?:ation)?)[\s:=#]*\d[\d,]*)/i;

/// Extract a marker (step/epoch/iter N) from a line, or null.
export function markerLabel(line: string): string | null {
  const m = MARKER_RE.exec(line);
  return m !== null ? m[1].replace(/\s+/g, ' ').trim() : null;
}

/// The `log_search` pattern used to pre-scan a log for its step/epoch markers.
export const MARKER_SEARCH = '\\b(step|epoch|iter|iteration)[ :=#]*[0-9]';
/// The `log_search` pattern behind the error/warn quick-filter.
export const WARN_SEARCH = '\\b(WARN|WARNING|ERROR|ERR|FATAL|CRITICAL)\\b';
