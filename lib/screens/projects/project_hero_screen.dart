import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'overview_widgets/registry.dart';

/// Standalone "Hero" host screen — wraps whichever `overview_widget` the
/// project's template declared (task_milestone_list / recent_artifacts /
/// children_status / experiment_dash / paper_acceptance / etc.) in a
/// dedicated Scaffold.
///
/// The hero is also rendered inline inside the Overview tab's 3-layer
/// chassis (header / hero / tiles), but the steward + deep-link route
/// `termipod://project/<id>/hero` lands here so the user can dive
/// straight into the centerpiece widget — same treatment as the
/// shortcut tiles, which each open their own dedicated screen.
class ProjectHeroScreen extends StatelessWidget {
  final Map<String, dynamic> project;
  const ProjectHeroScreen({super.key, required this.project});

  @override
  Widget build(BuildContext context) {
    final raw = (project['overview_widget'] ?? '').toString();
    final resolved = normalizeOverviewWidget(raw);
    final spec = overviewWidgetSpecFor(resolved);
    final projectName = (project['name'] ?? '').toString();
    return Scaffold(
      appBar: AppBar(
        title: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              spec.label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 16,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (projectName.isNotEmpty)
              Text(
                projectName,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  fontWeight: FontWeight.w500,
                ),
              ),
          ],
        ),
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          buildOverviewWidget(resolved, OverviewContext(project: project)),
        ],
      ),
    );
  }
}
