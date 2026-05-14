import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../services/host_label.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';
import '../../widgets/agent_config_sheet.dart';
import '../../widgets/agent_feed.dart';
import '../../widgets/session_details_sheet.dart';
import '../insights/insights_screen.dart';
import '../projects/projects_screen.dart' show confirmAndRecreateSteward;
import '../team/spawn_steward_sheet.dart';
import '../team/templates_screen.dart';
import 'search_screen.dart';

/// Merged Sessions/Stewards page (multi-steward wedge 2). Each live
/// steward gets its own section with its current session inline + a
/// collapsible "previous" subsection of archived sessions for that
/// steward. AppBar `+` spawns a new steward; `⋮` opens template /
/// engine management. Per-steward kebab carries View agent config,
/// Reset (new conversation), Replace, Rename, Stop session.
///
/// Multi-select mode: long-press on a session tile (or the AppBar
/// "Select" menu item) enters select mode. While selecting, tiles
/// render checkboxes, and a bottom action bar exposes batch Archive /
/// Delete. Archive is gated on at least one active/paused row in the
/// selection; Delete is gated on the selection being all-archived (the
/// hub refuses to delete an active or paused session — gating here
/// keeps the user from confusing the failure with a UI bug).
///
/// Single-steward installs collapse to the one-section view, which
/// reads close to the prior flat-list page.
class SessionsScreen extends ConsumerStatefulWidget {
  const SessionsScreen({super.key});

