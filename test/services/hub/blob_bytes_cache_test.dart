import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/hub/blob_bytes_cache.dart';

void main() {
  late Directory tmp;

  setUp(() async {
    tmp = await Directory.systemTemp.createTemp('blob_bytes_cache_test');
  });

  tearDown(() async {
    if (await tmp.exists()) {
      await tmp.delete(recursive: true);
    }
  });

  Uint8List bytes(int len, [int fill = 0xAA]) =>
      Uint8List.fromList(List<int>.filled(len, fill));

  test('get returns null for a miss without creating files', () async {
    final cache = BlobBytesCache(rootDir: '${tmp.path}/blobs');
    expect(await cache.get('deadbeef'), isNull);
    // Cache dir is created lazily by put, not get.
    expect(await Directory('${tmp.path}/blobs').exists(), isFalse);
  });

  test('put then get round-trips bytes verbatim', () async {
    final cache = BlobBytesCache(rootDir: '${tmp.path}/blobs');
    final payload = bytes(256, 0x42);
    await cache.put('sha1', payload);
    final got = await cache.get('sha1');
    expect(got, isNotNull);
    expect(got!.length, 256);
    expect(got.first, 0x42);
    expect(got.last, 0x42);
  });

  test('empty sha is a no-op on both put and get', () async {
    final cache = BlobBytesCache(rootDir: '${tmp.path}/blobs');
    await cache.put('', bytes(10));
    expect(await cache.get(''), isNull);
    expect(await cache.totalBytes(), 0);
  });

  test('wipeAll removes every file and reports the count', () async {
    final cache = BlobBytesCache(rootDir: '${tmp.path}/blobs');
    await cache.put('a', bytes(10));
    await cache.put('b', bytes(10));
    await cache.put('c', bytes(10));
    final removed = await cache.wipeAll();
    expect(removed, 3);
    expect(await cache.get('a'), isNull);
    expect(await cache.get('b'), isNull);
    expect(await cache.get('c'), isNull);
  });

  test('totalBytes sums file sizes across entries', () async {
    final cache = BlobBytesCache(rootDir: '${tmp.path}/blobs');
    await cache.put('a', bytes(100));
    await cache.put('b', bytes(200));
    expect(await cache.totalBytes(), 300);
  });

  test('LRU eviction drops oldest entries when over budget', () async {
    // 3 × 100-byte payloads with a 250-byte cap leaves room for 2.
    final cache =
        BlobBytesCache(rootDir: '${tmp.path}/blobs', maxBytes: 250);
    await cache.put('a', bytes(100));
    // Force distinct mtimes so the eviction ordering is deterministic;
    // some filesystems have 1s timestamp resolution.
    await Future.delayed(const Duration(milliseconds: 1100));
    await cache.put('b', bytes(100));
    await Future.delayed(const Duration(milliseconds: 1100));
    await cache.put('c', bytes(100));

    // 'a' is the oldest and should have been evicted.
    expect(await cache.get('a'), isNull);
    expect(await cache.get('b'), isNotNull);
    expect(await cache.get('c'), isNotNull);
  });
}
