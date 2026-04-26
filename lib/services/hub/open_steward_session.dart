import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/sessions/sessions_screen.dart';
import '../../screens/team/spawn_steward_sheet.dart';

/// Routes a "talk to the steward" intent to the right surface in the
/// post-W2 sessions ontology.
///
/// Behavior:
///   - **Active steward session (status=open)** → push the chat
///     directly. Same UX as the old `openHubMetaChannel` for healthy
///     stewards, but scoped to the steward's session, not the
///     team-wide hub-meta channel.
///   - **Active session is interrupted** → push SessionsScreen so
///     the user sees the warning chip + Resume button. We could
///     auto-resume here, but resume costs a respawn — better to
///     surface the choice than to fire it on a tap that the user
///     might have intended as "just check what's there".
///   - **No live steward at all** → open the spawn sheet (existing
///     bootstrap flow). Matches what the steward chip's tap-on-
///     absent does today.
///   - **Live steward but no session** (shouldn't happen post-
///     migration — the v1.0.274 shim creates one — but if a future
///     bug or a manual-DB-edit gets here) fall back to the
///     SessionsScreen empty state, which explains the situation.
///
/// Replaces direct calls to `openHubMetaChannel` from the Me FAB and
/// the project-page steward chip per `docs/steward-sessions.md`
/// §8.5 ("director↔steward sessions are not the team channel").
/// `hub-meta` stays reachable via the team switcher for legitimate
/// team-wide broadcast.
Future<void> openStewardSession(BuildContext context, WidgetRef ref) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;

  // Find the live steward agent.
  Map<String, dynamic>? steward;
  for (final a in hub.agents) {
    if ((a['handle'] ?? '').toString() != 'steward') continue;
    final status = (a['status'] ?? '').toString();
    if (status == 'running' || status == 'pending') {
      steward = a;
      break;
    }
  }
  if (steward == null) {
    if (context.mounted) {
      await showSpawnStewardSheet(context, hosts: hub.hosts);
    }
    return;
  }

  // Make sure the sessions list is up-to-date before we look for one
  // that points at this steward.
  await ref.read(sessionsProvider.notifier).refresh();
  final stewardId = (steward['id'] ?? '').toString();
  Map<String, dynamic>? session;
  final state = ref.read(sessionsProvider).value;
  if (state != null) {
    for (final s in state.active) {
      if ((s['current_agent_id'] ?? '').toString() == stewardId) {
        session = s;
        break;
      }
    }
  }
  if (!context.mounted) return;

  // Active + open → straight into the chat.
  final status = (session?['status'] ?? '').toString();
  if (session != null && status == 'open') {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => SessionChatScreen(
        sessionId: (session!['id'] ?? '').toString(),
        agentId: stewardId,
        title: (session['title'] ?? '').toString().isEmpty
            ? 'Steward session'
            : (session['title'] ?? '').toString(),
      ),
    ));
    return;
  }

  // Interrupted, missing, or any other state → SessionsScreen so the
  // user sees Resume / empty / etc. with full context.
  await Navigator.of(context).push(MaterialPageRoute(
    builder: (_) => const SessionsScreen(),
  ));
}
