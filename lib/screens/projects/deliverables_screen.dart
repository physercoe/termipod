import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/entity_names.dart';
import '../../theme/design_colors.dart';
import '../../widgets/deliverable_state_pip.dart';
import '../../widgets/hub_offline_banner.dart';
import '../deliverables/structured_deliverable_viewer.dart';

/// Project-scoped deliverables list (chassis-followup wave 1, ADR-024).
///
/// PhaseSummaryScreen lists a single phase's deliverables off the phase
/// ribbon; this screen lists the whole project's deliverables grouped by
/// phase so directors can find any ratified/in-flight artifact without
/// the per-phase navigation step. Backs the `deliverables` tile slug.
class DeliverablesScreen extends ConsumerStatefulWidget {
  final String projectId;
  const DeliverablesScreen({super.key, required this.projectId});

  @override
  ConsumerState<DeliverablesScreen> createState() => _DeliverablesScreenState();
}

class _DeliverablesScreenState extends ConsumerState<DeliverablesScreen> {
  List<Map<String, dynamic>> _rows = const [];
  bool _loading = true;
  String? _error;
  DateTime? _staleSince;

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
      final cached = await client.listDeliverablesCached(
        projectId: widget.projectId,
        includeComponents: true,
      );
      if (!mounted) return;
      setState(() {
        _rows = cached.body;
        _staleSince = cached.staleSince;
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

  /// Group by phase preserving the order phases first appear in the
  /// payload — the hub returns them in declared phase order, so we keep
  /// that without imposing a separate sort. Within a phase, ratified
  /// trails draft/in-review so the actionable items lead.
  List<MapEntry<String, List<Map<String, dynamic>>>> _grouped() {
    final order = <String>[];
    final buckets = <String, List<Map<String, dynamic>>>{};
    for (final d in _rows) {
      final phase = (d['phase'] ?? '').toString();
      if (!buckets.containsKey(phase)) {
        order.add(phase);
        buckets[phase] = <Map<String, dynamic>>[];
      }
      buckets[phase]!.add(d);
    }
    int rank(String s) => switch (s) {
          'draft' => 0,
          'in-review' => 1,
          'ratified' => 2,
          _ => 3,
        };
    for (final list in buckets.values) {
      list.sort((a, b) {
        final ra = rank((a['ratification_state'] ?? '').toString());
        final rb = rank((b['ratification_state'] ?? '').toString());
        if (ra != rb) return ra.compareTo(rb);
        return (a['kind'] ?? '')
            .toString()
            .compareTo((b['kind'] ?? '').toString());
      });
    }
    return [for (final p in order) MapEntry(p, buckets[p]!)];
  }

  @override
  Widget build(BuildContext context) {
    final projects = ref.watch(hubProvider).value?.projects ?? const [];
    final name = projectNameFor(widget.projectId, projects);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          name.isEmpty ? 'Deliverables' : 'Deliverables · $name',
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
                for (final d in group.value)
                  _DeliverableRow(
                    projectId: widget.projectId,
                    deliverable: d,
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

class _DeliverableRow extends StatelessWidget {
  final String projectId;
  final Map<String, dynamic> deliverable;
  final VoidCallback onChanged;
  const _DeliverableRow({
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
    final required = deliverable['required'] == true;
    final components = (deliverable['components'] as List? ?? const []);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: Material(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        child: InkWell(
          borderRadius: BorderRadius.circular(10),
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
                        _prettyKind(kind),
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 14,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                    DeliverableStatePip(state: state),
                  ],
                ),
                const SizedBox(height: 6),
                Text(
                  '${components.length} component${components.length == 1 ? '' : 's'}'
                  '${required ? ' · required' : ''}',
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
    if (slug.isEmpty) return 'Deliverable';
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
            const Icon(Icons.hourglass_empty, size: 24),
            const SizedBox(height: 8),
            Text(
              'No deliverables yet.',
              style: theme.textTheme.titleSmall,
            ),
            const SizedBox(height: 6),
            Text(
              'Templates instantiate per-phase deliverables as the project '
              'advances. Once a phase is entered, its deliverables land here.',
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
                Text('Could not load deliverables'),
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
