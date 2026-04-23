import 'package:flutter/material.dart';

import 'audit_screen.dart';

/// Tier-1 Activity tab per `docs/ia-redesign.md` §6.3.
///
/// Wedge 1 (nav skeleton) — this is a thin wrapper around the existing
/// [AuditScreen] so the feed is promoted from a deeply-nested hub page
/// to a top-level tab. Wedge 5 reshapes the content: digest card at the
/// top, richer filters, mirror to Me tab.
class ActivityScreen extends StatelessWidget {
  const ActivityScreen({super.key});

  @override
  Widget build(BuildContext context) => const AuditScreen();
}
