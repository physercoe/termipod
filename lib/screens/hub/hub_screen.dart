import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/spawn_preset_service.dart';
import '../../theme/design_colors.dart';
import '../../widgets/agent_feed.dart';
import 'archived_agents_screen.dart';
import 'hub_bootstrap_screen.dart';
import 'project_create_sheet.dart';
import 'project_detail_screen.dart';
import 'team_channel_screen.dart';
import 'team_screen.dart';

/// Main dashboard for a configured Termipod Hub. Four tabs:
///   - Projects: project inventory; FAB creates; tap opens detail.
///   - Agents:   kind/handle/status per agent, spawn actions.
///   - Hosts:    host-runners checking in.
///   - Templates: team-wide templates (agents/prompts/policies).
///
/// Attention/Feed/Tasks moved out: approvals land in the Inbox tab,
/// per-channel Feed and per-project Tasks live inside Project detail.
/// Header carries a Steward chip (shortcut to #hub-meta team channel) and
/// a Team icon (members/policies/channels/settings).
///
/// If the hub isn't configured yet, we push [HubBootstrapScreen] from the
/// empty state; once it pops true, the provider rebuilds and the real
/// dashboard takes over.
class HubScreen extends ConsumerStatefulWidget {
  const HubScreen({super.key});

  @override
  ConsumerState<HubScreen> createState() => _HubScreenState();
}

class _HubScreenState extends ConsumerState<HubScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 4, vsync: this);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final st = ref.read(hubProvider).value;
      if (st != null && st.configured) {
        ref.read(hubProvider.notifier).refreshAll();
      }
    });
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(hubProvider);
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Hub',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 22,
            fontWeight: FontWeight.w700,
            color: colorScheme.onSurface,
          ),
        ),
        actions: [
          const _StewardChip(),
          IconButton(
            tooltip: 'Team',
            icon: const Icon(Icons.group_outlined),
            onPressed: async.value?.configured == true
                ? () {
                    Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => const TeamScreen(),
                    ));
                  }
                : null,
          ),
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: async.value?.configured == true
                ? () => ref.read(hubProvider.notifier).refreshAll()
                : null,
          ),
          IconButton(
            tooltip: 'Hub settings',
            icon: const Icon(Icons.settings),
            onPressed: () {
              Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => const HubBootstrapScreen(),
              ));
            },
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(48),
          child: TabBar(
            controller: _tabs,
            tabs: const [
              Tab(icon: Icon(Icons.folder_outlined), text: 'Projects'),
              Tab(icon: Icon(Icons.smart_toy_outlined), text: 'Agents'),
              Tab(icon: Icon(Icons.dns_outlined), text: 'Hosts'),
              Tab(icon: Icon(Icons.description_outlined), text: 'Templates'),
            ],
          ),
        ),
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(error: '$e'),
        data: (st) {
          if (!st.configured) return const _NotConfiguredView();
          return Column(
            children: [
              if (st.error != null) _ErrorBanner(text: st.error!),
              Expanded(
                child: TabBarView(
                  controller: _tabs,
                  children: [
                    _ProjectsTab(items: st.projects),
                    _AgentsTab(
                        items: st.agents,
                        hosts: st.hosts,
                        spawns: st.spawns),
                    _HostsTab(items: st.hosts),
                    _TemplatesTab(items: st.templates),
                  ],
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}

/// Tiny pill in the AppBar that opens the team-scope `#hub-meta` channel
/// (the principal↔steward room). Lazily looks up the channel id on tap —
/// no state plumbing needed because the channel list is small and the
/// hub auto-seeds hub-meta.
///
/// The chip dims itself when no steward agent is currently running on the
/// hub — a live-colour pill on an empty channel was misleading users into
/// thinking an assistant was there. Tapping a dim chip still works (opens
/// the channel) but makes it obvious you'd be talking to nobody.
class _StewardChip extends ConsumerWidget {
  const _StewardChip();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider).value;
    if (hub == null || !hub.configured) return const SizedBox.shrink();
    final scheme = Theme.of(context).colorScheme;
    final present = _stewardPresent(hub.agents);
    final Color bg;
    final Color fg;
    if (present) {
      bg = scheme.primaryContainer;
      fg = scheme.onPrimaryContainer;
    } else {
      bg = scheme.surfaceContainerHighest;
      fg = scheme.onSurfaceVariant;
    }
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4),
        child: Tooltip(
          message: present
              ? 'Open #hub-meta (steward)'
              : 'Open #hub-meta — no steward running',
          child: Material(
            color: bg,
            borderRadius: BorderRadius.circular(16),
            child: InkWell(
              borderRadius: BorderRadius.circular(16),
              onTap: () => _openSteward(context, ref),
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      present
                          ? Icons.auto_awesome
                          : Icons.auto_awesome_outlined,
                      size: 16,
                      color: fg,
                    ),
                    const SizedBox(width: 6),
                    Text(
                      present ? 'Steward' : 'No steward',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: fg,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// A steward counts as "present" when any agent with handle=='steward'
  /// is in an active lifecycle state (pending or running). We include
  /// 'pending' because a freshly-spawned steward is on its way up — no
  /// reason to flash "No steward" during the 3s reconcile window.
  static bool _stewardPresent(List<Map<String, dynamic>> agents) {
    for (final a in agents) {
      if ((a['handle'] ?? '').toString() != 'steward') continue;
      final s = (a['status'] ?? '').toString();
      if (s == 'running' || s == 'pending') return true;
    }
    return false;
  }

  Future<void> _openSteward(BuildContext context, WidgetRef ref) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final channels = await client.listTeamChannels();
      final meta = channels.firstWhere(
        (c) => c['name'] == 'hub-meta',
        orElse: () => const {},
      );
      if (meta.isEmpty) {
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('#hub-meta channel not found')),
          );
        }
        return;
      }
      if (context.mounted) {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => TeamChannelScreen(
            channelId: (meta['id'] ?? '').toString(),
            channelName: (meta['name'] ?? '').toString(),
          ),
        ));
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Open steward failed: $e')),
        );
      }
    }
  }
}

