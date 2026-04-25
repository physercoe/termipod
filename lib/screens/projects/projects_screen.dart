import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/connection_provider.dart';
import '../../providers/host_binding_provider.dart';
import '../../providers/hub_provider.dart';
import '../../services/hub/open_team_channel.dart';
import '../../theme/design_colors.dart';
import '../../widgets/agent_feed.dart';
import '../../widgets/hub_offline_banner.dart';
import '../../widgets/team_switcher.dart';
import '../connections/connection_form_screen.dart';
import '../terminal/terminal_screen.dart';
import '../team/host_edit_sheet.dart';
import '../hub/hub_bootstrap_screen.dart';
import 'project_create_sheet.dart';
import 'project_detail_screen.dart';
import '../team/spawn_steward_sheet.dart';
import '../team/templates_screen.dart';

/// "Projects" tab — project inventory. Agents live inside Project detail;
/// templates have a single home (TemplatesScreen) reached via the AppBar
/// icon. Hosts have their own top-level bottom tab.
///
/// Attention/Feed/Tasks live in Me / Project detail respectively. Header
/// carries a Steward chip (shortcut to #hub-meta team channel) and a Team
/// switcher (members/policies/channels/settings).
///
/// If the hub server isn't configured yet, the empty state pushes
/// [HubBootstrapScreen]; once that pops true, the provider rebuilds and
/// the real dashboard takes over.
class ProjectsScreen extends ConsumerStatefulWidget {
  const ProjectsScreen({super.key});

  @override
  ConsumerState<ProjectsScreen> createState() => _ProjectsScreenState();
}

