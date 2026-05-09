import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Team-scoped 24h-vs-prior-7d spend delta for the Me tab Stats card
/// (Phase 2 W3 of insights-phase-2.md). Two `/v1/insights` reads — one
/// for the trailing 24h window, one for the 7 days before that — are
/// folded into a single state object so the card can show both the
/// absolute spend and the Δ% in one glance.
///
/// Hub-side latency is amortized because the 30s response cache covers
/// any view that re-mounts within the window; the prior-7d window is
/// rarely re-fetched outside Me-tab refresh.

class MeSpendDelta {
  /// Today's tokens (in + out) across the team.
  final int todayTokens;

  /// Average daily tokens across the prior 7 days.
  final double prior7dAvgTokens;

  /// Δ% — `null` when prior is zero (cold-start team) so the card can
  /// render "—" instead of a misleading percentage.
  final double? deltaPct;

  /// Cache-fall-back marker — propagated from the underlying hub
  /// readThrough call so the card can show a stale dot without
  /// re-implementing the freshness check.
  final DateTime? staleSince;

  /// First-error message — usually a network failure. Card renders an
  /// inline error pill rather than going blank.
  final String? error;

  const MeSpendDelta({
    this.todayTokens = 0,
    this.prior7dAvgTokens = 0,
    this.deltaPct,
    this.staleSince,
    this.error,
  });

  bool get hasData => todayTokens > 0 || prior7dAvgTokens > 0;
}

/// Family-keyed provider — keyed by team id so a multi-team future
/// (one Me tab spawns one card per team) can share the cached results.
final meTeamSpendDeltaProvider =
    FutureProvider.autoDispose.family<MeSpendDelta, String>(
  (ref, teamId) async {
    if (teamId.isEmpty) return const MeSpendDelta();
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const MeSpendDelta();

    final now = DateTime.now().toUtc();
    final dayAgo = now.subtract(const Duration(hours: 24));
    final weekAgo = now.subtract(const Duration(days: 8));

    try {
      // Two windows. We use the explicit since/until args rather than
      // letting the hub default to 24h, otherwise the second call would
      // also default to 24h (its since arg would be honored, but the
      // default until=now would overlap today's totals).
      final today = await client.getInsightsCached(
        teamId: teamId,
        since: dayAgo,
        until: now,
      );
      final prior = await client.getInsightsCached(
        teamId: teamId,
        since: weekAgo,
        until: dayAgo,
      );

      final todayTokens = _sumTokens(today.body);
      final priorTotal = _sumTokens(prior.body).toDouble();
      final priorAvg = priorTotal / 7.0;
      double? delta;
      if (priorAvg > 0) {
        delta = ((todayTokens - priorAvg) / priorAvg) * 100.0;
      }

      // Stale marker: any of the two reads being cache-only counts as
      // stale, since the user-visible figures depend on both.
      final staleSince = today.staleSince ?? prior.staleSince;

      return MeSpendDelta(
        todayTokens: todayTokens,
        prior7dAvgTokens: priorAvg,
        deltaPct: delta,
        staleSince: staleSince,
      );
    } catch (e) {
      return MeSpendDelta(error: e.toString());
    }
  },
);

int _sumTokens(Map<String, dynamic> body) {
  final spend = body['spend'];
  if (spend is! Map) return 0;
  final s = spend.cast<String, dynamic>();
  int n(Object? v) {
    if (v is int) return v;
    if (v is num) return v.toInt();
    return 0;
  }
  return n(s['tokens_in']) + n(s['tokens_out']);
}