// ---------------------------------------------------------------------
// Empty / error helpers
// ---------------------------------------------------------------------

class _NotConfiguredView extends StatelessWidget {
  const _NotConfiguredView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.hub_outlined,
                size: 72, color: DesignColors.primary.withValues(alpha: 0.7)),
            const SizedBox(height: 16),
            Text(
              'Termipod Hub not configured',
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 18, fontWeight: FontWeight.w700),
            ),
            const SizedBox(height: 8),
            Text(
              'Paste a hub URL and bearer token to see attention items, '
              'agents, and the live event feed.',
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
            ),
            const SizedBox(height: 24),
            FilledButton.icon(
              icon: const Icon(Icons.login),
              label: const Text('Configure Hub'),
              onPressed: () {
                Navigator.of(context).push(MaterialPageRoute(
                  builder: (_) => const HubBootstrapScreen(),
                ));
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String error;
  const _ErrorView({required this.error});
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(error,
            style: GoogleFonts.jetBrainsMono(color: DesignColors.error)),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  final String text;
  const _ErrorBanner({required this.text});
  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: DesignColors.error.withValues(alpha: 0.12),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(text,
          style: GoogleFonts.jetBrainsMono(
              fontSize: 11, color: DesignColors.error)),
    );
  }
}

// ---------------------------------------------------------------------
// Templates tab — list team templates; tap to view raw body
// ---------------------------------------------------------------------

class _TemplatesTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _TemplatesTab({required this.items});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) {
      return const _EmptyText(text: 'No templates on this hub');
    }
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final t = items[i];
          final category = t['category']?.toString() ?? '';
          final name = t['name']?.toString() ?? '?';
          final size = t['size'] is int ? t['size'] as int : 0;
          return _InfoTile(
            title: name,
            subtitle: '$category · ${size}B',
            onTap: () => _openTemplate(context, ref, category, name),
          );
        },
      ),
    );
  }

  Future<void> _openTemplate(
      BuildContext context, WidgetRef ref, String category, String name) async {
    try {
      final body = await ref
          .read(hubProvider.notifier)
          .getTemplateBody(category, name);
      if (!context.mounted) return;
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        builder: (_) => _TemplateViewer(
          category: category,
          name: name,
          body: body,
        ),
      );
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Fetch failed: $e')));
      }
    }
  }
}

class _TemplateViewer extends ConsumerStatefulWidget {
  final String category;
  final String name;
  final String body;
  const _TemplateViewer({
    required this.category,
    required this.name,
    required this.body,
  });

  @override
  ConsumerState<_TemplateViewer> createState() => _TemplateViewerState();
}

class _TemplateViewerState extends ConsumerState<_TemplateViewer> {
  late String _body = widget.body;
  bool _refreshing = false;

  bool get _isMarkdown => widget.name.toLowerCase().endsWith('.md');

