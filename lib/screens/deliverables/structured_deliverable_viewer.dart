import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/deliverable_state_pip.dart';
import '../projects/documents_screen.dart' show DocumentDetailScreen;

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
      final d = await client.getDeliverable(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      final crits = await client.listProjectCriteria(
        projectId: widget.projectId,
        deliverableId: widget.deliverableId,
      );
      if (!mounted) return;
      setState(() {
        _deliverable = d;
        _criteria = crits;
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
      ),
      body: _error != null
          ? _ErrorView(error: _error!, onRetry: _load)
          : Stack(
              children: [
                ListView(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
                  children: [
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
                            'Templates declare criteria per phase; the '
                            'criteria runtime lands with W6.',
                      )
                    else
                      for (final c in _criteria) _CriterionRow(criterion: c),
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

class _ComponentCard extends StatelessWidget {
  final Map<String, dynamic> component;
  final String projectId;
  const _ComponentCard({required this.component, required this.projectId});

  @override
  Widget build(BuildContext context) {
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
          onTap: () => _onTap(context),
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

  void _onTap(BuildContext context) {
    final kind = (component['kind'] ?? '').toString();
    final refId = (component['ref_id'] ?? '').toString();
    if (kind == 'document' && refId.isNotEmpty) {
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => DocumentDetailScreen(documentId: refId),
        ),
      );
      return;
    }
    // Artifact / run / commit components don't yet have dedicated viewer
    // routes from the deliverable surface; surface the ref so the user can
    // jump to the right tab.
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          '${_labelFor(kind)} component → $refId',
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

class _CriterionRow extends StatelessWidget {
  final Map<String, dynamic> criterion;
  const _CriterionRow({required this.criterion});

  @override
  Widget build(BuildContext context) {
    final kind = (criterion['kind'] ?? '').toString();
    final state = (criterion['state'] ?? 'pending').toString();
    final body = (criterion['body'] is Map)
        ? (criterion['body'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};
    final summary = _criterionSummary(kind, body);
    final color = _stateColor(state);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 10,
              height: 10,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                color: state == 'met' ? color : null,
                border: state == 'met'
                    ? null
                    : Border.all(color: color, width: 1.5),
              ),
            ),
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
                  Text(
                    '$kind · $state',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
            ),
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

  static Color _stateColor(String state) {
    switch (state) {
      case 'met':
        return DesignColors.terminalGreen;
      case 'failed':
        return DesignColors.error;
      case 'waived':
        return DesignColors.textMuted;
      default:
        return DesignColors.warning;
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
