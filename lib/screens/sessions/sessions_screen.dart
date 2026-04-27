import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';
import '../../widgets/agent_feed.dart';
import '../projects/projects_screen.dart' show confirmAndRecreateSteward;
import '../team/spawn_steward_sheet.dart';
import '../team/templates_screen.dart';

/// Merged Sessions/Stewards page (multi-steward wedge 2). Each live
/// steward gets its own section with its current session inline + a
/// collapsible "previous" subsection of closed sessions for that
/// steward. AppBar `+` spawns a new steward; `⋮` opens template /
/// engine management. Per-steward kebab carries Reset (new
/// conversation), Replace, Terminate, Rename.
///
/// Single-steward installs collapse to the one-section view, which
/// reads close to the prior flat-list page.
class SessionsScreen extends ConsumerWidget {
  const SessionsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(sessionsProvider);
    final hubState = ref.watch(hubProvider).value;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Sessions',
          style:
              GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700, fontSize: 18),
        ),
        actions: [
          IconButton(
            tooltip: 'Spawn new steward',
            icon: const Icon(Icons.add),
            onPressed: () => _spawnNewSteward(context, ref),
          ),
          PopupMenuButton<String>(
            tooltip: 'More',
            onSelected: (v) {
              switch (v) {
                case 'templates':
                  // TemplatesScreen has tabs for both prompt/persona
                  // templates and engines (agent_families) — one
                  // entry covers both surfaces.
                  Navigator.of(context).push(MaterialPageRoute(
                    builder: (_) => const TemplatesScreen(),
                  ));
                case 'refresh':
                  ref.read(sessionsProvider.notifier).refresh();
                  ref.read(hubProvider.notifier).refreshAll();
              }
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'templates',
                child: Text('Templates & engines'),
              ),
              PopupMenuDivider(),
              PopupMenuItem(value: 'refresh', child: Text('Refresh')),
            ],
          ),
        ],
      ),
      body: state.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text('Sessions failed to load: $e',
                style: GoogleFonts.jetBrainsMono(fontSize: 12)),
          ),
        ),
        data: (s) {
          final agents =
              hubState?.agents ?? const <Map<String, dynamic>>[];
          final groups = _groupByStateward(agents, s);
          if (groups.isEmpty) return const _EmptyState();
          return RefreshIndicator(
            onRefresh: () async {
              await ref.read(sessionsProvider.notifier).refresh();
              await ref.read(hubProvider.notifier).refreshAll();
            },
            child: ListView(
              padding: const EdgeInsets.symmetric(vertical: 4),
              children: [
                for (final g in groups) _StewardSection(group: g),
              ],
            ),
          );
        },
      ),
    );
  }
}

/// One steward's slice of the sessions list. Holds the agent record
/// (so we can render engine/model/host pills) and the sessions sorted
/// into current vs previous.
class _StewardGroup {
  final Map<String, dynamic> agent;
  final Map<String, dynamic>? current; // open or interrupted
  final List<Map<String, dynamic>> previous; // closed
  const _StewardGroup({
    required this.agent,
    required this.current,
    required this.previous,
  });

  String get handle => (agent['handle'] ?? '').toString();
  String get agentId => (agent['id'] ?? '').toString();
  String get kind => (agent['kind'] ?? '').toString();
  String get status => (agent['status'] ?? '').toString();
}