  Future<void> _forceRefresh() async {
    setState(() => _refreshing = true);
    try {
      final fresh = await ref.read(hubProvider.notifier).getTemplateBody(
            widget.category,
            widget.name,
            forceRefresh: true,
          );
      if (!mounted) return;
      setState(() => _body = fresh);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Refresh failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _refreshing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final title = '${widget.category}/${widget.name}';
    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Column(
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            child: Row(
              children: [
                Expanded(
                  child: Text(title,
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 15, fontWeight: FontWeight.w700)),
                ),
                IconButton(
                  tooltip: 'Re-fetch from hub',
                  icon: _refreshing
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.refresh),
                  onPressed: _refreshing ? null : _forceRefresh,
                ),
                IconButton(
                  icon: const Icon(Icons.close),
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: SingleChildScrollView(
              controller: scroll,
              padding: const EdgeInsets.all(16),
              child: _isMarkdown
                  ? MarkdownBody(data: _body, selectable: true)
                  : SelectableText(
                      _body,
                      style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Agents / Hosts / Projects tabs — read-only tables
// ---------------------------------------------------------------------

class _AgentsTab extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> items;
  final List<Map<String, dynamic>> hosts;
  final List<Map<String, dynamic>> spawns;
  const _AgentsTab({
    required this.items,
    required this.hosts,
    required this.spawns,
  });

  @override
  ConsumerState<_AgentsTab> createState() => _AgentsTabState();
}

class _AgentsTabState extends ConsumerState<_AgentsTab> {
  bool _treeView = false;

  @override
  Widget build(BuildContext context) {
    // Bootstrap: hub has hosts registered but no agents yet — prompt the
    // operator to spawn a Steward so there's someone to hand tasks to.
    if (widget.items.isEmpty && widget.hosts.isNotEmpty) {
      return Stack(
        children: [
          _SpawnStewardCard(hosts: widget.hosts),
          _SpawnAgentFab(hosts: widget.hosts),
        ],
      );
    }
    if (widget.items.isEmpty) {
      return Stack(
        children: [
          const _EmptyText(text: 'No agents registered'),
          if (widget.hosts.isNotEmpty) _SpawnAgentFab(hosts: widget.hosts),
        ],
      );
    }
    // Keep the steward row pinned at the top so the operator can see its
    // status at a glance without scrolling past other agents. The chip in
    // the AppBar only encodes presence; the row shows running/crashed/etc.
    final sorted = _sortedAgents(widget.items);
    final body = _treeView
        ? _AgentOrgChart(agents: widget.items, spawns: widget.spawns)
        : ListView.separated(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 88),
            itemCount: sorted.length,
            separatorBuilder: (_, __) => const SizedBox(height: 8),
            itemBuilder: (_, i) {
              final a = sorted[i];
              final isSteward = (a['handle']?.toString() ?? '') == 'steward';
              final mode = (a['mode'] ?? '').toString();
              final modeSuffix = mode.isEmpty ? '' : ' · $mode';
              return _InfoTile(
                title: a['handle']?.toString() ?? '?',
                subtitle:
                    '${a['kind'] ?? ''} · ${a['status'] ?? ''}$modeSuffix',
                leading: isSteward
                    ? Icon(
                        Icons.auto_awesome,
                        size: 18,
                        color: Theme.of(context).colorScheme.primary,
                      )
                    : null,
                trailing: (a['pause_state']?.toString() ?? 'running') == 'paused'
                    ? 'paused'
                    : null,
                onTap: () => _openAgentDetail(context, a),
              );
            },
          );

    return Stack(
      children: [
        Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
              child: Row(
                children: [
                  Expanded(
                    child: SegmentedButton<bool>(
                      segments: const [
                        ButtonSegment<bool>(
                          value: false,
                          label: Text('List'),
                          icon: Icon(Icons.view_list, size: 18),
                        ),
                        ButtonSegment<bool>(
                          value: true,
                          label: Text('Tree'),
                          icon: Icon(Icons.account_tree, size: 18),
                        ),
                      ],
                      selected: {_treeView},
                      onSelectionChanged: (sel) =>
                          setState(() => _treeView = sel.first),
                      showSelectedIcon: false,
                    ),
                  ),
                  IconButton(
                    tooltip: 'Archived agents',
                    icon: const Icon(Icons.inventory_2_outlined),
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => const ArchivedAgentsScreen(),
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Expanded(
              child: RefreshIndicator(
                onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
                child: body,
              ),
            ),
          ],
        ),
        if (widget.hosts.isNotEmpty) _SpawnAgentFab(hosts: widget.hosts),
      ],
    );
  }
}

/// Indented tree view of agent_spawns. Roots are agents with no spawn edge
/// pointing to them (steward, hand-registered agents). Children are grouped
/// by parent_agent_id. Depth is capped soft via the indent arithmetic —
/// the renderer will keep going if someone spawns a pathological chain.
class _AgentOrgChart extends StatelessWidget {
  final List<Map<String, dynamic>> agents;
  final List<Map<String, dynamic>> spawns;
  const _AgentOrgChart({required this.agents, required this.spawns});

  @override
  Widget build(BuildContext context) {
    final byId = {
      for (final a in agents) (a['id']?.toString() ?? ''): a,
    };
    // Build parent → [children] using spawns. An agent with no inbound edge
    // is a root.
    final children = <String, List<String>>{};
    final hasParent = <String>{};
    for (final sp in spawns) {
      final parent = sp['parent_agent_id']?.toString() ?? '';
      final child = sp['child_agent_id']?.toString() ?? '';
      if (child.isEmpty) continue;
      if (parent.isEmpty) continue;
      children.putIfAbsent(parent, () => []).add(child);
      hasParent.add(child);
    }
    final roots = [
      for (final a in agents)
        if (!hasParent.contains(a['id']?.toString() ?? '')) a,
    ];
    if (roots.isEmpty) {
      return const _EmptyText(
          text: 'No spawn edges yet — agents will appear as they delegate');
    }

    final rows = <Widget>[];
    final seen = <String>{};
    void walk(String id, int depth) {
      if (!seen.add(id)) return; // guard against cycles
      final a = byId[id];
      if (a == null) return;
      rows.add(Builder(
        builder: (ctx) => InkWell(
          borderRadius: BorderRadius.circular(8),
          onTap: () => _openAgentDetail(ctx, a),
          child: _OrgRow(agent: a, depth: depth),
        ),
      ));
      for (final cid in children[id] ?? const []) {
        walk(cid, depth + 1);
      }
    }

    for (final r in roots) {
      walk(r['id']?.toString() ?? '', 0);
    }
    return ListView.separated(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 88),
      itemCount: rows.length,
      separatorBuilder: (_, __) => const SizedBox(height: 6),
      itemBuilder: (_, i) => rows[i],
    );
  }
}

class _OrgRow extends StatelessWidget {
  final Map<String, dynamic> agent;
  final int depth;
  const _OrgRow({required this.agent, required this.depth});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final status = agent['status']?.toString() ?? '';
    final paused =
        (agent['pause_state']?.toString() ?? 'running') == 'paused';
    final mode = (agent['mode'] ?? '').toString();
    return Padding(
      padding: EdgeInsets.only(left: (depth * 20).toDouble()),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            if (depth > 0)
              Padding(
                padding: const EdgeInsets.only(right: 6),
                child: Icon(
                  Icons.subdirectory_arrow_right,
                  size: 16,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    agent['handle']?.toString() ?? '?',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  Text(
                    '${agent['kind'] ?? ''} · $status'
                    '${paused ? ' · paused' : ''}',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    ),
                  ),
                ],
              ),
            ),
            if (mode.isNotEmpty) ...[
              _Chip(text: mode, color: _agentModeColor(mode)),
              const SizedBox(width: 4),
            ],
            _Chip(text: status, color: _agentStatusColor(status)),
          ],
        ),
      ),
    );
  }
}

Color _agentModeColor(String mode) {
  switch (mode) {
    case 'M1':
      return DesignColors.secondary;
    case 'M2':
      return DesignColors.terminalBlue;
    case 'M4':
      return DesignColors.textMuted;
    default:
      return DesignColors.textMuted;
  }
}

/// Returns a list with the steward row pinned first. Relative order of the
/// other rows is preserved so sort behaviour is stable across refreshes.
/// Input list is not mutated.
List<Map<String, dynamic>> _sortedAgents(List<Map<String, dynamic>> items) {
  Map<String, dynamic>? steward;
  final rest = <Map<String, dynamic>>[];
  for (final a in items) {
    if (steward == null && (a['handle']?.toString() ?? '') == 'steward') {
      steward = a;
    } else {
      rest.add(a);
    }
  }
  if (steward == null) return items;
  return [steward, ...rest];
}

