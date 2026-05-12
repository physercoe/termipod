import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../models/artifact_kinds.dart';
import '../providers/hub_provider.dart';
import '../screens/artifacts/artifacts_by_kind_screen.dart';
import '../screens/deliverables/structured_deliverable_viewer.dart';
import '../screens/projects/acceptance_criteria_screen.dart';
import '../screens/projects/artifacts_screen.dart';
import '../screens/projects/blobs_section.dart';
import '../screens/projects/deliverables_screen.dart';
import '../screens/projects/documents_screen.dart';
import '../screens/projects/overview_widgets/registry.dart';
import '../screens/projects/plans_screen.dart';
import '../screens/projects/project_channels_list_screen.dart';
import '../screens/projects/runs_screen.dart';
import '../screens/projects/schedules_screen.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';

/// Closed registry of shortcut-tile slugs (template-yaml-schema §11).
/// Templates pick a per-phase subset; the chassis enforces the slug
/// vocabulary so a typo in YAML can't smuggle in a new tile.
enum TileSlug {
  outputs,
  documents,
  schedules,
  plans,
  assets,
  experiments,
  references,
  risks,
  discussion,
  // Chassis-followup wave 1 (ADR-024) — back deliverables + AC with
  // first-class list screens so templates can surface them as their
  // own shortcut tiles instead of forcing the user through a phase
  // chip every time.
  deliverables,
  acceptanceCriteria,
}

TileSlug? _slugFromString(String raw) {
  switch (raw.trim().toLowerCase()) {
    case 'outputs':
      return TileSlug.outputs;
    case 'documents':
      return TileSlug.documents;
    case 'schedules':
      return TileSlug.schedules;
    case 'plans':
      return TileSlug.plans;
    case 'assets':
      return TileSlug.assets;
    case 'experiments':
      return TileSlug.experiments;
    case 'references':
      return TileSlug.references;
    case 'risks':
      return TileSlug.risks;
    case 'discussion':
      return TileSlug.discussion;
    case 'deliverables':
      return TileSlug.deliverables;
    case 'acceptance_criteria':
    case 'acceptance-criteria':
    case 'acceptancecriteria':
    case 'criteria':
      return TileSlug.acceptanceCriteria;
  }
  return null;
}

/// Hardcoded research-template phase→tiles map. Mirrors
/// `docs/reference/research-template-spec.md` §3 exactly. W7 will pull
/// these from the project YAML and supersede this constant; W4 ships
/// the mobile rendering ahead of W7 so the demo path validates the
/// chassis cut without waiting on the template-loader work.
const Map<String, List<TileSlug>> _researchPhaseTiles = {
  // Idea phase is "conversation-first" by spec, but the steward routinely
  // creates idea memos here (lifecycle-walkthrough scenario 3) — the
  // Documents tile gives the director a way to find what just got written.
  'idea': [TileSlug.documents],
  'lit-review': [TileSlug.references, TileSlug.documents],
  'method': [
    TileSlug.references,
    TileSlug.documents,
    TileSlug.plans,
  ],
  'experiment': [
    TileSlug.outputs,
    TileSlug.documents,
    TileSlug.experiments,
  ],
  'paper': [TileSlug.outputs, TileSlug.documents],
};

/// Chassis default when no template/phase tile mapping applies. Aligned
/// with template-yaml-schema §11 ("`Outputs`, `Documents` if `tiles:`
/// is absent on a phase").
const List<TileSlug> _chassisDefault = [TileSlug.outputs, TileSlug.documents];

