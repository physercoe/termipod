import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/deliverable_state_pip.dart';

void main() {
  group('parseDeliverableState', () {
    test('parses canonical strings', () {
      expect(parseDeliverableState('draft'), DeliverableState.draft);
      expect(parseDeliverableState('in-review'), DeliverableState.inReview);
      expect(parseDeliverableState('in_review'), DeliverableState.inReview);
      expect(parseDeliverableState('ratified'), DeliverableState.ratified);
    });

    test('case-insensitive', () {
      expect(parseDeliverableState('Ratified'), DeliverableState.ratified);
      expect(parseDeliverableState('IN-REVIEW'), DeliverableState.inReview);
    });

    test('null and unknown fall back to draft', () {
      expect(parseDeliverableState(null), DeliverableState.draft);
      expect(parseDeliverableState(''), DeliverableState.draft);
      expect(parseDeliverableState('mystery'), DeliverableState.draft);
    });
  });

  group('DeliverableStatePip', () {
    Future<void> pumpPip(WidgetTester tester, DeliverableState state) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Center(child: DeliverableStatePip(state: state)),
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('renders draft label', (t) async {
      await pumpPip(t, DeliverableState.draft);
      expect(find.text('draft'), findsOneWidget);
    });

    testWidgets('renders in-review label', (t) async {
      await pumpPip(t, DeliverableState.inReview);
      expect(find.text('in review'), findsOneWidget);
    });

    testWidgets('renders ratified label', (t) async {
      await pumpPip(t, DeliverableState.ratified);
      expect(find.text('ratified'), findsOneWidget);
    });

    testWidgets('showLabel:false renders glyph only', (t) async {
      await t.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Center(
              child: DeliverableStatePip(
                state: DeliverableState.ratified,
                showLabel: false,
              ),
            ),
          ),
        ),
      );
      await t.pumpAndSettle();
      expect(find.text('ratified'), findsNothing);
    });
  });
}