Color _agentStatusColor(String status) {
  switch (status) {
    case 'running':
    case 'active':
      return Colors.green;
    case 'pending':
    case 'idle':
      return Colors.orange;
    case 'crashed':
    case 'failed':
    case 'terminated':
      return DesignColors.error;
    default:
      return DesignColors.primary;
  }
}

class _SpawnAgentFab extends ConsumerWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnAgentFab({required this.hosts});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Positioned(
      right: 16,
      bottom: 16,
      child: FloatingActionButton.extended(
        heroTag: 'spawn_agent_fab',
        onPressed: () => showModalBottomSheet<void>(
          context: context,
          isScrollControlled: true,
          builder: (_) => _SpawnAgentDialog(hosts: hosts),
        ),
        icon: const Icon(Icons.add),
        label: const Text('Spawn Agent'),
      ),
    );
  }
}

class _SpawnAgentDialog extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnAgentDialog({required this.hosts});

  @override
  ConsumerState<_SpawnAgentDialog> createState() => _SpawnAgentDialogState();
}

class _SpawnAgentDialogState extends ConsumerState<_SpawnAgentDialog> {
  final _handleCtl = TextEditingController();
  final _kindCtl = TextEditingController(text: 'claude-code');
  final _yamlCtl = TextEditingController(
    text: 'backend:\n  cmd: "claude --model opus-4-7 --no-update"\n',
  );
  String? _hostId;
  bool _busy = false;
  String? _error;
  List<Map<String, dynamic>>? _templates;
  final _presetSvc = SpawnPresetService();
  List<SpawnPreset> _presets = const [];

  @override
  void initState() {
    super.initState();
    // Prefer an online host as the default.
    final online = widget.hosts.where(
      (h) => (h['status']?.toString() ?? '') == 'online',
    );
    _hostId = (online.isNotEmpty ? online.first : widget.hosts.first)['id']
        ?.toString();
    _loadPresets();
  }

  Future<void> _loadPresets() async {
    final items = await _presetSvc.load();
    if (!mounted) return;
    setState(() => _presets = items);
  }

  void _applyPreset(SpawnPreset p) {
    setState(() {
      _handleCtl.text = p.handle;
      _kindCtl.text = p.kind;
      _yamlCtl.text = p.yaml;
    });
  }

