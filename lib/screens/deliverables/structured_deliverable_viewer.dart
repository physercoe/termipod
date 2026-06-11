import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/criterion_state_pip.dart';
import '../../widgets/deliverable_state_pip.dart';
import '../../widgets/hub_offline_banner.dart';
import '../projects/artifacts_screen.dart' show showArtifactDetailSheet;
import '../projects/documents_screen.dart' show DocumentDetailScreen;
import '../projects/runs_screen.dart' show RunDetailScreen;

/// W5b — Structured Deliverable Viewer (A5).
///
/// Wraps the document/artifact/run/commit components beneath a single
/// ratification gesture. Components are rendered as routing cards; the
/// criteria panel (read-only here) is filled in by W6 once the criteria
/// runtime ships. Action bar mirrors the section action bar from W5a:
/// Ratify (state=draft|in-review) or Unratify (state=ratified).
class StructuredDeliverableViewer extends ConsumerStatefulWidget {
  final String projectId;
  final String deliverableId;
  final Map<String, dynamic>? initialDeliverable;
  final List<Map<String, dynamic>>? initialCriteria;

  const StructuredDeliverableViewer({
    super.key,
    required this.projectId,
    required this.deliverableId,
    this.initialDeliverable,
    this.initialCriteria,
  });

  @override
  ConsumerState<StructuredDeliverableViewer> createState() =>
      _StructuredDeliverableViewerState();
}

