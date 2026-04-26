import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

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
      body: AgentFeed(agentId: agentId),
    );
  }
}