  Future<void> _deletePreset(SpawnPreset p) async {
    final items = await _presetSvc.delete(p.id);
    if (!mounted) return;
    setState(() => _presets = items);
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text('Preset "${p.name}" deleted')));
  }

  Future<void> _confirmDeletePreset(SpawnPreset p) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Delete preset "${p.name}"?'),
        content: const Text('This only removes it from this device.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok == true) await _deletePreset(p);
  }

  Future<void> _saveAsPreset() async {
    final handle = _handleCtl.text.trim();
    final kind = _kindCtl.text.trim();
    final yaml = _yamlCtl.text;
    if (handle.isEmpty || kind.isEmpty || yaml.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Fill handle, kind, and YAML first')),
      );
      return;
    }
    final nameCtl = TextEditingController(text: handle);
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Save spawn preset'),
        content: TextField(
          controller: nameCtl,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'Preset name',
            border: OutlineInputBorder(),
          ),
          onSubmitted: (v) => Navigator.of(ctx).pop(v.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(nameCtl.text.trim()),
            child: const Text('Save'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    final preset = SpawnPreset(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      name: name,
      handle: handle,
      kind: kind,
      yaml: yaml,
    );
    final items = await _presetSvc.upsert(preset);
    if (!mounted) return;
    setState(() => _presets = items);
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text('Saved preset "$name"')));
  }

  @override
  void dispose() {
    _handleCtl.dispose();
    _kindCtl.dispose();
    _yamlCtl.dispose();
    super.dispose();
  }

  Future<void> _loadTemplate() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      _templates ??= await client.listTemplates();
      final agentTemplates = _templates!
          .where((t) => (t['category']?.toString() ?? '') == 'agents')
          .toList();
      if (!mounted) return;
      if (agentTemplates.isEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('No agent templates on this hub')),
        );
        return;
      }
      final picked = await showModalBottomSheet<Map<String, dynamic>>(
        context: context,
        builder: (_) => ListView(
          shrinkWrap: true,
          children: [
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('Pick a template',
                  style: TextStyle(fontWeight: FontWeight.w700)),
            ),
            const Divider(height: 1),
            for (final t in agentTemplates)
              ListTile(
                title: Text(t['name']?.toString() ?? '?'),
                subtitle: Text('${t['size'] ?? 0}B'),
                onTap: () => Navigator.of(context).pop(t),
              ),
          ],
        ),
      );
      if (picked == null || !mounted) return;
      final name = picked['name']?.toString() ?? '';
      final body = await client.getTemplate('agents', name);
      if (!mounted) return;
      setState(() => _yamlCtl.text = body);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Load template failed: $e')));
    }
  }

  Future<void> _submit() async {
    final handle = _handleCtl.text.trim();
    final kind = _kindCtl.text.trim();
    final yaml = _yamlCtl.text;
    if (handle.isEmpty || kind.isEmpty || yaml.trim().isEmpty) {
      setState(() => _error = 'handle, kind, and YAML spec are required');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) throw StateError('Hub not configured');
      final res = await client.spawnAgent(
        childHandle: handle,
        kind: kind,
        spawnSpecYaml: yaml,
        hostId: _hostId,
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Agent "$handle" spawned.';
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    return DraggableScrollableSheet(
      initialChildSize: 0.9,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Padding(
        padding: EdgeInsets.only(bottom: bottomInset),
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Row(
                children: [
                  const Expanded(
                    child: Text('Spawn agent',
                        style: TextStyle(
                            fontSize: 16, fontWeight: FontWeight.w700)),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView(
                controller: scroll,
                padding: const EdgeInsets.all(16),
                children: [
                  if (_presets.isNotEmpty) ...[
                    Row(
                      children: [
                        const Text('Presets',
                            style: TextStyle(fontWeight: FontWeight.w600)),
                        const Spacer(),
                        Text('long-press to delete',
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: 10,
                              color: Theme.of(context)
                                  .textTheme
                                  .bodySmall
                                  ?.color
                                  ?.withValues(alpha: 0.7),
                            )),
                      ],
                    ),
                    const SizedBox(height: 6),
                    SizedBox(
                      height: 36,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _presets.length,
                        separatorBuilder: (_, __) => const SizedBox(width: 6),
                        itemBuilder: (_, i) {
                          final p = _presets[i];
                          return GestureDetector(
                            onLongPress: () => _confirmDeletePreset(p),
                            child: ActionChip(
                              avatar: const Icon(Icons.bolt, size: 16),
                              label: Text(p.name),
                              onPressed: () => _applyPreset(p),
                            ),
                          );
                        },
                      ),
                    ),
                    const SizedBox(height: 12),
                  ],
                  TextField(
                    controller: _handleCtl,
                    decoration: const InputDecoration(
                      labelText: 'Handle',
                      hintText: 'e.g. worker-fe',
                      border: OutlineInputBorder(),
                    ),
                    autofocus: true,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _kindCtl,
                    decoration: const InputDecoration(
                      labelText: 'Kind',
                      hintText: 'claude-code, kimi-code, …',
                      border: OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    initialValue: _hostId,
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: 'Host',
                      border: OutlineInputBorder(),
                    ),
                    items: widget.hosts
                        .map((h) => DropdownMenuItem<String>(
                              value: h['id']?.toString(),
                              child: Text(
                                '${h['name'] ?? '?'} '
                                '(${h['status'] ?? 'unknown'})',
                              ),
                            ))
                        .toList(),
                    onChanged: (v) => setState(() => _hostId = v),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      const Expanded(
                        child: Text('Spawn spec (YAML)',
                            style: TextStyle(fontWeight: FontWeight.w600)),
                      ),
                      IconButton(
                        onPressed: _saveAsPreset,
                        icon: const Icon(Icons.bookmark_add_outlined, size: 20),
                        tooltip: 'Save as preset',
                        visualDensity: VisualDensity.compact,
                      ),
                      IconButton(
                        onPressed: _loadTemplate,
                        icon: const Icon(Icons.file_open, size: 20),
                        tooltip: 'Load template',
                        visualDensity: VisualDensity.compact,
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  TextField(
                    controller: _yamlCtl,
                    maxLines: 14,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    decoration: const InputDecoration(
                      hintText:
                          'backend:\n  cmd: "claude --model opus-4-7"\n',
                      border: OutlineInputBorder(),
                      alignLabelWithHint: true,
                    ),
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 12),
                    Text(_error!,
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                  ],
                  const SizedBox(height: 16),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      TextButton(
                        onPressed:
                            _busy ? null : () => Navigator.of(context).pop(),
                        child: const Text('Cancel'),
                      ),
                      const SizedBox(width: 8),
                      FilledButton.icon(
                        onPressed: _busy ? null : _submit,
                        icon: _busy
                            ? const SizedBox(
                                width: 16,
                                height: 16,
                                child:
                                    CircularProgressIndicator(strokeWidth: 2),
                              )
                            : const Icon(Icons.play_arrow),
                        label: Text(_busy ? 'Spawning…' : 'Spawn'),
                      ),
                    ],
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

class _SpawnStewardCard extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnStewardCard({required this.hosts});

  @override
  ConsumerState<_SpawnStewardCard> createState() => _SpawnStewardCardState();
}

class _SpawnStewardCardState extends ConsumerState<_SpawnStewardCard> {
  bool _busy = false;
  String? _lastError;

  Future<void> _spawn() async {
    setState(() {
      _busy = true;
      _lastError = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) {
        throw StateError('Hub not configured');
      }
      final yaml = await client.getTemplate('agents', 'steward.v1');
      // Prefer an online host; fall back to whatever's listed first.
      final host = widget.hosts.firstWhere(
        (h) => (h['status']?.toString() ?? '') == 'online',
        orElse: () => widget.hosts.first,
      );
      final res = await client.spawnAgent(
        childHandle: 'steward',
        kind: 'claude-code',
        spawnSpecYaml: yaml,
        hostId: host['id']?.toString(),
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Steward spawned.';
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      setState(() => _lastError = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(children: const [
                  Icon(Icons.auto_awesome, size: 28),
                  SizedBox(width: 10),
                  Text('Welcome to your hub',
                      style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
                ]),
                const SizedBox(height: 12),
                const Text(
                  'A host is online but no agents are running yet. '
                  'Spawn a Steward to coordinate work, take delegations, '
                  'and hand out tasks to other agents.',
                ),
                const SizedBox(height: 16),
                if (_lastError != null) ...[
                  Text(_lastError!,
                      style: TextStyle(color: Theme.of(context).colorScheme.error)),
                  const SizedBox(height: 12),
                ],
                Align(
                  alignment: Alignment.centerRight,
                  child: FilledButton.icon(
                    onPressed: _busy ? null : _spawn,
                    icon: _busy
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.play_arrow),
                    label: Text(_busy ? 'Spawning…' : 'Spawn Steward'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _HostsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _HostsTab({required this.items});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (items.isEmpty) return const _EmptyText(text: 'No hosts registered');
    return RefreshIndicator(
      onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: items.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final h = items[i];
          return _InfoTile(
            title: h['name']?.toString() ?? '?',
            subtitle: 'status: ${h['status'] ?? 'unknown'}',
            trailing: _shortTs((h['last_seen_at'] ?? '') as String),
            onTap: () => _openHostDetail(context, h),
          );
        },
      ),
    );
  }
}

class _ProjectsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _ProjectsTab({required this.items});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final body = items.isEmpty
        ? const _EmptyText(text: 'No projects yet — tap + to create one')
        : RefreshIndicator(
            onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 96),
              itemCount: items.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) {
                final p = items[i];
                return _InfoTile(
                  title: p['name']?.toString() ?? '?',
                  subtitle: p['status']?.toString() ?? '',
                  trailing: _shortTs((p['created_at'] ?? '') as String),
                  onTap: () {
                    Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => ProjectDetailScreen(project: p),
                    ));
                  },
                );
              },
            ),
          );
    return Stack(
      children: [
        Positioned.fill(child: body),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton(
            heroTag: 'hub-projects-fab',
            onPressed: () => _openCreateSheet(context, ref),
            child: const Icon(Icons.add),
          ),
        ),
      ],
    );
  }

  Future<void> _openCreateSheet(BuildContext context, WidgetRef ref) async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const ProjectCreateSheet(),
    );
    if (created == true) {
      await ref.read(hubProvider.notifier).refreshAll();
    }
  }
}

// ---------------------------------------------------------------------
// Small UI helpers
// ---------------------------------------------------------------------

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
            fontSize: 14,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
        ),
      ),
    );
  }
}

