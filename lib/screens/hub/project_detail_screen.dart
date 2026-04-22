import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'blobs_section.dart';
import 'docs_section.dart';
import 'documents_screen.dart';
import 'project_channel_create_sheet.dart';
import 'project_channel_screen.dart';
import 'project_edit_sheet.dart';
import 'project_task_create_sheet.dart';
import 'reviews_screen.dart';
import 'runs_screen.dart';
import 'task_detail_screen.dart';

/// Linear-style project detail. Horizontal PageView over seven sections —
/// the pill bar and the PageView are bound to the same index so either
/// tapping a pill or swiping the body flips both.
///
/// Activity is the default landing view (index 0). It collapses the most
/// recent events across every channel in the project into a single feed.
///
/// Tabs:
///   0 Activity  — recent events from all channels (backfilled, no SSE
///                 — we only stream the single channel the feed opens).
///   1 Channels  — channel list + per-channel composer; FAB creates.
///   2 Tasks     — Kanban over the project's tasks; FAB creates.
///   3 Agents    — agents whose channel's project matches this one.
///   4 Docs      — read-only tree of the project's docs_root.
///   5 Blobs     — cached device-local blob list; upload + share.
///   6 Info      — name, id, docs_root, config_yaml; archive action.
class ProjectDetailScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic> project;
  const ProjectDetailScreen({super.key, required this.project});

  @override
  ConsumerState<ProjectDetailScreen> createState() =>
      _ProjectDetailScreenState();
}

class _ProjectDetailScreenState extends ConsumerState<ProjectDetailScreen> {
  final _pager = PageController();
  int _index = 0;
  late Map<String, dynamic> _project;

  static const _labels = [
    'Activity',
    'Channels',
    'Tasks',
    'Agents',
    'Docs',
    'Blobs',
    'Info',
  ];

  @override
  void initState() {
    super.initState();
    _project = Map<String, dynamic>.from(widget.project);
  }

  @override
  void dispose() {
    _pager.dispose();
    super.dispose();
  }

  void _jump(int i) {
    setState(() => _index = i);
    _pager.animateToPage(
      i,
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOut,
    );
  }

