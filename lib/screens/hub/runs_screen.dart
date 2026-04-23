import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'run_create_sheet.dart';

/// Experiment runs browser (blueprint §6.5).
///
/// Runs are the unit of work for training, evaluation, and notebooks.
/// Hub stores metadata + status + links to external dashboards
/// (tensorboard, wandb/trackio). Bytes and live curves stay on-host;
/// this screen lists, filters, and launches the external viewer.
///
/// The project filter only appears in the global listing (when no
/// [projectId] is passed); when scoped to a specific project the caller
/// has already narrowed the set, so an in-screen project filter would
/// just be noise.
class RunsScreen extends ConsumerStatefulWidget {
  final String? projectId;
  const RunsScreen({super.key, this.projectId});

  @override
  ConsumerState<RunsScreen> createState() => _RunsScreenState();
}

class _RunsScreenState extends ConsumerState<RunsScreen> {
  String? _status;
  String? _projectFilter;
  List<Map<String, dynamic>>? _rows;
  List<Map<String, dynamic>>? _projects;
  bool _loading = true;
  String? _error;

  static const _statuses = <String?>[
    null,
    'running',
    'succeeded',
    'failed',
    'cancelled',
  ];

  bool get _showProjectFilter => widget.projectId == null;

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
      final effectiveProject = widget.projectId ?? _projectFilter;
      final results = await Future.wait([
        client.listRuns(
          projectId: effectiveProject,
          status: _status,
        ),
        if (_showProjectFilter && _projects == null) client.listProjects(),
      ]);
      final rows = results[0];
      if (_showProjectFilter && _projects == null && results.length > 1) {
        _projects = results[1];
      }
      // Sort by status band first — running at top (what's live now),
      // then succeeded, then failed, then everything else. Within each
      // band, newest first. Frames the screen as "monitor active runs"
      // rather than a chronological feed.
      int rank(String s) => switch (s) {
            'running' => 0,
            'succeeded' => 1,
            'failed' => 2,
            'cancelled' => 3,
            _ => 4,
          };
      rows.sort((a, b) {
        final ra = rank((a['status'] ?? '').toString());
        final rb = rank((b['status'] ?? '').toString());
        if (ra != rb) return ra.compareTo(rb);
        return (b['created_at'] ?? '')
            .toString()
            .compareTo((a['created_at'] ?? '').toString());
      });
      if (!mounted) return;
      setState(() {
        _rows = rows;
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

  Future<void> _pickProject() async {
    final projects = _projects ?? const [];
    final picked = await showModalBottomSheet<_ProjectPick>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => _ProjectFilterSheet(
        projects: projects,
        selectedId: _projectFilter,
      ),
    );
    if (picked == null) return;
    setState(() => _projectFilter = picked.clear ? null : picked.id);
    _load();
  }

  String _projectFilterLabel() {
    final id = _projectFilter;
    if (id == null) return 'All projects';
    for (final p in _projects ?? const <Map<String, dynamic>>[]) {
      if ((p['id'] ?? '').toString() == id) {
        return (p['name'] ?? id).toString();
      }
    }
    return id;
  }

  Future<void> _createRun() async {
    final created = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => RunCreateSheet(
        projectId: widget.projectId ?? _projectFilter,
      ),
    );
    if (!mounted || created == null) return;
    await _load();
    final runId = (created['id'] ?? '').toString();
    if (!mounted || runId.isEmpty) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => RunDetailScreen(runId: runId, summary: created),
    ));
    if (mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.projectId == null ? 'Runs' : 'Runs · ${widget.projectId}',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _loading ? null : _load,
          ),
          PopupMenuButton<String>(
            tooltip: 'More',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'new') _createRun();
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'new',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.add, size: 20),
                  title: Text('New run'),
                ),
              ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          _FilterBar(
            statuses: _statuses,
            statusSelected: _status,
            onStatusSelected: (v) {
              if (_status == v) return;
              setState(() => _status = v);
              _load();
            },
            showProjectFilter: _showProjectFilter,
            projectLabel: _projectFilterLabel(),
            projectIsActive: _projectFilter != null,
            onProjectTap: _pickProject,
          ),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      final filtered = _status != null || _projectFilter != null;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            filtered
                ? 'No runs match the current filters.'
                : _status == null
                    ? 'No runs yet.'
                    : 'No $_status runs.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 4),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) => _RunRow(
          row: rows[i],
          onTap: () async {
            await Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => RunDetailScreen(
                runId: (rows[i]['id'] ?? '').toString(),
                summary: rows[i],
              ),
            ));
            if (mounted) _load();
          },
        ),
      ),
    );
  }
}