/// Resolves the tile set for a (template, phase) pair.
///
/// Resolution order (most-specific wins):
///   1. **Per-project override** — `phaseTileOverrides[<phase>]`,
///      sourced from `projects.phase_tile_overrides_json` (PATCH-able
///      via the steward and via the on-device tile editor).
///   2. **Template default** — `phaseTilesTemplate[<phase>]`, sourced
///      from the template YAML's `phase_specs[<phase>].tiles`.
///   3. **Chassis safety net** — hardcoded `_researchPhaseTiles` map
///      for the well-known phase ids (kept during rollout so installs
///      without the new payload fields still render correctly).
///   4. **Chassis default** — `[Outputs, Documents]`.
///
/// Unknown slugs are silently dropped at parse time (`_slugFromString`
/// returns null for them). The closed [TileSlug] enum is the
/// vocabulary; the composition is the data.
List<TileSlug> resolveTilesForPhase({
  required String templateId,
  required String phase,
  Map<String, List<String>>? phaseTileOverrides,
  Map<String, List<String>>? phaseTilesTemplate,
  @Deprecated('Use phaseTilesTemplate; phaseTilesYaml is the old name')
  Map<String, List<String>>? phaseTilesYaml,
}) {
  // Back-compat: old call sites passed phaseTilesYaml; treat it as a
  // template-tier override if phaseTilesTemplate isn't provided.
  phaseTilesTemplate ??= phaseTilesYaml;

  if (phaseTileOverrides != null && phaseTileOverrides.containsKey(phase)) {
    final raw = phaseTileOverrides[phase] ?? const <String>[];
    return raw.map(_slugFromString).whereType<TileSlug>().toList();
  }
  if (phaseTilesTemplate != null && phaseTilesTemplate.containsKey(phase)) {
    final raw = phaseTilesTemplate[phase] ?? const <String>[];
    return raw.map(_slugFromString).whereType<TileSlug>().toList();
  }
  if (_researchPhaseTiles.containsKey(phase)) {
    return _researchPhaseTiles[phase]!;
  }
  return _chassisDefault;
}

/// Parses a `{phase: [slug, ...]}` map from a project payload field.
/// Tolerant of both `List<String>` and `List<dynamic>` value shapes
/// since `dart:convert` decodes JSON arrays as the latter. Returns
/// null when the input is null / not a map / empty (so callers can
/// fall through to the next resolution tier).
Map<String, List<String>>? parsePhaseTilesMap(Object? raw) {
  if (raw is! Map) return null;
  final out = <String, List<String>>{};
  raw.forEach((k, v) {
    if (k is! String || v is! List) return;
    final slugs = <String>[];
    for (final e in v) {
      if (e is String) slugs.add(e);
    }
    if (slugs.isNotEmpty) out[k] = slugs;
  });
  return out.isEmpty ? null : out;
}

/// Parses a `{phase: "<slug>"}` map from a project payload field
/// (`overview_widget_overrides` / `overview_widget_template`). Returns
/// null on null/non-map/empty so callers can fall through cleanly.
Map<String, String>? parsePhaseStringMap(Object? raw) {
  if (raw is! Map) return null;
  final out = <String, String>{};
  raw.forEach((k, v) {
    if (k is String && v is String && v.isNotEmpty) out[k] = v;
  });
  return out.isEmpty ? null : out;
}

/// Renders the template-declared shortcut tiles for the project's
/// current phase (W4 — IA §6.2 / template-yaml-schema §11). Replaces
/// the prior 7-hard-coded-tile strip on Overview with a phase-filtered
/// set; Reviews is intentionally absent (the orange attention banner
/// already serves it, plan §6.4 acceptance gap #4).
class ShortcutTileStrip extends ConsumerWidget {
  final String projectId;
  final String projectName;
  final String templateId;
  final String phase;
  /// Fires with the updated project body after the Customize sheet
  /// saves. The strip itself only renders props, so the caller (the
  /// project-detail screen that owns `_project`) is responsible for
  /// refreshing local state. Without this, the sheet appears to "do
  /// nothing" — the PATCH succeeds but the strip rebuilds with the
  /// same stale props.
  final ValueChanged<Map<String, dynamic>>? onProjectChanged;
  /// Per-project override sourced from
  /// `projects.phase_tile_overrides_json`. Steward or user
  /// edits land here. Wins over [phaseTilesTemplate].
  final Map<String, List<String>>? phaseTileOverrides;
  /// Template default sourced from
  /// `template.phase_specs[<phase>].tiles` (the hub serves this on
  /// the project payload as `phase_tiles_template`).
  final Map<String, List<String>>? phaseTilesTemplate;
  /// Per-phase hero (overview-widget) override sourced from
  /// `projects.overview_widget_overrides_json` (ADR-024 D10). Plumbed
  /// through so the customize sheet can show the picker's current
  /// state. Wins over [overviewWidgetTemplate] in the picker.
  final Map<String, String>? overviewWidgetOverrides;
  /// Per-phase template-side hero declaration sourced from
  /// `template.phase_specs[<phase>].overview_widget`. Used by the
  /// picker's Reset affordance to show "what would this phase render
  /// without the override."
  final Map<String, String>? overviewWidgetTemplate;
  /// Currently-resolved hero slug for the project's current phase (the
  /// `overview_widget` field on the project payload). Used as the
  /// initial picker value when no per-phase override exists.
  final String currentOverviewWidget;

