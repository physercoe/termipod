import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/open_steward_session.dart';
import '../../theme/design_colors.dart';
import '../sessions/sessions_screen.dart';
import 'inline_actions.dart';

/// Detail view for an attention item (approval, select, help_request,
/// template proposal). Renders three layers of context so the principal
/// can decide without bouncing back to the chat first:
///
/// 1. **What** — kind, severity, summary, pending payload (the agent's
///    structured ask).
/// 2. **Where** — originating agent + session pointers, with a one-tap
///    "Open in chat" jump to the session that raised this attention,
///    plus a "Discuss with steward" button that opens a fresh
///    attention-scoped steward session for deeper analysis.
/// 3. **Why** — the last few transcript turns leading up to the
///    request, surfaced from `/v1/teams/{team}/attention/{id}/context`.
///    Empty for system-originated attentions (budget, spawn approval)
///    or pre-v1.0.336 rows that didn't capture a session pointer.
///
/// Inline actions mirror the Me-page card: Approve/Deny for
/// approval_request, per-option buttons for select, a free-text
/// composer for help_request. Resolving from here closes the screen.
class ApprovalDetailScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;

  const ApprovalDetailScreen({super.key, required this.attention});

  @override
  ConsumerState<ApprovalDetailScreen> createState() =>
      _ApprovalDetailScreenState();
}

class _ApprovalDetailScreenState extends ConsumerState<ApprovalDetailScreen> {
  Map<String, dynamic>? _context;
  bool _loadingContext = true;
  String? _contextError;

  @override
  void initState() {
    super.initState();
    _loadContext();
  }

