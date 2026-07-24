import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/team/spawn_steward_sheet.dart';

// #378 — the driving-mode picker's options source: the modes a steward
// template can actually run (its driving_mode ∪ fallback_modes), so an
// impossible mode can't leave the picker and 400 the spawn hub-side.
void main() {
  group('templateRunnableModes', () {
    test('driving_mode only', () {
      const yaml = 'driving_mode: M1\nbackend:\n  cmd: kimi --yolo\n';
      expect(templateRunnableModes(yaml), ['M1']);
    });

    test('driving_mode + inline fallback list, order preserved', () {
      const yaml = 'driving_mode: M1\nfallback_modes: [M4]\n'
          'backend:\n  kind: kimi-code-ts\n';
      expect(templateRunnableModes(yaml), ['M1', 'M4']);
    });

    test('claude M2 template shape', () {
      const yaml = 'driving_mode: M2\nfallback_modes: [M4]\n'
          'backend:\n  cmd: "claude --model {{model}} {{permission_flag}}"\n';
      expect(templateRunnableModes(yaml), ['M2', 'M4']);
    });

    test('duplicate fallback collapses', () {
      const yaml = 'driving_mode: M4\nfallback_modes: [M4]\n';
      expect(templateRunnableModes(yaml), ['M4']);
    });

    test('multi-entry fallback list with spaces', () {
      const yaml = 'driving_mode: M1\nfallback_modes: [M2, M4]\n';
      expect(templateRunnableModes(yaml), ['M1', 'M2', 'M4']);
    });

    test('no mode info → empty (picker hides itself)', () {
      const yaml = 'backend:\n  cmd: echo hi\n';
      expect(templateRunnableModes(yaml), isEmpty);
    });
  });
}
