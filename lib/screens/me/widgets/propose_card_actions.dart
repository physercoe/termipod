import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';

/// ADR-030 W15-W18 — shared action-row widgets for the per-kind
/// propose cards. Two flavours mirror the two card variants returned
/// by `isAddresseeOfPropose`:
///
/// - [PrimaryProposeActions] — the viewer IS the addressee. Render
///   Approve / Reject buttons that POST decide via the existing
///   hub.decide flow. No override semantics.
/// - [StalledProposeActions] — the viewer is NOT the addressee but
///   the row escalated to their tier. Render Override / View source.
///   Override opens a confirmation sheet that takes a reason and
///   POSTs decide with `override=true`. The reason field is required
///   (matches ADR-030 W9's override audit-meta expectation).
///
/// W20 will replace the inline Override dialog here with a proper
/// confirmation sheet (matching the D-8 sheet design). For now the
/// inline dialog gives the principal a working override path.

class PrimaryProposeActions extends ConsumerWidget {
  final String id;
  final VoidCallback? onResolved;
  const PrimaryProposeActions({super.key, required this.id, this.onResolved});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        OutlinedButton.icon(
          icon: const Icon(Icons.check, size: 16),
          label: const Text('Approve'),
          onPressed: () => _decide(context, ref, 'approve'),
        ),
        const SizedBox(width: 8),
        OutlinedButton.icon(
          icon: const Icon(Icons.close, size: 16),
          label: const Text('Reject'),
          style: OutlinedButton.styleFrom(foregroundColor: DesignColors.error),
          onPressed: () => _decide(context, ref, 'reject'),
        ),
      ],
    );
  }

  Future<void> _decide(BuildContext context, WidgetRef ref, String decision) async {
    try {
      await ref.read(hubProvider.notifier).decide(id, decision, by: '@mobile');
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decision recorded: $decision')),
        );
      }
      onResolved?.call();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decide failed: $e')),
        );
      }
    }
  }
}

class StalledProposeActions extends ConsumerWidget {
  final String id;
  final VoidCallback? onResolved;
  final String viewSourceLabel;
  final VoidCallback? onViewSource;

  const StalledProposeActions({
    super.key,
    required this.id,
    this.onResolved,
    this.viewSourceLabel = 'View source',
    this.onViewSource,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        FilledButton.icon(
          icon: const Icon(Icons.gavel, size: 16),
          label: const Text('Override'),
          onPressed: () => _override(context, ref),
        ),
        OutlinedButton.icon(
          icon: const Icon(Icons.open_in_new, size: 16),
          label: Text(viewSourceLabel),
          onPressed: onViewSource,
        ),
      ],
    );
  }

  Future<void> _override(BuildContext context, WidgetRef ref) async {
    final reason = await _promptForReason(context);
    if (reason == null || reason.isEmpty) return;
    try {
      // ADR-030 W9: override=true paired with decision='override' (or
      // 'approve', the hub accepts either when override=true). We use
      // 'override' explicitly so the decisions_json reads honestly in
      // the audit trail.
      await ref.read(hubProvider.notifier).decide(
            id,
            'override',
            by: '@principal',
            reason: reason,
            override: true,
          );
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Override recorded')),
        );
      }
      onResolved?.call();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Override failed: $e')),
        );
      }
    }
  }

  Future<String?> _promptForReason(BuildContext context) async {
    final controller = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Override decision'),
        content: TextField(
          controller: controller,
          autofocus: true,
          maxLines: 3,
          decoration: const InputDecoration(
            labelText: 'Reason (required)',
            hintText: 'Why are you overriding the addressee\'s decision?',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              final r = controller.text.trim();
              if (r.isEmpty) return;
              Navigator.pop(ctx, r);
            },
            child: const Text('Override'),
          ),
        ],
      ),
    );
  }
}