class _StructuredDeliverableViewerState
    extends ConsumerState<StructuredDeliverableViewer> {
  Map<String, dynamic>? _deliverable;
  List<Map<String, dynamic>> _criteria = const [];
  bool _busy = false;
  String? _error;
  // Non-null when the loaded body came from the offline cache; UI can
  // show "Last updated X" so the user knows the hub is unreachable.
  DateTime? _staleSince;

  @override
  void initState() {
    super.initState();
    if (widget.initialDeliverable != null) {
      _deliverable = Map<String, dynamic>.from(widget.initialDeliverable!);
    }
    if (widget.initialCriteria != null) {
      _criteria = widget.initialCriteria!;
    }
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final d = await client.getDeliverableCached(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      final crits = await client.listProjectCriteriaCached(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      if (!mounted) return;
      setState(() {
        _deliverable = d.body;
        _criteria = crits.body;
        _staleSince = d.staleSince ?? crits.staleSince;
        _busy = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  DeliverableState get _state =>
      parseDeliverableState((_deliverable?['ratification_state'] ?? '').toString());

  Future<void> _ratify() async {
    final l10n = AppLocalizations.of(context)!;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final out = await client.ratifyDeliverable(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      if (!mounted) return;
      setState(() {
        _deliverable = out;
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.ratifyFailedError('$e'))),
      );
    }
  }

  Future<void> _sendBack() async {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final steward = vocab.term(VocabAxis.roleSteward).lower;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    // Gather open annotations from each document component so the sheet
    // can offer them as checkboxes. Best-effort; failures fall through
    // to a notes-only send-back.
    final docs = ((_deliverable?['components'] as List?) ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .where((c) => (c['kind'] ?? '') == 'document')
        .toList();
    final List<Map<String, dynamic>> openAnnotations = [];
    for (final d in docs) {
      final docId = (d['ref_id'] ?? '').toString();
      if (docId.isEmpty) continue;
      try {
        final out = await client.listAnnotationsCached(
            documentId: docId, status: 'open');
        for (final a in out.body) {
          openAnnotations.add({...a, '_doc_id': docId});
        }
      } catch (_) {}
    }
    if (!mounted) return;
    final result = await showModalBottomSheet<_SendBackResult>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _SendBackSheet(
        l10n: l10n,
        steward: steward,
        annotations: openAnnotations,
      ),
    );
    if (!mounted || result == null) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _busy = true);
    try {
      final out = await client.sendBackDeliverable(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
        note: result.note,
        annotationIds: result.annotationIds,
      );
      if (!mounted) return;
      // The hub returns {deliverable, attention_item_id}; pull the
      // deliverable out to refresh the viewer state.
      final d = (out['deliverable'] as Map?)?.cast<String, dynamic>();
      setState(() {
        if (d != null) _deliverable = d;
        _busy = false;
      });
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.sentBackForRevision(steward))),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.sendBackFailedError('$e'))),
      );
    }
  }

  Future<void> _unratify() async {
    final l10n = AppLocalizations.of(context)!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text(l10n.unratifyDeliverableTitle),
        content: Text(l10n.unratifyDeliverableBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(l10n.buttonCancel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(l10n.buttonUnratify),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final out = await client.unratifyDeliverable(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      if (!mounted) return;
      setState(() {
        _deliverable = out;
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.unratifyFailedError('$e'))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final steward = vocab.term(VocabAxis.roleSteward).lower;
    final d = _deliverable;
    final title = (d?['kind'] ?? l10n.deliverableLabel).toString();
    final phase = (d?['phase'] ?? '').toString();
    final components = (d?['components'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();

    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Flexible(
              child: Text(
                _prettyKind(title),
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: FontSizes.subtitle,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(width: 8),
            DeliverableStatePip(state: _state),
          ],
        ),
        actions: [
          if (_state != DeliverableState.ratified)
            PopupMenuButton<String>(
              tooltip: l10n.moreTooltip,
              onSelected: (v) {
                if (v == 'send_back' && !_busy) _sendBack();
              },
              itemBuilder: (_) => [
                PopupMenuItem(
                  value: 'send_back',
                  child: ListTile(
                    leading: const Icon(Icons.assignment_return_outlined),
                    title: Text(l10n.sendBackWithNotes),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
              ],
            ),
        ],
      ),
      body: _error != null
          ? _ErrorView(l10n: l10n, error: _error!, onRetry: _load)
          : Stack(
              children: [
                ListView(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
                  children: [
                    HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
                    if (phase.isNotEmpty)
                      _MetaRow(
                        label: l10n.fieldPhase,
                        value: _prettyKind(phase),
                      ),
                    _MetaRow(label: l10n.fieldKind, value: title),
                    if ((d?['ratified_at'] ?? '').toString().isNotEmpty)
                      _MetaRow(
                        label: l10n.fieldRatified,
                        value: (d?['ratified_at'] ?? '').toString(),
                      ),
                    const SizedBox(height: 16),
                    _SectionHeading(
                      label: l10n.deliverableComponentsHeading(
                        components.length,
                      ),
                    ),
                    if (components.isEmpty)
                      _EmptyCard(
                        title: l10n.noComponentsYet,
                        body: l10n.componentsAddedHint(steward),
                      )
                    else
                      for (final c in components)
                        _ComponentCard(
                          l10n: l10n,
                          component: c,
                          projectId: widget.projectId,
                        ),
                    const SizedBox(height: 24),
                    _SectionHeading(
                      label: l10n.deliverableCriteriaHeading(_criteria.length),
                    ),
                    if (_criteria.isEmpty)
                      _EmptyCard(
                        title: l10n.noCriteriaDeclared,
                        body: l10n.criteriaDeclaredHint,
                      )
                    else
                      for (final c in _criteria)
                        _CriterionRow(
                          l10n: l10n,
                          criterion: c,
                          projectId: widget.projectId,
                          onChanged: _load,
                        ),
                  ],
                ),
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 0,
                  child: _DeliverableActionBar(
                    l10n: l10n,
                    state: _state,
                    busy: _busy,
                    onRatify: _busy || _state == DeliverableState.ratified
                        ? null
                        : _ratify,
                    onUnratify:
                        _busy || _state != DeliverableState.ratified
                            ? null
                            : _unratify,
                  ),
                ),
              ],
            ),
    );
  }

  static String _prettyKind(String slug) {
    if (slug.isEmpty) return slug;
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) =>
            p.isEmpty ? p : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }
}

class _MetaRow extends StatelessWidget {
  final String label;
  final String value;
  const _MetaRow({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}

class _SectionHeading extends StatelessWidget {
  final String label;
  const _SectionHeading({required this.label});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(0, 8, 0, 8),
      child: Text(
        label,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 13,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.6,
          color: DesignColors.textSecondary,
        ),
      ),
    );
  }
}

class _ComponentCard extends ConsumerStatefulWidget {
  final AppLocalizations l10n;
  final Map<String, dynamic> component;
  final String projectId;
  const _ComponentCard({
    required this.l10n,
    required this.component,
    required this.projectId,
  });

  @override
  ConsumerState<_ComponentCard> createState() => _ComponentCardState();
}

class _ComponentCardState extends ConsumerState<_ComponentCard> {
  // Resolved primary line (name/title) + secondary line (sub-kind for
  // artifacts). Populated by an eager fetch in initState so each row
  // surfaces the underlying entity instead of just its type label.
  // Tester report 2026-05-12: "the delivary row info only shows types
  // like document/artifact/run, no name or artifact-kind."
  String? _resolvedName;
  String? _artifactSubKind;
  bool _resolving = true;

  @override
  void initState() {
    super.initState();
    _resolve();
  }

  Future<void> _resolve() async {
    final kind = (widget.component['kind'] ?? '').toString();
    final refId = (widget.component['ref_id'] ?? '').toString();
    if (refId.isEmpty) {
      if (mounted) setState(() => _resolving = false);
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      if (mounted) setState(() => _resolving = false);
      return;
    }
    try {
      if (kind == 'artifact') {
        final row = await client.getArtifact(refId);
        if (!mounted) return;
        setState(() {
          _resolvedName = (row['name'] ?? '').toString();
          _artifactSubKind = (row['kind'] ?? '').toString();
          _resolving = false;
        });
      } else if (kind == 'document') {
        final row = await client.getDocument(refId);
        if (!mounted) return;
        setState(() {
          _resolvedName =
              (row['title'] ?? row['name'] ?? '').toString();
          _resolving = false;
        });
      } else if (kind == 'run') {
        final row = await client.getRun(refId);
        if (!mounted) return;
        setState(() {
          _resolvedName = (row['name'] ?? row['title'] ?? '').toString();
          _resolving = false;
        });
      } else {
        if (mounted) setState(() => _resolving = false);
      }
    } catch (_) {
      // Network/perm failures fall back to refId display below.
      if (mounted) setState(() => _resolving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final kind = (widget.component['kind'] ?? '').toString();
    final refId = (widget.component['ref_id'] ?? '').toString();
    final required = widget.component['required'] == true;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    // Document / Run kinds carry a themable vocab axis, so their label
    // re-words with the active preset; the other kinds stay plain l10n.
    final vocab = ref.watch(vocabularyProvider);
    final documentLabel = vocab.term(VocabAxis.entityDocument).title;
    final runLabel = vocab.term(VocabAxis.entityRun).title;
    final primaryLabel = _primaryLine(
      l10n: widget.l10n,
      kind: kind,
      refId: refId,
      documentLabel: documentLabel,
      runLabel: runLabel,
    );
    final secondaryLabel = _secondaryLine(
      l10n: widget.l10n,
      kind: kind,
      refId: refId,
      documentLabel: documentLabel,
      runLabel: runLabel,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Material(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: Radii.mdBorder,
        child: InkWell(
          borderRadius: Radii.mdBorder,
          onTap: () => _onTap(context, ref),
          child: Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              borderRadius: Radii.mdBorder,
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: Row(
              children: [
                Icon(_iconFor(kind),
                    size: 22, color: _colorFor(kind)),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      // Primary line: entity name (or "Loading…" / refId
                      // fallback). The big visible piece of new info per
                      // the tester report.
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              primaryLabel,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 13,
                                fontWeight: FontWeight.w700,
                              ),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                          if (required) ...[
                            const SizedBox(width: 8),
                            Container(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: Spacing.s8, vertical: 2),
                              decoration: BoxDecoration(
                                color: DesignColors.warning
                                    .withValues(alpha: 0.15),
                                borderRadius: BorderRadius.circular(4),
                              ),
                              child: Text(
                                widget.l10n.requiredTag,
                                style: GoogleFonts.jetBrainsMono(
                                  fontSize: FontSizes.label,
                                  color: DesignColors.warning,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                          ],
                        ],
                      ),
                      const SizedBox(height: 2),
                      // Secondary line: "<kind> · <subkind>" — e.g.
                      // "artifact · pdf" or just "document". Picks up
                      // the artifact kind from the resolved row.
                      Text(
                        secondaryLabel,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: FontSizes.label,
                          color: _colorFor(kind),
                          letterSpacing: 0.3,
                          fontWeight: FontWeight.w600,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ),
                ),
                if (kind == 'document')
                  const Icon(Icons.chevron_right, size: 18),
              ],
            ),
          ),
        ),
      ),
    );
  }

  String _primaryLine({
    required AppLocalizations l10n,
    required String kind,
    required String refId,
    required String documentLabel,
    required String runLabel,
  }) {
    if (_resolving) return l10n.loading;
    final name = _resolvedName ?? '';
    if (name.isNotEmpty) return name;
    return refId.isEmpty
        ? _labelFor(l10n, kind,
            documentLabel: documentLabel, runLabel: runLabel)
        : refId;
  }

  String _secondaryLine({
    required AppLocalizations l10n,
    required String kind,
    required String refId,
    required String documentLabel,
    required String runLabel,
  }) {
    final base = _labelFor(l10n, kind,
            documentLabel: documentLabel, runLabel: runLabel)
        .toLowerCase();
    if (kind == 'artifact') {
      final sub = _artifactSubKind ?? '';
      if (sub.isNotEmpty) return '$base · $sub';
    }
    // Echo the truncated refId on the secondary line so the user can
    // still verify which row maps to which entity, but stay short.
    if (refId.isNotEmpty && _resolvedName != null && _resolvedName!.isNotEmpty) {
      final short = refId.length > 14 ? '${refId.substring(0, 14)}…' : refId;
      return '$base · $short';
    }
    return base;
  }

  Future<void> _onTap(BuildContext context, WidgetRef ref) async {
    final l10n = widget.l10n;
    final kind = (widget.component['kind'] ?? '').toString();
    final refId = (widget.component['ref_id'] ?? '').toString();
    if (refId.isEmpty) return;
    if (kind == 'document') {
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => DocumentDetailScreen(documentId: refId),
        ),
      );
      return;
    }
    if (kind == 'run') {
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => RunDetailScreen(runId: refId),
        ),
      );
      return;
    }
    if (kind == 'artifact') {
      // Bottom-sheet detail mirrors the artifacts-tab tap behavior.
      // Fetch the row first since the deliverable component only
      // carries the artifact id, not the full row the sheet expects.
      final client = ref.read(hubProvider.notifier).client;
      Map<String, dynamic>? row;
      if (client != null) {
        try {
          row = await client.getArtifact(refId);
        } catch (_) {
          row = null;
        }
      }
      if (!context.mounted) return;
      if (row != null) {
        showArtifactDetailSheet(context, row);
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.couldNotLoadArtifact(refId))),
        );
      }
      return;
    }
    if (kind == 'commit') {
      final vocab = ref.read(vocabularyProvider);
      final project = vocab.term(VocabAxis.entityProject).lower;
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        backgroundColor: Colors.transparent,
        builder: (_) => _CommitDetailSheet(
          l10n: l10n,
          project: project,
          ref: refId,
        ),
      );
      return;
    }
    // Any future component kind we don't yet render — surface the ref
    // so the user can at least locate it.
    final vocab = ref.read(vocabularyProvider);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          l10n.componentRefMessage(
            _labelFor(l10n, kind,
                documentLabel: vocab.term(VocabAxis.entityDocument).title,
                runLabel: vocab.term(VocabAxis.entityRun).title),
            refId,
          ),
        ),
      ),
    );
  }

  static IconData _iconFor(String kind) {
    switch (kind) {
      case 'document':
        return Icons.description_outlined;
      case 'artifact':
        return Icons.insert_drive_file_outlined;
      case 'run':
        return Icons.play_arrow_outlined;
      case 'commit':
        return Icons.commit_outlined;
      default:
        return Icons.help_outline;
    }
  }

  static Color _colorFor(String kind) {
    switch (kind) {
      case 'document':
        return DesignColors.primary;
      case 'artifact':
        return DesignColors.terminalBlue;
      case 'run':
        return DesignColors.terminalGreen;
      case 'commit':
        return DesignColors.warning;
      default:
        return DesignColors.textMuted;
    }
  }

  // documentLabel / runLabel come from the active vocabulary preset
  // (VocabAxis.entityDocument / entityRun); the remaining kinds have no
  // matching axis and stay plain l10n strings.
  static String _labelFor(
    AppLocalizations l10n,
    String kind, {
    required String documentLabel,
    required String runLabel,
  }) {
    switch (kind) {
      case 'document':
        return documentLabel;
      case 'artifact':
        return l10n.componentKindArtifact;
      case 'run':
        return runLabel;
      case 'commit':
        return l10n.componentKindCommit;
      default:
        return l10n.componentKindGeneric;
    }
  }
}

