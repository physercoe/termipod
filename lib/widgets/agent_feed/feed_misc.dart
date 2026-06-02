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
  final VoidCallback onTap;
  const NewEventsPill({
    required this.count,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    // No scroll-percent: over a lazily-loaded transcript with no known
    // total it isn't monotonic (loading an older page above your row
    // re-scales the percent), so it read as buggy. The pill is now a
    // clean jump-to-latest with the unread count.
    final label = count > 0 ? '$count new' : 'Latest';
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
  // Per-lens counts shown next to each menu item (full-screen passes these
  // so the funnel replaces the old always-on lens BAR — same information,
  // no vertical row). Null in the dense host.
  final Map<FeedLens, int>? counts;
  const FeedFilterControl({
    required this.lens,
    required this.matchCount,
    required this.matchIndex,
    required this.canPrev,
    required this.canNext,
    required this.onSelectLens,
    required this.onPrev,
    required this.onNext,
    this.counts,
  });

  static IconData iconFor(FeedLens l) {
    switch (l) {
      case FeedLens.all:
        return Icons.filter_list;
      case FeedLens.text:
        return Icons.chat_bubble_outline;
      case FeedLens.turns:
        return Icons.call_received;
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
      case FeedLens.turns:
        return 'Turns';
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
                // Live count per lens (full-screen): the funnel menu now
                // carries what the old lens bar did. No Spacer — a
                // PopupMenuItem sizes to intrinsic width, where a flex child
                // would throw; a fixed gap keeps it safe.
                if (counts != null && l != FeedLens.all) ...[
                  const SizedBox(width: 14),
                  Text('${counts![l] ?? 0}',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w700,
                        color: l == FeedLens.errors && (counts![l] ?? 0) > 0
                            ? DesignColors.error
                            : muted,
                      )),
                ],
              ],
            ),
          ),
      ],
      child: lens == FeedLens.all
          ? Padding(
              // Bigger hit target — the 16px icon + 8/4 padding was an
              // awkward tap on the constrained host (tester feedback).
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
              child: Icon(Icons.filter_list, size: 20, color: muted),
            )
          : Padding(
              // Taller active-filter label so the whole pill (and the
              // step buttons beside it) is comfortably tappable.
              padding:
                  const EdgeInsets.fromLTRB(8, 8, 6, 8),
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
          // Roomier hit target — the 3/4 padding made prev/next an awkward
          // tap once a filter was active (tester feedback).
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 9),
          child: Icon(
            icon,
            size: 18,
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

/// One tick on the [FeedMinimap]: a normalized vertical position, the
/// seq to jump to, whether it marks an error, and the card-matching colour
/// (see `agentEventAccent`).
class FeedMinimapMark {
  final double frac; // 0..1 down the transcript
  final int seq;
  final bool isError;
  final Color color;
  const FeedMinimapMark({
    required this.frac,
    required this.seq,
    required this.isError,
    required this.color,
  });
}

/// Right-edge minimap (P3, full-screen only). A thin vertical strip with
/// a faint tick per tool call and a prominent red tick per error, laid
/// out by each event's position in the loaded transcript. Tapping jumps
/// (seq-anchored) to the nearest error — or the nearest tick when there
/// are no errors — so a failed call deep in a long run is one tap away.
class FeedMinimap extends StatelessWidget {
  final List<FeedMinimapMark> marks;
  // (fraction down the transcript, seq) of the tapped target — the host
  // proportionally pre-scrolls to [frac] (reliable for not-yet-built rows)
  // and uses [seq] for the landing highlight.
  final void Function(double frac, int seq) onJump;
  // Continuous drag-scrub: the host scrolls to [frac] as the finger moves
  // down the strip (no highlight, no seq anchor — pure position scrubbing).
  final ValueChanged<double>? onScrub;
  // Current viewport-top position (0..1) drawn as a position indicator so
  // the strip reads as a scrollbar. NB: over a lazily-loaded transcript
  // with no known total this isn't perfectly monotonic — loading an older
  // page above the viewport re-scales it — but a tester wanted the
  // position cue back, so it's an honest "where in the loaded window".
  final double viewportFrac;
  const FeedMinimap({
    required this.marks,
    required this.onJump,
    this.onScrub,
    this.viewportFrac = 0,
  });

  void _scrub(double dy, double h) {
    if (onScrub == null || h <= 0) return;
    onScrub!((dy / h).clamp(0.0, 1.0));
  }

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
          // Tap = jump to the nearest error (else nearest tick).
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
            if (best != null) onJump(best.frac, best.seq);
          },
          // Vertical drag = scrub the viewport continuously. Only attach the
          // drag recognizers when scrubbing is actually wired (onScrub != null
          // — the loaded-window minimap). In run-anchor mode onScrub is null:
          // attaching a no-op drag recognizer there would join the gesture
          // arena and swallow taps that move even a pixel, so the minimap read
          // as "untappable". Leaving them off makes the tap unambiguous.
          onVerticalDragStart: onScrub == null
              ? null
              : (d) => _scrub(d.localPosition.dy, constraints.maxHeight),
          onVerticalDragUpdate: onScrub == null
              ? null
              : (d) => _scrub(d.localPosition.dy, constraints.maxHeight),
          child: CustomPaint(
            size: Size.infinite,
            painter: _MinimapPainter(
              marks: marks,
              trackColor: tick,
              viewportFrac: viewportFrac,
            ),
          ),
        );
      },
    );
  }
}

