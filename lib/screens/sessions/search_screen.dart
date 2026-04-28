import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'sessions_screen.dart';

/// Phase 1.5c (MVP parity gap): full-text search across session
/// transcripts. Debounced query input, list of hits with snippets,
/// tap → SessionChatScreen for the matching session. Powered by
/// the hub's `agent_events_fts` index + `/v1/teams/:t/sessions/
/// search` endpoint.
///
/// Reachable via the search icon in `SessionsScreen`'s AppBar.
class SessionSearchScreen extends ConsumerStatefulWidget {
  const SessionSearchScreen({super.key});

  @override
  ConsumerState<SessionSearchScreen> createState() =>
      _SessionSearchScreenState();
}

class _SessionSearchScreenState extends ConsumerState<SessionSearchScreen> {
  final TextEditingController _ctrl = TextEditingController();
  Timer? _debounce;
  String _lastQuery = '';
  bool _loading = false;
  String? _error;
  List<Map<String, dynamic>> _results = const [];

  @override
  void dispose() {
    _debounce?.cancel();
    _ctrl.dispose();
    super.dispose();
  }

  void _onChanged(String v) {
    _debounce?.cancel();
    final trimmed = v.trim();
    if (trimmed.isEmpty) {
      setState(() {
        _results = const [];
        _error = null;
        _lastQuery = '';
      });
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 350), () {
      _runSearch(trimmed);
    });
  }

  Future<void> _runSearch(String query) async {
    if (query == _lastQuery) return;
    _lastQuery = query;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final hits = await client.searchSessions(query);
      if (!mounted || _lastQuery != query) return;
      setState(() {
        _results = hits;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
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
        title: TextField(
          controller: _ctrl,
          autofocus: true,
          onChanged: _onChanged,
          decoration: InputDecoration(
            hintText: 'Search past sessions',
            border: InputBorder.none,
            hintStyle: GoogleFonts.spaceGrotesk(color: muted),
          ),
          style: GoogleFonts.spaceGrotesk(fontSize: 16),
        ),
        actions: [
          if (_ctrl.text.isNotEmpty)
            IconButton(
              tooltip: 'Clear',
              icon: const Icon(Icons.close),
              onPressed: () {
                _ctrl.clear();
                _onChanged('');
              },
            ),
        ],
      ),
      body: _body(muted),
    );
  }

  Widget _body(Color muted) {
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Search failed: $_error',
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(fontSize: 12, color: muted),
          ),
        ),
      );
    }
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (_lastQuery.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Type to search transcripts. Matches are ranked by recency.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted),
          ),
        ),
      );
    }
    if (_results.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No matches for "$_lastQuery".',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted),
          ),
        ),
      );
    }
    return ListView.separated(
      itemCount: _results.length,
      separatorBuilder: (_, __) =>
          Divider(height: 1, color: muted.withValues(alpha: 0.15)),
      itemBuilder: (_, i) => _ResultTile(row: _results[i]),
    );
  }
}

class _ResultTile extends ConsumerWidget {
  final Map<String, dynamic> row;
  const _ResultTile({required this.row});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final sessionId = (row['session_id'] ?? '').toString();
    final title = (row['session_title'] ?? '').toString();
    final scopeKind = (row['scope_kind'] ?? '').toString();
    final scopeID = (row['scope_id'] ?? '').toString();
    final snippet = (row['snippet'] ?? '').toString();
    final ts = (row['ts'] ?? '').toString();
    final scopeLabel = _scopeLabel(ref, scopeKind, scopeID);
    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      title: Text(
        title.isEmpty ? '(untitled session)' : title,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: FontWeight.w600,
          fontSize: 14,
        ),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (snippet.isNotEmpty)
            Text(
              _stripMarks(snippet),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: muted,
              ),
            ),
          const SizedBox(height: 2),
          Row(
            children: [
              Text(
                scopeLabel,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: muted,
                  letterSpacing: 0.4,
                ),
              ),
              const SizedBox(width: 6),
              Text(
                '· $ts',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: muted,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ),
        ],
      ),
      onTap: sessionId.isEmpty
          ? null
          : () => _openSession(context, ref, sessionId, title),
    );
  }

  static String _stripMarks(String s) =>
      s.replaceAll('<mark>', '').replaceAll('</mark>', '');

  String _scopeLabel(WidgetRef ref, String kind, String id) {
    switch (kind) {
      case 'project':
        final hub = ref.read(hubProvider).value;
        if (hub != null) {
          for (final p in hub.projects) {
            if ((p['id'] ?? '').toString() == id) {
              final name = (p['name'] ?? p['title'] ?? '').toString();
              if (name.isNotEmpty) return 'Project: $name';
            }
          }
        }
        return 'Project';
      case 'attention':
        return 'Approving';
      case 'team':
      case '':
        return 'General';
      default:
        return kind;
    }
  }

  Future<void> _openSession(
    BuildContext context,
    WidgetRef ref,
    String sessionId,
    String title,
  ) async {
    // Resolve agent_id by walking the hub's session list. Cheap —
    // sessions provider already holds the rows.
    final hub = ref.read(hubProvider).value;
    String agentId = '';
    if (hub != null) {
      final client = ref.read(hubProvider.notifier).client;
      if (client != null) {
        try {
          final s = await client.getSession(sessionId);
          agentId = (s['current_agent_id'] ?? '').toString();
        } catch (_) {}
      }
    }
    if (agentId.isEmpty || !context.mounted) return;
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SessionChatScreen(
          sessionId: sessionId,
          agentId: agentId,
          title: title.isEmpty ? 'Session' : title,
        ),
      ),
    );
  }
}
