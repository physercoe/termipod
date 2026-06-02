// Unit tests for RandomAccessLoader — the `(ts, seq)` keyset fetch logic
// extracted from `_AgentFeedState` (#1049). Pure: drives the loader with a
// canned fetcher that records the cursor params, so the keyset contract
// (anchor-inclusive `afterSeq: seq - 1`, half-page head/tail detection,
// ascending merge of `older.reversed + newer`) is pinned directly instead of
// only through a widget.

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed/random_access_loader.dart';

/// One recorded call to the fetcher.
class _Call {
  _Call({this.beforeTs, this.beforeSeq, this.afterTs, this.afterSeq, required this.limit});
  final String? beforeTs;
  final int? beforeSeq;
  final String? afterTs;
  final int? afterSeq;
  final int limit;
}

/// Builds a fetcher that returns successive canned pages and records each call.
({AgentEventsFetcher fetch, List<_Call> calls}) fakeFetcher(
  List<List<Map<String, dynamic>>> pages,
) {
  final calls = <_Call>[];
  var i = 0;
  Future<List<Map<String, dynamic>>> fetch({
    String? beforeTs,
    int? beforeSeq,
    String? afterTs,
    int? afterSeq,
    required int limit,
  }) async {
    calls.add(_Call(
      beforeTs: beforeTs,
      beforeSeq: beforeSeq,
      afterTs: afterTs,
      afterSeq: afterSeq,
      limit: limit,
    ));
    final page = i < pages.length ? pages[i] : <Map<String, dynamic>>[];
    i++;
    return page;
  }

  return (fetch: fetch, calls: calls);
}

Map<String, dynamic> ev(int seq, String ts) => {'seq': seq, 'ts': ts, 'id': 'e$seq'};