  Future<void> _edit() async {
    final updated = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => ProjectEditSheet(project: _project),
    );
    if (updated == null || !mounted) return;
    setState(() => _project = Map<String, dynamic>.from(updated));
    await ref.read(hubProvider.notifier).refreshAll();
  }

  @override
  Widget build(BuildContext context) {
    final name = (_project['name'] ?? 'Project').toString();
    final projectId = (_project['id'] ?? '').toString();
    final kind = (_project['kind'] ?? 'goal').toString();
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Flexible(
              child: Text(
                name,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 18, fontWeight: FontWeight.w700),
              ),
            ),
            const SizedBox(width: 8),
            _ProjectKindChip(kind: kind),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.edit_outlined),
            tooltip: 'Edit project',
            onPressed: _edit,
          ),
        ],
      ),
      body: Column(
        children: [
          _PillBar(
            labels: _labels,
            selected: _index,
            onChanged: _jump,
          ),
          Expanded(
            child: PageView(
              controller: _pager,
              onPageChanged: (i) => setState(() => _index = i),
              children: [
                _ActivityView(projectId: projectId),
                _ChannelsView(projectId: projectId),
                _TasksView(projectId: projectId),
                _AgentsView(projectId: projectId),
                DocsSection(projectId: projectId),
                const BlobsSection(),
                _InfoView(project: _project),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PillBar extends StatelessWidget {
  final List<String> labels;
  final int selected;
  final ValueChanged<int> onChanged;
  const _PillBar({
    required this.labels,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Row(
          children: [
            for (var i = 0; i < labels.length; i++) ...[
              _Pill(
                label: labels[i],
                selected: selected == i,
                onTap: () => onChanged(i),
              ),
              const SizedBox(width: 8),
            ],
          ],
        ),
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _Pill({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary
              : (isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: selected
                ? Colors.white
                : (isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight),
          ),
        ),
      ),
    );
  }
}

// ---- Activity ----

class _ActivityView extends ConsumerStatefulWidget {
  final String projectId;
  const _ActivityView({required this.projectId});

  @override
  ConsumerState<_ActivityView> createState() => _ActivityViewState();
}

class _ActivityViewState extends ConsumerState<_ActivityView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _events = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final channels = await client.listChannels(widget.projectId);
      final all = <Map<String, dynamic>>[];
      for (final c in channels) {
        final id = (c['id'] ?? '').toString();
        if (id.isEmpty) continue;
        final evts = await client.listProjectChannelEvents(
          widget.projectId,
          id,
          limit: 20,
        );
        for (final e in evts) {
          e['channel_name'] = c['name'];
          all.add(e);
        }
      }
      all.sort((a, b) {
        final at = (a['received_ts'] ?? a['ts'] ?? '').toString();
        final bt = (b['received_ts'] ?? b['ts'] ?? '').toString();
        return bt.compareTo(at);
      });
      if (!mounted) return;
      setState(() {
        _events = all;
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
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    if (_events.isEmpty) {
      return const _Placeholder(text: 'No activity yet');
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
        itemCount: _events.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _ActivityRow(evt: _events[i]),
      ),
    );
  }
}

class _ActivityRow extends StatelessWidget {
  final Map<String, dynamic> evt;
  const _ActivityRow({required this.evt});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final channel = (evt['channel_name'] ?? '').toString();
    final from = (evt['from_id'] ?? '').toString();
    final ts = (evt['ts'] ?? evt['received_ts'] ?? '').toString();
    final preview = _preview((evt['parts'] as List?) ?? const []);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
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
              if (channel.isNotEmpty)
                Text('#$channel',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.primary,
                    )),
              if (from.isNotEmpty) ...[
                const SizedBox(width: 6),
                Text(from,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    )),
              ],
              const Spacer(),
              Text(_shortTs(ts),
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 9,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight)),
            ],
          ),
          const SizedBox(height: 4),
          Text(preview,
              style:
                  GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.3)),
        ],
      ),
    );
  }

  String _preview(List<dynamic> parts) {
    for (final raw in parts) {
      if (raw is! Map) continue;
      if (raw['kind'] == 'text' && raw['text'] is String) {
        final t = (raw['text'] as String).trim();
        if (t.isNotEmpty) return t;
      }
    }
    return '(no text)';
  }

  String _shortTs(String raw) {
    if (raw.isEmpty) return '';
    final t = DateTime.tryParse(raw);
    if (t == null) return raw;
    final diff = DateTime.now().difference(t);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}

// ---- Channels ----

class _ChannelsView extends ConsumerStatefulWidget {
  final String projectId;
  const _ChannelsView({required this.projectId});

  @override
  ConsumerState<_ChannelsView> createState() => _ChannelsViewState();
}

class _ChannelsViewState extends ConsumerState<_ChannelsView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _channels = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listChannels(widget.projectId);
      if (!mounted) return;
      setState(() {
        _channels = rows;
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

  Future<void> _create() async {
    final created = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      builder: (_) =>
          ProjectChannelCreateSheet(projectId: widget.projectId),
    );
    if (created == null || !mounted) return;
    setState(() => _channels = [..._channels, created]);
  }

  void _open(Map<String, dynamic> row) {
    final id = (row['id'] ?? '').toString();
    final name = (row['name'] ?? id).toString();
    if (id.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ProjectChannelScreen(
          projectId: widget.projectId,
          channelId: id,
          channelName: name,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    final body = _channels.isEmpty
        ? const _Placeholder(text: 'No channels yet — tap + to create')
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
              itemCount: _channels.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _ChannelRow(
                row: _channels[i],
                onTap: () => _open(_channels[i]),
              ),
            ),
          );
    return Stack(
      children: [
        body,
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.extended(
            heroTag: 'project-channel-fab-${widget.projectId}',
            onPressed: _create,
            icon: const Icon(Icons.add),
            label: const Text('Channel'),
          ),
        ),
      ],
    );
  }
}

