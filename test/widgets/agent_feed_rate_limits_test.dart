import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 W5 — rate_limits surface from status_line frames.
// Four top-level helpers under test:
//
//   rateLimitsFromEvents              — latest-wins reducer over status_line
//   formatRateLimitResetsAt           — epoch-seconds → "43m" / "3h43m" /
//                                       "3d19h" compact countdown
//                                       (v1.0.704 polish; device-local TZ)
//   formatRateLimitResetsAtAbsolute   — epoch-seconds → "Mon 03:00" for
//                                       the long-press tooltip
//   rateLimitAlarmTier                — used_percentage → (color, severity)
//                                       80% → amber, 95% → red, else green
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

  group('formatRateLimitResetsAt (ADR-036 D7 + W5 + v1.0.704 polish)', () {
    test('returns empty string for null / zero / negative epoch', () {
      // Caller drops the sub-line cleanly when the formatter returns
      // empty — these defensive cases must NOT render a stray "now"
      // or "0m" that would suggest a real reset is imminent.
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

    test('sub-minute horizons render as "<1m"', () {
      // Edge case in the minutes branch — within the same minute,
      // diff.inMinutes is 0 but the window hasn't actually reset.
      // "<1m" tells the user something is about to happen without
      // lying about the precise count. No "in" prefix (v1.0.704).
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      final f = now.add(const Duration(seconds: 30));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '<1m');
    });

    test('horizons under 1h render as compact "Xm" (no prefix)', () {
      // Plain minutes — single chip-cell unit, no whitespace, no "in".
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      var f = now.add(const Duration(minutes: 5));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '5m');
      f = now.add(const Duration(minutes: 43));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '43m');
      f = now.add(const Duration(minutes: 59));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '59m');
    });

    test('horizons under 1d render as compact "XhYm" / "Xh" (no prefix)', () {
      // Mixed hours+minutes. Exact-hour values drop the minutes part
      // — "1h" not "1h0m" — to honour the visual budget on narrow
      // strips. The v1.0.704 polish removed the prior "in"/"resets"
      // prefix; the chip label is the noun, the sub-line is the
      // pure countdown.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      // 1h exactly
      var f = now.add(const Duration(hours: 1));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '1h');
      // 1h 23m
      f = now.add(const Duration(hours: 1, minutes: 23));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '1h23m');
      // 3h 43m — the user's worked example
      f = now.add(const Duration(hours: 3, minutes: 43));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '3h43m');
      // 23h 59m — just under the day boundary
      f = now.add(const Duration(hours: 23, minutes: 59));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '23h59m');
    });

    test('horizons >= 1d render as compact "XdYh" / "Xd" (no prefix)', () {
      // Days+hours — the 7d-rolling chip is the main consumer. Same
      // shortening discipline as the hours branch: exact-day drops the
      // hours suffix. Minutes are dropped entirely past the 1d
      // boundary — the next-finer unit is signal enough at that scale.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      // 1d exactly
      var f = now.add(const Duration(days: 1));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '1d');
      // 3d 19h — the user's worked example
      f = now.add(const Duration(days: 3, hours: 19));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '3d19h');
      // 6d 23h — close to a full week
      f = now.add(const Duration(days: 6, hours: 23));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '6d23h');
      // 14d exactly — the sanity-bound boundary (14d itself still
      // valid; >14d rejected).
      f = now.add(const Duration(days: 14));
      expect(
          formatRateLimitResetsAt(f.millisecondsSinceEpoch ~/ 1000, now: now),
          '14d');
    });

    test('never emits "in " or "resets " prefix (v1.0.704 contract)', () {
      // Regression-pin: the compact contract is "no prefix on the
      // sub-line". A returning prefix would break the width budget +
      // the chip-label/sub-line semantic split.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      for (final d in <Duration>[
        const Duration(minutes: 1),
        const Duration(hours: 2, minutes: 30),
        const Duration(days: 3, hours: 19),
        const Duration(days: 13, hours: 23),
      ]) {
        final f = now.add(d);
        final got = formatRateLimitResetsAt(
            f.millisecondsSinceEpoch ~/ 1000, now: now);
        expect(got, isNot(startsWith('in ')),
            reason: 'compact form must not carry an "in" prefix');
        expect(got, isNot(startsWith('resets ')),
            reason: 'compact form must not carry a "resets" prefix');
      }
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
  });

  group('formatRateLimitResetsAtAbsolute (v1.0.704 polish)', () {
    test('returns "Day HH:MM" for valid future timestamps', () {
      // The companion absolute formatter is what the tooltip splices
      // into the long-press detail. Same defensive inputs as the
      // compact formatter so the tooltip composer doesn't gate twice.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      final f = now.add(const Duration(hours: 5));
      final got = formatRateLimitResetsAtAbsolute(
          f.millisecondsSinceEpoch ~/ 1000, now: now);
      expect(got, matches(RegExp(r'^(Mon|Tue|Wed|Thu|Fri|Sat|Sun) \d{2}:\d{2}$')));
    });

    test('renders in device-local TZ (mirrors compact-form contract)', () {
      // 5h-future-from-now timestamp; the absolute HH equals
      // (now.local.hour + 5) % 24 because the formatter ADDS the
      // diff in local TZ rather than naively printing UTC components.
      final now = DateTime.now();
      final target = now.add(const Duration(hours: 5));
      final got = formatRateLimitResetsAtAbsolute(
          target.millisecondsSinceEpoch ~/ 1000, now: now);
      final expectedHH = target.hour.toString().padLeft(2, '0');
      expect(got, contains(expectedHH));
    });

    test('returns empty for null / zero / past / >14d defensives', () {
      // Same gate as the compact formatter — the tooltip composer
      // can splice with `${absolute.isEmpty ? '' : ' (\$absolute)'}`
      // and not have to repeat any of the checks.
      final now = DateTime.utc(2026, 5, 25, 12, 0, 0);
      expect(formatRateLimitResetsAtAbsolute(null), '');
      expect(formatRateLimitResetsAtAbsolute(0), '');
      final past = now.subtract(const Duration(minutes: 30));
      expect(
          formatRateLimitResetsAtAbsolute(
              past.millisecondsSinceEpoch ~/ 1000,
              now: now),
          '');
      final faaaarFuture = now.add(const Duration(days: 365 * 100));
      expect(
          formatRateLimitResetsAtAbsolute(
              faaaarFuture.millisecondsSinceEpoch ~/ 1000,
              now: now),
          '');
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