/// Build one section per live steward + one section per "orphan"
/// (sessions whose current_agent_id doesn't match any live steward —
/// happens when the steward was terminated outside this UI).
/// Stewards without any session at all are still listed (the multi-
/// steward UX invariant says every live steward has one, so this is
/// the back-compat case for installs that pre-date auto_open_session).
List<_StewardGroup> _groupByStateward(
  List<Map<String, dynamic>> agents,
  SessionsState sessions,
) {
  final byAgent = <String, List<Map<String, dynamic>>>{};
  for (final ses in [...sessions.active, ...sessions.previous]) {
    final aid = (ses['current_agent_id'] ?? '').toString();
    byAgent.putIfAbsent(aid, () => []).add(ses);
  }
  final liveStewardIds = <String>{};
  final groups = <_StewardGroup>[];
  for (final a in agents) {
    final handle = (a['handle'] ?? '').toString();
    if (!isStewardHandle(handle)) continue;
    final status = (a['status'] ?? '').toString();
    if (status != 'running' &&
        status != 'pending' &&
        status != 'paused') {
      continue;
    }
    final id = (a['id'] ?? '').toString();
    liveStewardIds.add(id);
    final mine = byAgent[id] ?? const <Map<String, dynamic>>[];
    Map<String, dynamic>? current;
    final previous = <Map<String, dynamic>>[];
    for (final s in mine) {
      final st = (s['status'] ?? '').toString();
      if ((st == 'open' || st == 'interrupted') && current == null) {
        current = s;
      } else {
        previous.add(s);
      }
    }
    groups.add(_StewardGroup(
      agent: a,
      current: current,
      previous: previous,
    ));
  }
  // Sort: stewards with an active session by last_active_at desc;
  // stewards with only previous sessions go to the bottom.
  DateTime ts(_StewardGroup g) {
    final s = g.current ?? (g.previous.isEmpty ? null : g.previous.first);
    if (s == null) return DateTime.fromMillisecondsSinceEpoch(0);
    final raw = (s['last_active_at'] ?? s['opened_at'] ?? '').toString();
    return DateTime.tryParse(raw) ??
        DateTime.fromMillisecondsSinceEpoch(0);
  }
  groups.sort((a, b) {
    if ((a.current == null) != (b.current == null)) {
      return a.current == null ? 1 : -1;
    }
    return ts(b).compareTo(ts(a));
  });
  // Orphan sessions: bucket under a synthetic group so they aren't
  // silently swallowed. Status pill renders "no live agent".
  final orphanSessions = <Map<String, dynamic>>[];
  for (final s in [...sessions.active, ...sessions.previous]) {
    final aid = (s['current_agent_id'] ?? '').toString();
    if (aid.isEmpty || !liveStewardIds.contains(aid)) {
      orphanSessions.add(s);
    }
  }
  if (orphanSessions.isNotEmpty) {
    Map<String, dynamic>? current;
    final previous = <Map<String, dynamic>>[];
    for (final s in orphanSessions) {
      final st = (s['status'] ?? '').toString();
      if ((st == 'open' || st == 'interrupted') && current == null) {
        current = s;
      } else {
        previous.add(s);
      }
    }
    groups.add(_StewardGroup(
      agent: const {'handle': '(no live steward)', 'kind': '', 'status': ''},
      current: current,
      previous: previous,
    ));
  }
  return groups;
}

Future<void> _spawnNewSteward(BuildContext context, WidgetRef ref) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;
  await showSpawnStewardSheet(context, hosts: hub.hosts);
  if (!context.mounted) return;
  await ref.read(hubProvider.notifier).refreshAll();
  await ref.read(sessionsProvider.notifier).refresh();
}

/// Per-steward "Reset (new conversation)": closes the steward's
/// current session and opens a fresh one against the same agent. The
/// agent process keeps running (memory + model state preserved at the
/// engine level); only the visible transcript starts empty. Used by
/// the per-steward kebab on the merged sessions page.
///
/// Carries the prior session's worktree_path + spawn_spec_yaml forward
/// so a future Resume on the new session lands in the same workdir.
Future<void> _resetStewardConversation(
  BuildContext context,
  WidgetRef ref,
  String stewardId,
  String? stewardLabelText,
) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;
  final client = ref.read(hubProvider.notifier).client;
  if (client == null) return;

  final label = stewardLabelText ?? 'this steward';
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Reset conversation?'),
      content: Text(
        'Closes $label\'s current session and opens a fresh one. The '
        'agent process keeps running, so its engine-level memory is '
        'preserved — but the visible transcript starts empty. The '
        'prior conversation goes to Previous.',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(ctx, false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          child: const Text('Reset (new conversation)'),
        ),
      ],
    ),
  );
  if (ok != true) return;

  String? carryWorktree;
  String? carrySpec;
  final state = ref.read(sessionsProvider).value;
  if (state != null) {
    for (final s in state.active) {
      if ((s['current_agent_id'] ?? '').toString() != stewardId) continue;
      final wp = (s['worktree_path'] ?? '').toString();
      final spec = (s['spawn_spec_yaml'] ?? '').toString();
      if (wp.isNotEmpty) carryWorktree = wp;
      if (spec.isNotEmpty) carrySpec = spec;
      try {
        await client.closeSession((s['id'] ?? '').toString());
      } catch (_) {
        // Non-fatal — already closed by another path is fine.
      }
    }
  }

  try {
    await client.openSession(
      agentId: stewardId,
      worktreePath: carryWorktree,
      spawnSpecYaml: carrySpec,
    );
  } catch (e) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Reset failed: $e')),
    );
    return;
  }
  await ref.read(sessionsProvider.notifier).refresh();
}

