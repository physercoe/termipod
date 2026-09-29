import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/action_bar_provider.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late ProviderContainer container;
  late ActionBarNotifier notifier;
  setUp(() async {
    SharedPreferences.setMockInitialValues({});
    container = ProviderContainer();
    notifier = container.read(actionBarProvider.notifier);
    await Future<void>.delayed(Duration.zero);
  });
  tearDown(() => container.dispose());

  test('Shift composes arrows and consumes only one-shot modifiers', () {
    notifier.toggleShift();
    expect(notifier.applyModifiers('Left'), 'S-Left');
    expect(notifier.applyModifiers('Left'), isNull);
    notifier.toggleShift();
    notifier.toggleShift();
    notifier.toggleCtrl();
    expect(notifier.applyModifiers('Left'), 'C-S-Left');
    expect(notifier.applyModifiers('Right'), 'S-Right');
    notifier.toggleShift();
    expect(notifier.applyModifiers('Left'), isNull);
  });

  test('physical modifiers and existing chords merge without duplicates', () {
    notifier.toggleAlt();
    expect(
      notifier.applyModifiers('S-Left', ctrl: true, shift: true),
      'C-M-S-Left',
    );
    expect(notifier.applyModifiers('Tab', shift: true), 'S-Tab');
    expect(notifier.applyModifiers('Enter', shift: true), 'S-Enter');
    notifier.toggleCtrl();
    expect(notifier.applyModifiers('C-c'), 'C-c');
  });

  test('Shift preserves printable case and punctuation', () {
    for (final pair in {
      'a': 'A',
      ',': '<',
      '.': '>',
      "'": '"',
      '/': '?',
      '\\': '|',
      '1': '!',
      '<': '<',
      'A': 'A',
    }.entries) {
      notifier.toggleShift();
      expect(notifier.applyModifiers(pair.key), pair.value);
    }
    notifier.toggleShift();
    notifier.toggleAlt();
    expect(notifier.applyModifiers('a'), 'M-A');
    notifier.toggleShift();
    expect(notifier.applyModifiers(' '), 'Space');
  });

  test('native actions and multi-key macros are not corrupted', () {
    notifier.toggleShift();
    expect(notifier.applyModifiers('termipod:tmux:kill-pane'), isNull);
    expect(notifier.applyModifiers('Escape Escape'), isNull);
    expect(container.read(actionBarProvider).shiftArmed, isTrue);
    notifier.resetModifiers();
    expect(container.read(actionBarProvider).shiftArmed, isFalse);
  });
}