  @override
  ConsumerState<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends ConsumerState<SessionsScreen> {
  // Multi-select state. _selecting toggles the AppBar to select mode
  // and tiles to checkbox mode; _selectedIds tracks which session ids
  // are currently picked. The set is keyed by session id (unique
  // hub-side). Cleared when leaving select mode.
  bool _selecting = false;
  final Set<String> _selectedIds = <String>{};

  // Enter select mode from a long-press on a tile. Pre-selects the
  // tile so the gesture has the same feel as Gmail/Photos: long-press
  // = "select this and turn on multi-pick".
  void _enterSelectWith(String id) {
    setState(() {
      _selecting = true;
      _selectedIds.add(id);
    });
  }

  // Enter select mode from the AppBar kebab. No prime — the user picks
  // which rows to act on after entering.
  void _enterSelectEmpty() {
    setState(() {
      _selecting = true;
    });
  }

  void _exitSelect() {
    setState(() {
      _selecting = false;
      _selectedIds.clear();
    });
  }

  void _toggleSelect(String id) {
    setState(() {
      if (_selectedIds.contains(id)) {
        _selectedIds.remove(id);
      } else {
        _selectedIds.add(id);
      }
    });
  }

  // Gather all session rows (active + previous + orphans) currently
  // visible in the screen. Used by Select-all and by the gating logic
  // for the bottom-bar Archive/Delete actions.
  List<Map<String, dynamic>> _visibleSessions(List<_StewardGroup> groups) {
    final out = <Map<String, dynamic>>[];
    for (final g in groups) {
      if (g.current != null) out.add(g.current!);
      out.addAll(g.previous);
    }
    return out;
  }

  void _selectAll(List<_StewardGroup> groups) {
    setState(() {
      for (final s in _visibleSessions(groups)) {
        final id = (s['id'] ?? '').toString();
        if (id.isNotEmpty) _selectedIds.add(id);
      }
    });
  }

  Future<void> _bulkArchive(List<Map<String, dynamic>> visible) async {
    final ids = <String>[];
    for (final s in visible) {
      final id = (s['id'] ?? '').toString();
      if (id.isEmpty || !_selectedIds.contains(id)) continue;
      // Skip rows that are already archived — Archive is a no-op there
      // and would just add audit-log noise.
      final st = (s['status'] ?? '').toString();
      if (st == 'archived' || st == 'closed' || st == 'deleted') continue;
      ids.add(id);
    }
    if (ids.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Archive ${ids.length} session${ids.length == 1 ? '' : 's'}?'),
        content: const Text(
          'Archived sessions move to Previous. Their transcripts stay '
          'available; you can fork from archive later to continue.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final failed = await ref
        .read(sessionsProvider.notifier)
        .bulkArchive(ids);
    if (!mounted) return;
    final n = ids.length - failed.length;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(failed.isEmpty
            ? 'Archived $n session${n == 1 ? '' : 's'}.'
            : 'Archived $n; ${failed.length} failed.'),
      ),
    );
    _exitSelect();
  }

  Future<void> _bulkDelete(List<Map<String, dynamic>> visible) async {
    final ids = <String>[];
    var hasNonArchived = false;
    for (final s in visible) {
      final id = (s['id'] ?? '').toString();
      if (id.isEmpty || !_selectedIds.contains(id)) continue;
      final st = (s['status'] ?? '').toString();
      if (st == 'archived' || st == 'closed') {
        ids.add(id);
      } else {
        hasNonArchived = true;
      }
    }
    if (ids.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(hasNonArchived
              ? 'Delete only works on archived sessions. Archive first.'
              : 'No sessions selected.'),
        ),
      );
      return;
    }
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(
          'Delete ${ids.length} session${ids.length == 1 ? '' : 's'}?'
          + (hasNonArchived ? ' (skipping unarchived)' : ''),
        ),
        content: const Text(
          'The transcripts stay in the audit log but lose their '
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
    final failed = await ref
        .read(sessionsProvider.notifier)
        .bulkDelete(ids);
    if (!mounted) return;
    final n = ids.length - failed.length;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(failed.isEmpty
            ? 'Deleted $n session${n == 1 ? '' : 's'}.'
            : 'Deleted $n; ${failed.length} failed.'),
      ),
    );
    _exitSelect();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(sessionsProvider);
    final hubState = ref.watch(hubProvider).value;
    return Scaffold(
      appBar: _selecting
          ? _buildSelectAppBar(state, hubState)
          : _buildDefaultAppBar(),
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
          if (groups.isEmpty) {
            // Auto-exit select mode if everything disappeared (e.g.,
            // bulkDelete cleared the list) — keeps the AppBar honest.
            if (_selecting) {
              WidgetsBinding.instance.addPostFrameCallback((_) {
                if (mounted) _exitSelect();
              });
            }
            return const _EmptyState();
          }
          return RefreshIndicator(
            onRefresh: () async {
              await ref.read(sessionsProvider.notifier).refresh();
              await ref.read(hubProvider.notifier).refreshAll();
            },
            child: ListView(
              padding: const EdgeInsets.symmetric(vertical: 4),
              children: [
                for (final g in groups)
                  _StewardSection(
                    group: g,
                    selecting: _selecting,
                    selectedIds: _selectedIds,
                    onLongPressTile: _enterSelectWith,
                    onToggleTile: _toggleSelect,
                  ),
                if (_selecting) const SizedBox(height: 80),
              ],
            ),
          );
        },
      ),
      bottomNavigationBar: _selecting
          ? state.maybeWhen(
              data: (s) {
                final groups = _groupByStateward(
                  hubState?.agents ?? const <Map<String, dynamic>>[],
                  s,
                );
                return _SelectionActionBar(
                  selectedCount: _selectedIds.length,
                  onArchive: () => _bulkArchive(_visibleSessions(groups)),
                  onDelete: () => _bulkDelete(_visibleSessions(groups)),
                );
              },
              orElse: () => const SizedBox.shrink(),
            )
          : null,
    );
  }

  AppBar _buildDefaultAppBar() {
    return AppBar(
      title: Text(
        'Sessions',
        style: GoogleFonts.spaceGrotesk(
            fontWeight: FontWeight.w700, fontSize: 18),
      ),
      actions: [
        IconButton(
          tooltip: 'Steward insights',
          icon: const Icon(Icons.insights_outlined),
          onPressed: () => _openStewardInsights(context, ref),
        ),
        IconButton(
          tooltip: 'Search past sessions',
          icon: const Icon(Icons.search),
          onPressed: () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => const SessionSearchScreen(),
            ),
          ),
        ),
        IconButton(
          tooltip: 'Spawn new steward',
          icon: const Icon(Icons.add),
          onPressed: () => _spawnNewSteward(context, ref),
        ),
        PopupMenuButton<String>(
          tooltip: 'More',
          onSelected: (v) {
            switch (v) {
              case 'select':
                _enterSelectEmpty();
              case 'templates':
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
              value: 'select',
              child: ListTile(
                leading: Icon(Icons.check_box_outlined),
                title: Text('Select…'),
                contentPadding: EdgeInsets.zero,
                dense: true,
              ),
            ),
            PopupMenuDivider(),
            PopupMenuItem(
              value: 'templates',
              child: Text('Templates & engines'),
            ),
            PopupMenuDivider(),
            PopupMenuItem(value: 'refresh', child: Text('Refresh')),
          ],
        ),
      ],
    );
  }

  AppBar _buildSelectAppBar(
    AsyncValue<SessionsState> state,
    HubState? hubState,
  ) {
    final groups = state.maybeWhen(
      data: (s) => _groupByStateward(
        hubState?.agents ?? const <Map<String, dynamic>>[],
        s,
      ),
      orElse: () => const <_StewardGroup>[],
    );
    final visible = _visibleSessions(groups);
    final allSelected = visible.isNotEmpty &&
        visible.every((s) =>
            _selectedIds.contains((s['id'] ?? '').toString()));
    return AppBar(
      leading: IconButton(
        tooltip: 'Cancel selection',
        icon: const Icon(Icons.close),
        onPressed: _exitSelect,
      ),
      title: Text(
        '${_selectedIds.length} selected',
        style: GoogleFonts.spaceGrotesk(
            fontWeight: FontWeight.w700, fontSize: 18),
      ),
      actions: [
        IconButton(
          tooltip: allSelected ? 'Clear selection' : 'Select all',
          icon: Icon(allSelected
              ? Icons.deselect
              : Icons.select_all),
          onPressed: () {
            if (allSelected) {
              setState(() => _selectedIds.clear());
            } else {
              _selectAll(groups);
            }
          },
        ),
      ],
    );
  }
}

