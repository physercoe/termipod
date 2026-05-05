import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/criterion_state_pip.dart';

void main() {
  group('parseCriterionState', () {
    test('canonical strings', () {
      expect(parseCriterionState('pending'), CriterionState.pending);
      expect(parseCriterionState('met'), CriterionState.met);
      expect(parseCriterionState('failed'), CriterionState.failed);
      expect(parseCriterionState('waived'), CriterionState.waived);
    });

    test('case-insensitive', () {
      expect(parseCriterionState('MET'), CriterionState.met);
      expect(parseCriterionState('Failed'), CriterionState.failed);
    });

    test('null + unknown fall back to pending', () {
      expect(parseCriterionState(null), CriterionState.pending);
      expect(parseCriterionState(''), CriterionState.pending);
      expect(parseCriterionState('mystery'), CriterionState.pending);
    });
  });

  group('CriterionStatePip', () {
    Future<void> pump(WidgetTester t, CriterionState state) =>
        t.pumpWidget(MaterialApp(
          home: Scaffold(body: Center(child: CriterionStatePip(state: state))),
        ));

    testWidgets('renders pending label', (t) async {
      await pump(t, CriterionState.pending);
      expect(find.text('pending'), findsOneWidget);
    });

    testWidgets('renders met label', (t) async {
      await pump(t, CriterionState.met);
      expect(find.text('met'), findsOneWidget);
    });

    testWidgets('renders failed label', (t) async {
      await pump(t, CriterionState.failed);
      expect(find.text('failed'), findsOneWidget);
    });

    testWidgets('renders waived label', (t) async {
      await pump(t, CriterionState.waived);
      expect(find.text('waived'), findsOneWidget);
    });

    testWidgets('showLabel:false hides text', (t) async {
      await t.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: Center(
            child: CriterionStatePip(
              state: CriterionState.met,
              showLabel: false,
            ),
          ),
        ),
      ));
      expect(find.text('met'), findsNothing);
    });
  });
}
