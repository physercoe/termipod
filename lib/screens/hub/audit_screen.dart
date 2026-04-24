import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/open_team_channel.dart';
import '../../theme/design_colors.dart';
import '../../widgets/activity_digest_card.dart';
import '../../widgets/team_switcher.dart';

/// Activity tab body per `docs/ia-redesign.md` §6.3 — the team's mutation
/// feed backed by `audit_events`. Chronological, filterable; a digest card
/// at the top summarises the last 24h and is mirrored on the Me tab.
/// Rows come from `GET /v1/teams/{team}/audit` newest-first; the server
/// caps the response at 500 rows.
class AuditScreen extends ConsumerStatefulWidget {
  const AuditScreen({super.key});

  @override
  ConsumerState<AuditScreen> createState() => _AuditScreenState();
}

class _AuditScreenState extends ConsumerState<AuditScreen> {
  // Filter prefix derived from loaded rows; null == "All".
  // The chip set is data-driven (see `_prefixCounts`) so it reflects what's
  // actually in the feed rather than a hardcoded enumeration that drifts as
  // new action kinds land on the backend.
  String? _prefix;
  List<Map<String, dynamic>> _allRows = const [];
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
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
      final rows = await client.listAuditEvents(limit: 500);
      if (mounted) {
        setState(() {
          _allRows = rows;
          _loading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _loading = false;
          _error = '$e';
        });
      }
    }
  }

  /// Group actions by their `<verb>` prefix (`agent.spawn` → `agent`) and
  /// count. Sorted by count desc; ties broken alphabetically.
  List<MapEntry<String, int>> get _prefixCounts {
    final counts = <String, int>{};
    for (final r in _allRows) {
      final action = (r['action'] ?? '').toString();
      if (action.isEmpty) continue;
      final dot = action.indexOf('.');
      final prefix = dot > 0 ? action.substring(0, dot) : action;
      counts[prefix] = (counts[prefix] ?? 0) + 1;
    }
    final entries = counts.entries.toList()
      ..sort((a, b) {
        final byCount = b.value.compareTo(a.value);
        return byCount != 0 ? byCount : a.key.compareTo(b.key);
      });
    return entries;
  }

  List<Map<String, dynamic>> get _filteredRows {
    if (_prefix == null) return _allRows;
    return _allRows
        .where((r) => (r['action'] ?? '').toString().startsWith('$_prefix.') ||
            (r['action'] ?? '').toString() == _prefix)
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.tabActivity,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          const TeamSwitcher(),
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: Column(
        children: [
          const _TeamChannelIngress(),
          ActivityDigestCard(events: _filteredRows),
          _FilterChips(
            prefixes: _prefixCounts,
            totalCount: _allRows.length,
            selected: _prefix,
            onSelected: (p) => setState(() => _prefix = p),
          ),
          const Divider(height: 1),
          Expanded(
            child: RefreshIndicator(
              onRefresh: _load,
              child: _buildBody(),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildBody() {
    final rows = _filteredRows;
    if (_loading && _allRows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text(
            _error!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        ],
      );
    }
    if (rows.isEmpty) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Center(
            child: Text(
              _prefix == null
                  ? 'No audit events yet.'
                  : 'No $_prefix.* events.',
            ),
          ),
        ],
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 4),
      itemCount: rows.length,
      separatorBuilder: (_, __) => const Divider(height: 1),
      itemBuilder: (_, i) => _AuditRow(data: rows[i]),
    );
  }
}

/// Pinned ingress from Activity into the team-wide `#hub-meta` channel.
/// Users often land on Activity looking for "what's going on" and the
/// chat-adjacent context belongs one tap away, not behind the steward
/// icon on another screen.
class _TeamChannelIngress extends ConsumerWidget {
  const _TeamChannelIngress();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: () => openHubMetaChannel(context, ref),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        decoration: BoxDecoration(
          color: DesignColors.primary.withValues(alpha: isDark ? 0.10 : 0.06),
          border: Border(
            bottom: BorderSide(
              color: DesignColors.primary.withValues(alpha: 0.30),
            ),
          ),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag, size: 16, color: DesignColors.primary),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                'Team channel #hub-meta',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const Icon(Icons.chevron_right,
                size: 18, color: DesignColors.primary),
          ],
        ),
      ),
    );
  }
}

class _FilterChips extends StatelessWidget {
  final List<MapEntry<String, int>> prefixes;
  final int totalCount;
  final String? selected;
  final ValueChanged<String?> onSelected;