class _SelectionActionBar extends StatelessWidget {
  final int selectedCount;
  final VoidCallback onArchive;
  final VoidCallback onDelete;
  const _SelectionActionBar({
    required this.selectedCount,
    required this.onArchive,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    final disabled = selectedCount == 0;
    return SafeArea(
      top: false,
      child: Material(
        elevation: 8,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: disabled ? null : onArchive,
                  icon: const Icon(Icons.archive_outlined),
                  label: const Text('Archive'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton.icon(
                  onPressed: disabled ? null : onDelete,
                  style: FilledButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.error,
                  ),
                  icon: const Icon(Icons.delete_outline),
                  label: const Text('Delete'),
                ),
              ),
            ],
          ),
        ),
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
    // Include the team-scoped general steward (`@steward`, which
    // isStewardHandle deliberately excludes for spawn / collision
    // semantics) so its sessions get a proper group instead of
    // falling through to "Detached".
    if (!isStewardHandle(handle) && !isGeneralStewardHandle(handle)) continue;
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
      if ((st == 'active' || st == 'paused' || st == 'open' || st == 'interrupted') && current == null) {
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
  // silently swallowed. The engine they used to talk to is gone, so
  // we override the rendered status to 'paused' here (regardless of
  // what the hub reports) — a hub running old code can leave
  // status='active' when an agent died via a path that didn't auto-
  // pause its sessions, and showing those rows with a green active
  // pill misleads the user into thinking the chat is still live. The
  // backing row's status isn't mutated; only the copy passed to the
  // tile is. Migration 0032 heals the data on the hub itself; this
  // keeps the UI honest until the deployed hub picks it up.
  // ADR-025 W8: worker sessions (non-steward agent bound to a
  // project) live on the project detail Agents tab, not in the
  // global Sessions list — keeps this screen focused on the
  // operator's steward conversations rather than every per-worker
  // micro-chat. Build a quick "skip" set: agents whose kind is NOT
  // a steward variant AND whose project_id is non-empty.
  final workerSessionAgentIDs = <String>{};
  for (final a in agents) {
    final kind = (a['kind'] ?? '').toString();
    final projectID = (a['project_id'] ?? '').toString();
    if (projectID.isEmpty) continue;
    if (kind.startsWith('steward.') || kind == 'steward.v1') continue;
    final aid = (a['id'] ?? '').toString();
    if (aid.isNotEmpty) workerSessionAgentIDs.add(aid);
  }
  final orphanSessions = <Map<String, dynamic>>[];
  for (final s in [...sessions.active, ...sessions.previous]) {
    final aid = (s['current_agent_id'] ?? '').toString();
    if (workerSessionAgentIDs.contains(aid)) continue;
    if (aid.isEmpty || !liveStewardIds.contains(aid)) {
      final asPausedIfActive = {...s};
      final st = (asPausedIfActive['status'] ?? '').toString();
      if (st == 'active' || st == 'open') {
        asPausedIfActive['status'] = 'paused';
      }
      orphanSessions.add(asPausedIfActive);
    }
  }
  if (orphanSessions.isNotEmpty) {
    // Detached sessions have no live engine, so none of them belongs
    // in the "current" slot — bucket every row into Previous so the
    // UX matches the underlying reality (no Stop / no live transcript;
    // only Resume / Fork / Archive make sense).
    groups.add(_StewardGroup(
      // Sessions whose original steward agent was archived /
      // terminated outside of this UI, or never resolved (cache lag).
      // The bucket is informational — these sessions are still
      // openable for reading history; forking them spawns a fresh
      // steward into a continuation session via the unattached-fork
      // path.
      agent: const {
        'handle': 'Detached sessions',
        'kind': '',
        'status': '',
      },
      current: null,
      previous: orphanSessions,
    ));
  }
  return groups;
}

/// Open a fresh session against an existing live steward that has no
/// active one. Different from Reset (which closes a current session
/// before opening) and from Spawn new steward (which creates a new
/// agent). Used by the inline "Start session" button on the steward
/// section header when the steward is alive but session-less.
///
/// Per ADR-009 D7 (Phase 2), prompts the user for scope when they
/// start from this no-entry-point path — General by default, plus
/// one option per project. Implicit-from-entry-point still covers
/// Me-FAB and project-page paths where scope is unambiguous.
Future<void> _startSession(
  BuildContext context,
  WidgetRef ref,
  String agentId,
  String handle,
) async {
  final picked = await _pickScopeSheet(context, ref);
  if (picked == null || !context.mounted) return;
  final client = ref.read(hubProvider.notifier).client;
  if (client == null) return;
  try {
    await client.openSession(
      agentId: agentId,
      scopeKind: picked.kind,
      scopeId: picked.id,
    );
    await ref.read(sessionsProvider.notifier).refresh();
  } catch (e) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Start session for $handle failed: $e')),
    );
  }
}

/// Scope choice for the open-from-list path. `kind` is the hub's
/// `scope_kind` value (`team`, `project`, `attention`); `id` is the
/// `scope_id` (empty for team).
class _ScopePick {
  final String kind;
  final String id;
  final String label;
  const _ScopePick(this.kind, this.id, this.label);
}

Future<_ScopePick?> _pickScopeSheet(
    BuildContext context, WidgetRef ref) async {
  final hub = ref.read(hubProvider).value;
  final projects = hub?.projects ?? const <Map<String, dynamic>>[];
  final options = <_ScopePick>[
    const _ScopePick('team', '', 'General'),
    for (final p in projects)
      _ScopePick(
        'project',
        (p['id'] ?? '').toString(),
        'Project: ${(p['name'] ?? p['title'] ?? '').toString()}',
      ),
  ];
  return showModalBottomSheet<_ScopePick>(
    context: context,
    showDragHandle: true,
    // Tall portfolios easily exceed the default ~half-screen cap and the
    // original Column(mainAxisSize.min) had no scroll wrapper, leaving
    // trailing options clipped off the bottom of the sheet.
    isScrollControlled: true,
    builder: (ctx) {
      final maxH = MediaQuery.of(ctx).size.height * 0.7;
      return SafeArea(
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: maxH),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 4, 20, 8),
                child: Text(
                  'Scope for new session',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              Flexible(
                child: ListView.builder(
                  shrinkWrap: true,
                  itemCount: options.length,
                  itemBuilder: (_, i) {
                    final o = options[i];
                    return ListTile(
                      leading: Icon(
                        o.kind == 'project'
                            ? Icons.folder_outlined
                            : Icons.forum_outlined,
                      ),
                      title: Text(o.label),
                      onTap: () => Navigator.of(ctx).pop(o),
                    );
                  },
                ),
              ),
            ],
          ),
        ),
      );
    },
  );
}

