import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 W6 — the two final status_line-sourced surfaces
// in Phase B:
//
//   exceeds200kFromEvents — bool? from latest status_line.
//                           exceeds_200k_tokens. Drives the red
//                           "200K cap" alarm tile.
//   sessionNameFromEvents — String? from latest status_line.
//                           session_name. Drives the SessionChatScreen
//                           AppBar title fallback (user title wins;
//                           hint is the candidate).
//
// Pure-data reducers; the widget-tree composition + parent-callback
// wiring is exercised by manual smoke once it ships.

void main() {
  group('exceeds200kFromEvents (ADR-036 W6)', () {
    test('returns null when no status_line frame has fired', () {
      // Cold open. The alarm tile must self-gate, NOT render the
      // hard-cap warning when claude hasn't actually told us
      // anything is wrong.
      final events = <Map<String, dynamic>>[
        {'kind': 'session.init', 'payload': {'model': 'claude-opus-4-7'}},
        {'kind': 'text', 'payload': {'text': 'hi'}},
      ];
      expect(exceeds200kFromEvents(events), isNull);
    });

    test('returns null when status_line lacks the field', () {
      // Defensive: a status_line frame without the exceeds_200k_tokens
      // field — older claude versions or a partial payload.
      // Degrade silent rather than treat absence as "false" (which
      // would suggest a deliberate "you're fine" signal that wasn't
      // actually sent).
      final events = <Map<String, dynamic>>[
        {'kind': 'status_line', 'payload': {'cost': {'total_cost_usd': 0.01}}},
      ];
      expect(exceeds200kFromEvents(events), isNull);
    });

    test('returns true from the latest status_line frame', () {
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'exceeds_200k_tokens': true},
        },
      ];
      expect(exceeds200kFromEvents(events), isTrue);
    });

    test('returns false explicitly when status_line carries false', () {
      // Distinct from "null when absent" — explicit false from
      // claude means "you have NOT exceeded the cap", which the
      // chip respects by suppressing the alarm tile.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'exceeds_200k_tokens': false},
        },
      ];
      expect(exceeds200kFromEvents(events), isFalse);
    });

    test('latest-wins: trips on, then clears', () {
      // /clear within the same process resets the prompt size; the
      // next status_line frame will carry false again. The reducer
      // must reflect the most recent state, NOT an OR-across-history.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'exceeds_200k_tokens': true},
        },
        {'kind': 'text', 'payload': {'text': 'user: /clear'}},
        {
          'kind': 'status_line',
          'payload': {'exceeds_200k_tokens': false},
        },
      ];
      expect(exceeds200kFromEvents(events), isFalse);
    });

    test('non-bool value treated as absent', () {
      // Defensive: a malformed payload that ships a string or number.
      // The reducer's isA<bool> guard drops it cleanly.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'exceeds_200k_tokens': 'true'}, // string, not bool
        },
      ];
      expect(exceeds200kFromEvents(events), isNull);
    });

    test('empty event list returns null', () {
      expect(exceeds200kFromEvents(const []), isNull);
    });
  });

  group('sessionNameFromEvents (ADR-036 W6)', () {
    test('returns null when no status_line has fired', () {
      final events = <Map<String, dynamic>>[
        {'kind': 'text', 'payload': {'text': 'hi'}},
      ];
      expect(sessionNameFromEvents(events), isNull);
    });

    test('returns null when status_line lacks session_name', () {
      // claude auto-derives session_name only after a few turns;
      // pre-naming frames just omit the field. Hint stays null →
      // caller's user title (or (untitled session) placeholder)
      // wins.
      final events = <Map<String, dynamic>>[
        {'kind': 'status_line', 'payload': {'cost': {'total_cost_usd': 0.01}}},
      ];
      expect(sessionNameFromEvents(events), isNull);
    });

    test('returns the verbatim session_name string', () {
      // Real example from the probe — claude derives labels like
      // "List directory files" from the first user message.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'session_name': 'List directory files'},
        },
      ];
      expect(sessionNameFromEvents(events), 'List directory files');
    });

    test('empty string is normalized to null', () {
      // Caller would otherwise have to double-check for empty + null;
      // the reducer hands back a single falsy.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'session_name': ''},
        },
      ];
      expect(sessionNameFromEvents(events), isNull);
    });

    test('latest-wins across multiple frames', () {
      // claude might rename a session over time as it learns more
      // about the topic. The reducer must show the most recent.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'session_name': 'Old label'},
        },
        {
          'kind': 'status_line',
          'payload': {'session_name': 'New label'},
        },
      ];
      expect(sessionNameFromEvents(events), 'New label');
    });

    test('walks past interleaved non-status_line kinds', () {
      // Real wire interleaves status_line with text/usage/tool_call.
      // The reducer must find the latest status_line carrying a name
      // regardless of intervening events.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'session_name': 'First'},
        },
        {'kind': 'text', 'payload': {'text': 'a'}},
        {'kind': 'usage', 'payload': {'input_tokens': 1}},
        {
          'kind': 'status_line',
          'payload': {'session_name': 'Second'},
        },
        {'kind': 'tool_call', 'payload': {'id': 'tc'}},
      ];
      expect(sessionNameFromEvents(events), 'Second');
    });

    test('non-String value treated as absent', () {
      // Defensive: a payload that ships the wrong type for the field.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'session_name': 42},
        },
      ];
      expect(sessionNameFromEvents(events), isNull);
    });

    test('empty event list returns null', () {
      expect(sessionNameFromEvents(const []), isNull);
    });
  });
}
