import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';

/// A destructive-action control that fires only on a deliberate
/// **long-press + horizontal slide** (ADR-028 Phase 5 / plan W24).
///
/// Why not a plain button: the Admin pane drives fleet-wide shutdown /
/// update / restart and token rotation — a single mistaken tap there is
/// expensive. Long-press disambiguates from a list scroll; the slide
/// gives the operator a visible, abortable commit gesture (release
/// before the bar fills and nothing happens).
///
/// A plain tap does not fire the action — it surfaces [hint] via a
/// SnackBar so a first-time operator learns the gesture.
class ConfirmActionTile extends StatefulWidget {
  const ConfirmActionTile({
    super.key,
    required this.label,
    required this.icon,
    required this.onConfirmed,
    this.destructive = true,
    this.enabled = true,
    this.busy = false,
    this.hint = 'Long-press, then slide right to confirm.',
  });

  /// Action name, e.g. "Shutdown all" or "Restart runner-1".
  final String label;
  final IconData icon;

  /// Fired once the slide crosses the commit threshold.
  final VoidCallback onConfirmed;

  /// Destructive actions render in the error colour; non-destructive
  /// ones (rare here) render in the primary colour.
  final bool destructive;

  /// When false the tile is inert and dimmed (e.g. an offline host).
  final bool enabled;

  /// When true the tile shows a spinner and ignores gestures — set
  /// while the action's network call is in flight.
  final bool busy;

  /// Shown via SnackBar on a plain tap.
  final String hint;

  @override
  State<ConfirmActionTile> createState() => _ConfirmActionTileState();
}

class _ConfirmActionTileState extends State<ConfirmActionTile> {
  /// Slide distance, in logical pixels, required to commit.
  static const double _commitDistance = 200;

  double _progress = 0; // 0..1
  bool _armed = false;

  bool get _interactive => widget.enabled && !widget.busy;

  void _onLongPressStart(LongPressStartDetails _) {
    if (!_interactive) return;
    setState(() {
      _armed = true;
      _progress = 0;
    });
  }

  void _onLongPressMove(LongPressMoveUpdateDetails d) {
    if (!_armed) return;
    final p = (d.offsetFromOrigin.dx / _commitDistance).clamp(0.0, 1.0);
    if (p != _progress) setState(() => _progress = p);
  }

  void _onLongPressEnd(LongPressEndDetails _) {
    if (!_armed) return;
    final committed = _progress >= 1.0;
    setState(() {
      _armed = false;
      _progress = 0;
    });
    if (committed) widget.onConfirmed();
  }

  void _onTap() {
    if (!_interactive) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(
        content: Text(widget.hint),
        duration: const Duration(seconds: 2),
      ));
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final accent =
        widget.destructive ? DesignColors.error : DesignColors.primary;
    final track = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final fg = _interactive
        ? accent
        : (isDark ? DesignColors.textMuted : DesignColors.textMutedLight);

    // Visual structure (v1.0.662 redesign):
    //
    //   ┌──────────────────────────────────────────────────────┐
    //   │ [icon]        Centered label              [⇨ chip] │   resting
    //   │ ▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░ slide-bar fills as you drag    │   armed
    //   └──────────────────────────────────────────────────────┘
    //
    // Resting state: leading icon, centered label, trailing chip
    // (small filled pill with a chevron — replaces the pre-v1.0.662
    // "slide ▸" mono text that was easy to miss).
    //
    // Armed state (long-press held): the trailing chip morphs into a
    // mini progress bar overlay AND the background fill grows
    // left→right. Two redundant signals so the operator can read
    // commit-progress at a glance.
    return Opacity(
      opacity: _interactive ? 1 : 0.45,
      child: GestureDetector(
        onTap: _onTap,
        onLongPressStart: _onLongPressStart,
        onLongPressMoveUpdate: _onLongPressMove,
        onLongPressEnd: _onLongPressEnd,
        child: Container(
          height: 52,
          decoration: BoxDecoration(
            color: track,
            borderRadius: BorderRadius.circular(10),
            border: Border.all(
              color: _armed ? accent.withValues(alpha: 0.5) : border,
            ),
          ),
          clipBehavior: Clip.antiAlias,
          child: Stack(
            children: [
              // Background progress fill — grows left-to-right as
              // the operator slides. Slightly stronger when armed
              // so the operator sees they're committing.
              FractionallySizedBox(
                widthFactor: _progress.clamp(0.0, 1.0),
                heightFactor: 1,
                child: Container(
                  color: accent
                      .withValues(alpha: _armed ? 0.30 : 0.22),
                ),
              ),
              // Centered label + leading icon + trailing chip,
              // arranged so the label sits visually centered in the
              // tile (Row with mainAxisAlignment.center on a
              // Stack-wrapped layout = stable centering even when
              // chip text changes width during the slide).
              Positioned.fill(
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  child: Stack(
                    alignment: Alignment.center,
                    children: [
                      // Centered label.
                      Padding(
                        // Reserve horizontal margin so the label
                        // doesn't visually collide with the leading
                        // icon / trailing chip at small widths.
                        padding: const EdgeInsets.symmetric(horizontal: 56),
                        child: Text(
                          widget.label,
                          textAlign: TextAlign.center,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 14,
                            fontWeight: FontWeight.w700,
                            color: fg,
                            letterSpacing: 0.1,
                          ),
                        ),
                      ),
                      // Leading icon (left-aligned).
                      Align(
                        alignment: Alignment.centerLeft,
                        child: widget.busy
                            ? SizedBox(
                                width: 18,
                                height: 18,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  valueColor:
                                      AlwaysStoppedAnimation(fg),
                                ),
                              )
                            : Icon(widget.icon, size: 20, color: fg),
                      ),
                      // Trailing slide affordance — small pill,
                      // chevron forward icon. When armed, the chip
                      // dims slightly so the eye tracks the
                      // growing background fill instead.
                      Align(
                        alignment: Alignment.centerRight,
                        child: _SlideChip(
                          color: fg,
                          armed: _armed,
                          progress: _progress,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Trailing affordance on a [ConfirmActionTile]. Resting: a small
/// pill with a chevron, telling the operator the row is interactive
/// in a way a plain row isn't. Armed: dims so the eye tracks the
/// background fill (the real progress signal).
class _SlideChip extends StatelessWidget {
  const _SlideChip({
    required this.color,
    required this.armed,
    required this.progress,
  });
  final Color color;
  final bool armed;
  final double progress;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 36,
      height: 24,
      decoration: BoxDecoration(
        color: color.withValues(alpha: armed ? 0.10 : 0.16),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: color.withValues(alpha: armed ? 0.18 : 0.32),
        ),
      ),
      alignment: Alignment.center,
      child: Icon(
        Icons.chevron_right,
        size: 18,
        color: color.withValues(alpha: armed ? 0.5 : 0.85),
      ),
    );
  }
}
