import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';

/// Floating recording indicator anchored near the steward puck while a
/// Mode A (puck long-press) voice recording is in progress.
///
/// **Visual prominence (v1.0.540 polish).** Earlier revisions used a
/// 280-px pill with a small red dot and 13-pt text — tester feedback
/// was that it read like a passive tooltip, not a "you are LIVE on
/// the mic" indicator. This revision goes bigger and louder:
///
/// - A red header bar with the RECORDING label and a pulsing dot
///   that also emits a concentric ripple (the puck's red ring tells
///   you "mic is on"; the HUD's ripple tells you "and the cloud is
///   listening").
/// - 32-pt monospace mm:ss timer — readable mid-utterance.
/// - Three animated "audio level" bars next to the timer. They don't
///   reflect real audio amplitude (RMS strip is deferred polish) but
///   give a strong "live" cue without committing to a per-frame
///   amplitude pipeline.
/// - Larger transcript area (3 lines, 15 pt).
/// - "Release to send · drag away to cancel" footer in two halves so
///   the cancel affordance is unmissable.
///
/// The widget is purely visual — no gestures. The puck owns the
/// long-press gesture; this HUD is wrapped in `IgnorePointer` by the
/// caller so it never steals the recording gesture.
class VoiceRecordingHud extends StatefulWidget {
  const VoiceRecordingHud({
    super.key,
    required this.transcript,
    required this.elapsed,
  });

  /// Latest streaming partial (or accumulated text) to render. Empty
  /// string while the WebSocket is still in `connecting` phase.
  final String transcript;

  /// Time since the recording started — drives the mm:ss timer.
  final Duration elapsed;

  @override
  State<VoiceRecordingHud> createState() => _VoiceRecordingHudState();
}