class _FilterBar extends StatelessWidget {
  final List<String?> statuses;
  final String? statusSelected;
  final ValueChanged<String?> onStatusSelected;
  final bool showProjectFilter;
  final String projectLabel;
  final bool projectIsActive;
  final VoidCallback onProjectTap;
  const _FilterBar({
    required this.statuses,
    required this.statusSelected,
    required this.onStatusSelected,
    required this.showProjectFilter,
    required this.projectLabel,
    required this.projectIsActive,
    required this.onProjectTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final s in statuses) ...[
                  _StatusChip(
                    label: s ?? 'all',
                    selected: statusSelected == s,
                    onTap: () => onStatusSelected(s),
                  ),
                  const SizedBox(width: 6),
                ],
              ],
            ),
          ),
          if (showProjectFilter) ...[
            const SizedBox(height: 6),
            InkWell(
              onTap: onProjectTap,
              borderRadius: BorderRadius.circular(6),
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                decoration: BoxDecoration(
                  border: Border.all(
                    color: projectIsActive ? DesignColors.primary : border,
                  ),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Row(
                  children: [
                    Icon(
                      projectIsActive
                          ? Icons.filter_alt
                          : Icons.filter_alt_outlined,
                      size: 14,
                      color: projectIsActive
                          ? DesignColors.primary
                          : DesignColors.textMuted,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        projectLabel,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                          color: projectIsActive
                              ? DesignColors.primary
                              : DesignColors.textMuted,
                        ),
                      ),
                    ),
                    const Icon(Icons.arrow_drop_down,
                        size: 16, color: DesignColors.textMuted),
                  ],
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _StatusChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: selected ? DesignColors.primary : DesignColors.borderDark,
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _ProjectPick {
  final String? id;
  final bool clear;
  const _ProjectPick({this.id, this.clear = false});
}

class _ProjectFilterSheet extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  const _ProjectFilterSheet({
    required this.projects,
    required this.selectedId,
  });

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 16),
        child: ListView.separated(
          controller: scroll,
          itemCount: projects.length + 1,
          separatorBuilder: (_, __) => const Divider(height: 1),
          itemBuilder: (_, i) {
            if (i == 0) {
              return ListTile(
                leading: const Icon(Icons.clear, size: 18),
                title: Text(
                  'All projects',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                selected: selectedId == null,
                onTap: () =>
                    Navigator.of(context).pop(const _ProjectPick(clear: true)),
              );
            }
            final p = projects[i - 1];
            final id = (p['id'] ?? '').toString();
            final name = (p['name'] ?? id).toString();
            final kind = (p['kind'] ?? '').toString();
            return ListTile(
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                [if (kind.isNotEmpty) kind, id].join(' · '),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
              selected: selectedId == id,
              onTap: () => Navigator.of(context).pop(_ProjectPick(id: id)),
            );
          },
        ),
      ),
    );
  }
}

