import 'package:flutter/foundation.dart';

/// Drives an external jump-to-seq into a transcript from a sibling — the
/// analysis-mode payoff (plan P2): the run-report dashboard and the
/// structure-index rows live *above* the transcript, so a tapped error/turn/tool
/// needs a channel down into the transcript's seek. The transcript listens;
/// [seekTo] bumps a generation counter so re-requesting the *same* seq still
/// re-fires (a second tap on the same error jumps again). The transcript
/// resolves the seq against its loaded window, paging/window-resetting toward it
/// if the anchor is off-window, then anchors + highlights the row.
///
/// Mode-agnostic substrate (ADR-040): the live feed never wires a dashboard, so
/// in practice only the Insight surface drives this — but the shape is pure
/// `ChangeNotifier` plumbing with no surface knowledge, so it lives in the
/// shared substrate. `agent_feed.dart` keeps an `AgentFeedSeekController` alias
/// for its existing call sites + test until the P4 rename.
class TranscriptSeekController extends ChangeNotifier {
  int? _seq;
  String? _ts;
  int _generation = 0;

  /// The most recently requested seq, or null before any request.
  int? get seq => _seq;

  /// The anchor's timestamp, when the caller knows it (the Turns index carries
  /// `start_ts`). The session-scoped random-access loader needs it to window
  /// around the anchor via the `(ts, seq)` keyset; a seq-only request (the
  /// Errors stat) falls back to the bounded page-walk.
  String? get ts => _ts;

  /// Increments on every [seekTo]; lets the transcript distinguish a fresh
  /// request for the same seq from a no-op rebuild.
  int get generation => _generation;

  void seekTo(int seq, {String? ts}) {
    _seq = seq;
    _ts = ts;
    _generation++;
    notifyListeners();
  }
}