  const ShortcutTileStrip({
    super.key,
    required this.projectId,
    required this.projectName,
    required this.templateId,
    required this.phase,
    this.phaseTileOverrides,
    this.phaseTilesTemplate,
    this.overviewWidgetOverrides,
    this.overviewWidgetTemplate,
    this.currentOverviewWidget = '',
    this.onProjectChanged,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tiles = resolveTilesForPhase(
      templateId: templateId,
      phase: phase,
      phaseTileOverrides: phaseTileOverrides,
      phaseTilesTemplate: phaseTilesTemplate,
    );
    if (tiles.isEmpty) {
      return Column(
        children: [
          _NoTilesPlaceholder(phase: phase),
          const SizedBox(height: 8),
          _CustomizeTilesRow(
            projectId: projectId,
            phase: phase,
            currentTiles: tiles,
            phaseTileOverrides: phaseTileOverrides,
            phaseTilesTemplate: phaseTilesTemplate,
            overviewWidgetOverrides: overviewWidgetOverrides,
            overviewWidgetTemplate: overviewWidgetTemplate,
            currentOverviewWidget: currentOverviewWidget,
            onProjectChanged: onProjectChanged,
          ),
        ],
      );
    }
    return Column(
      children: [
        for (final t in tiles) ...[
          _TileRow(
            slug: t,
            projectId: projectId,
            projectName: projectName,
          ),
          const SizedBox(height: 8),
        ],
        _CustomizeTilesRow(
          projectId: projectId,
          phase: phase,
          currentTiles: tiles,
          phaseTileOverrides: phaseTileOverrides,
          phaseTilesTemplate: phaseTilesTemplate,
          overviewWidgetOverrides: overviewWidgetOverrides,
          overviewWidgetTemplate: overviewWidgetTemplate,
          currentOverviewWidget: currentOverviewWidget,
          onProjectChanged: onProjectChanged,
        ),
      ],
    );
  }
}

/// Trailing "Customize shortcuts" row — tap opens [PhaseTileEditorSheet]
/// so the director (or steward) can compose this phase's tile set. The
/// row deliberately mirrors `_TileRow`'s shape so it reads as part of
/// the strip rather than a foreign affordance.
class _CustomizeTilesRow extends ConsumerWidget {
  final String projectId;
  final String phase;
  final List<TileSlug> currentTiles;
  final Map<String, List<String>>? phaseTileOverrides;
  final Map<String, List<String>>? phaseTilesTemplate;
  final Map<String, String>? overviewWidgetOverrides;
  final Map<String, String>? overviewWidgetTemplate;
  final String currentOverviewWidget;
  final ValueChanged<Map<String, dynamic>>? onProjectChanged;