class _RunRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _RunRow({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final status = (row['status'] ?? '').toString();
    final name = (row['name'] ?? row['id'] ?? '(run)').toString();
    final kind = (row['kind'] ?? '').toString();
    final project = (row['project_id'] ?? '').toString();
    final created = (row['created_at'] ?? '').toString();
    return ListTile(
      onTap: onTap,
      title: Row(
        children: [
          RunStatusChip(status: status),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              name,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (kind.isNotEmpty)
            Text(
              kind,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.textMuted,
              ),
            ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          [
            if (project.isNotEmpty) project,
            if (created.isNotEmpty) created,
          ].join(' · '),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
    );
  }
}

class RunStatusChip extends StatelessWidget {
  final String status;
  const RunStatusChip({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    final s = status.toLowerCase();
    final color = switch (s) {
      'running' => DesignColors.terminalBlue,
      'succeeded' || 'success' || 'completed' => DesignColors.success,
      'failed' || 'error' => DesignColors.error,
      'cancelled' || 'canceled' => DesignColors.textMuted,
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
        s.isEmpty ? '?' : s,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

/// Per-run detail view: meta + metric links + summary + metadata JSON.
/// Metric URIs launch via url_launcher since the dashboard lives outside
/// the app (tensorboard/wandb/trackio).
class RunDetailScreen extends ConsumerStatefulWidget {
  final String runId;
  final Map<String, dynamic>? summary;
  const RunDetailScreen({
    super.key,
    required this.runId,
    this.summary,
  });

  @override
  ConsumerState<RunDetailScreen> createState() => _RunDetailScreenState();
}

class _RunDetailScreenState extends ConsumerState<RunDetailScreen> {
  Map<String, dynamic>? _run;
  List<Map<String, dynamic>> _metrics = const [];
  List<Map<String, dynamic>> _images = const [];
  bool _loading = true;
  bool _busy = false;
  String? _error;

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
      final r = await client.getRun(widget.runId);
      // Metric digests + image-panel entries are optional — a run without
      // an attached tracker (or before the poller's first tick) simply has
      // no rows yet. Fetch both in parallel; failures fall back to empty.
      List<Map<String, dynamic>> metrics = const [];
      List<Map<String, dynamic>> images = const [];
      try {
        final results = await Future.wait<List<Map<String, dynamic>>>([
          client.getRunMetrics(widget.runId),
          client.getRunImages(widget.runId),
        ]);
        metrics = results[0];
        images = results[1];
      } catch (_) {
        // Keep defaults; render the rest of the screen even if digests fail.
      }
      if (!mounted) return;
      setState(() {
        _run = r;
        _metrics = metrics;
        _images = images;
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

  Future<void> _launch(String uri) async {
    final u = Uri.tryParse(uri);
    if (u == null) return;
    final ok = await launchUrl(u, mode: LaunchMode.externalApplication);
    if (!ok && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not open $uri')),
      );
    }
  }

  Future<void> _complete() async {
    final result = await showDialog<_CompletePayload>(
      context: context,
      builder: (_) => const _CompleteRunDialog(),
    );
    if (result == null || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.completeRun(
        widget.runId,
        status: result.status,
        summary: result.summary.isEmpty ? null : result.summary,
      );
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Run → ${result.status}')));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Complete failed: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _attachMetric() async {
    final result = await showDialog<_MetricPayload>(
      context: context,
      builder: (_) => const _AttachMetricDialog(),
    );
    if (result == null || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.attachRunMetricURI(
        widget.runId,
        kind: result.kind,
        uri: result.uri,
      );
      if (!mounted) return;
      messenger.showSnackBar(const SnackBar(content: Text('Dashboard attached')));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Attach failed: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  List<Map<String, dynamic>> _metricUris() {
    final r = _run ?? widget.summary ?? const <String, dynamic>{};
    final raw = r['metric_uris'];
    if (raw is List) {
      return raw
          .whereType<Map>()
          .map((m) => m.cast<String, dynamic>())
          .toList(growable: false);
    }
    return const [];
  }

  @override
  Widget build(BuildContext context) {
    final r = _run ?? widget.summary ?? const <String, dynamic>{};
    final name = (r['name'] ?? r['id'] ?? '(run)').toString();
    final status = (r['status'] ?? '').toString().toLowerCase();
    final terminal = status == 'succeeded' ||
        status == 'failed' ||
        status == 'cancelled';
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            RunStatusChip(status: status),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                name,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 15,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'Attach dashboard',
            icon: const Icon(Icons.link),
            onPressed: _busy ? null : _attachMetric,
          ),
          if (!terminal)
            IconButton(
              tooltip: 'Mark complete',
              icon: const Icon(Icons.flag_outlined),
              onPressed: _busy ? null : _complete,
            ),
        ],
      ),
      body: _body(r),
    );
  }

  Widget _body(Map<String, dynamic> r) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final kind = (r['kind'] ?? '').toString();
    final project = (r['project_id'] ?? '').toString();
    final agent = (r['agent_id'] ?? '').toString();
    final parent = (r['parent_run_id'] ?? '').toString();
    final created = (r['created_at'] ?? '').toString();
    final completed = (r['completed_at'] ?? '').toString();
    final duration = _runDuration(created, completed);
    final summary = (r['summary'] ?? '').toString();
    final meta = r['metadata_json'];
    final uris = _metricUris();

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          if (kind.isNotEmpty) _metaRow('kind', kind),
          if (project.isNotEmpty) _metaRow('project', project),
          if (agent.isNotEmpty) _metaRow('agent', agent),
          if (parent.isNotEmpty) _metaRow('parent run', parent),
          if (created.isNotEmpty) _metaRow('started', created),
          if (completed.isNotEmpty) _metaRow('completed', completed),
          if (duration.isNotEmpty) _metaRow('duration', duration),
          if (summary.isNotEmpty) ...[
            const SizedBox(height: 12),
            _sectionLabel('Summary'),
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: 0.25),
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: DesignColors.borderDark),
              ),
              child: _SummaryBody(summary: summary),
            ),
          ],
          if (_metrics.isNotEmpty) ...[
            const SizedBox(height: 16),
            _sectionLabel('Metrics'),
            for (final g in _groupMetrics(_metrics))
              if (g.rows.length == 1)
                _MetricSparklineTile(row: g.rows.single)
              else
                _MetricGroupTile(groupName: g.name, rows: g.rows),
          ],
          if (_images.isNotEmpty) ...[
            const SizedBox(height: 16),
            _sectionLabel('Images'),
            for (final group in _groupImages(_images))
              _ImageSeriesTile(
                groupName: group.name,
                rows: group.rows,
                runId: widget.runId,
              ),
          ],
          if (uris.isNotEmpty) ...[
            const SizedBox(height: 16),
            _sectionLabel('Metric dashboards'),
            for (final u in uris) _MetricURITile(row: u, onLaunch: _launch),
          ],
          if (meta is Map && meta.isNotEmpty) ...[
            const SizedBox(height: 16),
            _sectionLabel('Metadata'),
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: 0.25),
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: DesignColors.borderDark),
              ),
              child: SelectableText(
                const JsonEncoder.withIndent('  ').convert(meta),
                style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
              ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _metaRow(String k, String v) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 90,
              child: Text(
                k,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            Expanded(
              child: SelectableText(
                v,
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
          ],
        ),
      );

  Widget _sectionLabel(String label) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: DesignColors.textMuted,
            letterSpacing: 0.5,
          ),
        ),
      );
}

