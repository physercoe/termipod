import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/entity_names.dart';
import '../../theme/design_colors.dart';
import '../../widgets/criterion_state_pip.dart';
import '../../widgets/hub_offline_banner.dart';
import '../deliverables/structured_deliverable_viewer.dart';

/// Project-scoped acceptance-criteria list (chassis-followup wave 1,
/// ADR-024). The deliverable viewer renders criteria inline per
/// deliverable; this screen flattens them for the project so the
/// director can scan "what's still pending" without opening each
/// deliverable in turn. Tapping a criterion opens its parent
/// deliverable's viewer (the criterion-state mutations live there).
class AcceptanceCriteriaScreen extends ConsumerStatefulWidget {
  final String projectId;
  const AcceptanceCriteriaScreen({super.key, required this.projectId});

  @override
  ConsumerState<AcceptanceCriteriaScreen> createState() =>
      _AcceptanceCriteriaScreenState();
}

class _AcceptanceCriteriaScreenState
    extends ConsumerState<AcceptanceCriteriaScreen> {
  List<Map<String, dynamic>> _rows = const [];
  List<Map<String, dynamic>> _deliverables = const [];
  bool _loading = true;
  String? _error;
  DateTime? _staleSince;
  // null = all states; non-null narrows the list to a single state.
  String? _stateFilter;

  static const _states = <String?>[null, 'pending', 'met', 'failed', 'waived'];

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
      final critsFut = client.listProjectCriteriaCached(
        projectId: widget.projectId,
      );
      final delsFut = client.listDeliverablesCached(
        projectId: widget.projectId,
      );
      final crits = await critsFut;
      final dels = await delsFut;
      if (!mounted) return;
      setState(() {
        _rows = crits.body;
        _deliverables = dels.body;
        _staleSince = crits.staleSince ?? dels.staleSince;
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

  /// Group criteria by phase preserving payload order. Within a phase,
  /// pending leads (actionable), then failed (needs attention), then met
  /// + waived (done).
  List<MapEntry<String, List<Map<String, dynamic>>>> _grouped() {
    final filtered = _stateFilter == null
        ? _rows
        : _rows
            .where(
                (c) => (c['state'] ?? '').toString() == _stateFilter)
            .toList();
    final order = <String>[];
    final buckets = <String, List<Map<String, dynamic>>>{};
    for (final c in filtered) {
      final phase = (c['phase'] ?? '').toString();
      if (!buckets.containsKey(phase)) {
        order.add(phase);
        buckets[phase] = <Map<String, dynamic>>[];
      }
      buckets[phase]!.add(c);
    }
    int rank(String s) => switch (s) {
          'pending' => 0,
          'failed' => 1,
          'met' => 2,
          'waived' => 3,
          _ => 4,
        };
    for (final list in buckets.values) {
      list.sort((a, b) {
        final ra = rank((a['state'] ?? '').toString());
        final rb = rank((b['state'] ?? '').toString());
        if (ra != rb) return ra.compareTo(rb);
        final oa = (a['ord'] as num?)?.toInt() ?? 0;
        final ob = (b['ord'] as num?)?.toInt() ?? 0;
        return oa.compareTo(ob);
      });
    }
    return [for (final p in order) MapEntry(p, buckets[p]!)];
  }

  Map<String, dynamic>? _deliverableFor(String? id) {
    if (id == null || id.isEmpty) return null;
    for (final d in _deliverables) {
      if ((d['id'] ?? '').toString() == id) return d;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final projects = ref.watch(hubProvider).value?.projects ?? const [];
    final name = projectNameFor(widget.projectId, projects);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          name.isEmpty ? 'Acceptance' : 'Acceptance · $name',
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
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
          children: [
            HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
            _StateFilterRow(
              current: _stateFilter,
              onChanged: (v) => setState(() => _stateFilter = v),
              states: _states,
            ),
            if (_loading)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(child: CircularProgressIndicator()),
              )
            else if (_error != null)
              _ErrorCard(error: _error!, onRetry: _load)
            else if (_rows.isEmpty)
              const _EmptyCard()
            else
              for (final group in _grouped()) ...[
                Padding(
                  padding: const EdgeInsets.fromLTRB(4, 14, 4, 8),
                  child: Text(
                    _prettyPhase(group.key),
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.textMuted,
                      letterSpacing: 0.6,
                    ),
                  ),
                ),
                for (final c in group.value)
                  _CriterionRow(
                    projectId: widget.projectId,
                    criterion: c,
                    deliverable: _deliverableFor(
                      (c['deliverable_id'] ?? '').toString(),
                    ),
                    onChanged: _load,
                  ),
              ],
          ],
        ),
      ),
    );
  }

  static String _prettyPhase(String slug) {
    if (slug.isEmpty) return 'No phase';
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) =>
            p.isEmpty ? p : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ')
        .toUpperCase();
  }
}

