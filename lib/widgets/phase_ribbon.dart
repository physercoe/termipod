import 'package:flutter/material.dart';

import '../theme/design_colors.dart';

/// Material horizontal stepper showing the project's phase set with the
/// current phase highlighted (D1, project-lifecycle-mvp.md W1).
///
/// Renders one chip per declared phase, with a thin connector between
/// them, scrollable horizontally on narrow screens. Phases preceding
/// the current one render as completed; the current phase is filled
/// with the primary color; phases after the current render as muted.
/// Taps fire [onTap] with the chip's phase value so the host screen
/// can route to the deliverable / phase-summary surface (W5b stub for
/// W1's acceptance: the route resolves to a phase summary screen even
/// when that screen is just a placeholder).
class PhaseRibbon extends StatelessWidget {
  final List<String> phases;
  final String currentPhase;
  final ValueChanged<String>? onTap;

  const PhaseRibbon({
    super.key,
    required this.phases,
    required this.currentPhase,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    if (phases.isEmpty) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final currentIndex = phases.indexOf(currentPhase);
    return SizedBox(
      height: 56,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            for (var i = 0; i < phases.length; i++) ...[
              _PhaseChip(
                phase: phases[i],
                state: _stateFor(i, currentIndex),
                onTap: onTap == null ? null : () => onTap!(phases[i]),
                isDark: isDark,
              ),
              if (i < phases.length - 1) const _Connector(),
            ],
          ],
        ),
      ),
    );
  }

  static _ChipState _stateFor(int index, int current) {
    if (current < 0) return _ChipState.upcoming;
    if (index < current) return _ChipState.completed;
    if (index == current) return _ChipState.current;
    return _ChipState.upcoming;
  }
}

enum _ChipState { completed, current, upcoming }

class _PhaseChip extends StatelessWidget {
  final String phase;
  final _ChipState state;
  final VoidCallback? onTap;
  final bool isDark;

  const _PhaseChip({
    required this.phase,
    required this.state,
    required this.onTap,
    required this.isDark,
  });

  @override
  Widget build(BuildContext context) {
    Color background;
    Color foreground;
    Color border;
    switch (state) {
      case _ChipState.current:
        background = DesignColors.primary;
        foreground = Colors.white;
        border = DesignColors.primary;
      case _ChipState.completed:
        background =
            (isDark ? DesignColors.primaryDark : DesignColors.primary)
                .withValues(alpha: 0.15);
        foreground =
            isDark ? DesignColors.primary : DesignColors.primaryDark;
        border = DesignColors.primary.withValues(alpha: 0.45);
      case _ChipState.upcoming:
        background = isDark
            ? DesignColors.surfaceDark
            : DesignColors.surfaceLight;
        foreground =
            (isDark ? Colors.white : Colors.black).withValues(alpha: 0.55);
        border = (isDark ? Colors.white : Colors.black)
            .withValues(alpha: 0.18);
    }
    return Semantics(
      button: onTap != null,
      label: 'phase ${_pretty(phase)}, ${_stateLabel(state)}',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(18),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          decoration: BoxDecoration(
            color: background,
            border: Border.all(color: border),
            borderRadius: BorderRadius.circular(18),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (state == _ChipState.completed) ...[
                Icon(Icons.check, size: 14, color: foreground),
                const SizedBox(width: 4),
              ],
              Text(
                _pretty(phase),
                style: TextStyle(
                  color: foreground,
                  fontSize: 13,
                  fontWeight: state == _ChipState.current
                      ? FontWeight.w700
                      : FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  static String _pretty(String slug) {
    if (slug.isEmpty) return slug;
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) => p.isEmpty
            ? p
            : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }

  static String _stateLabel(_ChipState s) => switch (s) {
        _ChipState.completed => 'completed',
        _ChipState.current => 'current',
        _ChipState.upcoming => 'upcoming',
      };
}

class _Connector extends StatelessWidget {
  const _Connector();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      width: 18,
      height: 2,
      margin: const EdgeInsets.symmetric(horizontal: 4),
      color: (isDark ? Colors.white : Colors.black).withValues(alpha: 0.18),
    );
  }
}
