import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/steward_liveness.dart';

void main() {
  final now = DateTime.utc(2026, 4, 25, 12, 0, 0);
  Map<String, dynamic> steward({String status = 'running', String? lastEvent}) =>
      {
        'handle': 'steward',
        'status': status,
        if (lastEvent != null) 'last_event_at': lastEvent,
      };

  group('stewardLiveness', () {
    test('returns none when no steward agent in the list', () {
      expect(
        stewardLiveness([
          {'handle': 'worker', 'status': 'running'},
        ], now: now),
        StewardLiveness.none,
      );
    });

    test('returns starting for pending steward', () {
      expect(
        stewardLiveness([steward(status: 'pending')], now: now),
        StewardLiveness.starting,
      );
    });

    test('returns starting when running steward has no events yet', () {
      expect(
        stewardLiveness([steward(lastEvent: null)], now: now),
        StewardLiveness.starting,
      );
    });

    test('returns healthy for fresh event under 2 min', () {
      final t = now.subtract(const Duration(seconds: 30)).toIso8601String();
      expect(
        stewardLiveness([steward(lastEvent: t)], now: now),
        StewardLiveness.healthy,
      );
    });

    test('returns idle for event aged 2–10 min', () {
      final t = now.subtract(const Duration(minutes: 5)).toIso8601String();
      expect(
        stewardLiveness([steward(lastEvent: t)], now: now),
        StewardLiveness.idle,
      );
    });

    test('returns stuck for event older than 10 min', () {
      final t = now.subtract(const Duration(minutes: 15)).toIso8601String();
      expect(
        stewardLiveness([steward(lastEvent: t)], now: now),
        StewardLiveness.stuck,
      );
    });

    test('returns none for terminated steward', () {
      expect(
        stewardLiveness([steward(status: 'terminated')], now: now),
        StewardLiveness.none,
      );
    });

    test('returns none for failed steward', () {
      expect(
        stewardLiveness([steward(status: 'failed')], now: now),
        StewardLiveness.none,
      );
    });

    test('boundary: exactly 2 min is healthy', () {
      final t = now.subtract(const Duration(minutes: 2)).toIso8601String();
      expect(
        stewardLiveness([steward(lastEvent: t)], now: now),
        StewardLiveness.healthy,
      );
    });

    test('boundary: exactly 10 min is stuck', () {
      final t = now.subtract(const Duration(minutes: 10)).toIso8601String();
      expect(
        stewardLiveness([steward(lastEvent: t)], now: now),
        StewardLiveness.stuck,
      );
    });

    test('unparseable timestamp falls back to idle', () {
      expect(
        stewardLiveness([steward(lastEvent: 'not-a-date')], now: now),
        StewardLiveness.idle,
      );
    });
  });
}
