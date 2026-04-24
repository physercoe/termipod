import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/hub_offline_banner.dart';

/// Artifacts browser (blueprint §6.6).
///
/// Artifacts are content-addressed outputs — checkpoints, eval curves,
/// logs, datasets, reports. They are the "output" axis alongside Files
/// (inputs) and Documents (authored writeups). This screen is the human
/// surface for "what did my runs produce?"
class ArtifactsScreen extends ConsumerStatefulWidget {
  /// Optional project scope. Null = all team artifacts.
  final String? projectId;

  /// Optional run scope. When set, list is filtered to this run and the
  /// project filter is ignored at the query level.
  final String? runId;

  const ArtifactsScreen({super.key, this.projectId, this.runId});

  @override
  ConsumerState<ArtifactsScreen> createState() => _ArtifactsScreenState();
}

class _ArtifactsScreenState extends ConsumerState<ArtifactsScreen> {
  String? _kind; // null = all
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;
  DateTime? _staleSince;

  // Common kinds (per §6.6 + mock-trainer output vocabulary). The filter
  // still honors any kind found in the data; these are just the pills.
  static const _kinds = <String?>[
    null,
    'checkpoint',
    'eval_curve',
    'report',
    'log',
    'dataset',
    'figure',
    'sample',
  ];

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
      final cached = await client.listArtifactsCached(
        projectId: widget.runId == null ? widget.projectId : null,
        runId: widget.runId,
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

  List<Map<String, dynamic>> get _filtered {
    final rows = _rows ?? const [];
    if (_kind == null) return rows;
    return rows
        .where((r) => (r['kind'] ?? '').toString() == _kind)
        .toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    final titleScope = widget.runId != null
        ? 'run ${_short(widget.runId!)}'
        : widget.projectId != null
            ? 'project ${_short(widget.projectId!)}'
            : null;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          titleScope == null ? 'Artifacts' : 'Artifacts · $titleScope',
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
      body: Column(
        children: [
          const _OutputsGuidance(),
          _KindBar(
            kinds: _kinds,
            selected: _kind,
            onChanged: (v) => setState(() => _kind = v),
          ),
          HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final rows = _filtered;
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _rows == null || _rows!.isEmpty
                ? 'No outputs yet.\nAgents attach checkpoints, eval curves,\n'
                    'and reports here as runs complete.'
                : 'No ${_kind ?? "artifacts"} match.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 4),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) => _ArtifactRow(row: rows[i]),
      ),
    );
  }
}

/// One-line role banner mirroring Files/Assets. "Outputs" is the user-facing
/// word; "artifacts" is the primitive name. Keep both so the decision map
/// (Files/Documents/Assets/Outputs) stays legible.
class _OutputsGuidance extends StatelessWidget {
  const _OutputsGuidance();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.output_outlined,
              size: 18, color: DesignColors.primary),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Outputs agents produce',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, fontWeight: FontWeight.w700),
                ),
                const SizedBox(height: 2),
                Text(
                  'Checkpoints, eval curves, logs, reports from runs. '
                  'Content-addressed with lineage — safe to share across teams.',
                  style: GoogleFonts.spaceGrotesk(fontSize: 11, color: muted),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _KindBar extends StatelessWidget {
  final List<String?> kinds;
  final String? selected;
  final ValueChanged<String?> onChanged;
  const _KindBar({
    required this.kinds,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Row(
        children: [
          for (final k in kinds) ...[
            _Pill(
              label: k ?? 'all',
              selected: k == selected,
              onTap: () => onChanged(k),
            ),
            const SizedBox(width: 6),
          ],
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _Pill({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: selected ? DesignColors.primary : DesignColors.borderDark,
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _ArtifactRow extends StatelessWidget {
  final Map<String, dynamic> row;
  const _ArtifactRow({required this.row});

  @override
  Widget build(BuildContext context) {
    final kind = (row['kind'] ?? '').toString();
    final name = (row['name'] ?? '(unnamed)').toString();
    final runId = (row['run_id'] ?? '').toString();
    final size = row['size'];
    final created = (row['created_at'] ?? '').toString();
    final uri = (row['uri'] ?? '').toString();
    return ListTile(
      title: Row(
        children: [
          ArtifactKindChip(kind: kind),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              name,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (size is int)
            Text(
              _formatSize(size),
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.textMuted,
              ),
            ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          [
            if (runId.isNotEmpty) 'run ${_short(runId)}',
            if (created.isNotEmpty) created,
            if (uri.isNotEmpty) uri,
          ].join(' · '),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
      onTap: () => _showDetail(context, row),
    );
  }
}

class ArtifactKindChip extends StatelessWidget {
  final String kind;
  const ArtifactKindChip({super.key, required this.kind});

  @override
  Widget build(BuildContext context) {
    final k = kind.toLowerCase();
    final color = switch (k) {
      'checkpoint' => DesignColors.terminalBlue,
      'eval_curve' => DesignColors.terminalCyan,
      'report' => DesignColors.primary,
      'log' => DesignColors.textMuted,
      'dataset' => DesignColors.terminalGreen,
      'figure' => DesignColors.terminalMagenta,
      'sample' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        k.isEmpty ? '?' : k,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

void _showDetail(BuildContext context, Map<String, dynamic> row) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (_) => _ArtifactDetailSheet(row: row),
  );
}

class _ArtifactDetailSheet extends StatelessWidget {
  final Map<String, dynamic> row;
  const _ArtifactDetailSheet({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final name = (row['name'] ?? '(unnamed)').toString();
    final kind = (row['kind'] ?? '').toString();
    final entries = <MapEntry<String, String>>[
      MapEntry('id', (row['id'] ?? '').toString()),
      MapEntry('project', (row['project_id'] ?? '').toString()),
      MapEntry('run', (row['run_id'] ?? '').toString()),
      MapEntry('uri', (row['uri'] ?? '').toString()),
      MapEntry('sha256', (row['sha256'] ?? '').toString()),
      MapEntry('size', row['size']?.toString() ?? ''),
      MapEntry('mime', (row['mime'] ?? '').toString()),
      MapEntry('producer', (row['producer_agent_id'] ?? '').toString()),
      MapEntry('created', (row['created_at'] ?? '').toString()),
      MapEntry('lineage', (row['lineage_json'] ?? '').toString()),
    ].where((e) => e.value.isNotEmpty).toList(growable: false);
    return DraggableScrollableSheet(
      initialChildSize: 0.55,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: BoxDecoration(
          color: bg,
          borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
        ),
        child: ListView(
          controller: scroll,
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
          children: [
            Center(
              child: Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: DesignColors.textMuted.withValues(alpha: 0.4),
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                ArtifactKindChip(kind: kind),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    name,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 15,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            for (final e in entries)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      e.key,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        fontWeight: FontWeight.w700,
                        letterSpacing: 0.5,
                        color: DesignColors.textMuted,
                      ),
                    ),
                    const SizedBox(height: 2),
                    SelectableText(
                      e.value,
                      style: GoogleFonts.jetBrainsMono(fontSize: 12),
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

String _short(String id) =>
    id.length <= 8 ? id : '${id.substring(0, 6)}…${id.substring(id.length - 2)}';

String _formatSize(int bytes) {
  if (bytes < 1024) return '${bytes}B';
  final kb = bytes / 1024;
  if (kb < 1024) return '${kb.toStringAsFixed(1)}KB';
  final mb = kb / 1024;
  if (mb < 1024) return '${mb.toStringAsFixed(1)}MB';
  final gb = mb / 1024;
  return '${gb.toStringAsFixed(1)}GB';
}
