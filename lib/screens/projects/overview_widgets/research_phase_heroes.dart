import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:pdfrx/pdfrx.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../../../widgets/artifact_viewers/metric_chart_viewer.dart';
import '../../../widgets/artifact_viewers/pdf_viewer.dart';
import '../../../widgets/criterion_state_pip.dart';
import '../../../widgets/deliverable_state_pip.dart';
import '../../../widgets/section_state_pip.dart';
import '../../deliverables/structured_deliverable_viewer.dart';
import '../../documents/section_detail_screen.dart';
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

  /// Optional typed-content embed rendered below the deliverable list.
  /// Receives the same `overview` map this widget already fetched, so
  /// embeds that key off deliverables/components/phase can read what's
  /// here without a second round-trip. `null` while loading; embeds
  /// should fall back to no-op or a loading spinner in that case.
  final Widget Function(BuildContext, Map<String, dynamic>?)? extrasBuilder;

  const _PhaseHero({
    required this.ctx,
    required this.headline,
    required this.subhead,
    required this.icon,
    required this.tone,
    this.extrasBuilder,
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
          if (widget.extrasBuilder != null) ...[
            const SizedBox(height: 12),
            widget.extrasBuilder!(context, _overview),
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
      extrasBuilder: (context, overview) => _ScopeCriterionEmbed(
        projectId: ctx.projectId,
        phase: 'idea',
      ),
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
      extrasBuilder: (context, overview) => _NextSectionEmbed(
        projectId: ctx.projectId,
        overview: overview,
      ),
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
      extrasBuilder: (context, overview) =>
          _ExperimentMetricChartEmbed(projectId: ctx.projectId),
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
      extrasBuilder: (context, overview) =>
          _PaperPdfEmbed(projectId: ctx.projectId),
    );
  }
}

/// In-hero embed: fetches the newest `pdf`-kind artifact attached to the
/// project, renders page-1 in a constrained box via pdfrx, tap-through
/// to the fullscreen PDF viewer. Silent when no PDF exists.
class _PaperPdfEmbed extends ConsumerStatefulWidget {
  final String projectId;
  const _PaperPdfEmbed({required this.projectId});

  @override
  ConsumerState<_PaperPdfEmbed> createState() => _PaperPdfEmbedState();
}

