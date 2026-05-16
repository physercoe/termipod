import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/connection_provider.dart';
import '../../providers/host_binding_provider.dart';
import '../../providers/hub_provider.dart';
import '../../services/keychain/secure_storage.dart';
import '../../theme/design_colors.dart';
import '../../widgets/hub_tile.dart';
import '../../widgets/team_switcher.dart';
import '../connections/connection_form_screen.dart';
import '../projects/projects_screen.dart' show openHostDetail;
import '../terminal/terminal_screen.dart';
import '../vault/vault_screen.dart';
import 'hub_detail_screen.dart';

/// Unified Hosts tab (Tier-2) per `docs/ia-redesign.md` §5 + §6.4.
///
/// One row per physical machine. Rows are the union of:
/// * local SSH bookmarks (`Connection`), scope `personal`
/// * hub-registered hosts (`HubState.hosts`), scope `team`
///
/// Join key is [HostBindingsNotifier]: when a hub host id is bound to a
/// connection id, the two fold into one `team+personal` row.
///
/// Tap behavior:
/// * row has a Connection → open TerminalScreen directly (credentials live
///   on this device)
/// * row is team-only → open the hub's host detail sheet (Enter-pane flow
///   inside will prompt to create a bookmark and bind it)
///
/// Wedge 2 scope: view-and-tap unification. Credential attachment UI,
/// register-from-phone, and three-verb delete semantics are deferred to
/// follow-up wedges (see `docs/ia-redesign.md` §5.4, §11).
enum HostScope { personal, team, teamPersonal }

class _HostRow {
  final String displayName;
  final String subtitle;
  final HostScope scope;
  final Connection? connection;
  final Map<String, dynamic>? hubHost;

  const _HostRow({
    required this.displayName,
    required this.subtitle,
    required this.scope,
    this.connection,
    this.hubHost,
  });
}

class HostsScreen extends ConsumerStatefulWidget {
  const HostsScreen({super.key});

  @override
  ConsumerState<HostsScreen> createState() => _HostsScreenState();
}

/// Sort order for the merged Hosts list. The page used to ship with a
/// name-vs-time picker that fell off in an earlier refactor; this
/// restores it. Persistence is in-memory for the screen lifetime —
/// the user's choice survives backgrounding the app via Riverpod's
/// keep-alive but resets across cold starts. Good enough for MVP;
/// can promote to SharedPreferences once we add a user-prefs sheet.
enum _HostSort {
  name,
  lastActive,
  status,
}

class _HostsScreenState extends ConsumerState<HostsScreen> {
  bool _hubHostsExpanded = true;
  bool _personalHostsExpanded = true;
  _HostSort _sort = _HostSort.name;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;

    final connections = ref.watch(connectionsProvider).connections;
    final bindings = ref.watch(hostBindingsProvider);
    final hubAsync = ref.watch(hubProvider);
    final hubHosts = hubAsync.value?.hosts ?? const <Map<String, dynamic>>[];
    final hubStats = hubAsync.value?.hubStats;
    final hubBaseUrl = hubAsync.value?.config?.baseUrl;
    final hubConfigured = hubAsync.value?.configured ?? false;

    final rows = _mergeRows(
      connections: connections,
      bindings: bindings,
      hubHosts: hubHosts,
    );
    _sortRows(rows);
    // Group rows by their relationship to the hub. teamPersonal hosts
    // (locally-bookmarked + hub-registered) live under HUB because the
    // hub side is the authoritative scope; personal-only hosts have no
    // hub presence and go in their own group.
    final hubChildRows = [
      for (final r in rows)
        if (r.scope != HostScope.personal) r,
    ];
    final personalRows = [
      for (final r in rows)
        if (r.scope == HostScope.personal) r,
    ];

