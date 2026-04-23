import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../theme/design_colors.dart';

/// Councils placeholder per `docs/ia-redesign.md` §11 Wedge 6.
///
/// The full surface (multi-reviewer panels, quorum, rotation) lands in a
/// later wedge; this scaffold reserves the Team Settings entry point so
/// the IA is complete even before the implementation is.
class CouncilsScreen extends StatelessWidget {
  const CouncilsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.councilsTitle,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
      ),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(Icons.how_to_vote_outlined,
                  size: 64, color: DesignColors.primary.withValues(alpha: 0.6)),
              const SizedBox(height: 16),
              Text(
                l10n.councilsComingSoon,
                textAlign: TextAlign.center,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 14, color: DesignColors.textMuted),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
