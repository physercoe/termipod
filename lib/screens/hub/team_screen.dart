import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'audit_screen.dart';
import 'budget_screen.dart';
import 'plans_screen.dart';
import 'schedules_screen.dart';
import 'team_channel_screen.dart';
import 'tokens_screen.dart';

/// Team-level surface. Four sub-tabs:
///   - Members — coalesced principals (one row per `scope.handle`).
///   - Policies — read-only view of the current policy.yaml.
///   - Channels — team-scope channels (project_id NULL).
///   - Settings — placeholder; editable team config lands in roadmap.
///
/// Pill-style tabs so it doesn't look like yet-another Material TabBar at
/// the top of the Hub screen.
class TeamScreen extends ConsumerStatefulWidget {
  const TeamScreen({super.key});

  @override
  ConsumerState<TeamScreen> createState() => _TeamScreenState();
}

class _TeamScreenState extends ConsumerState<TeamScreen> {
  int _tab = 0;

  static const _labels = ['Members', 'Policies', 'Channels', 'Settings'];

  @override
  Widget build(BuildContext context) {
    final teamId = ref.watch(hubProvider).value?.config?.teamId ?? '';
    return Scaffold(
      appBar: AppBar(
        title: Text(
          teamId.isEmpty ? 'Team' : 'Team · $teamId',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
      ),
      body: Column(
        children: [
          _PillTabs(
            labels: _labels,
            selected: _tab,
            onChanged: (i) => setState(() => _tab = i),
          ),
          Expanded(
            child: IndexedStack(
              index: _tab,
              children: const [
                _MembersView(),
                _PoliciesView(),
                _ChannelsView(),
                _SettingsView(),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PillTabs extends StatelessWidget {
  final List<String> labels;
  final int selected;
  final ValueChanged<int> onChanged;
  const _PillTabs({
    required this.labels,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Row(
          children: [
            for (var i = 0; i < labels.length; i++) ...[
              _Pill(
                label: labels[i],
                selected: selected == i,
                onTap: () => onChanged(i),
              ),
              const SizedBox(width: 8),
            ],
          ],
        ),
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
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary
              : (isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: selected
                ? Colors.white
                : (isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight),
          ),
        ),
      ),
    );
  }
}

// ---- Members ----

class _MembersView extends ConsumerStatefulWidget {
  const _MembersView();

  @override
  ConsumerState<_MembersView> createState() => _MembersViewState();
}

class _MembersViewState extends ConsumerState<_MembersView> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listPrincipals();
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading && _rows == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _ErrorText(text: _error!);
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) return const _EmptyText(text: 'No principals');
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _MemberTile(row: rows[i]),
      ),
    );
  }
}

class _MemberTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _MemberTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final handle = (row['handle'] ?? '').toString();
    final unnamed = row['unnamed'] == true;
    final count = (row['token_count'] is num)
        ? (row['token_count'] as num).toInt()
        : int.tryParse('${row['token_count'] ?? 0}') ?? 0;
    final issued = (row['first_issued_at'] ?? '').toString();
    final label = unnamed ? 'principal (unnamed)' : '@$handle';

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color:
              isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor: DesignColors.primary.withValues(alpha: 0.2),
            child: Icon(unnamed ? Icons.person_outline : Icons.person,
                size: 18, color: DesignColors.primary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w600)),
                if (issued.isNotEmpty)
                  Text('since ${_shortTs(issued)}',
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 10,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight)),
              ],
            ),
          ),
          _CountBadge(count: count),
        ],
      ),
    );
  }
}

class _CountBadge extends StatelessWidget {
  final int count;
  const _CountBadge({required this.count});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: DesignColors.primary.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        '$count token${count == 1 ? '' : 's'}',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.primary,
        ),
      ),
    );
  }
}

// ---- Policies ----

/// Live view + editor for <dataRoot>/team/policy.yaml. PUT triggers an
/// in-memory reload on the hub — no daemon restart required.
class _PoliciesView extends ConsumerStatefulWidget {
  const _PoliciesView();

  @override
  ConsumerState<_PoliciesView> createState() => _PoliciesViewState();
}

class _PoliciesViewState extends ConsumerState<_PoliciesView> {
  final TextEditingController _ctrl = TextEditingController();
  String _lastSaved = '';
  bool _loading = false;
  bool _saving = false;
  String? _error;
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final body = await client.getPolicy();
      if (!mounted) return;
      setState(() {
        _ctrl.text = body;
        _lastSaved = body;
        _loaded = true;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _save() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final body = _ctrl.text;
    setState(() => _saving = true);
    try {
      await client.putPolicy(body);
      if (!mounted) return;
      setState(() {
        _lastSaved = body;
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Policy saved · hub reloaded in-place')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Save failed: $e')),
      );
    }
  }

  void _discard() {
    setState(() => _ctrl.text = _lastSaved);
  }

