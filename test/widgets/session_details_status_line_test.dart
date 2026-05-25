import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/session_details_sheet.dart';

// Tests for ADR-036 v1.0.706 polish — the four statusLine accessors
// that drive the SESSION STATE section of showSessionDetailsSheet.
//
//   statusLineEffortLevel       — 'low'/'medium'/'high'/'xhigh' (string)
//   statusLineOutputStyleName   — 'default'/'concise'/...  (string)
//   statusLineThinkingEnabled   — bool? (null = absent)
//   statusLineFastMode          — bool? (null = absent)
//
// The null-vs-explicit-false distinction is load-bearing for the
// `thinking` + `fast_mode` rows: older claude versions don't ship
// those fields at all, and rendering "thinking: off" on those would
// be a guess. The reducer + the section's row gate must agree on
// "absent → no row at all".

void main() {
  group('statusLineEffortLevel', () {
    test('returns empty when statusLine is null', () {
      expect(statusLineEffortLevel(null), '');
    });

    test('extracts level from nested map shape', () {
      // The current claude-code schema (2.1.150 probe) ships effort
      // as {level: "xhigh"} — see ADR-036 D7.
      expect(
        statusLineEffortLevel({
          'effort': {'level': 'xhigh'},
        }),
        'xhigh',
      );
    });

    test('accepts bare-string shape (older versions)', () {
      // Older binaries shipped a flat string. The accessor accepts
      // both so we don't drop signal across a version skew.
      expect(statusLineEffortLevel({'effort': 'high'}), 'high');
    });

    test('returns empty when field is absent or wrong type', () {
      expect(statusLineEffortLevel(const {}), '');
      expect(statusLineEffortLevel({'effort': 42}), '');
      expect(statusLineEffortLevel({'effort': {'level': 7}}), '');
    });
  });

  group('statusLineOutputStyleName', () {
    test('returns empty when statusLine is null', () {
      expect(statusLineOutputStyleName(null), '');
    });

    test('extracts name from nested map shape', () {
      // statusLine ships output_style as {name: "default"} (the
      // hostrunner stdio driver flattens session.init's variant to a
      // string, but statusLine is closer to the raw claude shape).
      expect(
        statusLineOutputStyleName({
          'output_style': {'name': 'concise'},
        }),
        'concise',
      );
    });

    test('accepts bare-string shape', () {
      expect(
        statusLineOutputStyleName({'output_style': 'default'}),
        'default',
      );
    });

    test('returns empty when absent or wrong type', () {
      expect(statusLineOutputStyleName(const {}), '');
      expect(statusLineOutputStyleName({'output_style': true}), '');
    });
  });

  group('statusLineThinkingEnabled', () {
    test('returns null when statusLine is null', () {
      // The hide-the-row-when-absent contract — null must propagate
      // so the sheet's row gate (`if (thinking != null)`) drops the
      // row cleanly.
      expect(statusLineThinkingEnabled(null), isNull);
    });

    test('returns null when field is absent', () {
      // Older claude versions don't ship thinking at all. The sheet
      // must NOT render "thinking: off" — that would be a guess.
      expect(statusLineThinkingEnabled(const {}), isNull);
    });

    test('extracts enabled from nested map', () {
      expect(
        statusLineThinkingEnabled({
          'thinking': {'enabled': true},
        }),
        true,
      );
      expect(
        statusLineThinkingEnabled({
          'thinking': {'enabled': false},
        }),
        false,
      );
    });

    test('accepts bare-bool shape', () {
      expect(statusLineThinkingEnabled({'thinking': true}), true);
      expect(statusLineThinkingEnabled({'thinking': false}), false);
    });

    test('returns null for wrong types (e.g. enabled: "yes")', () {
      // Defensive: a future driver bug shipping "yes"/"no" strings
      // must NOT collapse to a true/false guess. Hide the row
      // until the wire is clean again.
      expect(statusLineThinkingEnabled({'thinking': {'enabled': 'yes'}}),
          isNull);
      expect(statusLineThinkingEnabled({'thinking': 1}), isNull);
    });
  });

  group('statusLineFastMode', () {
    test('returns null when statusLine is null', () {
      expect(statusLineFastMode(null), isNull);
    });

    test('returns null when field is absent', () {
      expect(statusLineFastMode(const {}), isNull);
    });

    test('returns the bool when present', () {
      expect(statusLineFastMode({'fast_mode': true}), true);
      expect(statusLineFastMode({'fast_mode': false}), false);
    });

    test('returns null for non-bool values', () {
      // Same null-vs-explicit-false rule as thinking. A "1" must NOT
      // render as "fast mode: on".
      expect(statusLineFastMode({'fast_mode': 1}), isNull);
      expect(statusLineFastMode({'fast_mode': 'true'}), isNull);
    });
  });
}
