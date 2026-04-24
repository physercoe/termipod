import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite/sqflite.dart';
import 'package:sqflite_common_ffi/sqflite_common_ffi.dart';
import 'package:termipod/services/hub/hub_snapshot_cache.dart';

void main() {
  setUpAll(() {
    sqfliteFfiInit();
  });

  late HubSnapshotCache cache;

  setUp(() {
    cache = HubSnapshotCache(
      dbPath: inMemoryDatabasePath,
      dbFactory: databaseFactoryFfi,
    );
  });

  tearDown(() async {
    await cache.close();
  });

  const hub = 'https://hub.example#team-a';
  const otherHub = 'https://hub.example#team-b';

  group('HubSnapshotCache', () {
    test('returns null for missing endpoint', () async {
      expect(await cache.get(hub, '/v1/nope'), isNull);
    });

    test('put then get round-trips body + fetchedAt', () async {
      final body = <String, Object?>{
        'projects': [
          <String, Object?>{'id': 'p1', 'name': 'Alpha'},
        ],
      };
      await cache.put(hub, '/v1/projects', body);
      final snap = await cache.get(hub, '/v1/projects');
      expect(snap, isNotNull);
      expect(snap!.body, equals(body));
      final age = DateTime.now().difference(snap.fetchedAt);
      expect(age, lessThan(const Duration(seconds: 5)));
    });

    test('put overwrites existing row with a fresh fetchedAt', () async {
      await cache.put(hub, '/v1/projects', <String, Object?>{'v': 1});
      final first = await cache.get(hub, '/v1/projects');
      await Future<void>.delayed(const Duration(milliseconds: 5));
      await cache.put(hub, '/v1/projects', <String, Object?>{'v': 2});
      final second = await cache.get(hub, '/v1/projects');
      expect(second!.body, equals(<String, Object?>{'v': 2}));
      expect(second.fetchedAt.isAfter(first!.fetchedAt), isTrue);
    });

    test('scopes rows by hubKey', () async {
      await cache.put(hub, '/v1/projects', <String, Object?>{'team': 'a'});
      await cache.put(
          otherHub, '/v1/projects', <String, Object?>{'team': 'b'});
      expect((await cache.get(hub, '/v1/projects'))!.body,
          equals(<String, Object?>{'team': 'a'}));
      expect((await cache.get(otherHub, '/v1/projects'))!.body,
          equals(<String, Object?>{'team': 'b'}));
    });

    test('invalidatePrefix drops only matching endpoints', () async {
      await cache.put(
          hub, '/v1/teams/t/projects/p/runs', <String, Object?>{'x': 1});
      await cache.put(hub, '/v1/teams/t/projects/p/runs?status=running',
          <String, Object?>{'x': 2});
      await cache.put(
          hub, '/v1/teams/t/projects/p/reviews', <String, Object?>{'x': 3});
      await cache.invalidatePrefix(hub, '/v1/teams/t/projects/p/runs');
      expect(await cache.get(hub, '/v1/teams/t/projects/p/runs'), isNull);
      expect(
        await cache.get(hub, '/v1/teams/t/projects/p/runs?status=running'),
        isNull,
      );
      expect(await cache.get(hub, '/v1/teams/t/projects/p/reviews'),
          isNotNull);
    });

    test('wipeHub clears only the target partition', () async {
      await cache.put(hub, '/v1/projects', <String, Object?>{'x': 1});
      await cache.put(otherHub, '/v1/projects', <String, Object?>{'x': 2});
      await cache.wipeHub(hub);
      expect(await cache.get(hub, '/v1/projects'), isNull);
      expect(await cache.get(otherHub, '/v1/projects'), isNotNull);
    });

    test('TTL-expired rows are dropped on read', () async {
      final tight = HubSnapshotCache(
        dbPath: inMemoryDatabasePath,
        dbFactory: databaseFactoryFfi,
        ttl: Duration.zero,
      );
      try {
        await tight.put(hub, '/v1/projects', <String, Object?>{'x': 1});
        await Future<void>.delayed(const Duration(milliseconds: 2));
        expect(await tight.get(hub, '/v1/projects'), isNull);
      } finally {
        await tight.close();
      }
    });

    test('LRU eviction drops oldest entries past maxRowsPerHub', () async {
      final tight = HubSnapshotCache(
        dbPath: inMemoryDatabasePath,
        dbFactory: databaseFactoryFfi,
        maxRowsPerHub: 3,
      );
      try {
        await tight.put(hub, '/a', <String, Object?>{});
        await Future<void>.delayed(const Duration(milliseconds: 5));
        await tight.put(hub, '/b', <String, Object?>{});
        await Future<void>.delayed(const Duration(milliseconds: 5));
        await tight.put(hub, '/c', <String, Object?>{});
        expect(await tight.countFor(hub), equals(3));
        await Future<void>.delayed(const Duration(milliseconds: 5));
        await tight.put(hub, '/d', <String, Object?>{});
        expect(await tight.countFor(hub), equals(3));
        expect(await tight.get(hub, '/a'), isNull);
        expect(await tight.get(hub, '/d'), isNotNull);
      } finally {
        await tight.close();
      }
    });

    test('hubCacheKey combines baseUrl and teamId', () {
      expect(
        hubCacheKey(baseUrl: 'https://x.example', teamId: 't1'),
        equals('https://x.example#t1'),
      );
    });
  });
}
