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
        itemBuilder: (_, i) => _ArchivedTile(
          row: _rows[i],
          onTap: () {
            Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ArchivedAgentDetailScreen(summary: _rows[i]),
            ));
          },
        ),
      ),
    );
  }
}

class _ArchivedTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _ArchivedTile({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final handle = (row['handle'] ?? '?').toString();
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final archivedAt = (row['archived_at'] ?? '').toString();
    return Opacity(
      opacity: 0.72,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(10),
          child: Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
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
                    const SizedBox(width: 6),
                    const Icon(Icons.chevron_right,
                        size: 18, color: DesignColors.textMuted),
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
        ),
      ),
    );
  }
}

/// Tombstone detail for an archived agent. Fetches the full agent row
/// (to pick up spawn_spec_yaml) and the journal so operators can audit
/// what the agent actually did before it was put to rest.
///
/// Intentionally read-only — there are no lifecycle actions once an
/// agent is archived; the live _AgentDetailSheet on hub_screen owns
/// pause/resume/terminate for the active list.
class ArchivedAgentDetailScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic> summary;
  const ArchivedAgentDetailScreen({super.key, required this.summary});

  @override
  ConsumerState<ArchivedAgentDetailScreen> createState() =>
      _ArchivedAgentDetailScreenState();
}

class _ArchivedAgentDetailScreenState
    extends ConsumerState<ArchivedAgentDetailScreen> {
  Map<String, dynamic>? _full;
  String? _journal;
  String? _error;
  bool _loading = true;

  String get _id => (widget.summary['id'] ?? '').toString();

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final full = await client.getAgent(_id);
      String? journal;
      try {
        journal = await client.readAgentJournal(_id);
      } catch (_) {
        // Journal may be unreadable or empty on archived agents; don't
        // block the rest of the detail on a journal-fetch failure.
        journal = null;
      }
      if (!mounted) return;
      setState(() {
        _full = full;
        _journal = journal;
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
    final handle = (widget.summary['handle'] ?? _id).toString();
    return Scaffold(
      appBar: AppBar(
        title: Text(
          '@$handle',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(_error!,
                        style: GoogleFonts.jetBrainsMono(
                            color: DesignColors.error, fontSize: 12)),
                  ),
                )
              : _body(),
    );
  }

  Widget _body() {
    final row = _full ?? widget.summary;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final archivedAt = (row['archived_at'] ?? '').toString();
    final terminatedAt = (row['terminated_at'] ?? '').toString();
    final createdAt = (row['created_at'] ?? '').toString();
    final projectId = (row['project_id'] ?? '').toString();
    final spawnSpec = (row['spawn_spec_yaml'] ?? '').toString();
    final spent = (row['spent_cents'] as num?)?.toInt() ?? 0;

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      children: [
        _section(
          'Summary',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _kv('Status', status),
              _kv('Kind', kind),
              if (projectId.isNotEmpty) _kv('Project', projectId),
              if (createdAt.isNotEmpty)
                _kv('Created', _trimIso(createdAt)),
              if (terminatedAt.isNotEmpty)
                _kv('Terminated', _trimIso(terminatedAt)),
              if (archivedAt.isNotEmpty)
                _kv('Archived', _trimIso(archivedAt)),
              _kv('Total spend', '\$${(spent / 100).toStringAsFixed(2)}'),
            ],
          ),
          isDark: isDark,
        ),
        const SizedBox(height: 16),
        if (spawnSpec.isNotEmpty)
          _section(
            'Spawn spec',
            child: SelectableText(
              spawnSpec,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.35),
            ),
            isDark: isDark,
          ),
        if (spawnSpec.isNotEmpty) const SizedBox(height: 16),
        _section(
          'Journal',
          child: (_journal ?? '').trim().isEmpty
              ? Text(
                  '(no journal entries)',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    color: DesignColors.textMuted,
                  ),
                )
              : SelectableText(
                  _journal!,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 12, height: 1.35),
                ),
          isDark: isDark,
        ),
      ],
    );
  }

  Widget _section(String label,
      {required Widget child, required bool isDark}) {
    return Container(
      padding: const EdgeInsets.all(12),
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
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.5,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          child,
        ],
      ),
    );
  }

  Widget _kv(String label, String value) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 96,
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.35),
            ),
          ),
        ],
      ),
    );
  }

  String _trimIso(String iso) =>
      iso.length >= 19 ? iso.substring(0, 19).replaceFirst('T', ' ') : iso;
}