class _MinimapPainter extends CustomPainter {
  final List<FeedMinimapMark> marks;
  final Color trackColor;
  final double viewportFrac;
  _MinimapPainter({
    required this.marks,
    required this.trackColor,
    required this.viewportFrac,
  });

  @override
  void paint(Canvas canvas, Size size) {
    // Faint rounded track so the strip reads as a tappable control rather
    // than stray ticks floating at the edge.
    final trackPaint = Paint()..color = trackColor.withValues(alpha: 0.10);
    canvas.drawRRect(
      RRect.fromRectAndRadius(
          Offset.zero & size, const Radius.circular(4)),
      trackPaint,
    );
    for (final m in marks) {
      final y = m.frac.clamp(0.0, 1.0) * size.height;
      // Each tick is painted in its card's accent colour so the minimap
      // reads like a colour-coded shrink of the transcript. Errors get a
      // full-width, thicker stroke so they still pop as alarms.
      final p = Paint()
        ..color = m.color
        ..strokeWidth = m.isError ? 2.5 : 1.5
        ..strokeCap = StrokeCap.round;
      final x0 = m.isError ? 0.0 : size.width * 0.4;
      canvas.drawLine(Offset(x0, y), Offset(size.width, y), p);
    }
    // Viewport position indicator — a rounded bar at the current scroll
    // position so the strip works as a scrollbar.
    final ty = viewportFrac.clamp(0.0, 1.0) * size.height;
    final thumbPaint = Paint()..color = trackColor.withValues(alpha: 0.9);
    canvas.drawRRect(
      RRect.fromRectAndRadius(
        Rect.fromLTWH(0, (ty - 4).clamp(0.0, size.height - 8), size.width, 8),
        const Radius.circular(4),
      ),
      thumbPaint,
    );
  }

  @override
  bool shouldRepaint(covariant _MinimapPainter old) =>
      old.marks != marks ||
      old.trackColor != trackColor ||
      old.viewportFrac != viewportFrac;
}

/// Tiny "view in context" affordance shown on the corner of each card in a
/// filtered (non-All) lens. Tapping clears the filter and seeks to this row
/// in the full transcript, so a match can be read with its surrounding
/// turns (a tester asked to jump from a filtered card back to its place in
/// the All view).
class ContextJumpButton extends StatelessWidget {
  final VoidCallback onTap;
  const ContextJumpButton({super.key, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Tooltip(
      message: 'View in full transcript',
      child: Material(
        color: bg.withValues(alpha: 0.85),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(10),
          side: BorderSide(color: border),
        ),
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(10),
          child: Padding(
            padding: const EdgeInsets.all(4),
            child: Icon(Icons.center_focus_strong, size: 15, color: muted),
          ),
        ),
      ),
    );
  }
}

/// Compact floating turn stepper (docs/plans/agent-transcript-debug-and-
/// header-parity.md — turn-nav follow-up). Replaces the earlier full-width
/// `TranscriptNavBar` row, which ate vertical space and whose `turn N/M`
/// number both disagreed with the cost/turn chip (it counted prompts, the
/// chip counts agent turns) and confused users. This is purely *relative*
/// navigation — `⤒` top-of-loaded, `‹` previous prompt, `›` next prompt —
/// with no ordinal to mismatch and explicit clamping (the buttons disable
/// at the ends, so stepping can't wrap around). Floats bottom-left over the
/// transcript like the verbose/funnel chips; the minimap stays the free-
/// position scrubber. Endpoints anchor on inbound *human/peer* prompts
/// (see `isTurnAnchorEvent`) — the meaningful exchange starts.
class TurnStepperPill extends StatelessWidget {
  final VoidCallback? onOldest;
  final VoidCallback? onPrevTurn;
  final VoidCallback? onNextTurn;
  // The unit the ‹/› step through in the current view ("prompt", "error",
  // "message", …) — drives the tooltips so prev/next read meaningfully in
  // a filtered view.
  final String unit;
  const TurnStepperPill({
    required this.onOldest,
    required this.onPrevTurn,
    required this.onNextTurn,
    this.unit = 'prompt',
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    return Material(
      color: bg.withValues(alpha: 0.92),
      elevation: 1,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: border),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            _btn(Icons.vertical_align_top, 'Top of loaded', onOldest, muted),
            _btn(Icons.expand_less, 'Previous $unit', onPrevTurn, muted),
            _btn(Icons.expand_more, 'Next $unit', onNextTurn, muted),
          ],
        ),
      ),
    );
  }

  Widget _btn(
      IconData icon, String tip, VoidCallback? onTap, Color color) {
    final enabled = onTap != null;
    return Tooltip(
      message: tip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(14),
        child: Padding(
          // Larger hit area per arrow — the stepper buttons were a tight
          // tap at 6/5 (tester feedback).
          padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 9),
          child: Icon(
            icon,
            size: 20,
            color: color.withValues(alpha: enabled ? 1.0 : 0.3),
          ),
        ),
      ),
    );
  }
}

