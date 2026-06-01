// Transcript lens classifier (docs/plans/agent-transcript-debug-and-
// header-parity.md, P1). Pins the pure predicate that narrows the
// visible feed to one family — All / Text / Turns / Tools / Errors — so the
// debug-affordance behavior is locked without spinning the widget tree.
//
// Symbols are imported through agent_feed.dart, which re-exports the
// feed_reducer layer (same convention as the other agent_feed_* tests).

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

Map<String, dynamic> _ev(String kind, {Map<String, dynamic>? payload}) =>
    {'kind': kind, 'payload': payload ?? const <String, dynamic>{}};

void main() {
  // No tool result/update context needed for the kind-only lenses.
  const noResults = <String, Map<String, dynamic>>{};
  const noUpdates = <String, Map<String, dynamic>>{};

  group('FeedLens.all', () {
    test('passes every event regardless of kind', () {
      for (final k in ['text', 'tool_call', 'error', 'status_line', 'raw']) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.all, noResults, noUpdates),
          isTrue,
          reason: '$k should pass the all lens',
        );
      }
    });
  });

  group('FeedLens.text', () {
    test('keeps assistant prose, reasoning, and user messages', () {
      for (final k in ['text', 'thought', 'input.text']) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.text, noResults, noUpdates),
          isTrue,
          reason: '$k belongs to the text lens',
        );
      }
    });
    test('drops tool activity', () {
      for (final k in ['tool_call', 'tool_result', 'error']) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.text, noResults, noUpdates),
          isFalse,
        );
      }
    });
  });

  group('FeedLens.turns', () {
    test('keeps inbound turns: user/a2a input, control turns, system', () {
      for (final k in [
        'input.text',
        'input.cancel',
        'input.approval',
        'input.attention_reply',
        'system',
      ]) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.turns, noResults, noUpdates),
          isTrue,
          reason: '$k belongs to the turns lens',
        );
      }
    });
    test('drops the agent\'s own output and tool activity', () {
      for (final k in ['text', 'thought', 'tool_call', 'tool_result']) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.turns, noResults, noUpdates),
          isFalse,
        );
      }
    });
  });

  group('FeedLens.tools', () {
    test('keeps every tool-related card', () {
      for (final k in ['tool_call', 'tool_result', 'tool_call_update']) {
        expect(
          agentEventMatchesLens(_ev(k), FeedLens.tools, noResults, noUpdates),
          isTrue,
          reason: '$k belongs to the tools lens',
        );
      }
    });
    test('drops prose', () {
      expect(
        agentEventMatchesLens(
            _ev('text'), FeedLens.tools, noResults, noUpdates),
        isFalse,
      );
    });
  });

  group('FeedLens.errors / agentEventIsError', () {
    test('a bare error event matches', () {
      expect(
        agentEventMatchesLens(
            _ev('error'), FeedLens.errors, noResults, noUpdates),
        isTrue,
      );
    });

    test('a tool_result with is_error matches', () {
      final e = _ev('tool_result', payload: {'is_error': true});
      expect(agentEventIsError(e, noResults, noUpdates), isTrue);
    });

    test('a tool_result without is_error does not match', () {
      final e = _ev('tool_result', payload: {'is_error': false});
      expect(agentEventIsError(e, noResults, noUpdates), isFalse);
    });

    test('a tool_call whose paired result failed matches', () {
      final call = _ev('tool_call', payload: {'id': 't1', 'name': 'bash'});
      final results = {
        't1': _ev('tool_result',
            payload: {'tool_use_id': 't1', 'is_error': true}),
      };
      expect(agentEventIsError(call, results, noUpdates), isTrue);
    });

    test('a tool_call whose paired result succeeded does not match', () {
      final call = _ev('tool_call', payload: {'id': 't1', 'name': 'bash'});
      final results = {
        't1': _ev('tool_result',
            payload: {'tool_use_id': 't1', 'is_error': false}),
      };
      expect(agentEventIsError(call, results, noUpdates), isFalse);
    });

    test('a tool_call with a failed update matches even without a result',
        () {
      final call = _ev('tool_call', payload: {'id': 't1', 'name': 'bash'});
      final updates = {
        't1': <String, dynamic>{'toolCallId': 't1', 'status': 'failed'},
      };
      expect(agentEventIsError(call, noResults, updates), isTrue);
    });

    test('a pending tool_call with no result/update does not match', () {
      final call = _ev('tool_call', payload: {'id': 't1', 'name': 'bash'});
      expect(agentEventIsError(call, noResults, noUpdates), isFalse);
    });

    test('plain text is not an error', () {
      expect(
        agentEventMatchesLens(
            _ev('text'), FeedLens.errors, noResults, noUpdates),
        isFalse,
      );
    });
  });

  group('turn-nav anchors', () {
    // A small rendered list: prompt, reply, tool, prompt, reply.
    final rendered = [
      _ev('input.text'), // 0 — turn 1
      _ev('text'), // 1
      _ev('tool_call'), // 2
      _ev('input.text'), // 3 — turn 2
      _ev('text'), // 4
    ];

    test('turnAnchorIndices picks only inbound prompts', () {
      expect(turnAnchorIndices(rendered), [0, 3]);
    });

    test('a2a/system are filtered, agent output never anchors', () {
      expect(turnAnchorIndices([_ev('text'), _ev('tool_call')]), isEmpty);
    });

    test('currentTurnOrdinal maps viewport fraction to the turn above it',
        () {
      final anchors = turnAnchorIndices(rendered); // [0, 3]
      // Top of the list → first turn.
      expect(currentTurnOrdinal(anchors, rendered.length, 0.0), 1);
      // Just past the second anchor (idx 3 of 4 → 0.75) → second turn.
      expect(currentTurnOrdinal(anchors, rendered.length, 0.8), 2);
      // Mid-list, before the second prompt → still first turn.
      expect(currentTurnOrdinal(anchors, rendered.length, 0.5), 1);
    });

    test('currentTurnOrdinal is 0 when there are no turns', () {
      expect(currentTurnOrdinal(const [], 10, 0.5), 0);
    });
  });
}
