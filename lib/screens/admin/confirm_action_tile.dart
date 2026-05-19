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

    return Opacity(
      opacity: _interactive ? 1 : 0.45,
      child: GestureDetector(
        onTap: _onTap,
        onLongPressStart: _onLongPressStart,
        onLongPressMoveUpdate: _onLongPressMove,
        onLongPressEnd: _onLongPressEnd,
        child: Container(
          height: 48,
          decoration: BoxDecoration(
            color: track,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: border),
          ),
          clipBehavior: Clip.antiAlias,
          child: Stack(
            children: [
              // Progress fill — grows left-to-right as the operator slides.
              FractionallySizedBox(
                widthFactor: _progress.clamp(0.0, 1.0),
                heightFactor: 1,
                child: Container(color: accent.withValues(alpha: 0.22)),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Row(
                  children: [
                    widget.busy
                        ? SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              valueColor: AlwaysStoppedAnimation(fg),
                            ),
                          )
                        : Icon(widget.icon, size: 18, color: fg),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        widget.label,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                          color: fg,
                        ),
                      ),
                    ),
                    Text(
                      _armed ? 'release at end' : 'slide ▸',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 9,
                        color: fg.withValues(alpha: 0.7),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
