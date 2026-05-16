import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/open_steward_session.dart';
import '../../theme/design_colors.dart';
import '../projects/documents_screen.dart' show DocumentDetailScreen;
import '../projects/project_detail_screen.dart';
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
    final status = (widget.attention['status'] ?? '').toString();
    final resolvedAt = (widget.attention['resolved_at'] ?? '').toString();
    final decisions = _decodeDecisions(widget.attention['decisions']);

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
              if (kind == 'elicit')
                _Chip(label: 'fill', color: DesignColors.terminalCyan),
            ],
          ),
          const SizedBox(height: 20),

          // ADR-020 W2 — director sent the deliverable back with notes.
          // Render the note + linked annotations so the steward can
          // address each one. Inline actions still apply (resolving the
          // attention item itself), but the dominant UI here is reading
          // the director's feedback.
          if (kind == 'revision_requested' && pending != null)
            _RevisionRequestedBlock(
              projectId: _projectScope()?.id ?? '',
              payload: pending,
            ),
          if (kind == 'revision_requested') const SizedBox(height: 16),

          // The agent has proposed a template body. Without a preview
          // the principal would be approving raw YAML blind — surface
          // the proposed body, the agent's rationale, and a diff hint
          // against the currently-installed template so the choice is
          // informed. Inline Approve/Reject below still resolves the
          // attention; this block is read-only context.
          if (kind == 'template_proposal' && pending != null)
            _TemplateProposalPreview(payload: pending),
          if (kind == 'template_proposal') const SizedBox(height: 16),

          // --- Inline actions: same widgets as the Me-page card. ---
          // Hidden once the attention is resolved — the decision history
          // section below carries the audit trail instead.
          if (status != 'resolved')
            _InlineActions(
              id: attentionId,
              kind: kind,
              pendingPayload: pending,
              onResolved: () {
                if (mounted) Navigator.of(context).pop();
              },
            ),
          if (status != 'resolved') const SizedBox(height: 24),

          // --- Where: agent + session + project pointers. ---
          _Section(title: 'Origin', children: [
            if (ctxAgentHandle.isNotEmpty)
              _Field(label: 'Agent', value: ctxAgentHandle),
            if (createdAt.isNotEmpty)
              _Field(label: 'Raised', value: _formatTs(createdAt)),
            if (ctxSessionID.isNotEmpty || _projectScope() != null)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 6,
                  children: [
                    if (ctxSessionID.isNotEmpty)
                      OutlinedButton.icon(
                        icon: const Icon(Icons.open_in_new, size: 16),
                        label: const Text('Open in chat'),
                        onPressed: () => _openSession(
                          context,
                          sessionID: ctxSessionID,
                          agentID: ctxAgentID,
                          handle: ctxAgentHandle,
                          targetSeq: _firstEventSeq(ctxEvents),
                        ),
                      ),
                    if (_projectScope() != null)
                      OutlinedButton.icon(
                        icon: const Icon(Icons.folder_open, size: 16),
                        label: const Text('Open project'),
                        onPressed: () => _openProject(context),
                      ),
                  ],
                ),
              ),
          ]),

          // --- Decision history: who decided what, when. ---
          // Populated once any /decide call lands on this attention.
          // For single-approver tiers (MVP default) the list will hold
          // exactly one entry on resolved items and be empty on open
          // ones; for multi-approver quorums each partial vote shows up
          // alongside the final resolution.
          if (decisions.isNotEmpty)
            _Section(title: 'Decision history', children: [
              for (final d in decisions)
                _DecisionTile(decision: d, kind: kind),
              if (status == 'resolved' && resolvedAt.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    'Resolved ${_formatTs(resolvedAt)}',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: muted,
                      fontStyle: FontStyle.italic,
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
    int? targetSeq,
  }) {
    // SessionChatScreen pulls the session's real title from its own
    // backfill (it queries the session row by id); we just need a
    // sensible placeholder until that lands. Fall back to the agent
    // handle which the caller already has — it's what the principal
    // would recognise anyway ("research-steward", not a session UUID).
    //
    // targetSeq lands the chat at (and briefly highlights) the event
    // closest to where this attention was raised, so the user sees
    // the agent's reasoning in place rather than the generic "tail".
    Navigator.of(context).pushReplacement(
      MaterialPageRoute(
        builder: (_) => SessionChatScreen(
          sessionId: sessionID,
          agentId: agentID,
          title: handle.isNotEmpty ? handle : 'Session',
          initialSeq: targetSeq,
        ),
      ),
    );
  }

  /// Returns (project_id, project_row) for project-scoped attentions —
  /// either the explicit `project_id` column or the scope_id when
  /// scope_kind='project'. Looks the row up from the cached hub state
  /// so we have a Project map to hand ProjectDetailScreen. Null when
  /// there's no project pointer or the project isn't in cache.
  ({String id, Map<String, dynamic> project})? _projectScope() {
    final att = widget.attention;
    String pid = (att['project_id'] ?? '').toString();
    if (pid.isEmpty &&
        (att['scope_kind'] ?? '').toString() == 'project') {
      pid = (att['scope_id'] ?? '').toString();
    }
    if (pid.isEmpty) return null;
    final hub = ref.read(hubProvider).value;
    if (hub == null) return null;
    for (final p in hub.projects) {
      if ((p['id'] ?? '').toString() == pid) {
        return (id: pid, project: p);
      }
    }
    return null;
  }

  void _openProject(BuildContext context) {
    final scope = _projectScope();
    if (scope == null) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ProjectDetailScreen(project: scope.project),
      ),
    );
  }

  /// First (newest) event's seq from the context payload. Used as the
  /// jump target when "Open in chat" is tapped — the agent's most
  /// recent turn before the request is the right anchor for what the
  /// principal is reviewing.
  static int? _firstEventSeq(List ctxEvents) {
    if (ctxEvents.isEmpty) return null;
    final first = ctxEvents.first;
    if (first is Map) {
      final raw = first['seq'];
      if (raw is num) return raw.toInt();
    }
    return null;
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

  static List<Map<String, dynamic>> _decodeDecisions(dynamic raw) {
    if (raw is List) {
      return [
        for (final d in raw)
          if (d is Map) d.cast<String, dynamic>(),
      ];
    }
    if (raw is String && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is List) {
          return [
            for (final d in decoded)
              if (d is Map) d.cast<String, dynamic>(),
          ];
        }
      } catch (_) {}
    }
    return const [];
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
    if (kind == 'help_request' || kind == 'elicit') {
      return InlineHelpRequestActions(
        id: id,
        kind: kind,
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

/// One row of the decision-history audit trail. Per-kind formatting
/// mirrors the user-turn text the agent sees on its own transcript
/// (driver_stdio.formatAttentionReplyText) so the history reads the
/// same way on both sides — what the principal sees here is what the
/// agent saw.
class _DecisionTile extends StatelessWidget {
  final Map<String, dynamic> decision;
  final String kind;
  const _DecisionTile({required this.decision, required this.kind});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final at = (decision['at'] ?? '').toString();
    final by = (decision['by'] ?? '').toString();
    final verdict = (decision['decision'] ?? '').toString();
    final reason = (decision['reason'] ?? '').toString();
    final body = (decision['body'] ?? '').toString();
    final optionID = (decision['option_id'] ?? '').toString();

    final approve = verdict == 'approve';
    final accent = approve ? DesignColors.success : DesignColors.error;
    final headline = _headline(kind, verdict, optionID);

    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(6),
        border: Border(
          left: BorderSide(color: accent, width: 2),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                approve
                    ? Icons.check_circle_outline
                    : Icons.cancel_outlined,
                size: 14,
                color: accent,
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  headline,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: accent,
                  ),
                ),
              ),
              if (at.isNotEmpty)
                Text(
                  _ApprovalDetailScreenState._formatTs(at),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
            ],
          ),
          if (by.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 2, left: 20),
              child: Text(
                'by $by',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: muted,
                ),
              ),
            ),
          if (body.isNotEmpty) ...[
            const SizedBox(height: 4),
            Padding(
              padding: const EdgeInsets.only(left: 20),
              child: SelectableText(
                body,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurface,
                ),
              ),
            ),
          ],
          if (reason.isNotEmpty) ...[
            const SizedBox(height: 4),
            Padding(
              padding: const EdgeInsets.only(left: 20),
              child: SelectableText(
                'Reason: $reason',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: muted,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }

  static String _headline(String kind, String verdict, String optionID) {
    final approve = verdict == 'approve';
    switch (kind) {
      case 'select':
        if (approve) {
          return optionID.isNotEmpty
              ? 'Selected: $optionID'
              : 'Selected';
        }
        return 'No option chosen';
      case 'help_request':
      case 'elicit':
        return approve ? 'Replied' : 'Dismissed';
      case 'template_proposal':
        return approve ? 'Approved template' : 'Rejected template';
      case 'approval_request':
      default:
        return approve ? 'Approved' : 'Rejected';
    }
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

/// ADR-020 W2 — renders the structured payload of a `revision_requested`
/// attention item: the director's note plus a clickable list of the
/// annotations they referenced. Each row pushes the deliverable's
/// document so the steward can see the annotation in the section
/// overlay (deep-linking straight to a section is W7+).
class _RevisionRequestedBlock extends ConsumerStatefulWidget {
  final String projectId;
  final Map<String, dynamic> payload;
  const _RevisionRequestedBlock({
    required this.projectId,
    required this.payload,
  });

  @override
  ConsumerState<_RevisionRequestedBlock> createState() =>
      _RevisionRequestedBlockState();
}

class _RevisionRequestedBlockState
    extends ConsumerState<_RevisionRequestedBlock> {
  bool _loading = true;
  String? _deliverableLabel;
  String? _firstDocumentId;
  // annotation id -> {section_slug, kind, body, document_id}
  Map<String, Map<String, dynamic>> _annotationsByID = const {};

  String get _deliverableId =>
      (widget.payload['deliverable_id'] ?? '').toString();

  String get _note => (widget.payload['note'] ?? '').toString();

  List<String> get _annotationIDs {
    final raw = widget.payload['annotation_ids'];
    if (raw is List) return raw.map((e) => e.toString()).toList();
    return const [];
  }

  @override
  void initState() {
    super.initState();
    _resolve();
  }

  Future<void> _resolve() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || widget.projectId.isEmpty || _deliverableId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final cached = await client.getDeliverableCached(
        projectId: widget.projectId,
        deliverableId: _deliverableId,
      );
      final d = cached.body;
      _deliverableLabel = (d['kind'] ?? 'Deliverable').toString();
      final comps = ((d['components'] as List?) ?? const [])
          .map((e) => (e as Map).cast<String, dynamic>())
          .where((c) => (c['kind'] ?? '') == 'document')
          .toList();
      final wantIDs = _annotationIDs.toSet();
      final byID = <String, Map<String, dynamic>>{};
      for (final c in comps) {
        final docID = (c['ref_id'] ?? '').toString();
        if (docID.isEmpty) continue;
        _firstDocumentId ??= docID;
        try {
          final list = await client.listAnnotationsCached(
              documentId: docID, status: 'all');
          for (final a in list.body) {
            final id = (a['id'] ?? '').toString();
            if (wantIDs.contains(id)) {
              byID[id] = {...a, '_doc_id': docID};
            }
          }
        } catch (_) {}
      }
      if (!mounted) return;
      setState(() {
        _annotationsByID = byID;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: bg,
        border: Border.all(color: border),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.assignment_return_outlined,
                  size: 16, color: DesignColors.warning),
              const SizedBox(width: 6),
              Text(
                'Director note',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.3,
                  color: DesignColors.textMuted,
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            _note.isEmpty ? '(no note)' : _note,
            style: GoogleFonts.spaceGrotesk(fontSize: 14, height: 1.4),
          ),
          if (widget.projectId.isNotEmpty && _deliverableId.isNotEmpty) ...[
            const SizedBox(height: 10),
            OutlinedButton.icon(
              icon: const Icon(Icons.layers_outlined, size: 16),
              label: Text(_deliverableLabel == null
                  ? 'Open deliverable'
                  : 'Open deliverable · $_deliverableLabel'),
              onPressed: _firstDocumentId == null
                  ? null
                  : () => _openDocument(context, _firstDocumentId!),
            ),
          ],
          if (_annotationIDs.isNotEmpty) ...[
            const SizedBox(height: 12),
            Text(
              'Linked annotations (${_annotationIDs.length})',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.3,
                color: DesignColors.textMuted,
              ),
            ),
            const SizedBox(height: 4),
            if (_loading)
              const Padding(
                padding: EdgeInsets.all(8),
                child: SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              )
            else
              for (final id in _annotationIDs)
                _LinkedAnnotationRow(
                  id: id,
                  resolved: _annotationsByID[id],
                  onTap: () {
                    final docID =
                        (_annotationsByID[id]?['_doc_id'] ?? '').toString();
                    if (docID.isNotEmpty) _openDocument(context, docID);
                  },
                ),
          ],
        ],
      ),
    );
  }

  void _openDocument(BuildContext context, String docID) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DocumentDetailScreen(documentId: docID),
      ),
    );
  }
}

