import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';

/// Floating recording indicator anchored near the steward puck while a
/// Mode A (puck long-press) voice recording is in progress. Renders:
///
/// - Header: red pulsing dot + elapsed timer + drag-to-cancel hint.
/// - Transcript: latest streaming partial (max 2 lines, ellipsis).
///
/// The plan's full HUD also includes a soundwave strip backed by PCM
/// RMS — that's a v1.0.537+ polish; v1.0.536 ships the strict-minimum
/// useful signal so the user knows the mic is hearing them.
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
    with SingleTickerProviderStateMixin {
  late final AnimationController _pulseController;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 900),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _pulseController.dispose();
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
    final surface = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final textColor =
        isDark ? Colors.white : DesignColors.textPrimaryLight;
    final transcript = widget.transcript.trim();

    return Material(
      elevation: 8,
      borderRadius: BorderRadius.circular(12),
      color: surface,
      child: ConstrainedBox(
        constraints: const BoxConstraints(
          maxWidth: 280,
          minWidth: 220,
        ),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 10, 12, 12),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ScaleTransition(
                    scale: Tween<double>(begin: 0.7, end: 1.0)
                        .animate(_pulseController),
                    child: Container(
                      width: 10,
                      height: 10,
                      decoration: const BoxDecoration(
                        color: DesignColors.error,
                        shape: BoxShape.circle,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Text(
                    _mmss(widget.elapsed),
                    style: GoogleFonts.spaceMono(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                      color: textColor,
                    ),
                  ),
                  const Spacer(),
                  Text(
                    'drag away to cancel',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 10,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              SizedBox(
                width: double.infinity,
                child: Text(
                  transcript.isEmpty ? '…listening' : transcript,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    color: transcript.isEmpty
                        ? DesignColors.textMuted
                        : textColor,
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
