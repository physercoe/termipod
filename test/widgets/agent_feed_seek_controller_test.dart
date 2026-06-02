import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/agent_feed.dart';

// P2 (agent-run-analysis-mode): the jump channel from the analysis dashboard
// down into the feed. The feed dedups on the generation counter, so these pin
// that seekTo always advances it (even for a repeat seq) and notifies — the
// contract the feed's _onSeekRequest relies on to re-fire a second tap on the
// same error.
void main() {
  group('AgentFeedSeekController', () {
    test('seekTo records the seq and notifies', () {
      final c = AgentFeedSeekController();
      var notified = 0;
      c.addListener(() => notified++);

      expect(c.seq, isNull);
      c.seekTo(42);
      expect(c.seq, 42);
      expect(notified, 1);
    });

    test('generation advances on every seekTo, including a repeat seq', () {
      final c = AgentFeedSeekController();
      final g0 = c.generation;

      c.seekTo(7);
      final g1 = c.generation;
      expect(g1, greaterThan(g0));

      // Same seq again must still advance the generation so the feed treats
      // it as a fresh request (re-jump on a second tap).
      c.seekTo(7);
      expect(c.generation, greaterThan(g1));
      expect(c.seq, 7);
    });
  });
}
