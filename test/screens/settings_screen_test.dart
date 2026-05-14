import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/settings/settings_screen.dart';

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

// Tap a category card on the Settings home and pump frames until the
// destination sub-screen has settled. Each new test that probes a
// setting that lived in the pre-W1 flat list MUST go through this
// helper first — the IA is now home → category, not one flat scroll.
Future<void> _openCategory(WidgetTester tester, String label) async {
  await tester.pumpWidget(_buildApp());
  await tester.pumpAndSettle();
  await tester.tap(find.text(label));
  await tester.pumpAndSettle();
}

void main() {
  group('SettingsScreen home', () {
    testWidgets('displays Settings title', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
    });

    testWidgets('renders six category cards', (tester) async {
      await tester.pumpWidget(_buildApp());
      await tester.pumpAndSettle();
      // Display · Input · Files & Media · Data · System · About — the
      // ADR/discussion-locked taxonomy. If any card title is renamed,
      // update both this test AND the engine-capability matrix in
      // docs/reference/steward-templates.md.
      expect(find.text('Display'), findsOneWidget);
      expect(find.text('Input'), findsOneWidget);
      expect(find.text('Files & Media'), findsOneWidget);
      expect(find.text('Data'), findsOneWidget);
      expect(find.text('System'), findsOneWidget);
      expect(find.text('About'), findsOneWidget);
    });
  });

  group('SettingsScreen → Display', () {
    testWidgets('shows Adjust Mode setting after tapping Display',
        (tester) async {
      await _openCategory(tester, 'Display');
      expect(find.text('Adjust Mode'), findsWidgets);
      expect(find.text('Auto Fit'), findsWidgets);
    });
  });

  group('SettingsScreen → Input', () {
    testWidgets('shows Haptic Feedback toggle after tapping Input',
        (tester) async {
      await _openCategory(tester, 'Input');
      // Haptic now sits in Input alongside keep-screen-on and
      // invert-pane-nav (was the "Behavior" section pre-W1). May need
      // a scroll on smaller test viewports because Input now hosts
      // ~14 rows.
      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(
          find.text('Haptic Feedback'), 200,
          scrollable: scrollable);
      expect(find.text('Haptic Feedback'), findsOneWidget);
    });

    testWidgets('shows Keep Screen On toggle after tapping Input',
        (tester) async {
      await _openCategory(tester, 'Input');
      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(
          find.text('Keep Screen On'), 200,
          scrollable: scrollable);
      expect(find.text('Keep Screen On'), findsOneWidget);
    });

    testWidgets('behavior toggles are interactive', (tester) async {
      await _openCategory(tester, 'Input');
      final scrollable = find.byType(Scrollable).first;

      await tester.scrollUntilVisible(
          find.text('Haptic Feedback'), 200,
          scrollable: scrollable);
      final hapticSwitch = find.ancestor(
        of: find.text('Haptic Feedback'),
        matching: find.byType(SwitchListTile),
      );
      expect(hapticSwitch, findsOneWidget);

      await tester.scrollUntilVisible(
          find.text('Keep Screen On'), 200,
          scrollable: scrollable);
      final keepScreenSwitch = find.ancestor(
        of: find.text('Keep Screen On'),
        matching: find.byType(SwitchListTile),
      );
      expect(keepScreenSwitch, findsOneWidget);
    });
  });

  group('SettingsScreen → About', () {
    testWidgets('shows Source Code link after tapping About',
        (tester) async {
      await _openCategory(tester, 'About');
      final scrollable = find.byType(Scrollable).first;
      await tester.scrollUntilVisible(
          find.text('Source Code'), 200,
          scrollable: scrollable);
      expect(find.text('Source Code'), findsOneWidget);
      expect(find.text('github.com/physercoe/termipod'), findsOneWidget);
    });
  });

  // Image / file transfer settings live behind the Files & Media
  // category. Tests for those specific rows would require an inner
  // scroll inside a Files & Media sub-screen; covered by manual
  // device verification per the release-testing.md scenario.
}
