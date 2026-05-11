import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../models/artifact_kinds.dart';
import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/hub_offline_banner.dart';
import '../projects/artifacts_screen.dart' show showArtifactDetailSheet;

/// Project-scoped artifact list filtered by closed-set kind (wave 2 W3
/// of artifact-type-registry). Used by the References tile to surface
/// tabular citation rows directly instead of falling through to
/// DocumentsScreen.
///
/// The schema filter is purely client-side today — the hub list
/// endpoint accepts a single `kind=` query param; tighter discriminators
/// (`schema=citation`) filter on the mobile after fetch. Q6 option (a):
/// MIME's `schema` param is the canonical discriminator.
class ArtifactsByKindScreen extends ConsumerStatefulWidget {
  final String projectId;
  final ArtifactKind kind;
  final String? schema;
  final String? title;

  const ArtifactsByKindScreen({
    super.key,
    required this.projectId,
    required this.kind,
    this.schema,
    this.title,
  });

  @override
  ConsumerState<ArtifactsByKindScreen> createState() =>
      _ArtifactsByKindScreenState();
}

class _ArtifactsByKindScreenState
    extends ConsumerState<ArtifactsByKindScreen> {
  List<Map<String, dynamic>>? _rows;
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
      final cached = await client.listArtifactsCached(
        projectId: widget.projectId,
        kind: widget.kind.slug,
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
    final schema = widget.schema?.toLowerCase();
    if (schema == null) return rows;
    return rows.where((r) {
      final mime = (r['mime'] ?? '').toString().toLowerCase();
      return mime.contains('schema=$schema');
    }).toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    final spec = kArtifactKindSpecs[widget.kind]!;
    final title = widget.title ??
        (widget.schema == null
            ? spec.label
            : '${spec.label} · ${widget.schema}');
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
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
          HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
          Expanded(child: _body(spec)),
        ],
      ),
    );
  }

  Widget _body(ArtifactKindSpec spec) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
              fontSize: 12, color: DesignColors.error),
        ),
      );
    }
    final rows = _filtered;
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            widget.schema == null
                ? 'No ${spec.label.toLowerCase()} artifacts yet.'
                : 'No ${spec.label.toLowerCase()} · ${widget.schema} '
                    'artifacts yet.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, color: DesignColors.textMuted),
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
        itemBuilder: (_, i) => _Row(row: rows[i], spec: spec),
      ),
    );
  }
}

class _Row extends StatelessWidget {
  final Map<String, dynamic> row;
  final ArtifactKindSpec spec;
  const _Row({required this.row, required this.spec});

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '(unnamed)').toString();
    final mime = (row['mime'] ?? '').toString();
    final created = (row['created_at'] ?? '').toString();
    return ListTile(
      leading: Icon(spec.icon, size: 20, color: DesignColors.textMuted),
      title: Text(
        name,
        style: GoogleFonts.spaceGrotesk(
            fontSize: 13, fontWeight: FontWeight.w600),
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        [if (mime.isNotEmpty) mime, if (created.isNotEmpty) created]
            .join(' · '),
        style: GoogleFonts.jetBrainsMono(
            fontSize: 10, color: DesignColors.textMuted),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      onTap: () => showArtifactDetailSheet(context, row),
    );
  }
}
