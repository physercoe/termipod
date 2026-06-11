import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/spawn_project_steward_sheet.dart';

/// Inline action widgets for attention items, shared between the
/// Me-page card and the approval-detail screen so a resolution from
/// either surface routes through the same /decide call. Extracted from
/// `me_screen.dart` to avoid a circular import — both consumers depend
/// on this file, and this file depends only on hub_provider + theme.
///
/// Two widgets:
///   * [InlineApprovalActions] — Approve/Deny buttons for kind=
///     approval_request, per-option buttons for kind=select.
///   * [InlineHelpRequestActions] — free-text composer + Send/Skip
///     for kind=help_request (request_help MCP tool).

class InlineApprovalActions extends ConsumerWidget {
  final String id;
  final String kind;
  final Map<String, dynamic>? pendingPayload;

  /// Optional callback invoked after a successful decide (approve,
  /// reject, or option pick). The Me-page card leaves this null —
  /// the row drops out of the open list on its own. The detail screen
  /// passes a Navigator.pop callback so a resolution closes the screen.
  final VoidCallback? onResolved;

  const InlineApprovalActions({
    super.key,
    required this.id,
    required this.kind,
    this.pendingPayload,
    this.onResolved,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final stewardTerm =
        ref.watch(vocabularyProvider).term(VocabAxis.roleSteward);
    final options = _options();
    // ADR-025 W4 — the general steward raises this when it can't operate
    // inside a project without a project-bound steward. The principal's
    // approval IS the spawn action: open the W7 host picker prefilled
    // with the suggested host, then resolve the attention item with the
    // resulting agent id so the audit trail links the two.
    if (kind == 'project_steward_request') {
      return Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          FilledButton.icon(
            icon: const Icon(Icons.bolt, size: 16),
            label: Text(l10n.spawnProjectSteward(stewardTerm.lower)),
            onPressed: () => _openProjectStewardSpawn(context, ref),
          ),
          OutlinedButton.icon(
            icon: const Icon(Icons.close, size: 16),
            label: Text(l10n.buttonReject),
            style: OutlinedButton.styleFrom(
              foregroundColor: DesignColors.error,
            ),
            onPressed: () => _decide(context, ref, 'reject'),
          ),
        ],
      );
    }
    if (kind == 'select' && options.isNotEmpty) {
      // Per-option buttons + Reject. Picking an option flows through
      // `decide(decision='approve', option_id=...)` so the hub's quorum
      // logic still applies and the agent gets the chosen option back
      // via the request_select long-poll.
      return Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          for (final opt in options)
            OutlinedButton(
              onPressed: () => _pick(context, ref, opt),
              child: Text(opt),
            ),
          OutlinedButton.icon(
            icon: const Icon(Icons.close, size: 16),
            label: Text(l10n.buttonReject),
            style: OutlinedButton.styleFrom(
              foregroundColor: DesignColors.error,
            ),
            onPressed: () => _decide(context, ref, 'reject'),
          ),
        ],
      );
    }
    // Informational kinds — the host-runner's idle detector raises
    // these to surface a "stuck at prompt" pane, and future system
    // notices land here too. They aren't approval requests; rendering
    // them with Approve/Reject was misleading (the user has nothing
    // to "approve" — they just want to acknowledge and clear the row).
    // A single Dismiss button routes through the same /decide endpoint
    // with decision='approve' so the audit trail records a resolution
    // and the row drops off the open list.
    if (_isInformational(kind)) {
      return OutlinedButton.icon(
        icon: const Icon(Icons.check_circle_outline, size: 16),
        label: Text(l10n.buttonDismiss),
        onPressed: () => _decide(context, ref, 'approve'),
      );
    }
    return Row(
      children: [
        OutlinedButton.icon(
          icon: const Icon(Icons.check, size: 16),
          label: Text(l10n.buttonApprove),
          onPressed: () => _decide(context, ref, 'approve'),
        ),
        const SizedBox(width: 8),
        OutlinedButton.icon(
          icon: const Icon(Icons.close, size: 16),
          label: Text(l10n.buttonReject),
          style: OutlinedButton.styleFrom(
            foregroundColor: DesignColors.error,
          ),
          onPressed: () => _decide(context, ref, 'reject'),
        ),
      ],
    );
  }

  /// Informational attention kinds — raised by background detectors
  /// to surface a state worth noticing, not by an agent waiting on a
  /// principal decision. The Approve/Reject pair doesn't fit; a single
  /// Dismiss is correct. v1.0.648: extracted so adding a new
  /// informational kind (e.g. 'system_notice', 'low_disk', 'budget_warning')
  /// is one-line.
  static bool _isInformational(String kind) {
    switch (kind) {
      case 'idle':
        return true;
      default:
        return false;
    }
  }

  List<String> _options() {
    final raw = pendingPayload?['options'];
    if (raw is! List) return const [];
    return [for (final v in raw) v.toString()];
  }

  Future<void> _pick(
    BuildContext context,
    WidgetRef ref,
    String option,
  ) async {
    final l10n = AppLocalizations.of(context)!;
    try {
      await ref.read(hubProvider.notifier).decide(
            id,
            'approve',
            by: '@mobile',
            optionId: option,
          );
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.pickedOption(option))),
        );
      }
      onResolved?.call();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.decideFailedError('$e'))),
        );
      }
    }
  }

  /// Opens the W7 host-picker prefilled from this attention item's
  /// pending payload. On a successful spawn the attention is resolved
  /// via decide(approve, body=agent_id) so the audit row links to the
  /// agent the general steward just got. Dismissing the sheet leaves
  /// the attention open — the user can retry.
  Future<void> _openProjectStewardSpawn(
    BuildContext context,
    WidgetRef ref,
  ) async {
    final l10n = AppLocalizations.of(context)!;
    final stewardTerm =
        ref.read(vocabularyProvider).term(VocabAxis.roleSteward);
    final projectId = (pendingPayload?['project_id'] ?? '').toString();
    if (projectId.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(l10n.missingProjectId),
      ));
      return;
    }
    final suggested = (pendingPayload?['suggested_host_id'] ?? '').toString();
    final agentId = await showSpawnProjectStewardSheet(
      context,
      projectId: projectId,
      suggestedHostId: suggested.isEmpty ? null : suggested,
    );
    if (agentId == null || agentId.isEmpty) return;
    try {
      await ref.read(hubProvider.notifier).decide(
            id,
            'approve',
            by: '@mobile',
            body: agentId,
          );
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(l10n.projectStewardSpawnedOk(stewardTerm.lower))),
        );
      }
      onResolved?.call();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.spawnedResolveFailedError('$e'))),
        );
      }
    }
  }

  Future<void> _decide(
    BuildContext context,
    WidgetRef ref,
    String decision,
  ) async {
    final l10n = AppLocalizations.of(context)!;
    try {
      await ref
          .read(hubProvider.notifier)
          .decide(id, decision, by: '@mobile');
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.decisionRecorded(decision))),
        );
      }
      onResolved?.call();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.decideFailedError('$e'))),
        );
      }
    }
  }
}

