import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/hub/entity_names.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/app_chip.dart';
import '../../widgets/histogram_tile.dart';
import '../../widgets/hub_offline_banner.dart';
import '../../widgets/view_switcher.dart';
import '../sessions/sessions_screen.dart';
import 'artifacts_screen.dart';
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
  bool _hubMissing = false;
  DateTime? _staleSince;

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
        _hubMissing = true;
      });
      return;
    }
    try {
      final effectiveProject = widget.projectId ?? _projectFilter;
      final runsFuture = client.listRunsCached(
        projectId: effectiveProject,
        status: _status,
      );
      final projectsFuture = (_showProjectFilter && _projects == null)
          ? client.listProjects()
          : null;
      final cached = await runsFuture;
      final rows = cached.body;
      _staleSince = cached.staleSince;
      if (projectsFuture != null) {
        _projects = await projectsFuture;
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

  String _projectFilterLabel(String allProjectsLabel) {
    final id = _projectFilter;
    if (id == null) return allProjectsLabel;
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
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final runTerm = vocab.term(VocabAxis.entityRun);
    final projectTerm = vocab.term(VocabAxis.entityProject);
    final projects =
        _projects ?? ref.watch(hubProvider).value?.projects ?? const [];
    final scopeName = widget.projectId == null
        ? null
        : projectNameFor(widget.projectId!, projects);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          scopeName == null
              ? runTerm.plural
              : '${runTerm.plural} · $scopeName',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: l10n.buttonRefresh,
            onPressed: _loading ? null : _load,
          ),
          PopupMenuButton<String>(
            tooltip: l10n.tooltipMore,
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'new') _createRun();
            },
            itemBuilder: (_) => [
              PopupMenuItem(
                value: 'new',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: const Icon(Icons.add, size: 20),
                  title: Text(l10n.newRun(runTerm.lower)),
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
            projectLabel:
                _projectFilterLabel(l10n.allProjects(projectTerm.pluralLower)),
            projectIsActive: _projectFilter != null,
            onProjectTap: _pickProject,
          ),
          HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    final l10n = AppLocalizations.of(context)!;
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_hubMissing || _error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _hubMissing ? l10n.hubNotConfigured : _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      final runs = ref.watch(vocabularyProvider).term(VocabAxis.entityRun);
      final filtered = _status != null || _projectFilter != null;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            filtered
                ? l10n.runsNoneMatch(runs.pluralLower)
                : _status == null
                    ? l10n.runsNoneYet(runs.pluralLower)
                    : l10n.runsNoneStatus(
                        runStatusLabel(l10n, _status!), runs.pluralLower),
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
          projects: _projects ??
              ref.watch(hubProvider).value?.projects ??
              const [],
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
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, Spacing.s8, 8, Spacing.s8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final s in statuses) ...[
                  AppChoiceChip(
                    label: s == null
                        ? l10n.filterAll
                        : runStatusLabel(l10n, s),
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
              borderRadius: Radii.smBorder,
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s8),
                decoration: BoxDecoration(
                  border: Border.all(
                    color: projectIsActive ? DesignColors.primary : border,
                  ),
                  borderRadius: Radii.smBorder,
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

class _ProjectPick {
  final String? id;
  final bool clear;
  const _ProjectPick({this.id, this.clear = false});
}

class _ProjectFilterSheet extends ConsumerWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  const _ProjectFilterSheet({
    required this.projects,
    required this.selectedId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final projectTerm =
        ref.watch(vocabularyProvider).term(VocabAxis.entityProject);
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
                  l10n.allProjects(projectTerm.pluralLower),
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
                  fontSize: FontSizes.label,
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
  final List<Map<String, dynamic>> projects;
  final VoidCallback onTap;
  const _RunRow({
    required this.row,
    required this.projects,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final status = (row['status'] ?? '').toString();
    final name = runLabelFor(row);
    final kind = (row['kind'] ?? '').toString();
    final projectId = (row['project_id'] ?? '').toString();
    final project = projectId.isEmpty
        ? ''
        : projectNameFor(projectId, projects);
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
                fontSize: FontSizes.label,
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
            fontSize: FontSizes.label,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
    );
  }
}

/// Maps a wire run status onto its localized label. Unknown states fall
/// back to the raw wire token so new server states still render.
String runStatusLabel(AppLocalizations l10n, String wire) {
  switch (wire.toLowerCase()) {
    case 'running':
      return l10n.runStatusRunning;
    case 'succeeded':
    case 'success':
    case 'completed':
      return l10n.runStatusSucceeded;
    case 'failed':
    case 'error':
      return l10n.runStatusFailed;
    case 'cancelled':
    case 'canceled':
      return l10n.runStatusCancelled;
    default:
      return wire;
  }
}

class RunStatusChip extends StatelessWidget {
  final String status;
  const RunStatusChip({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final s = status.toLowerCase();
    final color = switch (s) {
      'running' => DesignColors.terminalBlue,
      'succeeded' || 'success' || 'completed' => DesignColors.success,
      'failed' || 'error' => DesignColors.error,
      'cancelled' || 'canceled' => DesignColors.textMuted,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        s.isEmpty ? '?' : runStatusLabel(l10n, s),
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
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

/// The run-detail surfaces, reachable via the header `View ▾` switcher.
/// Every view is always present in the switcher (an empty one renders a
/// quiet empty state) so the menu shape stays stable run-to-run.
/// See docs/plans/run-detail-ui.md.
enum _RunView { overview, charts, media, outputs, config }

List<ViewOption> _runViewOptions(AppLocalizations l10n) => [
  ViewOption(label: l10n.runViewOverview, icon: Icons.dashboard_outlined),
  ViewOption(label: l10n.runViewCharts, icon: Icons.show_chart),
  ViewOption(label: l10n.runViewMedia, icon: Icons.image_outlined),
  ViewOption(label: l10n.runViewOutputs, icon: Icons.folder_outlined),
  ViewOption(label: l10n.runViewConfig, icon: Icons.tune),
];

class _RunDetailScreenState extends ConsumerState<RunDetailScreen> {
  Map<String, dynamic>? _run;
  List<Map<String, dynamic>> _metrics = const [];
  List<Map<String, dynamic>> _images = const [];
  List<Map<String, dynamic>> _histograms = const [];
  List<Map<String, dynamic>> _artifacts = const [];
  // Run "extras": alerts → Overview banner; config → highlight chips +
  // searchable Config view; system metrics → "System (GPU/CPU)" subsection
  // in Charts (x-axis is a sample ordinal, not a training step).
  List<Map<String, dynamic>> _alerts = const [];
  List<Map<String, dynamic>> _systemMetrics = const [];
  Map<String, dynamic>? _config;
  _RunView _view = _RunView.overview;
  bool _loading = true;
  bool _busy = false;
  bool _hubMissing = false;
  String? _error;
  // While the run is live, re-fetch the fast-moving signals (metrics +
  // alerts + status) on a timer so the glance stays current without a
  // manual pull-to-refresh. Cancelled on a terminal status and on dispose.
  Timer? _pollTimer;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
      });
      return;
    }
    try {
      final r = (await client.getRunCached(widget.runId)).body;
      // Metric digests + image-panel entries are optional — a run without
      // an attached tracker (or before the poller's first tick) simply has
      // no rows yet. Fetch both in parallel; failures fall back to empty.
      List<Map<String, dynamic>> metrics = const [];
      List<Map<String, dynamic>> images = const [];
      List<Map<String, dynamic>> histograms = const [];
      List<Map<String, dynamic>> artifacts = const [];
      List<Map<String, dynamic>> alerts = const [];
      List<Map<String, dynamic>> systemMetrics = const [];
      Map<String, dynamic>? config;
      try {
        final results = await Future.wait([
          client.getRunMetricsCached(widget.runId),
          client.getRunImagesCached(widget.runId),
          client.getRunHistogramsCached(widget.runId),
          client.listArtifactsCached(runId: widget.runId),
          client.getRunAlertsCached(widget.runId),
          client.getRunSystemMetricsCached(widget.runId),
        ]);
        metrics = results[0].body;
        images = results[1].body;
        histograms = results[2].body;
        artifacts = results[3].body;
        alerts = results[4].body;
        systemMetrics = results[5].body;
        // Config rides a different response shape (a {config, updated_at}
        // map, not a list), so it's fetched separately.
        final cfgEnvelope = (await client.getRunConfigCached(widget.runId)).body;
        final cfg = cfgEnvelope['config'];
        if (cfg is Map) config = cfg.cast<String, dynamic>();
      } catch (_) {
        // Keep defaults; render the rest of the screen even if digests fail.
      }
      if (!mounted) return;
      setState(() {
        _run = r;
        _metrics = metrics;
        _images = images;
        _histograms = histograms;
        _artifacts = artifacts;
        _alerts = alerts;
        _systemMetrics = systemMetrics;
        _config = config;
        _loading = false;
      });
      _syncPollTimer();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  // The run is "live" (worth polling) until it reaches a terminal status.
  bool get _runIsLive {
    final r = _run ?? widget.summary ?? const <String, dynamic>{};
    final s = (r['status'] ?? '').toString().toLowerCase();
    if (s.isEmpty) return false;
    return s != 'succeeded' && s != 'failed' && s != 'cancelled';
  }

  // Starts the live-refresh timer for a running run, stops it once the run
  // is terminal. Idempotent — safe to call after every load/poll.
  void _syncPollTimer() {
    if (_runIsLive) {
      _pollTimer ??=
          Timer.periodic(const Duration(seconds: 25), (_) => _pollLive());
    } else {
      _pollTimer?.cancel();
      _pollTimer = null;
    }
  }

  // Lightweight live refresh: the status + the two fast-moving digests
  // (metrics, alerts). Heavier, slow-changing data (images, histograms,
  // artifacts, config) waits for an explicit pull-to-refresh.
  Future<void> _pollLive() async {
    if (!mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final run = (await client.getRunCached(widget.runId)).body;
      final results = await Future.wait([
        client.getRunMetricsCached(widget.runId),
        client.getRunAlertsCached(widget.runId),
      ]);
      if (!mounted) return;
      setState(() {
        _run = run;
        _metrics = results[0].body;
        _alerts = results[1].body;
      });
      _syncPollTimer();
    } catch (_) {
      // Transient — the next tick (or a pull-to-refresh) will retry.
    }
  }

  // Opens the producing agent's session (its Insight surface is reachable
  // via that screen's View ▾). The hub session id is resolved from the
  // agent's newest event — the same lookup the project-agent sheet uses.
  Future<void> _openAgent(String agentId, String handle) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    String sid = '';
    try {
      final events =
          await client.listAgentEvents(agentId, tail: true, limit: 1);
      if (events.isNotEmpty) {
        sid = (events.first['session_id'] ?? '').toString();
      }
    } catch (_) {
      // Fall through with an empty session id; the screen still opens.
    }
    if (!mounted) return;
    final agentTerm = ref.read(vocabularyProvider).term(VocabAxis.roleAgent);
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => SessionChatScreen(
        sessionId: sid,
        agentId: agentId,
        title: handle.isEmpty ? agentTerm.title : handle,
      ),
    ));
  }

  Future<void> _launch(String uri) async {
    final u = Uri.tryParse(uri);
    if (u == null) return;
    final ok = await launchUrl(u, mode: LaunchMode.externalApplication);
    if (!ok && mounted) {
      final l10n = AppLocalizations.of(context)!;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.couldNotOpenUri(uri))),
      );
    }
  }

  Future<void> _complete() async {
    final runTerm = ref.read(vocabularyProvider).term(VocabAxis.entityRun);
    final result = await showDialog<_CompletePayload>(
      context: context,
      builder: (_) => _CompleteRunDialog(runLabel: runTerm.lower),
    );
    if (result == null || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    final l10n = AppLocalizations.of(context)!;
    try {
      await client.completeRun(
        widget.runId,
        status: result.status,
        summary: result.summary.isEmpty ? null : result.summary,
      );
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(
        content: Text(l10n.runMarkedStatus(
            runTerm.title, runStatusLabel(l10n, result.status))),
      ));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
          SnackBar(content: Text(l10n.completeFailedError('$e'))));
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
    final l10n = AppLocalizations.of(context)!;
    try {
      await client.attachRunMetricURI(
        widget.runId,
        kind: result.kind,
        uri: result.uri,
      );
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(l10n.dashboardAttached)));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
          SnackBar(content: Text(l10n.attachFailedError('$e'))));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _deleteRun() async {
    final l10n = AppLocalizations.of(context)!;
    final runTerm = ref.read(vocabularyProvider).term(VocabAxis.entityRun);
    final r = _run ?? widget.summary ?? const <String, dynamic>{};
    final name =
        (r['name'] ?? r['id'] ?? l10n.thisRun(runTerm.lower)).toString();
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.deleteRunTitle(runTerm.lower)),
        content: Text(l10n.deleteRunBody(name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l10n.buttonDelete),
          ),
        ],
      ),
    );
    if (confirm != true || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    final navigator = Navigator.of(context);
    try {
      await client.deleteRun(widget.runId);
      if (!mounted) return;
      messenger.showSnackBar(
          SnackBar(content: Text(l10n.runDeletedSnack(runTerm.title))));
      navigator.pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(
          SnackBar(content: Text(l10n.deleteFailedError('$e'))));
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
    final l10n = AppLocalizations.of(context)!;
    final runTerm = ref.watch(vocabularyProvider).term(VocabAxis.entityRun);
    final r = _run ?? widget.summary ?? const <String, dynamic>{};
    final name = (r['name'] ?? r['id'] ?? '(${runTerm.lower})').toString();
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
                  fontSize: FontSizes.subtitle,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ],
        ),
        actions: [
          if (!_loading && _error == null)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: ViewSwitcher(
                views: _runViewOptions(l10n),
                currentView: _view.index,
                onSelect: (i) =>
                    setState(() => _view = _RunView.values[i]),
              ),
            ),
          IconButton(
            tooltip: l10n.attachDashboard,
            icon: const Icon(Icons.link),
            onPressed: _busy ? null : _attachMetric,
          ),
          if (!terminal)
            IconButton(
              tooltip: l10n.markComplete,
              icon: const Icon(Icons.flag_outlined),
              onPressed: _busy ? null : _complete,
            ),
          PopupMenuButton<String>(
            tooltip: l10n.tooltipMore,
            enabled: !_busy,
            onSelected: (v) {
              if (v == 'delete') _deleteRun();
            },
            itemBuilder: (_) => [
              PopupMenuItem<String>(
                value: 'delete',
                child: Row(
                  children: [
                    const Icon(Icons.delete_outline,
                        size: 18, color: DesignColors.error),
                    const SizedBox(width: 10),
                    Text(l10n.deleteRunMenuItem(runTerm.lower)),
                  ],
                ),
              ),
            ],
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
    if (_hubMissing || _error != null) {
      final l10n = AppLocalizations.of(context)!;
      return Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          _hubMissing ? l10n.hubNotConfigured : _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    // One IndexedStack so each view keeps its own scroll position as the
    // user flips between them via the header `View ▾`. Every view is always
    // built (empty ones show a quiet empty state) — see _RunView.
    return IndexedStack(
      index: _view.index,
      children: [
        _overviewView(r),
        _chartsView(),
        _mediaView(),
        _outputsView(),
        _configView(r),
      ],
    );
  }

  // A scrolling, pull-to-refreshable column for one view. The
  // AlwaysScrollableScrollPhysics keeps pull-to-refresh working even when a
  // view is short (or empty).
  Widget _viewScroll(List<Widget> children) => RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
          children: children,
        ),
      );

  // ---- Overview: the glance — "is it healthy and how's it going?" ----
  Widget _overviewView(Map<String, dynamic> r) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final runTerm = vocab.term(VocabAxis.entityRun);
    final projectTerm = vocab.term(VocabAxis.entityProject);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    final status = (r['status'] ?? '').toString().toLowerCase();
    final kind = (r['kind'] ?? '').toString();
    final projectId = (r['project_id'] ?? '').toString();
    final agentId = (r['agent_id'] ?? '').toString();
    final parentId = (r['parent_run_id'] ?? '').toString();
    final hub = ref.watch(hubProvider).value;
    final projects = hub?.projects ?? const [];
    final agents = hub?.agents ?? const [];
    final project =
        projectId.isEmpty ? '' : projectNameFor(projectId, projects);
    final agent = agentId.isEmpty ? '' : agentHandleFor(agentId, agents);
    // Show "Open agent →" only when the producing agent still exists as a
    // live agent (in the hub's warm roster, which excludes terminated /
    // archived) — decision 8 of the plan.
    final agentAlive = agentId.isNotEmpty &&
        agents.any((a) => (a['id'] ?? '').toString() == agentId);
    // Parent is almost always on a different page of runs than the one
    // we're viewing; resolve to a short-id label instead of the raw ULID.
    final parent = parentId.isEmpty ? '' : runLabelForId(parentId, const []);
    final created = (r['created_at'] ?? '').toString();
    final completed = (r['completed_at'] ?? '').toString();
    final summary = (r['summary'] ?? '').toString();
    final headline = _headlineMetrics(_metrics);
    final highlights = _configHighlightEntries(_config);

    return _viewScroll([
      // 1. Status strip — status + live-ticking duration.
      _RunStatusStrip(status: status, created: created, completed: completed),
      // 6. Open agent → (placed near the strip, as in the target shape).
      if (agentAlive) ...[
        const SizedBox(height: 6),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              minimumSize: const Size(0, 32),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            icon: const Icon(Icons.smart_toy_outlined, size: 16),
            label: Text(l10n
                .openEntityArrow(agent.isEmpty ? agentTerm.lower : agent)),
            onPressed: () => _openAgent(agentId, agent),
          ),
        ),
      ],
      // 2. Alerts banner — only when the run logged alerts.
      if (_alerts.isNotEmpty) ...[
        const SizedBox(height: 12),
        _RunAlertsBanner(alerts: _alerts),
      ],
      // 3. Headline metric tiles.
      if (headline.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionHeaderAction(
          l10n.metricsLabel,
          l10n.seeAll,
          () => setState(() => _view = _RunView.charts),
        ),
        Wrap(
          spacing: 10,
          runSpacing: 10,
          children: [for (final m in headline) _MetricStatTile(row: m)],
        ),
      ],
      // 4. Config highlights.
      if (highlights.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionHeaderAction(
          l10n.configLabel,
          l10n.seeAll,
          () => setState(() => _view = _RunView.config),
        ),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [for (final e in highlights) _configChip(e.key, e.value)],
        ),
      ],
      // 5. Summary.
      if (summary.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionLabel(l10n.summaryLabel),
        _panel(child: _SummaryBody(summary: summary)),
      ],
      // Identity details below the glance.
      const SizedBox(height: 16),
      _sectionLabel(l10n.detailsLabel),
      if (kind.isNotEmpty) _metaRow(l10n.runDetailKind, kind),
      if (project.isNotEmpty) _metaRow(projectTerm.lower, project),
      if (agent.isNotEmpty) _metaRow(agentTerm.lower, agent),
      if (parent.isNotEmpty) _metaRow(l10n.parentRunLabel(runTerm.lower), parent),
      if (created.isNotEmpty) _metaRow(l10n.metaStarted, created),
      if (completed.isNotEmpty) _metaRow(l10n.metaCompleted, completed),
    ]);
  }

  // A section label with a trailing tappable action (e.g. "See all →" that
  // jumps to the full view).
  Widget _sectionHeaderAction(
          String label, String action, VoidCallback onTap) =>
      Padding(
        padding: const EdgeInsets.only(bottom: Spacing.s8),
        child: Row(
          children: [
            Expanded(
              child: Text(
                label,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.textMuted,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            InkWell(
              onTap: onTap,
              borderRadius: BorderRadius.circular(4),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                child: Text(
                  '$action →',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: DesignColors.primary,
                  ),
                ),
              ),
            ),
          ],
        ),
      );

  // A compact "key value" chip for the Overview config highlights.
  Widget _configChip(String key, String value) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: Colors.black.withValues(alpha: 0.25),
          borderRadius: Radii.smBorder,
          border: Border.all(color: DesignColors.borderDark),
        ),
        child: RichText(
          text: TextSpan(
            children: [
              TextSpan(
                text: '$key ',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: DesignColors.textMuted,
                ),
              ),
              TextSpan(
                text: value,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.terminalCyan,
                ),
              ),
            ],
          ),
        ),
      );

  // Picks up to four headline metrics by a priority heuristic
  // (loss → accuracy → lr, then alphabetical). Decision 2 of the plan;
  // user-pinned metrics are a later enhancement.
  List<Map<String, dynamic>> _headlineMetrics(
      List<Map<String, dynamic>> metrics) {
    if (metrics.isEmpty) return const [];
    int rank(String name) {
      final n = name.toLowerCase();
      if (n.contains('loss')) return 0;
      if (n.contains('acc')) return 1;
      if (n == 'lr' || n.contains('learning_rate')) return 2;
      return 3;
    }

    final sorted = [...metrics]..sort((a, b) {
        final na = (a['name'] ?? '').toString();
        final nb = (b['name'] ?? '').toString();
        final ra = rank(na);
        final rb = rank(nb);
        return ra != rb ? ra.compareTo(rb) : na.compareTo(nb);
      });
    return sorted.take(4).toList();
  }

  // Pulls a curated hyperparameter subset for the Overview chips, falling
  // back to the first few scalar keys. Nested values are skipped (they
  // belong in the full Config view). Decision 7 of the plan.
  List<MapEntry<String, String>> _configHighlightEntries(
      Map<String, dynamic>? config) {
    if (config == null || config.isEmpty) return const [];
    String? scalar(dynamic v) =>
        (v is num || v is bool || v is String) ? v.toString() : null;
    const curated = [
      'model',
      'batch',
      'batch_size',
      'lr',
      'learning_rate',
      'steps',
      'max_steps',
      'epochs',
      'seed',
    ];
    final out = <MapEntry<String, String>>[];
    for (final k in curated) {
      if (!config.containsKey(k)) continue;
      final s = scalar(config[k]);
      if (s != null) out.add(MapEntry(k, s));
    }
    if (out.isEmpty) {
      for (final e in config.entries) {
        final s = scalar(e.value);
        if (s == null) continue;
        out.add(MapEntry(e.key, s));
        if (out.length >= 4) break;
      }
    }
    return out.take(6).toList();
  }

  // ---- Charts: scalar metrics + system (GPU/CPU) + dashboard links ----
  Widget _chartsView() {
    final l10n = AppLocalizations.of(context)!;
    final uris = _metricUris();
    if (_metrics.isEmpty && _systemMetrics.isEmpty && uris.isEmpty) {
      return _emptyView(
        Icons.show_chart,
        l10n.runNoMetricsTitle,
        l10n.runNoMetricsSubtitle,
      );
    }
    return _viewScroll([
      if (_metrics.isNotEmpty) ...[
        _sectionLabel(l10n.metricsLabel),
        for (final g in _groupMetrics(_metrics))
          if (g.rows.length == 1)
            _MetricSparklineTile(row: g.rows.single)
          else
            _MetricGroupTile(groupName: g.name, rows: g.rows),
      ],
      if (_systemMetrics.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionLabel(l10n.systemGpuCpuLabel),
        // System points are keyed by a 0-based sample ordinal, not a
        // training step — tell the tiles to suppress the "step N" label.
        for (final g in _groupMetrics(_systemMetrics))
          if (g.rows.length == 1)
            _MetricSparklineTile(row: g.rows.single, sampleOrdinalX: true)
          else
            _MetricGroupTile(
                groupName: g.name, rows: g.rows, sampleOrdinalX: true),
      ],
      if (uris.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionLabel(l10n.metricDashboardsLabel),
        for (final u in uris) _MetricURITile(row: u, onLaunch: _launch),
      ],
    ]);
  }

  // ---- Media: images + distributions ----
  Widget _mediaView() {
    final l10n = AppLocalizations.of(context)!;
    if (_images.isEmpty && _histograms.isEmpty) {
      return _emptyView(
        Icons.image_outlined,
        l10n.runNoMediaTitle,
        l10n.runNoMediaSubtitle,
      );
    }
    return _viewScroll([
      if (_images.isNotEmpty) ...[
        _sectionLabel(l10n.imagesLabel),
        for (final group in _groupImages(_images))
          _ImageSeriesTile(
            groupName: group.name,
            rows: group.rows,
            runId: widget.runId,
          ),
      ],
      if (_histograms.isNotEmpty) ...[
        const SizedBox(height: 16),
        _sectionLabel(l10n.distributionsLabel),
        for (final group in _groupHistograms(_histograms))
          HistogramSeriesTile(
            groupName: group.name,
            rows: group.rows,
          ),
      ],
    ]);
  }

  // ---- Outputs: produced artifacts ----
  Widget _outputsView() {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final outputTerm = vocab.term(VocabAxis.entityOutput);
    final runTerm = vocab.term(VocabAxis.entityRun);
    if (_artifacts.isEmpty) {
      return _emptyView(
        Icons.folder_outlined,
        l10n.runNoOutputsTitle(outputTerm.pluralLower),
        l10n.runNoOutputsSubtitle(runTerm.lower),
      );
    }
    return _viewScroll([
      _sectionLabel(outputTerm.plural),
      for (final a in _artifacts.take(5)) _RunArtifactTile(row: a),
      if (_artifacts.length > 5)
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            icon: const Icon(Icons.open_in_new, size: 16),
            label: Text(l10n.viewAllOutputs(
                _artifacts.length, outputTerm.pluralLower)),
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ArtifactsScreen(runId: widget.runId),
            )),
          ),
        ),
    ]);
  }

  // ---- Config: metadata (P4 adds searchable hyperparameters) ----
  Widget _configView(Map<String, dynamic> r) {
    final l10n = AppLocalizations.of(context)!;
    final runTerm = ref.watch(vocabularyProvider).term(VocabAxis.entityRun);
    final meta = r['metadata_json'];
    final hasMeta = meta is Map && meta.isNotEmpty;
    final hasConfig = _config != null && _config!.isNotEmpty;
    if (!hasConfig && !hasMeta) {
      return _emptyView(
        Icons.tune,
        l10n.runNoConfigTitle,
        l10n.runNoConfigSubtitle(runTerm.lower),
      );
    }
    return _viewScroll([
      if (hasConfig) ...[
        _sectionLabel(l10n.hyperparametersLabel),
        _ConfigKeyValueList(config: _config!),
      ],
      if (hasMeta) ...[
        if (hasConfig) const SizedBox(height: 16),
        _sectionLabel(l10n.metadataLabel),
        _panel(
          child: SelectableText(
            const JsonEncoder.withIndent('  ').convert(meta),
            style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
          ),
        ),
      ],
    ]);
  }

  // A bordered, faintly-filled container — the shared chrome for the
  // summary/metadata blocks.
  Widget _panel({required Widget child}) => Container(
        padding: const EdgeInsets.all(Spacing.s8),
        decoration: BoxDecoration(
          color: Colors.black.withValues(alpha: 0.25),
          borderRadius: Radii.smBorder,
          border: Border.all(color: DesignColors.borderDark),
        ),
        child: child,
      );

  // A quiet, scrollable (so pull-to-refresh still works) empty state for a
  // view that has no data for this run yet.
  Widget _emptyView(IconData icon, String title, String subtitle) =>
      RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 64, 24, 24),
              child: Column(
                children: [
                  Icon(icon, size: 40, color: DesignColors.textMuted),
                  const SizedBox(height: 12),
                  Text(
                    title,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.textMuted,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    subtitle,
                    textAlign: TextAlign.center,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      );

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
        padding: const EdgeInsets.only(bottom: Spacing.s8),
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

// ---- Overview glance widgets (P3) ----

/// Maps an alert level to its accent colour. Unknown levels read as info.
Color _alertLevelColor(String level) {
  switch (level.toLowerCase()) {
    case 'error':
    case 'critical':
      return DesignColors.error;
    case 'warn':
    case 'warning':
      return DesignColors.warning;
    default:
      return DesignColors.terminalCyan;
  }
}

/// The Overview status strip: a status chip beside a duration that ticks
/// live while the run is still going.
class _RunStatusStrip extends StatelessWidget {
  final String status;
  final String created;
  final String completed;
  const _RunStatusStrip({
    required this.status,
    required this.created,
    required this.completed,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: 8),
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.25),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: Row(
        children: [
          RunStatusChip(status: status),
          const SizedBox(width: 10),
          Expanded(
            child: _LiveDurationText(created: created, completed: completed),
          ),
        ],
      ),
    );
  }
}

