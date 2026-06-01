// AgentFeed chrome widgets — small presentational pieces the feed
// container draws around the transcript.
//
// Part of the agent_feed split (docs/plans/agent-feed-split.md, W2).
// These are container-only widgets (used solely by `_AgentFeedState`);
// they move here purely to shrink the monolith, not as a shared layer.
// Promoted to public because the container is now a separate library.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import 'feed_reducer.dart' show FeedLens;

/// "Offline · last updated 2m ago" strip shown above the transcript
/// when the bootstrap fetch fell back to the snapshot cache. Cleared
/// the moment a live SSE event arrives — same trigger as `_error`,
/// because either a fresh fetch or the first stream push proves the
/// hub is reachable again.
class OfflineBanner extends StatelessWidget {
  final DateTime staleSince;
  const OfflineBanner({required this.staleSince});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.08),
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Row(
        children: [
          Icon(Icons.cloud_off_outlined, size: 14, color: muted),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              'Offline — showing cached transcript (last updated '
              '${_relative(staleSince)})',
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
            ),
          ),
        ],
      ),
    );
  }

  static String _relative(DateTime ts) {
    final diff = DateTime.now().difference(ts);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }
}

/// One-line bar above the event list: shows the verbose toggle and,
/// when off, how many events are currently hidden by it. Mirrors the
/// "show raw" toggle in claude-code's terminal (Ctrl+O) — by default
/// the transcript reads as a chat surface; flip to see the debug
/// stream when something looks wrong.
/// Floating chip in the feed's top-right corner. Toggles _verbose so
/// debug-fidelity events (lifecycle, raw, system) appear as cards.
/// Replaces the prior full-row toggle bar that ate vertical space on
/// every chat surface even when nothing was hidden. Tooltip carries
/// the explanatory copy so the chip stays icon+count-only.
class VerboseToggleChip extends StatelessWidget {
  final bool verbose;
  final int hiddenCount;
  final VoidCallback onToggle;
  const VerboseToggleChip({
    required this.verbose,
    required this.hiddenCount,
    required this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final label = verbose
        ? 'on'
        : (hiddenCount > 0 ? '$hiddenCount' : '');
    return Tooltip(
      message: verbose
          ? 'Hide debug events (lifecycle, raw, system)'
          : 'Show debug events (lifecycle, raw, system)'
              '${hiddenCount > 0 ? ' — $hiddenCount currently hidden' : ''}',
      child: Material(
        color: bg.withValues(alpha: 0.92),
        elevation: 1,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(14),
          side: BorderSide(color: border),
        ),
        child: InkWell(
          onTap: onToggle,
          borderRadius: BorderRadius.circular(14),
          child: Padding(
            padding: const EdgeInsets.symmetric(
                horizontal: 8, vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  verbose
                      ? Icons.visibility
                      : Icons.visibility_off_outlined,
                  size: 14,
                  color: muted,
                ),
                if (label.isNotEmpty) ...[
                  const SizedBox(width: 4),
                  Text(
                    label,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: muted,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class NewEventsPill extends StatelessWidget {
  // Number of events that arrived while scrolled away from tail. 0
  // when the user is just reading history with no new traffic — the
  // pill still renders as a plain jump-to-tail control so they can
  // snap back without scrolling manually.
  final int count;
  // Current scroll position as a 0..100 percent so the pill doubles
  // as a position indicator. Helpful in long sessions where "where am
  // I?" is non-obvious from row count alone.
  final int scrollPercent;
  final VoidCallback onTap;
  const NewEventsPill({
    required this.count,
    required this.scrollPercent,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final label = count > 0 ? '$count new · $scrollPercent%' : '$scrollPercent%';
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(16),
        child: Container(
          padding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: DesignColors.primary,
            borderRadius: BorderRadius.circular(16),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.2),
                blurRadius: 6,
                offset: const Offset(0, 2),
              ),
            ],
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                label,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: Colors.white,
                ),
              ),
              const SizedBox(width: 4),
              const Icon(Icons.arrow_downward,
                  size: 14, color: Colors.white),
            ],
          ),
        ),
      ),
    );
  }
}

/// Float-over-Stack transcript filter (docs/plans/agent-transcript-
/// debug-and-header-parity.md, P1). Two states, never a Column row:
///
///   rest  (lens == all)  → a funnel icon; tap opens the lens menu.
///   active(lens != all)  → one combined pill `⚠ Errors · 1/3 ▲▼ ✕`
///                          that both shows the filter and steps through
///                          matches (steppers drive the parent's seek).
///
/// Stepping is seq-anchored upstream — this widget only reports intent
/// (prev = older, next = newer, clear = back to All) and renders the
/// 1-based [matchIndex] / [matchCount] position. Styled to match
/// [VerboseToggleChip] (its mirror in the opposite top corner).
class FeedFilterControl extends StatelessWidget {
  final FeedLens lens;
  // Matches currently in the loaded+lensed list. Older matches beyond
  // the loaded pages aren't counted until scrolled into range.
  final int matchCount;
  // 1-based position of the active match (0 when none/empty).
  final int matchIndex;
  final bool canPrev;
  final bool canNext;
  final ValueChanged<FeedLens> onSelectLens;
  final VoidCallback onPrev;
  final VoidCallback onNext;
  const FeedFilterControl({
    required this.lens,
    required this.matchCount,
    required this.matchIndex,
    required this.canPrev,
    required this.canNext,
    required this.onSelectLens,
    required this.onPrev,
    required this.onNext,
  });