/// Plan P2 (agent-run-analysis-mode) — the monotonic "event N / M" position
/// chip floating over the full-screen analysis log. When [onTap] is null it's a
/// pure indicator (wrapped in an IgnorePointer so it never competes with the
/// funnel/minimap for a tap); when [onTap] is supplied (the random-access
/// Insights surface) it becomes a control — tap to open the "jump to any event"
/// scrubber, with a faint underline cueing that it's interactive. Analysis-only
/// chrome: AgentFeed renders it solely when a digest total was supplied (the
/// Insights surface); the live-tail Feed never does.
class FeedPositionPill extends StatelessWidget {
  final ({int n, int m}) pos;
  final VoidCallback? onTap;
  const FeedPositionPill({super.key, required this.pos, this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final pill = Material(
      color: bg.withValues(alpha: 0.88),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: border),
      ),
      child: InkWell(
        // No-op InkWell when onTap is null keeps the radius for the ripple
        // shape; the IgnorePointer below makes the whole pill inert anyway.
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
          child: Text(
            'event ${pos.n} / ${pos.m}',
            style: GoogleFonts.robotoMono(
              fontSize: 10,
              color: muted,
              decoration:
                  onTap == null ? null : TextDecoration.underline,
              decorationColor: muted,
            ),
          ),
        ),
      ),
    );
    // Inert indicator unless a tap handler was supplied.
    return onTap == null ? IgnorePointer(child: pill) : pill;
  }
}

/// Human label for a canonical error class (the digest's keys): `tool_error` →
/// "Tool error", `failed_turn` → "Failed turn", `error:<type>` → "Error · type".
String errorClassLabel(String cls) {
  switch (cls) {
    case 'tool_error':
      return 'Tool error';
    case 'failed_turn':
      return 'Failed turn';
    case 'error':
      return 'Error';
    default:
      if (cls.startsWith('error:')) return 'Error · ${cls.substring(6)}';
      return cls;
  }
}

/// "2m ago" from an RFC3339 timestamp; empty when absent/unparseable.
String relativeAgo(String? ts) {
  if (ts == null || ts.isEmpty) return '';
  final t = DateTime.tryParse(ts);
  if (t == null) return '';
  final d = DateTime.now().toUtc().difference(t.toUtc());
  final s = d.inSeconds;
  if (s < 0) return 'now';
  if (s < 60) return '${s}s ago';
  if (d.inMinutes < 60) return '${d.inMinutes}m ago';
  if (d.inHours < 24) return '${d.inHours}h ago';
  return '${d.inDays}d ago';
}

/// One row of the Insight **Errors** lens rendered as the run's complete error
/// list (ADR-039 P2): a red dot, the error class, a relative timestamp, and the
/// run ordinal — all from the digest, no event-body fetch. Tapping jumps to the
/// error in full context. Sized to a fixed extent so the funnel stepper can
/// scroll to a row by index.
class ErrorSummaryRow extends StatelessWidget {
  final int ordinal;
  final String errorClass;
  final String? ts;
  final bool active;
  final VoidCallback onTap;
  const ErrorSummaryRow({
    super.key,
    required this.ordinal,
    required this.errorClass,
    required this.onTap,
    this.ts,
    this.active = false,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final fg =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
    final rel = relativeAgo(ts);
    return Material(
      color: active
          ? DesignColors.error.withValues(alpha: 0.10)
          : Colors.transparent,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          child: Row(
            children: [
              Container(
                width: 8,
                height: 8,
                decoration: const BoxDecoration(
                  color: DesignColors.error,
                  shape: BoxShape.circle,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      errorClassLabel(errorClass),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: fg,
                      ),
                    ),
                    if (rel.isNotEmpty)
                      Text(
                        rel,
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10, color: muted),
                      ),
                  ],
                ),
              ),
              Text('#$ordinal',
                  style:
                      GoogleFonts.jetBrainsMono(fontSize: 10, color: muted)),
              const SizedBox(width: 6),
              Icon(Icons.chevron_right, size: 18, color: muted),
            ],
          ),
        ),
      ),
    );
  }
}
