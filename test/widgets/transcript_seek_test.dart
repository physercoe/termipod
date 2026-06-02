// Unit tests for TranscriptSeek's pure realized-row-window bookkeeping —
// beginFrame / recordBuiltRow (ADR-040 P2a). The convergence + ensureVisible
// path needs a live ScrollController + rendered list (a widget test), but the
// sentinel logic the convergence reads back is pure and pinned here, since a
// subtle error in the lift (min/max bracketing, the top-seq snapshot) would
// silently mis-land jumps.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/transcript_seek.dart';

void main() {
  TranscriptSeek make() =>
      TranscriptSeek(scroll: ScrollController(), isActive: () => true);

  test('beginFrame seeds empty sentinels for the new list length', () {
    final s = make();
    s.beginFrame(10);
    expect(s.minBuiltIdx, 10); // count → "nothing realised below this yet"
    expect(s.maxBuiltIdx, -1);
    expect(s.lensedCount, 10);
    expect(s.topBuiltSeq, 0);
  });

  test('recordBuiltRow brackets the realized index window + tracks top seq', () {
    final s = make();
    s.beginFrame(10);
    s.recordBuiltRow(3, 100);
    s.recordBuiltRow(5, 80);
    s.recordBuiltRow(4, 0); // seq 0 ignored for top-seq, still widens the window
    expect(s.minBuiltIdx, 3);
    expect(s.maxBuiltIdx, 5);
    expect(s.topBuiltSeq, 80); // smallest POSITIVE seq = viewport-top card
  });

  test('beginFrame snapshots the prior frame top seq into lastTopBuiltSeq', () {
    final s = make();
    s.beginFrame(10);
    s.recordBuiltRow(2, 50);
    expect(s.lastTopBuiltSeq, 0); // not yet snapshotted
    s.beginFrame(10); // next frame snapshots the prior top
    expect(s.lastTopBuiltSeq, 50);
    expect(s.topBuiltSeq, 0); // accumulator cleared for the new frame
  });

  test('a frame with no positive seqs leaves lastTopBuiltSeq untouched', () {
    final s = make();
    s.beginFrame(4);
    s.recordBuiltRow(0, 0); // no positive seq this frame
    s.beginFrame(4); // topBuiltSeq was 0 → no snapshot
    expect(s.lastTopBuiltSeq, 0);
  });

  test('isProgrammatic is false at rest', () {
    expect(make().isProgrammatic, false);
  });
}