class _CriterionRow extends ConsumerStatefulWidget {
  final AppLocalizations l10n;
  final Map<String, dynamic> criterion;
  final String projectId;
  final VoidCallback onChanged;
  const _CriterionRow({
    required this.l10n,
    required this.criterion,
    required this.projectId,
    required this.onChanged,
  });

  @override
  ConsumerState<_CriterionRow> createState() => _CriterionRowState();
}

class _CriterionRowState extends ConsumerState<_CriterionRow> {
  bool _busy = false;

  String get _kind => (widget.criterion['kind'] ?? '').toString();
  String get _state => (widget.criterion['state'] ?? 'pending').toString();
  String get _id => (widget.criterion['id'] ?? '').toString();

  Map<String, dynamic> get _body => (widget.criterion['body'] is Map)
      ? (widget.criterion['body'] as Map).cast<String, dynamic>()
      : <String, dynamic>{};

  Future<void> _act(String action) async {
    final l10n = widget.l10n;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    String? note;
    if (action == 'mark-met' && _kind == 'text') {
      note = await _promptForNote(
        context,
        l10n,
        l10n.evidenceTitle,
        l10n.evidenceHint,
      );
    } else if (action == 'mark-failed' || action == 'waive') {
      note = await _promptForNote(
        context,
        l10n,
        l10n.reasonTitle,
        l10n.reasonHint,
      );
    }
    setState(() => _busy = true);
    try {
      switch (action) {
        case 'mark-met':
          await client.markCriterionMet(
            projectId: widget.projectId,
            criterionId: _id,
            evidenceRef: note,
          );
          break;
        case 'mark-failed':
          await client.markCriterionFailed(
            projectId: widget.projectId,
            criterionId: _id,
            reason: note,
          );
          break;
        case 'waive':
          await client.waiveCriterion(
            projectId: widget.projectId,
            criterionId: _id,
            reason: note,
          );
          break;
      }
      if (!mounted) return;
      widget.onChanged();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.failedError('$e'))),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final summary = _criterionSummary(widget.l10n, _kind, _body);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final isGate = _kind == 'gate';
    final pipState = parseCriterionState(_state);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Material(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        child: InkWell(
          borderRadius: BorderRadius.circular(8),
          onTap: _busy || isGate ? null : () => _showActions(context),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: Spacing.s8),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: Row(
              children: [
                CriterionStatePip(state: pipState, showLabel: false, size: 10),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        summary,
                        style: GoogleFonts.spaceGrotesk(fontSize: 13),
                      ),
                      const SizedBox(height: 2),
                      Row(
                        children: [
                          Text(
                            '${criterionKindLabel(widget.l10n, _kind)} · '
                            '${criterionStateLabel(widget.l10n, _state)}',
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: FontSizes.label,
                              color: DesignColors.textMuted,
                            ),
                          ),
                          if (isGate) ...[
                            const SizedBox(width: 6),
                            Text(
                              widget.l10n.autoSuffix,
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: FontSizes.label,
                                color: DesignColors.textMuted,
                              ),
                            ),
                          ],
                        ],
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
                else if (!isGate)
                  const Icon(Icons.more_vert, size: 18),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _showActions(BuildContext context) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (_state != 'met')
              ListTile(
                leading: const Icon(Icons.check_circle,
                    color: DesignColors.terminalGreen),
                title: Text(widget.l10n.markMet),
                onTap: () => Navigator.pop(ctx, 'mark-met'),
              ),
            if (_state != 'failed')
              ListTile(
                leading: const Icon(Icons.cancel, color: DesignColors.error),
                title: Text(widget.l10n.markFailed),
                onTap: () => Navigator.pop(ctx, 'mark-failed'),
              ),
            if (_state != 'waived')
              ListTile(
                leading: const Icon(Icons.do_not_disturb,
                    color: DesignColors.textMuted),
                title: Text(widget.l10n.waive),
                onTap: () => Navigator.pop(ctx, 'waive'),
              ),
            ListTile(
              leading: const Icon(Icons.close),
              title: Text(widget.l10n.buttonCancel),
              onTap: () => Navigator.pop(ctx),
            ),
          ],
        ),
      ),
    );
    if (action != null) await _act(action);
  }

  static Future<String?> _promptForNote(
    BuildContext context,
    AppLocalizations l10n,
    String title,
    String hint,
  ) async {
    final ctrl = TextEditingController();
    final result = await showDialog<String?>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text(title),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: InputDecoration(hintText: hint),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, null),
            child: Text(l10n.buttonSkip),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, ctrl.text.trim()),
            child: Text(l10n.buttonOk),
          ),
        ],
      ),
    );
    return result;
  }

  static String _criterionSummary(
    AppLocalizations l10n,
    String kind,
    Map<String, dynamic> body,
  ) {
    switch (kind) {
      case 'text':
        return (body['text'] ?? body['body'] ?? l10n.unavailable).toString();
      case 'metric':
        final m = (body['metric'] ?? '').toString();
        final op = (body['operator'] ?? '').toString();
        final t = body['threshold'];
        return '$m $op $t'.trim();
      case 'gate':
        return (body['gate'] ?? l10n.unavailable).toString();
      default:
        return l10n.unavailable;
    }
  }
}

