import 'package:flutter/material.dart';

/// The transcript **landing engine** (ADR-040 P2a) — the scroll/converge core
/// lifted verbatim from `_AgentFeedState`. Given a target row INDEX in a
/// lazily-built, variable-height `ListView`, it scrolls the offset so that row
/// is visible by binary-searching against the realized-row window the host
/// reports each layout (no positioned-list dependency, no uniform-height
/// assumption — see docs/discussions/insight-navigation-fixed-pages.md §10).
///
/// It also owns the **programmatic-scroll guard** (so the host's scroll
/// listener can tell a seek/scrub/jump from a user scroll and not flip
/// tail-follow mid-motion — the "jump to end" bug) and the **seek GlobalKey**
/// the host attaches to the target card.
///
/// Mode-agnostic substrate: it knows nothing about lenses, events, or which
/// surface hosts it. The host drives it ([beginFrame] / [recordBuiltRow] each
/// layout) and reads back [isProgrammatic] + [topBuiltSeq]/[lastTopBuiltSeq].
class TranscriptSeek {
  final ScrollController scroll;

  /// Whether the host is still mounted; the convergence bails (releasing its
  /// guard) once false, mirroring the State's `mounted` checks.
  final bool Function() isActive;

  TranscriptSeek({required this.scroll, required this.isActive});

  /// Attached by the host to whichever card matches the active seek anchor; the
  /// convergence reads its `BuildContext` for the final `ensureVisible`.
  final GlobalKey seekKey = GlobalKey();

  // The realized (built) row-index window of the ListView, refreshed every
  // layout from the itemBuilder. A jump-to-known-row seek uses this as feedback
  // to binary-search the scroll offset onto a target index without assuming
  // uniform row heights. Reset at the top of the build that owns the list;
  // -1 / count are empty sentinels.
  int minBuiltIdx = 0;
  int maxBuiltIdx = -1;

  /// Length of the current lensed (rendered) list, snapshotted each build. The
  /// convergent index seek reads it to seed its first probe *proportionally*
  /// (idx / count → offset) so a structural jump lands within a screen of the
  /// target on the first frame — and to land the card by a final proportional
  /// jump if the realized-window feedback can't fully converge, so a jump never
  /// leaves the card off-screen ("card not in viewport").
  int lensedCount = 0;

  /// The smallest seq among the rows realised in the current build (the topmost
  /// on-screen card ≈ the viewport top). [topBuiltSeq] accumulates during a
  /// frame's layout; [lastTopBuiltSeq] snapshots it at the next build so the
  /// host's position readout can read a stable value.
  int topBuiltSeq = 0;
  int lastTopBuiltSeq = 0;

  /// Reset the realized-window sentinels at the top of the build that owns the
  /// list; [count] is the rendered (lensed) list length.
  void beginFrame(int count) {
    if (topBuiltSeq > 0) lastTopBuiltSeq = topBuiltSeq;
    topBuiltSeq = 0;
    minBuiltIdx = count;
    maxBuiltIdx = -1;
    lensedCount = count;
  }

  /// Record a row realised during layout: row [i] carrying [builtSeq].
  void recordBuiltRow(int i, int builtSeq) {
    if (i < minBuiltIdx) minBuiltIdx = i;
    if (i > maxBuiltIdx) maxBuiltIdx = i;
    if (builtSeq > 0 && (topBuiltSeq == 0 || builtSeq < topBuiltSeq)) {
      topBuiltSeq = builtSeq;
    }
  }

  // >0 while a seek/scrub/jump drives the scroll, so the host's scroll listener
  // doesn't mistake the programmatic motion for the user reaching the tail. A
  // depth counter (not a bool) so overlapping programmatic scrolls — a seek's
  // animateTo followed by its ensureVisible — keep the guard up until ALL of
  // them finish.
  int _programmaticDepth = 0;
  bool get isProgrammatic => _programmaticDepth > 0;
  void _release() {
    if (_programmaticDepth > 0) _programmaticDepth--;
  }

  /// Mark a synchronous scroll (jumpTo) as programmatic; clear after the frame
  /// it lands on.
  void jumpProgrammatic(void Function() body) {
    _programmaticDepth++;
    body();
    WidgetsBinding.instance.addPostFrameCallback((_) => _release());
  }

  /// Mark an animated scroll as programmatic for its whole duration — animateTo
  /// spans many frames, each firing the scroll listener, so the flag must hold
  /// until the animation future completes ([run] returns it).
  void animateProgrammatic(Future<void> Function() run) {
    _programmaticDepth++;
    run().whenComplete(_release);
  }