  const _CustomizeTilesRow({
    required this.projectId,
    required this.phase,
    required this.currentTiles,
    required this.phaseTileOverrides,
    required this.phaseTilesTemplate,
    required this.overviewWidgetOverrides,
    required this.overviewWidgetTemplate,
    required this.currentOverviewWidget,
    this.onProjectChanged,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => _open(context, ref),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: Colors.transparent,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight)
                .withValues(alpha: 0.5),
          ),
        ),
        child: Row(
          children: [
            Icon(
              Icons.tune,
              size: 16,
              color: DesignColors.textMuted,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                phase.isEmpty
                    ? 'Customize shortcuts'
                    : 'Customize shortcuts for this phase',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            Icon(
              Icons.chevron_right,
              size: 16,
              color: DesignColors.textMuted,
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context, WidgetRef ref) async {
    // Phaseless projects (manual create, no template) store their
    // tile + hero overrides under the empty-string phase key. The
    // hub's `resolveOverviewWidget` and the resolver below both honor
    // that key, so customization is the same shape as for phased
    // projects.
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final updated = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => PhaseTileEditorSheet(
        projectId: projectId,
        phase: phase,
        client: client,
        currentTiles: currentTiles,
        phaseTileOverrides: phaseTileOverrides,
        phaseTilesTemplate: phaseTilesTemplate,
        overviewWidgetOverrides: overviewWidgetOverrides,
        overviewWidgetTemplate: overviewWidgetTemplate,
        currentOverviewWidget: currentOverviewWidget,
      ),
    );
    if (updated == null) return;
    // Bubble the fresh project body up so the project-detail screen
    // can refresh its `_project` snapshot — without this, the strip
    // rebuilds with the same stale props and the user sees no change.
    onProjectChanged?.call(updated);
    // Also nudge the hub snapshot so the projects-list cache (used by
    // siblings like the Projects screen) doesn't render stale tile
    // overrides on the next visit.
    await ref.read(hubProvider.notifier).refreshAll();
  }
}

/// Modal sheet for editing the tile composition for a single phase.
/// Renders the closed [TileSlug] vocabulary as a list with checkbox +
/// drag handle. Save → PATCH `projects.phase_tile_overrides`; Reset →
/// PATCH the same field with this phase cleared (falls back to template
/// YAML + chassis default).
class PhaseTileEditorSheet extends StatefulWidget {
  final String projectId;
  final String phase;
  final HubClient client;
  final List<TileSlug> currentTiles;
  final Map<String, List<String>>? phaseTileOverrides;
  final Map<String, List<String>>? phaseTilesTemplate;
  final Map<String, String>? overviewWidgetOverrides;
  final Map<String, String>? overviewWidgetTemplate;
  final String currentOverviewWidget;

  const PhaseTileEditorSheet({
    super.key,
    required this.projectId,
    required this.phase,
    required this.client,
    required this.currentTiles,
    required this.phaseTileOverrides,
    required this.phaseTilesTemplate,
    this.overviewWidgetOverrides,
    this.overviewWidgetTemplate,
    this.currentOverviewWidget = '',
  });

  @override
  State<PhaseTileEditorSheet> createState() => _PhaseTileEditorSheetState();
}

class _PhaseTileEditorSheetState extends State<PhaseTileEditorSheet> {
  /// Working copy of the ordered slug list for this phase. Mutates as
  /// the user toggles checkboxes / reorders. Saved verbatim on tap.
  late List<TileSlug> _selected;
  /// Remaining (unselected) slugs, rendered below the selected list so
  /// the user can pick from the full closed vocabulary in one place.
  late List<TileSlug> _available;
  /// Currently-picked hero slug for this phase (ADR-024 D10). Defaults
  /// to the per-phase override if set, else the resolved widget on the
  /// project payload. Empty when neither is known (lifecycle-disabled
  /// projects); picker section hidden in that case.
  late String _selectedHero;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _selected = [...widget.currentTiles];
    _available = [
      for (final t in TileSlug.values)
        if (!_selected.contains(t)) t,
    ];
    final override = widget.overviewWidgetOverrides?[widget.phase];
    if (override != null && override.isNotEmpty) {
      _selectedHero = override;
    } else {
      _selectedHero = widget.currentOverviewWidget;
    }
  }

  bool get _isOverridden {
    final o = widget.phaseTileOverrides;
    final h = widget.overviewWidgetOverrides;
    final tileOver = o != null && o.containsKey(widget.phase);
    final heroOver = h != null && h.containsKey(widget.phase);
    return tileOver || heroOver;
  }

  // Always show the hero picker: even phaseless projects (manual
  // create, no template) resolve to a default overview widget on the
  // hub, so there's always *something* to swap. Empty
  // `currentOverviewWidget` was the legacy "lifecycle disabled" gate;
  // it no longer applies now that overrides can target the empty phase.
  bool get _showHeroPicker => true;

  String get _templateHero =>
      widget.overviewWidgetTemplate?[widget.phase] ?? '';

  String _slugFor(TileSlug t) {
    // The TileSlug enum's name is the lowercase slug already (e.g.
    // `documents`, `outputs`). The hub-side YAML uses TitleCase for
    // historical reasons (e.g. `Documents`). Match the YAML convention
    // when serialising to the wire so a payload written by either the
    // steward (TitleCase per spec) or this sheet round-trips identically.
    final n = t.name;
    return n[0].toUpperCase() + n.substring(1);
  }

  Future<void> _save() async {
    setState(() => _busy = true);
    final nextTiles = <String, List<String>>{
      ...?widget.phaseTileOverrides,
      widget.phase: [for (final t in _selected) _slugFor(t)],
    };
    // Build the hero map (D10). Only include an override when the user's
    // pick differs from the template-side hero for this phase; matching
    // the template = no override needed, keeps the map sparse.
    final nextHeroes = <String, String>{
      ...?widget.overviewWidgetOverrides,
    };
    final templateHero = _templateHero;
    if (_showHeroPicker &&
        _selectedHero.isNotEmpty &&
        _selectedHero != templateHero) {
      nextHeroes[widget.phase] = _selectedHero;
    } else {
      nextHeroes.remove(widget.phase);
    }
    try {
      final updated = await widget.client.updateProject(
        widget.projectId,
        phaseTileOverrides: nextTiles,
        overviewWidgetOverrides: nextHeroes,
      );
      if (!mounted) return;
      Navigator.of(context).pop(updated);
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Save failed: $e')),
      );
    }
  }