String criterionKindLabel(AppLocalizations l10n, String kind) {
  switch (kind) {
    case 'text':
      return l10n.criterionKindText;
    case 'metric':
      return l10n.criterionKindMetric;
    case 'gate':
      return l10n.criterionKindGate;
    default:
      return kind;
  }
}

String criterionStateLabel(AppLocalizations l10n, String state) {
  switch (state) {
    case 'pending':
      return l10n.criterionStatePending;
    case 'met':
      return l10n.criterionStateMet;
    case 'failed':
      return l10n.criterionStateFailed;
    case 'waived':
      return l10n.criterionStateWaived;
    default:
      return state;
  }
}

class _EmptyCard extends StatelessWidget {
  final String title;
  final String body;
  const _EmptyCard({required this.title, required this.body});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 4),
      padding: const EdgeInsets.all(Spacing.s12),
      decoration: BoxDecoration(
        color: (isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight)
            .withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            body,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final AppLocalizations l10n;
  final String error;
  final VoidCallback onRetry;
  const _ErrorView({
    required this.l10n,
    required this.error,
    required this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, size: 32),
            const SizedBox(height: 8),
            Text(error, textAlign: TextAlign.center),
            const SizedBox(height: 12),
            OutlinedButton(onPressed: onRetry, child: Text(l10n.buttonRetry)),
          ],
        ),
      ),
    );
  }
}

