import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Scope-kind enum for `/v1/insights`. Mirrors hub-side
/// `insights_scope.go` (insights-phase-2 W1). User scope is omitted —
/// the hub rejects it pending per-token user attribution (ADR-005's
/// principal/director model has no user table at MVP).
enum InsightsScopeKind { project, team, agent, engine, host }

/// Value object naming a single (kind, id) scope. `id` is whatever the
/// scope kind takes — project ULID, team handle, agent ULID, engine
/// label (`claude-code`/`gemini-cli`/`codex`), or host ULID.
///
/// Equality + hashCode are required because Riverpod family providers
/// key on the parameter; without these, two `InsightsScope.project(x)`
/// instances would be different cache entries even when conceptually
/// identical.
class InsightsScope {
  final InsightsScopeKind kind;
  final String id;

  const InsightsScope._(this.kind, this.id);

  const InsightsScope.project(String id) : this._(InsightsScopeKind.project, id);
  const InsightsScope.team(String id) : this._(InsightsScopeKind.team, id);
  const InsightsScope.agent(String id) : this._(InsightsScopeKind.agent, id);
  const InsightsScope.engine(String id) : this._(InsightsScopeKind.engine, id);
  const InsightsScope.host(String id) : this._(InsightsScopeKind.host, id);

  bool get isEmpty => id.isEmpty;

  @override
  bool operator ==(Object other) =>
      other is InsightsScope && other.kind == kind && other.id == id;

  @override
  int get hashCode => Object.hash(kind, id);

  @override
  String toString() => 'InsightsScope(${kind.name}:$id)';
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
      final res = await client.getInsightsCached(
        projectId: scope.kind == InsightsScopeKind.project ? scope.id : null,
        teamId: scope.kind == InsightsScopeKind.team ? scope.id : null,
        agentId: scope.kind == InsightsScopeKind.agent ? scope.id : null,
        engine: scope.kind == InsightsScopeKind.engine ? scope.id : null,
        hostId: scope.kind == InsightsScopeKind.host ? scope.id : null,
      );
      return InsightsState(body: res.body, staleSince: res.staleSince);
    } catch (e) {
      return InsightsState(error: e.toString());
    }
  },
);
