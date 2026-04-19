import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'hub_bootstrap_screen.dart';

/// Main dashboard for a configured Termipod Hub. Seven tabs:
///   - Attention: open attention_items (approvals, decisions, idle)
///   - Feed: live SSE of a chosen channel
///   - Tasks: per-project task list with status filter
///   - Templates: team-wide templates (agents/prompts/policies)
///   - Agents: kind/handle/status per agent
///   - Hosts: host-agents checking in
///   - Projects: project + channel inventory
///
/// If the hub isn't configured yet, we push [HubBootstrapScreen] from the
/// empty state; once it pops true, the provider rebuilds and the real
/// dashboard takes over.
class HubScreen extends ConsumerStatefulWidget {
  const HubScreen({super.key});

  @override
  ConsumerState<HubScreen> createState() => _HubScreenState();
}

class _HubScreenState extends ConsumerState<HubScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 7, vsync: this);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final st = ref.read(hubProvider).value;
      if (st != null && st.configured) {
        ref.read(hubProvider.notifier).refreshAll();
      }
    });
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(hubProvider);
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Hub',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 22,
            fontWeight: FontWeight.w700,
            color: colorScheme.onSurface,
          ),
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: async.value?.configured == true
                ? () => ref.read(hubProvider.notifier).refreshAll()
                : null,
          ),
          IconButton(
            tooltip: 'Hub settings',
            icon: const Icon(Icons.settings),
            onPressed: () {
              Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => const HubBootstrapScreen(),
              ));
            },
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(48),
          child: TabBar(
            controller: _tabs,
            isScrollable: true,
            tabs: const [
              Tab(icon: Icon(Icons.error_outline), text: 'Attention'),
              Tab(icon: Icon(Icons.podcasts), text: 'Feed'),
              Tab(icon: Icon(Icons.check_box_outlined), text: 'Tasks'),
              Tab(icon: Icon(Icons.description_outlined), text: 'Templates'),
              Tab(icon: Icon(Icons.smart_toy_outlined), text: 'Agents'),
              Tab(icon: Icon(Icons.dns_outlined), text: 'Hosts'),
              Tab(icon: Icon(Icons.folder_outlined), text: 'Projects'),
            ],
          ),
        ),
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(error: '$e'),
        data: (st) {
          if (!st.configured) return const _NotConfiguredView();
          return Column(
            children: [
              if (st.error != null) _ErrorBanner(text: st.error!),
              Expanded(
                child: TabBarView(
                  controller: _tabs,
                  children: [
                    _AttentionTab(items: st.attention),
                    _FeedTab(projects: st.projects),
                    _TasksTab(projects: st.projects),
                    _TemplatesTab(items: st.templates),
                    _AgentsTab(items: st.agents, hosts: st.hosts),
                    _HostsTab(items: st.hosts),
                    _ProjectsTab(items: st.projects),
                  ],
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Empty / error helpers
// ---------------------------------------------------------------------

class _NotConfiguredView extends StatelessWidget {
  const _NotConfiguredView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.hub_outlined,
                size: 72, color: DesignColors.primary.withValues(alpha: 0.7)),
            const SizedBox(height: 16),
            Text(
              'Termipod Hub not configured',
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 18, fontWeight: FontWeight.w700),
            ),
            const SizedBox(height: 8),
            Text(
              'Paste a hub URL and bearer token to see attention items, '
              'agents, and the live event feed.',
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
            ),
            const SizedBox(height: 24),
            FilledButton.icon(
              icon: const Icon(Icons.login),
              label: const Text('Configure Hub'),
              onPressed: () {
                Navigator.of(context).push(MaterialPageRoute(
                  builder: (_) => const HubBootstrapScreen(),
                ));
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String error;
  const _ErrorView({required this.error});
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(error,
            style: GoogleFonts.jetBrainsMono(color: DesignColors.error)),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  final String text;
  const _ErrorBanner({required this.text});
  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: DesignColors.error.withValues(alpha: 0.12),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(text,
          style: GoogleFonts.jetBrainsMono(
              fontSize: 11, color: DesignColors.error)),
    );
  }
}

// ---------------------------------------------------------------------
// Attention tab — approve / reject / resolve
// ---------------------------------------------------------------------

class _AttentionTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _AttentionTab({required this.items});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) {
      return const _EmptyText(text: 'No open attention items');
    }
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 10),
        itemBuilder: (_, i) => _AttentionCard(item: items[i]),
      ),
    );
  }
}

