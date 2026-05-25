import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/hub/session_display.dart';

// Tests for the shared session-title precedence helper (v1.0.705
// polish on top of ADR-036 W6). The helper formalises a contract
// every surface (session list row, me-page recent card, resume into
// chat) honours:
//
//   user title  >  session_name_hint  >  '(untitled session)'
//
// The search-screen row uses a different key shape (session_title /
// session_name_hint) so it inlines the same precedence at the call
// site; these tests cover the typed wrapper that the other three
// surfaces call.

void main() {
  group('sessionDisplayTitle', () {
    test('returns the user-set title when present', () {
      final session = <String, dynamic>{
        'title': 'My favourite session',
        'session_name_hint': 'Refactor schema',
      };
      // User wins — even if claude has a perfectly good hint, the
      // user's chosen name is sticky and load-bearing across search /
      // audit / voice.
      expect(sessionDisplayTitle(session), 'My favourite session');
    });

    test('falls back to session_name_hint when title is empty', () {
      // Common case in early conversation: user hasn't renamed yet
      // but claude has auto-derived a label.
      final session = <String, dynamic>{
        'title': '',
        'session_name_hint': 'List directory files',
      };
      expect(sessionDisplayTitle(session), 'List directory files');
    });

    test('falls back to placeholder when both title and hint are empty', () {
      // Three causes: pre-W6 sessions; engines that don't emit
      // session_name (codex/gemini/kimi today); status_line frame
      // hasn't fired yet on a fresh agent. All three produce empty
      // strings on both fields.
      final session = <String, dynamic>{
        'title': '',
        'session_name_hint': '',
      };
      expect(sessionDisplayTitle(session), '(untitled session)');
    });

    test('handles missing keys as empty (tolerates pre-W6 hub payloads)', () {
      // Older hubs that haven't shipped the v1.0.705 hub-side change
      // omit session_name_hint entirely. The helper must NOT throw —
      // the mobile build is forward-rolled but the hub on the
      // device's pod may lag during a coordinated rollout.
      expect(sessionDisplayTitle(const <String, dynamic>{}),
          '(untitled session)');
      expect(sessionDisplayTitle(const <String, dynamic>{'title': ''}),
          '(untitled session)');
      expect(
          sessionDisplayTitle(const <String, dynamic>{
            'session_name_hint': 'Just the hint',
          }),
          'Just the hint');
    });

    test('trims whitespace from both fields', () {
      // Defensive: the wire-side schema is `string` with `omitempty`,
      // but an accidentally-whitespace-only title shouldn't be
      // treated as "the user set something" — that would mask the
      // hint AND the placeholder.
      final session = <String, dynamic>{
        'title': '   ',
        'session_name_hint': '  hint with padding  ',
      };
      expect(sessionDisplayTitle(session), 'hint with padding');
    });

    test('non-string field values coerce defensively', () {
      // The mobile app reads sessions as `Map<String, dynamic>` — the
      // hub wire shape is well-typed but a future schema migration
      // could surface a non-string here transiently. The helper's
      // `.toString()` keeps the call site safe; null coerces to
      // empty, numeric coerces to its string form (a developer's
      // mistake, not a user-facing intent).
      final session = <String, dynamic>{
        'title': null,
        'session_name_hint': null,
      };
      expect(sessionDisplayTitle(session), '(untitled session)');
    });
  });
}