/// Renders a run summary as markdown when it contains common markdown
/// markers; otherwise falls back to selectable mono text so short
/// one-liners don't get paragraph-wrapped into oblivion. Same heuristic
/// as task bodies keeps the two surfaces consistent.
class _SummaryBody extends StatelessWidget {
  final String summary;
  const _SummaryBody({required this.summary});

  @override
  Widget build(BuildContext context) {
    final looksMd =
        RegExp(r'(^|\n)(#|- |\* |\d+\. |```|> )').hasMatch(summary);
    if (looksMd) {
      return MarkdownBody(
        data: summary,
        selectable: true,
        styleSheet: MarkdownStyleSheet(
          p: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
          code: GoogleFonts.jetBrainsMono(fontSize: 12),
          h1: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
          h2: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
          h3: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
        ),
      );
    }
    return SelectableText(
      summary,
      style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
    );
  }
}

/// Builds a human-readable duration string from the run's created_at
/// (required) and completed_at (optional). Running runs show elapsed
/// time since start; completed runs show total elapsed. Returns an
/// empty string if the timestamps don't parse — the caller omits the
/// row in that case rather than showing gibberish.
String _runDuration(String createdIso, String completedIso) {
  final start = DateTime.tryParse(createdIso);
  if (start == null) return '';
  final end =
      completedIso.isEmpty ? DateTime.now() : DateTime.tryParse(completedIso);
  if (end == null) return '';
  final diff = end.difference(start);
  final running = completedIso.isEmpty;
  final body = _fmtDurationMs(diff.inMilliseconds);
  return running ? 'running for $body' : body;
}

String _fmtDurationMs(int ms) {
  if (ms < 1000) return '${ms}ms';
  final s = ms ~/ 1000;
  if (s < 60) {
    final tenths = (ms % 1000) ~/ 100;
    return tenths == 0 ? '${s}s' : '$s.${tenths}s';
  }
  if (s < 3600) return '${s ~/ 60}m ${s % 60}s';
  final h = s ~/ 3600;
  final m = (s % 3600) ~/ 60;
  return '${h}h ${m}m';
}

/// Groups metric rows that share a "<group>/<series>" prefix so the UI
/// can overlay series that belong on the same axis (e.g. loss/train +
/// loss/val). Rows whose name has no slash live in a group of their
/// own, keyed by the full name. Group order follows first appearance
/// in [_metrics] so the caller controls layout.
class _MetricGroup {
  final String name;
  final List<Map<String, dynamic>> rows;
  _MetricGroup(this.name, this.rows);
}

List<_MetricGroup> _groupMetrics(List<Map<String, dynamic>> rows) {
  final order = <String>[];
  final byKey = <String, List<Map<String, dynamic>>>{};
  for (final r in rows) {
    final name = (r['name'] ?? '').toString();
    final slash = name.indexOf('/');
    final key = slash > 0 ? name.substring(0, slash) : name;
    if (!byKey.containsKey(key)) {
      order.add(key);
      byKey[key] = [];
    }
    byKey[key]!.add(r);
  }
  return [for (final k in order) _MetricGroup(k, byKey[k]!)];
}

