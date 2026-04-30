import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/sessions/sessions_screen.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';

/// Persistent home-tab card for the team-scoped general steward
/// (`steward.general.v1`, handle `@steward`). Surfaces the always-on
/// concierge that bootstraps new projects and stays alive after the
/// bootstrap window closes (W4 of the research-demo lifecycle).
///
/// Distinct from project-scoped domain stewards, which live on each
/// project page. Tapping the card lazily ensures the singleton is
/// running via `POST /v1/teams/{team}/steward.general/ensure` and then
/// opens its session chat. First-tap may take a beat (host pickup +
/// spawn); subsequent taps are immediate (idempotent).
///
/// The card hides itself when no team is configured. Empty teams (no
/// host registered yet) fail loudly via the snackbar — the user must
/// register a host-runner before the steward can run anywhere.
class PersistentStewardCard extends ConsumerStatefulWidget {
  const PersistentStewardCard({super.key});

  @override
  ConsumerState<PersistentStewardCard> createState() =>
      _PersistentStewardCardState();
}

class _PersistentStewardCardState extends ConsumerState<PersistentStewardCard> {
  bool _busy = false;

  /// Look up the live general-steward agent in cached hub state.
  /// Returns null when no `@steward` agent is running on this team —
  /// the card still renders, but the action label switches to
  /// "Start" so the user knows tapping triggers a spawn.
  Map<String, dynamic>? _findRunning() {
    final hub = ref.read(hubProvider).value;
    if (hub == null) return null;
    for (final a in hub.agents) {
      final handle = (a['handle'] ?? '').toString();
      if (!isGeneralStewardHandle(handle)) continue;
      final status = (a['status'] ?? '').toString();
      if (status == 'running' || status == 'pending') return a;
    }
    return null;
  }

  Future<void> _open() async {
    if (_busy) return;
    final client = ref.read(hubProvider.notifier).client;
    final hubState = ref.read(hubProvider).value;
    if (client == null || hubState == null || !hubState.configured) return;
    setState(() => _busy = true);
    try {
      // ensureGeneralSteward is idempotent — fast path returns the
      // existing agent id without touching host. Slow path picks a
      // host, copies the bundled template, and spawns. Both paths
      // return the same envelope.
      final res = await client.ensureGeneralSteward();
      final agentId = (res['agent_id'] ?? '').toString();
      if (agentId.isEmpty) {
        throw StateError('hub returned no agent_id');
      }
      // Refresh hub + sessions so the new agent + its auto-opened
      // session land in cached state before we navigate.
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
      if (!mounted) return;
      // Find the steward's active session — auto-open at spawn time
      // is guaranteed by the hub. If somehow missing (e.g. a stale
      // session row), fall back to SessionsScreen so the user can
      // pick what to do.
      Map<String, dynamic>? session;
      final sessions = ref.read(sessionsProvider).value;
      if (sessions != null) {
        for (final s in sessions.active) {
          if ((s['current_agent_id'] ?? '').toString() != agentId) continue;
          final status = (s['status'] ?? '').toString();
          if (status == 'active' || status == 'open') {
            session = s;
            break;
          }
        }
      }
      if (session != null) {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => SessionChatScreen(
            sessionId: (session!['id'] ?? '').toString(),
            agentId: agentId,
            title: (session['title'] ?? '').toString().isEmpty
                ? 'General Steward'
                : (session['title'] ?? '').toString(),
          ),
        ));
      } else {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => const SessionsScreen(),
        ));
      }
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('General steward: $e')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final hub = ref.watch(hubProvider).value;
    if (hub == null || !hub.configured) {
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final scheme = Theme.of(context).colorScheme;
    final running = _findRunning();
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final isLive = running != null;
    final actionLabel = isLive ? 'Open' : 'Start';
    final statusText = isLive
        ? 'Running · concierge'
        : 'Tap to start the team concierge';
    final tintBg = scheme.primary.withValues(alpha: isDark ? 0.10 : 0.08);
    final borderColor = scheme.primary.withValues(alpha: 0.35);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: _busy ? null : _open,
          borderRadius: BorderRadius.circular(12),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            decoration: BoxDecoration(
              color: tintBg,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: borderColor),
            ),
            child: Row(
              children: [
                Container(
                  width: 36,
                  height: 36,
                  decoration: BoxDecoration(
                    color: scheme.primary.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Icon(
                    Icons.auto_awesome,
                    size: 20,
                    color: scheme.primary,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Row(
                        children: [
                          Text(
                            'General Steward',
                            style: GoogleFonts.spaceGrotesk(
                              fontSize: 14,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                          const SizedBox(width: 8),
                          Container(
                            width: 8,
                            height: 8,
                            decoration: BoxDecoration(
                              shape: BoxShape.circle,
                              color: isLive
                                  ? DesignColors.success
                                  : muted.withValues(alpha: 0.6),
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 2),
                      Text(
                        statusText,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 11,
                          color: muted,
                        ),
                      ),
                    ],
                  ),
                ),
                if (_busy)
                  const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                else
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        actionLabel,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 12,
                          fontWeight: FontWeight.w700,
                          color: scheme.primary,
                        ),
                      ),
                      const SizedBox(width: 4),
                      Icon(Icons.chevron_right,
                          size: 18, color: scheme.primary),
                    ],
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
