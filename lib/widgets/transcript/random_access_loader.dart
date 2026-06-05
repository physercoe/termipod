// Random-access window loader — the dense `session_ordinal` keyset fetch logic
// for the Insights transcript (plan P2, docs/plans/agent-run-analysis-mode.md;
// #1048 shipped it inline, #1049 extracted it here, ADR-042 P4 re-keyed it onto
// the session ordinal).
//
// This is the *fetch* half of the random-access loader: the two network
// shapes a mid-run anchor needs — fetch a window *around* an anchor, and
// page *newer* than the loaded edge — pulled out of `_AgentFeedState` so
// they are pure and directly testable with a fake fetcher (no widgets, no
// setState, no HubClient auth). The buffer + setState stay in the State
// (they are inherently widget-bound); everything here is a free function of
// its inputs.
//
// The cursor is the dense, per-session `session_ordinal` (ADR-042): a single
// gap-free coordinate that is unique across the agents a resumed session spans
// (per-agent `seq` collides there — two agents both start at 1). So the
// window-around-anchor and page-newer/older fetches use one ordinal cursor
// instead of the old `(ts, seq)` compound keyset — no tiebreak, no same-ts
// sibling drop, and the loader lands on the right row after a resume.
//
// Keeping the keyset math standalone is the point: the "land on the wrong
// row / page-walk to the top" class of bug (device-test passes 1–3) lived in
// the seam between the fetch keys and the ingest, where it was invisible.
// A directly-tested module makes the keyset contract (anchor-inclusive
// `afterOrdinal: ordinal - 1`, half-page head/tail detection) explicit.

/// Narrow fetch dependency the [RandomAccessLoader] needs — the keyset subset
/// of `HubClient.listAgentEvents`. `agentId` and `sessionId` are bound by the
/// caller's closure; only the cursor + limit vary per call. Lets the loader be
/// unit-tested with a canned closure instead of a live client. The cursor is
/// the session ordinal (ADR-042 P4): `beforeOrdinal` pages strictly older
/// (server DESC), `afterOrdinal` strictly newer (server ASC).
typedef AgentEventsFetcher = Future<List<Map<String, dynamic>>> Function({
  int? beforeOrdinal,
  int? afterOrdinal,
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

/// Result of [RandomAccessLoader.fetchOlder]: the next backward page already
/// reversed to ascending order (the buffer's order), plus whether it came back
/// short — i.e. the list now reaches the start of the (kind-filtered) range.
class BackwardPage {
  const BackwardPage({required this.ascending, required this.reachedHead});

  /// The fetched page, reversed from the server's DESC to ascending. The
  /// caller dedupes + prepends.
  final List<Map<String, dynamic>> ascending;

  /// The page came back short → there is nothing older left; the list reaches
  /// the start of its range.
  final bool reachedHead;
}

/// Default backward lead for [RandomAccessLoader.fetchAround]: the number of
/// events fetched *before* the anchor. Deliberately small — see [_leadBefore].
const int kDefaultAnchorLead = 12;

/// Pure `(ts, seq)` keyset loader for the random-access (Insights) window.
/// Holds no buffer and no widget state — it only knows how to ask the fetcher
/// for the right rows and report whether an edge was reached.
class RandomAccessLoader {
  RandomAccessLoader({
    required AgentEventsFetcher fetch,
    required int pageSize,
    int leadBefore = kDefaultAnchorLead,
  })  : _fetch = fetch,
        _pageSize = pageSize,
        _leadBefore = leadBefore;

  final AgentEventsFetcher _fetch;
  final int _pageSize;

  /// How many events to fetch *before* the anchor. Small on purpose: the
  /// anchor then renders among the FIRST rows of the freshly-reset window, so
  /// it is realised at scroll offset 0 and `ensureVisible` lands it directly —
  /// no far-scroll convergence over a variable-height list (the "structural
  /// jump doesn't land the card in the viewport for unloaded context" class of
  /// bug, device-test pass 4). The rest of the page loads *after* the anchor;
  /// scrolling up reloads older context via the State's load-older pager. See
  /// docs/discussions/insight-navigation-fixed-pages.md §10.
  final int _leadBefore;

  int get _half => _pageSize ~/ 2;

  /// Backward lead, clamped to [`_half`] so tiny-page callers (tests) still get
  /// a sane split rather than a zero-length forward half.
  int get _lead => _leadBefore < _half ? _leadBefore : _half;

  /// Fetch one block *around* the anchor [ordinal]: a small backward lead
  /// ([_lead] events strictly before the anchor, DESC) and a large forward
  /// remainder (the anchor and after, ASC, anchor-inclusive via
  /// `afterOrdinal: ordinal - 1`), summing to [_pageSize]. The asymmetric split
  /// lands the anchor near the top of the window. The two fetches run
  /// sequentially. Returns the merged ascending window plus the head/tail-
  /// reached flags derived from each half's length against what it requested.
  Future<RandomAccessWindow> fetchAround(int ordinal) async {
    final lead = _lead;
    final afterLimit = _pageSize - lead;
    final older = await _fetch(beforeOrdinal: ordinal, limit: lead);
    final newer = await _fetch(afterOrdinal: ordinal - 1, limit: afterLimit);
    return RandomAccessWindow(
      ascending: <Map<String, dynamic>>[...older.reversed, ...newer],
      reachedHead: older.length < lead,
      reachedTail: newer.length < afterLimit,
    );
  }

  /// Page the next [_pageSize] block *newer* than the loaded edge
  /// [newestOrdinal]. Returns the raw page plus whether it came back short (the
  /// window has caught the live tail).
  Future<ForwardPage> fetchNewer(int newestOrdinal) async {
    final newer = await _fetch(afterOrdinal: newestOrdinal, limit: _pageSize);
    return ForwardPage(events: newer, reachedTail: newer.length < _pageSize);
  }

  /// Page the next [_pageSize] block *older* than the loaded edge
  /// [oldestOrdinal] — the backward complement of [fetchNewer]. The server
  /// returns DESC for a `before_ordinal` cursor; the result is reversed to the
  /// ascending order the buffer keeps. `reachedHead` is true when the page
  /// came back short (no older events remain). Used by the lens-as-query list
  /// (ADR-039) to scroll up through a homogeneous kind set — unlike the live
  /// Feed's load-older, every returned row is a real list item, so there is no
  /// filtered-prepend anchoring ambiguity.
  Future<BackwardPage> fetchOlder(int oldestOrdinal) async {
    final older = await _fetch(beforeOrdinal: oldestOrdinal, limit: _pageSize);
    return BackwardPage(
      ascending: older.reversed.toList(),
      reachedHead: older.length < _pageSize,
    );
  }
}
