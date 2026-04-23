import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../theme/design_colors.dart';

/// Steward configuration placeholder per `docs/ia-redesign.md` §11 Wedge 6.
///
/// Wedge 7 fleshes this out (steward prompt, escalation policy, budget
/// caps, attention routing). For now this is a reserved entry point in
/// Team Settings so users see the capability is coming.
class StewardConfigScreen extends StatelessWidget {
  const StewardConfigScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.stewardConfigTitle,
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
              Icon(Icons.smart_toy_outlined,
                  size: 64, color: DesignColors.primary.withValues(alpha: 0.6)),
              const SizedBox(height: 16),
              Text(
                l10n.stewardConfigComingSoon,
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
