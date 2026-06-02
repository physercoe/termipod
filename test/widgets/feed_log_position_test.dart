import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/agent_feed.dart';

// P2 (agent-run-analysis-mode): the monotonic "event N of M" position. The
// readout must reflect the whole run (M from the digest), not just the loaded
// window, and N must track the viewport across the loaded slice. These pin the
// interpolation, the digest-vs-loaded fallback for M, and the clamp.
void main() {
  group('feedLogPosition', () {
    test('null until something is loaded', () {
      expect(
        feedLogPosition(minSeq: 0, maxSeq: 0, viewFrac: 1.0),
        isNull,
      );
    });

    test('N tracks the viewport across the loaded slice; M is the run total',
        () {
      // Loaded tail slice [801, 1000] of a 5000-event run.
      final tail = feedLogPosition(
          minSeq: 801, maxSeq: 1000, viewFrac: 1.0, totalEventCount: 5000);
      expect(tail, isNotNull);
      expect(tail!.n, 1000); // viewport at the tail → newest loaded seq
      expect(tail.m, 5000); // M is the run total, not the loaded max

      final top = feedLogPosition(
          minSeq: 801, maxSeq: 1000, viewFrac: 0.0, totalEventCount: 5000);
      expect(top!.n, 801); // viewport at the top of the loaded window

      final mid = feedLogPosition(
          minSeq: 801, maxSeq: 1000, viewFrac: 0.5, totalEventCount: 5000);
      // Monotonic: top ≤ mid ≤ tail.
      expect(mid!.n, inInclusiveRange(801, 1000));
      expect(mid.n, greaterThan(top.n));
      expect(mid.n, lessThan(tail.n));
    });

    test('M falls back to the newest loaded seq before the digest resolves',
        () {
      final pos = feedLogPosition(minSeq: 1, maxSeq: 200, viewFrac: 1.0);
      expect(pos!.m, 200);
      expect(pos.n, 200);
    });

    test('N is clamped into [1, M] (multi-agent seq can exceed the total)',
        () {
      // A resumed session's per-agent seq can run past the run total.
      final pos = feedLogPosition(
          minSeq: 1, maxSeq: 400, viewFrac: 1.0, totalEventCount: 300);
      expect(pos!.n, lessThanOrEqualTo(300));
      expect(pos.m, 300);
    });
  });
}
