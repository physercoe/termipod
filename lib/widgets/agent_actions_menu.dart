// Shared agent-lifecycle action vocabulary + dispatcher — the single source of
// truth for the worker lifecycle actions and their *names*, so the project-agent
// surface and the steward/session surface can't drift (the "Terminate" vs
// "Archive" divergence this consolidates).
//
// Canonical vocabulary — docs/reference/glossary.md (principal-facing words):
//   - **Pause**  — SIGSTOP a still-alive process (`pause_state`); reversible.
//   - **Stop**   — kill the process, session → `paused` (RESUMABLE via Resume
//                  session). The recoverable halt.
//   - **Archive** — the PERMANENT end: kill the process, session → `archived`
//                  (fork-only, not resumable). (Hub op `terminateAgent`.)
//   - **Resume session** — inverse of Stop: respawn a fresh agent into the
//                  paused session, continuing the transcript.
//   - **Respawn** — spawn a NEW agent from the same spec (fresh transcript).
//   - **Delete**  — remove a dead agent's row from the live list (the row is
//                  preserved in the DB; hub op `archiveAgent`).
//
// Both the project-agent detail sheet and the session header build their menu
// from [agentLifecycleMenuItems] and run destructive/lifecycle actions through
// [runAgentLifecycleAction], which owns the confirm dialog + client call + hub
// refresh + error SnackBar. The caller only does surface-specific navigation
// from the returned [AgentActionOutcome] (and handles `config` / `respawn`,
// which need surface context — a config sheet, a spec + handle).
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/hub_provider.dart';
import '../providers/sessions_provider.dart';
import '../services/hub/agent_status.dart';
import '../services/hub/hub_transport.dart';
import '../theme/design_colors.dart';

/// Menu action values (also the `onSelected` switch keys). `config` and
/// `respawn` are surface-handled; the rest go through [runAgentLifecycleAction].
class AgentAction {
  static const config = 'agent_config';
  static const pauseResume = 'pause_resume';
  static const resumeSession = 'resume_session';
  static const respawn = 'respawn';
  static const stop = 'stop';
  static const archive = 'archive';
  static const delete = 'delete';
}

/// What a dispatched action did, so the caller can navigate its surface.
enum AgentActionOutcome {
  /// Cancelled at the confirm dialog, or no client — nothing changed.
  cancelled,

  /// Acted; the agent is still tracked here (Pause / Stop) — refresh + stay.
  stayed,

  /// Acted; the tracked agent left the live surface (Archive / Delete) — the
  /// caller should pop / return to the list.
  removed,

  /// A fresh agent now drives the (still-live) session (Resume session).
  sessionResumed,
}