  @override
  Widget build(BuildContext context) {
    if (!_loaded && _loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && !_loaded) {
      return _ErrorText(text: _error!);
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final dirty = _ctrl.text != _lastSaved;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: DesignColors.primary.withValues(alpha: 0.08),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Row(
              children: [
                const Icon(Icons.info_outline,
                    size: 16, color: DesignColors.primary),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'Saved changes apply immediately — no hub-server restart.',
                    style: GoogleFonts.spaceGrotesk(fontSize: 12),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 10),
          Expanded(
            child: Container(
              decoration: BoxDecoration(
                color: isDark
                    ? DesignColors.surfaceDark
                    : DesignColors.surfaceLight,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                  color: isDark
                      ? DesignColors.borderDark
                      : DesignColors.borderLight,
                ),
              ),
              padding: const EdgeInsets.all(10),
              child: TextField(
                controller: _ctrl,
                maxLines: null,
                expands: true,
                textAlignVertical: TextAlignVertical.top,
                keyboardType: TextInputType.multiline,
                style: GoogleFonts.jetBrainsMono(fontSize: 13),
                decoration: const InputDecoration(
                  isCollapsed: true,
                  border: InputBorder.none,
                  hintText:
                      '# Example:\ntiers:\n  spawn: moderate\napprovers:\n  moderate: ["@steward"]\n',
                ),
                onChanged: (_) => setState(() {}),
              ),
            ),
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              TextButton.icon(
                onPressed: _loading || _saving ? null : _load,
                icon: const Icon(Icons.refresh, size: 18),
                label: const Text('Reload'),
              ),
              const Spacer(),
              TextButton(
                onPressed: !dirty || _saving ? null : _discard,
                child: const Text('Discard'),
              ),
              const SizedBox(width: 8),
              FilledButton.icon(
                onPressed: !dirty || _saving ? null : _save,
                icon: _saving
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.save, size: 18),
                label: const Text('Save'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ---- Channels ----

class _ChannelsView extends ConsumerStatefulWidget {
  const _ChannelsView();

  @override
  ConsumerState<_ChannelsView> createState() => _ChannelsViewState();
}

class _ChannelsViewState extends ConsumerState<_ChannelsView> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listTeamChannels();
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _createChannel() async {
    final name = await _promptChannelName(context);
    if (name == null || name.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.createTeamChannel(name);
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Create channel failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading && _rows == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) return _ErrorText(text: _error!);
    final rows = _rows ?? const [];
    return Stack(
      children: [
        Positioned.fill(
          child: rows.isEmpty
              ? const _EmptyText(text: 'No team channels')
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView.separated(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
                    itemCount: rows.length,
                    separatorBuilder: (_, __) => const SizedBox(height: 8),
                    itemBuilder: (_, i) => _ChannelTile(row: rows[i]),
                  ),
                ),
        ),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.small(
            heroTag: 'team-channels-fab',
            onPressed: _createChannel,
            child: const Icon(Icons.add),
          ),
        ),
      ],
    );
  }
}

class _ChannelTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _ChannelTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final name = (row['name'] ?? '?').toString();
    final id = (row['id'] ?? '').toString();
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => TeamChannelScreen(channelId: id, channelName: name),
        ));
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag, size: 18, color: DesignColors.primary),
            const SizedBox(width: 10),
            Expanded(
              child: Text(name,
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 14, fontWeight: FontWeight.w600)),
            ),
            Icon(Icons.chevron_right,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight),
          ],
        ),
      ),
    );
  }
}

Future<String?> _promptChannelName(BuildContext context) async {
  final ctrl = TextEditingController();
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('New team channel'),
      content: TextField(
        controller: ctrl,
        autofocus: true,
        decoration: const InputDecoration(
          labelText: 'Name (e.g. announcements)',
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(ctx, false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          child: const Text('Create'),
        ),
      ],
    ),
  );
  return ok == true ? ctrl.text.trim() : null;
}

// ---- Settings (placeholder) ----

class _SettingsView extends StatelessWidget {
  const _SettingsView();

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      children: [
        ListTile(
          leading: const Icon(Icons.schedule),
          title: const Text('Schedules'),
          subtitle: const Text('Cron-driven agent spawns'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
            builder: (_) => const SchedulesScreen(),
          )),
        ),
        ListTile(
          leading: const Icon(Icons.account_tree_outlined),
          title: const Text('Plans'),
          subtitle: const Text('Phased scaffolds driven by stewards'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
            builder: (_) => const PlansScreen(),
          )),
        ),
        ListTile(
          leading: const Icon(Icons.account_balance_wallet_outlined),
          title: const Text('Usage'),
          subtitle: const Text('Agent budgets and spend'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
            builder: (_) => const BudgetScreen(),
          )),
        ),
        ListTile(
          leading: const Icon(Icons.history),
          title: const Text('Audit Log'),
          subtitle: const Text('Sensitive action history'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
            builder: (_) => const AuditScreen(),
          )),
        ),
        ListTile(
          leading: const Icon(Icons.key),
          title: const Text('Tokens'),
          subtitle: const Text('Invite humans, rotate host/agent tokens'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
            builder: (_) => const TokensScreen(),
          )),
        ),
      ],
    );
  }
}

// ---- shared small widgets ----

class _EmptyText extends StatelessWidget {
  final String text;
  const _EmptyText({required this.text});
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          text,
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 13,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
        ),
      ),
    );
  }
}

class _ErrorText extends StatelessWidget {
  final String text;
  const _ErrorText({required this.text});
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(text,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 11, color: DesignColors.error)),
      ),
    );
  }
}

String _shortTs(String raw) {
  if (raw.isEmpty) return '';
  final t = DateTime.tryParse(raw);
  if (t == null) return raw;
  final diff = DateTime.now().difference(t);
  if (diff.inMinutes < 1) return 'now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  if (diff.inDays < 7) return '${diff.inDays}d';
  return '${(diff.inDays / 7).floor()}w';
}
