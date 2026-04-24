import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';
import 'package:termipod/services/hub/hub_client.dart' show HubApiError;
import 'package:termipod/services/hub/hub_read_through.dart';
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
  const endpoint = '/v1/teams/team-a/runs';

  List<Map<String, dynamic>> decodeListMaps(Object body) => [
        for (final r in body as List) (r as Map).cast<String, dynamic>(),
      ];

  group('readThrough', () {
    test('fresh path returns body with null staleSince and writes cache',
        () async {
      final fresh = [
        <String, dynamic>{'id': 'r1'},
      ];
      final result = await readThrough<List<Map<String, dynamic>>>(
        cache: cache,
        hubKey: hub,
        endpoint: endpoint,
        fetch: () async => fresh,
        decode: decodeListMaps,
      );
      expect(result.body, equals(fresh));
      expect(result.staleSince, isNull);
      expect(result.isStale, isFalse);
      final snap = await cache.get(hub, endpoint);
      expect(snap, isNotNull);
      expect(snap!.body, equals(fresh));
    });

    test('SocketException falls back to cached snapshot with staleSince',
        () async {
      final seed = [
        <String, dynamic>{'id': 'r1', 'v': 1},
      ];
      await cache.put(hub, endpoint, seed);
      final result = await readThrough<List<Map<String, dynamic>>>(
        cache: cache,
        hubKey: hub,
        endpoint: endpoint,
        fetch: () async => throw const SocketException('offline'),
        decode: decodeListMaps,
      );
      expect(result.body, equals(seed));
      expect(result.staleSince, isNotNull);
      expect(result.isStale, isTrue);
    });

    test('TimeoutException falls back to cached snapshot', () async {
      await cache.put(hub, endpoint, <String, dynamic>{'ok': true});
      final result = await readThrough<Map<String, dynamic>>(
        cache: cache,
        hubKey: hub,
        endpoint: endpoint,
        fetch: () async => throw TimeoutException('slow'),
        decode: (b) => (b as Map).cast<String, dynamic>(),
      );
      expect(result.body, equals(<String, dynamic>{'ok': true}));
      expect(result.isStale, isTrue);
    });

    test('HubApiError 500 falls back to cached snapshot', () async {
      final seed = [
        <String, dynamic>{'id': 'r1'},
      ];
      await cache.put(hub, endpoint, seed);
      final result = await readThrough<List<Map<String, dynamic>>>(
        cache: cache,
        hubKey: hub,
        endpoint: endpoint,
        fetch: () async => throw HubApiError(503, 'upstream down'),
        decode: decodeListMaps,
      );
      expect(result.body, equals(seed));
      expect(result.isStale, isTrue);
    });

    test('HubApiError 404 rethrows without consulting cache', () async {
      await cache.put(hub, endpoint, [
        <String, dynamic>{'id': 'stale'},
      ]);
      await expectLater(
        readThrough<List<Map<String, dynamic>>>(
          cache: cache,
          hubKey: hub,
          endpoint: endpoint,
          fetch: () async => throw HubApiError(404, 'not found'),
          decode: decodeListMaps,
        ),
        throwsA(isA<HubApiError>()),
      );
    });

    test('offline failure with empty cache rethrows the transport error',
        () async {
      await expectLater(
        readThrough<List<Map<String, dynamic>>>(
          cache: cache,
          hubKey: hub,
          endpoint: endpoint,
          fetch: () async => throw const SocketException('no net'),
          decode: decodeListMaps,
        ),
        throwsA(isA<SocketException>()),
      );
    });

    test('null cache makes readThrough a pass-through on success', () async {
      final fresh = [
        <String, dynamic>{'id': 'r1'},
      ];
      final result = await readThrough<List<Map<String, dynamic>>>(
        cache: null,
        hubKey: hub,
        endpoint: endpoint,
        fetch: () async => fresh,
        decode: decodeListMaps,
      );
      expect(result.body, equals(fresh));
      expect(result.isStale, isFalse);
    });

    test('null cache rethrows on offline failure', () async {
      await expectLater(
        readThrough<List<Map<String, dynamic>>>(
          cache: null,
          hubKey: hub,
          endpoint: endpoint,
          fetch: () async => throw const SocketException('offline'),
          decode: decodeListMaps,
        ),
        throwsA(isA<SocketException>()),
      );
    });
  });

  group('buildEndpointKey', () {
    test('returns path alone when query is null', () {
      expect(buildEndpointKey('/v1/projects'), equals('/v1/projects'));
    });

    test('returns path alone when query is empty', () {
      expect(
        buildEndpointKey('/v1/projects', const <String, String>{}),
        equals('/v1/projects'),
      );
    });

    test('sorts query params for stable key', () {
      final k1 = buildEndpointKey('/v1/runs', {'b': '2', 'a': '1'});
      final k2 = buildEndpointKey('/v1/runs', {'a': '1', 'b': '2'});
      expect(k1, equals(k2));
      expect(k1, equals('/v1/runs?a=1&b=2'));
    });
  });
}
