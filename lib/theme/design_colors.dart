import 'package:flutter/material.dart';

/// HTMLデザイン仕様に基づくカラーパレット
class DesignColors {
  DesignColors._();

  // Primary Colors
  static const primary = Color(0xFF00C0D1);
  static const primaryDark = Color(0xFF009AA8);
  static const secondary = Color(0xFFF59E0B); // オレンジ/アンバー

  // Background Colors
  static const backgroundDark = Color(0xFF0E0E11);
  static const backgroundLight = Color(0xFFF9FAFB);

  // Surface Colors
  static const surfaceDark = Color(0xFF1E1F27);
  static const canvasDark = Color(0xFF101116);
  static const inputDark = Color(0xFF0B0F13);

  // Border Colors
  static const borderDark = Color(0xFF2A2B36);
  static const borderLight = Color(0xFFE5E7EB);

  // Text Colors (Dark Theme)
  static const textPrimary = Color(0xFFFFFFFF);
  static const textSecondary = Color(0xFF9CA3AF);
  // WCAG 2.1 AA floor (ADR-047 D-6): #6B7280 was 3.39:1 on surfaceDark and
  // failed AA; #868C96 is 4.85:1 on surfaceDark (5.6:1+ on darker surfaces),
  // still visibly muted below textSecondary.
  static const textMuted = Color(0xFF868C96);

  // Text Colors (Light Theme)
  static const textPrimaryLight = Color(0xFF111827);
  static const textSecondaryLight = Color(0xFF4B5563);
  // WCAG 2.1 AA floor (ADR-047 D-6): #9CA3AF was only 2.54:1 on white and
  // failed AA badly; #646B73 is 4.90:1 on canvasLight (its worst surface),
  // still clearly more muted than textSecondaryLight.
  static const textMutedLight = Color(0xFF646B73);

  // Surface Colors (Light Theme)
  static const surfaceLight = Color(0xFFFFFFFF);
  static const canvasLight = Color(0xFFF3F4F6);
  static const inputLight = Color(0xFFF9FAFB);

  // Status Colors
  static const success = Color(0xFF22C55E);
  static const error = Color(0xFFEF4444);
  static const warning = Color(0xFFF59E0B);

  // Semantic accents for tonal chips/badges (ADR-047 D-5). Used as the
  // base of an alpha-tinted chip (bg = base @ low alpha, fg = base), the
  // same model as AppStatusChip.
  static const info = Color(0xFF6366F1); // indigo — informational / phase
  static const slate = Color(0xFF64748B); // neutral slate — routine / status

  // Status containers (ADR-047 D-5/D-6 — M3 *Container / on*Container
  // idiom). A tinted callout background + the on-color that stays
  // WCAG AA (>=4.5:1) on it. Dark + Light variants resolved by theme
  // brightness, like textMuted/textMutedLight. Replaces the ad-hoc
  // Colors.amber.shadeNNN tonal pairs. on*Container also doubles as the
  // legible warning fg on the app's normal surfaces (verified in
  // test/theme/contrast_test.dart). Ratios: dark 8.96, light 6.37.
  static const warningContainer = Color(0xFF422E08); // deep amber wash
  static const warningContainerLight = Color(0xFFFEF3C7); // amber-100
  static const onWarningContainer = Color(0xFFFCD34D); // amber-300 (on dark)
  static const onWarningContainerLight = Color(0xFF92400E); // amber-800 (on light)

  // `success`/`warning` are bright enough to read on DARK surfaces but
  // fail AA on LIGHT ones; successOnLight is the green-800 tone for
  // success used as standalone fg (icon/label) on a light surface.
  // (matches connectedCardTextLight.) White 7.13, canvasLight 6.48.
  static const successOnLight = Color(0xFF166534); // green-800

  // Terminal Colors
  static const terminalGreen = Color(0xFF22C55E);
  static const terminalBlue = Color(0xFF3B82F6);
  static const terminalRed = Color(0xFFEF4444);
  static const terminalYellow = Color(0xFFEAB308);
  static const terminalCyan = Color(0xFF06B6D4);
  static const terminalMagenta = Color(0xFFA855F7);

  // Special Keys Bar Colors (Dark)
  static const keyBackground = Color(0xFF2A2B35);
  static const keyBackgroundHover = Color(0xFF353640);
  static const footerBackground = Color(0xFF14151A);

  // Special Keys Bar Colors (Light)
  static const keyBackgroundLight = Color(0xFFE5E7EB);
  static const keyBackgroundHoverLight = Color(0xFFD1D5DB);
  static const footerBackgroundLight = Color(0xFFF3F4F6);

  // Status Card Colors (Dark)
  static const connectedCardDark = Color(0xFF14532D);
  static const connectedCardBorderDark = Color(0xFF166534);
  static const connectedCardTextDark = Color(0xFF4ADE80);
  static const connectingCardDark = Color(0xFF153E42);
  static const connectingCardBorderDark = Color(0xFF1F5F66);

  // Status Card Colors (Light)
  static const connectedCardLight = Color(0xFFDCFCE7);
  static const connectedCardBorderLight = Color(0xFF86EFAC);
  static const connectedCardTextLight = Color(0xFF166534);
  static const connectingCardLight = Color(0xFFE0F7FA);
  static const connectingCardBorderLight = Color(0xFF80DEEA);
}
