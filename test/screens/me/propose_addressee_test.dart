import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_addressee.dart';

// ADR-030 W19.5 — propose-card variant-selector helpers. These are the
// load-bearing predicates that pick between the Approve/Reject card and
// the Override/View-source stalled card. A regression here would silently
// suppress the principal's override surface, which is the load-bearing
// safety valve for D-7 Option 2′ ("decision stays, signal walks").

void main() {
  group('isAddresseeOfPropose', () {
    test('primary variant when assigned_tier matches viewer tier', () {
      final row = {'assigned_tier': 'principal'};
      expect(isAddresseeOfPropose(row, 'principal'), isTrue);
    });

    test('stalled variant when assigned_tier doesn\'t match', () {
      final row = {'assigned_tier': 'project-steward'};
      // Principal viewing a row addressed to a project steward → stalled.
      expect(isAddresseeOfPropose(row, 'principal'), isFalse);
    });

    test('legacy row (no assigned_tier) → primary fallback', () {
      // Pre-ADR-030 rows have no assigned_tier; render primary so the
      // viewer can still act on them via the existing approve/reject UI.
      expect(isAddresseeOfPropose({}, 'principal'), isTrue);
      expect(isAddresseeOfPropose({'assigned_tier': ''}, 'principal'), isTrue);
    });

    test('viewer with empty tier never matches a tiered row', () {
      // Defensive: an empty viewer tier means we can't claim addressee
      // status against any tiered row. Avoids accidentally granting
      // primary variant when the tier resolution failed upstream.
      final row = {'assigned_tier': 'project-steward'};
      expect(isAddresseeOfPropose(row, ''), isFalse);
    });

    test('cross-tier comparison is exact-match (no fallthrough)', () {
      // 'general-steward' viewing a 'project-steward' row → stalled,
      // even though the principal-ward escalation ladder might bring
      // it to them eventually. The predicate only judges the CURRENT
      // assigned tier; the escalation_state column drives stalled
      // surfacing separately.
      final row = {'assigned_tier': 'project-steward'};
      expect(isAddresseeOfPropose(row, 'general-steward'), isFalse);
    });
  });

  group('isStalledPropose', () {
    test('escalation_state="none" → not stalled', () {
      expect(isStalledPropose({'escalation_state': 'none'}), isFalse);
    });

    test('missing escalation_state → not stalled (defaults to none)', () {
      expect(isStalledPropose({}), isFalse);
    });

    test('escalated_steward → stalled', () {
      expect(isStalledPropose({'escalation_state': 'escalated_steward'}),
          isTrue);
    });

    test('escalated_principal → stalled', () {
      expect(isStalledPropose({'escalation_state': 'escalated_principal'}),
          isTrue);
    });

    test('empty string escalation_state → not stalled', () {
      // Belt-and-braces: COALESCE on the hub side returns 'none' by
      // default, but defensively treat empty as not-stalled too.
      expect(isStalledPropose({'escalation_state': ''}), isFalse);
    });
  });

  group('stalledPillLabel', () {
    test('returns "Stuck" when escalated', () {
      expect(
          stalledPillLabel({'escalation_state': 'escalated_principal'}),
          'Stuck');
    });

    test('returns empty string when not escalated', () {
      expect(stalledPillLabel({'escalation_state': 'none'}), '');
    });
  });
}
