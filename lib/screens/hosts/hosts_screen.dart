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
import '../../widgets/team_switcher.dart';
import '../connections/connection_form_screen.dart';
import '../projects/projects_screen.dart' show openHostDetail;
import '../terminal/terminal_screen.dart';
import '../vault/vault_screen.dart';

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

class _HostsScreenState extends ConsumerState<HostsScreen> {
  bool _hostsExpanded = true;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;

    final connections = ref.watch(connectionsProvider).connections;
    final bindings = ref.watch(hostBindingsProvider);
    final hubAsync = ref.watch(hubProvider);
    final hubHosts = hubAsync.value?.hosts ?? const <Map<String, dynamic>>[];

    final rows = _mergeRows(
      connections: connections,
      bindings: bindings,
      hubHosts: hubHosts,
    );

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
          SliverToBoxAdapter(
            child: _CollapsibleHeader(
              label: 'Hosts',
              count: rows.length,
              expanded: _hostsExpanded,
              onTap: () =>
                  setState(() => _hostsExpanded = !_hostsExpanded),
            ),
          ),
          if (rows.isEmpty)
            SliverToBoxAdapter(child: _EmptyState(l10n: l10n))
          else if (_hostsExpanded)
            SliverPadding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              sliver: SliverList(
                delegate: SliverChildBuilderDelegate(
                  (context, i) => Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: _HostTile(row: rows[i]),
                  ),
                  childCount: rows.length,
                ),
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

  String _connectionSubtitle(Connection c) {
    final port = c.port == 22 ? '' : ':${c.port}';
    return '${c.username}@${c.host}$port';
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
              _ScopeBadge(scope: row.scope),
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
  const _ScopeBadge({required this.scope});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final (label, color) = switch (scope) {
      HostScope.personal => (l10n.hostScopePersonal, DesignColors.terminalCyan),
      HostScope.team => (l10n.hostScopeTeam, DesignColors.success),
      HostScope.teamPersonal =>
        (l10n.hostScopeTeamPersonal, DesignColors.terminalMagenta),
    };
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