  Future<void> _loadContext() async {
    final attentionId = (widget.attention['id'] ?? '').toString();
    if (attentionId.isEmpty) {
      setState(() => _loadingContext = false);
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loadingContext = false;
        _contextError = 'Hub not configured.';
      });
      return;
    }
    try {
      final ctx = await client.getAttentionContext(attentionId);
      if (!mounted) return;
      setState(() {
        _context = ctx;
        _loadingContext = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingContext = false;
        _contextError = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final summary = (widget.attention['summary'] ?? '').toString();
    final kind = (widget.attention['kind'] ?? '').toString();
    final severity = (widget.attention['severity'] ?? '').toString();
    final actorHandle = (widget.attention['actor_handle'] ?? '').toString();
    final createdAt = (widget.attention['created_at'] ?? '').toString();
    final pending = _decodePayload(widget.attention['pending_payload']);
    final attentionId = (widget.attention['id'] ?? '').toString();

    final ctx = _context;
    final ctxSessionID = (ctx?['session_id'] ?? '').toString();
    final ctxAgentID = (ctx?['agent_id'] ?? '').toString();
    final ctxAgentHandle =
        (ctx?['agent_handle'] ?? '').toString().isNotEmpty
            ? (ctx?['agent_handle'] ?? '').toString()
            : actorHandle;
    final ctxEvents = (ctx?['events'] as List?)?.cast<dynamic>() ?? const [];

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
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 4,
            children: [
              _Chip(label: _kindLabel(kind), color: DesignColors.primary),
              if (severity.isNotEmpty)
                _Chip(label: severity, color: _severityColor(severity)),
              if (kind == 'help_request' && pending != null)
                _Chip(
                  label: (pending['mode'] ?? 'clarify').toString() == 'handoff'
                      ? 'hand-back'
                      : 'clarify',
                  color: (pending['mode'] ?? 'clarify').toString() == 'handoff'
                      ? Colors.orange
                      : DesignColors.primary,
                ),
            ],
          ),
          const SizedBox(height: 20),

          // --- Inline actions: same widgets as the Me-page card. ---
          _InlineActions(
            id: attentionId,
            kind: kind,
            pendingPayload: pending,
            onResolved: () {
              if (mounted) Navigator.of(context).pop();
            },
          ),
          const SizedBox(height: 24),

          // --- Where: agent + session pointers. ---
          _Section(title: 'Origin', children: [
            if (ctxAgentHandle.isNotEmpty)
              _Field(label: 'Agent', value: ctxAgentHandle),
            if (createdAt.isNotEmpty)
              _Field(label: 'Raised', value: _formatTs(createdAt)),
            if (ctxSessionID.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: OutlinedButton.icon(
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: const Text('Open in chat'),
                  onPressed: () => _openSession(
                    context,
                    sessionID: ctxSessionID,
                    agentID: ctxAgentID,
                    handle: ctxAgentHandle,
                  ),
                ),
              ),
          ]),

          // --- Why: transcript leading up to the request. ---
          if (_loadingContext)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(child: CircularProgressIndicator()),
            )
          else if (_contextError != null)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(
                'Could not load context: $_contextError',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.error,
                ),
              ),
            )
          else if (ctxEvents.isNotEmpty)
            _Section(title: 'Recent transcript', children: [
              for (final e in ctxEvents.reversed)
                _TranscriptTile(event: (e as Map).cast<String, dynamic>()),
            ])
          else
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(
                'No transcript context recorded for this attention.',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: muted,
                  fontStyle: FontStyle.italic,
                ),
              ),
            ),

          if (pending != null && pending.isNotEmpty) ...[
            const SizedBox(height: 16),
            _Section(title: 'Pending payload', children: [
              _PayloadBlock(payload: pending, muted: muted),
            ]),
          ],
        ],
      ),
    );
  }

  void _openSession(
    BuildContext context, {
    required String sessionID,
    required String agentID,
    required String handle,
  }) {
    // SessionChatScreen pulls the session's real title from its own
    // backfill (it queries the session row by id); we just need a
    // sensible placeholder until that lands. Fall back to the agent
    // handle which the caller already has — it's what the principal
    // would recognise anyway ("research-steward", not a session UUID).
    Navigator.of(context).pushReplacement(
      MaterialPageRoute(
        builder: (_) => SessionChatScreen(
          sessionId: sessionID,
          agentId: agentID,
          title: handle.isNotEmpty ? handle : 'Session',
        ),
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

  static String _kindLabel(String kind) {
    if (kind.isEmpty) return 'attention';
    return kind.replaceAll('_', ' ');
  }

  static Color _severityColor(String severity) {
    switch (severity) {
      case 'critical':
        return DesignColors.error;
      case 'major':
        return Colors.orange;
      default:
        return DesignColors.primary;
    }
  }

  static String _formatTs(String raw) {
    final parsed = DateTime.tryParse(raw);
    if (parsed == null) return raw;
    final local = parsed.toLocal();
    return '${local.year.toString().padLeft(4, '0')}-'
        '${local.month.toString().padLeft(2, '0')}-'
        '${local.day.toString().padLeft(2, '0')} '
        '${local.hour.toString().padLeft(2, '0')}:'
        '${local.minute.toString().padLeft(2, '0')}';
  }
}

/// Routes the attention to the same widgets the Me-page card uses, so
/// approve/deny/pick/send behave identically across surfaces. The
/// onResolved callback closes the screen after a successful action so
/// the user lands back on the Me page where the row drops out of the
/// open list.
class _InlineActions extends StatelessWidget {
  final String id;
  final String kind;
  final Map<String, dynamic>? pendingPayload;
  final VoidCallback onResolved;
  const _InlineActions({
    required this.id,
    required this.kind,
    required this.pendingPayload,
    required this.onResolved,
  });

  @override
  Widget build(BuildContext context) {
    if (id.isEmpty) return const SizedBox.shrink();
    if (kind == 'help_request') {
      return InlineHelpRequestActions(
        id: id,
        pendingPayload: pendingPayload,
        onResolved: onResolved,
      );
    }
    return InlineApprovalActions(
      id: id,
      kind: kind,
      pendingPayload: pendingPayload,
      onResolved: onResolved,
    );
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

class _Chip extends StatelessWidget {
  final String label;
  final Color color;
  const _Chip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

/// Compact rendering of one transcript event. Goal is "fast scan, see
/// what the agent was doing right before the ask" — not the full chat
/// fidelity (no syntax highlighting, no tool icons, no streaming
/// merges). Body extraction handles the kinds the principal usually
/// needs to see: text turns (assistant + user), tool_use names, and
/// tool_result outcomes. Anything else falls through to a kind label
/// so the timeline remains complete.
class _TranscriptTile extends StatelessWidget {
  final Map<String, dynamic> event;
  const _TranscriptTile({required this.event});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final kind = (event['kind'] ?? '').toString();
    final producer = (event['producer'] ?? '').toString();
    final payload = _decodePayload(event['payload']);
    final body = _extractBody(kind, payload);
    final ts = (event['ts'] ?? '').toString();
    final tsLabel = _shortTs(ts);

    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.surfaceDark.withValues(alpha: 0.5)
            : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                _label(kind, producer),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  color: muted,
                ),
              ),
              const Spacer(),
              if (tsLabel.isNotEmpty)
                Text(
                  tsLabel,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
            ],
          ),
          if (body.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              body,
              maxLines: 6,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurface,
              ),
            ),
          ],
        ],
      ),
    );
  }

  static String _label(String kind, String producer) {
    final base = kind.isEmpty ? 'event' : kind;
    if (producer.isEmpty || producer == 'agent') return base;
    return '$producer · $base';
  }

  static String _extractBody(String kind, Map<String, dynamic>? payload) {
    if (payload == null) return '';
    switch (kind) {
      case 'text':
        return (payload['body'] ?? payload['text'] ?? '').toString();
      case 'tool_use':
        final name = (payload['name'] ?? '').toString();
        final input = payload['input'];
        if (input is Map) {
          // Pull a single high-signal field if present (file, command,
          // pattern, query). Otherwise let the name speak.
          for (final k in const [
            'file_path',
            'command',
            'pattern',
            'query',
            'prompt',
          ]) {
            final v = input[k];
            if (v is String && v.isNotEmpty) {
              return '$name: $v';
            }
          }
        }
        return name;
      case 'tool_result':
        final ok = payload['is_error'] != true;
        final body =
            (payload['content'] ?? payload['body'] ?? '').toString();
        if (body.isEmpty) return ok ? '(ok)' : '(error)';
        return body;
      case 'usage':
      case 'rate_limit':
      case 'session.init':
        return '';
      default:
        final body = payload['body'];
        if (body is String && body.isNotEmpty) return body;
        return '';
    }
  }

  static String _shortTs(String raw) {
    final t = DateTime.tryParse(raw);
    if (t == null) return '';
    final l = t.toLocal();
    return '${l.hour.toString().padLeft(2, '0')}:'
        '${l.minute.toString().padLeft(2, '0')}:'
        '${l.second.toString().padLeft(2, '0')}';
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