  /// Begin a convergent landing on row [idx]: holds ONE guard increment across
  /// the whole convergence (released once it finishes), scheduled after the
  /// next layout has attached [seekKey] to the target. The host sets its own
  /// anchor/highlight state before calling this.
  void landOnIndex(int idx) {
    // One guard increment held across the WHOLE convergence (many frames of
    // jumpTo + a final ensureVisible), released once at the end — so no
    // mid-seek frame flips tail-follow (the "jump to end" bug).
    _programmaticDepth++;
    // Start after the host's rebuild has attached seekKey to the new target,
    // so the realized-window read reflects the right frame.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!isActive() || !scroll.hasClients) {
        _release();
        return;
      }
      _converge(idx, 0.0, scroll.position.maxScrollExtent, 0);
    });
  }

  // Land row [idx] in the viewport by index (option 1+2,
  // docs/discussions/insight-navigation-fixed-pages.md §10). Offset rises
  // monotonically with index in this (non-reversed) list, so the realized
  // window brackets the target: idx below it → scroll up (hi=mid), above it →
  // scroll down (lo=mid). Two refinements over a plain [lo,hi] bisection make
  // this land the card RELIABLY rather than approximately:
  //   1. The first probe is *proportional* (idx / count → offset), not the
  //      [0,max] midpoint, so the target usually realizes on the first frame —
  //      a far jump no longer needs the full log₂(extent) bisection budget.
  //   2. On the iteration cap it NEVER bails to nothing: it does a final
  //      proportional jump + best-effort ensureVisible, so the card is brought
  //      into view even when the realized-window feedback can't fully converge
  //      (the "card not in viewport" bug was this branch releasing with a null
  //      seek context and no scroll).
  void _converge(int idx, double lo, double hi, int iter) {
    if (!isActive() || !scroll.hasClients) {
      _release();
      return;
    }
    final max = scroll.position.maxScrollExtent;
    final ctx = seekKey.currentContext;
    // ctx != null inline in the `if` so flow analysis promotes it (no `!`).
    if (idx >= minBuiltIdx && idx <= maxBuiltIdx && ctx != null) {
      Scrollable.ensureVisible(
        ctx,
        alignment: 0.3,
        duration: const Duration(milliseconds: 220),
        curve: Curves.easeOut,
      ).whenComplete(_release);
      return;
    }
    if (iter >= 14) {
      // Converged as far as the realized-window feedback allows. Guarantee the
      // card lands rather than leaving the viewport off-target.
      if (ctx != null) {
        Scrollable.ensureVisible(
          ctx,
          alignment: 0.3,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        ).whenComplete(_release);
        return;
      }
      if (lensedCount > 1) {
        scroll.jumpTo(((idx / (lensedCount - 1)) * max).clamp(0.0, max));
      }
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!isActive()) {
          _release();
          return;
        }
        final c2 = seekKey.currentContext;
        if (c2 != null) {
          Scrollable.ensureVisible(
            c2,
            alignment: 0.3,
            duration: const Duration(milliseconds: 200),
            curve: Curves.easeOut,
          ).whenComplete(_release);
        } else {
          _release();
        }
      });
      return;
    }
    // First probe proportional (lands within ~a screen so the target usually
    // realizes immediately); thereafter bisect using the realized window.
    final double mid;
    if (iter == 0 && lensedCount > 1) {
      mid = ((idx / (lensedCount - 1)) * max).clamp(0.0, max);
    } else {
      mid = ((lo + hi) / 2).clamp(0.0, max);
    }
    // Reset the realized-window sentinels so the post-jump layout reports ONLY
    // the new viewport. Critical: jumpTo re-runs the itemBuilder (which grows
    // the window) but NOT build() (where the reset otherwise lives), so without
    // this the window accumulates the UNION of every viewport visited during
    // the search — the bound test then finds idx already "inside" the union,
    // never narrows, and the seek stalls or lands on the wrong row. The failure
    // is intermittent and worse after lazy-loading, when the longer,
    // height-varied list makes the union span the target more often.
    minBuiltIdx = 1 << 30;
    maxBuiltIdx = -1;
    scroll.jumpTo(mid);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!isActive() || !scroll.hasClients) {
        _release();
        return;
      }
      var nlo = lo;
      var nhi = hi;
      if (idx < minBuiltIdx) {
        nhi = mid; // target is above the realized window — scroll up
      } else if (idx > maxBuiltIdx) {
        nlo = mid; // target is below — scroll down
      }
      _converge(idx, nlo, nhi, iter + 1);
    });
  }
}
