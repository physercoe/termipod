import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
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

AgentCategoryStyle agentCategoryStyle(AgentCategory category, AppLocalizations l10n) {
  switch (category) {
    case AgentCategory.teamSteward:
      return AgentCategoryStyle(
        color: DesignColors.primary,
        icon: Icons.support_agent,
        label: l10n.agentCategoryTeamSteward,
      );
    case AgentCategory.projectSteward:
      return AgentCategoryStyle(
        color: DesignColors.terminalBlue,
        icon: Icons.account_tree,
        label: l10n.agentCategoryProjectSteward,
      );
    case AgentCategory.domainSteward:
      return AgentCategoryStyle(
        color: DesignColors.terminalMagenta,
        icon: Icons.hub,
        label: l10n.agentCategoryDomainSteward,
      );
    case AgentCategory.worker:
      return AgentCategoryStyle(
        color: DesignColors.secondary,
        icon: Icons.bolt,
        label: l10n.agentCategoryWorker,
      );
  }
}
