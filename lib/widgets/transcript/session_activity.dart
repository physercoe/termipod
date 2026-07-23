/// P2 state dock derivation (docs/plans/agent-transcript-redesign.md §6 P2,
/// decision §7.5) — the session-state model behind the ambient chips above
/// the composer (kimi-web `ChatDock.vue` model: **state visibility, NOT feed
/// filtering** — the lens system stays untouched).
///
/// Pure over the event list + the [FoldMaps] lineage — no widget, no mode —
/// so the counts are unit-testable without a widget tree and the derivation
/// can never drift from the lineage the cards render from.
///
/// The input MUST be the FULL session event list, not the lens-filtered one:
/// the chips are session state, so a lens change must not move the counts.
/// The derivation only reads state-bearing kinds (`tool_call` + the
/// `tool_call_update`/`tool_result` lineage + `plan`), which is what makes
/// that invariance hold — dropping text/thought/completion events from the
/// input yields byte-identical output (pinned by the lens-invariance test).
///
/// Baseline-honesty note (ticket #368, locked design): background-vs-
/// foreground task detection needs metadata ACP doesn't carry today —
/// kimi's `display` hints land in P4 (the wire-tail adapter), claude's task
/// frames are their own follow-up. Until then this is the plan's
/// engine-agnostic NAME-MATCH baseline (§6 P2): shell/sub-agent calls are
/// recognized by tool name alone. Foreground sub-agents still render inline
/// in the transcript (the `Agent`/`Task` tool_call cards) — unchanged; the
/// dock is an additional at-a-glance surface, not a reclassification.
library;

import 'feed_reducer.dart';
import 'fold_maps.dart';

/// Cap on each per-kind call list the dock surfaces (§6 P2): running calls
/// first, then the most recent terminal ones. The RUNNING counts are never
/// capped — only the detail lists are.
const kSessionActivityCallCap = 20;

/// Shell-command tool names, lower-cased (§6 P2 locked set). Matched
/// case-insensitively against the bare tool name or the tool leaf of the
/// `mcp__<server>__<name>` form.
const kSessionDockShellNames = {
  'bash',
  'shell',
  'exec',
  'exec_command',
  'run_shell_command',
  'execute_command',
};

/// Sub-agent tool names, lower-cased (§6 P2 locked set) — same matching
/// rule as [kSessionDockShellNames].
const kSessionDockSubagentNames = {'agent', 'task'};

/// The display state of one surfaced call — running / done / error, the
/// same three states the P1 group-card rows render (`ToolGroupState`), so
/// the dock rows and the transcript rows share one status-glyph style.
enum SessionTaskStatus { running, done, error }

/// The kind of detail list a chip opens in the bottom sheet — also the
/// segmented switcher's segments.
enum SessionDockKind { tasks, subagents, todos }

/// One shell/sub-agent call surfaced by the dock: icon + name + key
/// argument + status glyph (kimi-web `ToolRow.vue` shape, mirroring the P1
/// group-card row).
class SessionTaskCall {
  /// The tool_call id (may be empty for id-less drivers).
  final String id;

  /// The raw payload tool name (`Bash`, `mcp__server__exec`, …) — already
  /// self-describing, shown verbatim like the group card's fallback.
  final String name;

  /// The one-line key argument ([toolCallKeyArg]) — the command for a
  /// shell call, the description/prompt for a sub-agent call. May be ''.
  final String keyArg;

  final SessionTaskStatus status;

  const SessionTaskCall({
    required this.id,
    required this.name,
    required this.keyArg,
    required this.status,
  });
}

/// One todo entry from the newest `plan` snapshot: content + status
/// (`pending` / `in_progress` / `completed` — the ACP plan-entry shape,
/// event_card.dart `_planBody`).
class SessionTodoItem {
  final String content;
  final String status;

  const SessionTodoItem({required this.content, required this.status});

  /// `'done'` tolerated alongside the canonical `'completed'` — desktop's
  /// planMark (EventCard.tsx) and dock already treat both as finished, and
  /// the same transcript must count the same on both clients.
  bool get isCompleted => status == 'completed' || status == 'done';
}

/// The todos rollup behind the `Todos (done/total)` chip: the NEWEST plan
/// event's entries (each plan payload is a full snapshot, so the latest
/// one wins outright — no merging).
class SessionTodos {
  final List<SessionTodoItem> items;
  final int done;
  final int total;

  const SessionTodos({
    required this.items,
    required this.done,
    required this.total,
  });
}

/// The derived session activity the dock renders from.
class SessionActivity {
  /// Shell calls still in flight — the `Tasks (n)` chip count.
  final int shellRunning;

  /// Shell calls for the sheet's Tasks list: running first (in event
  /// order), then recent terminal (completed + failed), newest first,
  /// capped at [kSessionActivityCallCap].
  final List<SessionTaskCall> shellCalls;

  /// Sub-agent calls still in flight — the `Sub-agents (n)` chip count.
  final int subagentRunning;

  /// Sub-agent calls for the sheet, same ordering/cap as [shellCalls].
  final List<SessionTaskCall> subagentCalls;

  /// The newest plan snapshot, or null when the session has no `plan`
  /// event at all (the Todos chip hides).
  final SessionTodos? todos;

  const SessionActivity({
    required this.shellRunning,
    required this.shellCalls,
    required this.subagentRunning,
    required this.subagentCalls,
    required this.todos,
  });