// Distinct colors cycled per series within a group — kept small so
// legends stay legible.
const _seriesPalette = <Color>[
  DesignColors.terminalCyan,
  DesignColors.warning,
  DesignColors.success,
  DesignColors.error,
  Color(0xFFB394FF),
];

/// Renders a metric group as a single multi-line chart with a legend.
/// Series share one y-axis — caller is expected to emit metrics whose
/// units already align (loss/{train,val}). Mixed-unit groups should
/// use distinct top-level group names.
class _MetricGroupTile extends StatelessWidget {
  final String groupName;
  final List<Map<String, dynamic>> rows;
  const _MetricGroupTile({required this.groupName, required this.rows});

  @override
  Widget build(BuildContext context) {
    // Parse once; skip series that have too few points to draw.
    final series = <(_SeriesMeta, List<(double, double)>)>[];
    for (var i = 0; i < rows.length; i++) {
      final r = rows[i];
      final name = (r['name'] ?? '').toString();
      final slash = name.indexOf('/');
      final label = slash > 0 ? name.substring(slash + 1) : name;
      final pts = _MetricSparklineTile._parsePoints(r['points']);
      final lastValue = (r['last_value'] as num?)?.toDouble() ??
          (pts.isEmpty ? null : pts.last.$2);
      final color = _seriesPalette[i % _seriesPalette.length];
      series.add((
        _SeriesMeta(label: label, color: color, lastValue: lastValue),
        pts,
      ));
    }
    final drawable = [
      for (final s in series)
        if (s.$2.length >= 2) s,
    ];
    final sampleCount = rows.fold<int>(
      0,
      (a, r) => a + ((r['sample_count'] as num?)?.toInt() ?? 0),
    );
    final lastStep = rows
        .map((r) => (r['last_step'] as num?)?.toInt() ?? 0)
        .fold<int>(0, (a, b) => b > a ? b : a);

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            groupName,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          SizedBox(
            height: 56,
            child: drawable.isEmpty
                ? Align(
                    alignment: Alignment.centerLeft,
                    child: Text(
                      'no samples yet',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  )
                : CustomPaint(
                    size: const Size.fromHeight(56),
                    painter: _MultiSparklinePainter([
                      for (final d in drawable) (d.$1.color, d.$2),
                    ]),
                  ),
          ),
          const SizedBox(height: 4),
          Wrap(
            spacing: 12,
            runSpacing: 2,
            children: [
              for (final s in series) _legendChip(s.$1),
            ],
          ),
          const SizedBox(height: 2),
          Text(
            lastStep > 0
                ? 'step $lastStep · $sampleCount samples'
                : '$sampleCount samples',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }

  Widget _legendChip(_SeriesMeta s) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 8,
          height: 8,
          decoration: BoxDecoration(
            color: s.color,
            borderRadius: BorderRadius.circular(2),
          ),
        ),
        const SizedBox(width: 4),
        Text(
          s.label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
        if (s.lastValue != null) ...[
          const SizedBox(width: 4),
          Text(
            _MetricSparklineTile._fmtValue(s.lastValue!),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              color: s.color,
            ),
          ),
        ],
      ],
    );
  }
}

class _SeriesMeta {
  final String label;
  final Color color;
  final double? lastValue;
  _SeriesMeta({required this.label, required this.color, this.lastValue});
}

/// Draws N series on a shared axis with per-series colors. All series
/// share x and y ranges derived from the union so relative shape is
/// comparable.
class _MultiSparklinePainter extends CustomPainter {
  final List<(Color, List<(double, double)>)> series;
  _MultiSparklinePainter(this.series);

