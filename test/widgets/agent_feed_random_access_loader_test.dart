// Unit tests for RandomAccessLoader — the dense `session_ordinal` keyset fetch
// logic extracted from `_AgentFeedState` (#1049) and re-keyed onto the session
// ordinal (ADR-042 P4). Pure: drives the loader with a canned fetcher that
// records the cursor params, so the keyset contract (anchor-inclusive
// `afterOrdinal: ordinal - 1`, half-page head/tail detection, ascending merge
// of `older.reversed + newer`) is pinned directly instead of only through a
// widget.

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/random_access_loader.dart';

/// One recorded call to the fetcher.
class _Call {
  _Call({this.beforeOrdinal, this.afterOrdinal, required this.limit});
  final int? beforeOrdinal;
  final int? afterOrdinal;
  final int limit;
}

/// Builds a fetcher that returns successive canned pages and records each call.
({AgentEventsFetcher fetch, List<_Call> calls}) fakeFetcher(
  List<List<Map<String, dynamic>>> pages,
) {
  final calls = <_Call>[];
  var i = 0;
  Future<List<Map<String, dynamic>>> fetch({
    int? beforeOrdinal,
    int? afterOrdinal,
    required int limit,
  }) async {
    calls.add(_Call(
      beforeOrdinal: beforeOrdinal,
      afterOrdinal: afterOrdinal,
      limit: limit,
    ));
    final page = i < pages.length ? pages[i] : <Map<String, dynamic>>[];
    i++;
    return page;
  }

  return (fetch: fetch, calls: calls);
}

// A row carries the session_ordinal as the canonical coordinate (ADR-042).
Map<String, dynamic> ev(int ord) =>
    {'seq': ord, 'session_ordinal': ord, 'ts': 't$ord', 'id': 'e$ord'};

void main() {
  group('RandomAccessLoader.fetchAround', () {
    test('uses the ordinal cursors: before=ordinal, after=ordinal-1, half limit',
        () async {
      final f = fakeFetcher([
        [ev(40), ev(30)], // older (DESC)
        [ev(50), ev(60)], // newer (ASC)
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      await loader.fetchAround(50);

      expect(f.calls, hasLength(2));
      // Backward half — strictly before the anchor.
      expect(f.calls[0].beforeOrdinal, 50);
      expect(f.calls[0].afterOrdinal, isNull);
      expect(f.calls[0].limit, 2); // pageSize 4 -> half 2
      // Forward half — anchor-inclusive (ordinal - 1).
      expect(f.calls[1].afterOrdinal, 49);
      expect(f.calls[1].beforeOrdinal, isNull);
      expect(f.calls[1].limit, 2);
    });

    test('anchor-near-top split: small lead before, large remainder after',
        () async {
      // At a realistic page size the split is asymmetric: a small backward lead
      // (kDefaultAnchorLead = 12) so the anchor renders near the top of the
      // window, and the rest of the page after it. (At tiny page sizes the lead
      // clamps to _half, which is why the pageSize:4 tests still see 2/2.)
      final f = fakeFetcher([
        [for (var s = 39; s >= 28; s--) ev(s)], // 12 older (DESC)
        [for (var s = 40; s < 40 + 28; s++) ev(s)], // 28 newer (ASC)
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 40);

      final w = await loader.fetchAround(40);

      expect(f.calls[0].limit, 12); // lead
      expect(f.calls[1].limit, 28); // pageSize - lead
      // Anchor (ordinal 40) sits at index 12 — near the top, after the lead.
      expect(w.ascending[12]['session_ordinal'], 40);
      expect(w.reachedHead, isFalse); // 12 == lead -> not short
      expect(w.reachedTail, isFalse); // 28 == afterLimit -> not short
    });

    test('explicit leadBefore overrides the default lead', () async {
      final f = fakeFetcher([
        [ev(39), ev(38), ev(37)], // 3 older
        [for (var s = 40; s < 40 + 17; s++) ev(s)], // 17 newer
      ]);
      final loader =
          RandomAccessLoader(fetch: f.fetch, pageSize: 20, leadBefore: 3);

      await loader.fetchAround(40);

      expect(f.calls[0].limit, 3);
      expect(f.calls[1].limit, 17); // 20 - 3
    });

    test('merges older.reversed + newer into one ascending run', () async {
      final f = fakeFetcher([
        [ev(40), ev(30)], // older DESC
        [ev(50), ev(60)], // newer ASC
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(50);

      expect(w.ascending.map((e) => e['session_ordinal']), [30, 40, 50, 60]);
      expect(w.isEmpty, isFalse);
    });

    test('short halves set reachedHead / reachedTail; full halves do not',
        () async {
      // half = 2. Backward returns 1 (< 2 -> head). Forward returns 2 (full -> not tail).
      final f = fakeFetcher([
        [ev(40)],
        [ev(50), ev(60)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(50);

      expect(w.reachedHead, isTrue);
      expect(w.reachedTail, isFalse);
    });

    test('both halves empty -> isEmpty (anchor out of range)', () async {
      final f = fakeFetcher([[], []]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(999);

      expect(w.isEmpty, isTrue);
      expect(w.reachedHead, isTrue);
      expect(w.reachedTail, isTrue);
    });
  });

  group('RandomAccessLoader.fetchNewer', () {
    test('pages with after=newestOrdinal at full pageSize', () async {
      final f = fakeFetcher([
        [ev(70), ev(80)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer(60);

      expect(f.calls.single.afterOrdinal, 60);
      expect(f.calls.single.beforeOrdinal, isNull);
      expect(f.calls.single.limit, 4); // full page, not half
      expect(p.events.map((e) => e['session_ordinal']), [70, 80]);
    });

    test('full page -> not yet at tail', () async {
      final f = fakeFetcher([
        [ev(70), ev(80), ev(90), ev(100)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer(60);

      expect(p.reachedTail, isFalse);
    });

    test('short page -> reached the live tail', () async {
      final f = fakeFetcher([
        [ev(70)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer(60);

      expect(p.reachedTail, isTrue);
    });
  });

  group('RandomAccessLoader.fetchOlder', () {
    test('pages with before=oldestOrdinal at full pageSize, reversed to '
        'ascending', () async {
      // Server returns DESC for a `before_ordinal` cursor.
      final f = fakeFetcher([
        [ev(50), ev(40)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder(60);

      expect(f.calls.single.beforeOrdinal, 60);
      expect(f.calls.single.afterOrdinal, isNull);
      expect(f.calls.single.limit, 4); // full page, not half
      // Reversed DESC -> ascending for the buffer.
      expect(p.ascending.map((e) => e['session_ordinal']), [40, 50]);
    });

    test('full page -> not yet at head', () async {
      final f = fakeFetcher([
        [ev(50), ev(40), ev(30), ev(20)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder(60);

      expect(p.reachedHead, isFalse);
    });

    test('short page -> reached the start of the range', () async {
      final f = fakeFetcher([
        [ev(50)],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder(60);

      expect(p.reachedHead, isTrue);
    });
  });
}