/// The shared lifecycle menu items — the single source of the labels. Gated by
/// the agent's state exactly like the project-agent sheet always was.
List<PopupMenuEntry<String>> agentLifecycleMenuItems(
  BuildContext context, {
  required bool isDead,
  required bool isPaused,
  required bool hasPane,
  required bool canRespawn,
  AgentResumability resumable = AgentResumability.unknown,
}) {
  final err = Theme.of(context).colorScheme.error;
  return [
    if (!isDead && hasPane)
      PopupMenuItem(
        value: AgentAction.pauseResume,
        child: ListTile(
          leading: Icon(isPaused ? Icons.play_arrow : Icons.pause),
          title: Text(isPaused ? 'Resume' : 'Pause'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    // Resume the paused session a Stop left behind (continue, not fresh).
    // Offered for a dead agent UNLESS we know its session was archived
    // (permanent — Resume would 409): an "Archived/ended" run is fork-only, so
    // hiding the item keeps the user from a guaranteed-failed resume (the
    // confusing "tried to resume, failed" path). When the session fate is
    // unknown (cold list) we still offer it and let the hub 409 clearly.
    if (isDead && resumable != AgentResumability.permanent)
      const PopupMenuItem(
        value: AgentAction.resumeSession,
        child: ListTile(
          leading:
              Icon(Icons.play_circle_outline, color: DesignColors.success),
          title: Text('Resume session'),
          subtitle: Text('Continue the paused session (keeps history)'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    if (canRespawn)
      const PopupMenuItem(
        value: AgentAction.respawn,
        child: ListTile(
          leading: Icon(Icons.replay),
          title: Text('Respawn'),
          subtitle: Text('Spawn a new agent from the same spec'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    // Stop = resumable halt; sits above Archive so the recoverable option is the
    // default reach. Both kill the live pane.
    if (!isDead)
      const PopupMenuItem(
        value: AgentAction.stop,
        child: ListTile(
          leading: Icon(Icons.pause_circle_outline),
          title: Text('Stop'),
          subtitle: Text('Halt; session paused (resumable)'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    if (!isDead)
      PopupMenuItem(
        value: AgentAction.archive,
        child: ListTile(
          leading: Icon(Icons.archive_outlined, color: err),
          title: Text('Archive', style: TextStyle(color: err)),
          subtitle: const Text('Permanent; session archived, fork-only'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    if (isDead)
      PopupMenuItem(
        value: AgentAction.delete,
        child: ListTile(
          leading: Icon(Icons.delete_outline, color: err),
          title: Text('Delete', style: TextStyle(color: err)),
          subtitle: const Text('Remove from the list; row preserved'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
  ];
}

/// Run a lifecycle [value] (Pause/Stop/Archive/Resume-session/Delete) end to
/// end: confirm dialog (where destructive) → client call → hub refresh, with a
/// success/error SnackBar. Returns an [AgentActionOutcome] for surface nav.
/// `config` / `respawn` are not handled here (surface-specific).
Future<AgentActionOutcome> runAgentLifecycleAction(
  BuildContext context,
  WidgetRef ref,
  String value, {
  required String agentId,
  required String handle,
  bool isPaused = false,
}) async {
  final client = ref.read(hubProvider.notifier).client;
  if (client == null || agentId.isEmpty) return AgentActionOutcome.cancelled;
  final messenger = ScaffoldMessenger.of(context);

  Future<bool> confirm({
    required String title,
    required String body,
    required String action,
    bool destructive = true,
  }) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title),
        content: Text(body),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: destructive
                ? FilledButton.styleFrom(
                    backgroundColor: Theme.of(ctx).colorScheme.error)
                : null,
            onPressed: () => Navigator.pop(ctx, true),
            child: Text(action),
          ),
        ],
      ),
    );
    return ok == true;
  }

  Future<AgentActionOutcome> guarded(
    Future<void> Function() op, {
    required String ok,
    required AgentActionOutcome outcome,
  }) async {
    try {
      await op();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('Action failed: $e')));
      return AgentActionOutcome.cancelled;
    }
    await ref.read(hubProvider.notifier).refreshAll();
    // Resumability ("stopped" vs "archived") is read off the session, not the
    // agent row — refresh the sessions snapshot too so a just-stopped agent
    // resolves as resumable immediately (the label + Resume gate depend on it).
    await ref.read(sessionsProvider.notifier).refresh();
    messenger.showSnackBar(SnackBar(content: Text(ok)));
    return outcome;
  }

  switch (value) {
    case AgentAction.pauseResume:
      // Reversible SIGSTOP — no confirm. Toggle on the caller-supplied state.
      return guarded(
          () async {
            if (isPaused) {
              await client.resumeAgent(agentId);
            } else {
              await client.pauseAgent(agentId);
            }
          },
          ok: isPaused ? 'Resume command enqueued' : 'Pause command enqueued',
          outcome: AgentActionOutcome.stayed);
    case AgentAction.stop:
      if (!await confirm(
        title: 'Stop "$handle"?',
        body: 'Halts this worker: the host-runner kills the pane (dirty '
            'worktrees are preserved) and the session is PAUSED — resumable '
            'later from where it left off. To end it permanently instead, use '
            'Archive.',
        action: 'Stop',
        destructive: false,
      )) {
        return AgentActionOutcome.cancelled;
      }
      return guarded(() => client.stopAgent(agentId),
          ok: 'Stopped — session paused (resumable)',
          outcome: AgentActionOutcome.stayed);
    case AgentAction.archive:
      if (!await confirm(
        title: 'Archive "$handle"?',
        body: 'Permanently ends this worker: the host-runner kills the pane '
            '(dirty worktrees are preserved) and the session is ARCHIVED — '
            'not resumable, only fork-eligible. To halt a worker you may want '
            'back, use Stop instead.',
        action: 'Archive',
      )) {
        return AgentActionOutcome.cancelled;
      }
      return guarded(() => client.terminateAgent(agentId),
          ok: 'Archived', outcome: AgentActionOutcome.removed);
    case AgentAction.delete:
      if (!await confirm(
        title: 'Delete "$handle"?',
        body: 'Moves this terminated agent off the live list. The row stays in '
            'the database so spawn history and audit events still resolve. You '
            'can review archived agents from the history page.',
        action: 'Delete',
      )) {
        return AgentActionOutcome.cancelled;
      }
      return guarded(() => client.archiveAgent(agentId),
          ok: 'Deleted', outcome: AgentActionOutcome.removed);
    case AgentAction.resumeSession:
      // A 409 here means there's no paused session to bring back — the run was
      // Archived (session archived, fork-only) or is still live. Surface that
      // plainly instead of the generic "Action failed" so a user who tapped
      // Resume on an archived run learns *why* and what to do (fork) — not just
      // that it failed. (The menu already hides Resume when the session fate is
      // known-permanent; this covers the cold-snapshot case where it isn't.)
      try {
        await client.resumeAgentSession(agentId);
      } on HubApiError catch (e) {
        final msg = e.status == 409
            ? 'No paused session to resume — this run was archived '
                '(permanent). Fork it to continue from its history.'
            : 'Resume failed: $e';
        messenger.showSnackBar(SnackBar(content: Text(msg)));
        return AgentActionOutcome.cancelled;
      } catch (e) {
        messenger.showSnackBar(SnackBar(content: Text('Resume failed: $e')));
        return AgentActionOutcome.cancelled;
      }
      await ref.read(hubProvider.notifier).refreshAll();
      await ref.read(sessionsProvider.notifier).refresh();
      messenger.showSnackBar(const SnackBar(content: Text('Session resumed')));
      return AgentActionOutcome.sessionResumed;
    default:
      return AgentActionOutcome.cancelled;
  }
}

/// Respawn = spawn a NEW agent from [specYaml] (fresh transcript, same spec).
/// Prompts for a new handle, then spawns + refreshes. Returns true if a respawn
/// was requested. Shared so the project-agent sheet and a worker session surface
/// offer it identically. (Stewards have their own singleton spawn flow — callers
/// gate this off for steward agents.)
Future<bool> respawnAgentFromSpec(
  BuildContext context,
  WidgetRef ref, {
  required String handle,
  required String kind,
  required String hostId,
  required String specYaml,
}) async {
  if (specYaml.isEmpty) return false;
  final messenger = ScaffoldMessenger.of(context);
  final suggested = '$handle-r${DateTime.now().millisecondsSinceEpoch % 10000}';
  final ctrl = TextEditingController(text: suggested);
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Respawn from spec'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'Spawns a new agent using the same spec. The original row stays '
            'untouched — archive it first if you want to free the handle.',
            style: TextStyle(fontSize: 12),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: ctrl,
            autofocus: true,
            decoration: const InputDecoration(labelText: 'New handle'),
          ),
        ],
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel')),
        FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Respawn')),
      ],
    ),
  );
  if (ok != true) return false;
  final newHandle = ctrl.text.trim();
  if (newHandle.isEmpty) return false;
  final client = ref.read(hubProvider.notifier).client;
  if (client == null) return false;
  try {
    await client.spawnAgent(
      childHandle: newHandle,
      kind: kind,
      spawnSpecYaml: specYaml,
      hostId: hostId.isEmpty ? null : hostId,
    );
  } catch (e) {
    messenger.showSnackBar(SnackBar(content: Text('Respawn failed: $e')));
    return false;
  }
  await ref.read(hubProvider.notifier).refreshAll();
  messenger.showSnackBar(SnackBar(content: Text('Respawn requested: $newHandle')));
  return true;
}