class _VoiceRecordingHudState extends State<VoiceRecordingHud>
    with TickerProviderStateMixin {
  late final AnimationController _pulseController;
  late final AnimationController _rippleController;
  late final AnimationController _barsController;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 900),
    )..repeat(reverse: true);
    _rippleController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1400),
    )..repeat();
    _barsController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 700),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _pulseController.dispose();
    _rippleController.dispose();
    _barsController.dispose();
    super.dispose();
  }

  String _mmss(Duration d) {
    final mins = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    final secs = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    return '$mins:$secs';
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final surface =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final textColor =
        isDark ? Colors.white : DesignColors.textPrimaryLight;
    final transcript = widget.transcript.trim();

    return Material(
      elevation: 14,
      borderRadius: BorderRadius.circular(16),
      color: surface,
      child: ConstrainedBox(
        constraints: const BoxConstraints(
          maxWidth: 340,
          minWidth: 260,
        ),
        child: Container(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(16),
            border: Border.all(
              color: DesignColors.error.withValues(alpha: 0.55),
              width: 1.5,
            ),
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _buildHeader(),
              _buildTimerRow(textColor),
              _buildTranscript(transcript, textColor, isDark),
              _buildFooter(isDark),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader() {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
      decoration: BoxDecoration(
        color: DesignColors.error.withValues(alpha: 0.18),
        borderRadius: const BorderRadius.only(
          topLeft: Radius.circular(15),
          topRight: Radius.circular(15),
        ),
      ),
      child: Row(
        children: [
          SizedBox(
            width: 22,
            height: 22,
            child: Stack(
              alignment: Alignment.center,
              children: [
                AnimatedBuilder(
                  animation: _rippleController,
                  builder: (context, _) {
                    final v = _rippleController.value;
                    return Opacity(
                      opacity: (1.0 - v).clamp(0.0, 1.0),
                      child: Container(
                        width: 22 * (0.4 + 0.6 * v),
                        height: 22 * (0.4 + 0.6 * v),
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: DesignColors.error,
                            width: 2,
                          ),
                        ),
                      ),
                    );
                  },
                ),
                ScaleTransition(
                  scale: Tween<double>(begin: 0.75, end: 1.0)
                      .animate(_pulseController),
                  child: Container(
                    width: 12,
                    height: 12,
                    decoration: const BoxDecoration(
                      color: DesignColors.error,
                      shape: BoxShape.circle,
                    ),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 10),
          Text(
            'RECORDING',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              fontWeight: FontWeight.w800,
              letterSpacing: 1.2,
              color: DesignColors.error,
            ),
          ),
          const Spacer(),
          Icon(
            Icons.mic,
            size: 16,
            color: DesignColors.error.withValues(alpha: 0.85),
          ),
        ],
      ),
    );
  }

  Widget _buildTimerRow(Color textColor) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Text(
            _mmss(widget.elapsed),
            style: GoogleFonts.spaceMono(
              fontSize: 32,
              fontWeight: FontWeight.w700,
              color: textColor,
              height: 1.0,
            ),
          ),
          const SizedBox(width: 14),
          // Animated "audio level" bars — three bars that pulse out of
          // phase. Not driven by real mic amplitude (deferred polish)
          // but the staggered motion sells "audio is flowing" better
          // than a static glyph at no per-frame compute cost.
          _LevelBars(controller: _barsController),
        ],
      ),
    );
  }

  Widget _buildTranscript(
      String transcript, Color textColor, bool isDark) {
    final empty = transcript.isEmpty;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(14, 4, 14, 10),
      child: Text(
        empty ? 'Listening…' : transcript,
        maxLines: 3,
        overflow: TextOverflow.ellipsis,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 15,
          height: 1.35,
          fontWeight: empty ? FontWeight.w400 : FontWeight.w500,
          fontStyle: empty ? FontStyle.italic : FontStyle.normal,
          color: empty
              ? (isDark
                  ? Colors.white.withValues(alpha: 0.55)
                  : DesignColors.textMutedLight)
              : textColor,
        ),
      ),
    );
  }

  Widget _buildFooter(bool isDark) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
      decoration: BoxDecoration(
        color: (isDark ? Colors.white : Colors.black)
            .withValues(alpha: 0.04),
        borderRadius: const BorderRadius.only(
          bottomLeft: Radius.circular(15),
          bottomRight: Radius.circular(15),
        ),
      ),
      child: Row(
        children: [
          Icon(
            Icons.send,
            size: 13,
            color: DesignColors.primary.withValues(alpha: 0.85),
          ),
          const SizedBox(width: 5),
          Text(
            'Release to send',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              color: DesignColors.primary.withValues(alpha: 0.85),
            ),
          ),
          const Spacer(),
          Icon(
            Icons.cancel_outlined,
            size: 13,
            color: DesignColors.textMuted,
          ),
          const SizedBox(width: 5),
          Text(
            'Drag away to cancel',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }
}

/// Three staggered vertical bars; each oscillates between a min and
/// max height at a different phase so the cluster feels "live".
class _LevelBars extends StatelessWidget {
  const _LevelBars({required this.controller});

  final AnimationController controller;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        final t = controller.value;
        return Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            _bar(_heightFor(t, 0.0)),
            const SizedBox(width: 4),
            _bar(_heightFor(t, 0.33)),
            const SizedBox(width: 4),
            _bar(_heightFor(t, 0.66)),
          ],
        );
      },
    );
  }

  double _heightFor(double t, double phase) {
    // Triangle wave with phase offset; clamped to [0.25, 1.0].
    final raw = (t + phase) % 1.0;
    final tri = raw < 0.5 ? raw * 2 : (1.0 - raw) * 2;
    return 0.25 + tri * 0.75;
  }

  Widget _bar(double scale) {
    final h = 8 + 20 * scale;
    return Container(
      width: 5,
      height: h,
      decoration: BoxDecoration(
        color: DesignColors.error,
        borderRadius: BorderRadius.circular(2.5),
      ),
    );
  }
}