class _AttentionCard extends ConsumerWidget {
  final Map<String, dynamic> item;
  const _AttentionCard({required this.item});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final severity = (item['severity'] ?? 'minor') as String;
    final kind = (item['kind'] ?? '') as String;
    final summary = (item['summary'] ?? '') as String;
    final id = (item['id'] ?? '') as String;
    final createdAt = (item['created_at'] ?? '') as String;

    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: _severityColor(severity).withValues(alpha: 0.5),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              _Chip(text: kind, color: DesignColors.primary),
              const SizedBox(width: 6),
              _Chip(text: severity, color: _severityColor(severity)),
              const Spacer(),
              Text(
                _shortTs(createdAt),
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            summary,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 14, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 10),
          if (kind == 'approval_request' ||
              kind == 'decision' ||
              kind == 'template_proposal')
            Row(
              children: [
                OutlinedButton.icon(
                  icon: const Icon(Icons.check, size: 16),
                  label: const Text('Approve'),
                  onPressed: () => _decide(context, ref, id, 'approve'),
                ),
                const SizedBox(width: 8),
                OutlinedButton.icon(
                  icon: const Icon(Icons.close, size: 16),
                  label: const Text('Reject'),
                  style: OutlinedButton.styleFrom(
                      foregroundColor: DesignColors.error),
                  onPressed: () => _decide(context, ref, id, 'reject'),
                ),
              ],
            )
          else
            OutlinedButton.icon(
              icon: const Icon(Icons.done, size: 16),
              label: const Text('Resolve'),
              onPressed: () => _resolve(context, ref, id),
            ),
        ],
      ),
    );
  }

  Future<void> _decide(
      BuildContext context, WidgetRef ref, String id, String decision) async {
    try {
      await ref.read(hubProvider.notifier).decide(id, decision, by: '@mobile');
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

  Future<void> _resolve(BuildContext context, WidgetRef ref, String id) async {
    try {
      await ref.read(hubProvider.notifier).resolve(id, by: '@mobile');
      if (context.mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('Resolved')));
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Resolve failed: $e')),
        );
      }
    }
  }

  Color _severityColor(String s) {
    switch (s) {
      case 'critical':
        return DesignColors.error;
      case 'major':
        return Colors.orange;
      case 'minor':
        return DesignColors.primary;
      default:
        return DesignColors.primary;
    }
  }
}

// ---------------------------------------------------------------------
// Feed tab — pick a channel, subscribe to SSE
// ---------------------------------------------------------------------

class _FeedTab extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> projects;
  const _FeedTab({required this.projects});

  @override
  ConsumerState<_FeedTab> createState() => _FeedTabState();
}

class _FeedTabState extends ConsumerState<_FeedTab> {
  String? _projectId;
  String? _channelId;
  List<Map<String, dynamic>> _channels = const [];

  @override
  void dispose() {
    ref.read(hubFeedProvider.notifier).stop();
    super.dispose();
  }

