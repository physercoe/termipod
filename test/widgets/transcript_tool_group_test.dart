// Agent-transcript-redesign §6 P1 reducer tests — the two client halves
// of the phase:
//
//   (a) `plan` fold-in-place: collapseStreamingPartials now chains
//       `kind=plan` events by kind+message_id the same way it chains
//       text/thought partials, so the hub's per-turn plan stamp
//       (message_id + partial:true) renders ONE checklist card that
//       updates instead of N snapshot cards (G3).
//   (b) tool-call grouping: groupConsecutiveToolCalls turns a run of
//       ≥2 consecutive visible tool_calls into one ToolCallGroup
//       display item (kimi-web tool-stack rule, decision §7.3); runs
//       break on non-tool_call rows and on stamped turn_id changes.
//   (c) aggregate state: running > error > done over the SAME
//       per-row lineage the standalone card resolves.
//
// Symbols are imported through live_feed.dart, which re-exports the
// feed_reducer layer (same convention as the other agent_feed_* tests).

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/live_feed.dart';
import 'package:termipod/widgets/transcript/fold_maps.dart';

Map<String, dynamic> _ev(String kind,
        {Map<String, dynamic>? payload, int? seq}) =>
    {
      'kind': kind,
      if (seq != null) 'seq': seq,
      'payload': payload ?? const <String, dynamic>{},
    };

Map<String, dynamic> _call(String id,
        {String name = 'Bash', int? seq, String? status, String? turnId}) =>
    _ev('tool_call', seq: seq, payload: {
      'id': id,
      'name': name,
      if (status != null) 'status': status,
      if (turnId != null) 'turn_id': turnId,
    });

// toolResults values are the full tool_result EVENT (FoldMaps stores
// the row, not just the payload); toolUpdates values are the update
// PAYLOAD (same shapes FoldMaps.fromEvents produces).
Map<String, Map<String, dynamic>> _results(
        String id, Map<String, dynamic> payload) =>
    {
      id: _ev('tool_result',
          payload: {'tool_use_id': id, ...payload}),
    };

Map<String, Map<String, dynamic>> _updates(
        String id, Map<String, dynamic> payload) =>
    {
      id: {'toolCallId': id, ...payload},
    };

const noResults = <String, Map<String, dynamic>>{};
const noUpdates = <String, Map<String, dynamic>>{};

