import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import 'design_colors.dart';

/// Fixed four-value task priority enum. Matches the hub migration 0021
/// CHECK constraint and the MCP `tasks.*` schemas; new values must be
/// added in all three places.
enum TaskPriority { low, med, high, urgent }

extension TaskPriorityX on TaskPriority {
  /// Wire value stored on the task row.
  String get wire {
    switch (this) {
      case TaskPriority.low:
        return 'low';
      case TaskPriority.med:
        return 'med';
      case TaskPriority.high:
        return 'high';
      case TaskPriority.urgent:
        return 'urgent';
    }
  }

  /// User-visible short label for chips / menus.
  String get label => wire;

  /// Localized short label for chips / menus.
  String localizedLabel(AppLocalizations l10n) {
    switch (this) {
      case TaskPriority.low:
        return l10n.taskPriorityLow;
      case TaskPriority.med:
        return l10n.taskPriorityMed;
      case TaskPriority.high:
        return l10n.taskPriorityHigh;
      case TaskPriority.urgent:
        return l10n.taskPriorityUrgent;
    }
  }

  /// Higher = more attention. Used for client-side sorts that mirror the
  /// server's default order when a caller can't hit the network.
  int get rank {
    switch (this) {
      case TaskPriority.urgent:
        return 3;
      case TaskPriority.high:
        return 2;
      case TaskPriority.med:
        return 1;
      case TaskPriority.low:
        return 0;
    }
  }
}

/// Parses the server-side priority string. Missing/unknown values fall
/// back to [TaskPriority.med] — the same default the hub applies when
/// the column was not provided on insert.
TaskPriority parseTaskPriority(Object? raw) {
  final s = (raw ?? '').toString();
  switch (s) {
    case 'low':
      return TaskPriority.low;
    case 'high':
      return TaskPriority.high;
    case 'urgent':
      return TaskPriority.urgent;
    case 'med':
    default:
      return TaskPriority.med;
  }
}

/// Localized user-visible label for a task status wire value
/// (`todo` / `in_progress` / `blocked` / `done`). Unknown values fall
/// back to the raw wire string.
String taskStatusLabel(AppLocalizations l10n, String wire) {
  switch (wire) {
    case 'todo':
      return l10n.taskStatusTodo;
    case 'in_progress':
      return l10n.taskStatusInProgress;
    case 'blocked':
      return l10n.taskStatusBlocked;
    case 'done':
      return l10n.taskStatusDone;
    case 'cancelled':
      return l10n.taskStatusCancelled;
    default:
      return wire;
  }
}

/// Localized user-visible label for a project status wire value
/// (`active` / `archived`). Empty wire is treated as `active`.
/// Unknown values fall back to the raw wire string.
String projectStatusLabel(AppLocalizations l10n, String wire) {
  final s = wire.isEmpty ? 'active' : wire;
  switch (s) {
    case 'active':
      return l10n.projectStatusActive;
    case 'archived':
      return l10n.projectStatusArchived;
    default:
      return s;
  }
}

/// Single source of truth for the priority dot color. Kept separate from
/// [DesignColors] so the palette file stays a generic token bag.
Color taskPriorityColor(TaskPriority p) {
  switch (p) {
    case TaskPriority.low:
      return DesignColors.textMuted;
    case TaskPriority.med:
      return DesignColors.terminalBlue;
    case TaskPriority.high:
      return DesignColors.warning;
    case TaskPriority.urgent:
      return DesignColors.error;
  }
}

/// Compact 8px filled circle used next to task titles to signal priority
/// without burning row height. Muted grey on `low` keeps it unobtrusive;
/// amber/red on the top tiers is what drew people to labels in the
/// first place.
class TaskPriorityDot extends StatelessWidget {
  final TaskPriority priority;
  final double size;
  const TaskPriorityDot({
    super.key,
    required this.priority,
    this.size = 8,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: taskPriorityColor(priority),
        shape: BoxShape.circle,
      ),
    );
  }
}