  Future<void> _loadChannels(String projectId) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final chans = await client.listChannels(projectId);
      if (!mounted) return;
      setState(() {
        _channels = chans;
        _channelId = null;
      });
    } catch (_) {
      if (mounted) setState(() => _channels = const []);
    }
  }

  void _subscribe() {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || _projectId == null || _channelId == null) return;
    ref.read(hubFeedProvider.notifier).subscribe(
          client: client,
          projectId: _projectId!,
          channelId: _channelId!,
        );
  }

  @override
  Widget build(BuildContext context) {
    final feed = ref.watch(hubFeedProvider);

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              Expanded(
                child: DropdownButtonFormField<String>(
                  initialValue: _projectId,
                  isExpanded: true,
                  decoration: const InputDecoration(
                    labelText: 'Project',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  items: widget.projects
                      .map((p) => DropdownMenuItem<String>(
                            value: p['id']?.toString(),
                            child: Text(p['name']?.toString() ?? '?'),
                          ))
                      .toList(),
                  onChanged: (v) {
                    setState(() => _projectId = v);
                    if (v != null) _loadChannels(v);
                  },
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: DropdownButtonFormField<String>(
                  initialValue: _channelId,
                  isExpanded: true,
                  decoration: const InputDecoration(
                    labelText: 'Channel',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  items: _channels
                      .map((c) => DropdownMenuItem<String>(
                            value: c['id']?.toString(),
                            child: Text(c['name']?.toString() ?? '?'),
                          ))
                      .toList(),
                  onChanged: (v) {
                    setState(() => _channelId = v);
                    _subscribe();
                  },
                ),
              ),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: feed.isEmpty
              ? const _EmptyText(
                  text: 'Pick a project and channel to stream events')
              : ListView.separated(
                  padding: const EdgeInsets.all(12),
                  itemCount: feed.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 8),
                  itemBuilder: (_, i) => _FeedRow(entry: feed[i]),
                ),
        ),
      ],
    );
  }
}

class _FeedRow extends StatelessWidget {
  final HubFeedEntry entry;
  const _FeedRow({required this.entry});
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              _Chip(text: entry.type, color: DesignColors.primary),
              const SizedBox(width: 6),
              if (entry.fromId.isNotEmpty)
                Flexible(
                  child: Text(entry.fromId,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.jetBrainsMono(fontSize: 11)),
                ),
              const Spacer(),
              Text(
                entry.ts == null
                    ? ''
                    : '${entry.ts!.hour.toString().padLeft(2, '0')}:'
                        '${entry.ts!.minute.toString().padLeft(2, '0')}:'
                        '${entry.ts!.second.toString().padLeft(2, '0')}',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight),
              ),
            ],
          ),
          if (entry.preview.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(entry.preview,
                style: GoogleFonts.jetBrainsMono(fontSize: 12)),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Tasks tab — pick a project, list tasks, tap to update status
// ---------------------------------------------------------------------

class _TasksTab extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> projects;
  const _TasksTab({required this.projects});

  @override
  ConsumerState<_TasksTab> createState() => _TasksTabState();
}

// Kanban columns, left-to-right. Swiping end→start on a card advances it
// one column to the right; start→end reverses it.
const List<String> _kanbanStatuses = ['open', 'in_progress', 'done'];

class _TasksTabState extends ConsumerState<_TasksTab> {
  String? _projectId;
  List<Map<String, dynamic>> _tasks = const [];
  bool _loading = false;
  String? _error;
  final PageController _pages = PageController();
  int _currentColumn = 0;

