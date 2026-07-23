/// Slash-command matching for the composer `/` picker (agent-transcript-redesign
/// §6 P3) — a TS port of mobile `agent_compose.dart` `_activeMatch` /
/// `_applySuggestion` / `isSlashCommandBody`. Engine-neutral: the pool is the
/// session's ACP command catalog (`session.init.slash_commands`, merged by
/// `mergeSessionInit` in AgentInfo.tsx), names only — the catalog carries no
/// descriptions. No runtime imports so it runs under `node --test`.

/// An active `/`-token match in a composer draft (mobile `_PrefixMatch`).
export interface SlashMatch {
  /// The filter text between the `/` and the caret.
  query: string;
  /// Offset of the token's leading `/` in the draft.
  tokenStart: number;
  /// Offset just past the token (= the caret).
  tokenEnd: number;
  /// Prefix-matched catalog entries, leading slash stripped, capped at 8.
  matches: string[];
}

/// Mobile caps the strip at 8 (agent_compose.dart:224); mirror that.
const MAX_MATCHES = 8;

/// The `/`-token immediately before the caret: start-of-input or after
/// whitespace, no whitespace inside the token. Mirrors the mention picker's
/// AT_RE shape so the two triggers can never both be active — only one token
/// ends at the caret, and its lead (`/` vs `@`) decides the pool.
const SLASH_RE = /(^|\s)\/([^\s]*)$/;

/// Filter the catalog by prefix, case-insensitive. claude-code ships names
/// WITH a leading slash ("/help") while ACP hubs send bare names ("help") —
/// strip a pool entry's slash before comparing so typing "/he" still matches
/// "/help" without offering a "//help" suggestion (mobile
/// `_activeMatch`, agent_compose.dart:216-225). An empty query lists the pool.
export function filterSlashCommands(query: string, commands: readonly string[]): string[] {
  const q = query.toLowerCase();
  const out: string[] = [];
  for (const c of commands) {
    const norm = c.startsWith('/') ? c.slice(1) : c;
    if (q === '' || norm.toLowerCase().startsWith(q)) {
      out.push(norm);
      if (out.length >= MAX_MATCHES) break;
    }
  }
  return out;
}

/// The `/`-token match active at `cursor` in `text`, or null when the caret
/// isn't on a `/` token, the catalog is empty, or nothing prefix-matches
/// (mobile `_activeMatch`, agent_compose.dart:201-233 — including its
/// no-match-hides-the-strip rule; deleting past the `/` breaks the token and
/// so dismisses the picker on the next change).
export function activeSlashMatch(
  text: string,
  cursor: number,
  commands: readonly string[],
): SlashMatch | null {
  if (commands.length === 0) return null;
  const upto = text.slice(0, cursor);
  const m = SLASH_RE.exec(upto);
  if (m === null) return null;
  const matches = filterSlashCommands(m[2], commands);
  if (matches.length === 0) return null;
  return {
    query: m[2],
    tokenStart: m.index + m[1].length,
    tokenEnd: upto.length,
    matches,
  };
}

/// True when `body` is an engine-control slash command (`/clear`,
/// `/compact focus`, `/model claude-sonnet-4`, …) — a TS port of mobile
/// `isSlashCommandBody` (agent_compose.dart:50-56). The shape gate is
/// deliberately narrow:
///
///   - first non-whitespace char must be `/`
///   - the command token after `/` must start with a letter and contain only
///     [A-Za-z0-9_.-] — the dot admits namespaced catalog names like
///     kimi-code's `/sub-skill.review`; `/` stays excluded so path-like
///     values (`/etc/foo`) and markdown list markers (`/ - item`) that
///     happen to start with a slash still fail the gate
///   - whitespace + optional args may follow on the same line; a multi-line
///     body is still allowed (`/compact <multiline focus>`)
///
/// Callers flip `raw: true` on `postAgentInput`, which makes the hub bypass
/// the principal-directive envelope wrap so the engine receives the verbatim
/// slash command rather than prose framed as a directive.
export function isSlashCommandBody(body: string): boolean {
  const t = body.trim();
  if (t === '' || !t.startsWith('/')) return false;
  const nl = t.indexOf('\n');
  const firstLine = nl === -1 ? t : t.slice(0, nl);
  return /^\/[A-Za-z][\w.-]*(?:\s.*)?$/.test(firstLine);
}