  @override
  void paint(Canvas canvas, Size size) {
    if (series.isEmpty) return;
    double? minX, maxX, minY, maxY;
    for (final s in series) {
      for (final p in s.$2) {
        minX = (minX == null || p.$1 < minX) ? p.$1 : minX;
        maxX = (maxX == null || p.$1 > maxX) ? p.$1 : maxX;
        minY = (minY == null || p.$2 < minY) ? p.$2 : minY;
        maxY = (maxY == null || p.$2 > maxY) ? p.$2 : maxY;
      }
    }
    if (minX == null) return;
    final dx = (maxX! - minX).abs() < 1e-12 ? 1.0 : (maxX - minX);
    final dy = (maxY! - minY!).abs() < 1e-12 ? 1.0 : (maxY - minY);

    for (final s in series) {
      final color = s.$1;
      final points = s.$2;
      if (points.length < 2) continue;
      final path = Path();
      for (var i = 0; i < points.length; i++) {
        final x = (points[i].$1 - minX) / dx * size.width;
        final y = size.height - ((points[i].$2 - minY) / dy * size.height);
        if (i == 0) {
          path.moveTo(x, y);
        } else {
          path.lineTo(x, y);
        }
      }
      final line = Paint()
        ..color = color
        ..strokeWidth = 1.4
        ..style = PaintingStyle.stroke
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round;
      canvas.drawPath(path, line);

      // Endpoint marker on each series.
      final last = points.last;
      final lx = (last.$1 - minX) / dx * size.width;
      final ly = size.height - ((last.$2 - minY) / dy * size.height);
      canvas.drawCircle(Offset(lx, ly), 2.2, Paint()..color = color);
    }
  }

  @override
  bool shouldRepaint(covariant _MultiSparklinePainter old) =>
      old.series != series;
}

/// Renders one metric digest row as a compact sparkline with headline
/// value. The digest is already downsampled on the host-runner side
/// (≤100 points by default); we just paint what arrives. Blueprint §4
/// keeps bulk time-series off the hub, so this surface is the digest,
/// not a full chart — users chase detail via the "Metric dashboards"
/// link below.
class _MetricSparklineTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _MetricSparklineTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '').toString();
    final points = _parsePoints(row['points']);
    final sampleCount = (row['sample_count'] as num?)?.toInt() ?? points.length;
    final lastStep = (row['last_step'] as num?)?.toInt();
    final lastValue = (row['last_value'] as num?)?.toDouble() ??
        (points.isEmpty ? null : points.last.$2);

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  name,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (lastValue != null)
                Text(
                  _fmtValue(lastValue),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: DesignColors.terminalCyan,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 4),
          SizedBox(
            height: 36,
            child: points.length < 2
                ? Align(
                    alignment: Alignment.centerLeft,
                    child: Text(
                      points.isEmpty
                          ? 'no samples yet'
                          : 'single sample — need ≥2 for a line',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  )
                : CustomPaint(
                    size: const Size.fromHeight(36),
                    painter: _SparklinePainter(points),
                  ),
          ),
          const SizedBox(height: 2),
          Text(
            lastStep != null
                ? 'step $lastStep · $sampleCount samples'
                : '$sampleCount samples',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }

  // Wire shape: [[step, value], ...] as json.RawMessage → List<dynamic>.
  static List<(double, double)> _parsePoints(dynamic raw) {
    if (raw is! List) return const [];
    final out = <(double, double)>[];
    for (final p in raw) {
      if (p is! List || p.length < 2) continue;
      final s = (p[0] as num?)?.toDouble();
      final v = (p[1] as num?)?.toDouble();
      if (s == null || v == null) continue;
      out.add((s, v));
    }
    return out;
  }

  static String _fmtValue(double v) {
    final abs = v.abs();
    if (abs != 0 && (abs < 0.01 || abs >= 10000)) {
      return v.toStringAsExponential(3);
    }
    return v.toStringAsFixed(4);
  }
}

class _SparklinePainter extends CustomPainter {
  final List<(double, double)> points;
  _SparklinePainter(this.points);

  @override
  void paint(Canvas canvas, Size size) {
    if (points.length < 2) return;

    double minX = points.first.$1, maxX = points.first.$1;
    double minY = points.first.$2, maxY = points.first.$2;
    for (final p in points) {
      if (p.$1 < minX) minX = p.$1;
      if (p.$1 > maxX) maxX = p.$1;
      if (p.$2 < minY) minY = p.$2;
      if (p.$2 > maxY) maxY = p.$2;
    }
    // Avoid div-by-zero on constant series or single-step spans.
    final dx = (maxX - minX).abs() < 1e-12 ? 1.0 : (maxX - minX);
    final dy = (maxY - minY).abs() < 1e-12 ? 1.0 : (maxY - minY);

    final path = Path();
    for (var i = 0; i < points.length; i++) {
      final x = (points[i].$1 - minX) / dx * size.width;
      // Flip Y — canvas grows downward but values grow upward.
      final y = size.height - ((points[i].$2 - minY) / dy * size.height);
      if (i == 0) {
        path.moveTo(x, y);
      } else {
        path.lineTo(x, y);
      }
    }

    final line = Paint()
      ..color = DesignColors.terminalCyan
      ..strokeWidth = 1.4
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    canvas.drawPath(path, line);

    // Endpoint dot to anchor the eye on the current value.
    final last = points.last;
    final lx = (last.$1 - minX) / dx * size.width;
    final ly = size.height - ((last.$2 - minY) / dy * size.height);
    final dot = Paint()..color = DesignColors.terminalCyan;
    canvas.drawCircle(Offset(lx, ly), 2.2, dot);
  }