/// One section per steward on the merged page. Renders:
///   - Header: status pill, handle, engine, model
///   - Per-steward kebab: Reset (new conversation), Replace, Terminate, Rename
///   - Current session inline as a tile (open or interrupted)
///   - Collapsible "previous (N)" subsection of closed sessions
class _StewardSection extends ConsumerStatefulWidget {
  final _StewardGroup group;
  const _StewardSection({required this.group});

  @override
  ConsumerState<_StewardSection> createState() => _StewardSectionState();
}

class _StewardSectionState extends ConsumerState<_StewardSection> {
  bool _showPrevious = false;

  _StewardGroup get group => widget.group;

  Future<void> _rename() async {
    final id = group.agentId;
    if (id.isEmpty) return;
    final next = await _promptForHandle(context, group.handle);
    if (next == null || next == group.handle || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.renameAgent(id, next);
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Rename failed: $e')),
      );
    }
  }

  Future<void> _terminate() async {
    final id = group.agentId;
    if (id.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Terminate steward?'),
        content: Text(
          'Kills ${group.handle}\'s agent process. The session flips to '
          'interrupted and stays in Previous; you can Resume it later or '
          'Replace this steward with a fresh one.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Terminate'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.terminateAgent(id);
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Terminate failed: $e')),
      );
    }
  }

  static Future<String?> _promptForHandle(
    BuildContext context,
    String current,
  ) async {
    final ctrl = TextEditingController(text: current);
    try {
      return await showDialog<String?>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Rename steward'),
          content: TextField(
            controller: ctrl,
            autofocus: true,
            decoration: const InputDecoration(
              hintText: 'steward, research-steward, infra-steward, …',
            ),
            onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, null),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                final v = ctrl.text.trim();
                final err = validateStewardHandle(v);
                if (err != null) {
                  ScaffoldMessenger.of(ctx).showSnackBar(
                    SnackBar(content: Text(err)),
                  );
                  return;
                }
                Navigator.pop(ctx, v);
              },
              child: const Text('Save'),
            ),
          ],
        ),
      );
    } finally {
      ctrl.dispose();
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final isOrphan = group.agentId.isEmpty;
    final hasMenu = !isOrphan;

    final statusColor = switch (group.status) {
      'running' => DesignColors.success,
      'pending' => DesignColors.warning,
      'paused' => DesignColors.textMuted,
      _ => muted,
    };
    final shortKind = group.kind == 'claude-code' ? 'claude' : group.kind;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Container(
        decoration: BoxDecoration(
          border: Border.all(color: border),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // ── Header ─────────────────────────────────────────────
            InkWell(
              onTap: hasMenu
                  ? () {
                      if (group.previous.isNotEmpty) {
                        setState(() => _showPrevious = !_showPrevious);
                      }
                    }
                  : null,
              borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(10)),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(12, 10, 4, 10),
                child: Row(
                  children: [
                    if (!isOrphan) ...[
                      Container(
                        width: 8,
                        height: 8,
                        decoration: BoxDecoration(
                          color: statusColor,
                          shape: BoxShape.circle,
                        ),
                      ),
                      const SizedBox(width: 8),
                    ],
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            group.handle,
                            style: GoogleFonts.spaceGrotesk(
                              fontWeight: FontWeight.w700,
                              fontSize: 14,
                            ),
                            overflow: TextOverflow.ellipsis,
                          ),
                          if (!isOrphan)
                            Text(
                              [
                                if (shortKind.isNotEmpty) shortKind,
                                if (group.status.isNotEmpty) group.status,
                              ].join(' · '),
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: 10,
                                color: muted,
                              ),
                            ),
                        ],
                      ),
                    ),
                    if (hasMenu)
                      PopupMenuButton<String>(
                        tooltip: 'Steward actions',
                        icon: Icon(Icons.more_vert,
                            size: 18, color: muted),
                        onSelected: (v) {
                          switch (v) {
                            case 'reset':
                              _resetStewardConversation(context, ref,
                                  group.agentId, group.handle);
                            case 'replace':
                              confirmAndRecreateSteward(
                                  context, ref, group.agentId);
                            case 'terminate':
                              _terminate();
                            case 'rename':
                              _rename();
                          }
                        },
                        itemBuilder: (_) => const [
                          PopupMenuItem(
                            value: 'reset',
                            child: Text('Reset (new conversation)'),
                          ),
                          PopupMenuItem(
                            value: 'replace',
                            child: Text('Replace steward'),
                          ),
                          PopupMenuDivider(),
                          PopupMenuItem(
                            value: 'rename',
                            child: Text('Rename'),
                          ),
                          PopupMenuItem(
                            value: 'terminate',
                            child: Text('Terminate steward'),
                          ),
                        ],
                      ),
                  ],
                ),
              ),
            ),
            // ── Current session ───────────────────────────────────
            if (group.current != null)
              _SessionTile(session: group.current!)
            else if (!isOrphan)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
                child: Text(
                  'No active session — Reset (new conversation) to start one.',
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 10, color: muted),
                ),
              ),
            // ── Previous (collapsible) ────────────────────────────
            if (group.previous.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: InkWell(
                  onTap: () =>
                      setState(() => _showPrevious = !_showPrevious),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    child: Row(
                      children: [
                        Icon(
                          _showPrevious
                              ? Icons.expand_less
                              : Icons.expand_more,
                          size: 14,
                          color: muted,
                        ),
                        const SizedBox(width: 4),
                        Text(
                          'previous (${group.previous.length})',
                          style: GoogleFonts.jetBrainsMono(
                              fontSize: 10, color: muted),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            if (_showPrevious)
              for (final ses in group.previous) _SessionTile(session: ses),
            const SizedBox(height: 4),
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.forum_outlined, size: 36, color: muted),
            const SizedBox(height: 8),
            Text(
              'No sessions yet',
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 14, fontWeight: FontWeight.w600, color: muted),
            ),
            const SizedBox(height: 4),
            Text(
              'Sessions appear here once a steward starts. After a host '
              'restart, the session will show as Interrupted with a Resume '
              'option.',
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(fontSize: 12, color: muted),
            ),
          ],
        ),
      ),
    );
  }
}

