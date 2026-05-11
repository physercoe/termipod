import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../../../widgets/artifact_viewers/metric_chart_viewer.dart';
import '../../../widgets/deliverable_state_pip.dart';
import '../../deliverables/structured_deliverable_viewer.dart';
import 'registry.dart';

/// W7 — research-template phase-scoped overview heroes (A6 §3-§7).
///
/// All four widgets compose existing primitives: a small explanatory
/// header + the project's deliverables + criteria for the active phase
/// (when the chassis exposes one) + a CTA pointer. They share enough
/// structure that a single `_PhaseHero` does the heavy lifting; per-
/// phase variants pass a label + tone.
///
/// Per the W7 plan, these are stub composition widgets — the demo's
/// chassis-generality story rests on them existing as recognisable
/// names that the template's YAML declares (so the YAML-reveal moment
/// can point to "this widget is what the YAML asks for"). They do not
/// invent novel UI affordances; they reuse the deliverable + criterion
/// pip vocabulary.

class _PhaseHero extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  final String headline;
  final String subhead;
  final IconData icon;
  final Color tone;

  /// Optional typed-artifact preview rendered below the deliverable list.
  /// Heroes pass a widget that fetches + renders one of the project's
  /// closed-set artifact kinds inline (e.g. metric-chart on
  /// experiment_dash). Owns its own lifecycle so the parent stays dumb.
  final Widget? extras;

  const _PhaseHero({
    required this.ctx,
    required this.headline,
    required this.subhead,
    required this.icon,
    required this.tone,
    this.extras,
  });

  @override
  ConsumerState<_PhaseHero> createState() => _PhaseHeroState();
}

class _PhaseHeroState extends ConsumerState<_PhaseHero> {
  Map<String, dynamic>? _overview;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final pid = widget.ctx.projectId;
    if (client == null || pid.isEmpty) {
      setState(() => _loading = false);
      return;
    }
    try {
      final ov = await client.getProjectOverview(pid);
      if (!mounted) return;
      setState(() {
        _overview = ov;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final deliverables = (_overview?['deliverables'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 8),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(widget.icon, size: 20, color: widget.tone),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  widget.headline,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            widget.subhead,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: DesignColors.textMuted,
            ),
          ),
          if (_loading)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: LinearProgressIndicator(minHeight: 2),
            )
          else if (deliverables.isNotEmpty) ...[
            const SizedBox(height: 12),
            for (final d in deliverables)
              _DeliverableLine(
                projectId: widget.ctx.projectId,
                deliverable: d,
                onChanged: _load,
              ),
          ],
          if (widget.extras != null) ...[
            const SizedBox(height: 12),
            widget.extras!,
          ],
        ],
      ),
    );
  }
}

class _DeliverableLine extends StatelessWidget {
  final String projectId;
  final Map<String, dynamic> deliverable;
  final VoidCallback onChanged;
  const _DeliverableLine({
    required this.projectId,
    required this.deliverable,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final id = (deliverable['id'] ?? '').toString();
    final kind = (deliverable['kind'] ?? '').toString();
    final state = parseDeliverableState(
        (deliverable['ratification_state'] ?? '').toString());
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: InkWell(
        borderRadius: BorderRadius.circular(6),
        onTap: () async {
          await Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => StructuredDeliverableViewer(
                projectId: projectId,
                deliverableId: id,
                initialDeliverable: deliverable,
              ),
            ),
          );
          onChanged();
        },
        child: Row(
          children: [
            DeliverableStatePip(state: state, showLabel: false),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                _prettyKind(kind),
                style: GoogleFonts.spaceGrotesk(fontSize: 13),
              ),
            ),
            const Icon(Icons.chevron_right, size: 16),
          ],
        ),
      ),
    );
  }

  static String _prettyKind(String slug) {
    if (slug.isEmpty) return 'Deliverable';
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) => p.isEmpty
            ? p
            : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }
}

class IdeaConversationHero extends StatelessWidget {
  final OverviewContext ctx;
  const IdeaConversationHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    return _PhaseHero(
      ctx: ctx,
      headline: 'Direct the steward',
      subhead:
          'No formal deliverable in this phase. Talk through scope, ratify the '
          'scope criterion when ready, then advance to Lit-review.',
      icon: Icons.forum_outlined,
      tone: DesignColors.primary,
    );
  }
}

