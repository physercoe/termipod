import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_muxpod/screens/settings/settings_screen.dart';

import '../helpers/test_helpers.dart';

Widget _buildApp() {
  return const ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: SettingsScreen(),
    ),
  );
}

void main() {
  group('SettingsScreen', () {
    testWidgets('displays Settings title', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
    });

    testWidgets('displays Adjust Mode setting', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      expect(find.text('Adjust Mode'), findsWidgets);
      expect(find.text('Auto Fit'), findsWidgets);
    });

    testWidgets('displays Haptic Feedback toggle', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      // Haptic Feedback is in the Behavior section - may need scroll
      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(find.text('Haptic Feedback'), 200, scrollable: scrollable);
      expect(find.text('Haptic Feedback'), findsOneWidget);
    });

    testWidgets('displays Keep Screen On toggle', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(find.text('Keep Screen On'), 200, scrollable: scrollable);
      expect(find.text('Keep Screen On'), findsOneWidget);
    });

    testWidgets('behavior toggles are interactive', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(find.text('Haptic Feedback'), 200, scrollable: scrollable);
      final hapticSwitch = find.ancestor(
        of: find.text('Haptic Feedback'),
        matching: find.byType(SwitchListTile),
      );
      expect(hapticSwitch, findsOneWidget);

      await tester.scrollUntilVisible(find.text('Keep Screen On'), 200, scrollable: scrollable);
      final keepScreenSwitch = find.ancestor(
        of: find.text('Keep Screen On'),
        matching: find.byType(SwitchListTile),
      );
      expect(keepScreenSwitch, findsOneWidget);
    });

    testWidgets('displays Source Code link', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(find.text('Source Code'), 200, scrollable: scrollable);
      expect(find.text('Source Code'), findsOneWidget);
      expect(find.text('github.com/physercoe/mux-pod'), findsOneWidget);
    });

    testWidgets('displays Image Transfer settings', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();

      final scrollable = find.byType(Scrollable).first;

      // Image Transfer section header — scroll with larger delta to reach bottom
      await tester.scrollUntilVisible(find.text('Image Transfer'), 500, scrollable: scrollable, maxScrolls: 50);
      expect(find.text('Image Transfer'), findsWidgets);

      // Individual settings
      await tester.scrollUntilVisible(find.text('Remote Path'), 200, scrollable: scrollable);
      expect(find.text('Remote Path'), findsWidgets);

      await tester.scrollUntilVisible(find.text('Output Format'), 200, scrollable: scrollable);
      expect(find.text('Output Format'), findsWidgets);

      await tester.scrollUntilVisible(find.text('Path Format'), 200, scrollable: scrollable);
      expect(find.text('Path Format'), findsWidgets);

      await tester.scrollUntilVisible(find.text('Auto Enter'), 200, scrollable: scrollable);
      expect(find.text('Auto Enter'), findsWidgets);

      await tester.scrollUntilVisible(find.text('Bracketed Paste'), 200, scrollable: scrollable);
      expect(find.text('Bracketed Paste'), findsWidgets);
    });
  });
}
