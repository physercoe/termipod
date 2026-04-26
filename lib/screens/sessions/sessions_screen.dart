import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/agent_feed.dart';

/// Sessions list, grouped into Active (open + interrupted) and
/// Previous (closed). Mirrors the home screen of comparable mobile
/// agent clients (Happy "Active sessions / Previous sessions",
/// CCUI session sidebar) — sessions are the navigational primitive,
/// not buried under an agent.
///
/// Tap a session → push [SessionChatScreen], which scopes
/// [AgentFeed] to that session's current_agent_id. When the resume
/// wedge (W2-S3) lands, interrupted sessions get a Resume affordance
/// here without changing the screen shape.
class SessionsScreen extends ConsumerWidget {
  const SessionsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(sessionsProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Sessions',
          style:
              GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700, fontSize: 18),
        ),
        actions: [
          IconButton(
            tooltip: 'New session',
            icon: const Icon(Icons.add),
            onPressed: () => _newSession(context, ref),
          ),
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: () => ref.read(sessionsProvider.notifier).refresh(),
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
          if (s.isEmpty) return const _EmptyState();
          return RefreshIndicator(
            onRefresh: () =>
                ref.read(sessionsProvider.notifier).refresh(),
            child: ListView(
              padding: const EdgeInsets.symmetric(vertical: 8),
              children: [
                if (s.active.isNotEmpty) ...[
                  const _SectionLabel(text: 'ACTIVE'),
                  for (final ses in s.active) _SessionTile(session: ses),
                ],
                if (s.previous.isNotEmpty) ...[
                  const _SectionLabel(text: 'PREVIOUS'),
                  for (final ses in s.previous) _SessionTile(session: ses),
                ],
              ],
            ),
          );
        },
      ),
    );
  }
}

/// Open a fresh session attached to the live steward (if any),
/// after closing the prior open session for that steward.
///
/// "New session" semantics for V1 of the workband: one active
/// session per steward agent. Today's steward is M2 single-process,
/// so its conversation context is process-bound — a "new session"
/// that just renames the bookmark wouldn't actually feel fresh
/// to the user. Closing the prior session signals "this is a
/// restart of the conversation, the old transcript is final and
/// goes to Previous". Future wedges can refine this if/when we
/// support multiple parallel sessions on the same agent.
Future<void> _newSession(BuildContext context, WidgetRef ref) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;
  final client = ref.read(hubProvider.notifier).client;
  if (client == null) return;

  // Find live steward.
  Map<String, dynamic>? steward;
  for (final a in hub.agents) {
    if ((a['handle'] ?? '').toString() != 'steward') continue;
    final status = (a['status'] ?? '').toString();
    if (status == 'running' || status == 'pending' || status == 'paused') {
      steward = a;
      break;
    }
  }
  if (steward == null) {
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'No live steward — start one from the project page first.',
          ),
        ),
      );
    }
    return;
  }
  final stewardId = (steward['id'] ?? '').toString();

  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Start a new session?'),
      content: const Text(
        'Closes the current session for this steward and opens a fresh '
        'one. The prior transcript stays available under Previous.',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(ctx, false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          child: const Text('New session'),
        ),
      ],
    ),
  );
  if (ok != true) return;

  // Close any active session for this steward, carrying its
  // worktree_path + spawn_spec_yaml forward so the new session
  // inherits the resume context.
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
      SnackBar(content: Text('New session failed: $e')),
    );
    return;
  }
  await ref.read(sessionsProvider.notifier).refresh();
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

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel({required this.text});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
      child: Text(
        text,
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

class _SessionTile extends ConsumerStatefulWidget {
  final Map<String, dynamic> session;
  const _SessionTile({required this.session});

  @override
  ConsumerState<_SessionTile> createState() => _SessionTileState();
}

class _SessionTileState extends ConsumerState<_SessionTile> {
  bool _resuming = false;

  Map<String, dynamic> get session => widget.session;

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
      trailing: status == 'interrupted'
          ? FilledButton.tonal(
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
            )
          : status == 'closed'
              ? Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (lastActive.isNotEmpty)
                      Text(
                        _shortTimestamp(lastActive),
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10, color: muted),
                      ),
                    PopupMenuButton<String>(
                      tooltip: 'Session actions',
                      icon:
                          Icon(Icons.more_vert, size: 18, color: muted),
                      onSelected: (v) {
                        if (v == 'delete') _confirmDelete(context);
                      },
                      itemBuilder: (_) => const [
                        PopupMenuItem(
                          value: 'delete',
                          child: Text('Delete'),
                        ),
                      ],
                    ),
                  ],
                )
              : (lastActive.isEmpty
                  ? null
                  : Text(
                      _shortTimestamp(lastActive),
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 10, color: muted),
                    )),
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

/// Per-session chat view. For W2-S2 it's a thin wrapper over
/// [AgentFeed] scoped to the session's current_agent_id, with the
/// session title in the AppBar. Once W2-S3 lands and resume swaps
/// agent_ids, the AgentFeed query becomes session-scoped instead so
/// the transcript carries across the swap; for now agent-scoped is
/// indistinguishable from session-scoped because each session has
/// exactly one current agent.
class SessionChatScreen extends StatelessWidget {
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
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: AgentFeed(agentId: agentId, sessionId: sessionId),
    );
  }
}