/// Renders the run duration, repainting itself once a second while the run
/// is still running (completed timestamp empty) so the elapsed time stays
/// live without rebuilding the whole screen.
class _LiveDurationText extends StatefulWidget {
  final String created;
  final String completed;
  const _LiveDurationText({required this.created, required this.completed});

  @override
  State<_LiveDurationText> createState() => _LiveDurationTextState();
}

class _LiveDurationTextState extends State<_LiveDurationText> {
  Timer? _ticker;

  @override
  void initState() {
    super.initState();
    _syncTicker();
  }

  @override
  void didUpdateWidget(covariant _LiveDurationText oldWidget) {
    super.didUpdateWidget(oldWidget);
    _syncTicker();
  }

  void _syncTicker() {
    final running = widget.completed.isEmpty;
    if (running) {
      _ticker ??= Timer.periodic(const Duration(seconds: 1), (_) {
        if (mounted) setState(() {});
      });
    } else {
      _ticker?.cancel();
      _ticker = null;
    }
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final d = _runDuration(l10n, widget.created, widget.completed);
    return Text(
      d.isEmpty ? '—' : d,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: GoogleFonts.jetBrainsMono(
        fontSize: 12,
        color: DesignColors.textMuted,
      ),
    );
  }
}

/// The Overview alerts banner. Collapsed it shows a level-coloured headline
/// (the most severe, newest alert); tapping expands the full list inline.
class _RunAlertsBanner extends StatefulWidget {
  final List<Map<String, dynamic>> alerts;
  const _RunAlertsBanner({required this.alerts});

