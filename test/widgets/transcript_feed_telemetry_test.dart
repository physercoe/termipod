// Unit tests for FeedTelemetry — the LiveFeed-only telemetry rollup extracted
// from `_AgentFeedState.build()` (ADR-040 P1b). Pure: drives the fold with
// canned events + a session-cost map and pins the strip's inputs without a
// widget tree.

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/feed_telemetry.dart';

Map<String, dynamic> _ev(String kind, Map<String, dynamic> payload) =>
    {'kind': kind, 'payload': payload};

void main() {
  test('empty events + no session cost → hasTelemetry false', () {
    final t = FeedTelemetry.fromEvents(const [], null);
    expect(t.hasTelemetry, false);
    expect(t.totalCostUsd, 0.0);
    expect(t.turnCount, 0);
    expect(t.modelTotals, isEmpty);
    expect(t.latestContextWindow, isNull);
  });

  test('turn.result sums cost, counts turns, aggregates by_model', () {
    final t = FeedTelemetry.fromEvents([
      _ev('turn.result', {
        'cost_usd': 0.10,
        'by_model': {
          'opus': {'input': 100, 'output': 50},
        },
      }),
      _ev('turn.result', {
        'cost_usd': 0.05,
        'by_model': {
          'opus': {'input': 20, 'output': 10},
          'haiku': {'input': 5, 'output': 2},
        },
      }),
    ], null);
    expect(t.turnCount, 2);
    expect(t.totalCostUsd, closeTo(0.15, 1e-9));
    expect(t.modelTotals.keys, containsAll(<String>['opus', 'haiku']));
    expect(t.hasTelemetry, true);
  });

  test('codex cumulative usage sets context window + last-turn used', () {
    final t = FeedTelemetry.fromEvents([
      _ev('usage', {
        'cumulative': true,
        'engine': 'codex',
        'context_window': 258000,
        'last_total_tokens': 19000,
        'total_tokens': 169000, // cumulative — must NOT win over last_*
        'input_tokens': 1000,
        'output_tokens': 500,
      }),
    ], null);
    expect(t.latestContextWindow, 258000);
    expect(t.latestContextUsed, 19000); // last-turn, not the inflated cumulative
    expect(t.modelTotals.containsKey('codex'), true); // cumulative bucket key
    expect(t.hasTelemetry, true);
  });

  test('claude-code per-message usage drives context-used sum', () {
    final t = FeedTelemetry.fromEvents([
      _ev('usage', {
        'input_tokens': 1000,
        'cache_read': 4000,
        'cache_create': 200,
        'output_tokens': 300,
        'model': 'claude-opus',
        'context_window': 200000,
      }),
    ], null);
    expect(t.latestContextWindow, 200000);
    expect(t.latestContextUsed, 1000 + 4000 + 200); // per-message sum
    expect(t.modelTotals.containsKey('claude-opus'), true);
  });

  test('session-cost poll feeds imputed cost + flips hasTelemetry', () {
    final t = FeedTelemetry.fromEvents(const [], {
      'total_usd': 1.23,
      'tokens_by_model': {'opus': 1},
    });
    expect(t.sessionCostUsdImputed, closeTo(1.23, 1e-9));
    expect(t.hasTelemetry, true);
  });

  test('zero session cost with empty token map does not impute', () {
    final t = FeedTelemetry.fromEvents(const [], {
      'total_usd': 0,
      'tokens_by_model': {},
    });
    expect(t.sessionCostUsdImputed, isNull);
    expect(t.hasTelemetry, false);
  });
}
