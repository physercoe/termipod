import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/transcript/seek_controller.dart';

// P2 (agent-run-analysis-mode): the jump channel from the analysis dashboard
// down into the feed. The feed dedups on the generation counter, so these pin
// that seekTo always advances it (even for a repeat seq) and notifies — the
// contract the feed's _onSeekRequest relies on to re-fire a second tap on the
// same error.
void main() {
  group('TranscriptSeekController', () {
    test('seekTo records the seq and notifies', () {
      final c = TranscriptSeekController();
      var notified = 0;
      c.addListener(() => notified++);

      expect(c.seq, isNull);
      c.seekTo(42);
      expect(c.seq, 42);
      expect(notified, 1);
    });

    test('generation advances on every seekTo, including a repeat seq', () {
      final c = TranscriptSeekController();
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

    test('carries the optional ts for the random-access window reset', () {
      final c = TranscriptSeekController();

      // A Turns-index jump supplies the anchor's timestamp.
      c.seekTo(12, ts: '2026-06-01T00:00:01Z');
      expect(c.seq, 12);
      expect(c.ts, '2026-06-01T00:00:01Z');

      // A seq-only jump (the Errors stat) clears it → page-walk fallback.
      c.seekTo(20);
      expect(c.seq, 20);
      expect(c.ts, isNull);
    });
  });
}