  @override
  void dispose() {
    _pages.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final pid = _projectId;
    if (client == null || pid == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      // Pull every status once and bucket locally — lets us show counts and
      // move cards between columns without refetching on every swipe.
      final items = await client.listTasks(pid);
      if (!mounted) return;
      setState(() {
        _tasks = items;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _patchStatus(Map<String, dynamic> task, String status) async {
    final client = ref.read(hubProvider.notifier).client;
    final pid = _projectId;
    final tid = task['id']?.toString();
    if (client == null || pid == null || tid == null) return;
    // Optimistic: flip the card locally so the board updates immediately.
    final prev = task['status']?.toString() ?? 'open';
    setState(() => task['status'] = status);
    try {
      await client.patchTask(pid, tid, status: status);
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Task → $status')));
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => task['status'] = prev);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Update failed: $e')));
    }
  }

  List<Map<String, dynamic>> _tasksIn(String status) {
    return _tasks
        .where((t) => (t['status']?.toString() ?? 'open') == status)
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: DropdownButtonFormField<String>(
            initialValue: _projectId,
            isExpanded: true,
            decoration: const InputDecoration(
              labelText: 'Project',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            items: widget.projects
                .map((p) => DropdownMenuItem<String>(
                      value: p['id']?.toString(),
                      child: Text(p['name']?.toString() ?? '?'),
                    ))
                .toList(),
            onChanged: (v) {
              setState(() => _projectId = v);
              if (v != null) _load();
            },
          ),
        ),
        _KanbanHeader(
          currentIndex: _currentColumn,
          counts: {
            for (final s in _kanbanStatuses) s: _tasksIn(s).length,
          },
          onTap: (i) {
            setState(() => _currentColumn = i);
            _pages.animateToPage(
              i,
              duration: const Duration(milliseconds: 200),
              curve: Curves.easeOut,
            );
          },
        ),
        const Divider(height: 1),
        Expanded(
          child: _projectId == null
              ? const _EmptyText(text: 'Pick a project to list its tasks')
              : _loading
                  ? const Center(child: CircularProgressIndicator())
                  : _error != null
                      ? _EmptyText(text: _error!)
                      : PageView.builder(
                          controller: _pages,
                          itemCount: _kanbanStatuses.length,
                          onPageChanged: (i) =>
                              setState(() => _currentColumn = i),
                          itemBuilder: (_, i) {
                            final status = _kanbanStatuses[i];
                            final bucket = _tasksIn(status);
                            if (bucket.isEmpty) {
                              return RefreshIndicator(
                                onRefresh: _load,
                                child: ListView(
                                  children: [
                                    SizedBox(
                                      height: 240,
                                      child: _EmptyText(
                                          text: 'No $status tasks'),
                                    ),
                                  ],
                                ),
                              );
                            }
                            return RefreshIndicator(
                              onRefresh: _load,
                              child: ListView.separated(
                                padding: const EdgeInsets.all(12),
                                itemCount: bucket.length,
                                separatorBuilder: (_, __) =>
                                    const SizedBox(height: 8),
                                itemBuilder: (_, j) => _SwipableTaskCard(
                                  key: ValueKey(
                                      'task-${bucket[j]['id']}-$status'),
                                  task: bucket[j],
                                  columnIndex: i,
                                  onAdvance: (s) =>
                                      _patchStatus(bucket[j], s),
                                ),
                              ),
                            );
                          },
                        ),
        ),
      ],
    );
  }
}

class _KanbanHeader extends StatelessWidget {
  final int currentIndex;
  final Map<String, int> counts;
  final ValueChanged<int> onTap;
  const _KanbanHeader({
    required this.currentIndex,
    required this.counts,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: [
          for (var i = 0; i < _kanbanStatuses.length; i++)
            Expanded(
              child: _KanbanPill(
                status: _kanbanStatuses[i],
                count: counts[_kanbanStatuses[i]] ?? 0,
                selected: i == currentIndex,
                onTap: () => onTap(i),
              ),
            ),
        ],
      ),
    );
  }
}

class _KanbanPill extends StatelessWidget {
  final String status;
  final int count;
  final bool selected;
  final VoidCallback onTap;
  const _KanbanPill({
    required this.status,
    required this.count,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final color = _statusPillColor(status);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        margin: const EdgeInsets.symmetric(horizontal: 2, vertical: 8),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? color.withValues(alpha: 0.18)
              : (isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight),
          borderRadius: BorderRadius.circular(6),
          border: Border.all(
            color: selected
                ? color
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(status,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: selected ? color : null)),
            Text('$count',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 14, fontWeight: FontWeight.w700)),
          ],
        ),
      ),
    );
  }
}

Color _statusPillColor(String status) {
  switch (status) {
    case 'done':
      return Colors.green;
    case 'in_progress':
      return Colors.orange;
    case 'open':
    default:
      return DesignColors.primary;
  }
}

class _SwipableTaskCard extends StatelessWidget {
  final Map<String, dynamic> task;
  final int columnIndex;
  final void Function(String) onAdvance;
  const _SwipableTaskCard({
    super.key,
    required this.task,
    required this.columnIndex,
    required this.onAdvance,
  });