  @override
  bool shouldRepaint(covariant _SparklinePainter old) =>
      old.points != points;
}

class _MetricURITile extends StatelessWidget {
  final Map<String, dynamic> row;
  final Future<void> Function(String uri) onLaunch;
  const _MetricURITile({required this.row, required this.onLaunch});

  @override
  Widget build(BuildContext context) {
    final kind = (row['kind'] ?? '').toString();
    final uri = (row['uri'] ?? '').toString();
    return ListTile(
      dense: true,
      contentPadding: EdgeInsets.zero,
      leading: Icon(
        _iconForKind(kind),
        size: 18,
        color: DesignColors.terminalCyan,
      ),
      title: Text(
        kind.isEmpty ? 'dashboard' : kind,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: Text(
        uri,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          color: DesignColors.textMuted,
        ),
      ),
      trailing: const Icon(Icons.open_in_new, size: 16),
      onTap: uri.isEmpty ? null : () => onLaunch(uri),
    );
  }

  IconData _iconForKind(String kind) {
    final k = kind.toLowerCase();
    if (k.contains('tensor')) return Icons.insert_chart_outlined;
    if (k.contains('wandb') || k.contains('trackio')) return Icons.timeline;
    return Icons.bar_chart;
  }
}

/// Groups image rows by metric_name so the UI renders one scrubber per
/// image series (e.g. `samples/generations`, `samples/attention`). Order
/// follows first appearance in the input list.
class _ImageGroup {
  final String name;
  final List<Map<String, dynamic>> rows;
  _ImageGroup(this.name, this.rows);
}

List<_ImageGroup> _groupImages(List<Map<String, dynamic>> rows) {
  final order = <String>[];
  final byKey = <String, List<Map<String, dynamic>>>{};
  for (final r in rows) {
    final key = (r['metric_name'] ?? '').toString();
    if (key.isEmpty) continue;
    if (!byKey.containsKey(key)) {
      order.add(key);
      byKey[key] = [];
    }
    byKey[key]!.add(r);
  }
  // Sort each group by step so the slider scrubs in training order.
  for (final k in order) {
    byKey[k]!.sort(
      (a, b) => ((a['step'] as num?)?.toInt() ?? 0)
          .compareTo((b['step'] as num?)?.toInt() ?? 0),
    );
  }
  return [for (final k in order) _ImageGroup(k, byKey[k]!)];
}

/// Renders one image series as a step-scrubbable panel. The Slider
/// selects an index into the sorted rows; the preview below swaps as
/// the index moves. Blob bytes are fetched lazily per step and cached
/// in-memory for the lifetime of this widget (scrubbing back and forth
/// then doesn't re-hit the network).
///
/// Bytes come from `/v1/blobs/{sha}`, so a long-poll disconnect or
/// logged-out client degrades to an error placeholder rather than
/// taking down the whole Run Detail screen.
class _ImageSeriesTile extends ConsumerStatefulWidget {
  final String groupName;
  final List<Map<String, dynamic>> rows;
  final String runId;
  const _ImageSeriesTile({
    required this.groupName,
    required this.rows,
    required this.runId,
  });

  @override
  ConsumerState<_ImageSeriesTile> createState() => _ImageSeriesTileState();
}

class _ImageSeriesTileState extends ConsumerState<_ImageSeriesTile> {
  int _index = 0;
  final Map<String, Uint8List> _cache = {};
  final Map<String, String> _errors = {};
  final Set<String> _loading = {};

  @override
  void initState() {
    super.initState();
    // Default to the last (most-trained) frame — matches what the user
    // usually wants to see first when opening a finished run.
    if (widget.rows.isNotEmpty) {
      _index = widget.rows.length - 1;
      _ensureLoaded(widget.rows[_index]);
    }
  }

