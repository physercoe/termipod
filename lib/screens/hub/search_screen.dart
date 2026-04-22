import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'search_event_sheet.dart';

/// Full-screen full-text search over hub events. Debounces keystrokes by
/// 350ms so we aren't hammering the FTS endpoint on every character.
class SearchScreen extends ConsumerStatefulWidget {
  const SearchScreen({super.key});

  @override
  ConsumerState<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends ConsumerState<SearchScreen> {
  final TextEditingController _q = TextEditingController();
  Timer? _debounce;
  List<Map<String, dynamic>> _results = const [];
  bool _loading = false;
  String? _error;
  int _reqSeq = 0;

  static const int _minQueryLen = 2;
  static const Duration _debounceDur = Duration(milliseconds: 350);

  // channel_id → channel name, populated once from listTeamChannels and
  // handed to the result sheet so it can offer "Open channel" navigation
  // for team-scoped message events.
  Map<String, String> _teamChannels = const {};

  @override
  void initState() {
    super.initState();
    _q.addListener(_onChanged);
    _loadTeamChannels();
  }

  Future<void> _loadTeamChannels() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final rows = await client.listTeamChannels();
      if (!mounted) return;
      final map = <String, String>{};
      for (final r in rows) {
        final id = (r['id'] ?? '').toString();
        final name = (r['name'] ?? '').toString();
        if (id.isNotEmpty) map[id] = name;
      }
      setState(() => _teamChannels = map);
    } catch (_) {
      // Best-effort — lookup simply falls back to raw channel_id.
    }
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _q.removeListener(_onChanged);
    _q.dispose();
    super.dispose();
  }

  void _onChanged() {
    _debounce?.cancel();
    _debounce = Timer(_debounceDur, _run);
  }

  Future<void> _run() async {
    final query = _q.text.trim();
    if (query.length < _minQueryLen) {
      if (!mounted) return;
      setState(() {
        _results = const [];
        _loading = false;
        _error = null;
      });
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Hub not configured';
        _results = const [];
      });
      return;
    }
    final seq = ++_reqSeq;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.searchEvents(query, limit: 50);
      if (!mounted || seq != _reqSeq) return;
      setState(() {
        _results = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted || seq != _reqSeq) return;
      setState(() {
        _loading = false;
        _error = '$e';
        _results = const [];
      });
    }
  }

  void _clear() {
    _debounce?.cancel();
    _q.clear();
    setState(() {
      _results = const [];
      _loading = false;
      _error = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: TextField(
          controller: _q,
          autofocus: true,
          decoration: const InputDecoration.collapsed(
            hintText: 'Search events…',
          ),
          style: GoogleFonts.spaceGrotesk(fontSize: 16),
          onSubmitted: (_) => _run(),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.clear),
            tooltip: 'Clear',
            onPressed: _clear,
          ),
        ],
      ),
      body: _body(),
    );
  }

  Widget _body() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final query = _q.text.trim();

    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error!,
            style: GoogleFonts.jetBrainsMono(
              color: DesignColors.error,
              fontSize: 12,
            ),
          ),
        ),
      );
    }
    if (query.length < _minQueryLen) {
      return Center(
        child: Text(
          'type to search',
          style: GoogleFonts.jetBrainsMono(fontSize: 12, color: muted),
        ),
      );
    }
    if (_results.isEmpty) {
      return Center(
        child: Text(
          'No matches',
          style: GoogleFonts.jetBrainsMono(fontSize: 12, color: muted),
        ),
      );
    }
    return ListView.separated(
      itemCount: _results.length,
      separatorBuilder: (_, __) => const Divider(height: 1),
      itemBuilder: (_, i) => _ResultRow(
        event: _results[i],
        teamChannels: _teamChannels,
      ),
    );
  }
}

class _ResultRow extends StatelessWidget {
  final Map<String, dynamic> event;
  final Map<String, String> teamChannels;
  const _ResultRow({required this.event, required this.teamChannels});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final type = (event['type'] ?? '').toString();
    final fromId = (event['from_id'] ?? '').toString();
    final ts = (event['received_ts'] ?? '').toString();
    final title = _titleFromParts(event['parts'], fallback: type);

    return ListTile(
      leading: Icon(_iconForType(type), size: 20),
      title: Text(
        title,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: GoogleFonts.spaceGrotesk(fontSize: 14),
      ),
      subtitle: Text(
        '$fromId · ${_shortTs(ts)}',
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
      ),
      onTap: () => showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        backgroundColor: Colors.transparent,
        builder: (_) => SearchEventSheet(
          event: event,
          teamChannels: teamChannels,
        ),
      ),
    );
  }

  String _titleFromParts(dynamic rawParts, {required String fallback}) {
    if (rawParts is List) {
      for (final raw in rawParts) {
        if (raw is! Map) continue;
        if (raw['kind'] == 'text' && raw['text'] is String) {
          final t = (raw['text'] as String).trim();
          if (t.isNotEmpty) return t;
        }
      }
    }
    return fallback.isEmpty ? '(event)' : fallback;
  }

  IconData _iconForType(String type) {
    if (type == 'message') return Icons.forum;
    if (type.startsWith('attention')) return Icons.priority_high;
    if (type.startsWith('agent')) return Icons.terminal;
    return Icons.article;
  }

  String _shortTs(String iso) {
    if (iso.isEmpty) return '';
    final dt = DateTime.tryParse(iso);
    if (dt == null) return iso;
    final local = dt.toLocal();
    final now = DateTime.now();
    final sameDay =
        local.year == now.year && local.month == now.month && local.day == now.day;
    if (sameDay) {
      final hh = local.hour.toString().padLeft(2, '0');
      final mm = local.minute.toString().padLeft(2, '0');
      return '$hh:$mm';
    }
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    final mon = months[local.month - 1];
    return '$mon ${local.day}';
  }
}
