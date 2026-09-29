import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/action_bar_provider.dart';
import 'package:termipod/providers/settings_provider.dart';
import 'package:termipod/widgets/custom_keyboard.dart';
import 'package:termipod/widgets/floating_joystick.dart';
import 'package:termipod/widgets/navigation_pad.dart';
import 'package:termipod/widgets/action_bar/profile_sheet.dart';

import '../helpers/test_helpers.dart';

void main() {
  late ProviderContainer container;
  late List<String> keys;
  setUp(() {
    SharedPreferences.setMockInitialValues({'settings_nav_pad_mode': 'off'});
    container = ProviderContainer();
    keys = [];
  });
  tearDown(() => container.dispose());

  Future<void> mount(WidgetTester tester) async {
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: Scaffold(
            body: Column(
              children: [
                NavigationPad(
                  onKeyPressed: keys.add,
                  onSpecialKeyPressed: keys.add,
                ),
                CustomKeyboard(
                  onKeyPressed: keys.add,
                  onSpecialKeyPressed: keys.add,
                  haptic: false,
                ),
                Consumer(
                  builder: (context, ref, _) => TextButton(
                    onPressed: () => ProfileSheet.show(
                      context,
                      ref: ref,
                      onKeyTap: keys.add,
                      onSpecialKeyTap: keys.add,
                    ),
                    child: const Text('Open palette'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> shift(WidgetTester tester) async {
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.pump();
  }

  testWidgets('Shift + Left, Enter, Tab and printable text', (tester) async {
    await mount(tester);
    await shift(tester);
    await tester.tap(find.byIcon(Icons.chevron_left));
    await tester.tap(find.byIcon(Icons.chevron_left));
    await shift(tester);
    await tester.tap(find.text('↵'));
    await shift(tester);
    await tester.tap(find.text('Tab'));
    await shift(tester);
    await tester.tap(find.text('A'));
    await tester.pumpAndSettle();
    expect(keys, ['S-Left', 'Left', 'S-Enter', 'S-Tab', 'A']);
  });

  testWidgets('held arrow repeats the same one-shot chord', (tester) async {
    await mount(tester);
    await shift(tester);
    final gesture = await tester.startGesture(
      tester.getCenter(find.byIcon(Icons.chevron_left)),
    );
    await tester.pump(const Duration(milliseconds: 900));
    await gesture.up();
    await tester.pumpAndSettle();
    expect(keys.length, greaterThan(1));
    expect(keys, everyElement('S-Left'));
    await tester.tap(find.byIcon(Icons.chevron_left));
    await tester.pumpAndSettle();
    expect(keys.last, 'Left');
  });

  testWidgets('default navigation pad uses keyboard Shift, including hold', (
    tester,
  ) async {
    await mount(tester);
    await container.read(settingsProvider.notifier).setNavPadMode('compact');
    await tester.pumpAndSettle();
    await shift(tester);
    await tester.tap(find.byIcon(Icons.keyboard_arrow_left));
    await shift(tester);
    final gesture = await tester.startGesture(
      tester.getCenter(find.byIcon(Icons.keyboard_arrow_left)),
    );
    await tester.pump(const Duration(milliseconds: 1000));
    await gesture.up();
    await tester.pumpAndSettle();
    expect(keys.length, greaterThan(1));
    expect(keys, everyElement('S-Left'));
  });

  testWidgets('hardware Ctrl + Shift + Left is preserved', (tester) async {
    await mount(tester);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.arrowLeft);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    expect(keys, ['C-S-Left']);
  });

  testWidgets('locked Shift survives arrows until explicitly unlocked', (
    tester,
  ) async {
    await mount(tester);
    await shift(tester);
    await shift(tester);
    await tester.tap(find.byIcon(Icons.chevron_left));
    await tester.tap(find.byIcon(Icons.chevron_right));
    await tester.tap(find.byIcon(Icons.keyboard_capslock));
    await tester.tap(find.byIcon(Icons.chevron_left));
    await tester.pumpAndSettle();
    expect(keys, ['S-Left', 'S-Right', 'Left']);
  });

  testWidgets('floating joystick preserves modifiers for taps and repeats', (
    tester,
  ) async {
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(
          home: Scaffold(
            body: Stack(
              children: [
                FloatingJoystick(onSpecialKeyPressed: keys.add, haptic: false),
              ],
            ),
          ),
        ),
      ),
    );
    final notifier = container.read(actionBarProvider.notifier);
    await tester.pumpAndSettle();
    final left =
        tester.getCenter(find.byType(FloatingJoystick)) - const Offset(45, 0);
    notifier.toggleShift();
    await tester.tapAt(left);
    await tester.pumpAndSettle();
    expect(keys, ['S-Left']);
    notifier.toggleShift();
    final gesture = await tester.startGesture(left);
    await tester.pump(const Duration(milliseconds: 1000));
    await gesture.up();
    await tester.pumpAndSettle();
    expect(keys.length, greaterThan(2));
    expect(keys, everyElement('S-Left'));
  });

  testWidgets('palette applies armed Shift to navigation chips', (
    tester,
  ) async {
    await mount(tester);
    await shift(tester);
    await tester.tap(find.text('Open palette'));
    await tester.pumpAndSettle();
    // The palette renders navigation labels as text, unlike the keyboard icons.
    final left = find.descendant(
      of: find.byType(ProfileSheet),
      matching: find.text('←'),
    );
    await tester.ensureVisible(left);
    await tester.tap(left);
    await tester.pumpAndSettle();
    expect(keys, ['S-Left']);
    expect(container.read(actionBarProvider).shiftArmed, isFalse);
  });
}