  @override
  State<_RunAlertsBanner> createState() => _RunAlertsBannerState();
}

class _RunAlertsBannerState extends State<_RunAlertsBanner> {
  bool _expanded = false;

  static int _severity(String level) {
    switch (level.toLowerCase()) {
      case 'error':
      case 'critical':
        return 2;
      case 'warn':
      case 'warning':
        return 1;
      default:
        return 0;
    }
  }

  static String _line(Map<String, dynamic> a) {
    final title = (a['title'] ?? '').toString();
    final step = a['step'];
    return step is num ? '$title · step $step' : title;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final alerts = widget.alerts;
    // Headline = most severe; ties broken by recency (later in the
    // server's oldest-first ordering wins via >=).
    var head = alerts.first;
    for (final a in alerts) {
      if (_severity((a['level'] ?? '').toString()) >=
          _severity((head['level'] ?? '').toString())) {
        head = a;
      }
    }
    final color = _alertLevelColor((head['level'] ?? '').toString());
    final n = alerts.length;
    final muted = DesignColors.textMuted;
    return InkWell(
      onTap: () => setState(() => _expanded = !_expanded),
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.all(Spacing.s8),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: color.withValues(alpha: 0.5)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.warning_amber_rounded, size: 18, color: color),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    l10n.alertCount(n),
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      fontWeight: FontWeight.w700,
                      color: color,
                    ),
                  ),
                ),
                Icon(_expanded ? Icons.expand_less : Icons.expand_more,
                    size: 18, color: muted),
              ],
            ),
            const SizedBox(height: 4),
            if (!_expanded)
              Text(
                _line(head),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.3),
              )
            else
              for (final a in alerts) _alertRow(a, muted),
          ],
        ),
      ),
    );
  }

  Widget _alertRow(Map<String, dynamic> a, Color muted) {
    final level = (a['level'] ?? '').toString();
    final color = _alertLevelColor(level);
    final text = (a['text'] ?? '').toString();
    return Padding(
      padding: const EdgeInsets.only(top: Spacing.s8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            margin: const EdgeInsets.only(top: 4),
            width: 7,
            height: 7,
            decoration: BoxDecoration(color: color, shape: BoxShape.circle),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  _line(a),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                    height: 1.3,
                  ),
                ),
                if (text.isNotEmpty)
                  Text(
                    text,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: FontSizes.label,
                      color: muted,
                      height: 1.3,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// A single Overview headline tile: metric name, its last value (large),
/// and a mini-sparkline. Reuses the metric digest's point parser + painter.
class _MetricStatTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _MetricStatTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '').toString();
    final points = _MetricSparklineTile._parsePoints(row['points']);
    final lastValue = (row['last_value'] as num?)?.toDouble() ??
        (points.isEmpty ? null : points.last.$2);
    return Container(
      width: 150,
      padding: const EdgeInsets.all(Spacing.s8),
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.25),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            name,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            lastValue == null
                ? '—'
                : _MetricSparklineTile._fmtValue(lastValue),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 18,
              fontWeight: FontWeight.w700,
              color: DesignColors.terminalCyan,
            ),
          ),
          const SizedBox(height: 6),
          SizedBox(
            height: 24,
            child: points.length >= 2
                ? CustomPaint(
                    size: const Size.fromHeight(24),
                    painter: _SparklinePainter(points),
                  )
                : const SizedBox.shrink(),
          ),
        ],
      ),
    );
  }
}