class _LinkedAnnotationRow extends StatelessWidget {
  final String id;
  final Map<String, dynamic>? resolved;
  final VoidCallback onTap;
  const _LinkedAnnotationRow({
    required this.id,
    required this.resolved,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final r = resolved;
    final missing = r == null;
    final kind = (r?['kind'] ?? 'comment').toString();
    final section = (r?['section_slug'] ?? '').toString();
    final body = (r?['body'] ?? '').toString();
    final status = (r?['status'] ?? 'open').toString();
    return InkWell(
      onTap: missing ? null : onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Icon(
                _glyphFor(kind),
                size: 14,
                color: missing
                    ? DesignColors.textMuted
                    : (status == 'resolved'
                        ? DesignColors.textMuted
                        : DesignColors.primary),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    missing
                        ? '(annotation $id no longer available)'
                        : body,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      height: 1.35,
                      color: missing
                          ? DesignColors.textMuted
                          : null,
                      fontStyle: missing ? FontStyle.italic : null,
                    ),
                  ),
                  if (!missing) ...[
                    const SizedBox(height: 2),
                    Text(
                      '$kind · $section${status == 'resolved' ? ' · resolved' : ''}',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 9,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            if (!missing)
              const Icon(Icons.chevron_right,
                  size: 16, color: DesignColors.textMuted),
          ],
        ),
      ),
    );
  }

  IconData _glyphFor(String kind) {
    switch (kind) {
      case 'redline':
        return Icons.format_strikethrough;
      case 'suggestion':
        return Icons.swap_horiz;
      case 'question':
        return Icons.help_outline;
      default:
        return Icons.chat_bubble_outline;
    }
  }
}

