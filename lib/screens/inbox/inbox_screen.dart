import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/active_session_provider.dart';
import '../../providers/hub_provider.dart';
import '../../providers/session_history_provider.dart';
import '../../theme/design_colors.dart';
import '../hub/search_screen.dart';
import '../terminal/terminal_screen.dart';

/// Inbox: single surface that collapses everything that wants the user's
/// attention into one scannable list. Replaces the old "recent sessions"
/// dashboard which duplicated the Active Sessions tab.
///
/// Sources, by filter chip:
///   - Approvals — attention items with kind in
///     {approval_request, decision, template_proposal}.
///   - Agents    — attention items with kind='idle' (host-runner flags agents
///     whose pane output has been stable past threshold).
///   - Messages  — every other attention kind (mentions, generic decisions).
///   - SSH       — detached tmux sessions, surfaced so the user can
///     reattach without hunting them down in the Servers tab.
class InboxScreen extends ConsumerWidget {
  const InboxScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider);
    final sessions = ref.watch(sessionHistoryProvider);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    final hubState = hub.value ?? const HubState();
    final items = _buildItems(hubState.attention, sessions);
    final filter = ref.watch(_filterProvider);
    final filtered = items.where(filter.matches).toList();

    return Scaffold(
      body: RefreshIndicator(
        onRefresh: () async {
          if (hubState.configured) {
            await ref.read(hubProvider.notifier).refreshAll();
          }
        },
        child: CustomScrollView(
          slivers: [
            SliverAppBar(
              floating: true,
              pinned: true,
              elevation: 0,
              backgroundColor: isDark
                  ? DesignColors.backgroundDark.withValues(alpha: 0.95)
                  : DesignColors.backgroundLight.withValues(alpha: 0.95),
              title: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(8),
                    child: Image.asset(
                      'assets/icon/icon.png',
                      width: 32,
                      height: 32,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Text(
                    'Inbox',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 24,
                      fontWeight: FontWeight.w700,
                      color: colorScheme.onSurface,
                    ),
                  ),
                ],
              ),
              actions: [
                IconButton(
                  icon: const Icon(Icons.search),
                  tooltip: 'Search events',
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const SearchScreen(),
                    ),
                  ),
                ),
              ],
            ),
            SliverToBoxAdapter(
              child: _FilterBar(
                counts: _countsByFilter(items),
                selected: filter,
                onChanged: (f) => ref.read(_filterProvider.notifier).set(f),
              ),
            ),
            if (hubState.loading && items.isEmpty)
              const SliverFillRemaining(
                hasScrollBody: false,
                child: Center(child: CircularProgressIndicator()),
              )
            else if (filtered.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: _EmptyState(filter: filter),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 100),
                sliver: SliverList.separated(
                  itemCount: filtered.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 10),
                  itemBuilder: (_, i) => _InboxCard(item: filtered[i]),
                ),
              ),
          ],
        ),
      ),
    );
  }

  List<_InboxItem> _buildItems(
    List<Map<String, dynamic>> attention,
    List<ActiveSession> sessions,
  ) {
    final out = <_InboxItem>[];
    for (final a in attention) {
      out.add(_InboxItem.attention(a));
    }
    for (final s in sessions.where((s) => !s.isAttached)) {
      out.add(_InboxItem.ssh(s));
    }
    out.sort((a, b) => b.ts.compareTo(a.ts));
    return out;
  }

  Map<_Filter, int> _countsByFilter(List<_InboxItem> items) {
    final counts = {for (final f in _Filter.values) f: 0};
    for (final item in items) {
      counts[_Filter.all] = (counts[_Filter.all] ?? 0) + 1;
      final f = item.filter;
      counts[f] = (counts[f] ?? 0) + 1;
    }
    return counts;
  }
}

enum _Filter { all, approvals, agents, messages, ssh }

extension _FilterX on _Filter {
  String get label {
    switch (this) {
      case _Filter.all:
        return 'All';
      case _Filter.approvals:
        return 'Approvals';
      case _Filter.agents:
        return 'Agents';
      case _Filter.messages:
        return 'Messages';
      case _Filter.ssh:
        return 'SSH';
    }
  }

  bool matches(_InboxItem item) {
    if (this == _Filter.all) return true;
    return item.filter == this;
  }
}

class _FilterNotifier extends Notifier<_Filter> {
  @override
  _Filter build() => _Filter.all;
  void set(_Filter f) => state = f;
}

final _filterProvider =
    NotifierProvider<_FilterNotifier, _Filter>(_FilterNotifier.new);

class _InboxItem {
  final String id;
  final _Filter filter;
  final String kind;
  final String title;
  final String? subtitle;
  final DateTime ts;
  final String? severity;
  // Only one of these is set; drives which handler the card calls.
  final Map<String, dynamic>? attention;
  final ActiveSession? sshSession;

  const _InboxItem({
    required this.id,
    required this.filter,
    required this.kind,
    required this.title,
    required this.ts,
    this.subtitle,
    this.severity,
    this.attention,
    this.sshSession,
  });

  factory _InboxItem.attention(Map<String, dynamic> a) {
    final kind = (a['kind'] ?? 'attention').toString();
    final filter = _filterForAttention(kind);
    return _InboxItem(
      id: (a['id'] ?? '').toString(),
      filter: filter,
      kind: kind,
      title: (a['summary'] ?? '').toString(),
      subtitle: (a['scope_kind'] ?? '').toString(),
      ts: _parseTs(a['created_at']?.toString()),
      severity: a['severity']?.toString(),
      attention: a,
    );
  }

  factory _InboxItem.ssh(ActiveSession s) => _InboxItem(
        id: s.key,
        filter: _Filter.ssh,
        kind: 'ssh_detached',
        title: '${s.connectionName}: ${s.sessionName}',
        subtitle: s.host,
        ts: s.lastAccessedAt ?? s.connectedAt,
        sshSession: s,
      );