    return Scaffold(
      body: CustomScrollView(
        slivers: [
          SliverAppBar(
            floating: true,
            pinned: true,
            expandedHeight: 100,
            backgroundColor: colorScheme.surface.withValues(alpha: 0.95),
            surfaceTintColor: Colors.transparent,
            flexibleSpace: FlexibleSpaceBar(
              titlePadding: const EdgeInsets.only(left: 24, bottom: 16),
              title: Text(
                l10n.tabHosts,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 24,
                  fontWeight: FontWeight.w700,
                  color: colorScheme.onSurface,
                  letterSpacing: -0.5,
                ),
              ),
            ),
            actions: [
              const TeamSwitcher(),
              PopupMenuButton<_HostSort>(
                tooltip: 'Sort',
                icon: const Icon(Icons.sort),
                onSelected: (v) => setState(() => _sort = v),
                itemBuilder: (_) => [
                  CheckedPopupMenuItem(
                    value: _HostSort.name,
                    checked: _sort == _HostSort.name,
                    child: const Text('Name (A→Z)'),
                  ),
                  CheckedPopupMenuItem(
                    value: _HostSort.lastActive,
                    checked: _sort == _HostSort.lastActive,
                    child: const Text('Last active (newest first)'),
                  ),
                  CheckedPopupMenuItem(
                    value: _HostSort.status,
                    checked: _sort == _HostSort.status,
                    child: const Text('Status (online first)'),
                  ),
                ],
              ),
              IconButton(
                icon: const Icon(Icons.refresh),
                tooltip: 'Refresh',
                onPressed: () async {
                  ref.invalidate(connectionsProvider);
                  final notifier = ref.read(hubProvider.notifier);
                  if (notifier.client != null) {
                    await notifier.refreshAll();
                  }
                },
              ),
            ],
          ),
          if (hubConfigured) ...[
            SliverToBoxAdapter(
              child: _CollapsibleHeader(
                label: 'Hub',
                count: hubChildRows.length,
                expanded: _hubHostsExpanded,
                onTap: () => setState(
                    () => _hubHostsExpanded = !_hubHostsExpanded),
              ),
            ),
            SliverPadding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
              sliver: SliverToBoxAdapter(
                child: HubTile(
                  name: _hubDisplayName(hubStats, hubBaseUrl),
                  stats: hubStats,
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const HubDetailScreen(),
                    ),
                  ),
                ),
              ),
            ),
            if (_hubHostsExpanded)
              SliverPadding(
                // Indent the children visually so they read as
                // belonging to the hub above. A thin left rail on
                // each tile would be tidier than padding alone but
                // would touch every theme; padding is good enough
                // for an MVP grouping cue.
                padding: const EdgeInsets.fromLTRB(32, 0, 16, 8),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, i) => Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: _HostTile(row: hubChildRows[i]),
                    ),
                    childCount: hubChildRows.length,
                  ),
                ),
              ),
            if (_hubHostsExpanded && hubChildRows.isEmpty)
              const SliverToBoxAdapter(
                child: Padding(
                  padding: EdgeInsets.fromLTRB(32, 0, 16, 12),
                  child: _NoHostsHint(text: 'No hub-registered hosts yet.'),
                ),
              ),
          ],
          SliverToBoxAdapter(
            child: _CollapsibleHeader(
              label: 'Personal',
              count: personalRows.length,
              expanded: _personalHostsExpanded,
              onTap: () => setState(
                  () => _personalHostsExpanded = !_personalHostsExpanded),
            ),
          ),
          if (rows.isEmpty)
            SliverToBoxAdapter(child: _EmptyState(l10n: l10n))
          else if (_personalHostsExpanded && personalRows.isNotEmpty)
            SliverPadding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              sliver: SliverList(
                delegate: SliverChildBuilderDelegate(
                  (context, i) => Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: _HostTile(row: personalRows[i]),
                  ),
                  childCount: personalRows.length,
                ),
              ),
            )
          else if (_personalHostsExpanded && rows.isNotEmpty)
            const SliverToBoxAdapter(
              child: Padding(
                padding: EdgeInsets.fromLTRB(20, 0, 16, 12),
                child:
                    _NoHostsHint(text: 'No personal-only bookmarks yet.'),
              ),
            ),
          const SliverToBoxAdapter(child: _VaultSection()),
          const SliverToBoxAdapter(child: SizedBox(height: 120)),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () async {
          final result = await Navigator.of(context).push<Object?>(
            MaterialPageRoute(
              builder: (_) => const ConnectionFormScreen(),
            ),
          );
          if (result != null) ref.invalidate(connectionsProvider);
        },
        icon: const Icon(Icons.add),
        label: Text(l10n.hostsAddBookmark),
      ),
    );
  }

  List<_HostRow> _mergeRows({
    required List<Connection> connections,
    required Map<String, String> bindings,
    required List<Map<String, dynamic>> hubHosts,
  }) {
    // connectionId → hub host map, via inverted bindings.
    final connIdToHubHost = <String, Map<String, dynamic>>{};
    final boundHubIds = <String>{};
    for (final entry in bindings.entries) {
      final hubId = entry.key;
      final connId = entry.value;
      for (final h in hubHosts) {
        if (h['id']?.toString() == hubId) {
          connIdToHubHost[connId] = h;
          boundHubIds.add(hubId);
          break;
        }
      }
    }

    final rows = <_HostRow>[];

    // Rows from Connections first — stable order, user's local world.
    for (final c in connections) {
      final hub = connIdToHubHost[c.id];
      rows.add(_HostRow(
        displayName: c.name,
        subtitle: _connectionSubtitle(c),
        scope: hub == null ? HostScope.personal : HostScope.teamPersonal,
        connection: c,
        hubHost: hub,
      ));
    }

    // Unbound team hosts.
    for (final h in hubHosts) {
      final hubId = h['id']?.toString() ?? '';
      if (hubId.isEmpty || boundHubIds.contains(hubId)) continue;
      rows.add(_HostRow(
        displayName: h['name']?.toString() ?? hubId,
        subtitle: _hubHostSubtitle(h),
        scope: HostScope.team,
        hubHost: h,
      ));
    }

    return rows;
  }

  // Stable comparator over the union of personal + team rows. Each
  // sort key reads from whichever side has the data:
  //   - name: lowercase displayName, both sides have it
  //   - lastActive: hub_host.last_seen_at (ISO string) when present,
  //     otherwise hub_host.created_at, otherwise epoch — connections
  //     don't track last-active locally so they sort to the bottom.
  //   - status: hub_host.status with online > pending > offline >
  //     unknown; personal-only rows sit between online and offline
  //     because they're "reachable in principle".
  void _sortRows(List<_HostRow> rows) {
    int cmpName(_HostRow a, _HostRow b) =>
        a.displayName.toLowerCase().compareTo(b.displayName.toLowerCase());
    DateTime lastActive(_HostRow r) {
      final h = r.hubHost;
      if (h == null) return DateTime.fromMillisecondsSinceEpoch(0);
      final raw = (h['last_seen_at'] ?? h['created_at'] ?? '').toString();
      return DateTime.tryParse(raw) ??
          DateTime.fromMillisecondsSinceEpoch(0);
    }
    int statusRank(_HostRow r) {
      final h = r.hubHost;
      if (h == null) return 2; // personal-only — neutral
      switch ((h['status'] ?? '').toString()) {
        case 'online':
          return 0;
        case 'pending':
          return 1;
        case 'offline':
          return 3;
        default:
          return 4;
      }
    }
    switch (_sort) {
      case _HostSort.name:
        rows.sort(cmpName);
      case _HostSort.lastActive:
        rows.sort((a, b) {
          final byTime = lastActive(b).compareTo(lastActive(a));
          if (byTime != 0) return byTime;
          return cmpName(a, b);
        });
      case _HostSort.status:
        rows.sort((a, b) {
          final byStatus = statusRank(a).compareTo(statusRank(b));
          if (byStatus != 0) return byStatus;
          return cmpName(a, b);
        });
    }
  }

  String _connectionSubtitle(Connection c) {
    final port = c.port == 22 ? '' : ':${c.port}';
    return '${c.username}@${c.host}$port';
  }

  String _hubDisplayName(
      Map<String, dynamic>? stats, String? baseUrl) {
    final machine = stats?['machine'];
    if (machine is Map) {
      final hostname = machine['hostname']?.toString();
      if (hostname != null && hostname.isNotEmpty) return hostname;
    }
    if (baseUrl != null && baseUrl.isNotEmpty) {
      // Strip scheme + trailing slash for the tile label.
      var s = baseUrl;
      final colon = s.indexOf('://');
      if (colon >= 0) s = s.substring(colon + 3);
      if (s.endsWith('/')) s = s.substring(0, s.length - 1);
      return s;
    }
    return 'Hub';
  }

  String _hubHostSubtitle(Map<String, dynamic> h) {
    final hint = h['ssh_hint_json'];
    Map<String, dynamic>? parsed;
    if (hint is Map) {
      parsed = hint.cast<String, dynamic>();
    } else if (hint is String && hint.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(hint);
        if (decoded is Map) parsed = decoded.cast<String, dynamic>();
      } catch (_) {
        // Malformed hint — fall through to status-only subtitle.
      }
    }
    if (parsed != null) {
      final host = parsed['host']?.toString() ?? '';
      final user = parsed['username']?.toString() ?? '';
      final port = parsed['port']?.toString();
      if (host.isNotEmpty) {
        final portPart = (port == null || port.isEmpty || port == '22')
            ? ''
            : ':$port';
        final userPart = user.isEmpty ? '' : '$user@';
        return '$userPart$host$portPart';
      }
    }
    return 'status: ${h['status'] ?? 'unknown'}';
  }
}

