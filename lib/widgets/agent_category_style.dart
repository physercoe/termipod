import 'package:flutter/material.dart';

import '../services/steward_handle.dart';
import '../theme/design_colors.dart';

/// Visual treatment — accent color + icon + short label — for an
/// [AgentCategory]. The single source of truth for the active-session
/// card's left stripe + tinted icon, and reusable anywhere agent kind
/// needs a glanceable mark. Color AND icon shape both encode the category
/// so the cue survives colour-blindness and crowded horizontal strips.
class AgentCategoryStyle {
  final Color color;
  final IconData icon;
  final String label;
  const AgentCategoryStyle({
    required this.color,
    required this.icon,
    required this.label,
  });
}

AgentCategoryStyle agentCategoryStyle(AgentCategory category) {
  switch (category) {
    case AgentCategory.teamSteward:
      return const AgentCategoryStyle(
        color: DesignColors.primary, // cyan — the always-on concierge
        icon: Icons.support_agent,
        label: 'steward · general',
      );
    case AgentCategory.projectSteward:
      return const AgentCategoryStyle(
        color: DesignColors.terminalBlue,
        icon: Icons.account_tree,
        label: 'project steward',
      );
    case AgentCategory.domainSteward:
      return const AgentCategoryStyle(
        color: DesignColors.terminalMagenta, // violet
        icon: Icons.hub,
        label: 'domain steward',
      );
    case AgentCategory.worker:
      return const AgentCategoryStyle(
        color: DesignColors.secondary, // amber — the executor tier
        icon: Icons.bolt,
        label: 'worker',
      );
  }
}