class _ProjectsScreenState extends ConsumerState<ProjectsScreen> {
  /// Fires once per screen lifetime so a user who Skips the auto-bootstrap
  /// sheet doesn't get nagged again as the hub state ticks (e.g. a refresh
  /// rebuilds with the same "no steward" snapshot).
  bool _bootstrapAttempted = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final st = ref.read(hubProvider).value;
      if (st != null && st.configured) {
        ref.read(hubProvider.notifier).refreshAll();
        // Cover the cached-state-already-populated case: navigating back to
        // Projects after the cache has been hydrated produces no provider
        // transition, so the ref.listen below would never fire.
        _maybeShowBootstrap(st);
      }
    });
  }

  /// Auto-presents the steward bootstrap sheet the first time we see a
  /// configured team that has at least one online host but no steward —
  /// the W4 first-run UX. Respects a per-team SharedPreferences "dismissed"
  /// flag so a user who tapped Skip isn't re-prompted on future cold starts.
  Future<void> _maybeShowBootstrap(HubState st) async {
    if (_bootstrapAttempted) return;
    if (!st.configured) return;
    if (stewardPresent(st.agents)) return;
    final hasOnlineHost = st.hosts.any(
      (h) => (h['status']?.toString() ?? '') == 'online',
    );
    if (!hasOnlineHost) return;
    final teamId = st.config?.teamId ?? '';
    if (teamId.isEmpty) return;
    // Mark attempted before the await so a fast re-build can't double-fire.
    _bootstrapAttempted = true;
    final prefs = await SharedPreferences.getInstance();
    final dismissed = prefs.getString(bootstrapDismissedKey(teamId));
    if (dismissed != null && dismissed.isNotEmpty) return;
    if (!mounted) return;
    await showSpawnStewardSheet(
      context,
      hosts: st.hosts,
      autoTriggered: true,
    );
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(hubProvider);
    final colorScheme = Theme.of(context).colorScheme;

    // React to hub-state transitions rather than reading once in initState —
    // hosts/agents are populated by refreshAll (an async fetch), so the
    // first build often has empty lists and the bootstrap conditions only
    // become true a few frames later.
    ref.listen<AsyncValue<HubState>>(hubProvider, (_, next) {
      final st = next.value;
      if (st == null) return;
      _maybeShowBootstrap(st);
    });

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Projects',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 22,
            fontWeight: FontWeight.w700,
            color: colorScheme.onSurface,
          ),
        ),
        actions: [
          const _StewardChip(),
          const TeamSwitcher(),
          IconButton(
            tooltip: 'Templates',
            icon: const Icon(Icons.description_outlined),
            onPressed: async.value?.configured == true
                ? () {
                    Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => const TemplatesScreen(),
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
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(error: '$e'),
        data: (st) {
          if (!st.configured) return const _NotConfiguredView();
          return Column(
            children: [
              HubOfflineBanner(
                staleSince: st.staleSince,
                onRetry: () => ref.read(hubProvider.notifier).refreshAll(),
              ),
              if (st.error != null && st.staleSince == null)
                _ErrorBanner(text: st.error!),
              Expanded(child: _ProjectsTab(items: st.projects)),
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
    final present = stewardPresent(hub.agents);
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
              : 'No steward — tap to spawn',
          child: Material(
            color: bg,
            borderRadius: BorderRadius.circular(16),
            child: InkWell(
              borderRadius: BorderRadius.circular(16),
              onTap: () => present
                  ? openHubMetaChannel(context, ref)
                  : showSpawnStewardSheet(context, hosts: hub.hosts),
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

}

/// A steward counts as "present" when any agent with handle=='steward' is
/// in an active lifecycle state (pending or running). We include 'pending'
/// because a freshly-spawned steward is on its way up — no reason to flash
/// "No steward" during the 3s reconcile window. Top-level so both the
/// AppBar chip and the W4 auto-bootstrap trigger share one definition.
bool stewardPresent(List<Map<String, dynamic>> agents) {
  for (final a in agents) {
    if ((a['handle'] ?? '').toString() != 'steward') continue;
    final s = (a['status'] ?? '').toString();
    if (s == 'running' || s == 'pending') return true;
  }
  return false;
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

class _ProjectsTab extends ConsumerWidget {
  final List<Map<String, dynamic>> items;
  const _ProjectsTab({required this.items});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Roll up open attention items by project so each row can surface
    // how many things need you on that project — the canonical "needs
    // you" signal per blueprint §6.8. Uses the already-loaded attention
    // list (HubNotifier.refreshAll → listAttention status=open), so this
    // is free: no extra fetch.
    final l10n = AppLocalizations.of(context)!;
    final attention = ref.watch(hubProvider).value?.attention ?? const [];
    final openByProject = <String, int>{};
    for (final a in attention) {
      final pid = (a['project_id'] ?? '').toString();
      if (pid.isEmpty) continue;
      openByProject[pid] = (openByProject[pid] ?? 0) + 1;
    }
    // Partition on `kind` per blueprint §6.1: goal vs. standing. The
    // schema is one table; the mobile IA splits them into two named
    // sections (Projects vs. Workspaces) since the mental models differ
    // (bounded outcome vs. ongoing container).
    //
    // Within each section, W5 flattens sub-projects inline under their
    // parent with a 12px-per-level indent and a thin left rail, rather
    // than collapsing them behind a tap. Attention-first (Blueprint A1)
    // beats scroll savings — open attention on a child must be visible
    // without drilling in.
    final goals = <Map<String, dynamic>>[];
    final standings = <Map<String, dynamic>>[];
    for (final p in items) {
      final kind = (p['kind'] ?? 'goal').toString();
      if (kind == 'standing') {
        standings.add(p);
      } else {
        goals.add(p);
      }
    }
    final goalRows = _flattenWithChildren(goals);
    final standingRows = _flattenWithChildren(standings);

    final body = items.isEmpty
        ? _EmptyText(text: l10n.projectsEmpty)
        : RefreshIndicator(
            onRefresh: () => ref.read(hubProvider.notifier).refreshAll(),
            child: CustomScrollView(
              slivers: [
                if (goalRows.isNotEmpty) ...[
                  SliverToBoxAdapter(
                    child: _ProjectsSectionLabel(text: l10n.sectionProjects),
                  ),
                  SliverPadding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                    sliver: SliverList.separated(
                      itemCount: goalRows.length,
                      separatorBuilder: (_, __) => const SizedBox(height: 8),
                      itemBuilder: (_, i) => _projectRow(
                        context,
                        goalRows[i],
                        openByProject,
                      ),
                    ),
                  ),
                ],
                if (standingRows.isNotEmpty) ...[
                  SliverToBoxAdapter(
                    child: _ProjectsSectionLabel(text: l10n.sectionWorkspaces),
                  ),
                  SliverPadding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                    sliver: SliverList.separated(
                      itemCount: standingRows.length,
                      separatorBuilder: (_, __) => const SizedBox(height: 8),
                      itemBuilder: (_, i) => _projectRow(
                        context,
                        standingRows[i],
                        openByProject,
                      ),
                    ),
                  ),
                ],
                const SliverPadding(padding: EdgeInsets.only(bottom: 96)),
              ],
            ),
          );
    return Stack(
      children: [
        Positioned.fill(child: body),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.extended(
            heroTag: 'hub-projects-fab',
            onPressed: () => _openCreateMenu(context, ref),
            tooltip: l10n.projectCreateFabTooltip,
            icon: const Icon(Icons.add),
            label: Text(l10n.projectCreateFabLabel),
          ),
        ),
      ],
    );
  }

  /// Flattens a section's projects with their direct children inlined
  /// right under each parent, in the order the list came in. Children
  /// whose parent isn't in this section are rendered as orphan parents
  /// at depth 0 so archived-parent drift doesn't hide rows (W5 edge case).
  /// Depth is clamped to 1 on the client even though the server caps it
  /// server-side; log-on-clamp rather than drop so a data bug surfaces.
  static List<_ProjectNode> _flattenWithChildren(
    List<Map<String, dynamic>> rows,
  ) {
    final byId = <String, Map<String, dynamic>>{};
    for (final p in rows) {
      final id = (p['id'] ?? '').toString();
      if (id.isNotEmpty) byId[id] = p;
    }
    final childrenByParent = <String, List<Map<String, dynamic>>>{};
    final tops = <Map<String, dynamic>>[];
    for (final p in rows) {
      final parent = (p['parent_project_id'] ?? '').toString();
      if (parent.isNotEmpty && byId.containsKey(parent)) {
        childrenByParent.putIfAbsent(parent, () => []).add(p);
      } else {
        tops.add(p);
      }
    }
    final out = <_ProjectNode>[];
    for (final parent in tops) {
      final pid = (parent['id'] ?? '').toString();
      final kids = childrenByParent[pid] ?? const <Map<String, dynamic>>[];
      out.add(_ProjectNode(
        project: parent,
        depth: 0,
        childCount: kids.length,
      ));
      for (final child in kids) {
        out.add(_ProjectNode(project: child, depth: 1, childCount: 0));
      }
    }
    return out;
  }

  Widget _projectRow(
    BuildContext context,
    _ProjectNode node,
    Map<String, int> openByProject,
  ) {
    final p = node.project;
    final kind = (p['kind'] ?? 'goal').toString();
    final pid = (p['id'] ?? '').toString();
    final openCount = openByProject[pid] ?? 0;
    // For parents, fold children's own open attention into the parent
    // row's count so the roll-up reads "what will touch me if I don't
    // drill in" without hiding child signal behind the parent.
    var rolled = openCount;
    if (node.depth == 0 && node.childCount > 0) {
      for (final p2 in items) {
        if ((p2['parent_project_id'] ?? '').toString() != pid) continue;
        rolled += openByProject[(p2['id'] ?? '').toString()] ?? 0;
      }
    }
    // Parent aggregate subtitle: "N sub-projects · M attention". The
    // %-done figure is intentionally omitted — it would require fetching
    // tasks per child and the parent row is a summary, not a dashboard.
    // Children's own rows carry their status, which is the authoritative
    // per-child progress signal.
    String subtitle;
    if (node.depth == 0 && node.childCount > 0) {
      final childLabel = kind == 'standing'
          ? (node.childCount == 1 ? 'sub-Workspace' : 'sub-Workspaces')
          : (node.childCount == 1 ? 'sub-project' : 'sub-projects');
      final parts = <String>[
        '${node.childCount} $childLabel',
      ];
      final status = (p['status'] ?? '').toString();
      if (status.isNotEmpty) parts.add(status);
      subtitle = parts.join(' · ');
    } else {
      subtitle = (p['status'] ?? '').toString();
    }
    final tile = _InfoTile(
      title: p['name']?.toString() ?? '?',
      subtitle: subtitle,
      leading: ProjectKindChip(kind: kind),
      trailingWidget:
          rolled > 0 ? _AttentionBadge(count: rolled) : null,
      trailing:
          rolled > 0 ? null : _shortTs((p['created_at'] ?? '') as String),
      onTap: () {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => ProjectDetailScreen(project: p),
        ));
      },
    );
    if (node.depth == 0) return tile;
    // Child row: 12px indent + a thin 1px left rail in the gutter. The
    // rail is drawn as a separate child in an IntrinsicHeight row so it
    // spans exactly the row height — cleaner than a Stack with
    // Positioned.fill, which was flaky when the tile changed height.
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.only(left: 12),
      child: IntrinsicHeight(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(
              width: 1,
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
            ),
            const SizedBox(width: 11),
            Expanded(child: tile),
          ],
        ),
      ),
    );
  }

  Future<void> _openCreateMenu(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context)!;
    final choice = await showModalBottomSheet<String>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.flag_outlined),
              title: Text(l10n.newProject),
              subtitle: Text(l10n.kindProjectHelper),
              onTap: () => Navigator.of(ctx).pop('goal'),
            ),
            ListTile(
              leading: const Icon(Icons.all_inclusive),
              title: Text(l10n.newWorkspace),
              subtitle: Text(l10n.kindWorkspaceHelper),
              onTap: () => Navigator.of(ctx).pop('standing'),
            ),
          ],
        ),
      ),
    );
    if (choice == null || !context.mounted) return;
    await _openCreateSheet(context, ref, initialKind: choice);
  }

  Future<void> _openCreateSheet(
    BuildContext context,
    WidgetRef ref, {
    String initialKind = 'goal',
  }) async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ProjectCreateSheet(initialKind: initialKind),
    );
    if (created == true) {
      await ref.read(hubProvider.notifier).refreshAll();
    }
  }
}

