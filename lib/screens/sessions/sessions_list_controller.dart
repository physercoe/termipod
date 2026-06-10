import '../../providers/sessions_provider.dart';
import '../../services/steward_handle.dart';

/// Orchestration seam for the Sessions list (WS2 of
/// docs/plans/internal-techdebt-cleanup.md). The session-list bucketing —
/// grouping sessions under their steward, splitting current vs previous,
/// categorising stewards, and collecting orphan/detached sessions — is pure
/// list-shaping over the hub's `Map<String, dynamic>` rows, so it lives here
/// as plain functions that can be unit-tested without a widget harness. The
/// `SessionsScreen` widget keeps view composition (and lifecycle routing) only.

/// One steward and its sessions: the live [agent], its [current] session
/// (active or paused, at most one), and the [previous] (archived/detached)
/// sessions. A synthetic group (empty [agentId]) buckets orphan sessions.
class StewardGroup {
  final Map<String, dynamic> agent;
  final Map<String, dynamic>? current; // active or paused
  final List<Map<String, dynamic>> previous; // archived
  const StewardGroup({
    required this.agent,
    required this.current,
    required this.previous,
  });

  String get handle => (agent['handle'] ?? '').toString();
  String get agentId => (agent['id'] ?? '').toString();
  String get kind => (agent['kind'] ?? '').toString();
  String get status => (agent['status'] ?? '').toString();
  String get projectId => (agent['project_id'] ?? '').toString();
}

/// Categories the steward sections cluster under in the Sessions list.
/// User has multiple stewards (general + per-project + domain) and a
/// flat list hides the hierarchy. Each category renders a collapsible
/// header so the user can hide categories they aren't actively using.
enum StewardCategory { general, project, domain, detached }

/// Classify a group by its agent's handle/kind/project shape. The
/// classification mirrors how stewards are spawned:
///   - General: the team-singleton concierge (`@steward`)
///   - Project: stewards bound to a project (`@steward.<pid8>` or kind
///     starts with `steward.` + project_id non-empty)
///   - Domain: long-lived role stewards (`research-steward`,
///     `infra-steward`, …) — handle ends with `-steward` and not
///     project-bound
///   - Detached: synthetic group with empty agentId (orphan sessions)
StewardCategory categorizeStewardGroup(StewardGroup g) {
  if (g.agentId.isEmpty) return StewardCategory.detached;
  if (isGeneralStewardHandle(g.handle)) return StewardCategory.general;
  final isProjectBound =
      g.handle.startsWith('@steward.') || g.projectId.isNotEmpty;
  if (isProjectBound) return StewardCategory.project;
  return StewardCategory.domain;
}

/// Display count for a category header / filter chip. The general /
/// project / domain categories carry one [StewardGroup] per steward, so
/// the group count is the meaningful number ("5 project stewards").
/// [StewardCategory.detached] is a single synthetic group that buckets
/// *every* orphan session, so its group count is always 1 — count the
/// sessions it holds instead (#122).
int categoryDisplayCount(StewardCategory cat, Iterable<StewardGroup> groups) {
  if (cat == StewardCategory.detached) {
    var n = 0;
    for (final g in groups) {
      if (categorizeStewardGroup(g) == StewardCategory.detached) {
        n += g.previous.length;
      }
    }
    return n;
  }
  return groups.where((g) => categorizeStewardGroup(g) == cat).length;
}

/// Scope key for a session row — `'<scope_kind>|<scope_id>'`. Mirrors
/// the bucketing key the scope-grouped Previous list renders, so the
/// detached scope sub-filter (#122) and the rendered sub-headers stay in
/// lockstep.
String sessionScopeKey(Map<String, dynamic> s) {
  final kind = (s['scope_kind'] ?? '').toString();
  final id = (s['scope_id'] ?? '').toString();
  return '$kind|$id';
}