/// Read-only preview block for kind=`template_proposal`. The proposer
/// (an agent calling `templates.propose`) supplies a blob containing
/// the new template body; this widget fetches and renders it so the
/// principal can read what they're approving instead of trusting the
/// summary line. Also shows the rationale, proposed-by handle, and a
/// "same as current" / "differs from current" hint against the
/// installed template at `<category>/<name>` so the user can tell a
/// new-template create apart from an in-place revision.
class _TemplateProposalPreview extends ConsumerStatefulWidget {
  final Map<String, dynamic> payload;
  const _TemplateProposalPreview({required this.payload});

  @override
  ConsumerState<_TemplateProposalPreview> createState() =>
      _TemplateProposalPreviewState();
}

class _TemplateProposalPreviewState
    extends ConsumerState<_TemplateProposalPreview> {
  bool _loading = true;
  String? _error;
  String? _proposed;
  String? _current;
  bool _currentMissing = false;

  String get _category => (widget.payload['category'] ?? '').toString();
  String get _name => (widget.payload['name'] ?? '').toString();
  String get _sha => (widget.payload['blob_sha256'] ?? '').toString();
  String get _rationale => (widget.payload['rationale'] ?? '').toString();
  String get _proposedBy => (widget.payload['proposed_by'] ?? '').toString();

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || _sha.isEmpty) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured or proposal missing blob_sha256.';
      });
      return;
    }
    try {
      // Fetch the proposed body via the content-addressed blob.
      final bytes = await client.downloadBlob(_sha);
      _proposed = utf8.decode(bytes, allowMalformed: true);
      // Best-effort: fetch the currently-installed template for a same-
      // /differs hint. 404 means this is a create rather than a revise —
      // surfaced via _currentMissing so the user knows.
      if (_category.isNotEmpty && _name.isNotEmpty) {
        try {
          _current = await client.getTemplate(_category, _name);
        } catch (e) {
          if (e.toString().contains('404')) {
            _currentMissing = true;
          }
          // Other errors fall through — preview without diff hint.
        }
      }
      if (!mounted) return;
      setState(() => _loading = false);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Failed to load proposal: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: bg,
        border: Border.all(color: border),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.description_outlined,
                  size: 16, color: DesignColors.primary),
              const SizedBox(width: 6),
              Text(
                'Template proposal',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.3,
                ),
              ),
              const Spacer(),
              if (!_loading && _proposed != null)
                _DiffStatusChip(
                  isCreate: _currentMissing,
                  hasCurrent: _current != null,
                  isSame: _current != null && _current == _proposed,
                ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            '$_category/$_name',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          if (_proposedBy.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              'Proposed by $_proposedBy',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: muted,
              ),
            ),
          ],
          if (_rationale.isNotEmpty) ...[
            const SizedBox(height: 10),
            Text(
              'Rationale',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: muted,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              _rationale,
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
            ),
          ],
          const SizedBox(height: 12),
          Text(
            'Proposed body',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              color: muted,
            ),
          ),
          const SizedBox(height: 6),
          if (_loading)
            const Padding(
              padding: EdgeInsets.all(8),
              child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
            )
          else if (_error != null)
            Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.error,
              ),
            )
          else
            Container(
              constraints: const BoxConstraints(maxHeight: 320),
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: isDark
                    ? DesignColors.backgroundDark
                    : DesignColors.backgroundLight,
                border: Border.all(color: border),
                borderRadius: BorderRadius.circular(8),
              ),
              child: SingleChildScrollView(
                child: SelectableText(
                  _proposed ?? '',
                  style: GoogleFonts.jetBrainsMono(fontSize: 11),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// Status chip in the proposal header row: create / same / differs.
/// Drives the principal toward the right mental model at a glance —
/// "this is a brand-new template" reads differently than "this revises
/// an existing template" or "this is identical to what's already on
/// disk" (a no-op proposal worth rejecting).
class _DiffStatusChip extends StatelessWidget {
  final bool isCreate;
  final bool hasCurrent;
  final bool isSame;
  const _DiffStatusChip({
    required this.isCreate,
    required this.hasCurrent,
    required this.isSame,
  });

  @override
  Widget build(BuildContext context) {
    String label;
    Color color;
    if (isCreate) {
      label = 'NEW';
      color = DesignColors.terminalCyan;
    } else if (!hasCurrent) {
      label = 'unknown';
      color = DesignColors.textMuted;
    } else if (isSame) {
      label = 'no change';
      color = DesignColors.textMuted;
    } else {
      label = 'revise';
      color = Colors.orange;
    }
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
