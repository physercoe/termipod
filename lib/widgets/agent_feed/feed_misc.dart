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