class DeliverableFocusHero extends StatelessWidget {
  final OverviewContext ctx;
  const DeliverableFocusHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    return _PhaseHero(
      ctx: ctx,
      headline: 'Active deliverable',
      subhead:
          'Tap to open the structured viewer; ratify sections one by one or '
          'ratify the deliverable as a whole.',
      icon: Icons.description_outlined,
      tone: DesignColors.primary,
    );
  }
}

class ExperimentDashHero extends StatelessWidget {
  final OverviewContext ctx;
  const ExperimentDashHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    return _PhaseHero(
      ctx: ctx,
      headline: 'Experiment dashboard',
      subhead:
          'Mixed-component deliverable: report doc + artifacts + runs. Metric '
          'criterion auto-fires when threshold is met.',
      icon: Icons.science_outlined,
      tone: DesignColors.terminalBlue,
      extras: _ExperimentMetricChartEmbed(projectId: ctx.projectId),
    );
  }
}

/// In-hero embed: fetches the newest `metric-chart` artifact for the
/// project, parses its body, renders a compact inline chart with a
/// tap-through to the fullscreen viewer. Silent when no chart exists
/// (the experiment hasn't produced one yet) so phases that haven't
/// reached results don't show an empty card.
class _ExperimentMetricChartEmbed extends ConsumerStatefulWidget {
  final String projectId;
  const _ExperimentMetricChartEmbed({required this.projectId});

  @override
  ConsumerState<_ExperimentMetricChartEmbed> createState() =>
      _ExperimentMetricChartEmbedState();
}

class _ExperimentMetricChartEmbedState
    extends ConsumerState<_ExperimentMetricChartEmbed> {
  Map<String, dynamic>? _artifact;
  MetricChartBody? _chart;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || widget.projectId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final cached = await client.listArtifactsCached(
        projectId: widget.projectId,
        kind: 'metric-chart',
      );
      final rows = cached.body;
      if (rows.isEmpty) {
        if (mounted) setState(() => _loading = false);
        return;
      }
      // Hub returns newest-first; tolerant defensive sort.
      final sorted = [...rows];
      sorted.sort((a, b) {
        final ac = (a['created_at'] ?? '').toString();
        final bc = (b['created_at'] ?? '').toString();
        return bc.compareTo(ac);
      });
      final row = sorted.first;
      final uri = (row['uri'] ?? '').toString();
      if (!uri.startsWith('blob:sha256/')) {
        if (mounted) {
          setState(() {
            _artifact = row;
            _loading = false;
          });
        }
        return;
      }
      final sha = uri.substring('blob:sha256/'.length);
      final bytes = await client.downloadBlobCached(sha);
      final decoded = jsonDecode(utf8.decode(Uint8List.fromList(bytes)));
      final chart = parseMetricChart(decoded);
      if (!mounted) return;
      setState(() {
        _artifact = row;
        _chart = chart;
        _loading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const SizedBox(
        height: 24,
        child: Center(
          child: SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
        ),
      );
    }
    final chart = _chart;
    final row = _artifact;
    if (chart == null || row == null) {
      // Either no metric-chart artifact yet, or parse/fetch failed.
      // Stay silent — the parent hero already explains the phase.
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final name = (row['name'] ?? 'metric chart').toString();
    final uri = (row['uri'] ?? '').toString();
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => ArtifactMetricChartViewerScreen(
            uri: uri,
            title: chart.title.isNotEmpty ? chart.title : name,
          ),
        ),
      ),
      child: Container(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.show_chart, size: 14, color: muted),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    chart.title.isNotEmpty ? chart.title : name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                Icon(Icons.chevron_right, size: 16, color: muted),
              ],
            ),
            const SizedBox(height: 8),
            MetricChartInline(chart: chart, collapsedHeight: 140),
          ],
        ),
      ),
    );
  }
}

class PaperAcceptanceHero extends StatelessWidget {
  final OverviewContext ctx;
  const PaperAcceptanceHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    return _PhaseHero(
      ctx: ctx,
      headline: 'Paper draft',
      subhead:
          'Synthesise method + experiment-report into the paper. Ratify the '
          'draft to close the project.',
      icon: Icons.menu_book_outlined,
      tone: DesignColors.warning,
    );
  }
}

