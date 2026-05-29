// AgentFeed interaction cards — the inline approval/selection surfaces
// the steward chat renders for open attention items.
//
// Cluster wedge of the agent_feed split (docs/plans/agent-feed-split.md,
// W5). Two container-mounted lists (PendingPermissionPrompts,
// PendingSelections) plus the per-row cards they build (permission
// prompt, plan-approval body, compaction body, selection card). Only the
// two list widgets are referenced cross-library (by the container), so
// they alone are public; every card below them is interaction-only and
// stays private. The two byte-identical `_payloadOf` statics that the
// two lists each carried are folded into one private top-level helper
// here — single-cluster, so it stays private per the lazy/cross-cluster
// rule the split follows.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import 'feed_render.dart';

/// Decode an attention item's `pending_payload` into a string-keyed map.
/// Folded from the two byte-identical statics that PendingPermissionPrompts
/// and PendingSelections each carried (agent_feed split W5). The payload
/// arrives either pre-decoded (a Map) or as a JSON string; anything else
/// yields an empty map.
Map<String, dynamic> _payloadOf(Map<String, dynamic> attention) {
  final raw = attention['pending_payload'];
  if (raw is Map) return raw.cast<String, dynamic>();
  if (raw is String && raw.isNotEmpty) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map) return decoded.cast<String, dynamic>();
    } catch (_) {}
  }
  return const {};
}

/// W1.A — inline tool-call approval surface for the steward chat.
///
/// Reads open `kind=permission_prompt` attention items from
/// hubProvider, filters to ones whose pending payload's `agent_id`
/// matches this AgentFeed's agentId, renders a card per pending
/// item. Each card shows the tier (significant / strategic),
/// tool name + input preview, and Approve / Deny buttons.
///
/// Strategic tier requires a typed reason and is non-default-yes —
/// the user must explicitly tap Approve, no Enter-to-confirm. This
/// is the load-bearing difference between "user delegated this
/// scope" and "user genuinely intervened".
///
/// Trivial / Routine tiers don't reach here: the server short-
/// circuits them in mcpPermissionPrompt with an audit-only allow.
class PendingPermissionPrompts extends ConsumerWidget {
  const PendingPermissionPrompts();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feed = context.findAncestorStateOfType<_AgentFeedState>();
    final agentId = feed?.widget.agentId ?? '';
    final attention = ref
            .watch(hubProvider.select((s) => s.value?.attention)) ??
        const <Map<String, dynamic>>[];
    final pending = <Map<String, dynamic>>[];
    for (final a in attention) {
      if ((a['kind'] ?? '').toString() != 'permission_prompt') continue;
      if ((a['status'] ?? '').toString() != 'open') continue;
      final payload = _payloadOf(a);
      if (agentId.isNotEmpty &&
          (payload['agent_id'] ?? '').toString() != agentId) {
        continue;
      }
      pending.add(a);
    }
    if (pending.isEmpty) return const SizedBox.shrink();
    return Material(
      color: Colors.transparent,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final a in pending)
            _PermissionPromptCard(
              attention: a,
              payload: _payloadOf(a),
            ),
        ],
      ),
    );
  }

}

class _PermissionPromptCard extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;
  final Map<String, dynamic> payload;
  const _PermissionPromptCard({
    required this.attention,
    required this.payload,
  });

  @override
  ConsumerState<_PermissionPromptCard> createState() =>
      _PermissionPromptCardState();
}

