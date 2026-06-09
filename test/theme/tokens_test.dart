import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/theme/tokens.dart';

/// Pins the design-token scales (ADR-047). These tests fail if a scale value
/// changes, so any edit to the vocabulary is deliberate and reviewed.
void main() {
  group('Spacing', () {
    test('is a 4px grid', () {
      const values = [
        Spacing.s2,
        Spacing.s4,
        Spacing.s8,
        Spacing.s12,
        Spacing.s16,
        Spacing.s24,
        Spacing.s32,
      ];
      expect(values, [2, 4, 8, 12, 16, 24, 32]);
      // Every layout value (everything past the s2 hairline) is on the 4-grid.
      for (final v in values.where((v) => v != Spacing.s2)) {
        expect(v % 4, 0, reason: '$v is off the 4px grid');
      }
    });
  });

  group('Radii', () {
    test('is the M3 shape scale with md as default', () {
      expect(Radii.xs, 4);
      expect(Radii.sm, 8);
      expect(Radii.md, 12);
      expect(Radii.lg, 16);
      expect(Radii.stadium, 999);
    });

    test('border getters match their scalar values', () {
      expect(Radii.mdBorder, BorderRadius.circular(Radii.md));
      expect(Radii.stadiumBorder, BorderRadius.circular(Radii.stadium));
    });
  });

  group('FontSizes', () {
    test('is a 6-step scale with a 13 body floor', () {
      expect(FontSizes.label, 11);
      expect(FontSizes.caption, 12);
      expect(FontSizes.bodySmall, 13);
      expect(FontSizes.body, 14);
      expect(FontSizes.subtitle, 16);
      expect(FontSizes.title, 18);
      expect(FontSizes.titleLarge, 20);
    });

    test('body-readable sizes are at least 13', () {
      for (final size in [FontSizes.bodySmall, FontSizes.body]) {
        expect(size, greaterThanOrEqualTo(13));
      }
    });
  });

  group('IconSizes', () {
    test('pins sm/md/lg', () {
      expect(IconSizes.sm, 14);
      expect(IconSizes.md, 18);
      expect(IconSizes.lg, 22);
    });
  });
}
