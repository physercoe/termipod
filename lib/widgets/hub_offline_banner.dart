import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// Inline "Offline · last updated HH:MM" banner for hub list screens.
///
/// Rendered above the list body whenever the current snapshot came from
/// the on-disk read-through cache instead of a fresh network call. Pass
/// null to render nothing (fresh data path).
class HubOfflineBanner extends StatelessWidget {
  final DateTime? staleSince;
  final VoidCallback? onRetry;
  const HubOfflineBanner({super.key, required this.staleSince, this.onRetry});

  @override
  Widget build(BuildContext context) {
    final when = staleSince;
    if (when == null) return const SizedBox.shrink();
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: const BoxDecoration(
        color: Color(0x332A2B36),
        border: Border(
          bottom: BorderSide(color: DesignColors.borderDark, width: 0.5),
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.cloud_off, size: 14, color: DesignColors.warning),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Offline · last updated ${_formatHm(when)}',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textSecondary,
              ),
            ),
          ),
          if (onRetry != null)
            InkWell(
              onTap: onRetry,
              child: Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: 6, vertical: 2),
                child: Text(
                  'Retry',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: DesignColors.primary,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  static String _formatHm(DateTime t) {
    final local = t.toLocal();
    final h = local.hour.toString().padLeft(2, '0');
    final m = local.minute.toString().padLeft(2, '0');
    return '$h:$m';
  }
}