class _PermissionPromptCardState
    extends ConsumerState<_PermissionPromptCard> {
  bool _sending = false;
  String? _error;
  final TextEditingController _reasonCtrl = TextEditingController();

  @override
  void dispose() {
    _reasonCtrl.dispose();
    super.dispose();
  }

  String get _tier =>
      (widget.payload['tier'] ?? 'significant').toString().toLowerCase();
  bool get _isStrategic => _tier == 'strategic';

  /// ADR-027 W8: dialog_type discriminator on permission_prompt
  /// attention items. Defaults to tool_permission for backward
  /// compatibility with rows that pre-date the discriminator.
  ///
  ///   tool_permission (default) — render tool name + input preview
  ///   plan_approval             — render plan_body as markdown
  ///   compaction                — render "Compact context now?" prompt
  ///
  /// user_question is NOT handled here — it goes through
  /// approval_request agent_events + _ApprovalCard, not attention_items.
  String get _dialogType =>
      (widget.payload['dialog_type'] ?? 'tool_permission').toString();

  Future<void> _decide(String decision) async {
    final id = (widget.attention['id'] ?? '').toString();
    if (id.isEmpty) return;
    final reason = _reasonCtrl.text.trim();
    if (_isStrategic && decision == 'approve' && reason.isEmpty) {
      setState(() => _error = 'Strategic-tier approvals require a reason');
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.decideAttention(
        id,
        decision: decision,
        reason: reason.isEmpty ? null : reason,
      );
      // refreshAll picks up the resolved attention so this card vanishes
      // on the next provider tick. Cheaper than a per-item invalidate
      // and consistent with the pattern used by other decide flows.
      await ref.read(hubProvider.notifier).refreshAll();
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final scheme = Theme.of(context).colorScheme;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;

    // ADR-027 W8: per-dialog_type title + body + button labels.
    final dialogType = _dialogType;
    final toolName = (widget.payload['tool_name'] ?? 'tool').toString();
    final input = widget.payload['input'];
    final inputText = input == null
        ? ''
        : (input is String ? input : feedJsonPretty(input));
    final tierColor = switch (_tier) {
      'strategic' => DesignColors.error,
      'significant' => DesignColors.warning,
      _ => DesignColors.primary,
    };
    final String headerTitle;
    final String approveLabel;
    final String denyLabel;
    switch (dialogType) {
      case 'plan_approval':
        headerTitle = 'Approve plan?';
        approveLabel = 'Approve';
        denyLabel = 'Reject';
        break;
      case 'compaction':
        headerTitle = 'Compact context?';
        approveLabel = 'Compact';
        denyLabel = 'Defer';
        break;
      default:
        headerTitle = 'Approve $toolName?';
        approveLabel = _isStrategic ? 'Approve (strategic)' : 'Approve';
        denyLabel = 'Deny';
    }
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: scheme.surface,
        border: Border.all(color: tierColor.withValues(alpha: 0.6), width: 1.5),
        borderRadius: BorderRadius.circular(10),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.08),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Icon(Icons.gavel, size: 16, color: tierColor),
              const SizedBox(width: 6),
              Text(
                _tier.toUpperCase(),
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  fontWeight: FontWeight.w800,
                  color: tierColor,
                  letterSpacing: 0.6,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  headerTitle,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          // Body — per dialog_type. ADR-027 W8.
          if (dialogType == 'plan_approval') ...[
            const SizedBox(height: 6),
            _PlanApprovalBody(
              planBody: (widget.payload['plan_body'] ?? '').toString(),
            ),
          ] else if (dialogType == 'compaction') ...[
            const SizedBox(height: 6),
            _CompactionBody(
              trigger: (widget.payload['trigger'] ?? '').toString(),
              customInstructions:
                  (widget.payload['custom_instructions'] ?? '').toString(),
            ),
          ] else if (inputText.isNotEmpty) ...[
            const SizedBox(height: 6),
            CollapsibleMono(text: inputText),
          ],
          if (_isStrategic) ...[
            const SizedBox(height: 8),
            TextField(
              controller: _reasonCtrl,
              enabled: !_sending,
              minLines: 1,
              maxLines: 3,
              style: GoogleFonts.jetBrainsMono(fontSize: 11),
              decoration: InputDecoration(
                isDense: true,
                hintText: 'Reason required for strategic-tier approvals',
                hintStyle: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: muted),
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 10, vertical: 8),
                border: const OutlineInputBorder(),
              ),
            ),
          ],
          if (_error != null) ...[
            const SizedBox(height: 6),
            Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ],
          const SizedBox(height: 8),
          Row(
            children: [
              FilledButton.tonal(
                onPressed: _sending ? null : () => _decide('reject'),
                style: FilledButton.styleFrom(
                  backgroundColor: DesignColors.error.withValues(alpha: 0.15),
                  foregroundColor: DesignColors.error,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(denyLabel),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: _sending ? null : () => _decide('approve'),
                style: FilledButton.styleFrom(
                  backgroundColor: dialogType == 'plan_approval'
                      ? DesignColors.primary
                      : (_isStrategic
                          ? DesignColors.error
                          : DesignColors.success),
                  foregroundColor: Colors.white,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(approveLabel),
              ),
              const Spacer(),
              if (_sending)
                const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// ADR-027 W8: body widget for permission_prompt rows whose
/// dialog_type is "plan_approval". Renders the agent's proposed plan
/// (carried in payload.plan_body) as markdown so the principal can
/// scan it inside the approval card. Empty plan_body falls back to a
/// muted placeholder rather than rendering an empty body.
class _PlanApprovalBody extends StatelessWidget {
  final String planBody;
  const _PlanApprovalBody({required this.planBody});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    if (planBody.trim().isEmpty) {
      return Text(
        '(no plan body provided)',
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
      );
    }
    final textColor = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    return ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 360),
      child: SingleChildScrollView(
        child: MarkdownBody(
          data: planBody,
          selectable: true,
          shrinkWrap: true,
          styleSheet: MarkdownStyleSheet(
            p: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              height: 1.4,
              color: textColor,
            ),
            code: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: textColor,
            ),
            listBullet: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: textColor,
            ),
          ),
        ),
      ),
    );
  }
}

