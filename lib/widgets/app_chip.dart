import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';
import '../theme/tokens.dart';

/// Shared chip widgets — the single source for chip visuals (ADR-047 D-7).
///
/// Two archetypes cover the ~45 bespoke `_*Chip`/`_*Pill` classes that
/// predated this widget:
///
/// * [AppStatusChip] — a static, tinted label (status / kind / count badge),
///   optionally with a leading icon. The color carries the semantic; the
///   chip tints its background and border from it.
/// * [AppChoiceChip] — a selectable filter/segment chip (`selected` +
///   `onTap`), tinted with the brand accent when selected.
///
/// Both compose from [Spacing] / [Radii] / [FontSizes] tokens so a scale
/// change restyles every chip. New chips use these instead of a new private
/// class; see `docs/decisions/047-design-system-enforcement.md`.

/// A static tinted label chip: `[icon] label`, colored from [color].
class AppStatusChip extends StatelessWidget {
  final String label;

  /// The semantic color. Background is [color] at 15% alpha, border at 50%,
  /// text/icon at full strength. Defaults to the brand accent.
  final Color color;
  final IconData? icon;

  const AppStatusChip({
    super.key,
    required this.label,
    this.color = DesignColors.primary,
    this.icon,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding:
          const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: Radii.xsBorder,
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (icon != null) ...[
            Icon(icon, size: IconSizes.sm, color: color),
            const SizedBox(width: Spacing.s4),
          ],
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              fontWeight: FontWeight.w700,
              color: color,
              letterSpacing: 0.3,
            ),
          ),
        ],
      ),
    );
  }
}

/// A selectable filter/segment chip. Tinted with the brand accent when
/// [selected]; a neutral outline otherwise. Theme-aware (resolves the
/// unselected outline/text per brightness).
class AppChoiceChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const AppChoiceChip({
    super.key,
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final outline = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final mutedText =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return InkWell(
      onTap: onTap,
      borderRadius: Radii.mdBorder,
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: Spacing.s12, vertical: Spacing.s8),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: Radii.mdBorder,
          border: Border.all(color: selected ? DesignColors.primary : outline),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: FontSizes.label,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : mutedText,
          ),
        ),
      ),
    );
  }
}
