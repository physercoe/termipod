import 'package:flutter/material.dart';

import '../../theme/design_colors.dart';

/// Curated icon mapping for built-in templates (blueprint §6.1).
///
/// Templates surface in three places today:
///   1. TemplatesScreen - file templates under team/templates/{agents,
///      prompts,policies}/ (names like `steward.v1.yaml`).
///   2. _TemplatePickerSheet in project_create_sheet - project rows
///      with is_template=1 (names like `reproduce-paper`).
///   3. The spawn-agent sheet in hub_screen - agent-category file
///      templates only.
///
/// We treat the built-ins as a curated launcher (Slack Workflows /
/// Raycast style) so they are scannable at a glance. User-created
/// templates fall back to a colored-initial chip keyed off a stable
/// hash of their id - this avoids iconographic sprawl if user
/// templates proliferate.
///
/// Only Material Icons from the shipped set are used here - no new
/// asset files. All glyphs render monochrome in the app's primary
/// color (see templateIconWidget).

/// Normalises a template row's identifier into the key used by
/// [templateIconFor]. Accepts either the `id` field (for is_template
/// project rows) or the `name` field (for file templates), then
/// strips trailing versioned suffixes and file extensions so we can
/// match templates regardless of versioning. For example:
///   `steward.v1.yaml` -> `steward`
///   `reproduce-paper` -> `reproduce-paper`
///   `briefing.v2.md` -> `briefing`
String templateCanonicalKey(String raw) {
  var key = raw.trim().toLowerCase();
  if (key.isEmpty) return key;
  // Strip file extension.
  final dot = key.lastIndexOf('.');
  if (dot > 0) {
    final ext = key.substring(dot + 1);
    if (ext == 'yaml' || ext == 'yml' || ext == 'md' || ext == 'json') {
      key = key.substring(0, dot);
    }
  }
  // Strip trailing ".v1" / ".v2" style version suffix.
  final vMatch = RegExp(r'\.v\d+$').firstMatch(key);
  if (vMatch != null) {
    key = key.substring(0, vMatch.start);
  }
  return key;
}

/// Curated glyph for a built-in template key. Returns null for
/// unknown (user-created) templates so callers can render the
/// colored-initial fallback instead.
IconData? templateIconFor(String rawIdOrName) {
  final key = templateCanonicalKey(rawIdOrName);
  switch (key) {
    // Project templates (is_template=1 rows seeded from
    // hub/templates/projects/*.yaml).
    case 'reproduce-paper':
    case 'reproduce_paper':
      return Icons.menu_book_outlined;
    case 'ablation-sweep':
    case 'sweep':
      return Icons.tune;
    case 'benchmark-comparison':
      return Icons.compare_arrows;
    case 'write-memo':
      return Icons.edit_note_outlined;

    // Agent / prompt file templates under team/templates/.
    case 'steward':
    case 'steward_bootstrap':
    case 'agents.steward':
      return Icons.smart_toy_outlined;
    case 'briefing':
      return Icons.article_outlined;
    case 'ml-worker':
    case 'ml_worker':
      return Icons.memory_outlined;

    // Policies.
    case 'default':
      return Icons.policy_outlined;

    // Reserved built-ins from the blueprint curation plan that are
    // not seeded today but should land on a consistent glyph if
    // someone adds them later.
    case 'red-team':
    case 'red_team':
      return Icons.shield_outlined;
    case 'infra-ops':
    case 'infra_ops':
      return Icons.dns_outlined;
  }
  return null;
}

/// Deterministic palette used for the user-template fallback chip.
/// Picked from DesignColors so the app's primary + status tokens
/// stay the source of truth.
const List<Color> _fallbackPalette = <Color>[
  DesignColors.primary,
  DesignColors.secondary,
  DesignColors.terminalBlue,
  DesignColors.terminalMagenta,
  DesignColors.terminalGreen,
  DesignColors.terminalYellow,
];

/// Stable colour for an unknown template. We fold the key into a
/// small int with a cheap hash so the mapping is repeatable across
/// app launches without needing to persist a palette index.
Color fallbackChipColor(String rawIdOrName) {
  final key = templateCanonicalKey(rawIdOrName);
  if (key.isEmpty) return _fallbackPalette.first;
  var hash = 0;
  for (final unit in key.codeUnits) {
    hash = (hash * 31 + unit) & 0x7fffffff;
  }
  return _fallbackPalette[hash % _fallbackPalette.length];
}

/// First letter of [displayName] (upper-cased) for the fallback chip.
/// Falls back to `?` when the name is empty or starts with a
/// non-letter glyph - keeps the chip from rendering as a bare dot.
String fallbackChipInitial(String displayName) {
  final trimmed = displayName.trim();
  if (trimmed.isEmpty) return '?';
  // Plain substring keeps imports tight; the fallback is only shown
  // for user-created template names which are ASCII-ish in practice.
  return trimmed.substring(0, 1).toUpperCase();
}

/// Renders either the curated Material icon or the colored-initial
/// fallback at [size] x [size]. Pass the template's identifier as
/// [idOrName] (prefer the DB id when available; fall back to the
/// file name for file-based templates). [displayName] feeds the
/// fallback chip's initial when no curated glyph matches.
Widget templateIconWidget({
  required String idOrName,
  required String displayName,
  double size = 24,
  Color? color,
}) {
  final icon = templateIconFor(idOrName);
  if (icon != null) {
    return SizedBox(
      width: size,
      height: size,
      child: Icon(
        icon,
        size: size,
        color: color ?? DesignColors.primary,
      ),
    );
  }
  final bg = fallbackChipColor(idOrName);
  final initial = fallbackChipInitial(displayName);
  return Container(
    width: size,
    height: size,
    alignment: Alignment.center,
    decoration: BoxDecoration(
      color: bg.withValues(alpha: 0.18),
      borderRadius: BorderRadius.circular(size / 4),
      border: Border.all(color: bg.withValues(alpha: 0.55), width: 1),
    ),
    child: Text(
      initial,
      style: TextStyle(
        fontSize: size * 0.55,
        fontWeight: FontWeight.w700,
        color: bg,
        height: 1.0,
      ),
    ),
  );
}
