import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/stalled_decisions_digest.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W19.6-mobile — top-of-Me stalled-decisions digest.
//
// Covers:
//   * pure-function counters (hasStalledDecisions, stalledDecisionsCount,
//     stalledOverDayDecisionsCount) — predicate-only tests, no widgets
//   * the StalledDecisionsDigest widget — renders/hidden conditions,
//     subtitle copy variants, filter toggle on tap

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(body: child),
    ),
  );
}

Map<String, dynamic> _stalled({String state = 'escalated_principal'}) {
  return {
    'id': 'att-${state}-${DateTime.now().microsecondsSinceEpoch}',
    'kind': 'propose',
    'change_kind': 'task.set_status',
    'assigned_tier': 'project-steward',
    'escalation_state': state,
  };
}

Map<String, dynamic> _notStalled() {
  return {
    'id': 'att-fresh',
    'kind': 'propose',
    'change_kind': 'task.set_status',
    'assigned_tier': 'principal',
    'escalation_state': 'none',
  };
}

void main() {
  group('counters — pure functions', () {
    test('hasStalledDecisions returns false for empty list', () {
      expect(hasStalledDecisions(const []), isFalse);
    });

    test('hasStalledDecisions returns false when no rows are escalated', () {
      expect(hasStalledDecisions([_notStalled()]), isFalse);
    });

    test('hasStalledDecisions returns true when at least one row is stalled',
        () {
      expect(hasStalledDecisions([_notStalled(), _stalled()]), isTrue);
    });

    test('stalledDecisionsCount counts only stalled rows', () {
      final items = [
        _notStalled(),
        _stalled(),
        _stalled(state: 'escalated_steward'),
      ];
      expect(stalledDecisionsCount(items), 2);
    });

    test('stalledOverDayDecisionsCount counts only escalated_principal', () {
      final items = [
        _stalled(state: 'escalated_principal'),
        _stalled(state: 'escalated_steward'),
        _notStalled(),
      ];
      expect(stalledOverDayDecisionsCount(items), 1);
    });
  });

  group('StalledDecisionsDigest widget', () {
    testWidgets('renders nothing when stalledCount is 0', (tester) async {
      await tester.pumpWidget(_wrap(
        const StalledDecisionsDigest(
          stalledCount: 0,
          stalledOverDayCount: 0,
        ),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Stalled decisions'), findsNothing);
      expect(find.text('Showing stalled decisions'), findsNothing);
    });

    testWidgets('renders count badge + header when stalledCount > 0',
        (tester) async {
      await tester.pumpWidget(_wrap(
        const StalledDecisionsDigest(
          stalledCount: 3,
          stalledOverDayCount: 0,
        ),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Stalled decisions'), findsOneWidget);
      expect(find.text('3'), findsOneWidget);
    });

    testWidgets(
        'subtitle splits younger vs with-you when both counts present',
        (tester) async {
      await tester.pumpWidget(_wrap(
        const StalledDecisionsDigest(
          stalledCount: 3,
          stalledOverDayCount: 1,
        ),
      ));
      await tester.pumpAndSettle();

      // 3 stalled total, 1 with principal → "2 stalled at stewards · 1
      // stalled with you. Tap to filter."
      expect(find.textContaining('2 stalled at stewards'), findsOneWidget);
      expect(find.textContaining('1 stalled with you'), findsOneWidget);
    });

    testWidgets('tap toggles stalledFilterProvider', (tester) async {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            localizationsDelegates: testLocalizationsDelegates,
            supportedLocales: testSupportedLocales,
            home: const Scaffold(
              body: StalledDecisionsDigest(
                stalledCount: 1,
                stalledOverDayCount: 1,
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(container.read(stalledFilterProvider), isFalse);
      await tester.tap(find.text('Stalled decisions'));
      await tester.pumpAndSettle();

      expect(container.read(stalledFilterProvider), isTrue);
      // Header label flips to active state.
      expect(find.text('Showing stalled decisions'), findsOneWidget);
    });
  });
}
