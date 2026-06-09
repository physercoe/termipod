import 'dart:math' as math;
import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/theme/design_colors.dart';

/// Guards the WCAG 2.1 AA floor for text tokens (ADR-047 D-6). Every
/// text-tier color must clear 4.5:1 against each surface it renders on. If a
/// token or surface changes and drops below the floor, this test fails.

/// Relative luminance per WCAG 2.1 (sRGB channels are 0..1 via Color.r/g/b).
double _luminance(Color c) {
  double channel(double cs) =>
      cs <= 0.03928 ? cs / 12.92 : math.pow((cs + 0.055) / 1.055, 2.4).toDouble();
  return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
}

/// WCAG contrast ratio in [1, 21].
double contrastRatio(Color a, Color b) {
  final la = _luminance(a);
  final lb = _luminance(b);
  final hi = math.max(la, lb);
  final lo = math.min(la, lb);
  return (hi + 0.05) / (lo + 0.05);
}

void main() {
  const aa = 4.5; // WCAG 2.1 §1.4.3 normal-text AA floor.

  group('contrastRatio helper', () {
    test('black on white is 21:1', () {
      expect(contrastRatio(const Color(0xFF000000), const Color(0xFFFFFFFF)),
          closeTo(21, 0.05));
    });
    test('identical colors are 1:1', () {
      expect(contrastRatio(DesignColors.primary, DesignColors.primary),
          closeTo(1, 0.001));
    });
  });

  group('dark theme text tokens clear AA on every dark surface', () {
    const surfaces = {
      'surfaceDark': DesignColors.surfaceDark,
      'canvasDark': DesignColors.canvasDark,
      'backgroundDark': DesignColors.backgroundDark,
      'inputDark': DesignColors.inputDark,
    };
    const tokens = {
      'textPrimary': DesignColors.textPrimary,
      'textSecondary': DesignColors.textSecondary,
      'textMuted': DesignColors.textMuted,
    };
    tokens.forEach((tname, tcol) {
      surfaces.forEach((sname, scol) {
        test('$tname on $sname', () {
          expect(contrastRatio(tcol, scol), greaterThanOrEqualTo(aa),
              reason: '$tname fails AA on $sname');
        });
      });
    });
  });

  group('light theme text tokens clear AA on every light surface', () {
    const surfaces = {
      'surfaceLight': DesignColors.surfaceLight,
      'canvasLight': DesignColors.canvasLight,
      'inputLight': DesignColors.inputLight,
      'backgroundLight': DesignColors.backgroundLight,
    };
    const tokens = {
      'textPrimaryLight': DesignColors.textPrimaryLight,
      'textSecondaryLight': DesignColors.textSecondaryLight,
      'textMutedLight': DesignColors.textMutedLight,
    };
    tokens.forEach((tname, tcol) {
      surfaces.forEach((sname, scol) {
        test('$tname on $sname', () {
          expect(contrastRatio(tcol, scol), greaterThanOrEqualTo(aa),
              reason: '$tname fails AA on $sname');
        });
      });
    });
  });

  test('muted stays visibly lighter than secondary (not identical)', () {
    // Muted should be a distinct, lower-contrast tier — not collapsed into
    // textSecondary.
    expect(DesignColors.textMuted, isNot(DesignColors.textSecondary));
    expect(DesignColors.textMutedLight, isNot(DesignColors.textSecondaryLight));
  });
}
