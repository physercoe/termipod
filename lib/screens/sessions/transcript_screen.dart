// TranscriptScreen — the dedicated full-screen transcript route (P3 —
// docs/plans/agent-transcript-debug-and-header-parity.md).
//
// A constrained host (the project-agent sheet, the steward overlay) wires
// `LiveFeed.onExpand` to push this screen, which runs the feed in its
// full-screen `dense: false` mode: the lens unfolds into a horizontal bar
// with per-lens counts — the richest debugging surface for a long run.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../widgets/live_feed.dart';

class TranscriptScreen extends StatelessWidget {
  final String agentId;
  final String? sessionId;
  final String title;
  const TranscriptScreen({
    super.key,
    required this.agentId,
    this.sessionId,
    required this.title,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: LiveFeed(
        agentId: agentId,
        sessionId: sessionId,
        dense: false,
      ),
    );
  }
}
