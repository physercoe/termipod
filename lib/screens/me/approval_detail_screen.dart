import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../services/hub/open_steward_session.dart';
import '../../theme/design_colors.dart';

/// Detail view for an attention item (approval, select, template
/// proposal). Surfaces the full payload + requester chain + audit
/// references so the user can decide with context, per ADR-009 and
/// the device-walkthrough finding that title-only approval cards
/// don't carry enough information to act on.
///
/// The card on the Me page keeps its inline Approve/Deny buttons —
/// this screen is for the case where the user wants to read first.
class ApprovalDetailScreen extends ConsumerWidget {
  final Map<String, dynamic> attention;

  const ApprovalDetailScreen({super.key, required this.attention});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final summary = (attention['summary'] ?? '').toString();
    final kind = (attention['kind'] ?? '').toString();
    final severity = (attention['severity'] ?? '').toString();
    final scopeKind = (attention['scope_kind'] ?? '').toString();
    final scopeId = (attention['scope_id'] ?? '').toString();
    final actorKind = (attention['actor_kind'] ?? '').toString();
    final actorHandle = (attention['actor_handle'] ?? '').toString();
    final createdAt = (attention['created_at'] ?? '').toString();
    final tier = (attention['tier'] ?? '').toString();
    final pending = _decodePayload(attention['pending_payload']);

    final attentionId = (attention['id'] ?? '').toString();

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Approval detail',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          if (attentionId.isNotEmpty)
            IconButton(
              tooltip: 'Discuss with steward',
              icon: const Icon(Icons.forum_outlined),
              // ADR-009 D7 + Phase 2: open a session scoped to this
              // attention item so the steward sees the request's
              // context in its system prompt and the audit trail
              // links the conversation to the decision.
              onPressed: () {
                Navigator.of(context).pop();
                openStewardSession(
                  context,
                  ref,
                  scopeKind: 'attention',
                  scopeId: attentionId,
                );
              },
            ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (summary.isNotEmpty)
            Text(
              summary,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
          const SizedBox(height: 16),
          _Section(title: 'What', children: [
            _Field(label: 'Kind', value: kind),
            if (severity.isNotEmpty) _Field(label: 'Severity', value: severity),
            if (tier.isNotEmpty) _Field(label: 'Tier', value: tier),
          ]),
          if (actorKind.isNotEmpty || actorHandle.isNotEmpty)
            _Section(title: 'Who', children: [
              if (actorKind.isNotEmpty)
                _Field(label: 'Actor kind', value: actorKind),
              if (actorHandle.isNotEmpty)
                _Field(label: 'Actor handle', value: actorHandle),
            ]),
          _Section(title: 'Where', children: [
            _Field(label: 'Scope kind',
                value: scopeKind.isEmpty ? '(team)' : scopeKind),
            if (scopeId.isNotEmpty) _Field(label: 'Scope id', value: scopeId),
            if (createdAt.isNotEmpty)
              _Field(label: 'Raised at', value: createdAt),
          ]),
          if (pending != null && pending.isNotEmpty)
            _Section(title: 'Pending payload', children: [
              _PayloadBlock(payload: pending, muted: muted),
            ]),
          const SizedBox(height: 24),
          Text(
            'Use the Approve / Deny buttons on the Me-page card to '
            'resolve. This screen is read-only for now; inline actions '
            'will land here in a follow-up.',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: muted,
              fontStyle: FontStyle.italic,
            ),
          ),
        ],
      ),
    );
  }

  static Map<String, dynamic>? _decodePayload(dynamic raw) {
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return null;
  }
}

class _Section extends StatelessWidget {
  final String title;
  final List<Widget> children;
  const _Section({required this.title, required this.children});

  @override
  Widget build(BuildContext context) {
    if (children.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.6,
              color: Theme.of(context).colorScheme.primary,
            ),
          ),
          const SizedBox(height: 6),
          ...children,
        ],
      ),
    );
  }
}

class _Field extends StatelessWidget {
  final String label;
  final String value;
  const _Field({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 96,
            child: Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: muted,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurface,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PayloadBlock extends StatelessWidget {
  final Map<String, dynamic> payload;
  final Color muted;
  const _PayloadBlock({required this.payload, required this.muted});

  @override
  Widget build(BuildContext context) {
    final pretty =
        const JsonEncoder.withIndent('  ').convert(payload);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: muted.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(8),
      ),
      child: SelectableText(
        pretty,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: Theme.of(context).colorScheme.onSurface,
        ),
      ),
    );
  }
}