/// Free-text composer for attentions whose resolution is a typed
/// reply rather than a yes/no decision — currently `help_request` and
/// `elicit`. Send routes through the same `/decide` endpoint as
/// approve/select, with the body field carrying the answer back to the
/// requesting party (the agent's `request_help` long-poll for help
/// requests, or codex's parked `mcpServer/elicitation/request` for
/// elicit). The mode chip ("clarify" / "handoff" / "fill") surfaces
/// the framing so the principal sees at a glance whether this is a
/// routine question, a hand-back, or an MCP-server form.
class InlineHelpRequestActions extends ConsumerStatefulWidget {
  final String id;
  final String kind;
  final Map<String, dynamic>? pendingPayload;

  /// Optional callback invoked after a successful Send (approve+body)
  /// or Skip (reject). Lets the detail screen close itself once the
  /// agent's request is resolved; the Me-page card leaves it null and
  /// just lets the row drop out of the open list.
  final VoidCallback? onResolved;

  const InlineHelpRequestActions({
    super.key,
    required this.id,
    this.kind = 'help_request',
    this.pendingPayload,
    this.onResolved,
  });

  @override
  ConsumerState<InlineHelpRequestActions> createState() =>
      InlineHelpRequestActionsState();
}

class InlineHelpRequestActionsState
    extends ConsumerState<InlineHelpRequestActions> {
  final _controller = TextEditingController();
  bool _sending = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  String get _mode {
    if (widget.kind == 'elicit') return 'fill';
    final m = widget.pendingPayload?['mode'];
    return m is String ? m : 'clarify';
  }

  String? get _agentContext {
    if (widget.kind == 'elicit') {
      // For elicit, surface the message + the schema (if any) as
      // context so the principal knows what shape of reply is wanted.
      // The schema lift comes through pending_payload.params (the
      // codex JSON-RPC call's params), per driver_appserver's
      // marshalPending — we walk a couple of common path variants.
      final p = widget.pendingPayload;
      if (p == null) return null;
      final params = p['params'];
      if (params is Map) {
        // rmcp wraps the MCP elicitation/create payload under a
        // nested `params` key; surface its requestedSchema if there.
        final inner = params['params'];
        if (inner is Map && inner['requestedSchema'] != null) {
          return 'Reply with JSON matching: ${inner['requestedSchema']}';
        }
        if (params['requestedSchema'] != null) {
          return 'Reply with JSON matching: ${params['requestedSchema']}';
        }
      }
      return null;
    }
    final c = widget.pendingPayload?['context'];
    return (c is String && c.isNotEmpty) ? c : null;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final stewardTerm =
        ref.watch(vocabularyProvider).term(VocabAxis.roleSteward);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final isHandoff = _mode == 'handoff';
    final isFill = _mode == 'fill';
    final chipColor = isHandoff
        ? DesignColors.warning
        : (isFill ? DesignColors.terminalCyan : DesignColors.primary);
    final chipLabel = isHandoff
        ? l10n.modeHandBack
        : (isFill ? l10n.modeFill : l10n.modeClarify);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: Spacing.s4),
              decoration: BoxDecoration(
                color: chipColor.withValues(alpha: 0.15),
                borderRadius: Radii.smBorder,
              ),
              child: Text(
                chipLabel,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  fontWeight: FontWeight.w600,
                  color: chipColor,
                ),
              ),
            ),
          ],
        ),
        if (_agentContext != null) ...[
          const SizedBox(height: 8),
          Text(
            _agentContext!,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
          ),
        ],
        const SizedBox(height: 10),
        TextField(
          controller: _controller,
          enabled: !_sending,
          minLines: 2,
          maxLines: 6,
          decoration: InputDecoration(
            hintText: isFill
                ? l10n.replyHintFill
                : isHandoff
                    ? l10n.replyHintHandoff(stewardTerm.lower)
                    : l10n.replyHintClarify(stewardTerm.lower),
            isDense: true,
            border: const OutlineInputBorder(),
            contentPadding:
                const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s8),
          ),
        ),
        const SizedBox(height: 8),
        Row(
          children: [
            FilledButton.icon(
              icon: const Icon(Icons.send, size: 16),
              label: Text(l10n.buttonSend),
              onPressed: _sending ? null : _send,
            ),
            const SizedBox(width: 8),
            OutlinedButton.icon(
              icon: const Icon(Icons.close, size: 16),
              label: Text(l10n.buttonSkip),
              style: OutlinedButton.styleFrom(
                foregroundColor: DesignColors.error,
              ),
              onPressed: _sending ? null : _skip,
            ),
          ],
        ),
      ],
    );
  }

  Future<void> _send() async {
    final l10n = AppLocalizations.of(context)!;
    final body = _controller.text.trim();
    if (body.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.typeReplyOrSkip)),
      );
      return;
    }
    setState(() => _sending = true);
    try {
      await ref.read(hubProvider.notifier).decide(
            widget.id,
            'approve',
            by: '@mobile',
            body: body,
          );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.replySent)),
        );
      }
      widget.onResolved?.call();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.sendFailedError('$e'))),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _skip() async {
    final l10n = AppLocalizations.of(context)!;
    setState(() => _sending = true);
    try {
      await ref.read(hubProvider.notifier).decide(
            widget.id,
            'reject',
            by: '@mobile',
            reason: 'dismissed without reply',
          );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.dismissed)),
        );
      }
      widget.onResolved?.call();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.dismissFailedError('$e'))),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }
}
