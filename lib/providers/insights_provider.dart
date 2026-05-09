import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Scope-kind enum for `/v1/insights`. Mirrors hub-side
/// `insights_scope.go` (insights-phase-2 W1). User scope is omitted —
/// the hub rejects it pending per-token user attribution (ADR-005's
/// principal/director model has no user table at MVP).
///
/// `teamStewards` is a sub-qualifier on team scope — narrows to agents
/// whose handle matches the steward convention (`steward`,
/// `*-steward`, or `@steward`). On the wire it's `team_id=X&kind=steward`.
enum InsightsScopeKind { project, team, teamStewards, agent, engine, host }

/// Value object naming a single (kind, id) scope plus an optional
/// time window. `id` is whatever the scope kind takes — project ULID,
/// team handle, agent ULID, engine label
/// (`claude-code`/`gemini-cli`/`codex`), or host ULID.
///
/// `since`/`until` are baked into the value object so the family
/// provider's cache key includes them; switching between 24h / 7d /
/// 30d on `InsightsScreen` rebuilds the scope and re-keys the cache
/// row, so each window's snapshot persists independently across
/// screen revisits (ADR-022 D6).
///
/// Equality + hashCode are required because Riverpod family providers
/// key on the parameter; without these, two `InsightsScope.project(x)`
/// instances would be different cache entries even when conceptually
/// identical.
class InsightsScope {
  final InsightsScopeKind kind;
  final String id;
  final DateTime? since;
  final DateTime? until;

  const InsightsScope._(this.kind, this.id, {this.since, this.until});

  const InsightsScope.project(String id, {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.project, id, since: since, until: until);
  const InsightsScope.team(String id, {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.team, id, since: since, until: until);
  const InsightsScope.teamStewards(String id,
      {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.teamStewards, id,
            since: since, until: until);
  const InsightsScope.agent(String id, {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.agent, id, since: since, until: until);
  const InsightsScope.engine(String id, {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.engine, id, since: since, until: until);
  const InsightsScope.host(String id, {DateTime? since, DateTime? until})
      : this._(InsightsScopeKind.host, id, since: since, until: until);

  bool get isEmpty => id.isEmpty;

  /// Returns a copy with the given time window. Used by InsightsScreen
  /// when the user picks a chip on the time-range row.
  InsightsScope withWindow({DateTime? since, DateTime? until}) =>
      InsightsScope._(kind, id, since: since, until: until);

  @override
  bool operator ==(Object other) =>
      other is InsightsScope &&
      other.kind == kind &&
      other.id == id &&
      other.since == since &&
      other.until == until;

  @override
  int get hashCode => Object.hash(kind, id, since, until);

  @override
  String toString() =>
      'InsightsScope(${kind.name}:$id since=$since until=$until)';
}

/// Snapshot of an Insights response. The mobile panel renders every
/// block off [body], with [staleSince] carrying the snapshot fetch
/// time when we fell back to the offline cache (cache-first per
/// ADR-006).
class InsightsState {
  final Map<String, dynamic>? body;
  final DateTime? staleSince;
  final String? error;

  const InsightsState({
    this.body,
    this.staleSince,
    this.error,
  });
}

/// Family provider keyed by [InsightsScope]. Empty scope resolves to
/// an empty state so callers can `watch` unconditionally without
/// short-circuiting. `autoDispose` so closing a project / agent /
/// host detail screen frees the snapshot.
final insightsProvider =
    FutureProvider.autoDispose.family<InsightsState, InsightsScope>(
  (ref, scope) async {
    if (scope.isEmpty) return const InsightsState();
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const InsightsState();
    try {
      // teamStewards routes through team_id but adds kind=steward;
      // the qualifier is handled by hub_client.dart.
      final isTeamStewards = scope.kind == InsightsScopeKind.teamStewards;
      final res = await client.getInsightsCached(
        projectId: scope.kind == InsightsScopeKind.project ? scope.id : null,
        teamId: scope.kind == InsightsScopeKind.team || isTeamStewards
            ? scope.id
            : null,
        agentId: scope.kind == InsightsScopeKind.agent ? scope.id : null,
        engine: scope.kind == InsightsScopeKind.engine ? scope.id : null,
        hostId: scope.kind == InsightsScopeKind.host ? scope.id : null,
        stewardOnly: isTeamStewards,
        since: scope.since,
        until: scope.until,
      );
      return InsightsState(body: res.body, staleSince: res.staleSince);
    } catch (e) {
      return InsightsState(error: e.toString());
    }
  },
);
