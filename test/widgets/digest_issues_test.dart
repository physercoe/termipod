import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/transcript/digest_issues.dart';

// Reader checks for the Issues sheet (transcript P5 A2). The two rules pinned
// here are the ones that must NOT drift from the hub or from desktop's reader
// (desktop/src/state/digestIssues.ts): the severity ordering, and the seek
// anchor (session_ordinal when > 0, else seq — seq collides across a resumed
// session's agents, ADR-042).
//
// This family has shipped four mobile↔desktop misses; keeping the logic in a
// pure module with the SAME assertions on both sides is the answer.

Map<String, dynamic> _digest(Object? issues, {String? worst}) => {
      'issues': issues,
      if (worst != null) 'issue_worst_severity': worst,
    };

void main() {
  test('a hub that predates the field yields an empty summary, not a clean bill',
      () {
    // "no issues" and "never looked" must not read the same, or an old hub
    // would report a run as clean.
    for (final d in <Map<String, dynamic>?>[
      null,
      <String, dynamic>{},
      _digest(null),
      _digest('nope'),
      _digest(<dynamic>[]),
    ]) {
      final got = readDigestIssues(d);
      expect(got.total, 0);
      expect(got.isEmpty, isTrue);
      expect(got.worst, '');
    }
  });

  test('classes sort severity-first, then loudest, then alphabetical', () {
    final got = readDigestIssues(_digest({
      'mixed_id_shape': {'count': 9, 'severity': 'info', 'sample_seqs': [1]},
      'orphan_tool_result': {'count': 2, 'severity': 'warning', 'sample_seqs': [2]},
      'abnormal_stop': {'count': 7, 'severity': 'warning', 'sample_seqs': [3]},
      'missing_tool_result': {'count': 1, 'severity': 'error', 'sample_seqs': [4]},
    }));
    expect(got.classes.map((c) => c.cls).toList(), [
      'missing_tool_result',
      'abnormal_stop',
      'orphan_tool_result',
      'mixed_id_shape',
    ]);
    expect(got.total, 19);
    expect(got.worst, 'error');
  });

  test('equal severity and count falls back to a stable alphabetical order', () {
    final got = readDigestIssues(_digest({
      'orphan_tool_result': {'count': 3, 'severity': 'warning'},
      'abnormal_stop': {'count': 3, 'severity': 'warning'},
      'incomplete_turn': {'count': 3, 'severity': 'warning'},
    }));
    expect(got.classes.map((c) => c.cls).toList(),
        ['abnormal_stop', 'incomplete_turn', 'orphan_tool_result']);
  });

  test('the seek coord prefers the session ordinal and falls back to the seq',
      () {
    final got = readDigestIssues(_digest({
      'missing_tool_result': {
        'count': 3,
        'severity': 'error',
        'sample_seqs': [10, 20, 30],
        // A session-less agent folds every ordinal as 0; a partial list (the
        // pre-v5 degrade) leaves the tail absent. Both must fall back.
        'sample_ordinals': [101, 0],
        'sample_ts': ['t1', 't2', 't3'],
        'sample_labels': ['Bash', '', 'Edit'],
      },
    }));
    final g = got.classes.single;
    expect(g.samples.map((s) => s.coord).toList(), [101, 20, 30]);
    expect(g.samples.map((s) => s.label).toList(), ['Bash', '', 'Edit']);
    expect(g.samples.map((s) => s.ts).toList(), ['t1', 't2', 't3']);
  });

  test('a capped sample list is flagged so the sheet can say so', () {
    final got = readDigestIssues(_digest({
      'missing_tool_result': {
        'count': 250,
        'severity': 'error',
        'sample_seqs': [1, 2, 3],
      },
      'orphan_tool_result': {
        'count': 2,
        'severity': 'warning',
        'sample_seqs': [4, 5],
      },
    }));
    expect(got.classes[0].capped, isTrue);
    expect(got.classes[1].capped, isFalse);
  });

  test('a class with no samples at all is still counted and marked capped', () {
    final got = readDigestIssues(
        _digest({'incomplete_turn': {'count': 4, 'severity': 'warning'}}));
    expect(got.total, 4);
    expect(got.classes.single.samples, isEmpty);
    expect(got.classes.single.capped, isTrue);
  });

  test('zero-count and malformed classes are dropped', () {
    final got = readDigestIssues(_digest({
      'incomplete_turn': {'count': 0, 'severity': 'error'},
      'abnormal_stop': null,
      'orphan_tool_result': 'nope',
      'mixed_id_shape': {'count': 1, 'severity': 'info', 'sample_seqs': [1]},
    }));
    expect(got.classes.map((c) => c.cls).toList(), ['mixed_id_shape']);
    expect(got.total, 1);
  });

  test('an unknown severity degrades to warning rather than dropping the row',
      () {
    final got = readDigestIssues(_digest({
      'future_rule': {'count': 1, 'severity': 'catastrophe', 'sample_seqs': [1]},
    }));
    expect(got.classes.single.severity, 'warning');
  });

  test('the hub rollup wins over the client table, else worst is computed', () {
    // So a severity this client cannot rank yet still tints from the server.
    final fromWire = readDigestIssues(_digest(
      {'future_rule': {'count': 1, 'severity': 'info', 'sample_seqs': [1]}},
      worst: 'error',
    ));
    expect(fromWire.worst, 'error');

    final computed = readDigestIssues(_digest({
      'mixed_id_shape': {'count': 5, 'severity': 'info', 'sample_seqs': [1]},
      'orphan_tool_result': {'count': 1, 'severity': 'warning', 'sample_seqs': [2]},
    }));
    expect(computed.worst, 'warning');
  });
}
