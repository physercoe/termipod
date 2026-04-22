import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Read-only list of archived agents. Operators archive terminated agents
/// from the detail sheet to declutter the live list; the rows stay in the
/// DB so spawn history and audit rows continue to resolve.
class ArchivedAgentsScreen extends ConsumerStatefulWidget {
  const ArchivedAgentsScreen({super.key});

  @override
  ConsumerState<ArchivedAgentsScreen> createState() =>
      _ArchivedAgentsScreenState();
}

class _ArchivedAgentsScreenState extends ConsumerState<ArchivedAgentsScreen> {
  List<Map<String, dynamic>> _rows = const [];
  bool _loading = false;
  String? _error;

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
      final all = await client.listAgents(includeArchived: true);
      final archived = [
        for (final a in all)
          if (a['archived_at'] != null) a,
      ];
      if (!mounted) return;
      setState(() {
        _rows = archived;
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

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Archived agents',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading && _rows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error!,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.error),
          ),
        ),
      );
    }
    if (_rows.isEmpty) {
      return Center(
        child: Text(
          'No archived agents.\nTerminate an agent, then tap Delete on its detail sheet to move it here.',
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(color: DesignColors.textMuted),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        itemCount: _rows.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _ArchivedTile(row: _rows[i]),
      ),
    );
  }
}

class _ArchivedTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _ArchivedTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final handle = (row['handle'] ?? '?').toString();
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final archivedAt = (row['archived_at'] ?? '').toString();
    return Opacity(
      opacity: 0.72,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color:
                isDark ? DesignColors.borderDark : DesignColors.borderLight,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.inventory_2_outlined,
                    size: 18, color: DesignColors.textMuted),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(handle,
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 14, fontWeight: FontWeight.w600)),
                ),
                Text(status,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 10, color: DesignColors.textMuted)),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              [
                kind,
                if (archivedAt.isNotEmpty)
                  'archived ${archivedAt.substring(0, archivedAt.length >= 19 ? 19 : archivedAt.length)}',
              ].where((s) => s.isNotEmpty).join(' · '),
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.textMuted),
            ),
          ],
        ),
      ),
    );
  }
}
