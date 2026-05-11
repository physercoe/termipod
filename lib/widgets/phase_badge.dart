import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';
import 'phase_ribbon.dart';

/// Compact phase indicator. Replaces the inline [PhaseRibbon] when
/// vertical space matters (e.g. project detail header): one pill
/// reading `Method · 3/5 ›`; tap opens a bottom sheet that hosts the
/// full ribbon so the user can still review or jump to other phases.
///
/// Pattern reference: Linear / Jira / Notion status badge — phase is
/// metadata, not navigation, so it earns a badge rather than its own
/// row. The expand-on-tap affordance keeps navigation reachable
/// without the chrome cost.
class PhaseBadge extends StatelessWidget {
  final List<String> phases;
  final String currentPhase;
  /// Fires with the tapped phase from inside the expanded sheet. The
  /// badge itself never dispatches — the badge tap just opens the
  /// sheet, the sheet's phase chips dispatch.
  final ValueChanged<String>? onTap;

  const PhaseBadge({
    super.key,
    required this.phases,
    required this.currentPhase,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    if (phases.isEmpty) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final idx = phases.indexOf(currentPhase);
    final position = idx < 0 ? null : '${idx + 1}/${phases.length}';
    final label = currentPhase.isEmpty ? 'No phase' : _pretty(currentPhase);
    final pillFg =
        isDark ? DesignColors.primary : DesignColors.primaryDark;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Align(
        alignment: Alignment.centerLeft,
        child: InkWell(
          borderRadius: BorderRadius.circular(16),
          onTap: () => _expand(context),
          child: Container(
            padding: const EdgeInsets.fromLTRB(10, 5, 8, 5),
            decoration: BoxDecoration(
              color: DesignColors.primary.withValues(alpha: 0.12),
              border: Border.all(
                color: DesignColors.primary.withValues(alpha: 0.45),
              ),
              borderRadius: BorderRadius.circular(16),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.timeline, size: 13, color: pillFg),
                const SizedBox(width: 6),
                Text(
                  label,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: pillFg,
                  ),
                ),
                if (position != null) ...[
                  const SizedBox(width: 6),
                  Text(
                    '· $position',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      fontWeight: FontWeight.w500,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
                const SizedBox(width: 4),
                Icon(
                  Icons.chevron_right,
                  size: 16,
                  color: DesignColors.textMuted,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _expand(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => _PhasePickerSheet(
        phases: phases,
        currentPhase: currentPhase,
        onTap: (p) {
          Navigator.of(ctx).pop();
          onTap?.call(p);
        },
      ),
    );
  }

  static String _pretty(String slug) {
    if (slug.isEmpty) return slug;
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) =>
            p.isEmpty ? p : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }
}

/// Internal bottom sheet hosting the full ribbon. Kept private since
/// the only entry point is [PhaseBadge._expand]; if a second caller
/// surfaces, lift it to a top-level widget.
class _PhasePickerSheet extends StatelessWidget {
  final List<String> phases;
  final String currentPhase;
  final ValueChanged<String> onTap;

  const _PhasePickerSheet({
    required this.phases,
    required this.currentPhase,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: 12),
          Container(
            width: 36,
            height: 4,
            decoration: BoxDecoration(
              color: DesignColors.textMuted.withValues(alpha: 0.4),
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const SizedBox(height: 12),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                'PHASES',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.8,
                  color: muted,
                ),
              ),
            ),
          ),
          const SizedBox(height: 4),
          PhaseRibbon(
            phases: phases,
            currentPhase: currentPhase,
            onTap: onTap,
          ),
          const SizedBox(height: 12),
        ],
      ),
    );
  }
}
