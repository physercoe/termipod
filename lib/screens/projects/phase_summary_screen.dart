import 'package:flutter/material.dart';

/// Stub destination for taps on the Project Detail phase ribbon (W1).
///
/// W5b lands the real surface — the Structured Deliverable Viewer (A5)
/// composes deliverables, components, and per-criterion pips. Until then
/// this placeholder satisfies the W1 acceptance criterion that "tapping
/// a phase chip routes correctly to a phase summary screen" without
/// pretending to render content that doesn't exist yet.
class PhaseSummaryScreen extends StatelessWidget {
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
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Text(_pretty(phase)),
      ),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(projectName,
                style: theme.textTheme.titleMedium
                    ?.copyWith(fontWeight: FontWeight.w700)),
            const SizedBox(height: 4),
            Text(
              isCurrent ? 'Current phase' : 'Phase summary',
              style: theme.textTheme.bodyMedium
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: 24),
            Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Icon(Icons.construction, size: 32),
                    const SizedBox(height: 8),
                    Text(
                      'Structured Deliverable Viewer pending (W5b).',
                      style: theme.textTheme.titleSmall,
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Once the deliverables + criteria APIs land, this '
                      'screen will surface this phase\'s deliverables, '
                      'their components, and the criteria gating advance.',
                      style: theme.textTheme.bodySmall,
                    ),
                  ],
                ),
              ),
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
