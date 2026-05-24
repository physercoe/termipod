import 'package:flutter/material.dart';

import '../inline_actions.dart';
import 'propose_card_deliverable.dart';

/// ADR-030 Phase 3 — router widget that picks the per-kind propose
/// card (W15-W18) based on the attention row's `change_kind` field.
///
/// Falls back to [InlineApprovalActions] (Approve/Reject only) when:
/// - the attention is not actually a `propose` kind, OR
/// - the `change_kind` is one we don't yet have a per-kind card for
///   (e.g. agent.spawn / template.install ship after W15 lands; the
///   fallback keeps the screen usable in the meantime).
///
/// Single entry point so me_screen.dart needs ONE new branch instead
/// of a switch-on-change_kind. Per-kind cards register themselves
/// here when they ship (W15: deliverable.set_state; W16: phase.advance;
/// W17: task.set_status; W18: agent.spawn + template.install).
class ProposeCardRouter extends StatelessWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardRouter({
    super.key,
    required this.attention,
    this.myTier = 'principal',
    this.onResolved,
  });

  @override
  Widget build(BuildContext context) {
    final kind = (attention['kind'] ?? '').toString();
    final changeKind = (attention['change_kind'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    if (kind == 'propose') {
      switch (changeKind) {
        case 'deliverable.set_state':
          return ProposeCardDeliverable(
            attention: attention,
            myTier: myTier,
            onResolved: onResolved,
          );
        // W16/W17/W18 register here when they ship.
        default:
          break;
      }
    }
    // Fallback: generic Approve/Reject from the existing inline actions.
    return InlineApprovalActions(
      id: id,
      kind: kind,
      pendingPayload: _pendingPayload(),
    );
  }

  Map<String, dynamic>? _pendingPayload() {
    final p = attention['pending_payload'];
    if (p is Map<String, dynamic>) return p;
    return null;
  }
}
