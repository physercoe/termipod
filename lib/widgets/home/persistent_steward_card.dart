import 'dart:convert';

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

  /// Tolerate either a parsed JSON object or a raw JSON string under
  /// `capabilities` — the hub serializes it as a json.RawMessage which
  /// Dart parses to Map, but cache/SSE paths may surface the verbatim
  /// string form.
  Map<String, dynamic> _capsAsMap(dynamic raw) {
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return const <String, dynamic>{};
  }

  /// Bottom sheet picker for the binding host. Surfaces each team-host
  /// with a one-line caps summary so the principal sees at a glance
  /// which boxes can actually run claude-code (the steward's backend);
  /// hosts that haven't probed yet, or report claude-code missing,
  /// stay tappable but show a warning subtitle so the call lands the
  /// real "not installed on host" error rather than masking it.
  Future<String?> _pickHost(List<Map<String, dynamic>> hosts) {
    return showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (sheetCtx) {
        final muted = Theme.of(sheetCtx).brightness == Brightness.dark
            ? DesignColors.textMuted
            : DesignColors.textMutedLight;
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  'Choose a host',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'The general steward will run on this host.',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: muted,
                  ),
                ),
                const SizedBox(height: 12),
                for (final h in hosts) _hostTile(sheetCtx, h, muted),
              ],
            ),
          ),
        );
      },
    );
  }

  /// One row in the host picker. Resolves the host's caps_json (if
  /// present) so we can flag boxes that don't have claude-code yet —
  /// the spawn would still fail there, and surfacing that ahead of
  /// time saves the user a round-trip.
  Widget _hostTile(
      BuildContext sheetCtx, Map<String, dynamic> host, Color muted) {
    final id = (host['id'] ?? '').toString();
    final name = (host['name'] ?? '').toString();
    final displayName = name.isEmpty ? 'host:${id.substring(0, 8)}' : name;
    final status = (host['status'] ?? '').toString();
    final caps = _capsAsMap(host['capabilities']);
    final agents = caps['agents'] is Map
        ? (caps['agents'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final claude = agents['claude-code'] is Map
        ? (agents['claude-code'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final installed = claude['installed'] == true;
    final probed = agents.isNotEmpty;
    final subtitle = !probed
        ? 'No probe yet · status=$status'
        : installed
            ? 'claude-code ready · status=$status'
            : 'claude-code NOT installed · status=$status';
    final warn = probed && !installed;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Icon(
        Icons.dns_outlined,
        color: warn ? Colors.orange : null,
      ),
      title: Text(
        displayName,
        style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
      ),
      subtitle: Text(
        subtitle,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: warn ? Colors.orange : muted,
        ),
      ),
      onTap: () => Navigator.of(sheetCtx).pop(id),
    );
  }

  Future<void> _open() async {
    if (_busy) return;
    final client = ref.read(hubProvider.notifier).client;
    final hubState = ref.read(hubProvider).value;
    if (client == null || hubState == null || !hubState.configured) return;

    // First-time spawn on a multi-host team — let the principal pick
    // which host the always-on steward binds to. Single-host stays
    // one-tap; idempotent re-opens (existing live steward) skip the
    // sheet because the host is already locked in.
    String? pinnedHostId;
    final isLive = _findRunning() != null;
    if (!isLive && hubState.hosts.length >= 2) {
      pinnedHostId = await _pickHost(hubState.hosts);
      if (pinnedHostId == null) return; // user dismissed
    }

    setState(() => _busy = true);
    try {
      // ensureGeneralSteward is idempotent — fast path returns the
      // existing agent id without touching host. Slow path picks a
      // host, copies the bundled template, and spawns. Both paths
      // return the same envelope.
      final res = await client.ensureGeneralSteward(hostId: pinnedHostId);
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