  Future<void> _reset() async {
    setState(() => _busy = true);
    final nextTiles = <String, List<String>>{
      ...?widget.phaseTileOverrides,
    };
    nextTiles.remove(widget.phase);
    final nextHeroes = <String, String>{
      ...?widget.overviewWidgetOverrides,
    };
    nextHeroes.remove(widget.phase);
    try {
      final updated = await widget.client.updateProject(
        widget.projectId,
        phaseTileOverrides: nextTiles,
        overviewWidgetOverrides: nextHeroes,
      );
      if (!mounted) return;
      Navigator.of(context).pop(updated);
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reset failed: $e')),
      );
    }
  }

  void _toggle(TileSlug t, bool include) {
    setState(() {
      if (include) {
        if (!_selected.contains(t)) _selected.add(t);
        _available.remove(t);
      } else {
        _selected.remove(t);
        if (!_available.contains(t)) _available.add(t);
      }
    });
  }

  void _onReorder(int oldIndex, int newIndex) {
    setState(() {
      if (newIndex > oldIndex) newIndex -= 1;
      final t = _selected.removeAt(oldIndex);
      _selected.insert(newIndex, t);
    });
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mq = MediaQuery.of(context);
    return Padding(
      padding: EdgeInsets.only(bottom: mq.viewInsets.bottom),
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: mq.size.height * 0.85,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 12),
            Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: DesignColors.textMuted.withValues(alpha: 0.4),
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            const SizedBox(height: 12),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          'Shortcuts for "${widget.phase}"',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 16,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          _isOverridden
                              ? 'Custom — tap reset to revert to template default'
                              : 'Template default — edits will create a per-project override',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 11,
                            color: DesignColors.textMuted,
                          ),
                        ),
                      ],
                    ),
                  ),
                  if (_isOverridden)
                    TextButton(
                      onPressed: _busy ? null : _reset,
                      child: const Text('Reset'),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 8),
            const Divider(height: 1),
            Expanded(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(8, 12, 8, 12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (_showHeroPicker) ...[
                      Padding(
                        padding: const EdgeInsets.fromLTRB(8, 0, 8, 6),
                        child: Text(
                          'Phase hero',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 11,
                            color: DesignColors.textMuted,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.fromLTRB(8, 0, 8, 4),
                        child: Text(
                          overviewWidgetSpecFor(_selectedHero).label,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.fromLTRB(8, 0, 8, 10),
                        child: Text(
                          overviewWidgetSpecFor(_selectedHero).subtitle,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 11,
                            color: DesignColors.textMuted,
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        child: Wrap(
                          spacing: 6,
                          runSpacing: 6,
                          children: [
                            for (final slug in kKnownOverviewWidgets)
                              ChoiceChip(
                                label: Text(
                                  overviewWidgetSpecFor(slug).label,
                                  style: const TextStyle(fontSize: 11),
                                ),
                                selected: _selectedHero == slug,
                                onSelected: (_) => setState(
                                    () => _selectedHero = slug),
                              ),
                          ],
                        ),
                      ),
                      const SizedBox(height: 14),
                      const Divider(height: 1),
                      const SizedBox(height: 8),
                    ],
                    if (_selected.isNotEmpty) ...[
                      Padding(
                        padding: const EdgeInsets.fromLTRB(8, 0, 8, 6),
                        child: Text(
                          'Shown on this phase (drag to reorder)',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 11,
                            color: DesignColors.textMuted,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      ReorderableListView(
                        shrinkWrap: true,
                        physics: const NeverScrollableScrollPhysics(),
                        onReorder: _onReorder,
                        children: [
                          for (final t in _selected)
                            _TileEditorRow(
                              key: ValueKey('selected-${t.name}'),
                              slug: t,
                              selected: true,
                              isDark: isDark,
                              onChanged: (v) => _toggle(t, v),
                              showDragHandle: true,
                            ),
                        ],
                      ),
                    ],
                    if (_available.isNotEmpty) ...[
                      Padding(
                        padding: const EdgeInsets.fromLTRB(8, 12, 8, 6),
                        child: Text(
                          'Available',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 11,
                            color: DesignColors.textMuted,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      for (final t in _available)
                        _TileEditorRow(
                          key: ValueKey('available-${t.name}'),
                          slug: t,
                          selected: false,
                          isDark: isDark,
                          onChanged: (v) => _toggle(t, v),
                          showDragHandle: false,
                        ),
                    ],
                  ],
                ),
              ),
            ),
            const Divider(height: 1),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
              child: Row(
                children: [
                  TextButton(
                    onPressed: _busy
                        ? null
                        : () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const Spacer(),
                  FilledButton(
                    onPressed: _busy ? null : _save,
                    child: _busy
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('Save'),
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

class _TileEditorRow extends StatelessWidget {
  final TileSlug slug;
  final bool selected;
  final bool isDark;
  final bool showDragHandle;
  final ValueChanged<bool> onChanged;

  const _TileEditorRow({
    super.key,
    required this.slug,
    required this.selected,
    required this.isDark,
    required this.showDragHandle,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final spec = tileSpecFor(slug);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: () => onChanged(!selected),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
          child: Row(
            children: [
              Checkbox(
                value: selected,
                onChanged: (v) => onChanged(v ?? false),
                visualDensity: VisualDensity.compact,
              ),
              Icon(spec.icon, size: 18, color: DesignColors.primary),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(spec.label,
                        style: GoogleFonts.spaceGrotesk(
                            fontSize: 14, fontWeight: FontWeight.w600)),
                    Text(spec.subtitle,
                        style: GoogleFonts.spaceGrotesk(
                            fontSize: 11, color: DesignColors.textMuted),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                  ],
                ),
              ),
              if (showDragHandle)
                Icon(Icons.drag_handle,
                    size: 18, color: DesignColors.textMuted),
            ],
          ),
        ),
      ),
    );
  }
}

class _NoTilesPlaceholder extends StatelessWidget {
  final String phase;
  const _NoTilesPlaceholder({required this.phase});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.surfaceDark.withValues(alpha: 0.5)
            : DesignColors.surfaceLight.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.chat_outlined,
              size: 14, color: DesignColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              phase.isEmpty
                  ? 'No shortcuts for this project — keep the conversation going.'
                  : 'Conversation-first phase — no shortcut tiles for "$phase".',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _TileRow extends ConsumerWidget {
  final TileSlug slug;
  final String projectId;
  final String projectName;
  const _TileRow({
    required this.slug,
    required this.projectId,
    required this.projectName,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final spec = tileSpecFor(slug);
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => _open(context, ref),
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
            Icon(spec.icon, size: 18, color: DesignColors.primary),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    spec.label,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    spec.subtitle,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      color: DesignColors.textMuted,
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

  Future<void> _open(BuildContext context, WidgetRef ref) async {
    // References needs an async resolve: route to the project's
    // lit-review deliverable when one exists (its body carries
    // citations + prior-work), else fall back to the Documents screen.
    if (slug == TileSlug.references) {
      await _openReferences(context, ref);
      return;
    }
    Widget page;
    switch (slug) {
      case TileSlug.outputs:
        page = ArtifactsScreen(projectId: projectId);
      case TileSlug.documents:
        page = DocumentsScreen(projectId: projectId);
      case TileSlug.schedules:
        page = SchedulesScreen(projectId: projectId);
      case TileSlug.plans:
        page = PlansScreen(projectId: projectId);
      case TileSlug.assets:
        page = const _AssetsHostScreen();
      case TileSlug.experiments:
        page = RunsScreen(projectId: projectId);
      case TileSlug.references:
        // Handled by the early return above.
        return;
      case TileSlug.risks:
        page = _StubScreen(
          title: 'Risks',
          message: 'Risk register lands with W5b/W7.',
        );
      case TileSlug.discussion:
        page = ProjectChannelsListScreen(
          projectId: projectId,
          projectName: projectName,
        );
      case TileSlug.deliverables:
        page = DeliverablesScreen(projectId: projectId);
      case TileSlug.acceptanceCriteria:
        page = AcceptanceCriteriaScreen(projectId: projectId);
    }
    if (!context.mounted) return;
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => page));
  }

  Future<void> _openReferences(BuildContext context, WidgetRef ref) async {
    // Wave 2 W3 — references resolve to the tabular citation artifact
    // produced by the lit-review deliverable. If any citation-shaped
    // artifact exists for the project, route straight to its kind
    // screen so the table renders. Otherwise fall back to the older
    // lit-review-deliverable / documents-screen sequence so the tile
    // is still useful pre-ratification.
    final client = ref.read(hubProvider.notifier).client;
    bool hasCitation = false;
    Map<String, dynamic>? litReviewDeliverable;
    if (client != null && projectId.isNotEmpty) {
      try {
        final cached = await client.listArtifactsCached(
          projectId: projectId,
          kind: ArtifactKind.tabular.slug,
        );
        for (final a in cached.body) {
          final mime = (a['mime'] ?? '').toString().toLowerCase();
          if (mime.contains('schema=citation')) {
            hasCitation = true;
            break;
          }
        }
      } catch (_) {
        // Swallow — we still try the deliverable route below.
      }
      if (!hasCitation) {
        try {
          final cached = await client.listDeliverablesCached(
            projectId: projectId,
            includeComponents: true,
          );
          for (final d in cached.body) {
            if ((d['kind'] ?? '').toString() == 'lit-review') {
              litReviewDeliverable = d;
              break;
            }
          }
        } catch (_) {
          // Swallow — fall through to the Documents screen below.
        }
      }
    }
    if (!context.mounted) return;
    if (hasCitation) {
      Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => ArtifactsByKindScreen(
          projectId: projectId,
          kind: ArtifactKind.tabular,
          schema: 'citation',
          title: 'References',
        ),
      ));
      return;
    }
    if (litReviewDeliverable != null) {
      final deliverable = litReviewDeliverable;
      Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => StructuredDeliverableViewer(
          projectId: projectId,
          deliverableId: (deliverable['id'] ?? '').toString(),
          initialDeliverable: deliverable,
        ),
      ));
      return;
    }
    // No lit-review deliverable yet — open the Documents screen so the
    // director can see whatever the steward has in flight, with a
    // breadcrumb that explains References will fill in once the
    // lit-review phase ratifies a doc.
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => DocumentsScreen(projectId: projectId),
    ));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text(
          'No lit-review document yet — showing all project documents. '
          'References will land here once the lit-review deliverable ratifies.',
        ),
      ),
    );
  }
}