/// ADR-027 W8: body widget for permission_prompt rows whose
/// dialog_type is "compaction". Surfaces the trigger source (manual /
/// auto) + any custom instructions claude shipped with the
/// PreCompact hook payload so the principal can decide whether to
/// allow the context collapse now or defer.
class _CompactionBody extends StatelessWidget {
  final String trigger;
  final String customInstructions;
  const _CompactionBody({
    required this.trigger,
    required this.customInstructions,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final body = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final triggerLabel = trigger.isEmpty ? '(unspecified)' : trigger;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          'Trigger: $triggerLabel',
          style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
        ),
        if (customInstructions.trim().isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            customInstructions,
            style: GoogleFonts.spaceGrotesk(fontSize: 12, color: body),
          ),
        ],
      ],
    );
  }
}

/// Inline selection card for kind=select attention items raised by the
/// agent we're watching. The agent's request_select MCP call long-polls
/// for the user's pick; surfacing this card in the chat keeps the round-
/// trip in one place instead of forcing a trip to the Me page.
class PendingSelections extends ConsumerWidget {
  const PendingSelections();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feed = context.findAncestorStateOfType<_AgentFeedState>();
    final agentId = feed?.widget.agentId ?? '';
    final attention = ref
            .watch(hubProvider.select((s) => s.value?.attention)) ??
        const <Map<String, dynamic>>[];
    final pending = <Map<String, dynamic>>[];
    for (final a in attention) {
      if ((a['kind'] ?? '').toString() != 'select') continue;
      if ((a['status'] ?? '').toString() != 'open') continue;
      final payload = _payloadOf(a);
      if (agentId.isNotEmpty &&
          (payload['agent_id'] ?? '').toString() != agentId) {
        continue;
      }
      pending.add(a);
    }
    if (pending.isEmpty) return const SizedBox.shrink();
    return Material(
      color: Colors.transparent,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final a in pending)
            _SelectionCard(attention: a, payload: _payloadOf(a)),
        ],
      ),
    );
  }

}

class _SelectionCard extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;
  final Map<String, dynamic> payload;
  const _SelectionCard({required this.attention, required this.payload});

  @override
  ConsumerState<_SelectionCard> createState() => _SelectionCardState();
}

class _SelectionCardState extends ConsumerState<_SelectionCard> {
  bool _sending = false;
  String? _error;
  // Header label is "SELECT" — the action is "pick one of these
  // labelled options", which is sharper than the old generic "decision".

  Future<void> _pick(String? optionId, {String decision = 'approve'}) async {
    final id = (widget.attention['id'] ?? '').toString();
    if (id.isEmpty) return;
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await ref.read(hubProvider.notifier).decide(
            id,
            decision,
            by: '@mobile',
            optionId: optionId,
          );
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final question = (widget.payload['question'] ??
            widget.attention['summary'] ??
            'Selection needed')
        .toString();
    final optionsRaw = widget.payload['options'];
    final options = optionsRaw is List
        ? [for (final v in optionsRaw) v.toString()]
        : const <String>[];
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: scheme.surface,
        border: Border.all(
          color: DesignColors.primary.withValues(alpha: 0.6),
          width: 1.5,
        ),
        borderRadius: BorderRadius.circular(10),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.08),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Icon(Icons.help_outline,
                  size: 16, color: DesignColors.primary),
              const SizedBox(width: 6),
              Text(
                'SELECT',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  fontWeight: FontWeight.w800,
                  color: DesignColors.primary,
                  letterSpacing: 0.6,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  question,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          if (options.isEmpty)
            Text(
              'No options provided. Tap Approve or Reject below.',
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
            )
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final opt in options)
                  FilledButton.tonal(
                    onPressed: _sending ? null : () => _pick(opt),
                    style: FilledButton.styleFrom(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 6),
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Text(opt),
                  ),
              ],
            ),
          if (_error != null) ...[
            const SizedBox(height: 6),
            Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ],
          const SizedBox(height: 8),
          Row(
            children: [
              FilledButton.tonal(
                onPressed:
                    _sending ? null : () => _pick(null, decision: 'reject'),
                style: FilledButton.styleFrom(
                  backgroundColor:
                      DesignColors.error.withValues(alpha: 0.15),
                  foregroundColor: DesignColors.error,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: const Text('Reject'),
              ),
              if (options.isEmpty) ...[
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: _sending ? null : () => _pick(null),
                  style: FilledButton.styleFrom(
                    backgroundColor: DesignColors.success,
                    foregroundColor: Colors.white,
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 6),
                    minimumSize: const Size(0, 32),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: const Text('Approve'),
                ),
              ],
              const Spacer(),
              if (_sending)
                const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Per-event card. The kind drives which fields get a first-class
/// treatment; everything else falls through to a raw JSON block so we
/// stay forward-compatible with new event kinds the hub emits.
