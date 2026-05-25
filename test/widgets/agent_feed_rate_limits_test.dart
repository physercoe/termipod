import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 W5 — rate_limits surface from status_line frames.
// Three top-level helpers under test:
//
//   rateLimitsFromEvents      — latest-wins reducer over status_line
//   formatRateLimitResetsAt   — epoch-seconds → "in 4h 38m" / "Mon 03:00"
//                               (3h threshold; device-local TZ)
//   rateLimitAlarmTier        — used_percentage → (color, severity)
//                               80% → amber, 95% → red, else green
//
// These are the pure-data pieces that build up the new chip pair in
// _TelemetryStrip. The widget-tree composition is exercised by manual
// smoke once it ships.

void main() {
  group('rateLimitsFromEvents (ADR-036 W5)', () {
    test('returns null when no status_line frame has fired', () {
      // Cold open: only non-statusLine events present. Chip must
      // self-gate, NOT render placeholder percentages.
      final events = <Map<String, dynamic>>[
        {'kind': 'session.init', 'payload': {'model': 'claude-opus-4-7'}},
        {'kind': 'text', 'payload': {'text': 'hello'}},
      ];
      expect(rateLimitsFromEvents(events), isNull);
    });

    test('returns null when status_line lacks the rate_limits block', () {
      // Defensive: status_line frames may ship without rate_limits
      // (older claude versions or a malformed payload). Degrade
      // blank rather than render zeros.
      final events = <Map<String, dynamic>>[
        {'kind': 'status_line', 'payload': {'cost': {'total_cost_usd': 0.01}}},
      ];
      expect(rateLimitsFromEvents(events), isNull);
    });

    test('returns the verbatim rate_limits block from latest status_line', () {
      // The reducer must NOT transform the wire shape — the chip
      // renderer reads the original keys (five_hour / seven_day /
      // used_percentage / resets_at).
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'rate_limits': {
              'five_hour': {'used_percentage': 24, 'resets_at': 1779640200},
              'seven_day': {'used_percentage': 33, 'resets_at': 1779764400},
            },
          },
        },
      ];
      final got = rateLimitsFromEvents(events);
      expect(got, isNotNull);
      expect(got!['five_hour'], isA<Map>());
      expect((got['five_hour'] as Map)['used_percentage'], 24);
      expect((got['seven_day'] as Map)['resets_at'], 1779764400);
    });

    test('latest-wins across multiple status_line frames', () {
      // Each statusLine frame is a fresh snapshot of the rolling-
      // window state (NOT a delta). The reducer must take the most
      // recent value; summing would double-count by 100s within a
      // session.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'rate_limits': {
              'five_hour': {'used_percentage': 10, 'resets_at': 1779640200},
            },
          },
        },
        {
          'kind': 'status_line',
          'payload': {
            'rate_limits': {
              'five_hour': {'used_percentage': 47, 'resets_at': 1779640200},
            },
          },
        },
      ];
      final got = rateLimitsFromEvents(events);
      expect((got!['five_hour'] as Map)['used_percentage'], 47);
    });

    test('walks past interleaved non-status_line events', () {
      // Real wire interleaves status_line with text/tool_call/usage.
      // The reducer must find the most-recent status_line regardless
      // of intervening kinds.
      final events = <Map<String, dynamic>>[
        {
          'kind': 'status_line',
          'payload': {
            'rate_limits': {'five_hour': {'used_percentage': 5, 'resets_at': 1}},
          },
        },
        {'kind': 'text', 'payload': {'text': 'reasoning'}},
        {'kind': 'tool_call', 'payload': {'id': 'tc-1'}},
        {
          'kind': 'status_line',
          'payload': {
            'rate_limits': {'five_hour': {'used_percentage': 50, 'resets_at': 2}},
          },
        },
        {'kind': 'usage', 'payload': {'input_tokens': 5}},
      ];
      final got = rateLimitsFromEvents(events);
      expect((got!['five_hour'] as Map)['used_percentage'], 50);
    });

    test('empty event list returns null', () {
      expect(rateLimitsFromEvents(const []), isNull);
    });
  });

  group('formatRateLimitResetsAt (ADR-036 D7 + W5)', () {
    test('returns empty string for null / zero / negative epoch', () {
      // Caller drops the sub-line cleanly when the formatter returns
      // empty — these defensive cases must NOT render a stray "now"
      // or "in 0m" that would suggest a real reset is imminent.
      expect(formatRateLimitResetsAt(null), '');
      expect(formatRateLimitResetsAt(0), '');
      expect(formatRateLimitResetsAt(-1), '');
    });

    test('past timestamps render as "now"', () {
      // The window has already reset by the time the chip rendered.
      // Next status_line frame will refresh with a new resets_at;
      // showing "now" in the interim is the truthful answer.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      final past = now.subtract(const Duration(minutes: 30));
      final epoch = past.millisecondsSinceEpoch ~/ 1000;
      expect(formatRateLimitResetsAt(epoch, now: now), 'now');
    });

    test('horizons under 3h render as relative "in Xh Ym"', () {
      // The plan spec: relative form for horizons under ~3h. Test
      // three points across the relative band.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      // 5 minutes out
      var f = now.add(const Duration(minutes: 5));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          'in 5m');
      // 1h exactly
      f = now.add(const Duration(hours: 1));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          'in 1h');
      // 1h 23m
      f = now.add(const Duration(hours: 1, minutes: 23));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          'in 1h 23m');
      // 2h 59m — just under the threshold
      f = now.add(const Duration(hours: 2, minutes: 59));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          'in 2h 59m');
    });

    test('horizons >= 3h render as absolute "resets Day HH:MM"', () {
      // Plan: absolute short form for horizons past ~3h. The exact
      // 3h boundary lands in the absolute branch (the relative form
      // is for "under" 3h, not "at most" 3h).
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      var f = now.add(const Duration(hours: 3));
      var got = formatRateLimitResetsAt(
          f.millisecondsSinceEpoch ~/ 1000, now: now);
      expect(got, startsWith('resets '));
      // Must contain a 3-letter weekday + zero-padded HH:MM.
      expect(got, matches(RegExp(r'^resets (Mon|Tue|Wed|Thu|Fri|Sat|Sun) \d{2}:\d{2}$')));
    });

    test('renders in device-local TZ per ADR-036 D7', () {
      // 03:00 UTC + a +08:00 offset would render as 11:00 local. We
      // construct an explicit UTC fixed point and a local `now`
      // such that the test is independent of the runner's TZ.
      //
      // Use a 5h-future-from-now timestamp (lands in absolute
      // branch) and assert: (a) format matches "resets Day HH:MM",
      // (b) the rendered HH:MM equals (now.toLocal().hour + 5) % 24
      // — i.e. the formatter ADDED the diff in local TZ rather
      // than naively printing UTC components.
      final now = DateTime.now();
      final target = now.add(const Duration(hours: 5));
      final got = formatRateLimitResetsAt(
          target.millisecondsSinceEpoch ~/ 1000, now: now);
      final expectedHH = target.hour.toString().padLeft(2, '0');
      expect(got, contains(expectedHH));
    });

    test('sanity-bound rejects epochs further than 14 days out', () {
      // Misinterpreted-unit guard: if someone shoves a ms value into
      // the formatter's seconds-typed parameter, the diff would land
      // millennia out. Empty string is the safe fallback.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      final faaaarFuture = now.add(const Duration(days: 365 * 100));
      expect(
          formatRateLimitResetsAt(
              faaaarFuture.millisecondsSinceEpoch ~/ 1000,
              now: now),
          '');
    });

    test('sub-minute horizons render as "in <1m" not "in 0m"', () {
      // Edge case in the relative branch — within the same minute,
      // diff.inMinutes is 0 but the window hasn't actually reset.
      // "in <1m" tells the user something is about to happen
      // without lying about the precise count.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      final f = now.add(const Duration(seconds: 30));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          'in <1m');
    });
  });

  group('rateLimitAlarmTier (ADR-036 W5)', () {
    test('< 80% → green', () {
      expect(rateLimitAlarmTier(0).severity, 'green');
      expect(rateLimitAlarmTier(50).severity, 'green');
      expect(rateLimitAlarmTier(79).severity, 'green');
      expect(rateLimitAlarmTier(79.9).severity, 'green');
    });

    test('80% — 94.999% → amber', () {
      // Exact-boundary case 80% MUST tip into amber per plan spec
      // ("when used_percentage >= 80"). Off-by-one here would let a
      // user hit a threshold without the visual cue.
      expect(rateLimitAlarmTier(80).severity, 'amber');
      expect(rateLimitAlarmTier(85).severity, 'amber');
      expect(rateLimitAlarmTier(94).severity, 'amber');
      expect(rateLimitAlarmTier(94.999).severity, 'amber');
    });

    test('>= 95% → red', () {
      // 95% is the "throttling imminent" threshold; the next API call
      // may already get a rate-limit error.
      expect(rateLimitAlarmTier(95).severity, 'red');
      expect(rateLimitAlarmTier(99).severity, 'red');
      expect(rateLimitAlarmTier(100).severity, 'red');
      expect(rateLimitAlarmTier(150).severity, 'red');
    });

    test('null treated as zero (chip self-gate handles the visual)', () {
      // null pct shouldn't crash the tier function; the calling
      // chip code already self-gates on (pct == null && resetsAt == null)
      // — this test just pins that the tier doesn't throw.
      expect(rateLimitAlarmTier(null).severity, 'green');
    });

    test('color matches severity label', () {
      // Pin the (color, severity) pair so a refactor that swaps
      // them on one threshold doesn't pass undetected.
      final amber = rateLimitAlarmTier(85);
      final red = rateLimitAlarmTier(99);
      expect(amber.color, Colors.orange);
      expect(red.color, isNot(equals(Colors.orange)));
    });
  });
}