  @override
  Widget build(BuildContext context) {
    final hasNext = columnIndex < _kanbanStatuses.length - 1;
    final hasPrev = columnIndex > 0;
    return Dismissible(
      key: ValueKey('dismiss-${task['id']}-$columnIndex'),
      direction: hasNext && hasPrev
          ? DismissDirection.horizontal
          : hasNext
              ? DismissDirection.endToStart
              : DismissDirection.startToEnd,
      background: _swipeBackground(
        alignment: Alignment.centerLeft,
        color: DesignColors.primary,
        icon: Icons.arrow_back,
        label: hasPrev
            ? 'Move to ${_kanbanStatuses[columnIndex - 1]}'
            : '',
      ),
      secondaryBackground: _swipeBackground(
        alignment: Alignment.centerRight,
        color: _statusPillColor(
            hasNext ? _kanbanStatuses[columnIndex + 1] : 'done'),
        icon: Icons.arrow_forward,
        label: hasNext
            ? 'Move to ${_kanbanStatuses[columnIndex + 1]}'
            : '',
      ),
      confirmDismiss: (dir) async {
        if (dir == DismissDirection.endToStart && hasNext) {
          onAdvance(_kanbanStatuses[columnIndex + 1]);
        } else if (dir == DismissDirection.startToEnd && hasPrev) {
          onAdvance(_kanbanStatuses[columnIndex - 1]);
        }
        // Return false so Dismissible snaps back and we can manage the list
        // ourselves via the optimistic status flip.
        return false;
      },
      child: _TaskCard(
        task: task,
        onAdvance: onAdvance,
      ),
    );
  }

  Widget _swipeBackground({
    required Alignment alignment,
    required Color color,
    required IconData icon,
    required String label,
  }) {
    return Container(
      alignment: alignment,
      padding: const EdgeInsets.symmetric(horizontal: 20),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.22),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (alignment == Alignment.centerLeft) Icon(icon, color: color),
          if (label.isNotEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Text(label,
                  style: GoogleFonts.spaceGrotesk(
                      color: color, fontWeight: FontWeight.w600)),
            ),
          if (alignment == Alignment.centerRight) Icon(icon, color: color),
        ],
      ),
    );
  }
}

class _TaskCard extends StatelessWidget {
  final Map<String, dynamic> task;
  final void Function(String) onAdvance;
  const _TaskCard({required this.task, required this.onAdvance});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final status = (task['status'] ?? 'open') as String;
    final title = (task['title'] ?? '(untitled)') as String;
    final body = (task['body_md'] ?? '') as String;
    final updated = (task['updated_at'] ?? '') as String;

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
            color:
                isDark ? DesignColors.borderDark : DesignColors.borderLight),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              _Chip(text: status, color: _statusColor(status)),
              const Spacer(),
              Text(_shortTs(updated),
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight)),
            ],
          ),
          const SizedBox(height: 6),
          Text(title,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 14, fontWeight: FontWeight.w600)),
          if (body.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              body,
              maxLines: 3,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight),
            ),
          ],
          const SizedBox(height: 8),
          Wrap(
            spacing: 6,
            children: [
              if (status != 'in_progress')
                OutlinedButton(
                  onPressed: () => onAdvance('in_progress'),
                  child: const Text('Start'),
                ),
              if (status != 'done')
                OutlinedButton(
                  onPressed: () => onAdvance('done'),
                  child: const Text('Done'),
                ),
              if (status != 'open')
                OutlinedButton(
                  onPressed: () => onAdvance('open'),
                  child: const Text('Reopen'),
                ),
            ],
          ),
        ],
      ),
    );
  }

  Color _statusColor(String s) {
    switch (s) {
      case 'done':
        return Colors.green;
      case 'in_progress':
        return Colors.orange;
      case 'open':
        return DesignColors.primary;
      default:
        return DesignColors.primary;
    }
  }
}

// ---------------------------------------------------------------------
// Templates tab — list team templates; tap to view raw body
// ---------------------------------------------------------------------

class _TemplatesTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _TemplatesTab({required this.items});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) {
      return const _EmptyText(text: 'No templates on this hub');
    }
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final t = items[i];
          final category = t['category']?.toString() ?? '';
          final name = t['name']?.toString() ?? '?';
          final size = t['size'] is int ? t['size'] as int : 0;
          return _InfoTile(
            title: name,
            subtitle: '$category · ${size}B',
            onTap: () => _openTemplate(context, ref, category, name),
          );
        },
      ),
    );
  }

  Future<void> _openTemplate(
      BuildContext context, WidgetRef ref, String category, String name) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final body = await client.getTemplate(category, name);
      if (!context.mounted) return;
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        builder: (_) => _TemplateViewer(
          title: '$category/$name',
          body: body,
        ),
      );
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Fetch failed: $e')));
      }
    }
  }
}