/// Display row for the Projects tab: a project + its tree metadata.
/// Depth 0 = top-level row; depth 1 = sub-project row rendered with the
/// indent + left-rail treatment. Max depth is 2 (server-enforced, clamped
/// client-side in [_ProjectsTab._flattenWithChildren]).
class _ProjectNode {
  final Map<String, dynamic> project;
  final int depth;
  final int childCount;
  const _ProjectNode({
    required this.project,
    required this.depth,
    required this.childCount,
  });
}

class _ProjectsSectionLabel extends StatelessWidget {
  final String text;
  const _ProjectsSectionLabel({required this.text});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 16, 20, 2),
      child: Text(
        text.toUpperCase(),
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: muted,
          letterSpacing: 0.8,
        ),
      ),
    );
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

/// Small pill shown on project rows and the project Overview to surface
/// open attention count. Muted warning color — not a red-dot "unread"
/// because attention items are approvals/decisions, not missed messages.
class _AttentionBadge extends StatelessWidget {
  final int count;
  const _AttentionBadge({required this.count});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: DesignColors.warning.withValues(alpha: 0.6),
        ),
      ),
      child: Text(
        '$count open',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.warning,
        ),
      ),
    );
  }
}

class _InfoTile extends StatelessWidget {
  final String title;
  final String subtitle;
  final String? trailing;
  final Widget? trailingWidget;
  final Widget? leading;
  final VoidCallback? onTap;
  const _InfoTile({
    required this.title,
    required this.subtitle,
    this.trailing,
    this.trailingWidget,
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
          if (trailingWidget != null)
            trailingWidget!
          else if (trailing != null && trailing!.isNotEmpty)
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

/// Opens the agent detail sheet (pause / resume / terminate / pane preview /
/// journal / respawn). Exposed so the project detail Agents pill can reach
/// the same sheet without duplicating the lifecycle code.
void openAgentDetail(BuildContext context, Map<String, dynamic> agent) {
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

/// Opens the team-host detail sheet. Exposed so the unified Hosts tab
/// (lib/screens/hosts/hosts_screen.dart, Wedge 2) can reach the same sheet
/// without duplicating the bind/unbind/enter-pane flow.
void openHostDetail(BuildContext context, Map<String, dynamic> host) {
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

  Map<String, dynamic> _parsedHint() {
    final raw = widget.host['ssh_hint_json'];
    if (raw == null) return const {};
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {
        // Fall through — a malformed hint shouldn't crash the sheet.
      }
    }
    return const {};
  }

  Future<void> _enterPane() async {
    final hostId = widget.host['id']?.toString() ?? '';
    if (hostId.isEmpty) return;
    final bindings = ref.read(hostBindingsProvider.notifier);
    final connections = ref.read(connectionsProvider);
    final existingId = bindings.connectionIdFor(hostId);
    String? connectionId;
    if (existingId != null &&
        connections.connections.any((c) => c.id == existingId)) {
      connectionId = existingId;
    } else {
      // No binding yet (or it points to a deleted connection) — open the
      // form pre-filled from the host's non-secret hint. The form pops
      // the new connection id on save; record it as the binding.
      final hint = {
        'name': widget.host['name']?.toString() ?? '',
        ..._parsedHint(),
      };
      final result = await Navigator.of(context).push<Object?>(
        MaterialPageRoute(
          builder: (_) => ConnectionFormScreen(initialHint: hint),
        ),
      );
      if (!mounted) return;
      if (result is String && result.isNotEmpty) {
        await bindings.bind(hostId, result);
        connectionId = result;
      } else {
        return;
      }
    }
    if (!mounted) return;
    // Capture the root navigator before popping the bottom sheet — this
    // context will be unmounted as soon as we pop.
    final rootNav = Navigator.of(context, rootNavigator: true);
    Navigator.of(context).pop();
    rootNav.push(
      MaterialPageRoute(
        builder: (_) => TerminalScreen(connectionId: connectionId!),
      ),
    );
  }

  Future<void> _unbind() async {
    final hostId = widget.host['id']?.toString() ?? '';
    if (hostId.isEmpty) return;
    await ref.read(hostBindingsProvider.notifier).unbind(hostId);
    if (mounted) setState(() {});
  }

  Future<void> _edit() async {
    final updated = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => HostEditSheet(host: widget.host),
    );
    if (updated == true && mounted) {
      // The host row in widget.host is stale after a write; pop the sheet
      // so the caller re-opens against the refreshed list.
      Navigator.of(context).pop();
    }
  }

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
                  icon: const Icon(Icons.edit_outlined),
                  tooltip: 'Edit host',
                  onPressed: _busy ? null : _edit,
                ),
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
            if (_parsedHint().isNotEmpty)
              _kv('SSH hint', _formatHint(_parsedHint()), mutedColor),
            Builder(builder: (_) {
              final hostId = h['id']?.toString() ?? '';
              final bindingId = ref.watch(hostBindingsProvider)[hostId];
              if (bindingId == null) return _kv('Bound', 'none', mutedColor);
              final conns = ref.watch(connectionsProvider).connections;
              Connection? bound;
              for (final c in conns) {
                if (c.id == bindingId) {
                  bound = c;
                  break;
                }
              }
              if (bound == null) return _kv('Bound', 'none', mutedColor);
              return _kv('Bound', '${bound.name} (${bound.host})', mutedColor);
            }),
            const SizedBox(height: 16),
            if (_error != null) ...[
              Text(_error!,
                  style: const TextStyle(color: DesignColors.error)),
              const SizedBox(height: 8),
            ],
            FilledButton.icon(
              onPressed: _busy ? null : _enterPane,
              style: FilledButton.styleFrom(
                backgroundColor: DesignColors.terminalCyan,
                foregroundColor: Colors.white,
              ),
              icon: const Icon(Icons.terminal),
              label: const Text('Enter pane'),
            ),
            const SizedBox(height: 8),
            Builder(builder: (_) {
              final hostId = h['id']?.toString() ?? '';
              final hasBinding =
                  ref.watch(hostBindingsProvider).containsKey(hostId);
              if (!hasBinding) return const SizedBox.shrink();
              return Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: OutlinedButton.icon(
                  onPressed: _busy ? null : _unbind,
                  icon: const Icon(Icons.link_off, size: 18),
                  label: const Text('Unbind connection'),
                ),
              );
            }),
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

  // Compact one-line rendering of the parsed ssh_hint_json object, e.g.
  // `user@host:2222`. Falls back to a raw key=value join when the common
  // shorthand fields aren't present.
  String _formatHint(Map<String, dynamic> hint) {
    final host = hint['host']?.toString() ?? '';
    final user = hint['username']?.toString() ?? '';
    final port = hint['port'];
    if (host.isNotEmpty) {
      final buf = StringBuffer();
      if (user.isNotEmpty) buf.write('$user@');
      buf.write(host);
      if (port != null && port.toString() != '22') {
        buf.write(':$port');
      }
      return buf.toString();
    }
    return hint.entries.map((e) => '${e.key}=${e.value}').join(' · ');
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