class _InfoTile extends StatelessWidget {
  final String title;
  final String subtitle;
  final String? trailing;
  final Widget? leading;
  final VoidCallback? onTap;
  const _InfoTile({
    required this.title,
    required this.subtitle,
    this.trailing,
    this.leading,
    this.onTap,
  });
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final tile = Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          if (leading != null) ...[
            leading!,
            const SizedBox(width: 10),
          ],
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w600)),
                if (subtitle.isNotEmpty)
                  Text(subtitle,
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 11,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight)),
              ],
            ),
          ),
          if (trailing != null && trailing!.isNotEmpty)
            Text(trailing!,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight)),
        ],
      ),
    );
    if (onTap == null) return tile;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: onTap,
      child: tile,
    );
  }
}

class _Chip extends StatelessWidget {
  final String text;
  final Color color;
  const _Chip({required this.text, required this.color});
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        text,
        style: GoogleFonts.jetBrainsMono(
            fontSize: 10, fontWeight: FontWeight.w600, color: color),
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Agent / Host detail sheets — Pause, Resume, Terminate, pane preview,
// journal read/append, host delete. Shown via showModalBottomSheet from
// the Agents and Hosts tabs.
// ---------------------------------------------------------------------

void _openAgentDetail(BuildContext context, Map<String, dynamic> agent) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (_) => _AgentDetailSheet(agent: agent),
  );
}

void _openHostDetail(BuildContext context, Map<String, dynamic> host) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (_) => _HostDetailSheet(host: host),
  );
}

class _AgentDetailSheet extends ConsumerStatefulWidget {
  final Map<String, dynamic> agent;
  const _AgentDetailSheet({required this.agent});
  @override
  ConsumerState<_AgentDetailSheet> createState() => _AgentDetailSheetState();
}

class _AgentDetailSheetState extends ConsumerState<_AgentDetailSheet> {
  bool _busy = false;
  String? _error;
  String? _paneText;
  String? _paneCapturedAt;
  String? _journal;
  bool _journalLoaded = false;
  final _noteCtl = TextEditingController();
  // Full agent row (fetched via GET /agents/{id}) includes the
  // spawn_spec_yaml join; the list payload omits it to stay small.
  Map<String, dynamic>? _full;

  String get _id => widget.agent['id']?.toString() ?? '';
  String get _handle => widget.agent['handle']?.toString() ?? '?';
  String get _status => widget.agent['status']?.toString() ?? 'unknown';
  // Mode lives on the list row (P1 resolver output). Prefer the
  // freshly-fetched full row when available so a spawn that was
  // pending at open time picks up its resolved mode on first load.
  String get _mode =>
      (_full?['mode'] ?? widget.agent['mode'] ?? '').toString();
  String get _pauseState =>
      widget.agent['pause_state']?.toString() ?? 'running';
  bool get _isPaused => _pauseState == 'paused';
  bool get _isDead =>
      _status == 'terminated' ||
      _status == 'failed' ||
      _status == 'crashed';
  bool get _hasPane =>
      (widget.agent['pane_id']?.toString() ?? '').isNotEmpty;

  @override
  void initState() {
    super.initState();
    _loadPane();
    _loadFull();
  }

