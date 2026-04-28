import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/sessions/sessions_screen.dart';
import '../../screens/team/spawn_steward_sheet.dart';
import '../steward_handle.dart';

/// Routes a "talk to the steward" intent to the right surface in the
/// post-W2 sessions ontology.
///
/// Behavior:
///   - **Active session (status=active)** → push the chat directly.
///     Same UX as the old `openHubMetaChannel` for healthy stewards,
///     but scoped to the steward's session, not the team-wide
///     hub-meta channel.
///   - **Session is paused** → push SessionsScreen so the user sees
///     the warning chip + Resume button. We could auto-resume here,
///     but resume costs a respawn — better to surface the choice
///     than to fire it on a tap that the user might have intended
///     as "just check what's there".
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
///
/// Per ADR-009 D7, `scopeKind` + `scopeId` route to a session
/// matching that scope when one exists. Today the Me FAB calls
/// without scope (general/team default); the project-page chip
/// calls with `(project, projectId)`. Phase 2 will add the create-
/// new-project-scoped-session path; for Phase 1 this falls back to
/// the live steward's default session if no match is found, so the
/// project chip is at least no worse than it is today.
Future<void> openStewardSession(
  BuildContext context,
  WidgetRef ref, {
  String? scopeKind,
  String? scopeId,
}) async {
  final hub = ref.read(hubProvider).value;
  if (hub == null || !hub.configured) return;

  // Multi-steward routing (wedge 3):
  //   - 0 live stewards → spawn sheet (bootstrap path).
  //   - 1 live steward + active session → tap-into-chat (preserves
  //     the low-friction single-steward UX).
  //   - 2+ live stewards, OR exactly 1 with no active session →
  //     SessionsScreen so the user picks via the merged page.
  // Calling out the "1 with no session" case to SessionsScreen is
  // deliberate: the user might want Resume, or might want to spawn
  // another steward — either path lives on that surface now.
  final liveStewards = <Map<String, dynamic>>[];
  for (final a in hub.agents) {
    if (!isStewardHandle((a['handle'] ?? '').toString())) continue;
    final status = (a['status'] ?? '').toString();
    if (status == 'running' || status == 'pending') {
      liveStewards.add(a);
    }
  }
  if (liveStewards.isEmpty) {
    if (context.mounted) {
      await showSpawnStewardSheet(context, hosts: hub.hosts);
    }
    return;
  }
  if (liveStewards.length > 1) {
    if (!context.mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => const SessionsScreen(),
    ));
    return;
  }

  // Single-steward path: refresh sessions, look for an active one,
  // and jump straight into the chat if it exists.
  //
  // When a scope was passed, prefer a session matching that scope.
  // If none exists, OPEN one — silently falling through to the
  // steward's general/team session (the prior behavior) made
  // project-page taps land on team/general by accident, which read
  // as a routing bug to the user. Spawning a fresh scoped session
  // matches the documented "scope from entry point" intent of
  // ADR-009 D7.
  //
  // No scope passed: use the steward's first active session as
  // before — there's no scope ambiguity to resolve.
  await ref.read(sessionsProvider.notifier).refresh();
  final steward = liveStewards.first;
  final stewardId = (steward['id'] ?? '').toString();
  Map<String, dynamic>? session;
  Map<String, dynamic>? scopedMatch;
  Map<String, dynamic>? fallback;
  final state = ref.read(sessionsProvider).value;
  if (state != null) {
    for (final s in state.active) {
      if ((s['current_agent_id'] ?? '').toString() != stewardId) continue;
      fallback ??= s;
      if (scopeKind != null && scopeKind.isNotEmpty &&
          (s['scope_kind'] ?? '').toString() == scopeKind &&
          (s['scope_id'] ?? '').toString() == (scopeId ?? '')) {
        scopedMatch = s;
        break;
      }
    }
  }
  if (scopedMatch == null && scopeKind != null && scopeKind.isNotEmpty) {
    final client = ref.read(hubProvider.notifier).client;
    if (client != null) {
      try {
        final created = await client.openSession(
          agentId: stewardId,
          scopeKind: scopeKind,
          scopeId: scopeId,
        );
        await ref.read(sessionsProvider.notifier).refresh();
        scopedMatch = created;
      } catch (_) {
        // Fall through to fallback so a transient open failure still
        // gets the user *somewhere* useful instead of a dead tap.
      }
    }
  }
  session = scopedMatch ?? fallback;
  if (!context.mounted) return;

  final status = (session?['status'] ?? '').toString();
  if (session != null && (status == 'active' || status == 'open')) {
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

  // Paused, missing, or any other state → SessionsScreen.
  await Navigator.of(context).push(MaterialPageRoute(
    builder: (_) => const SessionsScreen(),
  ));
}