class _ChannelRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _ChannelRow({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final name = (row['name'] ?? '').toString();
    final id = (row['id'] ?? '').toString();
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.surfaceDark
              : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag, size: 18, color: DesignColors.primary),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    name.isEmpty ? '(unnamed)' : name,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (id.isNotEmpty)
                    Text(
                      id,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, size: 18),
          ],
        ),
      ),
    );
  }
}

// ---- Tasks ----

class _TasksView extends ConsumerStatefulWidget {
  final String projectId;
  const _TasksView({required this.projectId});

  @override
  ConsumerState<_TasksView> createState() => _TasksViewState();
}

class _TasksViewState extends ConsumerState<_TasksView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _tasks = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listTasks(widget.projectId);
      if (!mounted) return;
      setState(() {
        _tasks = rows;
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

  Future<void> _openCreate() async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ProjectTaskCreateSheet(projectId: widget.projectId),
    );
    if (created == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    final body = _tasks.isEmpty
        ? const _Placeholder(text: 'No tasks yet — tap + to create')
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
              itemCount: _tasks.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _TaskTile(
                task: _tasks[i],
                projectId: widget.projectId,
                onChanged: _load,
              ),
            ),
          );
    return Stack(
      children: [
        Positioned.fill(child: body),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.small(
            heroTag: 'project-tasks-fab',
            onPressed: _openCreate,
            child: const Icon(Icons.add),
          ),
        ),
      ],
    );
  }
}

class _TaskTile extends StatelessWidget {
  final Map<String, dynamic> task;
  final String projectId;
  final VoidCallback onChanged;
  const _TaskTile({
    required this.task,
    required this.projectId,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final title = (task['title'] ?? '?').toString();
    final status = (task['status'] ?? '').toString();
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () async {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => TaskDetailScreen(
            projectId: projectId,
            taskId: (task['id'] ?? '').toString(),
            initial: task,
          ),
        ));
        onChanged();
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            _StatusDot(status: status),
            const SizedBox(width: 10),
            Expanded(
              child: Text(title,
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 13, fontWeight: FontWeight.w600)),
            ),
            Text(status,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight)),
          ],
        ),
      ),
    );
  }
}

class _StatusDot extends StatelessWidget {
  final String status;
  const _StatusDot({required this.status});
  @override
  Widget build(BuildContext context) {
    Color c;
    switch (status) {
      case 'done':
        c = Colors.green;
        break;
      case 'in_progress':
        c = Colors.orange;
        break;
      case 'blocked':
        c = DesignColors.error;
        break;
      default:
        c = DesignColors.primary;
    }
    return Container(
      width: 10,
      height: 10,
      decoration: BoxDecoration(
        color: c,
        shape: BoxShape.circle,
      ),
    );
  }
}

// ---- Agents (filtered to this project) ----

class _AgentsView extends ConsumerWidget {
  final String projectId;
  const _AgentsView({required this.projectId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final all = ref.watch(hubProvider).value?.agents ?? const [];
    final rows = all
        .where((a) => (a['project_id'] ?? '').toString() == projectId)
        .toList();
    if (rows.isEmpty) {
      return const _Placeholder(text: 'No agents on this project');
    }
    return ListView.separated(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      itemCount: rows.length,
      separatorBuilder: (_, __) => const SizedBox(height: 8),
      itemBuilder: (_, i) {
        final a = rows[i];
        final isDark = Theme.of(context).brightness == Brightness.dark;
        return Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: isDark
                ? DesignColors.surfaceDark
                : DesignColors.surfaceLight,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
            ),
          ),
          child: Row(
            children: [
              const Icon(Icons.smart_toy_outlined,
                  size: 18, color: DesignColors.primary),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  (a['handle'] ?? a['id'] ?? '?').toString(),
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 13, fontWeight: FontWeight.w600),
                ),
              ),
              Text((a['status'] ?? '').toString(),
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight)),
            ],
          ),
        );
      },
    );
  }
}