class _DeliverableActionBar extends StatelessWidget {
  final AppLocalizations l10n;
  final DeliverableState state;
  final bool busy;
  final VoidCallback? onRatify;
  final VoidCallback? onUnratify;
  const _DeliverableActionBar({
    required this.l10n,
    required this.state,
    required this.busy,
    required this.onRatify,
    required this.onUnratify,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: EdgeInsets.fromLTRB(
        12,
        Spacing.s8,
        12,
        Spacing.s8 + MediaQuery.of(context).padding.bottom,
      ),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        border: Border(
          top: BorderSide(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
      ),
      child: Row(
        children: [
          Expanded(
            child: state == DeliverableState.ratified
                ? OutlinedButton.icon(
                    icon: const Icon(Icons.undo, size: 16),
                    label: Text(l10n.buttonUnratify),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: DesignColors.warning,
                    ),
                    onPressed: onUnratify,
                  )
                : ElevatedButton.icon(
                    icon: const Icon(Icons.check_circle_outline, size: 16),
                    label: Text(l10n.buttonRatify),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: DesignColors.terminalGreen,
                      foregroundColor: Colors.white,
                    ),
                    onPressed: onRatify,
                  ),
          ),
          if (busy) ...[
            const SizedBox(width: 12),
            const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ],
        ],
      ),
    );
  }
}

