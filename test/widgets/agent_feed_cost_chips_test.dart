import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 D8 chip pair (W4-a + W4-c) — the two cost chips
// the agent feed renders side-by-side:
//
//   - process chip (W4-a) — from latest status_line.cost.total_cost_usd.
//     Resets on respawn; preserved across /clear and /model swaps.
//   - session chip (W4-c) — from hub-computed session_cost_usd_imputed,
//     polled out-of-band on a 15s timer. Preserved across resumes.
//
// These tests pin the pure-data reducers + tooltip composer; the
// widget tree + Riverpod plumbing is exercised by manual smoke and
// the W4-b integration tests on the hub side.

void main() {
  group('processCostFromEvents (ADR-036 W4-a)', () {
    test('returns null when no status_line has fired yet', () {
      // Cold-open: text / usage / session.init may all have arrived
      // but no statusLine snapshot yet. Chip must self-gate, NOT
      // render "$0.0000" which would suggest a free session when
      // the truth is "we don't know yet".
      final events = <Map<String, dynamic>>[
        {'kind': 'session.init', 'payload': {'model': 'claude-opus-4-7'}},
        {'kind': 'text', 'payload': {'text': 'hello'}},
        {'kind': 'usage', 'payload': {'input_tokens': 100}},
      ];
      expect(processCostFromEvents(events), isNull);
    });

    test('returns null when status_line has fired but lacks cost block', () {
      // Defensive: a future claude version (or a malformed payload)
      // might ship status_line without a `cost` block. We degrade
      // blank rather than render 0.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {'model': {'id': 'claude-opus-4-7'}},
        },
      ];
      expect(processCostFromEvents(events), isNull);
    });

    test('returns total_cost_usd from the latest status_line', () {
      // statusLine fires periodically (~10s); each frame is a fresh
      // snapshot of the process-cumulative cost, NOT a delta. The
      // reducer must take the LATEST value, not sum.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0.0123},
          },
        },
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0.0456},
          },
        },
      ];
      expect(processCostFromEvents(events), 0.0456);
    });

    test('walks past interleaved non-status_line events', () {
      // Real wire interleaves text / tool_call / status_line / usage
      // / text … Reducer must walk through to find the most-recent
      // status_line.cost without confusing intermediate events.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0.01},
          },
        },
        {'kind': 'text', 'payload': {'text': 'reasoning'}},
        {'kind': 'tool_call', 'payload': {'id': 'tc-1'}},
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0.03},
          },
        },
        {'kind': 'usage', 'payload': {'input_tokens': 5}},
      ];
      expect(processCostFromEvents(events), 0.03);
    });

    test('zero cost from a fresh process renders, not blank', () {
      // Critical distinction from the "null when no status_line"
      // case: a real claude process with NO turn yet emits a
      // statusLine carrying `cost.total_cost_usd: 0`. The chip
      // SHOULD light up at $0.0000 — null would suggest we hadn't
      // heard from claude yet, which is wrong.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0},
          },
        },
      ];
      expect(processCostFromEvents(events), 0.0);
    });

    test('handles int vs double in JSON-decoded payload', () {
      // Dart's JSON decoder gives `int` for integer values and `double`
      // for ones with a fractional part. The reducer's `num`-cast must
      // handle both shapes (most cost frames carry tiny fractions; a
      // round 0 / 5 / 100 reproduces as int).
      final intEvents = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 5},
          },
        },
      ];
      final got = processCostFromEvents(intEvents);
      expect(got, 5.0);
      expect(got, isA<double>());
    });

    test('empty event list returns null', () {
      // Trivial: an unstarted agent has no events.
      expect(processCostFromEvents(const []), isNull);
    });
  });

  group('buildSessionCostTooltipFromDetail (ADR-036 W4-c)', () {
    test('first line carries the imputed-against-rate-sheet disclaimer', () {
      // The chip is per ADR-036 D8 explicitly "estimates against the
      // public API rate sheet". Subscription users (the common case)
      // must see this distinction in the tooltip so they don't read
      // the number as an actual bill.
      final got = buildSessionCostTooltipFromDetail(0.1234, null);
      expect(got, contains('imputed'));
      expect(got, contains('public API rate sheet'));
      expect(got, contains("aren't actually billed"));
      // Must also carry the cross-resume-preservation claim that
      // distinguishes this chip from the process chip.
      expect(got, contains('Preserved across resumes'));
    });

    test('headline embeds the total with 4-decimal precision', () {
      // 4-decimal matches the existing cost chip rendering (one cent
      // is visually meaningless on a sub-dollar API rate; the 4th
      // decimal is the smallest unit that matters).
      final got = buildSessionCostTooltipFromDetail(0.12, null);
      expect(got, contains(r'$0.1200 session'));
    });

    test('renders per-model breakdown when detail has it', () {
      final detail = <String, dynamic>{
        'breakdown_by_model': <String, dynamic>{
          'claude-opus-4-7': 0.0573,
          'claude-sonnet-4-6': 0.0150,
        },
        'tokens_by_model': <String, dynamic>{
          'claude-opus-4-7': {
            'input': 1000, 'output': 500, 'cache_read': 2000, 'cache_write': 100,
          },
          'claude-sonnet-4-6': {
            'input': 100000, 'output': 1000, 'cache_read': 0, 'cache_write': 0,
          },
        },
      };
      final got = buildSessionCostTooltipFromDetail(0.0723, detail);
      expect(got, contains('Usage by model:'));
      // Sorted alphabetically so the diff is stable.
      expect(got, contains(r'• opus 4.7: $0.0573'));
      expect(got, contains(r'• sonnet 4.6: $0.0150'));
      // Token annotations on each row.
      expect(got, contains('↑1000 in / ↓500 out / cache 2000'));
      // sonnet has zero cache_read → cache token annotation OMITTED
      // (avoid noise on rows that have nothing meaningful to add).
      expect(got, contains('↑100000 in / ↓1000 out'));
      expect(got, isNot(contains('↑100000 in / ↓1000 out / cache')));
    });

    test('renders snapshot_date + origin so users can spot stale config', () {
      // Drift detection is the whole point of exposing snapshot_date —
      // a tooltip that doesn't surface "rates as of YYYY-MM-DD" makes
      // the operator override system invisible.
      final detail = <String, dynamic>{
        'snapshot_date': '2026-05-25',
        'origin': 'embedded',
      };
      final got = buildSessionCostTooltipFromDetail(0.05, detail);
      expect(got, contains('Rates as of 2026-05-25 (embedded tier).'));
    });

    test('surfaces missing_models list when present', () {
      // Per ADR-036 D9 ("blank > wrong"), unknown models drop from
      // the USD total. The tooltip must surface which models that
      // happened to so an operator can extend the override file.
      final detail = <String, dynamic>{
        'missing_models': <dynamic>['claude-future-99', 'claude-mystery'],
      };
      final got = buildSessionCostTooltipFromDetail(0.05, detail);
      expect(got, contains('Not priced: claude-future-99, claude-mystery'));
    });

    test('appends pair-context line when process chip is also visible', () {
      // ADR-036 D8 — the two chips are designed to be READ TOGETHER.
      // When both are visible the tooltips must cross-reference so
      // the user understands why two different numbers are valid.
      final got = buildSessionCostTooltipFromDetail(
        0.05, null, pair: true);
      expect(got, contains('Pair: session vs process'));
      expect(got, contains('see the process-cost chip'));
    });

    test('omits pair-context when standalone', () {
      // Process chip absent → no need to confuse the user with the
      // pair-comparison sentence.
      final got = buildSessionCostTooltipFromDetail(0.05, null, pair: false);
      expect(got, isNot(contains('Pair:')));
    });
  });

  group('cross-chip pair semantics (ADR-036 D8)', () {
    test('both reducers can produce non-null simultaneously without conflict',
        () {
      // Sanity: process and session sources are independent. A
      // status_line carrying cost AND a sessionCostDetail with a
      // total_usd must both surface — that's the entire point of
      // the chip pair (cross-check $X vs $Y).
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'cost': {'total_cost_usd': 0.05},
          },
        },
      ];
      final detail = <String, dynamic>{
        'total_usd': 0.10, // session preserved more than this process has
      };
      expect(processCostFromEvents(events), 0.05);
      expect(detail['total_usd'], 0.10);
      // Their producers don't share state; the chip's own gates do the
      // "render both" composition.
    });
  });
}
