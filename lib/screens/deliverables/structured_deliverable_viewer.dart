import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
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
        SnackBar(content: Text('Ratify failed: $e')),
      );
    }
  }

  Future<void> _sendBack() async {
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
      builder: (_) => _SendBackSheet(annotations: openAnnotations),
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
        const SnackBar(content: Text('Sent back · steward will revise')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(
        SnackBar(content: Text('Send back failed: $e')),
      );
    }
  }

  Future<void> _unratify() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Unratify deliverable?'),
        content: const Text(
          'This moves the deliverable back to draft. Director-only gesture.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Unratify'),
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
        SnackBar(content: Text('Unratify failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = _deliverable;
    final title = (d?['kind'] ?? 'Deliverable').toString();
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
                  fontSize: 15,
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
              tooltip: 'More',
              onSelected: (v) {
                if (v == 'send_back' && !_busy) _sendBack();
              },
              itemBuilder: (_) => const [
                PopupMenuItem(
                  value: 'send_back',
                  child: ListTile(
                    leading: Icon(Icons.assignment_return_outlined),
                    title: Text('Send back with notes'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
              ],
            ),
        ],
      ),
      body: _error != null
          ? _ErrorView(error: _error!, onRetry: _load)
          : Stack(
              children: [
                ListView(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
                  children: [
                    HubOfflineBanner(
                        staleSince: _staleSince, onRetry: _load),
                    if (phase.isNotEmpty)
                      _MetaRow(label: 'Phase', value: _prettyKind(phase)),
                    _MetaRow(label: 'Kind', value: title),
                    if ((d?['ratified_at'] ?? '').toString().isNotEmpty)
                      _MetaRow(
                        label: 'Ratified',
                        value: (d?['ratified_at'] ?? '').toString(),
                      ),
                    const SizedBox(height: 16),
                    _SectionHeading(label: 'Components (${components.length})'),
                    if (components.isEmpty)
                      _EmptyCard(
                        title: 'No components yet',
                        body:
                            'Components are added when the steward authors '
                            'their underlying documents/artifacts/runs.',
                      )
                    else
                      for (final c in components)
                        _ComponentCard(component: c, projectId: widget.projectId),
                    const SizedBox(height: 24),
                    _SectionHeading(label: 'Criteria (${_criteria.length})'),
                    if (_criteria.isEmpty)
                      _EmptyCard(
                        title: 'No criteria declared',
                        body:
                            'Templates declare criteria per phase. Tap the '
                            'criterion to mark it met / failed / waived.',
                      )
                    else
                      for (final c in _criteria)
                        _CriterionRow(
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

class _ComponentCard extends ConsumerWidget {
  final Map<String, dynamic> component;
  final String projectId;
  const _ComponentCard({required this.component, required this.projectId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final kind = (component['kind'] ?? '').toString();
    final refId = (component['ref_id'] ?? '').toString();
    final required = component['required'] == true;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Material(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        child: InkWell(
          borderRadius: BorderRadius.circular(10),
          onTap: () => _onTap(context, ref),
          child: Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(10),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: Row(
              children: [
                Icon(_iconFor(kind), size: 22, color: _colorFor(kind)),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Text(
                            _labelFor(kind),
                            style: GoogleFonts.spaceGrotesk(
                              fontSize: 12,
                              fontWeight: FontWeight.w700,
                              color: _colorFor(kind),
                              letterSpacing: 0.4,
                            ),
                          ),
                          if (required) ...[
                            const SizedBox(width: 8),
                            Container(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 6, vertical: 2),
                              decoration: BoxDecoration(
                                color:
                                    DesignColors.warning.withValues(alpha: 0.15),
                                borderRadius: BorderRadius.circular(4),
                              ),
                              child: Text(
                                'required',
                                style: GoogleFonts.jetBrainsMono(
                                  fontSize: 9,
                                  color: DesignColors.warning,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                          ],
                        ],
                      ),
                      const SizedBox(height: 2),
                      Text(
                        refId,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 11,
                          color: DesignColors.textMuted,
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

  Future<void> _onTap(BuildContext context, WidgetRef ref) async {
    final kind = (component['kind'] ?? '').toString();
    final refId = (component['ref_id'] ?? '').toString();
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
          SnackBar(content: Text('Could not load artifact $refId')),
        );
      }
      return;
    }
    if (kind == 'commit') {
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        backgroundColor: Colors.transparent,
        builder: (_) => _CommitDetailSheet(ref: refId),
      );
      return;
    }
    // Any future component kind we don't yet render — surface the ref
    // so the user can at least locate it.
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text('${_labelFor(kind)} component → $refId'),
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

  static String _labelFor(String kind) {
    switch (kind) {
      case 'document':
        return 'Document';
      case 'artifact':
        return 'Artifact';
      case 'run':
        return 'Run';
      case 'commit':
        return 'Commit';
      default:
        return 'Component';
    }
  }
}

class _CriterionRow extends ConsumerStatefulWidget {
  final Map<String, dynamic> criterion;
  final String projectId;
  final VoidCallback onChanged;
  const _CriterionRow({
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
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    String? note;
    if (action == 'mark-met' && _kind == 'text') {
      note = await _promptForNote(context, 'Evidence',
          'Optional reference (e.g. document://doc-1#method)');
    } else if (action == 'mark-failed' || action == 'waive') {
      note = await _promptForNote(context, 'Reason',
          'Optional explanation, recorded in the audit log');
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
      messenger.showSnackBar(SnackBar(content: Text('Failed: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final summary = _criterionSummary(_kind, _body);
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
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
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
                            '$_kind · $_state',
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: 10,
                              color: DesignColors.textMuted,
                            ),
                          ),
                          if (isGate) ...[
                            const SizedBox(width: 6),
                            Text(
                              '· auto',
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: 10,
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
                title: const Text('Mark met'),
                onTap: () => Navigator.pop(ctx, 'mark-met'),
              ),
            if (_state != 'failed')
              ListTile(
                leading: const Icon(Icons.cancel, color: DesignColors.error),
                title: const Text('Mark failed'),
                onTap: () => Navigator.pop(ctx, 'mark-failed'),
              ),
            if (_state != 'waived')
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
    if (action != null) await _act(action);
  }

  static Future<String?> _promptForNote(
      BuildContext context, String title, String hint) async {
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
            child: const Text('Skip'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, ctrl.text.trim()),
            child: const Text('OK'),
          ),
        ],
      ),
    );
    return result;
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
      padding: const EdgeInsets.all(14),
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
  final String error;
  final VoidCallback onRetry;
  const _ErrorView({required this.error, required this.onRetry});

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
            OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}

class _DeliverableActionBar extends StatelessWidget {
  final DeliverableState state;
  final bool busy;
  final VoidCallback? onRatify;
  final VoidCallback? onUnratify;
  const _DeliverableActionBar({
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
        10,
        12,
        10 + MediaQuery.of(context).padding.bottom,
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
                    label: const Text('Unratify'),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: DesignColors.warning,
                    ),
                    onPressed: onUnratify,
                  )
                : ElevatedButton.icon(
                    icon: const Icon(Icons.check_circle_outline, size: 16),
                    label: const Text('Ratify deliverable'),
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
  final List<Map<String, dynamic>> annotations;
  const _SendBackSheet({required this.annotations});

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
                  'Send back with notes',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'Steward gets a revision_requested attention item; the deliverable moves to in-review.',
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
                  decoration: const InputDecoration(
                    hintText:
                        'What needs to change before this can be ratified?',
                    border: OutlineInputBorder(),
                  ),
                ),
                const SizedBox(height: 12),
                if (hasAnnotations) ...[
                  Text(
                    'Attach open annotations (${widget.annotations.length})',
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
                              fontSize: 9,
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
                      child: const Text('Cancel'),
                    ),
                    const SizedBox(width: 8),
                    FilledButton.icon(
                      icon: const Icon(Icons.assignment_return_outlined,
                          size: 16),
                      label: const Text('Send back'),
                      onPressed: () {
                        final note = _ctl.text.trim();
                        if (note.isEmpty) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                                content: Text('Note required')),
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
  final String ref;
  const _CommitDetailSheet({required this.ref});

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
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            Row(
              children: [
                const Icon(Icons.commit_outlined,
                    size: 20, color: DesignColors.warning),
                const SizedBox(width: 8),
                Text(
                  'Commit',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (shortSha.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 6, vertical: 2),
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
                      label: const Text('Open commit'),
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
                    label: const Text('Copy ref'),
                    onPressed: () async {
                      await Clipboard.setData(ClipboardData(text: ref));
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(content: Text('Copied')),
                        );
                      }
                    },
                  ),
                ),
              ],
            ),
            const SizedBox(height: 14),
            Text(
              'Commit components pin a deliverable to a specific revision of the project repo so reviewers can rebuild the same artifacts/runs from source.',
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