class _TemplateViewer extends StatelessWidget {
  final String title;
  final String body;
  const _TemplateViewer({required this.title, required this.body});

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Column(
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            child: Row(
              children: [
                Expanded(
                  child: Text(title,
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 15, fontWeight: FontWeight.w700)),
                ),
                IconButton(
                  icon: const Icon(Icons.close),
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: SingleChildScrollView(
              controller: scroll,
              padding: const EdgeInsets.all(16),
              child: SelectableText(
                body,
                style: GoogleFonts.jetBrainsMono(fontSize: 12),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Agents / Hosts / Projects tabs — read-only tables
// ---------------------------------------------------------------------

class _AgentsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  final List<Map<String, dynamic>> hosts;
  const _AgentsTab({required this.items, required this.hosts});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Bootstrap: hub has hosts registered but no agents yet — prompt the
    // operator to spawn a Steward so there's someone to hand tasks to.
    if (items.isEmpty && hosts.isNotEmpty) {
      return Stack(
        children: [
          _SpawnStewardCard(hosts: hosts),
          _SpawnAgentFab(hosts: hosts),
        ],
      );
    }
    if (items.isEmpty) {
      return Stack(
        children: [
          const _EmptyText(text: 'No agents registered'),
          if (hosts.isNotEmpty) _SpawnAgentFab(hosts: hosts),
        ],
      );
    }
    return Stack(
      children: [
        RefreshIndicator(
          onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
          child: ListView.separated(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 88),
            itemCount: items.length,
            separatorBuilder: (_, __) => const SizedBox(height: 8),
            itemBuilder: (_, i) {
              final a = items[i];
              return _InfoTile(
                title: a['handle']?.toString() ?? '?',
                subtitle: '${a['kind'] ?? ''} · ${a['status'] ?? ''}',
                trailing: (a['pause_state']?.toString() ?? 'running') == 'paused'
                    ? 'paused'
                    : null,
              );
            },
          ),
        ),
        if (hosts.isNotEmpty) _SpawnAgentFab(hosts: hosts),
      ],
    );
  }
}

class _SpawnAgentFab extends ConsumerWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnAgentFab({required this.hosts});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Positioned(
      right: 16,
      bottom: 16,
      child: FloatingActionButton.extended(
        heroTag: 'spawn_agent_fab',
        onPressed: () => showModalBottomSheet<void>(
          context: context,
          isScrollControlled: true,
          builder: (_) => _SpawnAgentDialog(hosts: hosts),
        ),
        icon: const Icon(Icons.add),
        label: const Text('Spawn Agent'),
      ),
    );
  }
}

class _SpawnAgentDialog extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnAgentDialog({required this.hosts});

  @override
  ConsumerState<_SpawnAgentDialog> createState() => _SpawnAgentDialogState();
}

class _SpawnAgentDialogState extends ConsumerState<_SpawnAgentDialog> {
  final _handleCtl = TextEditingController();
  final _kindCtl = TextEditingController(text: 'claude-code');
  final _yamlCtl = TextEditingController();
  String? _hostId;
  bool _busy = false;
  String? _error;
  List<Map<String, dynamic>>? _templates;

  @override
  void initState() {
    super.initState();
    // Prefer an online host as the default.
    final online = widget.hosts.where(
      (h) => (h['status']?.toString() ?? '') == 'online',
    );
    _hostId = (online.isNotEmpty ? online.first : widget.hosts.first)['id']
        ?.toString();
  }

  @override
  void dispose() {
    _handleCtl.dispose();
    _kindCtl.dispose();
    _yamlCtl.dispose();
    super.dispose();
  }