  Future<void> _loadFull() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final out = await client.getAgent(_id);
      if (!mounted) return;
      setState(() => _full = out);
    } catch (_) {
      // Spec-fetch failure is non-fatal — the sheet still works without it.
    }
  }

  String get _specYaml =>
      (_full?['spawn_spec_yaml'] ?? '').toString();

  Future<void> _respawn() async {
    final spec = _specYaml;
    if (spec.isEmpty) return;
    final kind = (widget.agent['kind'] ?? '').toString();
    final hostId = (widget.agent['host_id'] ?? '').toString();
    final suggested =
        '$_handle-r${DateTime.now().millisecondsSinceEpoch % 10000}';
    final ctrl = TextEditingController(text: suggested);
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Respawn from spec'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Spawns a new agent using the same spec. The original row stays '
              'untouched — terminate it first if you want to free the handle.',
              style: GoogleFonts.spaceGrotesk(fontSize: 12),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: ctrl,
              autofocus: true,
              decoration: const InputDecoration(labelText: 'New handle'),
            ),
          ],
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('Respawn')),
        ],
      ),
    );
    if (ok != true) return;
    final newHandle = ctrl.text.trim();
    if (newHandle.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final done = await _guard(() async {
      await client.spawnAgent(
        childHandle: newHandle,
        kind: kind,
        spawnSpecYaml: spec,
        hostId: hostId.isEmpty ? null : hostId,
      );
      return true;
    });
    if (done != true || !mounted) return;
    await ref.read(hubProvider.notifier).refreshAll();
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Respawn requested: $newHandle')),
      );
    }
  }

  @override
  void dispose() {
    _noteCtl.dispose();
    super.dispose();
  }

  Future<T?> _guard<T>(Future<T> Function() op) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      return await op();
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
      return null;
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _loadPane({bool refresh = false}) async {
    if (!_hasPane) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final out = await _guard(() => client.getAgentPane(_id, refresh: refresh));
    if (out == null || !mounted) return;
    setState(() {
      _paneText = out['text']?.toString();
      _paneCapturedAt = out['captured_at']?.toString();
    });
  }

  Future<void> _loadJournal() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final out = await _guard(() => client.readAgentJournal(_id));
    if (!mounted) return;
    setState(() {
      _journal = out ?? '';
      _journalLoaded = true;
    });
  }

  Future<void> _appendJournal() async {
    final entry = _noteCtl.text.trim();
    if (entry.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final ok = await _guard(() async {
      await client.appendAgentJournal(_id, entry);
      return true;
    });
    if (!mounted || ok != true) return;
    _noteCtl.clear();
    await _loadJournal();
  }

  Future<void> _pauseOrResume() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final ok = await _guard(() =>
        _isPaused ? client.resumeAgent(_id) : client.pauseAgent(_id));
    if (ok == null || !mounted) return;
    // Command is enqueued; the host-runner flips pause_state after it runs.
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(_isPaused
          ? 'Resume command enqueued'
          : 'Pause command enqueued'),
    ));
    await ref.read(hubProvider.notifier).refreshAll();
  }

  Future<void> _archive() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Delete "$_handle"?'),
        content: const Text(
            'Moves this terminated agent off the live list. The row stays in '
            'the database so spawn history and audit events still resolve. '
            'You can review archived agents from the hub menu.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
                backgroundColor: Theme.of(ctx).colorScheme.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final done = await _guard(() async {
      await client.archiveAgent(_id);
      return true;
    });
    if (done != true || !mounted) return;
    await ref.read(hubProvider.notifier).refreshAll();
    if (mounted) Navigator.pop(context);
  }

  Future<void> _terminate() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Terminate "$_handle"?'),
        content: const Text(
            'Marks status=terminated. The host-runner kills the pane and '
            'cleans up any clean worktree; dirty worktrees are preserved.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
                backgroundColor: Theme.of(ctx).colorScheme.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Terminate'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final done = await _guard(() async {
      await client.terminateAgent(_id);
      return true;
    });
    if (done != true || !mounted) return;
    await ref.read(hubProvider.notifier).refreshAll();
    if (mounted) Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Padding(
      padding: EdgeInsets.only(
          bottom: MediaQuery.of(context).viewInsets.bottom),
      child: FractionallySizedBox(
        heightFactor: 0.9,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 8, 4),
              child: Row(
                children: [
                  Expanded(
                    child: Text(_handle,
                        style: GoogleFonts.spaceGrotesk(
                            fontSize: 18, fontWeight: FontWeight.w700)),
                  ),
                  if (_mode.isNotEmpty) ...[
                    _Chip(text: _mode, color: DesignColors.primary),
                    const SizedBox(width: 6),
                  ],
                  _Chip(text: _status, color: _agentStatusColor(_status)),
                  if (_isPaused) ...[
                    const SizedBox(width: 6),
                    const _Chip(text: 'paused', color: Colors.orange),
                  ],
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.pop(context),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Text(
                '${widget.agent['kind'] ?? ''}'
                '${widget.agent['host_id'] != null ? ' · host ${widget.agent['host_id']}' : ''}',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: mutedColor),
              ),
            ),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: Text(_error!,
                    style: const TextStyle(color: DesignColors.error)),
              ),
            const SizedBox(height: 8),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  FilledButton.tonalIcon(
                    onPressed: (_busy || _isDead || !_hasPane)
                        ? null
                        : _pauseOrResume,
                    icon: Icon(
                        _isPaused ? Icons.play_arrow : Icons.pause),
                    label: Text(_isPaused ? 'Resume' : 'Pause'),
                  ),
                  if (!_isDead)
                    FilledButton.icon(
                      onPressed: _busy ? null : _terminate,
                      style: FilledButton.styleFrom(
                        backgroundColor:
                            Theme.of(context).colorScheme.error,
                      ),
                      icon: const Icon(Icons.stop_circle_outlined),
                      label: const Text('Terminate'),
                    ),
                  if (_isDead)
                    OutlinedButton.icon(
                      onPressed: _busy ? null : _archive,
                      style: OutlinedButton.styleFrom(
                        foregroundColor:
                            Theme.of(context).colorScheme.error,
                      ),
                      icon: const Icon(Icons.delete_outline),
                      label: const Text('Delete'),
                    ),
                  if (_specYaml.isNotEmpty)
                    OutlinedButton.icon(
                      onPressed: _busy ? null : _respawn,
                      icon: const Icon(Icons.replay),
                      label: const Text('Respawn'),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 12),
            Expanded(
              child: DefaultTabController(
                length: 3,
                child: Column(
                  children: [
                    TabBar(
                      isScrollable: true,
                      tabs: const [
                        Tab(text: 'Feed'),
                        Tab(text: 'Pane'),
                        Tab(text: 'Journal'),
                      ],
                    ),
                    Expanded(
                      child: TabBarView(
                        children: [
                          // --- Feed: live agent_events from P1.1 drivers.
                          AgentFeed(agentId: _id),
                          // --- Pane capture (legacy M4 view).
                          ListView(
                            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
                            children: [
                              _SectionHeader(
                                title: 'Pane capture',
                                trailing: _hasPane
                                    ? TextButton.icon(
                                        onPressed: _busy
                                            ? null
                                            : () => _loadPane(refresh: true),
                                        icon: const Icon(Icons.refresh, size: 18),
                                        label: const Text('Refresh'),
                                      )
                                    : null,
                              ),
                              if (!_hasPane)
                                Text('No pane attached yet.',
                                    style: TextStyle(color: mutedColor))
                              else
                                Container(
                                  padding: const EdgeInsets.all(10),
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
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        _paneCapturedAt == null
                                            ? '(no capture yet)'
                                            : 'captured ${_shortTs(_paneCapturedAt!)} ago',
                                        style: GoogleFonts.jetBrainsMono(
                                            fontSize: 10, color: mutedColor),
                                      ),
                                      const SizedBox(height: 6),
                                      SelectableText(
                                        _paneText == null || _paneText!.isEmpty
                                            ? '(empty — hit Refresh to request a fresh capture)'
                                            : _paneText!,
                                        style: GoogleFonts.jetBrainsMono(
                                            fontSize: 11),
                                      ),
                                    ],
                                  ),
                                ),
                              if (_specYaml.isNotEmpty) ...[
                                const SizedBox(height: 16),
                                const _SectionHeader(title: 'Spawn spec'),
                                Container(
                                  padding: const EdgeInsets.all(10),
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
                                  child: SelectableText(
                                    _specYaml,
                                    style: GoogleFonts.jetBrainsMono(
                                        fontSize: 11),
                                  ),
                                ),
                              ],
                            ],
                          ),
                          // --- Journal.
                          ListView(
                            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
                            children: [
                              _SectionHeader(
                                title: 'Journal',
                                trailing: TextButton.icon(
                                  onPressed: _busy ? null : _loadJournal,
                                  icon: Icon(
                                      _journalLoaded
                                          ? Icons.refresh
                                          : Icons.download,
                                      size: 18),
                                  label: Text(
                                      _journalLoaded ? 'Refresh' : 'Load'),
                                ),
                              ),
                              if (_journalLoaded)
                                Container(
                                  padding: const EdgeInsets.all(10),
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
                                  child: SelectableText(
                                    (_journal ?? '').isEmpty
                                        ? '(empty — the agent hasn\'t written a journal yet)'
                                        : _journal!,
                                    style: GoogleFonts.jetBrainsMono(
                                        fontSize: 11),
                                  ),
                                ),
                              const SizedBox(height: 8),
                              TextField(
                                controller: _noteCtl,
                                maxLines: 3,
                                decoration: InputDecoration(
                                  hintText: 'Append a note to the journal…',
                                  border: const OutlineInputBorder(),
                                  suffixIcon: IconButton(
                                    icon: const Icon(Icons.send),
                                    tooltip: 'Append',
                                    onPressed:
                                        _busy ? null : _appendJournal,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ],
                      ),
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
}

class _HostDetailSheet extends ConsumerStatefulWidget {
  final Map<String, dynamic> host;
  const _HostDetailSheet({required this.host});
  @override
  ConsumerState<_HostDetailSheet> createState() => _HostDetailSheetState();
}

class _HostDetailSheetState extends ConsumerState<_HostDetailSheet> {
  bool _busy = false;
  String? _error;

  Future<void> _delete() async {
    final name = widget.host['name']?.toString() ?? 'this host';
    final id = widget.host['id']?.toString() ?? '';
    if (id.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Delete "$name"?'),
        content: const Text(
          'Removes the host row from the hub. The host-runner, if still '
          'running, will register a fresh row on its next boot. The hub '
          'refuses the delete if any agents are still alive on this host.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
                backgroundColor: Theme.of(ctx).colorScheme.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) return;
      await client.deleteHost(id);
      if (!mounted) return;
      await ref.read(hubProvider.notifier).refreshAll();
      if (mounted) Navigator.pop(context);
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final h = widget.host;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final status = h['status']?.toString() ?? 'unknown';
    final lastSeen = h['last_seen_at']?.toString() ?? '';
    return Padding(
      padding: EdgeInsets.only(
          bottom: MediaQuery.of(context).viewInsets.bottom),
      child: SingleChildScrollView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(h['name']?.toString() ?? '?',
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 18, fontWeight: FontWeight.w700)),
                ),
                _Chip(text: status, color: _agentStatusColor(status)),
                IconButton(
                  icon: const Icon(Icons.close),
                  onPressed: () => Navigator.pop(context),
                ),
              ],
            ),
            const SizedBox(height: 8),
            _kv('Host ID', h['id']?.toString() ?? '', mutedColor),
            _kv(
                'Last seen',
                lastSeen.isEmpty
                    ? 'never'
                    : '${_shortTs(lastSeen)} ago · $lastSeen',
                mutedColor),
            _kv('Created', h['created_at']?.toString() ?? '', mutedColor),
            _kv('Capabilities',
                h['capabilities']?.toString() ?? '{}', mutedColor),
            const SizedBox(height: 16),
            if (_error != null) ...[
              Text(_error!,
                  style: const TextStyle(color: DesignColors.error)),
              const SizedBox(height: 8),
            ],
            FilledButton.icon(
              onPressed: _busy ? null : _delete,
              style: FilledButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.error,
              ),
              icon: const Icon(Icons.delete_outline),
              label: const Text('Delete host'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _kv(String k, String v, Color mutedColor) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 96,
              child: Text(k,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 11, color: mutedColor)),
            ),
            Expanded(
              child: SelectableText(v,
                  style: GoogleFonts.jetBrainsMono(fontSize: 11)),
            ),
          ],
        ),
      );
}

class _SectionHeader extends StatelessWidget {
  final String title;
  final Widget? trailing;
  const _SectionHeader({required this.title, this.trailing});
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6, top: 4),
      child: Row(
        children: [
          Text(title,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 13, fontWeight: FontWeight.w700)),
          const Spacer(),
          if (trailing != null) trailing!,
        ],
      ),
    );
  }
}

String _shortTs(String iso) {
  if (iso.isEmpty) return '';
  final t = DateTime.tryParse(iso);
  if (t == null) return iso;
  final local = t.toLocal();
  final now = DateTime.now();
  final diff = now.difference(local);
  if (diff.inSeconds < 60) return '${diff.inSeconds}s';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  return '${diff.inDays}d';
}