class TileSpec {
  final String label;
  final String subtitle;
  final IconData icon;
  const TileSpec({
    required this.label,
    required this.subtitle,
    required this.icon,
  });
}

/// Public for tests / future template-yaml lookup. The chassis owns the
/// label + icon mapping; templates only pick which slugs to surface.
TileSpec tileSpecFor(TileSlug slug) {
  switch (slug) {
    case TileSlug.outputs:
      return const TileSpec(
        label: 'Outputs',
        subtitle: 'Outputs runs produce · checkpoints, curves, reports',
        icon: Icons.output_outlined,
      );
    case TileSlug.documents:
      return const TileSpec(
        label: 'Documents',
        subtitle: 'Authored writeups · memos, drafts, reports',
        icon: Icons.article_outlined,
      );
    case TileSlug.schedules:
      return const TileSpec(
        label: 'Schedules',
        subtitle: 'Recurring firings across the team',
        icon: Icons.schedule_outlined,
      );
    case TileSlug.plans:
      return const TileSpec(
        label: 'Plans',
        subtitle: 'Plan templates the steward executes',
        icon: Icons.playlist_play_outlined,
      );
    case TileSlug.assets:
      return const TileSpec(
        label: 'Assets',
        subtitle: 'Browse media from channels · standalone uploads',
        icon: Icons.perm_media_outlined,
      );
    case TileSlug.experiments:
      return const TileSpec(
        label: 'Experiments',
        subtitle: 'ML training/eval runs (blueprint §6.5)',
        icon: Icons.science_outlined,
      );
    case TileSlug.references:
      return const TileSpec(
        label: 'References',
        subtitle: 'Citations · SOTA library · prior art',
        icon: Icons.menu_book_outlined,
      );
    case TileSlug.risks:
      return const TileSpec(
        label: 'Risks',
        subtitle: 'Open risks · mitigations · status',
        icon: Icons.warning_amber_outlined,
      );
    case TileSlug.discussion:
      return const TileSpec(
        label: 'Discussion',
        subtitle: 'Channels · steward thread',
        icon: Icons.chat_outlined,
      );
    case TileSlug.deliverables:
      return const TileSpec(
        label: 'Deliverables',
        subtitle: 'Phase deliverables · documents + components',
        icon: Icons.description_outlined,
      );
    case TileSlug.acceptanceCriteria:
      return const TileSpec(
        label: 'Acceptance',
        subtitle: 'Open criteria · gates · ratification checklist',
        icon: Icons.checklist_outlined,
      );
  }
}

/// Lightweight host so the Assets tile can push a Scaffold-titled page
/// without tying every caller to the private `_BlobsScreen` inside the
/// project detail file.
class _AssetsHostScreen extends StatelessWidget {
  const _AssetsHostScreen();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Assets',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: const BlobsSection(),
    );
  }
}

class _StubScreen extends StatelessWidget {
  final String title;
  final String message;
  const _StubScreen({required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      ),
    );
  }
}