class _SessionTile extends ConsumerStatefulWidget {
  final Map<String, dynamic> session;
  const _SessionTile({required this.session});

  @override
  ConsumerState<_SessionTile> createState() => _SessionTileState();
}

class _SessionTileState extends ConsumerState<_SessionTile> {
  bool _resuming = false;

  Map<String, dynamic> get session => widget.session;

  Future<void> _rename(BuildContext context) async {
    final id = (session['id'] ?? '').toString();
    if (id.isEmpty) return;
    final current = (session['title'] ?? '').toString();
    final next = await _promptForSessionTitle(context, current);
    if (next == null || !mounted) return;
    try {
      await ref.read(sessionsProvider.notifier).rename(id, next);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Rename failed: $e')),
      );
    }
  }

  Future<void> _confirmDelete(BuildContext context) async {
    final id = (session['id'] ?? '').toString();
    if (id.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete this session?'),
        content: const Text(
          'The transcript stays in the audit log but loses its '
          'session-link. This is final.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await ref.read(sessionsProvider.notifier).delete(id);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Delete failed: $e')),
      );
    }
  }

  Future<void> _resume() async {
    final id = (session['id'] ?? '').toString();
    if (id.isEmpty || _resuming) return;
    setState(() => _resuming = true);
    try {
      final newAgentId =
          await ref.read(sessionsProvider.notifier).resume(id);
      if (!mounted) return;
      if (newAgentId != null && newAgentId.isNotEmpty) {
        final title = (session['title'] ?? '').toString();
        Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => SessionChatScreen(
              sessionId: id,
              agentId: newAgentId,
              title: title.isEmpty ? '(untitled session)' : title,
            ),
          ),
        );
      }
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Resume failed: $e')),
      );
    } finally {
      if (mounted) setState(() => _resuming = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final id = (session['id'] ?? '').toString();
    final title = (session['title'] ?? '').toString();
    final status = (session['status'] ?? '').toString();
    final scopeKind = (session['scope_kind'] ?? '').toString();
    final agentId = (session['current_agent_id'] ?? '').toString();
    final lastActive = (session['last_active_at'] ?? '').toString();
    final worktree = (session['worktree_path'] ?? '').toString();

    final statusColor = switch (status) {
      'open' => DesignColors.success,
      'interrupted' => DesignColors.warning,
      'closed' => muted,
      _ => muted,
    };

    final displayTitle = title.isEmpty ? '(untitled session)' : title;

    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
      leading: CircleAvatar(
        radius: 14,
        backgroundColor: statusColor.withValues(alpha: 0.18),
        child: Icon(
          status == 'interrupted'
              ? Icons.pause_circle_outline
              : (status == 'closed' ? Icons.history : Icons.forum_outlined),
          size: 16,
          color: statusColor,
        ),
      ),
      title: Text(
        displayTitle,
        style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w600),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        [
          status,
          if (scopeKind.isNotEmpty) scopeKind,
          if (worktree.isNotEmpty) _shortPath(worktree),
        ].join(' · '),
        style:
            GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: _trailing(context, status, lastActive, muted),
      onTap: agentId.isEmpty
          ? null
          : () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => SessionChatScreen(
                    sessionId: id,
                    agentId: agentId,
                    title: displayTitle,
                  ),
                ),
              ),
    );
  }

  Widget? _trailing(
    BuildContext context,
    String status,
    String lastActive,
    Color muted,
  ) {
    // Single popup menu shared across statuses — Rename is always
    // available; Delete only on closed sessions (open/interrupted have
    // to be closed first per the hub contract).
    // Per-row session menu. Closing an active session was previously
    // an option here but was removed: it violated the multi-steward
    // invariant ("every live steward has a session") by leaving the
    // steward without one. Use Reset (per-steward kebab on the section
    // header) to rotate the conversation, or Terminate (chat AppBar)
    // to end the steward.
    final menu = PopupMenuButton<String>(
      tooltip: 'Session actions',
      icon: Icon(Icons.more_vert, size: 18, color: muted),
      onSelected: (v) {
        if (v == 'rename') _rename(context);
        if (v == 'delete') _confirmDelete(context);
      },
      itemBuilder: (_) => [
        const PopupMenuItem(value: 'rename', child: Text('Rename')),
        if (status == 'closed')
          const PopupMenuItem(value: 'delete', child: Text('Delete')),
      ],
    );
    final timestamp = lastActive.isEmpty
        ? null
        : Text(
            _shortTimestamp(lastActive),
            style:
                GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
          );
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (status == 'interrupted')
          FilledButton.tonal(
            onPressed: _resuming ? null : _resume,
            style: FilledButton.styleFrom(
              backgroundColor:
                  DesignColors.warning.withValues(alpha: 0.16),
              foregroundColor: DesignColors.warning,
              padding: const EdgeInsets.symmetric(
                  horizontal: 12, vertical: 6),
              minimumSize: const Size(0, 32),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            child: _resuming
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('Resume'),
          ),
        if (timestamp != null) ...[
          const SizedBox(width: 6),
          timestamp,
        ],
        menu,
      ],
    );
  }

  // Shared with the chat AppBar's rename action so both surfaces show
  // the same dialog shape.
  static Future<String?> _promptForSessionTitle(
    BuildContext context,
    String current,
  ) async {
    final ctrl = TextEditingController(text: current);
    try {
      return await showDialog<String?>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Rename session'),
          content: TextField(
            controller: ctrl,
            autofocus: true,
            decoration: const InputDecoration(
              hintText: 'Untitled — empty clears the name',
            ),
            onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, null),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
              child: const Text('Save'),
            ),
          ],
        ),
      );
    } finally {
      ctrl.dispose();
    }
  }

  static String _shortPath(String path) {
    if (path.length <= 24) return path;
    return '…${path.substring(path.length - 23)}';
  }

  static String _shortTimestamp(String iso) {
    final ts = DateTime.tryParse(iso);
    if (ts == null) return iso;
    final diff = DateTime.now().toUtc().difference(ts.toUtc());
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${diff.inDays ~/ 7}w';
  }
}

