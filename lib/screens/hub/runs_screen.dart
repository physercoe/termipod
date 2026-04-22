import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Experiment runs browser (blueprint §6.5).
///
/// Runs are the unit of work for training, evaluation, and notebooks.
/// Hub stores metadata + status + links to external dashboards
/// (tensorboard, wandb/trackio). Bytes and live curves stay on-host;
/// this screen lists, filters, and launches the external viewer.
class RunsScreen extends ConsumerStatefulWidget {
  final String? projectId;
  const RunsScreen({super.key, this.projectId});

  @override
  ConsumerState<RunsScreen> createState() => _RunsScreenState();
}

class _RunsScreenState extends ConsumerState<RunsScreen> {
  String? _status;
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;

  static const _statuses = <String?>[
    null,
    'running',
    'succeeded',
    'failed',
    'cancelled',
  ];

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
      final rows = await client.listRuns(
        projectId: widget.projectId,
        status: _status,
      );
      rows.sort((a, b) => (b['created_at'] ?? '')
          .toString()
          .compareTo((a['created_at'] ?? '').toString()));
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
        ],
      ),
      body: Column(
        children: [
          _StatusBar(
            statuses: _statuses,
            selected: _status,
            onChanged: (v) {
              setState(() => _status = v);
              _load();
            },
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
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _status == null
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

class _StatusBar extends StatelessWidget {
  final List<String?> statuses;
  final String? selected;
  final ValueChanged<String?> onChanged;
  const _StatusBar({
    required this.statuses,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Row(
        children: [
          for (final s in statuses) ...[
            _Pill(
              label: s ?? 'all',
              selected: s == selected,
              onTap: () => onChanged(s),
            ),
            const SizedBox(width: 6),
          ],
        ],
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
  bool _loading = true;
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
      if (!mounted) return;
      setState(() {
        _run = r;
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
    final status = (r['status'] ?? '').toString();
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
              child: SelectableText(
                summary,
                style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
              ),
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
