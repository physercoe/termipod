import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/phase_ribbon.dart';

import '../helpers/test_helpers.dart';

void main() {
  group('PhaseRibbon', () {
    testWidgets('renders one chip per phase with the current highlighted',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: Scaffold(
            body: PhaseRibbon(
              phases: ['idea', 'lit-review', 'method'],
              currentPhase: 'lit-review',
            ),
          ),
        ),
      );
      expect(find.text('Idea'), findsOneWidget);
      expect(find.text('Lit Review'), findsOneWidget);
      expect(find.text('Method'), findsOneWidget);
    });

    testWidgets('emits onTap with the chip\'s phase value', (tester) async {
      String? tapped;
      await tester.pumpWidget(
        MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: Scaffold(
            body: PhaseRibbon(
              phases: const ['idea', 'method'],
              currentPhase: 'idea',
              onTap: (p) => tapped = p,
            ),
          ),
        ),
      );
      await tester.tap(find.text('Method'));
      expect(tapped, 'method');
    });

    testWidgets('renders nothing when phases is empty', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: Scaffold(
            body: PhaseRibbon(phases: [], currentPhase: ''),
          ),
        ),
      );
      expect(find.byType(InkWell), findsNothing);
    });
  });
}
