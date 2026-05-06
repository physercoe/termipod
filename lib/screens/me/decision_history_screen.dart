import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'approval_detail_screen.dart';

/// Recent resolved attentions — the audit-trail companion to the Me
/// page's open list. Tapping a row opens [ApprovalDetailScreen], which
/// renders the per-decision history (who approved/denied, when, with
/// what reason or reply body) once the attention has been decided.
///
/// Data source: `GET /v1/teams/{team}/attention?status=resolved` (capped
/// at 200 newest by the hub). Single-fetch — no cache integration yet,
/// since this is an explicit drill-in rather than a hot path.
class DecisionHistoryScreen extends ConsumerStatefulWidget {
  const DecisionHistoryScreen({super.key});

  @override
  ConsumerState<DecisionHistoryScreen> createState() =>
      _DecisionHistoryScreenState();
}

class _DecisionHistoryScreenState
    extends ConsumerState<DecisionHistoryScreen> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _items = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final items = await client.listAttention(status: 'resolved');
      if (!mounted) return;
      setState(() {
        _items = items;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Decision history',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: _load,
        child: _loading && _items.isEmpty
            ? const Center(child: CircularProgressIndicator())
            : _error != null
                ? Center(
                    child: Text(
                      'Failed: $_error',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 12,
                        color: DesignColors.error,
                      ),
                    ),
                  )
                : _items.isEmpty
                    ? Center(
                        child: Text(
                          'No decisions yet.',
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 12,
                            color: muted,
                          ),
                        ),
                      )
                    : ListView.separated(
                        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
                        itemCount: _items.length,
                        separatorBuilder: (_, __) =>
                            const SizedBox(height: 10),
                        itemBuilder: (_, i) =>
                            _HistoryRow(attention: _items[i]),
                      ),
      ),
    );
  }
}

class _HistoryRow extends StatelessWidget {
  final Map<String, dynamic> attention;
  const _HistoryRow({required this.attention});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final kind = (attention['kind'] ?? '').toString();
    final summary = (attention['summary'] ?? '').toString();
    final actor = (attention['actor_handle'] ?? '').toString();
    final resolvedAt = (attention['resolved_at'] ?? '').toString();
    final lastDecision = _lastDecision(attention['decisions']);
    final verdict = (lastDecision?['decision'] ?? '').toString();
    final approve = verdict == 'approve';
    final accent = verdict.isEmpty
        ? DesignColors.primary
        : approve
            ? DesignColors.success
            : DesignColors.error;
    final headline = _headline(kind, verdict, lastDecision);

    return Material(
      color: isDark
          ? DesignColors.surfaceDark
          : DesignColors.surfaceLight,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: () => Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ApprovalDetailScreen(attention: attention),
          ),
        ),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(8),
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
                  Icon(
                    approve
                        ? Icons.check_circle_outline
                        : verdict.isEmpty
                            ? Icons.history
                            : Icons.cancel_outlined,
                    size: 14,
                    color: accent,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    headline,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                      color: accent,
                    ),
                  ),
                  const Spacer(),
                  if (resolvedAt.isNotEmpty)
                    Text(
                      _shortTs(resolvedAt),
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: muted,
                      ),
                    ),
                ],
              ),
              if (summary.isNotEmpty) ...[
                const SizedBox(height: 6),
                Text(
                  summary,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              ],
              if (actor.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  'by $actor · ${kind.replaceAll('_', ' ')}',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  static Map<String, dynamic>? _lastDecision(dynamic raw) {
    final list = _decodeList(raw);
    if (list.isEmpty) return null;
    return list.last;
  }

  static List<Map<String, dynamic>> _decodeList(dynamic raw) {
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

  static String _headline(
    String kind,
    String verdict,
    Map<String, dynamic>? last,
  ) {
    if (verdict.isEmpty) return 'Resolved';
    final approve = verdict == 'approve';
    switch (kind) {
      case 'select':
        if (approve) {
          final opt = (last?['option_id'] ?? '').toString();
          return opt.isNotEmpty ? 'Selected: $opt' : 'Selected';
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

  static String _shortTs(String raw) {
    final t = DateTime.tryParse(raw);
    if (t == null) return raw;
    final l = t.toLocal();
    final now = DateTime.now();
    final sameDay =
        l.year == now.year && l.month == now.month && l.day == now.day;
    if (sameDay) {
      return '${l.hour.toString().padLeft(2, '0')}:'
          '${l.minute.toString().padLeft(2, '0')}';
    }
    return '${l.month.toString().padLeft(2, '0')}-'
        '${l.day.toString().padLeft(2, '0')} '
        '${l.hour.toString().padLeft(2, '0')}:'
        '${l.minute.toString().padLeft(2, '0')}';
  }
}