  // Chip visibility rules (§6 P2 locked): the task chips exist ONLY while
  // something is running — kimi-web's "background tasks earn chips" rule;
  // a finished task list is transcript history, not ambient state. The
  // Todos chip exists whenever the agent has published a plan (done/total
  // is state even at 5/5).
  bool get showTasks => shellRunning > 0;
  bool get showSubagents => subagentRunning > 0;
  bool get showTodos => todos != null;

  /// No chips visible → the strip renders nothing (zero layout cost).
  bool get isEmpty => !showTasks && !showSubagents && !showTodos;
}

/// The name-match baseline: lowercase the raw name and unwrap the
/// `mcp__<server>__<tool>` form to its tool leaf, so kimi's MCP-proxied
/// shell (`mcp__…__bash`) classifies the same as a bare `Bash`. Non-MCP
/// names pass through untouched — `BashOutput`/`Taskforce` must NOT match
/// the locked sets, so the comparison is exact-set membership, never
/// prefix/substring.
String sessionDockBaseToolName(String rawName) {
  var n = rawName.trim().toLowerCase();
  if (n.startsWith('mcp__')) {
    final i = n.lastIndexOf('__');
    // i > 'mcp'.length guards the degenerate `mcp__bash` form (no server
    // segment) — that isn't the documented shape, leave it unmatched.
    if (i > 4 && i + 2 < n.length) n = n.substring(i + 2);
  }
  return n;
}

/// Derive the session activity from the FULL event list + its [FoldMaps]
/// lineage. [fold] must have been built from the same [events]
/// (`FoldMaps.fromEvents(events)`) — the live feed already builds it for
/// the cards, so this adds one cheap linear scan per rebuild.
SessionActivity sessionActivityFromEvents(
  List<Map<String, dynamic>> events,
  FoldMaps fold,
) {
  final shellRunning = <SessionTaskCall>[];
  final shellTerminal = <SessionTaskCall>[];
  final subRunning = <SessionTaskCall>[];
  final subTerminal = <SessionTaskCall>[];
  SessionTodos? todos;

  for (final e in events) {
    final kind = (e['kind'] ?? '').toString();
    final p = e['payload'];
    if (p is! Map) continue;
    final payload = p.cast<String, dynamic>();

    if (kind == 'plan') {
      // Every plan event is a FULL snapshot (ACP sessionUpdate "plan"), so
      // the newest one seen wins outright — no merge, no chaining (the
      // raw list still carries every partial of a P1-folded chain; only
      // the last matters here).
      final entriesRaw = payload['entries'];
      final items = <SessionTodoItem>[];
      if (entriesRaw is List) {
        for (final entry in entriesRaw) {
          if (entry is! Map) continue;
          items.add(
            SessionTodoItem(
              content: (entry['content'] ?? '').toString(),
              status: (entry['status'] ?? '').toString(),
            ),
          );
        }
      }
      final done = items.where((t) => t.isCompleted).length;
      todos = SessionTodos(items: items, done: done, total: items.length);
      continue;
    }

    if (kind != 'tool_call') continue;
    final name = (payload['name'] ?? payload['tool'] ?? '').toString();
    if (name.isEmpty) continue;
    final base = sessionDockBaseToolName(name);
    final isShell = kSessionDockShellNames.contains(base);
    final isSubagent = !isShell && kSessionDockSubagentNames.contains(base);
    if (!isShell && !isSubagent) continue;

    // Per-call status reuses the P1 shared derivation verbatim
    // ([toolCallDisplayStatus]): the streaming tool_call_update status
    // wins over the creation-frame status; a paired tool_result resolves
    // completed/failed by is_error when no status exists; nothing yet →
    // pending. Mapping (locked): completed → done, failed → error (with
    // 'error' treated as failed, matching toolCallRowState's terminal
    // classification), pending/in_progress/anything non-terminal →
    // running.
    final id = callToolIdOf(payload);
    final resEvent = id.isNotEmpty ? fold.toolResults[id] : null;
    final resPayload = resEvent != null && resEvent['payload'] is Map
        ? (resEvent['payload'] as Map).cast<String, dynamic>()
        : null;
    final status = toolCallDisplayStatus(
      payload,
      id.isNotEmpty ? fold.toolUpdates[id] : null,
      resPayload,
    );
    final taskStatus = switch (status) {
      'completed' => SessionTaskStatus.done,
      'failed' || 'error' => SessionTaskStatus.error,
      _ => SessionTaskStatus.running,
    };
    final call = SessionTaskCall(
      id: id,
      name: name,
      keyArg: toolCallKeyArg(name, payload['input']),
      status: taskStatus,
    );
    if (isShell) {
      (taskStatus == SessionTaskStatus.running ? shellRunning : shellTerminal)
          .add(call);
    } else {
      (taskStatus == SessionTaskStatus.running ? subRunning : subTerminal).add(
        call,
      );
    }
  }

  // Running first (event order — the scan appended them in order), then
  // recent terminal calls newest-first, capped. Errors stay in the list:
  // a failed background task is exactly the state an operator opens the
  // dock to find.
  List<SessionTaskCall> ordered(
    List<SessionTaskCall> running,
    List<SessionTaskCall> terminal,
  ) =>
      [...running, ...terminal.reversed].take(kSessionActivityCallCap).toList();

  return SessionActivity(
    shellRunning: shellRunning.length,
    shellCalls: ordered(shellRunning, shellTerminal),
    subagentRunning: subRunning.length,
    subagentCalls: ordered(subRunning, subTerminal),
    todos: todos,
  );
}
