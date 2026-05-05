import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/activity_snippet.dart';

void main() {
  group('activityActionLabel', () {
    test('maps W1 lifecycle kinds to human labels', () {
      expect(activityActionLabel('project.phase_advanced'), 'Phase advanced');
      expect(activityActionLabel('project.phase_reverted'), 'Phase reverted');
      expect(activityActionLabel('project.phase_set'), 'Phase set');
    });

    test('maps W5b/W6 future kinds to human labels', () {
      expect(activityActionLabel('deliverable.ratify'), 'Deliverable ratified');
      expect(activityActionLabel('criterion.met'), 'Criterion met');
    });

    test('falls through for unknown actions', () {
      expect(activityActionLabel('foo.bar'), 'foo.bar');
      expect(activityActionLabel(''), '');
    });
  });

  group('shortRelativeTs', () {
    test('returns empty for empty input', () {
      expect(shortRelativeTs(''), '');
    });

    test('returns "now" within 30 seconds', () {
      final ts = DateTime.now().toUtc().toIso8601String();
      expect(shortRelativeTs(ts), 'now');
    });

    test('returns minute granularity for sub-hour gaps', () {
      final ts = DateTime.now()
          .toUtc()
          .subtract(const Duration(minutes: 7))
          .toIso8601String();
      expect(shortRelativeTs(ts), '7m');
    });

    test('returns raw value for unparseable input', () {
      expect(shortRelativeTs('not-a-timestamp'), 'not-a-timestamp');
    });
  });
}