class _EmptyState extends StatelessWidget {
  final AppLocalizations l10n;
  const _EmptyState({required this.l10n});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.dns_outlined,
                size: 64, color: Theme.of(context).disabledColor),
            const SizedBox(height: 16),
            Text(l10n.hostsEmpty,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 18, fontWeight: FontWeight.w700)),
            const SizedBox(height: 8),
            Text(l10n.hostsEmptyDesc, textAlign: TextAlign.center),
          ],
        ),
      ),
    );
  }
}

class _HostTile extends ConsumerWidget {
  final _HostRow row;
  const _HostTile({required this.row});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;

    return Material(
      color: cardBg,
      borderRadius: BorderRadius.circular(12),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: () => _handleTap(context, ref),
        onLongPress: () => _showActionSheet(context, ref),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(row.displayName,
                        style: GoogleFonts.spaceGrotesk(
                            fontSize: 15, fontWeight: FontWeight.w700)),
                    const SizedBox(height: 2),
                    Text(row.subtitle,
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 11,
                            color: Theme.of(context).hintColor)),
                  ],
                ),
              ),
              _ScopeBadge(
                scope: row.scope,
                hostStatus: (row.hubHost?['status'] ?? '').toString(),
              ),
              const SizedBox(width: 4),
              if (row.connection != null)
                IconButton(
                  icon: const Icon(Icons.bolt),
                  tooltip: row.connection!.isRawMode
                      ? 'Raw shell'
                      : 'Open raw shell (bypass tmux)',
                  onPressed: () => _openTerminal(context, ref, raw: true),
                ),
            ],
          ),
        ),
      ),
    );
  }

  void _handleTap(BuildContext context, WidgetRef ref) {
    if (row.connection != null) {
      _openTerminal(context, ref);
    } else if (row.hubHost != null) {
      openHostDetail(context, row.hubHost!);
    }
  }

  /// Open the terminal for this row's connection. When [raw] is true the
  /// PTY is forced into plain-shell mode even on tmux-configured hosts —
  /// restores the "rightmost icon = direct shell" affordance the older
  /// Servers card had. Default tap still uses the connection's configured
  /// mode (tmux → session picker inside the terminal screen).
  void _openTerminal(BuildContext context, WidgetRef ref, {bool raw = false}) {
    final c = row.connection;
    if (c == null) return;
    ref.read(connectionsProvider.notifier).updateLastConnected(c.id);
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => TerminalScreen(
          connectionId: c.id,
          forceRawMode: raw,
        ),
      ),
    );
  }

  void _showActionSheet(BuildContext context, WidgetRef ref) {
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (row.hubHost != null)
                ListTile(
                  leading: const Icon(Icons.cloud_outlined),
                  title: const Text('Team host details'),
                  onTap: () {
                    Navigator.pop(ctx);
                    openHostDetail(context, row.hubHost!);
                  },
                ),
              if (row.connection != null)
                ListTile(
                  leading: const Icon(Icons.edit_outlined),
                  title: const Text('Edit bookmark'),
                  onTap: () async {
                    Navigator.pop(ctx);
                    final result = await Navigator.of(context).push<bool>(
                      MaterialPageRoute(
                        builder: (_) => ConnectionFormScreen(
                          connectionId: row.connection!.id,
                        ),
                      ),
                    );
                    if (result == true) ref.invalidate(connectionsProvider);
                  },
                ),
              if (row.connection != null)
                ListTile(
                  leading: Icon(Icons.delete_outline,
                      color: Theme.of(context).colorScheme.error),
                  title: Text('Remove local bookmark',
                      style: TextStyle(
                          color: Theme.of(context).colorScheme.error)),
                  onTap: () async {
                    Navigator.pop(ctx);
                    await _confirmDeleteBookmark(context, ref);
                  },
                ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _confirmDeleteBookmark(
      BuildContext context, WidgetRef ref) async {
    final c = row.connection;
    if (c == null) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (dctx) => AlertDialog(
        title: Text('Remove "${c.name}"?'),
        content: const Text(
          'Deletes this device\'s credentials for the host. If the host is '
          'also team-registered, the row stays visible until credentials '
          'are re-added. Team state is untouched.',
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(dctx, false),
              child: const Text('Cancel')),
          TextButton(
            style: TextButton.styleFrom(
                foregroundColor: Theme.of(context).colorScheme.error),
            onPressed: () => Navigator.pop(dctx, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await SecureStorageService().deletePassword(c.id);
    await ref.read(connectionsProvider.notifier).remove(c.id);
  }
}

class _ScopeBadge extends StatelessWidget {
  final HostScope scope;
  // Hub-host status string ('online' / 'offline' / 'pending' / '');
  // empty for personal-scope rows that have no hub presence.
  final String hostStatus;
  const _ScopeBadge({required this.scope, this.hostStatus = ''});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final (label, baseColor) = switch (scope) {
      HostScope.personal => (l10n.hostScopePersonal, DesignColors.terminalCyan),
      HostScope.team => (l10n.hostScopeTeam, DesignColors.success),
      HostScope.teamPersonal =>
        (l10n.hostScopeTeamPersonal, DesignColors.terminalMagenta),
    };
    // For hub-registered rows (team / teamPersonal), the chip mirrors
    // host liveness so the user sees at a glance which boxes can take
    // work. Personal-only rows have no hub status, so they keep the
    // intrinsic cyan. 'pending' is a transient post-registration
    // state — warning amber matches the StewardStrip's awaiting-
    // director treatment.
    Color color = baseColor;
    if (scope != HostScope.personal) {
      switch (hostStatus) {
        case 'offline':
          color = DesignColors.terminalRed;
          break;
        case 'pending':
          color = DesignColors.warning;
          break;
        case 'online':
        case '':
        default:
          // online or unknown → keep the scope's intrinsic color.
          break;
      }
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: color.withValues(alpha: 0.4), width: 0.5),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
            fontSize: 10, fontWeight: FontWeight.w600, color: color),
      ),
    );
  }
}