/// Flattens a (possibly nested) config map to dotted keys so the Config
/// view can show one searchable row per leaf. Lists and leftover maps are
/// rendered as compact JSON values rather than exploded further.
Map<String, String> _flattenConfig(Map<String, dynamic> config,
    [String prefix = '']) {
  final out = <String, String>{};
  config.forEach((k, v) {
    final key = prefix.isEmpty ? k : '$prefix.$k';
    if (v is Map) {
      out.addAll(_flattenConfig(v.cast<String, dynamic>(), key));
    } else if (v is List) {
      out[key] = jsonEncode(v);
    } else {
      out[key] = '$v';
    }
  });
  return out;
}

/// The Config view's hyperparameter list — flattened dotted key/value
/// rows with a filter field, since configs routinely run to 50+ keys.
class _ConfigKeyValueList extends StatefulWidget {
  final Map<String, dynamic> config;
  const _ConfigKeyValueList({required this.config});

  @override
  State<_ConfigKeyValueList> createState() => _ConfigKeyValueListState();
}

class _ConfigKeyValueListState extends State<_ConfigKeyValueList> {
  String _q = '';
  late final List<MapEntry<String, String>> _flat =
      (_flattenConfig(widget.config).entries.toList()
        ..sort((a, b) => a.key.compareTo(b.key)));

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final entries = _q.isEmpty
        ? _flat
        : _flat
            .where((e) =>
                e.key.toLowerCase().contains(_q) ||
                e.value.toLowerCase().contains(_q))
            .toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextField(
          onChanged: (v) => setState(() => _q = v.trim().toLowerCase()),
          style: GoogleFonts.jetBrainsMono(fontSize: 12),
          decoration: InputDecoration(
            isDense: true,
            hintText: l10n.filterKeysHint(_flat.length),
            hintStyle: GoogleFonts.jetBrainsMono(
              fontSize: 12,
              color: DesignColors.textMuted,
            ),
            prefixIcon: const Icon(Icons.search, size: 18),
            contentPadding:
                const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
            border: OutlineInputBorder(
              borderRadius: Radii.smBorder,
              borderSide: const BorderSide(color: DesignColors.borderDark),
            ),
            enabledBorder: OutlineInputBorder(
              borderRadius: Radii.smBorder,
              borderSide: const BorderSide(color: DesignColors.borderDark),
            ),
          ),
        ),
        const SizedBox(height: 8),
        if (entries.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Text(
              l10n.noKeysMatch(_q),
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          )
        else
          for (final e in entries) _kvRow(e.key, e.value),
      ],
    );
  }

  Widget _kvRow(String k, String v) => Padding(
        padding: const EdgeInsets.symmetric(vertical: Spacing.s4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              flex: 5,
              child: SelectableText(
                k,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              flex: 6,
              child: SelectableText(
                v,
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
          ],
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
String _runDuration(
    AppLocalizations l10n, String createdIso, String completedIso) {
  final start = DateTime.tryParse(createdIso);
  if (start == null) return '';
  final end =
      completedIso.isEmpty ? DateTime.now() : DateTime.tryParse(completedIso);
  if (end == null) return '';
  final diff = end.difference(start);
  final running = completedIso.isEmpty;
  final body = _fmtDurationMs(diff.inMilliseconds);
  return running ? l10n.runningFor(body) : body;
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
  DesignColors.violet,
];

/// Renders a metric group as a single multi-line chart with a legend.
/// Series share one y-axis — caller is expected to emit metrics whose
/// units already align (loss/{train,val}). Mixed-unit groups should
/// use distinct top-level group names.
class _MetricGroupTile extends StatelessWidget {
  final String groupName;
  final List<Map<String, dynamic>> rows;
  // When true the x-axis is a sample ordinal (system metrics) — the
  // bottom label drops the misleading "step N".
  final bool sampleOrdinalX;
  const _MetricGroupTile({
    required this.groupName,
    required this.rows,
    this.sampleOrdinalX = false,
  });

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
      padding: const EdgeInsets.symmetric(vertical: Spacing.s8),
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
                        fontSize: FontSizes.label,
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
            (!sampleOrdinalX && lastStep > 0)
                ? 'step $lastStep · $sampleCount samples'
                : '$sampleCount samples',
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
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
            borderRadius: Radii.xsBorder,
          ),
        ),
        const SizedBox(width: 4),
        Text(
          s.label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: FontSizes.label,
            color: DesignColors.textMuted,
          ),
        ),
        if (s.lastValue != null) ...[
          const SizedBox(width: 4),
          Text(
            _MetricSparklineTile._fmtValue(s.lastValue!),
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
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
  // When true the x-axis is a sample ordinal (system metrics) — the
  // bottom label drops the misleading "step N".
  final bool sampleOrdinalX;
  const _MetricSparklineTile({required this.row, this.sampleOrdinalX = false});

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '').toString();
    final points = _parsePoints(row['points']);
    final sampleCount = (row['sample_count'] as num?)?.toInt() ?? points.length;
    final lastStep = (row['last_step'] as num?)?.toInt();
    final lastValue = (row['last_value'] as num?)?.toDouble() ??
        (points.isEmpty ? null : points.last.$2);

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Spacing.s8),
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
                        fontSize: FontSizes.label,
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
            (!sampleOrdinalX && lastStep != null)
                ? 'step $lastStep · $sampleCount samples'
                : '$sampleCount samples',
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
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
    final l10n = AppLocalizations.of(context)!;
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
        kind.isEmpty ? l10n.dashboardLabel : kind,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: Text(
        uri,
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
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

/// Compact artifact row used in the Run detail Outputs section. Taps open
/// the ArtifactsScreen scoped to this run so the full list and filters
/// are one hop away.
class _RunArtifactTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _RunArtifactTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final kind = (row['kind'] ?? '').toString();
    final name = (row['name'] ?? '(unnamed)').toString();
    final size = row['size'];
    return ListTile(
      dense: true,
      contentPadding: EdgeInsets.zero,
      leading: ArtifactKindChip(kind: kind),
      title: Text(
        name,
        overflow: TextOverflow.ellipsis,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: size is int
          ? Text(
              _fmtArtifactSize(size),
              style: GoogleFonts.jetBrainsMono(
                fontSize: FontSizes.label,
                color: DesignColors.textMuted,
              ),
            )
          : null,
      trailing: const Icon(Icons.chevron_right, size: 18),
      onTap: () {
        // Open the artifacts screen filtered by this run; the user can
        // drill into any single artifact from there.
        final runId = (row['run_id'] ?? '').toString();
        if (runId.isEmpty) return;
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => ArtifactsScreen(runId: runId),
        ));
      },
    );
  }
}

