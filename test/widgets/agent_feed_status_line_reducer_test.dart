import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 v1.0.706 polish — the `latestStatusLinePayload`
// reducer that backs the new SessionInitChip → showSessionDetailsSheet
// `statusLine` plumbing. The reducer's job is exactly one thing: walk
// newest-last and return the most recent `status_line` event's
// payload as a fresh Map (or null). It pairs with the existing
// reducers (`processCostFromEvents`, `sessionNameFromEvents`,
// `rateLimitsFromEvents`, etc.) which all share the same latest-wins
// shape over status_line.

void main() {
  group('latestStatusLinePayload (v1.0.706 polish)', () {
    test('returns null when no status_line event has fired', () {
      // Cold open or a session on a non-claude engine. The session-
      // details sheet's SESSION STATE section must self-gate — the
      // four rows must render NOTHING (not "off"/"off"/"off"/"")
      // when the wire data isn't present.
      final events = <Map<String, dynamic>>[
        {'kind': 'session.init', 'payload': {'model': 'claude-opus-4-7'}},
        {'kind': 'text', 'payload': {'text': 'hello'}},
        {'kind': 'usage', 'payload': {'input_tokens': 100}},
      ];
      expect(latestStatusLinePayload(events), isNull);
    });

    test('returns null for an empty event list', () {
      expect(latestStatusLinePayload(const []), isNull);
    });

    test('returns the latest status_line payload verbatim', () {
      // The reducer must NOT transform the wire shape — the sheet's
      // helpers read `effort.level`, `thinking.enabled`,
      // `output_style.name`, `fast_mode` directly from this map.
      // Shipping a transformed shape would force a contract dance
      // every time claude evolves the schema.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'session_id': 's1',
            'effort': {'level': 'high'},
            'thinking': {'enabled': false},
            'fast_mode': false,
          },
        },
      ];
      final p = latestStatusLinePayload(events);
      expect(p, isNotNull);
      expect(p!['session_id'], 's1');
      expect((p['effort'] as Map)['level'], 'high');
      expect((p['thinking'] as Map)['enabled'], false);
      expect(p['fast_mode'], false);
    });

    test('returns the LATEST when multiple status_line events exist', () {
      // statusLine fires ~every 10s; the sheet must show whichever
      // toggle state is current, not the spawn-time state.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'effort': {'level': 'low'}, 'fast_mode': false},
        },
        // intermediate non-statusLine traffic should not confuse the
        // reducer (it's a forward-scan-from-the-end pattern)
        {'kind': 'text', 'payload': {'text': 'mid-turn note'}},
        {
          'kind': 'status_line',
          'payload': {'effort': {'level': 'xhigh'}, 'fast_mode': true},
        },
      ];
      final p = latestStatusLinePayload(events);
      expect(p, isNotNull);
      expect((p!['effort'] as Map)['level'], 'xhigh');
      expect(p['fast_mode'], true);
    });

    test('tolerates non-Map payload (degrade null, no throw)', () {
      // Defensive: the wire schema is well-typed but a future driver
      // bug could ship a non-map payload. The reducer must NOT
      // throw — the sheet's null branch handles it cleanly.
      final events = <Map<String, dynamic>>[
        {'kind': 'status_line', 'payload': 'not a map'},
      ];
      expect(latestStatusLinePayload(events), isNull);
    });

    test('walks past non-status_line events to find the latest match', () {
      // Mixed timeline: text / tool_call / usage events follow the
      // latest status_line. The reducer must walk past them.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'fast_mode': true},
        },
        {'kind': 'tool_call', 'payload': {'name': 'Bash'}},
        {'kind': 'tool_result', 'payload': {'content': 'ok'}},
        {'kind': 'text', 'payload': {'text': 'done'}},
        {'kind': 'usage', 'payload': {'input_tokens': 50}},
      ];
      final p = latestStatusLinePayload(events);
      expect(p, isNotNull);
      expect(p!['fast_mode'], true);
    });
  });
}
