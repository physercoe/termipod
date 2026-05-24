import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import 'override_sheet.dart';

/// ADR-030 W15-W18 — shared action-row widgets for the per-kind
/// propose cards. Two flavours mirror the two card variants returned
/// by `isAddresseeOfPropose`:
///
/// - [PrimaryProposeActions] — the viewer IS the addressee. Render
///   Approve / Reject buttons that POST decide via the existing
///   hub.decide flow. No override semantics.
/// - [StalledProposeActions] — the viewer is NOT the addressee but
///   the row escalated to their tier. Render Override / View source.
///   Override opens the W20 confirmation sheet ([showOverrideSheet])
///   that takes a required reason, shows the change_kind +
///   change_spec context, then POSTs decide with `override=true`.

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
  /// Full attention row (not just the id) so the W20 override sheet
  /// can render the change_kind + change_spec context block without
  /// a separate fetch. Kept-as-Map (no typed model — the mobile
  /// reads hub entities as JSON maps per repo convention).
  final Map<String, dynamic> attention;
  final VoidCallback? onResolved;
  final String viewSourceLabel;
  final VoidCallback? onViewSource;

  const StalledProposeActions({
    super.key,
    required this.attention,
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
          onPressed: () => _override(context),
        ),
        OutlinedButton.icon(
          icon: const Icon(Icons.open_in_new, size: 16),
          label: Text(viewSourceLabel),
          onPressed: onViewSource,
        ),
      ],
    );
  }

  Future<void> _override(BuildContext context) async {
    // ADR-030 W20 — modal bottom sheet replaces the inline AlertDialog
    // that v1.0.688's W15 shipped. The sheet handles the decide call
    // internally + surfaces errors via inline copy; returns true on
    // success, false on cancellation.
    final ok = await showOverrideSheet(context, attention: attention);
    if (!ok) return;
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Override recorded')),
      );
    }
    onResolved?.call();
  }
}
