// Session-title display precedence (v1.0.705 polish on top of ADR-036
// W6). Centralises the three-tier fallback that every surface
// rendering a session label honours:
//
//   user-set title  >  session_name_hint  >  '(untitled session)'
//
// `title` comes from the hub `sessions.title` column — only the user
// (or an explicit rename API call) writes it. `session_name_hint`
// comes from claude-code's statusLine `session_name` field captured
// on every status_line event (see hub/internal/server's
// captureSessionNameHint). Other engines (codex / gemini / kimi) leave
// the hint empty today — the precedence ladder still terminates
// cleanly at the literal placeholder.
//
// The hint is auto-derived and lossy by design: claude updates it
// continuously as the topic evolves, so a session that was "list
// directory files" five minutes ago may now be "refactor schema". A
// user-set title is sticky and load-bearing across surfaces (search
// index, audit log, voice). The fallback only kicks in when the user
// hasn't taken the trouble to name the session, which is the common
// case in early-conversation chat.

/// Returns the visible title for a session JSON map, applying the
/// `user title > session_name_hint > '(untitled session)'` precedence
/// in one place. Each consumer reads the same `session_name_hint`
/// field name as ships on the wire (see hub `sessionOut`).
///
/// Trims defensively because the wire-side schema is `string` with
/// `omitempty` — a hint with surrounding whitespace would still pass
/// the `isNotEmpty` check otherwise.
String sessionDisplayTitle(Map<String, dynamic> session) {
  final title = (session['title'] ?? '').toString().trim();
  if (title.isNotEmpty) return title;
  final hint = (session['session_name_hint'] ?? '').toString().trim();
  if (hint.isNotEmpty) return hint;
  return '(untitled session)';
}
