import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import '../../../theme/tokens.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';
import 'propose_card_visuals.dart';

/// ADR-030 W18 — per-kind propose card for `agent.spawn`.
///
/// agent.spawn's change_spec IS a spawnIn JSON (the legacy
/// approval_request+spawnIn shape, re-routed through the propose
/// dispatcher per Phase 1 W8). The card surfaces the most actionable
/// fields: child_handle, engine kind, host (if pinned), project (if
/// bound). The full spawn_spec_yaml is a blob that the legacy
/// approval-detail screen already renders; the card stays compact and
/// punts to the Details affordance for the full YAML.
///
/// Override edge case (W9 plan §5): when the principal overrides an
/// agent.spawn propose, the Rollback emits a `agent.spawn.rollback_todo`
/// audit row rather than terminating the agent — `agent.terminate` is
/// a post-MVP propose kind. The card's Override button leaves the
/// rollback details to the W20 confirmation sheet's reason field.
class ProposeCardAgentSpawn extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardAgentSpawn({
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
    final childHandle = (changeSpec['child_handle'] ?? '').toString();
    final engineKind = (changeSpec['kind'] ?? '').toString();
    final hostId = (changeSpec['host_id'] ?? '').toString();
    final projectId = (changeSpec['project_id'] ?? '').toString();
    final reason = (attention['summary'] ?? '').toString();
    final addressee = (attention['assigned_tier'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (stalled && !isAddressee) StalledPill(addressee: addressee),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        // Header: handle + engine kind as a single bold line. This is
        // the at-a-glance signal — who would land + with what engine.
        Row(
          children: [
            Icon(Icons.smart_toy, size: 14, color: mutedColor),
            const SizedBox(width: 4),
            Expanded(
              child: Text(
                childHandle.isEmpty ? '(no handle)' : childHandle,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: isDark ? Colors.white : Colors.black87,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (engineKind.isNotEmpty)
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: Spacing.s4, vertical: Spacing.s2),
                decoration: BoxDecoration(
                  color: isDark
                      ? Colors.deepPurple.shade900
                      : Colors.deepPurple.shade100,
                  borderRadius: Radii.xsBorder,
                ),
                child: Text(
                  engineKind,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: FontSizes.label,
                    color: isDark
                        ? Colors.deepPurple.shade200
                        : Colors.deepPurple.shade900,
                  ),
                ),
              ),
          ],
        ),
        if (reason.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            reason,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
        if (hostId.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'host: $hostId',
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: mutedColor),
          ),
        ],
        if (projectId.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text(
            'project: $projectId',
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: mutedColor),
          ),
        ],
        const SizedBox(height: 10),
        if (isAddressee)
          PrimaryProposeActions(id: id, onResolved: onResolved)
        else
          StalledProposeActions(
            attention: attention,
            onResolved: onResolved,
            viewSourceLabel: 'View spawn detail',
            onViewSource: () => _viewSpawn(context, childHandle),
          ),
      ],
    );
  }

  static void _viewSpawn(BuildContext context, String handle) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          handle.isEmpty
              ? 'Open Details for the full spawn_spec_yaml'
              : 'Spawn $handle — open Details for full spec',
        ),
      ),
    );
  }
}