/// Bucket sessions by scope key, preserving first-seen order but
/// floating the Team / empty-scope bucket to the top (the common case
/// shouldn't sink under project-specific groups when there are many
/// projects). Returns ordered (key, sessions) pairs. Shared by the
/// scope-grouped Previous list and the select-mode scope sub-filter
/// chips so both partition identically (#122).
List<MapEntry<String, List<Map<String, dynamic>>>> bucketSessionsByScope(
  List<Map<String, dynamic>> sessions,
) {
  final buckets = <String, List<Map<String, dynamic>>>{};
  final order = <String>[];
  for (final s in sessions) {
    final key = sessionScopeKey(s);
    if (!buckets.containsKey(key)) {
      buckets[key] = [];
      order.add(key);
    }
    buckets[key]!.add(s);
  }
  order.sort((a, b) {
    final aGen = a.startsWith('team|') || a.startsWith('|');
    final bGen = b.startsWith('team|') || b.startsWith('|');
    if (aGen != bGen) return aGen ? -1 : 1;
    return 0;
  });
  return [for (final k in order) MapEntry(k, buckets[k]!)];
}

/// All detached (orphan) sessions across the synthetic group(s),
/// bucketed by scope. Empty when there are no detached sessions. Drives
/// the select-mode scope sub-filter chips (#122).
List<MapEntry<String, List<Map<String, dynamic>>>> detachedScopeBuckets(
  Iterable<StewardGroup> groups,
) {
  final detached = <Map<String, dynamic>>[];
  for (final g in groups) {
    if (categorizeStewardGroup(g) == StewardCategory.detached) {
      detached.addAll(g.previous);
    }
  }
  return bucketSessionsByScope(detached);
}

String stewardCategoryLabel(StewardCategory c) {
  switch (c) {
    case StewardCategory.general:
      return 'General steward';
    case StewardCategory.project:
      return 'Project stewards';
    case StewardCategory.domain:
      return 'Domain stewards';
    case StewardCategory.detached:
      return 'Detached sessions';
  }
}

/// Build one section per live steward + one section per "orphan"
/// (sessions whose current_agent_id doesn't match any live steward —
/// happens when the steward was terminated outside this UI).
/// Stewards without any session at all are still listed (the multi-
/// steward UX invariant says every live steward has one, so this is
/// the back-compat case for installs that pre-date auto_open_session).
List<StewardGroup> groupSessionsBySteward(
  List<Map<String, dynamic>> agents,
  SessionsState sessions,
) {
  final byAgent = <String, List<Map<String, dynamic>>>{};
  for (final ses in [...sessions.active, ...sessions.previous]) {
    final aid = (ses['current_agent_id'] ?? '').toString();
    byAgent.putIfAbsent(aid, () => []).add(ses);
  }
  final liveStewardIds = <String>{};
  final groups = <StewardGroup>[];
  for (final a in agents) {
    // Steward predicates by handle don't catch project stewards —
    // they're spawned with @steward.<pid8> (handlers_project_steward.go
    // line 46) which isStewardHandle deliberately excludes. isStewardAgent
    // folds in the `kind` column as the authoritative steward signal so
    // general + domain + project stewards all surface in the Sessions list.
    if (!isStewardAgent(a)) continue;
    final status = (a['status'] ?? '').toString();
    if (status != 'running' && status != 'pending' && status != 'paused') {
      continue;
    }
    final id = (a['id'] ?? '').toString();
    liveStewardIds.add(id);
    final mine = byAgent[id] ?? const <Map<String, dynamic>>[];
    Map<String, dynamic>? current;
    final previous = <Map<String, dynamic>>[];
    for (final s in mine) {
      final st = (s['status'] ?? '').toString();
      if (isLiveSessionStatus(st) && current == null) {
        current = s;
      } else {
        previous.add(s);
      }
    }
    groups.add(StewardGroup(
      agent: a,
      current: current,
      previous: previous,
    ));
  }
  // Sort: stewards with an active session by last_active_at desc;
  // stewards with only previous sessions go to the bottom.
  DateTime ts(StewardGroup g) {
    final s = g.current ?? (g.previous.isEmpty ? null : g.previous.first);
    if (s == null) return DateTime.fromMillisecondsSinceEpoch(0);
    final raw = (s['last_active_at'] ?? s['opened_at'] ?? '').toString();
    return DateTime.tryParse(raw) ?? DateTime.fromMillisecondsSinceEpoch(0);
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
      if (st == 'active') {
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
    groups.add(StewardGroup(
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