void main() {
  group('RandomAccessLoader.fetchAround', () {
    test('uses the keyset cursors: before=(ts,seq), after=(ts,seq-1), half limit', () async {
      final f = fakeFetcher([
        [ev(40, 't40'), ev(30, 't30')], // older (DESC)
        [ev(50, 't50'), ev(60, 't60')], // newer (ASC)
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      await loader.fetchAround(50, 't50');

      expect(f.calls, hasLength(2));
      // Backward half.
      expect(f.calls[0].beforeTs, 't50');
      expect(f.calls[0].beforeSeq, 50);
      expect(f.calls[0].afterTs, isNull);
      expect(f.calls[0].limit, 2); // pageSize 4 -> half 2
      // Forward half — anchor-inclusive (seq - 1).
      expect(f.calls[1].afterTs, 't50');
      expect(f.calls[1].afterSeq, 49);
      expect(f.calls[1].beforeTs, isNull);
      expect(f.calls[1].limit, 2);
    });

    test('anchor-near-top split: small lead before, large remainder after', () async {
      // At a realistic page size the split is asymmetric: a small backward lead
      // (kDefaultAnchorLead = 12) so the anchor renders near the top of the
      // window, and the rest of the page after it. (At tiny page sizes the lead
      // clamps to _half, which is why the pageSize:4 tests still see 2/2.)
      final f = fakeFetcher([
        [for (var s = 39; s >= 28; s--) ev(s, 't$s')], // 12 older (DESC)
        [for (var s = 40; s < 40 + 28; s++) ev(s, 't$s')], // 28 newer (ASC)
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 40);

      final w = await loader.fetchAround(40, 't40');

      expect(f.calls[0].limit, 12); // lead
      expect(f.calls[1].limit, 28); // pageSize - lead
      // Anchor (seq 40) sits at index 12 — near the top, after the 12-event lead.
      expect(w.ascending[12]['seq'], 40);
      expect(w.reachedHead, isFalse); // 12 == lead -> not short
      expect(w.reachedTail, isFalse); // 28 == afterLimit -> not short
    });

    test('explicit leadBefore overrides the default lead', () async {
      final f = fakeFetcher([
        [ev(39, 't39'), ev(38, 't38'), ev(37, 't37')], // 3 older
        [for (var s = 40; s < 40 + 17; s++) ev(s, 't$s')], // 17 newer
      ]);
      final loader =
          RandomAccessLoader(fetch: f.fetch, pageSize: 20, leadBefore: 3);

      await loader.fetchAround(40, 't40');

      expect(f.calls[0].limit, 3);
      expect(f.calls[1].limit, 17); // 20 - 3
    });

    test('merges older.reversed + newer into one ascending run', () async {
      final f = fakeFetcher([
        [ev(40, 't40'), ev(30, 't30')], // older DESC
        [ev(50, 't50'), ev(60, 't60')], // newer ASC
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(50, 't50');

      expect(w.ascending.map((e) => e['seq']), [30, 40, 50, 60]);
      expect(w.isEmpty, isFalse);
    });

    test('short halves set reachedHead / reachedTail; full halves do not', () async {
      // half = 2. Backward returns 1 (< 2 -> head). Forward returns 2 (full -> not tail).
      final f = fakeFetcher([
        [ev(40, 't40')],
        [ev(50, 't50'), ev(60, 't60')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(50, 't50');

      expect(w.reachedHead, isTrue);
      expect(w.reachedTail, isFalse);
    });

    test('both halves empty -> isEmpty (anchor out of range)', () async {
      final f = fakeFetcher([[], []]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final w = await loader.fetchAround(999, 't999');

      expect(w.isEmpty, isTrue);
      expect(w.reachedHead, isTrue);
      expect(w.reachedTail, isTrue);
    });
  });

  group('RandomAccessLoader.fetchNewer', () {
    test('pages with after=(newestTs,newestSeq) at full pageSize', () async {
      final f = fakeFetcher([
        [ev(70, 't70'), ev(80, 't80')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer('t60', 60);

      expect(f.calls.single.afterTs, 't60');
      expect(f.calls.single.afterSeq, 60);
      expect(f.calls.single.limit, 4); // full page, not half
      expect(p.events.map((e) => e['seq']), [70, 80]);
    });

    test('full page -> not yet at tail', () async {
      final f = fakeFetcher([
        [ev(70, 't70'), ev(80, 't80'), ev(90, 't90'), ev(100, 't100')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer('t60', 60);

      expect(p.reachedTail, isFalse);
    });

    test('short page -> reached the live tail', () async {
      final f = fakeFetcher([
        [ev(70, 't70')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchNewer('t60', 60);

      expect(p.reachedTail, isTrue);
    });
  });

  group('RandomAccessLoader.fetchOlder', () {
    test('pages with before=(oldestTs,oldestSeq) at full pageSize, reversed to '
        'ascending', () async {
      // Server returns DESC for a `before` cursor.
      final f = fakeFetcher([
        [ev(50, 't50'), ev(40, 't40')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder('t60', 60);

      expect(f.calls.single.beforeTs, 't60');
      expect(f.calls.single.beforeSeq, 60);
      expect(f.calls.single.afterTs, isNull);
      expect(f.calls.single.limit, 4); // full page, not half
      // Reversed DESC -> ascending for the buffer.
      expect(p.ascending.map((e) => e['seq']), [40, 50]);
    });

    test('full page -> not yet at head', () async {
      final f = fakeFetcher([
        [ev(50, 't50'), ev(40, 't40'), ev(30, 't30'), ev(20, 't20')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder('t60', 60);

      expect(p.reachedHead, isFalse);
    });

    test('short page -> reached the start of the range', () async {
      final f = fakeFetcher([
        [ev(50, 't50')],
      ]);
      final loader = RandomAccessLoader(fetch: f.fetch, pageSize: 4);

      final p = await loader.fetchOlder('t60', 60);

      expect(p.reachedHead, isTrue);
    });
  });
}
