// Random-access window loader — the `(ts, seq)` compound-keyset fetch logic
// for the Insights transcript (plan P2, docs/plans/agent-run-analysis-mode.md;
// #1048 shipped it inline, #1049 extracted it here).
//
// This is the *fetch* half of the random-access loader: the two network
// shapes a mid-run anchor needs — fetch a window *around* an anchor, and
// page *newer* than the loaded edge — pulled out of `_AgentFeedState` so
// they are pure and directly testable with a fake fetcher (no widgets, no
// setState, no HubClient auth). The buffer + setState stay in the State
// (they are inherently widget-bound); everything here is a free function of
// its inputs.
//
// Keeping the keyset math standalone is the point: the "land on the wrong
// row / page-walk to the top" class of bug (device-test passes 1–3) lived in
// the seam between the fetch keys and the ingest, where it was invisible.
// A directly-tested module makes the keyset contract (anchor-inclusive
// `afterSeq: seq - 1`, half-page head/tail detection) explicit.

/// Narrow fetch dependency the [RandomAccessLoader] needs — the keyset subset
/// of `HubClient.listAgentEvents`. `agentId` and `sessionId` are bound by the
/// caller's closure; only the cursor + limit vary per call. Lets the loader be
/// unit-tested with a canned closure instead of a live client.
typedef AgentEventsFetcher = Future<List<Map<String, dynamic>>> Function({
  String? beforeTs,
  int? beforeSeq,
  String? afterTs,
  int? afterSeq,
  required int limit,
});

/// Result of [RandomAccessLoader.fetchAround]: the contiguous ascending window
/// (the backward half reversed, then the forward half) plus whether each half
/// came back short — i.e. the window already reaches the start of the
/// transcript ([reachedHead]) and/or the live tail ([reachedTail]).
class RandomAccessWindow {
  const RandomAccessWindow({
    required this.ascending,
    required this.reachedHead,
    required this.reachedTail,
  });

  /// `older.reversed + newer` — a single ascending run with the anchor row
  /// inside it. Empty when the anchor is out of range (both halves empty).
  final List<Map<String, dynamic>> ascending;

  /// The backward half came back short → no older page exists; the window
  /// floor is the start of the transcript.
  final bool reachedHead;

  /// The forward half came back short → no newer page exists; the window
  /// reaches the live tail (so SSE append may resume).
  final bool reachedTail;

  /// Nothing came back — the anchor is out of range. The caller should leave
  /// the current window untouched rather than blanking the viewport.
  bool get isEmpty => ascending.isEmpty;
}

/// Result of [RandomAccessLoader.fetchNewer]: the next forward page (raw, not
/// yet deduped against the loaded window — that touches the State's id set)
/// plus whether it came back short, which means the window has rejoined the
/// live tail.
class ForwardPage {
  const ForwardPage({required this.events, required this.reachedTail});

  /// The raw fetched page, ascending. The caller dedupes + appends.
  final List<Map<String, dynamic>> events;

  /// The page came back short → there is nothing newer left; the window has
  /// caught up to the live tail.
  final bool reachedTail;
}

/// Pure `(ts, seq)` keyset loader for the random-access (Insights) window.
/// Holds no buffer and no widget state — it only knows how to ask the fetcher
/// for the right rows and report whether an edge was reached.
class RandomAccessLoader {
  RandomAccessLoader({required AgentEventsFetcher fetch, required int pageSize})
      : _fetch = fetch,
        _pageSize = pageSize;

  final AgentEventsFetcher _fetch;
  final int _pageSize;

  /// A window is one [_pageSize] block split evenly across the anchor: half
  /// before, half after.
  int get _half => _pageSize ~/ 2;

  /// Fetch one block *around* the anchor `(ts, seq)` — the backward half
  /// (events strictly before the anchor key, DESC) and the forward half (the
  /// anchor and after, ASC, anchor-inclusive via `afterSeq: seq - 1`). The two
  /// fetches run sequentially (preserving the prior inline behaviour). Returns
  /// the merged ascending window plus the head/tail-reached flags derived from
  /// each half's length.
  Future<RandomAccessWindow> fetchAround(int seq, String ts) async {
    final older = await _fetch(beforeTs: ts, beforeSeq: seq, limit: _half);
    final newer = await _fetch(afterTs: ts, afterSeq: seq - 1, limit: _half);
    return RandomAccessWindow(
      ascending: <Map<String, dynamic>>[...older.reversed, ...newer],
      reachedHead: older.length < _half,
      reachedTail: newer.length < _half,
    );
  }

  /// Page the next [_pageSize] block *newer* than the loaded edge
  /// `(newestTs, newestSeq)`. Returns the raw page plus whether it came back
  /// short (the window has caught the live tail).
  Future<ForwardPage> fetchNewer(String newestTs, int newestSeq) async {
    final newer = await _fetch(
      afterTs: newestTs,
      afterSeq: newestSeq,
      limit: _pageSize,
    );
    return ForwardPage(events: newer, reachedTail: newer.length < _pageSize);
  }
}