  Future<void> _ensureLoaded(Map<String, dynamic> row) async {
    final sha = (row['blob_sha'] ?? '').toString();
    if (sha.isEmpty) return;
    if (_cache.containsKey(sha) || _loading.contains(sha)) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    _loading.add(sha);
    try {
      final bytes = await client.downloadBlob(sha);
      if (!mounted) return;
      setState(() {
        _cache[sha] = Uint8List.fromList(bytes);
        _loading.remove(sha);
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _errors[sha] = '$e';
        _loading.remove(sha);
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (widget.rows.isEmpty) return const SizedBox.shrink();
    final row = widget.rows[_index];
    final sha = (row['blob_sha'] ?? '').toString();
    final step = (row['step'] as num?)?.toInt() ?? 0;
    final caption = (row['caption'] ?? '').toString();
    final bytes = _cache[sha];
    final err = _errors[sha];

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  widget.groupName,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Text(
                'step $step',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.terminalCyan,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          AspectRatio(
            aspectRatio: 1.0,
            child: Container(
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: 0.35),
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: DesignColors.borderDark),
              ),
              clipBehavior: Clip.antiAlias,
              child: _preview(bytes, err),
            ),
          ),
          if (widget.rows.length >= 2) ...[
            const SizedBox(height: 4),
            Slider(
              min: 0,
              max: (widget.rows.length - 1).toDouble(),
              divisions: widget.rows.length - 1,
              value: _index.toDouble(),
              label: 'step $step',
              onChanged: (v) {
                final i = v.round();
                if (i == _index) return;
                setState(() => _index = i);
                _ensureLoaded(widget.rows[i]);
              },
            ),
          ],
          if (caption.isNotEmpty)
            Text(
              caption,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.textMuted,
              ),
            ),
          Text(
            '${widget.rows.length} checkpoints',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }

  Widget _preview(Uint8List? bytes, String? err) {
    if (err != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Text(
            'failed to load: $err',
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.error,
            ),
          ),
        ),
      );
    }
    if (bytes == null) {
      return const Center(
        child: SizedBox(
          width: 24,
          height: 24,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    // filterQuality=none preserves the pixel grid for tiny placeholder
    // PNGs; the seed-demo images are 64×64 so linear filtering would
    // blur them into mush at any realistic display size.
    return Image.memory(
      bytes,
      fit: BoxFit.contain,
      gaplessPlayback: true,
      filterQuality: FilterQuality.none,
    );
  }
}

class _CompletePayload {
  final String status;
  final String summary;
  const _CompletePayload(this.status, this.summary);
}

class _CompleteRunDialog extends StatefulWidget {
  const _CompleteRunDialog();

  @override
  State<_CompleteRunDialog> createState() => _CompleteRunDialogState();
}

class _CompleteRunDialogState extends State<_CompleteRunDialog> {
  String _status = 'succeeded';
  final _summary = TextEditingController();

  static const _options = ['succeeded', 'failed', 'cancelled'];

  @override
  void dispose() {
    _summary.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Complete run'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              for (final s in _options)
                ChoiceChip(
                  label: Text(s),
                  selected: _status == s,
                  onSelected: (_) => setState(() => _status = s),
                ),
            ],
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _summary,
            minLines: 2,
            maxLines: 6,
            style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
            decoration: const InputDecoration(
              labelText: 'Summary (optional)',
              border: OutlineInputBorder(),
              isDense: true,
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(
            _CompletePayload(_status, _summary.text.trim()),
          ),
          child: const Text('Complete'),
        ),
      ],
    );
  }
}

class _MetricPayload {
  final String kind;
  final String uri;
  const _MetricPayload(this.kind, this.uri);
}

class _AttachMetricDialog extends StatefulWidget {
  const _AttachMetricDialog();

  @override
  State<_AttachMetricDialog> createState() => _AttachMetricDialogState();
}

class _AttachMetricDialogState extends State<_AttachMetricDialog> {
  final _kind = TextEditingController(text: 'tensorboard');
  final _uri = TextEditingController();

  @override
  void dispose() {
    _kind.dispose();
    _uri.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Attach dashboard'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: _kind,
            style: GoogleFonts.jetBrainsMono(fontSize: 13),
            decoration: const InputDecoration(
              labelText: 'Kind',
              helperText: 'e.g. tensorboard, wandb, trackio',
              border: OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _uri,
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
            decoration: const InputDecoration(
              labelText: 'URI',
              hintText: 'https://...',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            keyboardType: TextInputType.url,
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            final kind = _kind.text.trim();
            final uri = _uri.text.trim();
            if (kind.isEmpty || uri.isEmpty) return;
            Navigator.of(context).pop(_MetricPayload(kind, uri));
          },
          child: const Text('Attach'),
        ),
      ],
    );
  }
}
