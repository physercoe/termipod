// Unit tests for FoldMaps — the per-event fold extracted from
// `_AgentFeedState.build()` (ADR-040 P1, the shared substrate). Pure: drives
// the fold with canned event maps and pins the four lineage maps directly,
// without a widget tree.

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/fold_maps.dart';

Map<String, dynamic> _ev(String kind, Map<String, dynamic> payload,
        {int seq = 0, String ts = ''}) =>
    {'kind': kind, 'payload': payload, if (seq > 0) 'seq': seq, if (ts.isNotEmpty) 'ts': ts};

void main() {
  test('tool_call id → name (only when both present)', () {
    final f = FoldMaps.fromEvents([
      _ev('tool_call', {'id': 't1', 'name': 'Bash'}),
      _ev('tool_call', {'id': 't2'}), // no name → skipped
      _ev('tool_call', {'name': 'Edit'}), // no id → skipped
    ]);
    expect(f.toolNames, {'t1': 'Bash'});
  });

  test('tool_call_update keyed by toolCallId or tool_call_id, latest wins', () {
    final f = FoldMaps.fromEvents([
      _ev('tool_call_update', {'toolCallId': 't1', 'status': 'running'}),
      _ev('tool_call_update', {'toolCallId': 't1', 'status': 'completed'}),
      _ev('tool_call_update', {'tool_call_id': 't2', 'status': 'failed'}),
    ]);
    expect(f.toolUpdates['t1']!['status'], 'completed'); // latest replaces
    expect(f.toolUpdates['t2']!['status'], 'failed'); // snake_case key honoured
  });

  test('tool_result keyed by tool_use_id stores the full event row', () {
    final f = FoldMaps.fromEvents([
      _ev('tool_result', {'tool_use_id': 't1', 'is_error': true},
          seq: 9, ts: '2026-06-02T00:00:00Z'),
    ]);
    final row = f.toolResults['t1']!;
    expect(row['seq'], 9); // the full event, not just the payload
    expect(row['ts'], '2026-06-02T00:00:00Z');
    expect((row['payload'] as Map)['is_error'], true);
  });

  test('input.approval records request_id → decision', () {
    final f = FoldMaps.fromEvents([
      _ev('input.approval', {'request_id': 'r1', 'decision': 'allow'}),
    ]);
    expect(f.resolvedApprovals, {'r1': 'allow'});
  });

  test('events with a non-map payload are skipped, not thrown', () {
    final f = FoldMaps.fromEvents([
      {'kind': 'tool_call', 'payload': 'not-a-map'},
      _ev('text', {'text': 'hello'}), // irrelevant kind → no maps
    ]);
    expect(f.toolNames, isEmpty);
    expect(f.toolUpdates, isEmpty);
    expect(f.toolResults, isEmpty);
    expect(f.resolvedApprovals, isEmpty);
  });
}