class _SendBackResult {
  final String note;
  final List<String> annotationIds;
  const _SendBackResult({required this.note, required this.annotationIds});
}

class _SendBackSheet extends StatefulWidget {
  final AppLocalizations l10n;
  final String steward;
  final List<Map<String, dynamic>> annotations;
  const _SendBackSheet({
    required this.l10n,
    required this.steward,
    required this.annotations,
  });

  @override
  State<_SendBackSheet> createState() => _SendBackSheetState();
}

class _SendBackSheetState extends State<_SendBackSheet> {
  final _ctl = TextEditingController();
  final Set<String> _selected = <String>{};

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    final hasAnnotations = widget.annotations.isNotEmpty;
    return Padding(
      padding: EdgeInsets.only(bottom: media.viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: media.size.height * 0.8),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  widget.l10n.sendBackWithNotes,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  widget.l10n.sendBackSheetBody(widget.steward),
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: DesignColors.textMuted,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _ctl,
                  autofocus: true,
                  minLines: 3,
                  maxLines: 6,
                  decoration: InputDecoration(
                    hintText: widget.l10n.sendBackNoteHint,
                    border: const OutlineInputBorder(),
                  ),
                ),
                const SizedBox(height: 12),
                if (hasAnnotations) ...[
                  Text(
                    widget.l10n.attachOpenAnnotations(
                      widget.annotations.length,
                    ),
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.textMuted,
                      letterSpacing: 0.3,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Flexible(
                    child: ListView.builder(
                      shrinkWrap: true,
                      itemCount: widget.annotations.length,
                      itemBuilder: (_, i) {
                        final a = widget.annotations[i];
                        final id = (a['id'] ?? '').toString();
                        final kind = (a['kind'] ?? 'comment').toString();
                        final section = (a['section_slug'] ?? '').toString();
                        final body = (a['body'] ?? '').toString();
                        return CheckboxListTile(
                          dense: true,
                          contentPadding: EdgeInsets.zero,
                          controlAffinity: ListTileControlAffinity.leading,
                          value: _selected.contains(id),
                          onChanged: (on) {
                            setState(() {
                              if (on == true) {
                                _selected.add(id);
                              } else {
                                _selected.remove(id);
                              }
                            });
                          },
                          title: Text(
                            body,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: GoogleFonts.spaceGrotesk(fontSize: 12),
                          ),
                          subtitle: Text(
                            '$kind · $section',
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: FontSizes.label,
                              color: DesignColors.textMuted,
                            ),
                          ),
                        );
                      },
                    ),
                  ),
                ],
                const SizedBox(height: 12),
                Row(
                  mainAxisAlignment: MainAxisAlignment.end,
                  children: [
                    TextButton(
                      onPressed: () => Navigator.of(context).pop(),
                      child: Text(widget.l10n.buttonCancel),
                    ),
                    const SizedBox(width: 8),
                    FilledButton.icon(
                      icon: const Icon(Icons.assignment_return_outlined,
                          size: 16),
                      label: Text(widget.l10n.sendBack),
                      onPressed: () {
                        final note = _ctl.text.trim();
                        if (note.isEmpty) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            SnackBar(
                              content: Text(widget.l10n.noteRequired),
                            ),
                          );
                          return;
                        }
                        Navigator.of(context).pop(_SendBackResult(
                          note: note,
                          annotationIds: _selected.toList(),
                        ));
                      },
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Bottom sheet for `commit` components. The ref_id is opaque from the
/// hub's perspective (no commits table backs it yet), but lifecycle
/// seeds use git-host URLs of the form
/// `https://<host>/<owner>/<repo>/commit/<sha>` so this sheet pulls out
/// the host/repo/short-sha for display and offers open-in-browser +
/// copy-ref buttons. Non-URL refs fall through to a raw view.
class _CommitDetailSheet extends StatelessWidget {
  final AppLocalizations l10n;
  final String project;
  final String ref;
  const _CommitDetailSheet({
    required this.l10n,
    required this.project,
    required this.ref,
  });

  ({String host, String repo, String sha})? _parse() {
    final uri = Uri.tryParse(ref);
    if (uri == null || !uri.hasScheme) return null;
    final segs = uri.pathSegments
        .where((s) => s.isNotEmpty)
        .toList(growable: false);
    // Expected shape: <owner>/<repo>/commit/<sha>
    int idx = segs.indexOf('commit');
    if (idx < 0 || idx >= segs.length - 1 || idx < 2) return null;
    final repo = '${segs[idx - 2]}/${segs[idx - 1]}';
    final sha = segs[idx + 1];
    return (host: uri.host, repo: repo, sha: sha);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final parsed = _parse();
    final shortSha = parsed == null
        ? ''
        : (parsed.sha.length > 7 ? parsed.sha.substring(0, 7) : parsed.sha);
    return DraggableScrollableSheet(
      initialChildSize: 0.45,
      minChildSize: 0.3,
      maxChildSize: 0.85,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: BoxDecoration(
          color: bg,
          borderRadius:
              const BorderRadius.vertical(top: Radius.circular(16)),
        ),
        child: ListView(
          controller: scroll,
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
          children: [
            Center(
              child: Container(
                width: 40,
                height: 4,
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: DesignColors.textMuted.withValues(alpha: 0.4),
                  borderRadius: Radii.xsBorder,
                ),
              ),
            ),
            Row(
              children: [
                const Icon(Icons.commit_outlined,
                    size: 20, color: DesignColors.warning),
                const SizedBox(width: 8),
                Text(
                  l10n.componentKindCommit,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (shortSha.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: Spacing.s8, vertical: 2),
                    decoration: BoxDecoration(
                      color: DesignColors.warning.withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(
                      shortSha,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        color: DesignColors.warning,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 14),
            if (parsed != null) ...[
              _CommitField(label: 'host', value: parsed.host),
              _CommitField(label: 'repo', value: parsed.repo),
              _CommitField(label: 'sha', value: parsed.sha),
            ],
            _CommitField(label: 'ref', value: ref),
            const SizedBox(height: 14),
            Row(
              children: [
                if (parsed != null)
                  Expanded(
                    child: FilledButton.icon(
                      icon: const Icon(Icons.open_in_new, size: 16),
                      label: Text(l10n.openCommit),
                      onPressed: () async {
                        final uri = Uri.tryParse(ref);
                        if (uri == null) return;
                        await launchUrl(uri,
                            mode: LaunchMode.externalApplication);
                      },
                    ),
                  ),
                if (parsed != null) const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton.icon(
                    icon: const Icon(Icons.copy, size: 16),
                    label: Text(l10n.copyRef),
                    onPressed: () async {
                      await Clipboard.setData(ClipboardData(text: ref));
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(content: Text(l10n.copied)),
                        );
                      }
                    },
                  ),
                ),
              ],
            ),
            const SizedBox(height: 14),
            Text(
              l10n.commitComponentDescription(project),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
                height: 1.4,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CommitField extends StatelessWidget {
  final String label;
  final String value;
  const _CommitField({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 60,
            child: Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurface,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