Future<void> _spawnNewSteward(BuildContext context, WidgetRef ref) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;
  await showSpawnStewardSheet(context, hosts: hub.hosts);
  if (!context.mounted) return;
  await ref.read(hubProvider.notifier).refreshAll();
  await ref.read(sessionsProvider.notifier).refresh();
}

/// Opens the team-stewards Insights view — aggregate spend / latency
/// / errors across every live steward (general + domain) plus a
/// `by_agent` breakdown for per-steward drill-in. The hub aggregator
/// receives `team_id=X&kind=steward`; mobile materializes that as
/// [InsightsScope.teamStewards].
void _openStewardInsights(BuildContext context, WidgetRef ref) {
  final hub = ref.read(hubProvider).value;
  final teamId = hub?.config?.teamId ?? '';
  if (teamId.isEmpty) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Configure a team first')),
    );
    return;
  }
  Navigator.of(context).push(MaterialPageRoute(
    builder: (_) => InsightsScreen(
      scope: InsightsScope.teamStewards(teamId),
    ),
  ));
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
        await client.archiveSession((s['id'] ?? '').toString());
      } catch (_) {
        // Non-fatal — already archived by another path is fine.
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
///   - Per-steward kebab: Reset (new conversation), Replace, Stop session, Rename
///   - Current session inline as a tile (open or interrupted)
///   - Collapsible "previous (N)" subsection of closed sessions
class _StewardSection extends ConsumerStatefulWidget {
  final _StewardGroup group;
  // Multi-select wiring (sessions list batch-ops). Threaded through
  // from the screen down to each _SessionTile so the same widget tree
  // renders both modes — null in single-select callers means tiles
  // behave like before.
  final bool selecting;
  final Set<String> selectedIds;
  final void Function(String id) onLongPressTile;
  final void Function(String id) onToggleTile;
  const _StewardSection({
    required this.group,
    this.selecting = false,
    this.selectedIds = const <String>{},
    required this.onLongPressTile,
    required this.onToggleTile,
  });

  @override
  ConsumerState<_StewardSection> createState() => _StewardSectionState();
}

class _StewardSectionState extends ConsumerState<_StewardSection> {
  // Default-collapse Previous when the steward has a current session
  // (the row above it is what users want to see); auto-expand when
  // there's no current — typical of the Detached group, where the
  // previous list IS the content.
  late bool _showPrevious = widget.group.current == null;

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

  Future<void> _stopSession() async {
    final id = group.agentId;
    if (id.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Stop session?'),
        content: Text(
          'Kills ${group.handle}\'s agent process. The session pauses '
          'and stays in Previous; you can Resume it later or Replace '
          'this steward with a fresh one.',
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
            child: const Text('Stop'),
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
        SnackBar(content: Text('Stop failed: $e')),
      );
    }
  }

  static Future<String?> _promptForHandle(
    BuildContext context,
    String current,
  ) async {
    // Show the bare name (no `-steward` suffix) so the rename
    // dialog matches the spawn-steward sheet's UX. The app
    // re-attaches the suffix on save via normalizeStewardHandle.
    final ctrl = TextEditingController(text: stewardLabel(current));
    try {
      return await showDialog<String?>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Rename steward'),
          content: TextField(
            controller: ctrl,
            autofocus: true,
            decoration: const InputDecoration(
              labelText: 'Name',
              hintText: 'steward, research, infra-east, …',
            ),
            onSubmitted: (v) =>
                Navigator.pop(ctx, normalizeStewardHandle(v.trim())),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, null),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                final v = normalizeStewardHandle(ctrl.text.trim());
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
    // Where this steward is running. Resolved from the cached hosts
    // list so the operator sees the friendly name, not a UUID.
    // Hidden when the agent has no host_id (worker bootstrapping)
    // or the host record isn't loaded yet.
    final hubHosts = ref.watch(hubProvider).value?.hosts ?? const [];
    final hostName = hostLabel(
      hubHosts,
      (group.agent['host_id'] ?? '').toString(),
    );

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
                                if (hostName != null) '@$hostName',
                              ].join(' · '),
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: 10,
                                color: muted,
                              ),
                            )
                          else
                            Text(
                              'Original steward gone — open to read, '
                              'fork to continue with a fresh one',
                              style: GoogleFonts.spaceGrotesk(
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
                            case 'agent_config':
                              showAgentConfigSheet(context,
                                  agentId: group.agentId);
                            case 'reset':
                              _resetStewardConversation(context, ref,
                                  group.agentId, group.handle);
                            case 'replace':
                              confirmAndRecreateSteward(
                                  context, ref, group.agentId);
                            case 'stop':
                              _stopSession();
                            case 'rename':
                              _rename();
                          }
                        },
                        itemBuilder: (_) => const [
                          PopupMenuItem(
                            value: 'agent_config',
                            child: Text('View agent config'),
                          ),
                          PopupMenuDivider(),
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
                            value: 'stop',
                            child: Text('Stop session'),
                          ),
                        ],
                      ),
                  ],
                ),
              ),
            ),
            // ── Current session ───────────────────────────────────
            if (group.current != null)
              _SessionTile(
                session: group.current!,
                selecting: widget.selecting,
                selected: widget.selectedIds
                    .contains((group.current!['id'] ?? '').toString()),
                onLongPress: widget.onLongPressTile,
                onToggleSelect: widget.onToggleTile,
              )
            else if (!isOrphan)
              // Steward is alive but has no open/interrupted session.
              // Possible causes: pre-v1.0.290 steward spawned without
              // auto_open_session, or a Reset whose openSession failed
              // silently. Either way the user wants a single tap to
              // reach a chat — provide it inline rather than asking
              // them to dig through a kebab.
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        'No active session for this steward.',
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10, color: muted),
                      ),
                    ),
                    FilledButton.tonal(
                      onPressed: () => _startSession(context, ref,
                          group.agentId, group.handle),
                      style: FilledButton.styleFrom(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 10, vertical: 4),
                        minimumSize: const Size(0, 28),
                        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      ),
                      child: const Text('Start session'),
                    ),
                  ],
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
              ..._buildScopeGroupedPrevious(
                context,
                ref,
                group.previous,
                muted,
                selecting: widget.selecting,
                selectedIds: widget.selectedIds,
                onLongPressTile: widget.onLongPressTile,
                onToggleTile: widget.onToggleTile,
              ),
            const SizedBox(height: 4),
          ],
        ),
      ),
    );
  }
}