  static IconData iconFor(FeedLens l) {
    switch (l) {
      case FeedLens.all:
        return Icons.filter_list;
      case FeedLens.text:
        return Icons.chat_bubble_outline;
      case FeedLens.tools:
        return Icons.build_outlined;
      case FeedLens.errors:
        return Icons.error_outline;
    }
  }

  static String labelFor(FeedLens l) {
    switch (l) {
      case FeedLens.all:
        return 'All';
      case FeedLens.text:
        return 'Text';
      case FeedLens.tools:
        return 'Tools';
      case FeedLens.errors:
        return 'Errors';
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    // Errors get the alarm tint so an active error filter reads as
    // urgent; the other lenses borrow the brand accent.
    final accent =
        lens == FeedLens.errors ? DesignColors.error : DesignColors.primary;

    final menu = PopupMenuButton<FeedLens>(
      tooltip: 'Filter transcript',
      padding: EdgeInsets.zero,
      onSelected: onSelectLens,
      itemBuilder: (_) => [
        for (final l in FeedLens.values)
          PopupMenuItem<FeedLens>(
            value: l,
            child: Row(
              children: [
                Icon(iconFor(l),
                    size: 16,
                    color: l == lens ? accent : muted),
                const SizedBox(width: 8),
                Text(labelFor(l),
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      fontWeight:
                          l == lens ? FontWeight.w700 : FontWeight.w500,
                    )),
              ],
            ),
          ),
      ],
      child: lens == FeedLens.all
          ? Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              child: Icon(Icons.filter_list, size: 16, color: muted),
            )
          : Padding(
              padding:
                  const EdgeInsets.fromLTRB(8, 4, 6, 4),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(iconFor(lens), size: 14, color: accent),
                  const SizedBox(width: 4),
                  Text(
                    labelFor(lens),
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                      color: accent,
                    ),
                  ),
                ],
              ),
            ),
    );

    return Material(
      color: bg.withValues(alpha: 0.92),
      elevation: 1,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(14),
        side: BorderSide(
            color: lens == FeedLens.all
                ? border
                : accent.withValues(alpha: 0.5)),
      ),
      child: lens == FeedLens.all
          ? menu
          : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                menu,
                Text(
                  matchCount == 0 ? '0' : '$matchIndex/$matchCount',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                    color: muted,
                  ),
                ),
                _StepButton(
                  icon: Icons.keyboard_arrow_up,
                  tooltip: 'Older match',
                  color: muted,
                  onTap: canPrev ? onPrev : null,
                ),
                _StepButton(
                  icon: Icons.keyboard_arrow_down,
                  tooltip: 'Newer match',
                  color: muted,
                  onTap: canNext ? onNext : null,
                ),
                _StepButton(
                  icon: Icons.close,
                  tooltip: 'Clear filter',
                  color: muted,
                  onTap: () => onSelectLens(FeedLens.all),
                ),
              ],
            ),
    );
  }
}

class _StepButton extends StatelessWidget {
  final IconData icon;
  final String tooltip;
  final Color color;
  // Null disables the button (rendered dimmed) — used at the first /
  // last match so the user can see they've hit a boundary.
  final VoidCallback? onTap;
  const _StepButton({
    required this.icon,
    required this.tooltip,
    required this.color,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final enabled = onTap != null;
    return Tooltip(
      message: tooltip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 4),
          child: Icon(
            icon,
            size: 16,
            color: color.withValues(alpha: enabled ? 1.0 : 0.35),
          ),
        ),
      ),
    );
  }
}

/// Floating "expand to full screen" button (P3). Shown by a constrained
/// `AgentFeed` (dense) whose host wired `onExpand`; tapping pushes the
/// caller's dedicated full-screen transcript route. Styled to match the
/// verbose chip it sits beside.
class ExpandFeedButton extends StatelessWidget {
  final VoidCallback onTap;
  const ExpandFeedButton({super.key, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Tooltip(
      message: 'Open full-screen transcript',
      child: Material(
        color: bg.withValues(alpha: 0.92),
        elevation: 1,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(14),
          side: BorderSide(color: border),
        ),
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(14),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Icon(Icons.open_in_full, size: 14, color: muted),
          ),
        ),
      ),
    );
  }
}

