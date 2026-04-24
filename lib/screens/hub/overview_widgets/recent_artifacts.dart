import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../artifacts_screen.dart';
import 'registry.dart';

/// `recent_artifacts` hero — newest-first stream of outputs produced by
/// this project (figures, checkpoints, eval curves, reports, etc.).
/// Surfaces the right question for reproduction and memo projects:
/// "what's been produced so far?"
///
/// Caps at [_cap] rows with an overflow indicator; tapping the full-list
/// shortcut is the Outputs shortcut in the Overview body below (so this
/// hero stays focused on the scan, not the manage).
class RecentArtifactsHero extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  const RecentArtifactsHero({super.key, required this.ctx});

  @override
  ConsumerState<RecentArtifactsHero> createState() =>
      _RecentArtifactsHeroState();
}

class _RecentArtifactsHeroState
    extends ConsumerState<RecentArtifactsHero> {
  static const int _cap = 10;

  List<Map<String, dynamic>>? _rows;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final projectId = widget.ctx.projectId;
    if (client == null || projectId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final cached =
          await client.listArtifactsCached(projectId: projectId);
      if (!mounted) return;
      setState(() {
        _rows = cached.body;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 24),
        child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    final rows = _rows ?? const <Map<String, dynamic>>[];
    if (rows.isEmpty) {
      return _EmptyCard(projectId: widget.ctx.projectId);
    }
    // Hub already orders by created_at desc; defensive sort in case.
    final sorted = [...rows];
    sorted.sort((a, b) {
      final ac = (a['created_at'] ?? '').toString();
      final bc = (b['created_at'] ?? '').toString();
      return bc.compareTo(ac);
    });
    final capped = sorted.take(_cap).toList();
    final overflow = sorted.length - capped.length;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 4),
          child: Text(
            'RECENT OUTPUTS',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.5,
              color: DesignColors.textMuted,
            ),
          ),
        ),
        for (final row in capped) _ArtifactLine(row: row),
        if (overflow > 0)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: InkWell(
              onTap: () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) =>
                    ArtifactsScreen(projectId: widget.ctx.projectId),
              )),
              child: Text(
                '+ $overflow more — open Outputs',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.primary,
                  fontStyle: FontStyle.italic,
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class _ArtifactLine extends StatelessWidget {
  final Map<String, dynamic> row;
  const _ArtifactLine({required this.row});

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '(unnamed)').toString();
    final kind = (row['kind'] ?? '').toString();
    final created = (row['created_at'] ?? '').toString();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          ArtifactKindChip(kind: kind),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          const SizedBox(width: 8),
          Text(
            _shortDate(created),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }

  /// Very coarse "yyyy-MM-dd" slice. Good enough for the scan view;
  /// full timestamps live on the artifact detail sheet.
  String _shortDate(String iso) {
    if (iso.length < 10) return iso;
    return iso.substring(0, 10);
  }
}

class _EmptyCard extends StatelessWidget {
  final String projectId;
  const _EmptyCard({required this.projectId});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 16),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Text(
        'No outputs yet. Runs will surface figures, checkpoints, and reports here as they land.',
        style: GoogleFonts.spaceGrotesk(
          fontSize: 12,
          color: DesignColors.textMuted,
        ),
      ),
    );
  }
}