  static _Filter _filterForAttention(String kind) {
    switch (kind) {
      case 'approval_request':
      case 'decision':
      case 'template_proposal':
        return _Filter.approvals;
      case 'idle':
      case 'agent_error':
        return _Filter.agents;
      default:
        return _Filter.messages;
    }
  }

  static DateTime _parseTs(String? raw) {
    if (raw == null || raw.isEmpty) return DateTime.now();
    return DateTime.tryParse(raw) ?? DateTime.now();
  }
}

class _FilterBar extends StatelessWidget {
  final Map<_Filter, int> counts;
  final _Filter selected;
  final ValueChanged<_Filter> onChanged;

  const _FilterBar({
    required this.counts,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          for (final f in _Filter.values) ...[
            _FilterChip(
              label: f.label,
              count: counts[f] ?? 0,
              selected: selected == f,
              onTap: () => onChanged(f),
            ),
            const SizedBox(width: 8),
          ],
        ],
      ),
    );
  }
}

class _FilterChip extends StatelessWidget {
  final String label;
  final int count;
  final bool selected;
  final VoidCallback onTap;

  const _FilterChip({
    required this.label,
    required this.count,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = selected
        ? DesignColors.primary
        : (isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight);
    final fg = selected
        ? Colors.white
        : (isDark
            ? DesignColors.textSecondary
            : DesignColors.textSecondaryLight);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: fg,
              ),
            ),
            if (count > 0) ...[
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: selected
                      ? Colors.white.withValues(alpha: 0.2)
                      : DesignColors.primary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Text(
                  '$count',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                    color: selected ? Colors.white : DesignColors.primary,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  final _Filter filter;
  const _EmptyState({required this.filter});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final msg = filter == _Filter.all
        ? 'Nothing in the inbox'
        : 'Nothing here';
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.inbox_outlined, size: 64, color: muted),
          const SizedBox(height: 16),
          Text(
            msg,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 16,
              fontWeight: FontWeight.w600,
              color: muted,
            ),
          ),
        ],
      ),
    );
  }
}

class _InboxCard extends ConsumerWidget {
  final _InboxItem item;
  const _InboxCard({required this.item});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final accent = _accentColor(item);

    return InkWell(
      onTap: () => _primaryAction(context, ref),
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: accent.withValues(alpha: 0.4)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                _KindChip(text: _kindLabel(item.kind), color: accent),
                if (item.severity != null && item.severity!.isNotEmpty) ...[
                  const SizedBox(width: 6),
                  _KindChip(
                    text: item.severity!,
                    color: _severityColor(item.severity!),
                  ),
                ],
                const Spacer(),
                Text(
                  _shortTs(item.ts),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              item.title,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: colorScheme.onSurface,
              ),
            ),
            if (item.subtitle != null && item.subtitle!.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                item.subtitle!,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
            if (item.filter == _Filter.approvals) ...[
              const SizedBox(height: 10),
              _ApprovalActions(id: item.id),
            ],
          ],
        ),
      ),
    );
  }

  void _primaryAction(BuildContext context, WidgetRef ref) {
    final s = item.sshSession;
    if (s != null) {
      ref.read(activeSessionsProvider.notifier).touchSession(
            s.connectionId,
            s.sessionName,
          );
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => TerminalScreen(
            connectionId: s.connectionId,
            sessionName: s.sessionName,
            lastWindowIndex: s.lastWindowIndex,
            lastPaneId: s.lastPaneId,
          ),
        ),
      );
      return;
    }
    // For attention items the inline Approve/Reject/Resolve buttons do the
    // real work; tapping the card is a no-op for now. A future patch can
    // push a full-screen detail view here.
  }

  Color _accentColor(_InboxItem item) {
    switch (item.filter) {
      case _Filter.approvals:
        return DesignColors.primary;
      case _Filter.agents:
        return Colors.orange;
      case _Filter.messages:
        return DesignColors.primary;
      case _Filter.ssh:
        return Colors.teal;
      case _Filter.all:
        return DesignColors.primary;
    }
  }

  Color _severityColor(String s) {
    switch (s) {
      case 'critical':
        return DesignColors.error;
      case 'major':
        return Colors.orange;
      case 'minor':
      default:
        return DesignColors.primary;
    }
  }

  String _kindLabel(String kind) {
    if (kind.isEmpty) return 'event';
    return kind.replaceAll('_', ' ');
  }

  String _shortTs(DateTime ts) {
    final diff = DateTime.now().difference(ts);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}

class _ApprovalActions extends ConsumerWidget {
  final String id;
  const _ApprovalActions({required this.id});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        OutlinedButton.icon(
          icon: const Icon(Icons.check, size: 16),
          label: const Text('Approve'),
          onPressed: () => _decide(context, ref, 'approve'),
        ),
        const SizedBox(width: 8),
        OutlinedButton.icon(
          icon: const Icon(Icons.close, size: 16),
          label: const Text('Reject'),
          style: OutlinedButton.styleFrom(
            foregroundColor: DesignColors.error,
          ),
          onPressed: () => _decide(context, ref, 'reject'),
        ),
      ],
    );
  }

  Future<void> _decide(
    BuildContext context,
    WidgetRef ref,
    String decision,
  ) async {
    try {
      await ref
          .read(hubProvider.notifier)
          .decide(id, decision, by: '@mobile');
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decision recorded: $decision')),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decide failed: $e')),
        );
      }
    }
  }
}

class _KindChip extends StatelessWidget {
  final String text;
  final Color color;
  const _KindChip({required this.text, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        text,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
          letterSpacing: 0.3,
        ),
      ),
    );
  }
}