/// Per-session chat view. Thin wrapper over [AgentFeed] scoped to the
/// session's current_agent_id with the session title in the AppBar.
/// AppBar offers a Rename action so the user can title the session
/// while still chatting in it (the Sessions list also exposes Rename
/// via the kebab menu).
class SessionChatScreen extends ConsumerStatefulWidget {
  final String sessionId;
  final String agentId;
  final String title;
  const SessionChatScreen({
    super.key,
    required this.sessionId,
    required this.agentId,
    required this.title,
  });

  @override
  ConsumerState<SessionChatScreen> createState() =>
      _SessionChatScreenState();
}

class _SessionChatScreenState extends ConsumerState<SessionChatScreen> {
  late String _title = widget.title;
  // Latest session.init payload reported up by AgentFeed. Drives the
  // AppBar's compact session chip (model + perm + tool/mcp counts);
  // tap → details sheet. Lifted out of the transcript so the chat
  // surface itself isn't paying a row of vertical real estate for a
  // fixed-shape header.
  Map<String, dynamic>? _sessionInit;

  // Best-effort lookup of the agent's `kind` (engine: claude-code,
  // codex, …) from the cached hub state. Returns null when the agent
  // record isn't loaded yet — the chip falls back to model-only.
  String? _agentKind() {
    final hub = ref.read(hubProvider).value;
    if (hub == null) return null;
    for (final a in hub.agents) {
      if ((a['id'] ?? '').toString() != widget.agentId) continue;
      final kind = (a['kind'] ?? '').toString();
      if (kind.isNotEmpty) return kind;
    }
    return null;
  }