class _StateFilterRow extends StatelessWidget {
  final String? current;
  final ValueChanged<String?> onChanged;
  final List<String?> states;
  const _StateFilterRow({
    required this.current,
    required this.onChanged,
    required this.states,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Row(
          children: [
            for (final s in states) ...[
              ChoiceChip(
                label: Text(s ?? 'All'),
                selected: current == s,
                onSelected: (_) => onChanged(s),
              ),
              const SizedBox(width: 8),
            ],
          ],
        ),
      ),
    );
  }
}

class _CriterionRow extends StatelessWidget {
  final String projectId;
  final Map<String, dynamic> criterion;
  final Map<String, dynamic>? deliverable;
  final VoidCallback onChanged;
  const _CriterionRow({
    required this.projectId,
    required this.criterion,
    required this.deliverable,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final state = parseCriterionState(
        (criterion['state'] ?? '').toString());
    final kind = (criterion['kind'] ?? '').toString();
    final body = (criterion['body'] as Map?)?.cast<String, dynamic>() ??
        const <String, dynamic>{};
    final description = (body['description'] ??
            body['summary'] ??
            body['statement'] ??
            '')
        .toString();
    final required = criterion['required'] == true;
    final deliverableLabel = deliverable == null
        ? null
        : _prettyKind((deliverable!['kind'] ?? '').toString());
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final tappable = deliverable != null;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: Material(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        child: InkWell(
          borderRadius: BorderRadius.circular(10),
          onTap: tappable
              ? () async {
                  await Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => StructuredDeliverableViewer(
                        projectId: projectId,
                        deliverableId:
                            (deliverable!['id'] ?? '').toString(),
                        initialDeliverable: deliverable,
                      ),
                    ),
                  );
                  onChanged();
                }
              : null,
          child: Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(10),
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
                    Expanded(
                      child: Text(
                        description.isEmpty ? _prettyKind(kind) : description,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 14,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    CriterionStatePip(state: state),
                  ],
                ),
                const SizedBox(height: 6),
                Text(
                  [
                    if (kind.isNotEmpty) 'kind: $kind',
                    if (deliverableLabel != null)
                      'under: $deliverableLabel'
                    else
                      'project-level',
                    if (required) 'required',
                  ].join(' · '),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  static String _prettyKind(String slug) {
    if (slug.isEmpty) return 'Criterion';
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) =>
            p.isEmpty ? p : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }
}

class _EmptyCard extends StatelessWidget {
  const _EmptyCard();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Icon(Icons.check_circle_outline, size: 24),
            const SizedBox(height: 8),
            Text(
              'No acceptance criteria yet.',
              style: theme.textTheme.titleSmall,
            ),
            const SizedBox(height: 6),
            Text(
              'Templates declare per-phase criteria; once W7 hydration runs '
              'they appear here, scoped under their parent deliverable.',
              style: theme.textTheme.bodySmall,
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorCard extends StatelessWidget {
  final String error;
  final VoidCallback onRetry;
  const _ErrorCard({required this.error, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: const [
                Icon(Icons.error_outline, size: 20),
                SizedBox(width: 8),
                Text('Could not load criteria'),
              ],
            ),
            const SizedBox(height: 6),
            Text(error, style: const TextStyle(fontSize: 12)),
            const SizedBox(height: 12),
            OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}