/// Non-collapsible variant of the section label used for the Hub block.
/// Hub is a single tile, not a list, so the chevron + count would be
/// Inline empty-section caption rendered under the Hub or Personal
/// header when the group has no rows. Keeps the section visible (so
/// the user knows where new hosts would land) without the heavier
/// full-screen [_EmptyState] which is reserved for the
/// nothing-at-all case.
class _NoHostsHint extends StatelessWidget {
  final String text;
  const _NoHostsHint({required this.text});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Text(
      text,
      style: GoogleFonts.spaceGrotesk(
        fontSize: 12,
        fontStyle: FontStyle.italic,
        color: muted,
      ),
    );
  }
}

/// Tappable section header used to fold the host list. Mirrors the
/// section-label rhythm used elsewhere (Me, Projects) but adds a chevron
/// + count so the user can collapse the list when the Vault card below
/// is what they want.
class _CollapsibleHeader extends StatelessWidget {
  final String label;
  final int count;
  final bool expanded;
  final VoidCallback onTap;
  const _CollapsibleHeader({
    required this.label,
    required this.count,
    required this.expanded,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 12, 16, 4),
        child: Row(
          children: [
            Text(
              label.toUpperCase(),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: muted,
                letterSpacing: 0.8,
              ),
            ),
            const SizedBox(width: 8),
            Text(
              '$count',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: muted,
              ),
            ),
            const Spacer(),
            Icon(
              expanded ? Icons.expand_less : Icons.expand_more,
              size: 20,
              color: muted,
            ),
          ],
        ),
      ),
    );
  }
}

/// Device-scoped Vault — surfaces snippets + command-history collections.
/// Lives on Hosts because the host list and the per-host shortcuts are
/// the same mental "this device's terminal toolkit"; pulling Vault here
/// also keeps the Me tab focused on attention items rather than tools.
class _VaultSection extends StatelessWidget {
  const _VaultSection();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
          child: Text(
            'VAULT',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: muted,
              letterSpacing: 0.8,
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
          child: Material(
            color: bg,
            borderRadius: BorderRadius.circular(12),
            child: InkWell(
              borderRadius: BorderRadius.circular(12),
              onTap: () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => const VaultScreen(),
              )),
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: border),
                ),
                child: Row(
                  children: [
                    Icon(Icons.inventory_2_outlined,
                        size: 20, color: DesignColors.primary),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('Snippets & History',
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 14,
                                fontWeight: FontWeight.w700,
                              )),
                          const SizedBox(height: 2),
                          Text(
                            'Terminal shortcuts and recent commands',
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: 11,
                              color: muted,
                            ),
                          ),
                        ],
                      ),
                    ),
                    Icon(Icons.chevron_right, size: 18, color: muted),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}