class _PaperPdfEmbedState extends ConsumerState<_PaperPdfEmbed> {
  Map<String, dynamic>? _artifact;
  Uint8List? _bytes;
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
        kind: 'pdf',
      );
      final rows = cached.body;
      if (rows.isEmpty) {
        if (mounted) setState(() => _loading = false);
        return;
      }
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
      if (!mounted) return;
      setState(() {
        _artifact = row;
        _bytes = Uint8List.fromList(bytes);
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
    final row = _artifact;
    final bytes = _bytes;
    if (row == null || bytes == null) {
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final name = (row['name'] ?? 'paper.pdf').toString();
    final uri = (row['uri'] ?? '').toString();
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => ArtifactPdfViewerScreen(uri: uri, title: name),
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
                Icon(Icons.picture_as_pdf_outlined, size: 14, color: muted),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    name,
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
            // 220 px is enough to show page 1 fit-to-width on a phone;
            // IgnorePointer locks user gestures so the preview can't
            // scroll into page 2 — that's what the fullscreen route is
            // for.
            SizedBox(
              height: 220,
              child: IgnorePointer(
                child: PdfViewer.data(bytes, sourceName: name),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// In-hero embed: walks the first deliverable's document component, parses
/// its sections, surfaces the first non-ratified section as a tappable
/// card. Tap → SectionDetailScreen so the director can read + ratify
/// without first opening the structured viewer.
///
/// Phase-scoped via the overview map (loaded by [_PhaseHero]); when no
/// deliverable or no document component is present the embed is silent.
class _NextSectionEmbed extends ConsumerStatefulWidget {
  final String projectId;
  final Map<String, dynamic>? overview;
  const _NextSectionEmbed({required this.projectId, required this.overview});

  @override
  ConsumerState<_NextSectionEmbed> createState() => _NextSectionEmbedState();
}

class _NextSectionEmbedState extends ConsumerState<_NextSectionEmbed> {
  String? _docId;
  String? _docTitle;
  Map<String, dynamic>? _nextSection;
  int _ratifiedCount = 0;
  int _totalSections = 0;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(covariant _NextSectionEmbed old) {
    super.didUpdateWidget(old);
    // Refresh when the parent reloads overview (e.g. after section ratify).
    if (old.overview != widget.overview) {
      _load();
    }
  }

  String? _firstDocComponentId() {
    final ov = widget.overview;
    if (ov == null) return null;
    final dels = (ov['deliverables'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>());
    for (final d in dels) {
      final comps = (d['components'] as List? ?? const [])
          .map((e) => (e as Map).cast<String, dynamic>());
      for (final c in comps) {
        if ((c['kind'] ?? '').toString() == 'document') {
          final id = (c['ref_id'] ?? '').toString();
          if (id.isNotEmpty) return id;
        }
      }
    }
    return null;
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final docId = _firstDocComponentId();
    if (client == null || docId == null) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final doc = await client.getDocument(docId);
      final raw = (doc['content_inline'] ?? '').toString();
      if (raw.isEmpty) {
        if (mounted) {
          setState(() {
            _docId = docId;
            _docTitle = (doc['title'] ?? '').toString();
            _loading = false;
          });
        }
        return;
      }
      final decoded = jsonDecode(raw);
      if (decoded is! Map) {
        if (mounted) setState(() => _loading = false);
        return;
      }
      final sections = (decoded['sections'] as List? ?? const [])
          .whereType<Map>()
          .map((s) => s.cast<String, dynamic>())
          .toList();
      final next = sections.firstWhere(
        (s) => (s['status'] ?? '').toString() != 'ratified',
        orElse: () => <String, dynamic>{},
      );
      final ratified = sections
          .where((s) => (s['status'] ?? '').toString() == 'ratified')
          .length;
      if (!mounted) return;
      setState(() {
        _docId = docId;
        _docTitle = (doc['title'] ?? '').toString();
        _nextSection = next.isEmpty ? null : next;
        _ratifiedCount = ratified;
        _totalSections = sections.length;
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
    final docId = _docId;
    final section = _nextSection;
    if (docId == null || section == null) {
      // No doc, or every section already ratified. Silent — the deliverable
      // already exposes ratify-as-whole + the chassis exposes counts.
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final title =
        (section['title'] ?? section['slug'] ?? 'section').toString();
    final body = (section['body'] ?? '').toString();
    final preview = _previewLines(body, 3);
    final state =
        parseSectionState((section['status'] ?? '').toString());
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () async {
        await Navigator.of(context).push(
          MaterialPageRoute<void>(
            builder: (_) => SectionDetailScreen(
              documentId: docId,
              documentTitle: _docTitle ?? '',
              slug: (section['slug'] ?? '').toString(),
              initialSection: section,
            ),
          ),
        );
        if (mounted) await _load();
      },
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
                SectionStatePip(state: state, showLabel: false, size: 10),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'Next: $title',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                Text(
                  '$_ratifiedCount/$_totalSections',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
                const SizedBox(width: 4),
                Icon(Icons.chevron_right, size: 16, color: muted),
              ],
            ),
            if (preview.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                preview,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  height: 1.35,
                  color: muted,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  static String _previewLines(String body, int maxLines) {
    final lines = body
        .split('\n')
        .map((l) => l.trim())
        .where((l) => l.isNotEmpty)
        .take(maxLines)
        .toList();
    return lines.join(' · ');
  }
}

/// In-hero embed: surfaces the project's first pending scope criterion as
/// a tappable card with the actions sheet (Mark met / Mark failed /
/// Waive). When no pending criterion remains, falls back to a "scope
/// ratified" status hint so the operator still sees the gate state.
class _ScopeCriterionEmbed extends ConsumerStatefulWidget {
  final String projectId;
  final String phase;
  const _ScopeCriterionEmbed({
    required this.projectId,
    required this.phase,
  });

  @override
  ConsumerState<_ScopeCriterionEmbed> createState() =>
      _ScopeCriterionEmbedState();
}

class _ScopeCriterionEmbedState
    extends ConsumerState<_ScopeCriterionEmbed> {
  List<Map<String, dynamic>>? _criteria;
  bool _loading = true;
  bool _busy = false;

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
      final cached = await client.listProjectCriteriaCached(
        projectId: widget.projectId,
        phase: widget.phase,
      );
      if (!mounted) return;
      setState(() {
        _criteria = cached.body;
        _loading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  Map<String, dynamic>? _pickActive() {
    final rows = _criteria ?? const <Map<String, dynamic>>[];
    // Prefer the first pending criterion (the "next decision").
    for (final c in rows) {
      if ((c['state'] ?? '').toString() == 'pending') return c;
    }
    // Otherwise surface the most-recently-resolved one so the operator
    // still sees the gate state rather than nothing.
    return rows.isEmpty ? null : rows.first;
  }

  Future<void> _act(String criterionId, String action) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _busy = true);
    try {
      switch (action) {
        case 'mark-met':
          await client.markCriterionMet(
            projectId: widget.projectId,
            criterionId: criterionId,
          );
          break;
        case 'mark-failed':
          await client.markCriterionFailed(
            projectId: widget.projectId,
            criterionId: criterionId,
          );
          break;
        case 'waive':
          await client.waiveCriterion(
            projectId: widget.projectId,
            criterionId: criterionId,
          );
          break;
      }
      if (!mounted) return;
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Failed: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _showActions(String criterionId, String currentState) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (currentState != 'met')
              ListTile(
                leading: const Icon(Icons.check_circle,
                    color: DesignColors.terminalGreen),
                title: const Text('Mark met'),
                onTap: () => Navigator.pop(ctx, 'mark-met'),
              ),
            if (currentState != 'failed')
              ListTile(
                leading: const Icon(Icons.cancel, color: DesignColors.error),
                title: const Text('Mark failed'),
                onTap: () => Navigator.pop(ctx, 'mark-failed'),
              ),
            if (currentState != 'waived')
              ListTile(
                leading: const Icon(Icons.do_not_disturb,
                    color: DesignColors.textMuted),
                title: const Text('Waive'),
                onTap: () => Navigator.pop(ctx, 'waive'),
              ),
            ListTile(
              leading: const Icon(Icons.close),
              title: const Text('Cancel'),
              onTap: () => Navigator.pop(ctx),
            ),
          ],
        ),
      ),
    );
    if (action != null) await _act(criterionId, action);
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
    final criterion = _pickActive();
    if (criterion == null) {
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final id = (criterion['id'] ?? '').toString();
    final kind = (criterion['kind'] ?? '').toString();
    final state = (criterion['state'] ?? 'pending').toString();
    final body = (criterion['body'] is Map)
        ? (criterion['body'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final summary = _criterionSummary(kind, body);
    final pip = parseCriterionState(state);
    final isGate = kind == 'gate';
    final actionable = !isGate && !_busy && id.isNotEmpty;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: actionable ? () => _showActions(id, state) : null,
      child: Container(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: border),
        ),
        child: Row(
          children: [
            CriterionStatePip(state: pip, showLabel: false, size: 10),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    summary,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '$kind · $state${isGate ? ' · auto' : ''}',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: muted,
                    ),
                  ),
                ],
              ),
            ),
            if (_busy)
              const SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            else if (actionable)
              Icon(Icons.more_vert, size: 18, color: muted),
          ],
        ),
      ),
    );
  }

  static String _criterionSummary(String kind, Map<String, dynamic> body) {
    switch (kind) {
      case 'text':
        return (body['text'] ?? body['body'] ?? '—').toString();
      case 'metric':
        final m = (body['metric'] ?? '').toString();
        final op = (body['operator'] ?? '').toString();
        final t = body['threshold'];
        return '$m $op $t'.trim();
      case 'gate':
        return (body['gate'] ?? '—').toString();
      default:
        return '—';
    }
  }
}

