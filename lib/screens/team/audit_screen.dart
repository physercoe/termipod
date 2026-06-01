import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../widgets/activity_feed.dart';
import '../../widgets/team_switcher.dart';

/// Activity tab per `docs/ia-redesign.md` §6.3 — the team's mutation feed
/// backed by `audit_events`. A thin Scaffold wrapper around the shared
/// [ActivityFeed]; the project detail Activity tab renders the same widget
/// scoped to a single project, so filters / search / tappable detail rows
/// stay in one place.
class AuditScreen extends StatelessWidget {
  const AuditScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.tabActivity,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: const [TeamSwitcher()],
      ),
      body: const ActivityFeed(),
    );
  }
}
