import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/deliverable_state_pip.dart';
import '../deliverables/structured_deliverable_viewer.dart';

/// W5b — Phase summary screen (A5 §3). Lands when the director taps a
/// phase chip on the project ribbon. Lists the phase's deliverables; tap
/// → Structured Deliverable Viewer. When a single deliverable exists for
/// the phase, the caller may bypass this screen and push the viewer
/// directly (this screen handles the N=0 / N≥2 cases gracefully too).
class PhaseSummaryScreen extends ConsumerStatefulWidget {
  final String projectId;
  final String projectName;
  final String phase;
  final bool isCurrent;

  const PhaseSummaryScreen({
    super.key,
    required this.projectId,
    required this.projectName,
    required this.phase,
    required this.isCurrent,
  });

  @override
  ConsumerState<PhaseSummaryScreen> createState() => _PhaseSummaryScreenState();
}

class _PhaseSummaryScreenState extends ConsumerState<PhaseSummaryScreen> {
  List<Map<String, dynamic>> _deliverables = const [];
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
      final items = await client.listDeliverables(
        projectId: widget.projectId,
        phase: widget.phase,
        includeComponents: true,
      );
      if (!mounted) return;
      setState(() {
        _deliverables = items;
        _loading = false;
        _error = null;
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
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Text(_pretty(widget.phase)),
      ),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
          children: [
            Text(widget.projectName,
                style: theme.textTheme.titleMedium
                    ?.copyWith(fontWeight: FontWeight.w700)),
            const SizedBox(height: 4),
            Text(
              widget.isCurrent ? 'Current phase' : 'Phase summary',
              style: theme.textTheme.bodyMedium
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: 16),
            if (_loading)
              const Center(child: Padding(
                padding: EdgeInsets.all(24),
                child: CircularProgressIndicator(),
              ))
            else if (_error != null)
              _ErrorCard(error: _error!, onRetry: _load)
            else if (_deliverables.isEmpty)
              _EmptyCard()
            else
              for (final d in _deliverables)
                _DeliverableTile(
                  projectId: widget.projectId,
                  deliverable: d,
                  onChanged: _load,
                ),
          ],
        ),
      ),
    );
  }

  static String _pretty(String slug) {
    if (slug.isEmpty) return 'Phase';
    final parts = slug.split(RegExp(r'[-_]'));
    return parts
        .map((p) => p.isEmpty
            ? p
            : '${p[0].toUpperCase()}${p.substring(1)}')
        .join(' ');
  }
}

class _DeliverableTile extends StatelessWidget {
  final String projectId;
  final Map<String, dynamic> deliverable;
  final VoidCallback onChanged;
  const _DeliverableTile({
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
      padding: const EdgeInsets.symmetric(vertical: 6),
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
              'No deliverables for this phase yet.',
              style: theme.textTheme.titleSmall,
            ),
            const SizedBox(height: 6),
            Text(
              'Templates declare per-phase deliverables; once W7 ships the '
              'research template content, advancing into a phase will '
              'instantiate them.',
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