  Future<void> _loadTemplate() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      _templates ??= await client.listTemplates();
      final agentTemplates = _templates!
          .where((t) => (t['category']?.toString() ?? '') == 'agents')
          .toList();
      if (!mounted) return;
      if (agentTemplates.isEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('No agent templates on this hub')),
        );
        return;
      }
      final picked = await showModalBottomSheet<Map<String, dynamic>>(
        context: context,
        builder: (_) => ListView(
          shrinkWrap: true,
          children: [
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('Pick a template',
                  style: TextStyle(fontWeight: FontWeight.w700)),
            ),
            const Divider(height: 1),
            for (final t in agentTemplates)
              ListTile(
                title: Text(t['name']?.toString() ?? '?'),
                subtitle: Text('${t['size'] ?? 0}B'),
                onTap: () => Navigator.of(context).pop(t),
              ),
          ],
        ),
      );
      if (picked == null || !mounted) return;
      final name = picked['name']?.toString() ?? '';
      final body = await client.getTemplate('agents', name);
      if (!mounted) return;
      setState(() => _yamlCtl.text = body);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Load template failed: $e')));
    }
  }

  Future<void> _submit() async {
    final handle = _handleCtl.text.trim();
    final kind = _kindCtl.text.trim();
    final yaml = _yamlCtl.text;
    if (handle.isEmpty || kind.isEmpty || yaml.trim().isEmpty) {
      setState(() => _error = 'handle, kind, and YAML spec are required');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) throw StateError('Hub not configured');
      final res = await client.spawnAgent(
        childHandle: handle,
        kind: kind,
        spawnSpecYaml: yaml,
        hostId: _hostId,
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Agent "$handle" spawned.';
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    return DraggableScrollableSheet(
      initialChildSize: 0.9,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Padding(
        padding: EdgeInsets.only(bottom: bottomInset),
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Row(
                children: [
                  const Expanded(
                    child: Text('Spawn agent',
                        style: TextStyle(
                            fontSize: 16, fontWeight: FontWeight.w700)),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView(
                controller: scroll,
                padding: const EdgeInsets.all(16),
                children: [
                  TextField(
                    controller: _handleCtl,
                    decoration: const InputDecoration(
                      labelText: 'Handle',
                      hintText: 'e.g. worker-fe',
                      border: OutlineInputBorder(),
                    ),
                    autofocus: true,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _kindCtl,
                    decoration: const InputDecoration(
                      labelText: 'Kind',
                      hintText: 'claude-code, kimi-code, …',
                      border: OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    initialValue: _hostId,
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: 'Host',
                      border: OutlineInputBorder(),
                    ),
                    items: widget.hosts
                        .map((h) => DropdownMenuItem<String>(
                              value: h['id']?.toString(),
                              child: Text(
                                '${h['name'] ?? '?'} '
                                '(${h['status'] ?? 'unknown'})',
                              ),
                            ))
                        .toList(),
                    onChanged: (v) => setState(() => _hostId = v),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      const Text('Spawn spec (YAML)',
                          style: TextStyle(fontWeight: FontWeight.w600)),
                      const Spacer(),
                      TextButton.icon(
                        onPressed: _loadTemplate,
                        icon: const Icon(Icons.file_open, size: 18),
                        label: const Text('Load template'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  TextField(
                    controller: _yamlCtl,
                    maxLines: 14,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    decoration: const InputDecoration(
                      hintText:
                          'template: agents.custom.v1\nhandle: {{handle}}\n…',
                      border: OutlineInputBorder(),
                      alignLabelWithHint: true,
                    ),
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 12),
                    Text(_error!,
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                  ],
                  const SizedBox(height: 16),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      TextButton(
                        onPressed:
                            _busy ? null : () => Navigator.of(context).pop(),
                        child: const Text('Cancel'),
                      ),
                      const SizedBox(width: 8),
                      FilledButton.icon(
                        onPressed: _busy ? null : _submit,
                        icon: _busy
                            ? const SizedBox(
                                width: 16,
                                height: 16,
                                child:
                                    CircularProgressIndicator(strokeWidth: 2),
                              )
                            : const Icon(Icons.play_arrow),
                        label: Text(_busy ? 'Spawning…' : 'Spawn'),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SpawnStewardCard extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnStewardCard({required this.hosts});

  @override
  ConsumerState<_SpawnStewardCard> createState() => _SpawnStewardCardState();
}

class _SpawnStewardCardState extends ConsumerState<_SpawnStewardCard> {
  bool _busy = false;
  String? _lastError;

  Future<void> _spawn() async {
    setState(() {
      _busy = true;
      _lastError = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) {
        throw StateError('Hub not configured');
      }
      final yaml = await client.getTemplate('agents', 'steward.v1');
      // Prefer an online host; fall back to whatever's listed first.
      final host = widget.hosts.firstWhere(
        (h) => (h['status']?.toString() ?? '') == 'online',
        orElse: () => widget.hosts.first,
      );
      final res = await client.spawnAgent(
        childHandle: 'steward',
        kind: 'claude-code',
        spawnSpecYaml: yaml,
        hostId: host['id']?.toString(),
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Steward spawned.';
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      setState(() => _lastError = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(children: const [
                  Icon(Icons.auto_awesome, size: 28),
                  SizedBox(width: 10),
                  Text('Welcome to your hub',
                      style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
                ]),
                const SizedBox(height: 12),
                const Text(
                  'A host is online but no agents are running yet. '
                  'Spawn a Steward to coordinate work, take delegations, '
                  'and hand out tasks to other agents.',
                ),
                const SizedBox(height: 16),
                if (_lastError != null) ...[
                  Text(_lastError!,
                      style: TextStyle(color: Theme.of(context).colorScheme.error)),
                  const SizedBox(height: 12),
                ],
                Align(
                  alignment: Alignment.centerRight,
                  child: FilledButton.icon(
                    onPressed: _busy ? null : _spawn,
                    icon: _busy
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.play_arrow),
                    label: Text(_busy ? 'Spawning…' : 'Spawn Steward'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _HostsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _HostsTab({required this.items});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) return const _EmptyText(text: 'No hosts registered');
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final h = items[i];
          return _InfoTile(
            title: h['name']?.toString() ?? '?',
            subtitle: 'status: ${h['status'] ?? 'unknown'}',
            trailing: _shortTs((h['last_seen_at'] ?? '') as String),
          );
        },
      ),
    );
  }
}

class _ProjectsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _ProjectsTab({required this.items});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) return const _EmptyText(text: 'No projects yet');
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final p = items[i];
          return _InfoTile(
            title: p['name']?.toString() ?? '?',
            subtitle: p['status']?.toString() ?? '',
            trailing: _shortTs((p['created_at'] ?? '') as String),
          );
        },
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Small UI helpers
// ---------------------------------------------------------------------

class _EmptyText extends StatelessWidget {
  final String text;
  const _EmptyText({required this.text});
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          text,
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 14,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
        ),
      ),
    );
  }
}

class _InfoTile extends StatelessWidget {
  final String title;
  final String subtitle;
  final String? trailing;
  final VoidCallback? onTap;
  const _InfoTile({
    required this.title,
    required this.subtitle,
    this.trailing,
    this.onTap,
  });
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final tile = Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w600)),
                if (subtitle.isNotEmpty)
                  Text(subtitle,
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 11,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight)),
              ],
            ),
          ),
          if (trailing != null && trailing!.isNotEmpty)
            Text(trailing!,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight)),
        ],
      ),
    );
    if (onTap == null) return tile;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: onTap,
      child: tile,
    );
  }
}

class _Chip extends StatelessWidget {
  final String text;
  final Color color;
  const _Chip({required this.text, required this.color});
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        text,
        style: GoogleFonts.jetBrainsMono(
            fontSize: 10, fontWeight: FontWeight.w600, color: color),
      ),
    );
  }
}

String _shortTs(String iso) {
  if (iso.isEmpty) return '';
  final t = DateTime.tryParse(iso);
  if (t == null) return iso;
  final local = t.toLocal();
  final now = DateTime.now();
  final diff = now.difference(local);
  if (diff.inSeconds < 60) return '${diff.inSeconds}s';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  return '${diff.inDays}d';
}
