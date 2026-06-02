import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/agent_feed.dart';

// P2 (agent-run-analysis-mode): the full-run minimap anchors. Positions must
// be the run ordinal (seq / total), error ticks must paint last (on top of
// turn ticks), and an unresolved digest (total <= 0) must yield nothing.
void main() {
  group('feedRunAnchorMarks', () {
    test('positions anchors by run ordinal; errors come last', () {
      final marks = feedRunAnchorMarks(
        errorSeqs: [50],
        turnSeqs: [1, 100],
        total: 100,
      );
      // turns first, then errors → [turn 1, turn 100, error 50].
      expect(marks.length, 3);
      expect(marks[0].seq, 1);
      expect(marks[0].isError, isFalse);
      expect(marks[0].frac, closeTo(0.01, 1e-9));
      expect(marks[1].seq, 100);
      expect(marks[1].frac, closeTo(1.0, 1e-9));
      // Error painted last so it sits on top.
      expect(marks.last.seq, 50);
      expect(marks.last.isError, isTrue);
      expect(marks.last.frac, closeTo(0.5, 1e-9));
    });

    test('frac is clamped into [0, 1] for an out-of-range seq', () {
      final marks =
          feedRunAnchorMarks(errorSeqs: [500], turnSeqs: const [], total: 100);
      expect(marks.single.frac, 1.0);
    });

    test('no anchors before the digest resolves (total <= 0)', () {
      expect(
        feedRunAnchorMarks(errorSeqs: [1, 2], turnSeqs: [3], total: 0),
        isEmpty,
      );
    });
  });
}