String _fmtArtifactSize(int bytes) {
  if (bytes < 1024) return '${bytes}B';
  final kb = bytes / 1024;
  if (kb < 1024) return '${kb.toStringAsFixed(1)}KB';
  final mb = kb / 1024;
  if (mb < 1024) return '${mb.toStringAsFixed(1)}MB';
  final gb = mb / 1024;
  return '${gb.toStringAsFixed(1)}GB';
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

/// Groups histogram rows by metric_name and sorts each group by step so
/// the scrubber moves through training in order. Mirrors _groupImages
/// but keyed off histogram rows coming from /v1/runs/{id}/histograms.
class _HistogramGroup {
  final String name;
  final List<Map<String, dynamic>> rows;
  _HistogramGroup(this.name, this.rows);
}

List<_HistogramGroup> _groupHistograms(List<Map<String, dynamic>> rows) {
  final order = <String>[];
  final byKey = <String, List<Map<String, dynamic>>>{};
  for (final r in rows) {
    final key = (r['name'] ?? '').toString();
    if (key.isEmpty) continue;
    if (!byKey.containsKey(key)) {
      order.add(key);
      byKey[key] = [];
    }
    byKey[key]!.add(r);
  }
  for (final k in order) {
    byKey[k]!.sort(
      (a, b) => ((a['step'] as num?)?.toInt() ?? 0)
          .compareTo((b['step'] as num?)?.toInt() ?? 0),
    );
  }
  return [for (final k in order) _HistogramGroup(k, byKey[k]!)];
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
      final bytes = await client.downloadBlobCached(sha);
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
      padding: const EdgeInsets.symmetric(vertical: Spacing.s8),
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
                borderRadius: Radii.smBorder,
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
                fontSize: FontSizes.label,
                color: DesignColors.textMuted,
              ),
            ),
          Text(
            '${widget.rows.length} checkpoints',
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }

  Widget _preview(Uint8List? bytes, String? err) {
    if (err != null) {
      final l10n = AppLocalizations.of(context)!;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Text(
            l10n.failedToLoadError(err),
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
  final String runLabel;
  const _CompleteRunDialog({required this.runLabel});

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
    final l10n = AppLocalizations.of(context)!;
    return AlertDialog(
      title: Text(l10n.completeRunTitle(widget.runLabel)),
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
                  label: Text(runStatusLabel(l10n, s)),
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
            decoration: InputDecoration(
              labelText: l10n.summaryOptional,
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(
            _CompletePayload(_status, _summary.text.trim()),
          ),
          child: Text(l10n.buttonComplete),
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
    final l10n = AppLocalizations.of(context)!;
    return AlertDialog(
      title: Text(l10n.attachDashboard),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: _kind,
            style: GoogleFonts.jetBrainsMono(fontSize: 13),
            decoration: InputDecoration(
              labelText: l10n.fieldKind,
              helperText: l10n.attachKindHelper,
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _uri,
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
            decoration: InputDecoration(
              labelText: l10n.attachUriLabel,
              hintText: 'https://...',
              border: const OutlineInputBorder(),
              isDense: true,
            ),
            keyboardType: TextInputType.url,
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: () {
            final kind = _kind.text.trim();
            final uri = _uri.text.trim();
            if (kind.isEmpty || uri.isEmpty) return;
            Navigator.of(context).pop(_MetricPayload(kind, uri));
          },
          child: Text(l10n.buttonAttach),
        ),
      ],
    );
  }
}