  const _FilterChips({
    required this.prefixes,
    required this.totalCount,
    required this.selected,
    required this.onSelected,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 44,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        itemCount: prefixes.length + 1,
        separatorBuilder: (_, __) => const SizedBox(width: 6),
        itemBuilder: (_, i) {
          if (i == 0) {
            return ChoiceChip(
              label: Text('All ($totalCount)'),
              selected: selected == null,
              onSelected: (_) => onSelected(null),
            );
          }
          final entry = prefixes[i - 1];
          final isSel = selected == entry.key;
          return ChoiceChip(
            label: Text('${entry.key} (${entry.value})'),
            selected: isSel,
            onSelected: (_) => onSelected(isSel ? null : entry.key),
          );
        },
      ),
    );
  }
}

class _AuditRow extends StatelessWidget {
  final Map<String, dynamic> data;
  const _AuditRow({required this.data});

  @override
  Widget build(BuildContext context) {
    final action = (data['action'] ?? '').toString();
    final summary = (data['summary'] ?? '').toString();
    final ts = (data['ts'] ?? '').toString();
    final actorHandle = (data['actor_handle'] ?? '').toString();
    final actorKind = (data['actor_kind'] ?? '').toString();
    final icon = _iconForAction(action);
    final color = _colorForAction(context, action);
    final actorLabel = actorHandle.isNotEmpty
        ? '@$actorHandle'
        : (actorKind.isNotEmpty ? actorKind : 'system');

    return ListTile(
      leading: Icon(icon, color: color),
      title: Text(
        summary,
        style: const TextStyle(fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '$actorLabel  ·  $action',
        style: TextStyle(
          color: Theme.of(context).colorScheme.onSurfaceVariant,
          fontSize: 12,
        ),
      ),
      trailing: Text(
        _shortTime(ts),
        style: TextStyle(
          fontFamily: 'HackGenConsole',
          fontSize: 11,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
      onTap: () => _showDetail(context),
    );
  }

  void _showDetail(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (_) => DraggableScrollableSheet(
        expand: false,
        initialChildSize: 0.55,
        minChildSize: 0.3,
        maxChildSize: 0.9,
        builder: (_, controller) => SingleChildScrollView(
          controller: controller,
          padding: const EdgeInsets.all(16),
          child: _DetailView(data: data),
        ),
      ),
    );
  }

  static IconData _iconForAction(String action) {
    if (action.startsWith('agent.spawn')) return Icons.rocket_launch_outlined;
    if (action.startsWith('agent.terminate')) return Icons.power_settings_new;
    if (action.startsWith('attention.')) return Icons.flag_outlined;
    if (action.startsWith('schedule.')) return Icons.schedule;
    if (action.startsWith('host.')) return Icons.dns_outlined;
    return Icons.history;
  }

  static Color _colorForAction(BuildContext context, String action) {
    final scheme = Theme.of(context).colorScheme;
    if (action == 'agent.terminate' ||
        action == 'schedule.delete' ||
        action == 'host.delete') {
      return scheme.error;
    }
    if (action == 'agent.spawn' || action == 'schedule.create') {
      return DesignColors.primary;
    }
    return scheme.onSurfaceVariant;
  }

  static String _shortTime(String ts) {
    // Accepts "2026-04-21T10:33:33.123Z"; show "MM-DD HH:MM".
    if (ts.length < 16) return ts;
    return '${ts.substring(5, 10)} ${ts.substring(11, 16)}';
  }
}

class _DetailView extends StatelessWidget {
  final Map<String, dynamic> data;
  const _DetailView({required this.data});

  @override
  Widget build(BuildContext context) {
    final meta = data['meta'];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          (data['summary'] ?? '').toString(),
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 12),
        _kv(context, 'Action', (data['action'] ?? '').toString()),
        _kv(context, 'Actor', [
          (data['actor_handle'] ?? '').toString(),
          (data['actor_kind'] ?? '').toString(),
        ].where((s) => s.isNotEmpty).join('  ·  ')),
        _kv(context, 'Target', [
          (data['target_kind'] ?? '').toString(),
          (data['target_id'] ?? '').toString(),
        ].where((s) => s.isNotEmpty).join(' ')),
        _kv(context, 'Time', (data['ts'] ?? '').toString()),
        if (meta is Map && meta.isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(
            'Metadata',
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 4),
          for (final e in meta.entries)
            _kv(context, e.key.toString(), '${e.value}'),
        ],
      ],
    );
  }

  Widget _kv(BuildContext context, String k, String v) {
    if (v.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              k,
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              v,
              style: const TextStyle(fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}