/// Groups a list of previous (archived/paused) sessions by scope and
/// emits section headers between groups. Per ADR-009 Phase 2 plan
/// item 4: General / Project: <name> / Approving / Other. Within
/// each group, sessions stay in input order (caller pre-sorts by
/// last_active_at desc).
List<Widget> _buildScopeGroupedPrevious(
  BuildContext context,
  WidgetRef ref,
  List<Map<String, dynamic>> previous,
  Color muted, {
  bool selecting = false,
  Set<String> selectedIds = const <String>{},
  void Function(String id)? onLongPressTile,
  void Function(String id)? onToggleTile,
}) {
  if (previous.isEmpty) return const [];
  final hub = ref.watch(hubProvider).value;
  // Bucket sessions into scope groups, preserving order.
  final buckets = <String, List<Map<String, dynamic>>>{};
  final order = <String>[];
  for (final s in previous) {
    final kind = (s['scope_kind'] ?? '').toString();
    final id = (s['scope_id'] ?? '').toString();
    final key = '$kind|$id';
    if (!buckets.containsKey(key)) {
      buckets[key] = [];
      order.add(key);
    }
    buckets[key]!.add(s);
  }
  String labelFor(String kind, String id) {
    switch (kind) {
      case 'project':
        if (hub != null) {
          for (final p in hub.projects) {
            if ((p['id'] ?? '').toString() == id) {
              final name = (p['name'] ?? p['title'] ?? '').toString();
              if (name.isNotEmpty) return 'Project: $name';
            }
          }
        }
        return 'Project';
      case 'attention':
        return 'Approving';
      case 'team':
      case '':
        return 'General';
      default:
        return kind;
    }
  }
  // Move the General bucket to the top so the most-common case
  // doesn't sink under project-specific groups when there are many
  // projects.
  order.sort((a, b) {
    final aGen = a.startsWith('team|') || a.startsWith('|');
    final bGen = b.startsWith('team|') || b.startsWith('|');
    if (aGen != bGen) return aGen ? -1 : 1;
    return 0;
  });
  final out = <Widget>[];
  for (final key in order) {
    final parts = key.split('|');
    final kind = parts.first;
    final id = parts.sublist(1).join('|');
    final label = labelFor(kind, id);
    final count = buckets[key]!.length;
    out.add(Padding(
      padding: const EdgeInsets.fromLTRB(20, 6, 20, 2),
      child: Row(
        children: [
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: muted,
              letterSpacing: 0.6,
            ),
          ),
          const SizedBox(width: 6),
          Text(
            '· $count',
            style:
                GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
          ),
        ],
      ),
    ));
    for (final s in buckets[key]!) {
      out.add(_SessionTile(
        session: s,
        selecting: selecting,
        selected: selectedIds.contains((s['id'] ?? '').toString()),
        onLongPress: onLongPressTile,
        onToggleSelect: onToggleTile,
      ));
    }
  }
  return out;
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
  // Multi-select wiring. When [selecting] is true, the tile renders a
  // checkbox in the leading position and intercepts taps to call
  // [onToggleSelect] instead of opening the chat. Long-press always
  // routes to [onLongPress] so users can enter select mode without
  // opening a kebab menu first.
  final bool selecting;
  final bool selected;
  final void Function(String id)? onLongPress;
  final void Function(String id)? onToggleSelect;
  const _SessionTile({
    required this.session,
    this.selecting = false,
    this.selected = false,
    this.onLongPress,
    this.onToggleSelect,
  });

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

  /// Fork an archived session into a new active one (ADR-009 D4).
  /// Pushes the new session's chat on success.
  Future<void> _fork(BuildContext context) async {
    final id = (session['id'] ?? '').toString();
    if (id.isEmpty) return;
    try {
      final out = await ref.read(sessionsProvider.notifier).fork(id);
      if (out == null || !mounted) return;
      final newSessionId = (out['session_id'] ?? '').toString();
      final newAgentId = (out['agent_id'] ?? '').toString();
      final title = (out['title'] ?? '').toString();
      if (newSessionId.isEmpty || newAgentId.isEmpty) return;
      await Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => SessionChatScreen(
            sessionId: newSessionId,
            agentId: newAgentId,
            title: title.isEmpty ? 'Forked session' : title,
          ),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Fork failed: $e')),
      );
    }
  }

  /// Stop this session's engine from the row (mirrors the chat
  /// AppBar's Stop). Kills the attached agent → server auto-pauses
  /// the session. Used for active sessions where the user wants to
  /// detach the engine without first opening the chat.
  Future<void> _stopFromRow(BuildContext context) async {
    final agentId = (session['current_agent_id'] ?? '').toString();
    if (agentId.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Stop session?'),
        content: const Text(
          "Kills the steward's agent process. The session pauses and "
          "stays in Previous; you can Resume it later or Fork from "
          "archive once you've moved on.",
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
            child: const Text('Stop'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.terminateAgent(agentId);
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Stop failed: $e')),
      );
    }
  }

  /// Archive a paused session — moves it to Previous so the row no
  /// longer shows a Resume button. The transcript stays intact and is
  /// reachable via Fork. Used when the user has moved on from this
  /// conversation and doesn't want it cluttering the active list.
  Future<void> _archiveFromRow(BuildContext context) async {
    final id = (session['id'] ?? '').toString();
    if (id.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Archive session?'),
        content: const Text(
          'Marks the session as done. The transcript stays available '
          'under Previous, and you can fork it later to continue.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await ref.read(sessionsProvider.notifier).archive(id);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Archive failed: $e')),
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
      'active' => DesignColors.success,
      'paused' => DesignColors.warning,
      'archived' => muted,
      // Tolerate legacy strings during the brief rollout window
      // (ADR-009): a not-yet-migrated hub may still emit these.
      'open' => DesignColors.success,
      'interrupted' => DesignColors.warning,
      'closed' => muted,
      _ => muted,
    };

    final displayTitle = title.isEmpty ? '(untitled session)' : title;

    // Selecting? Render a Checkbox in the leading slot, hide the
    // per-row trailing actions (resume/menu) since they don't apply
    // mid-batch, and route taps + long-press to the selection
    // callbacks. The chat-open onTap is replaced wholesale so users
    // can't accidentally navigate away with a selection in progress.
    final selected = widget.selected;
    final inSelect = widget.selecting;
    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
      leading: inSelect
          ? Checkbox(
              value: selected,
              onChanged: (_) => widget.onToggleSelect?.call(id),
            )
          : CircleAvatar(
              radius: 14,
              backgroundColor: statusColor.withValues(alpha: 0.18),
              child: Icon(
                (status == 'paused' || status == 'interrupted')
                    ? Icons.pause_circle_outline
                    : ((status == 'archived' || status == 'closed')
                        ? Icons.history
                        : Icons.forum_outlined),
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
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: inSelect
          ? null
          : _trailing(context, status, lastActive, muted),
      selected: inSelect && selected,
      onLongPress: inSelect
          ? null
          : (id.isEmpty ? null : () => widget.onLongPress?.call(id)),
      onTap: inSelect
          ? (id.isEmpty ? null : () => widget.onToggleSelect?.call(id))
          : (agentId.isEmpty
              ? null
              : () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => SessionChatScreen(
                        sessionId: id,
                        agentId: agentId,
                        title: displayTitle,
                      ),
                    ),
                  )),
    );
  }

  Widget? _trailing(
    BuildContext context,
    String status,
    String lastActive,
    Color muted,
  ) {
    // Per-row session menu. Per-status terminal action:
    //   active → Stop session (kills agent → auto-pauses)
    //   paused → Archive (gives up resume; transcript stays, Fork still works)
    //   archived → Fork (continue with fresh agent) + Delete
    // Stop used to live only on the chat AppBar, but a list row needs
    // its own escape hatch — the user shouldn't have to open a session
    // just to kill it. The multi-steward invariant ("every live
    // steward has a session") is preserved because Stop terminates
    // the agent first; the session pauses but the steward dies with
    // it, so no agent-without-session intermediate.
    final isActive = status == 'active' || status == 'open';
    final isPaused = status == 'paused' || status == 'interrupted';
    final isArchived = status == 'archived' || status == 'closed';
    final hasAgent = (session['current_agent_id'] ?? '').toString().isNotEmpty;
    final menu = PopupMenuButton<String>(
      tooltip: 'Session actions',
      icon: Icon(Icons.more_vert, size: 18, color: muted),
      onSelected: (v) {
        if (v == 'rename') _rename(context);
        if (v == 'delete') _confirmDelete(context);
        if (v == 'fork') _fork(context);
        if (v == 'stop') _stopFromRow(context);
        if (v == 'archive') _archiveFromRow(context);
      },
      itemBuilder: (_) => [
        const PopupMenuItem(value: 'rename', child: Text('Rename')),
        if (isActive && hasAgent)
          const PopupMenuItem(
            value: 'stop',
            child: Text('Stop session'),
          ),
        if (isActive || isPaused)
          const PopupMenuItem(
            value: 'archive',
            child: Text('Archive'),
          ),
        if (isArchived) ...[
          const PopupMenuItem(
            value: 'fork',
            child: Text('Fork from archive'),
          ),
          const PopupMenuItem(value: 'delete', child: Text('Delete')),
        ],
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
        if (status == 'paused' || status == 'interrupted')
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
  /// When set, the chat opens scrolled to (and briefly highlights) the
  /// event whose seq matches. Used by "Open in chat" from the approval
  /// detail screen so the principal lands at the agent's turn that
  /// raised the request, not at the generic tail. Null = default
  /// behavior (auto-scroll to tail on cold open).
  final int? initialSeq;
  const SessionChatScreen({
    super.key,
    required this.sessionId,
    required this.agentId,
    required this.title,
    this.initialSeq,
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
  // Latest mode + model picker payload reported up by AgentFeed (ADR-
  // 021 W2.5). Hosting the picker in the AppBar keeps it one tap away
  // without burning a chip strip above the transcript on every turn.
  // Null when no agent has advertised either capability — the AppBar
  // icon hides in that case.
  ModeModelPickerData? _modeModel;

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

  // Friendly host label for the agent's host_id. Resolved against the
  // cached hosts list so the AppBar shows the operator's name for the
  // box (e.g. "research-pi") rather than a UUID. Returns null when the
  // agent or host record isn't loaded yet — the chip then hides.
  String? _hostName() {
    final hub = ref.read(hubProvider).value;
    if (hub == null) return null;
    String? hostId;
    for (final a in hub.agents) {
      if ((a['id'] ?? '').toString() != widget.agentId) continue;
      hostId = (a['host_id'] ?? '').toString();
      break;
    }
    return hostLabel(hub.hosts, hostId);
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

  /// Stops the session by killing the attached engine. Destructive —
  /// the agent process is killed, the session auto-flips to paused
  /// (and stays that way until the user explicitly resumes or the
  /// steward is replaced).
  ///
  /// "Close session" used to live next to this as a separate action,
  /// but it violated the multi-steward design invariant ("every live
  /// steward has a session"): archiving the only active session
  /// would leave a steward agent without a current session, an
  /// agent-without-session intermediate that the design forbids.
  /// The two clean options are now: Reset (new conversation), which
  /// preserves the agent and rotates the transcript via the per-
  /// steward kebab on the Sessions page; or Stop session (this
  /// action), which detaches the engine. Per ADR-009 D6 the action
  /// is gated on session.state == active so it doesn't render in
  /// paused/archived sessions where there is no engine to stop.
  Future<void> _stopSession() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Stop session?'),
        content: const Text(
          "Kills the steward's agent process. The session will pause; "
          'you can Resume it later or replace the steward with a fresh '
          'one (potentially a different engine/model).\n\n'
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
            child: const Text('Stop'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.terminateAgent(widget.agentId);
      // Refresh state so the Sessions list reflects the pause and
      // the home/projects screens drop the dead steward chip.
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
      if (!mounted) return;
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Stop failed: $e')),
      );
    }
  }

  /// Forks the archived session (ADR-009 D4): server creates a new
  /// session with the same scope. When a live steward exists, the
  /// fork is attached to it (status=active) and we navigate into the
  /// chat. When none exists, the fork comes back unattached
  /// (agent_id empty, status=paused) — we open the spawn-steward
  /// sheet so the user can drive a steward into the new session.
  /// Either way, the user ends up with a working continuation
  /// without seeing a misleading "no live steward" error.
  Future<void> _forkSession() async {
    try {
      final out = await ref
          .read(sessionsProvider.notifier)
          .fork(widget.sessionId);
      if (out == null || !mounted) return;
      final newSessionId = (out['session_id'] ?? '').toString();
      final newAgentId = (out['agent_id'] ?? '').toString();
      final title = (out['title'] ?? '').toString();
      if (newSessionId.isEmpty) return;
      if (newAgentId.isEmpty) {
        // Unattached fork: route through the spawn-steward sheet
        // bound to this new session id. The sheet's session-swap
        // path (sessionId != null) atomically spawns the steward
        // and points the session at it; on close we refresh and
        // navigate into the chat with the freshly-resolved agent.
        final hub = ref.read(hubProvider).value;
        if (hub == null) return;
        await showSpawnStewardSheet(
          context,
          hosts: hub.hosts,
          sessionId: newSessionId,
        );
        if (!mounted) return;
        await ref.read(hubProvider.notifier).refreshAll();
        await ref.read(sessionsProvider.notifier).refresh();
        if (!mounted) return;
        // Resolve the agent the spawn sheet attached. If the user
        // dismissed the sheet without spawning, current_agent_id
        // stays NULL — bail out silently; the paused fork is
        // visible on the Sessions list and they can finish the
        // attach later.
        final fresh = ref.read(sessionsProvider).value;
        String? attachedAgent;
        if (fresh != null) {
          for (final s in [...fresh.active, ...fresh.previous]) {
            if ((s['id'] ?? '').toString() != newSessionId) continue;
            final a = (s['current_agent_id'] ?? '').toString();
            if (a.isNotEmpty) attachedAgent = a;
            break;
          }
        }
        if (attachedAgent == null) return;
        await Navigator.of(context).pushReplacement(
          MaterialPageRoute(
            builder: (_) => SessionChatScreen(
              sessionId: newSessionId,
              agentId: attachedAgent!,
              title: title.isEmpty ? 'Forked session' : title,
            ),
          ),
        );
        return;
      }
      // Replace the current archived chat with the fresh fork; users
      // who want to refer back can navigate via the sessions list.
      await Navigator.of(context).pushReplacement(
        MaterialPageRoute(
          builder: (_) => SessionChatScreen(
            sessionId: newSessionId,
            agentId: newAgentId,
            title: title.isEmpty ? 'Forked session' : title,
          ),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Fork failed: $e')),
      );
    }
  }

  /// Returns a chip showing the session's scope (per ADR-009 D7) so
  /// the user can tell at a glance whether this is a general / team
  /// session or scoped to a project / attention item. Display-only;
  /// re-scoping is post-MVP. Returns null when scope can't be
  /// resolved (no session row yet, or unknown scope_kind).
  Widget? _buildScopeChip(
      BuildContext context, WidgetRef ref, Map<String, dynamic>? session) {
    if (session == null) return null;
    final kind = (session['scope_kind'] ?? '').toString();
    final id = (session['scope_id'] ?? '').toString();
    String label;
    IconData icon;
    switch (kind) {
      case 'project':
        final hub = ref.read(hubProvider).value;
        String? name;
        if (hub != null) {
          for (final p in hub.projects) {
            if ((p['id'] ?? '').toString() == id) {
              name = (p['name'] ?? p['title'] ?? '').toString();
              break;
            }
          }
        }
        label = (name != null && name.isNotEmpty)
            ? 'Project: $name'
            : 'Project';
        icon = Icons.folder_outlined;
      case 'attention':
        label = 'Approving';
        icon = Icons.gavel_outlined;
      case 'team':
      case '':
        label = 'General';
        icon = Icons.forum_outlined;
      default:
        label = kind;
        icon = Icons.label_outline;
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        decoration: BoxDecoration(
          color: muted.withValues(alpha: 0.10),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 12, color: muted),
            const SizedBox(width: 4),
            Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: muted,
                fontWeight: FontWeight.w500,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Session state pair (active|paused|archived) drives affordance
    // gating per ADR-009 D6: "Stop session" is meaningful only when
    // there's an engine attached to stop.
    final sessions = ref.watch(sessionsProvider).value;
    Map<String, dynamic>? sessionRow;
    if (sessions != null) {
      for (final s in [...sessions.active, ...sessions.previous]) {
        if ((s['id'] ?? '').toString() == widget.sessionId) {
          sessionRow = s;
          break;
        }
      }
    }
    final sessionStatus = (sessionRow?['status'] ?? '').toString();
    // Mirror the sessions-list defensive override: if the session
    // claims to be active but the attached agent is gone (terminal
    // status or missing from hub.agents), there's nothing for Stop to
    // kill. Hide it so the chat AppBar matches reality.
    final hub = ref.watch(hubProvider).value;
    bool agentLive = false;
    if (hub != null) {
      for (final a in hub.agents) {
        if ((a['id'] ?? '').toString() != widget.agentId) continue;
        final st = (a['status'] ?? '').toString();
        agentLive = st == 'running' || st == 'pending' || st == 'paused';
        break;
      }
    }
    final canStop =
        agentLive && (sessionStatus == 'active' || sessionStatus == 'open');
    final canFork =
        sessionStatus == 'archived' || sessionStatus == 'closed';
    final scopeChip = _buildScopeChip(context, ref, sessionRow);

    final hostName = _hostName();
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              _title,
              style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            // Host the steward's process is running on. Surfaces the
            // box+login-user the agent is bound to so users running
            // multiple host-runners can tell at a glance which one is
            // doing the work. Silent when no host record is loaded.
            if (hostName != null)
              Text(
                '@$hostName',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: Theme.of(context).brightness == Brightness.dark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
          ],
        ),
        actions: [
          if (scopeChip != null) scopeChip,
          if (_sessionInit != null)
            SessionInitChip(
              payload: _sessionInit!,
              agentKind: _agentKind(),
              // Folds the mode/model picker into the engine chip's
              // details sheet so the AppBar carries one entry instead
              // of two — and the sheet itself stays scrollable for
              // long model lists. Falls back to the standalone tune
              // icon below when there's no session.init payload yet.
              modeModel:
                  (_modeModel != null && _modeModel!.hasAny) ? _modeModel : null,
            ),
          if (_sessionInit == null && _modeModel != null && _modeModel!.hasAny)
            IconButton(
              tooltip: () {
                final parts = <String>[];
                final mode = _modeModel!.currentModeLabel;
                final model = _modeModel!.currentModelLabel;
                if (mode != null) parts.add('mode: $mode');
                if (model != null) parts.add('model: $model');
                return parts.isEmpty
                    ? 'Mode & model'
                    : parts.join(' · ');
              }(),
              icon: const Icon(Icons.tune),
              onPressed: () =>
                  showModeModelPickerSheet(context, _modeModel!),
            ),
          // Single overflow that carries everything except the
          // engine-state chip and scope chip. Rename moved off the bar
          // (was its own icon) to make room for the agent-config entry
          // without the AppBar growing wider. Reset/Replace live on the
          // steward-row kebab, not here, because they affect the
          // steward identity (not just this session).
          PopupMenuButton<String>(
            tooltip: 'Session actions',
            onSelected: (v) {
              switch (v) {
                case 'agent_config':
                  final aid = (sessionRow?['current_agent_id'] ?? '')
                      .toString();
                  if (aid.isNotEmpty) {
                    showAgentConfigSheet(context, agentId: aid);
                  }
                case 'rename':
                  _rename();
                case 'stop':
                  _stopSession();
                case 'fork':
                  _forkSession();
              }
            },
            itemBuilder: (_) => [
              if ((sessionRow?['current_agent_id'] ?? '')
                  .toString()
                  .isNotEmpty)
                const PopupMenuItem(
                  value: 'agent_config',
                  child: ListTile(
                    leading: Icon(Icons.account_tree_outlined),
                    title: Text('View agent config'),
                    subtitle: Text(
                        'Kind, role, mode, spawn spec'),
                    contentPadding: EdgeInsets.zero,
                    dense: true,
                  ),
                ),
              const PopupMenuItem(
                value: 'rename',
                child: ListTile(
                  leading: Icon(Icons.edit_outlined),
                  title: Text('Rename session'),
                  contentPadding: EdgeInsets.zero,
                  dense: true,
                ),
              ),
              if (canStop)
                PopupMenuItem(
                  value: 'stop',
                  child: ListTile(
                    leading: Icon(Icons.power_settings_new,
                        color: Theme.of(context).colorScheme.error),
                    title: Text('Stop session',
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                    subtitle: const Text(
                        'Kills the agent process; session pauses'),
                    contentPadding: EdgeInsets.zero,
                    dense: true,
                  ),
                ),
              if (canFork)
                PopupMenuItem(
                  value: 'fork',
                  child: ListTile(
                    leading: Icon(Icons.fork_right,
                        color: Theme.of(context).colorScheme.primary),
                    title: const Text('Fork from archive'),
                    subtitle: const Text(
                        'New active session, same scope, fresh transcript'),
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
        initialSeq: widget.initialSeq,
        onSessionInit: (p) => setState(() => _sessionInit = p),
        onModeModelChanged: (d) => setState(() => _modeModel = d),
      ),
    );
  }
}
