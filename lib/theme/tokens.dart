import 'package:flutter/widgets.dart';

/// Design tokens — the single source of truth for the app's visual scales.
///
/// Per ADR-047 (design-system enforcement), new code composes from these
/// named scales instead of ad-hoc literals: `Spacing.s16`, `Radii.md`,
/// `FontSizes.body`, `IconSizes.md`. Colors live in [DesignColors]
/// (`design_colors.dart`); these classes cover spacing, radius, type, and
/// icon size. All values are `const` — trivial ceremony, large payoff.
///
/// See `docs/decisions/047-design-system-enforcement.md` and
/// `docs/reference/ui-guidelines.md`.

/// Spacing scale — a 4px grid (ADR-047 D-2).
///
/// Use these for padding, gaps, and insets. `s2` is a hairline value for
/// borders/dividers only; layout spacing should start at `s4`. Off-grid
/// values (6, 7, 10, 11, 14) are barred in new code.
class Spacing {
  Spacing._();

  /// 2px — hairline insets only (borders, dividers), not layout spacing.
  static const double s2 = 2;
  static const double s4 = 4;
  static const double s8 = 8;
  static const double s12 = 12;
  static const double s16 = 16;
  static const double s24 = 24;
  static const double s32 = 32;
}

/// Corner-radius scale — the Material 3 shape scale (ADR-047 D-3).
///
/// `md` (12) is the default for surfaces and cards — it matches the coded
/// `ThemeData` default. `stadium` is for pills and the FAB.
class Radii {
  Radii._();

  static const double xs = 4;
  static const double sm = 8;
  static const double md = 12;
  static const double lg = 16;

  /// A large radius standing in for a stadium/pill shape. Prefer
  /// [stadiumBorder] for true pills.
  static const double stadium = 999;

  static const BorderRadius xsBorder = BorderRadius.all(Radius.circular(xs));
  static const BorderRadius smBorder = BorderRadius.all(Radius.circular(sm));
  static const BorderRadius mdBorder = BorderRadius.all(Radius.circular(md));
  static const BorderRadius lgBorder = BorderRadius.all(Radius.circular(lg));
  static const BorderRadius stadiumBorder =
      BorderRadius.all(Radius.circular(stadium));
}

/// Type scale — a 6-step semantic scale (ADR-047 D-4).
///
/// `bodySmall` (13) is the floor for primary readable text; 8/9/10/11 are
/// reserved for non-body labels only. Weight and letter-spacing stay on the
/// app's [TextTheme]; these are sizes.
class FontSizes {
  FontSizes._();

  /// 11px — chip/badge labels and other non-body micro-text only.
  static const double label = 11;
  static const double caption = 12;

  /// 13px — the floor for primary readable body text.
  static const double bodySmall = 13;
  static const double body = 14;
  static const double subtitle = 16;
  static const double title = 18;
  static const double titleLarge = 20;
}

/// Icon-size scale (ADR-047 D-4/§6).
class IconSizes {
  IconSizes._();

  static const double sm = 14;
  static const double md = 18;
  static const double lg = 22;
}