/// Full-screen-only lens *bar* (P3 — docs/plans/agent-transcript-debug-
/// and-header-parity.md). When a full-screen host runs `AgentFeed(dense:
/// false)`, the floating funnel unfolds into this horizontal selector
/// showing every lens with its live count, so the whole filter surface
/// is visible at once instead of behind a tap. Horizontally scrollable
/// so it never overflows a narrow device.
class FeedLensBar extends StatelessWidget {
  final FeedLens lens;
  final Map<FeedLens, int> counts;
  final ValueChanged<FeedLens> onSelectLens;
  const FeedLensBar({
    required this.lens,
    required this.counts,
    required this.onSelectLens,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Row(
          children: [
            for (final l in FeedLens.values) ...[
              _chip(context, l, muted),
              const SizedBox(width: 6),
            ],
          ],
        ),
      ),
    );
  }

  Widget _chip(BuildContext context, FeedLens l, Color muted) {
    final selected = l == lens;
    final count = counts[l] ?? 0;
    final isErr = l == FeedLens.errors;
    final accent = isErr ? DesignColors.error : DesignColors.primary;
    final fg = selected ? accent : muted;
    return InkWell(
      onTap: () => onSelectLens(l),
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        decoration: BoxDecoration(
          color: selected ? accent.withValues(alpha: 0.14) : Colors.transparent,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: selected ? accent.withValues(alpha: 0.5) : Colors.transparent,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(FeedFilterControl.iconFor(l), size: 13, color: fg),
            const SizedBox(width: 5),
            Text(
              FeedFilterControl.labelFor(l),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
                color: fg,
              ),
            ),
            // The error count rides every state so "3 errors" is visible
            // without selecting the Errors lens first.
            if (l != FeedLens.all && count > 0) ...[
              const SizedBox(width: 5),
              Text(
                '$count',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  color: isErr ? DesignColors.error : muted,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// One tick on the [FeedMinimap]: a normalized vertical position, the
/// seq to jump to, and whether it marks an error turn.
class FeedMinimapMark {
  final double frac; // 0..1 down the transcript
  final int seq;
  final bool isError;
  const FeedMinimapMark({
    required this.frac,
    required this.seq,
    required this.isError,
  });
}

/// Right-edge minimap (P3, full-screen only). A thin vertical strip with
/// a faint tick per tool call and a prominent red tick per error, laid
/// out by each event's position in the loaded transcript. Tapping jumps
/// (seq-anchored) to the nearest error — or the nearest tick when there
/// are no errors — so a failed call deep in a long run is one tap away.
class FeedMinimap extends StatelessWidget {
  final List<FeedMinimapMark> marks;
  final ValueChanged<int> onJump;
  const FeedMinimap({required this.marks, required this.onJump});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final tick =
        (isDark ? DesignColors.textMuted : DesignColors.textMutedLight)
            .withValues(alpha: 0.55);
    return LayoutBuilder(
      builder: (ctx, constraints) {
        return GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTapUp: (d) {
            final h = constraints.maxHeight;
            if (h <= 0 || marks.isEmpty) return;
            final frac = (d.localPosition.dy / h).clamp(0.0, 1.0);
            FeedMinimapMark? best;
            var bestD = double.infinity;
            // Prefer the nearest error tick (the debugging target).
            for (final m in marks) {
              if (!m.isError) continue;
              final dd = (m.frac - frac).abs();
              if (dd < bestD) {
                bestD = dd;
                best = m;
              }
            }
            if (best == null) {
              for (final m in marks) {
                final dd = (m.frac - frac).abs();
                if (dd < bestD) {
                  bestD = dd;
                  best = m;
                }
              }
            }
            if (best != null) onJump(best.seq);
          },
          child: CustomPaint(
            size: Size.infinite,
            painter: _MinimapPainter(
              marks: marks,
              tickColor: tick,
              errorColor: DesignColors.error,
            ),
          ),
        );
      },
    );
  }
}

class _MinimapPainter extends CustomPainter {
  final List<FeedMinimapMark> marks;
  final Color tickColor;
  final Color errorColor;
  _MinimapPainter({
    required this.marks,
    required this.tickColor,
    required this.errorColor,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final tickPaint = Paint()
      ..color = tickColor
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;
    final errPaint = Paint()
      ..color = errorColor
      ..strokeWidth = 2.5
      ..strokeCap = StrokeCap.round;
    for (final m in marks) {
      final y = m.frac.clamp(0.0, 1.0) * size.height;
      if (m.isError) {
        // Full-width red tick — reads as an alarm against the strip.
        canvas.drawLine(Offset(0, y), Offset(size.width, y), errPaint);
      } else {
        // Faint half-width tick — structure without noise.
        canvas.drawLine(Offset(size.width * 0.5, y), Offset(size.width, y),
            tickPaint);
      }
    }
  }

  @override
  bool shouldRepaint(covariant _MinimapPainter old) =>
      old.marks != marks ||
      old.tickColor != tickColor ||
      old.errorColor != errorColor;
}