  Future<void> _rename() async {
    final current = _title == '(untitled session)' ? '' : _title;
    final next = await _SessionTileState._promptForSessionTitle(
      context,
      current,
    );
    if (next == null || !mounted) return;
    try {
      await ref
          .read(sessionsProvider.notifier)
          .rename(widget.sessionId, next);
      if (!mounted) return;
      setState(() => _title = next.isEmpty ? '(untitled session)' : next);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Rename failed: $e')),
      );
    }
  }

  /// Terminate the steward agent. Destructive — the agent process is
  /// killed, the session auto-flips to interrupted (and stays that way
  /// until the user explicitly resumes or the steward is replaced).
  ///
  /// "Close session" used to live next to this as a separate action,
  /// but it violated the multi-steward design invariant ("every live
  /// steward has a session"): closing the only active session would
  /// leave a steward agent without a current session, an
  /// agent-without-session intermediate that the design forbids. The
  /// two clean options are now: Reset (new conversation), which
  /// preserves the agent and rotates the transcript via the per-
  /// steward kebab on the Sessions page; or Terminate, which ends
  /// the steward entirely. See docs/wedges/multi-steward.md §9.
  Future<void> _terminateSteward() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Terminate steward?'),
        content: const Text(
          "Kills the steward's agent process. The session will mark as "
          'interrupted; you can Resume it later or replace the steward '
          'with a fresh one (potentially a different engine/model).\n\n'
          "This doesn't delete any history — the transcript stays "
          'available under Previous.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Terminate'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.terminateAgent(widget.agentId);
      // Refresh state so the Sessions list reflects the interruption
      // and the home/projects screens drop the dead steward chip.
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
      if (!mounted) return;
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Terminate failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _title,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          if (_sessionInit != null)
            SessionInitChip(
              payload: _sessionInit!,
              agentKind: _agentKind(),
            ),
          IconButton(
            tooltip: 'Rename session',
            icon: const Icon(Icons.edit_outlined),
            onPressed: _rename,
          ),
          PopupMenuButton<String>(
            tooltip: 'Session actions',
            onSelected: (v) {
              if (v == 'terminate') _terminateSteward();
            },
            itemBuilder: (_) => [
              PopupMenuItem(
                value: 'terminate',
                child: ListTile(
                  leading: Icon(Icons.power_settings_new,
                      color: Theme.of(context).colorScheme.error),
                  title: Text('Terminate steward',
                      style: TextStyle(
                          color: Theme.of(context).colorScheme.error)),
                  subtitle: const Text(
                      'Kills the agent process; session interrupts'),
                  contentPadding: EdgeInsets.zero,
                  dense: true,
                ),
              ),
            ],
          ),
        ],
      ),
      body: AgentFeed(
        agentId: widget.agentId,
        sessionId: widget.sessionId,
        onSessionInit: (p) => setState(() => _sessionInit = p),
      ),
    );
  }
}
