import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';
import 'propose_card_visuals.dart';

/// WS4 / ADR-046 — per-kind propose card for `project.create`.
///
/// In the inline-spec model a project's spec IS its config_yaml; the
/// steward composes one and proposes it, and the director reviews the spec
/// on this card before approving. change_spec carries the spec inline:
/// - name (project name — the at-a-glance signal)
/// - goal (intent text)
/// - kind (goal | standing)
/// - config_yaml (the full inline spec: phases, deliverables, criteria,
///   tasks, plan, typed parameters)
/// - parameters_json (bound parameter values)
/// - on_create_template_id (the bound domain steward — bound at create,
///   spawned later via the project's Start)
///
/// The card surfaces name + goal + bound steward as the headline and puts
/// the full config_yaml behind "View spec" so approval is an informed
/// review (#39/#40), not a blind yes.
class ProposeCardProjectCreate extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardProjectCreate({
    super.key,
    required this.attention,
    this.myTier = 'principal',
    this.onResolved,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final isAddressee = isAddresseeOfPropose(attention, myTier);
    final stalled = isStalledPropose(attention);

    final changeSpec = decodeJsonObject(attention['change_spec']);
    final name = (changeSpec['name'] ?? '').toString();
    final goal = (changeSpec['goal'] ?? '').toString();
    final steward = (changeSpec['on_create_template_id'] ?? '').toString();
    final configYaml = (changeSpec['config_yaml'] ?? '').toString();
    final addressee = (attention['assigned_tier'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final phaseCount = _countPhases(configYaml);
    final nameLabel = name.isEmpty ? '(unnamed project)' : name;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (stalled && !isAddressee) StalledPill(addressee: addressee),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        // Header: project name as the primary signal.
        Row(
          children: [
            Icon(Icons.create_new_folder_outlined, size: 14, color: mutedColor),
            const SizedBox(width: 4),
            Expanded(
              child: Text(
                nameLabel,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: isDark ? Colors.white : Colors.black87,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (phaseCount > 0)
              Text(
                '$phaseCount ${phaseCount == 1 ? 'phase' : 'phases'}',
                style: GoogleFonts.jetBrainsMono(fontSize: 10, color: mutedColor),
              ),
          ],
        ),
        if (goal.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            goal,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
          ),
        ],
        if (steward.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'steward: $steward',
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: mutedColor),
          ),
        ],
        const SizedBox(height: 10),
        if (isAddressee) ...[
          if (configYaml.isNotEmpty) ...[
            _ViewSpecButton(
              name: nameLabel,
              configYaml: configYaml,
            ),
            const SizedBox(height: 8),
          ],
          PrimaryProposeActions(id: id, onResolved: onResolved),
        ] else
          StalledProposeActions(
            attention: attention,
            onResolved: onResolved,
            viewSourceLabel: 'View spec',
            onViewSource: configYaml.isEmpty
                ? null
                : () => showProjectSpecSheet(context, nameLabel, configYaml),
          ),
      ],
    );
  }

  /// Counts the entries under the spec's top-level `phases:` list by a
  /// shallow scan — enough for an at-a-glance "N phases" badge without a
  /// YAML parser. Returns 0 when no `phases:` block is found.
  static int _countPhases(String yaml) {
    if (yaml.isEmpty) return 0;
    final lines = yaml.split('\n');
    var inPhases = false;
    var count = 0;
    for (final raw in lines) {
      final line = raw.replaceAll('\t', '  ');
      final trimmed = line.trimRight();
      if (!inPhases) {
        if (RegExp(r'^phases\s*:\s*$').hasMatch(trimmed)) {
          inPhases = true;
        } else if (RegExp(r'^phases\s*:\s*\[').hasMatch(trimmed)) {
          // Inline flow list: count comma-separated entries.
          final inner = trimmed.replaceFirst(RegExp(r'^phases\s*:\s*\['), '')
              .replaceAll(']', '');
          if (inner.trim().isEmpty) return 0;
          return inner.split(',').where((s) => s.trim().isNotEmpty).length;
        }
        continue;
      }
      // Inside the block list: count `  - item` lines; stop at the next
      // top-level (non-indented, non-list) key.
      if (RegExp(r'^\s+-\s+').hasMatch(line)) {
        count++;
      } else if (trimmed.isNotEmpty && !line.startsWith(' ')) {
        break;
      }
    }
    return count;
  }
}

class _ViewSpecButton extends StatelessWidget {
  final String name;
  final String configYaml;
  const _ViewSpecButton({required this.name, required this.configYaml});

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      icon: const Icon(Icons.description_outlined, size: 16),
      label: const Text('View spec'),
      onPressed: () => showProjectSpecSheet(context, name, configYaml),
    );
  }
}

/// Bottom sheet showing the full inline project spec (config_yaml) in
/// monospace so the director can review every phase / criterion / parameter
/// before approving the create.
Future<void> showProjectSpecSheet(
  BuildContext context,
  String name,
  String configYaml,
) {
  final isDark = Theme.of(context).brightness == Brightness.dark;
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) {
      return DraggableScrollableSheet(
        initialChildSize: 0.7,
        minChildSize: 0.4,
        maxChildSize: 0.95,
        expand: false,
        builder: (ctx, scrollController) {
          return Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    const Icon(Icons.description_outlined, size: 18),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        'Spec — $name',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    IconButton(
                      icon: const Icon(Icons.close, size: 18),
                      onPressed: () => Navigator.of(ctx).pop(),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Expanded(
                  child: SingleChildScrollView(
                    controller: scrollController,
                    child: SelectableText(
                      configYaml,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        height: 1.4,
                        color: isDark ? Colors.white70 : Colors.black87,
                      ),
                    ),
                  ),
                ),
              ],
            ),
          );
        },
      );
    },
  );
}