// ---- Info ----

class _InfoView extends ConsumerWidget {
  final Map<String, dynamic> project;
  const _InfoView({required this.project});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final rows = <MapEntry<String, String>>[
      MapEntry('Name', (project['name'] ?? '').toString()),
      MapEntry('Kind', (project['kind'] ?? 'goal').toString()),
      MapEntry('Status', (project['status'] ?? '').toString()),
      if ((project['goal'] ?? '').toString().isNotEmpty)
        MapEntry('Goal', (project['goal'] ?? '').toString()),
      if ((project['template_id'] ?? '').toString().isNotEmpty)
        MapEntry('Steward template', (project['template_id'] ?? '').toString()),
      if ((project['on_create_template_id'] ?? '').toString().isNotEmpty)
        MapEntry('On-create template',
            (project['on_create_template_id'] ?? '').toString()),
      MapEntry('ID', (project['id'] ?? '').toString()),
      MapEntry('Docs root', (project['docs_root'] ?? '').toString()),
      MapEntry('Created', (project['created_at'] ?? '').toString()),
    ];
    final projectId = (project['id'] ?? '').toString();
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      children: [
        if (projectId.isNotEmpty) ...[
          _ShortcutTile(
            icon: Icons.science_outlined,
            label: 'Runs',
            sub: 'Experiment runs in this project',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => RunsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.article_outlined,
            label: 'Writeups',
            sub: 'Memos, drafts, reports (§6.7)',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => DocumentsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.rate_review_outlined,
            label: 'Reviews',
            sub: 'Pending human decisions on this project',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ReviewsScreen(projectId: projectId),
            )),
          ),
          const SizedBox(height: 16),
          const Divider(height: 1),
          const SizedBox(height: 16),
        ],
        for (final r in rows)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(r.key,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.5,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    )),
                const SizedBox(height: 2),
                Text(r.value.isEmpty ? '—' : r.value,
                    style: GoogleFonts.spaceGrotesk(fontSize: 13)),
              ],
            ),
          ),
        const SizedBox(height: 16),
        OutlinedButton.icon(
          icon: const Icon(Icons.archive_outlined),
          label: const Text('Archive project'),
          style: OutlinedButton.styleFrom(foregroundColor: DesignColors.error),
          onPressed: () => _archive(context, ref),
        ),
      ],
    );
  }

  Future<void> _archive(BuildContext context, WidgetRef ref) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Archive project'),
        content: Text(
          'Archive "${project['name']}"? The project will be hidden from lists '
          'but data is preserved.',
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.archiveProject((project['id'] ?? '').toString());
      await ref.read(hubProvider.notifier).refreshAll();
      if (context.mounted) Navigator.of(context).pop();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Archive failed: $e')),
        );
      }
    }
  }
}

/// Compact navigation tile surfaced at the top of the Info tab. Routes
/// to a screen scoped to this project via its `projectId` param.
class _ShortcutTile extends StatelessWidget {
  final IconData icon;
  final String label;
  final String sub;
  final VoidCallback onTap;
  const _ShortcutTile({
    required this.icon,
    required this.label,
    required this.sub,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Icon(icon, color: DesignColors.primary),
      title: Text(
        label,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 14,
          fontWeight: FontWeight.w700,
        ),
      ),
      subtitle: Text(
        sub,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.textMuted,
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: onTap,
    );
  }
}

class _ProjectKindChip extends StatelessWidget {
  final String kind;
  const _ProjectKindChip({required this.kind});

  @override
  Widget build(BuildContext context) {
    final color = switch (kind) {
      'goal' => DesignColors.terminalCyan,
      'standing' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        kind,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _Placeholder extends StatelessWidget {
  final String text;
  const _Placeholder({required this.text});
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(text,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            )),
      ),
    );
  }
}