void main() {
  group('collapseStreamingPartials — plan fold-in-place (P1)', () {
    test('two partial plan updates sharing a message_id fold to one entry',
        () {
      final events = [
        _ev('plan', payload: {
          'message_id': 'm1',
          'partial': true,
          'entries': [
            {'content': 'step 1', 'status': 'in_progress'},
          ],
        }),
        _ev('plan', payload: {
          'message_id': 'm1',
          'partial': true,
          'entries': [
            {'content': 'step 1', 'status': 'completed'},
            {'content': 'step 2', 'status': 'in_progress'},
          ],
        }),
      ];
      final out = collapseStreamingPartials(events);
      expect(out, hasLength(1));
      // Latest snapshot wins — the single card carries the newest list.
      final entries =
          (out.single['payload'] as Map)['entries'] as List;
      expect(entries, hasLength(2));
    });

    test('a non-partial plan event replaces its partial chain', () {
      final events = [
        _ev('plan',
            payload: {'message_id': 'm1', 'partial': true, 'entries': []}),
        _ev('plan', payload: {
          'message_id': 'm1',
          'entries': [
            {'content': 'done', 'status': 'completed'},
          ],
        }),
      ];
      final out = collapseStreamingPartials(events);
      expect(out, hasLength(1));
      expect((out.single['payload'] as Map)['partial'], isNull);
    });

    test('a non-partial plan with no preceding chain appends', () {
      // Engines that emit one-shot plans (no partial chain) must keep
      // stacking the way they did before the fold — only chain members
      // redirect.
      final events = [
        _ev('plan', payload: {
          'message_id': 'm2',
          'entries': [
            {'content': 'a', 'status': 'pending'},
          ],
        }),
        _ev('plan', payload: {
          'message_id': 'm2',
          'entries': [
            {'content': 'b', 'status': 'pending'},
          ],
        }),
      ];
      expect(collapseStreamingPartials(events), hasLength(2));
    });

    test('a plan without a message_id appends (pre-stamp transcripts)', () {
      final events = [
        _ev('plan', payload: {
          'entries': [
            {'content': 'a', 'status': 'pending'},
          ],
        }),
        _ev('plan', payload: {
          'entries': [
            {'content': 'b', 'status': 'pending'},
          ],
        }),
      ];
      expect(collapseStreamingPartials(events), hasLength(2));
    });

    test('plan chains do not fold into text chains sharing a message_id',
        () {
      final events = [
        _ev('text',
            payload: {'message_id': 'm1', 'partial': true, 'text': 'hi'}),
        _ev('plan',
            payload: {'message_id': 'm1', 'partial': true, 'entries': []}),
      ];
      // Chains are namespaced by kind — both survive.
      expect(collapseStreamingPartials(events), hasLength(2));
    });
  });

  group('groupConsecutiveToolCalls (P1 grouping)', () {
    test('≥2 consecutive tool_calls become one group item', () {
      final out = groupConsecutiveToolCalls([
        _call('t1', seq: 1),
        _call('t2', seq: 2),
      ]);
      expect(out, hasLength(1));
      expect(out.single.isGroup, isTrue);
      expect(out.single.group!.events, hasLength(2));
    });

    test('a lone tool_call stays a standalone single', () {
      final out = groupConsecutiveToolCalls([
        _ev('text', seq: 1, payload: {'text': 'working…'}),
        _call('t1', seq: 2),
        _ev('text', seq: 3, payload: {'text': 'done'}),
      ]);
      expect(out, hasLength(3));
      expect(out.every((i) => !i.isGroup), isTrue);
      expect(out[1].event, isNotNull);
    });

    test('the run breaks at an intervening non-tool_call row', () {
      final out = groupConsecutiveToolCalls([
        _call('t1', seq: 1),
        _call('t2', seq: 2),
        _ev('text', seq: 3, payload: {'text': 'mid-turn prose'}),
        _call('t3', seq: 4),
        _call('t4', seq: 5),
      ]);
      // Two groups of two with the text row between them — assistant
      // prose flows BETWEEN groups (kimi-web turn shape).
      expect(out.map((i) => i.isGroup).toList(),
          [true, false, true]);
      expect(out[0].group!.events, hasLength(2));
      expect(out[1].event!['kind'], 'text');
      expect(out[2].group!.events, hasLength(2));
    });

    test('the run breaks when both neighbours carry differing turn_ids',
        () {
      final out = groupConsecutiveToolCalls([
        _call('t1', seq: 1, turnId: 't-1'),
        _call('t2', seq: 2, turnId: 't-2'),
      ]);
      expect(out, hasLength(2));
      expect(out.every((i) => !i.isGroup), isTrue);
    });

    test('same turn_id still groups; missing turn_id falls back to '
        'adjacency', () {
      final sameTurn = groupConsecutiveToolCalls([
        _call('t1', seq: 1, turnId: 't-1'),
        _call('t2', seq: 2, turnId: 't-1'),
      ]);
      expect(sameTurn.single.isGroup, isTrue);

      // Only ONE side stamped (replay seam): do not fragment.
      final oneStamped = groupConsecutiveToolCalls([
        _call('t1', seq: 1, turnId: 't-1'),
        _call('t2', seq: 2),
      ]);
      expect(oneStamped.single.isGroup, isTrue);

      // Neither stamped (drivers without turn tracking): pure adjacency.
      final unstamped = groupConsecutiveToolCalls([
        _call('t1', seq: 1),
        _call('t2', seq: 2),
        _call('t3', seq: 3),
      ]);
      expect(unstamped.single.group!.events, hasLength(3));
    });

    test('a group of one never forms mid-list', () {
      final out = groupConsecutiveToolCalls([
        _call('t1', seq: 1),
        _call('t2', seq: 2),
        _call('t3', seq: 3),
        _ev('text', seq: 4, payload: {'text': 'x'}),
        _call('t4', seq: 5),
      ]);
      expect(out, hasLength(3));
      expect(out[0].group!.events, hasLength(3));
      expect(out[2].isGroup, isFalse);
    });

    test('display-item anchors: group anchor is the first member seq; '
        'containsSeq matches any member', () {
      final out = groupConsecutiveToolCalls([
        _call('t1', seq: 10),
        _call('t2', seq: 11),
        _ev('text', seq: 12, payload: {'text': 'x'}),
      ]);
      final group = out.first;
      expect(group.anchorSeq, 10);
      expect(group.containsSeq(11), isTrue);
      expect(group.containsSeq(12), isFalse);
      expect(out[1].anchorSeq, 12);
      expect(out[1].containsSeq(12), isTrue);
    });
  });

  group('toolCallGroupState (P1 aggregate: running > error > done)', () {
    final twoCalls = [_call('t1'), _call('t2')];
    ToolCallGroup groupOf(List<Map<String, dynamic>> events) =>
        groupConsecutiveToolCalls(events).single.group!;

    test('all-resolved group reports done', () {
      final results = {
        ..._results('t1', {'is_error': false}),
        ..._results('t2', {'is_error': false}),
      };
      final group = groupOf(twoCalls);
      expect(toolCallGroupState(group, results, noUpdates),
          ToolGroupState.done);
      expect(toolCallGroupErrorCount(group, results, noUpdates), 0);
    });

    test('a pending call with no failures reports running', () {
      final results = _results('t1', {'is_error': false});
      final group = groupOf([
        _call('t1'),
        _call('t2', status: 'pending'),
      ]);
      expect(toolCallGroupState(group, results, noUpdates),
          ToolGroupState.running);
    });

    test('an in-flight update status reports running too', () {
      final results = _results('t1', {'is_error': false});
      final updates = _updates('t2', {'status': 'in_progress'});
      final group = groupOf(twoCalls);
      expect(toolCallGroupState(group, results, updates),
          ToolGroupState.running);
    });

    test('a group with a failed result reports error; header counts it',
        () {
      final results = {
        ..._results('t1', {'is_error': true}),
        ..._results('t2', {'is_error': false}),
      };
      final group = groupOf(twoCalls);
      expect(toolCallGroupState(group, results, noUpdates),
          ToolGroupState.error);
      expect(toolCallGroupErrorCount(group, results, noUpdates), 1);
    });

    test('a failed tool_call_update marks the row error without a result',
        () {
      final results = _results('t2', {'is_error': false});
      final updates = _updates('t1', {'status': 'failed'});
      final group = groupOf(twoCalls);
      expect(toolCallGroupState(group, results, updates),
          ToolGroupState.error);
      expect(toolCallGroupErrorCount(group, results, updates), 1);
    });

    test('running outranks error: failed + pending reads running, '
        'error still counted', () {
      final results = _results('t1', {'is_error': true});
      final group = groupOf([
        _call('t1'),
        _call('t2', status: 'pending'),
      ]);
      expect(toolCallGroupState(group, results, noUpdates),
          ToolGroupState.running);
      expect(toolCallGroupErrorCount(group, results, noUpdates), 1);
    });
  });

  group('toolCallDisplayStatus — shared with the standalone card', () {
    test('update status wins over the creation-frame status', () {
      final p = {'id': 't1', 'name': 'Bash', 'status': 'pending'};
      expect(toolCallDisplayStatus(p, {'status': 'in_progress'}, null),
          'in_progress');
    });

    test('a paired result resolves terminal status without an update', () {
      final p = {'id': 't1', 'name': 'Bash'};
      expect(toolCallDisplayStatus(p, null, {'is_error': false}),
          'completed');
      expect(
          toolCallDisplayStatus(p, null, {'is_error': true}), 'failed');
    });

    test('no lineage at all reads pending', () {
      expect(toolCallDisplayStatus({'id': 't1'}, null, null), 'pending');
    });
  });

  group('log-tail claude-code shape — call id under tool_use_id only', () {
    // The local-log-tail claude-code mapper writes the call id as
    // `tool_use_id` with NO `id` key (mapper.go `tool_use` arm), while
    // results key on the same value. Pairing must go through
    // `callToolIdOf` (fold_maps.dart) — reading `payload['id']` alone
    // leaves every such group row "running" forever. Desktop parity:
    // toolGroups.ts `callToolId`.
    Map<String, dynamic> logTailCall(String id, {int? seq}) =>
        _ev('tool_call',
            seq: seq, payload: {'tool_use_id': id, 'name': 'Bash'});
    ToolCallGroup groupOf(List<Map<String, dynamic>> events) =>
        ToolCallGroup(events);

    test('a resolved result reads done, not running-forever', () {
      final results = _results('toolu_1', {'is_error': false});
      expect(
          toolCallRowState(logTailCall('toolu_1'), results, noUpdates),
          ToolGroupState.done);
    });

    test('an error result classifies the row error', () {
      final results = _results('toolu_1', {'is_error': true});
      expect(
          toolCallRowState(logTailCall('toolu_1'), results, noUpdates),
          ToolGroupState.error);
    });

    test('the group aggregate resolves once every log-tail call pairs',
        () {
      final results = {
        ..._results('toolu_1', {'is_error': false}),
        ..._results('toolu_2', {'is_error': false}),
      };
      final group = groupOf([
        logTailCall('toolu_1', seq: 1),
        logTailCall('toolu_2', seq: 2),
      ]);
      expect(toolCallGroupState(group, results, noUpdates),
          ToolGroupState.done);
    });

    test('FoldMaps names a log-tail call so the update hide-rule pairs',
        () {
      final maps = FoldMaps.fromEvents([logTailCall('toolu_1')]);
      expect(maps.toolNames['toolu_1'], 'Bash');
    });
  });
}
