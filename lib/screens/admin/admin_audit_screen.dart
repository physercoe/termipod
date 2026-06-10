import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';

/// Filterable cross-team view of `audit_events` (ADR-028 Phase 5 /
/// plan W25). Backed by the owner-scope `GET /v1/admin/audit` query.
///
/// Reached from the Admin pane (its AppBar and its audit strip). The
/// filters map one-to-one onto the endpoint's query params: an action
/// prefix, a target kind, a time window, and an actor handle.
class AdminAuditScreen extends ConsumerStatefulWidget {
  const AdminAuditScreen({super.key});

  @override
  ConsumerState<AdminAuditScreen> createState() => _AdminAuditScreenState();
}

class _AdminAuditScreenState extends ConsumerState<AdminAuditScreen> {
  // Filter state.
  String _actionPrefix = ''; // '' = all
  String _targetKind = ''; // '' = all
  Duration? _window = const Duration(hours: 24); // null = all time
  final TextEditingController _actorCtrl = TextEditingController();

  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _events = const [];

  static const _actionPrefixes = {
    '': 'all',
    'host.': 'host',
    'db.': 'db',
    'token.': 'token',
    'agent.': 'agent',
    'session.': 'session',
  };
  static const _targetKinds = ['', 'host', 'agent', 'session', 'hub', 'token'];
  static const _windows = <String, Duration?>{
    '1h': Duration(hours: 1),
    '24h': Duration(hours: 24),
    '7d': Duration(days: 7),
    'all': null,
  };

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _actorCtrl.dispose();
    super.dispose();
  }

  /// Floors the window start to whole seconds — the hub stores RFC3339
  /// timestamps without sub-second precision, so a millisecond suffix
  /// would only introduce boundary skew.
  String? _sinceParam() {
    if (_window == null) return null;
    final dt = DateTime.now().toUtc().subtract(_window!);
    final s = dt.toIso8601String();
    final dot = s.indexOf('.');
    return dot < 0 ? s : '${s.substring(0, dot)}Z';
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final events = await client.adminListAudit(
        actionPrefix: _actionPrefix,
        targetKind: _targetKind,
        actor: _actorCtrl.text.trim(),
        since: _sinceParam(),
        limit: 200,
      );
      if (!mounted) return;
      setState(() {
        _events = events;
        _loading = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.status == 403
            ? 'The audit log requires an owner-kind token.'
            : '${e.status}: ${e.message}';
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
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Audit log',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: Column(
        children: [
          _filterBar(),
          const Divider(height: 1),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Text(
                            _error!,
                            textAlign: TextAlign.center,
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: 12,
                              color: DesignColors.error,
                            ),
                          ),
                        ),
                      )
                    : _events.isEmpty
                        ? Center(
                            child: Text(
                              'No matching audit events.',
                              style: GoogleFonts.jetBrainsMono(fontSize: 12),
                            ),
                          )
                        : ListView.separated(
                            padding: const EdgeInsets.all(12),
                            itemCount: _events.length,
                            separatorBuilder: (_, __) =>
                                const SizedBox(height: 6),
                            itemBuilder: (_, i) => _eventCard(_events[i]),
                          ),
          ),
        ],
      ),
    );
  }

  Widget _filterBar() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _chipRow(
            'action',
            _actionPrefixes.entries
                .map((e) => _filterChip(e.value, _actionPrefix == e.key, () {
                      setState(() => _actionPrefix = e.key);
                      _load();
                    }))
                .toList(),
          ),
          const SizedBox(height: 4),
          _chipRow(
            'target',
            _targetKinds
                .map((k) => _filterChip(k.isEmpty ? 'all' : k,
                        _targetKind == k, () {
                      setState(() => _targetKind = k);
                      _load();
                    }))
                .toList(),
          ),
          const SizedBox(height: 4),
          _chipRow(
            'window',
            _windows.entries
                .map((e) => _filterChip(e.key, _window == e.value, () {
                      setState(() => _window = e.value);
                      _load();
                    }))
                .toList(),
          ),
          const SizedBox(height: 6),
          SizedBox(
            height: 36,
            child: TextField(
              controller: _actorCtrl,
              style: GoogleFonts.jetBrainsMono(fontSize: 12),
              decoration: InputDecoration(
                isDense: true,
                hintText: 'actor handle (exact) — blank for any',
                hintStyle: GoogleFonts.jetBrainsMono(fontSize: 11),
                prefixIcon: const Icon(Icons.person_search, size: 16),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
              onSubmitted: (_) => _load(),
            ),
          ),
        ],
      ),
    );
  }

  Widget _chipRow(String label, List<Widget> chips) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Row(
      children: [
        SizedBox(
          width: 52,
          child: Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
        ),
        Expanded(
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(children: chips),
          ),
        ),
      ],
    );
  }

  Widget _filterChip(String label, bool selected, VoidCallback onTap) {
    return Padding(
      padding: const EdgeInsets.only(right: Spacing.s8),
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s4),
          decoration: BoxDecoration(
            color: selected
                ? DesignColors.primary.withValues(alpha: 0.18)
                : Colors.transparent,
            borderRadius: Radii.lgBorder,
            border: Border.all(
              color: selected
                  ? DesignColors.primary
                  : DesignColors.textMuted.withValues(alpha: 0.4),
            ),
          ),
          child: Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: selected ? DesignColors.primary : null,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w400,
            ),
          ),
        ),
      ),
    );
  }

  Widget _eventCard(Map<String, dynamic> e) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final action = (e['action'] as String?) ?? '';
    final summary = (e['summary'] as String?) ?? '';
    final ts = (e['ts'] as String?) ?? '';
    final actor = (e['actor_handle'] as String?) ?? '';
    final actorKind = (e['actor_kind'] as String?) ?? '';
    final team = (e['team_id'] as String?) ?? '';

    return Container(
      padding: const EdgeInsets.all(Spacing.s8),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  action,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                    color: DesignColors.primary,
                  ),
                ),
              ),
              Text(
                ts,
                style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: muted),
              ),
            ],
          ),
          const SizedBox(height: 3),
          Text(summary, style: GoogleFonts.jetBrainsMono(fontSize: 11)),
          const SizedBox(height: 3),
          Text(
            '${actor.isEmpty ? actorKind : actor} · $team',
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: muted),
          ),
        ],
      ),
    );
  }
}
