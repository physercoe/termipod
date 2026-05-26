import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-036 D4 — `status_line` is a periodic-snapshot event
// (chip-source only, NOT a transcript bubble or a turn-active signal).
//
// v1.0.698 shipped Phase A of ADR-036 (hub-side gateway + adapter +
// session-id rotation handler) but the mobile feed's per-kind dispatch
// lists weren't swept — the new `status_line` kind fell through both
// gates, with two visible regressions on cold-open:
//   1) The raw JSON snapshot rendered as a visible feed bubble
//      (forbidden by ADR-036 D4 — chips reduce over the latest
//      payload, the transcript shouldn't show it).
//   2) The busy-state inference pinned to true forever (status_line
//      hit the default "anything else from the agent is a turn-active
//      signal" branch — same regression class as v1.0.667's `usage`
//      fix).
//
// These tests pin the two kind-list contracts so a future addition of
// a new periodic-snapshot kind doesn't re-open the regression.

void main() {
  group('kAgentFeedAlwaysHiddenKinds (ADR-036 D4)', () {
    test('includes status_line so cold-open snapshots stay out of the feed', () {
      // The bug: pre-v1.0.699, a cold-open status_line event with
      // mostly-null fields rendered as a JSON card. ADR-036 D4
      // explicitly forbids this — status_line is a reducer-over-events
      // signal for chips, not a transcript bubble.
      expect(kAgentFeedAlwaysHiddenKinds, contains('status_line'));
    });

    test('still includes the pre-ADR-036 hidden kinds', () {
      // Regression guard: ADR-036 added status_line, but the three
      // pre-existing always-hidden kinds (session.init / usage /
      // rate_limit) must stay hidden too. A refactor that drops one
      // would re-introduce the chip-vs-bubble duplication.
      expect(kAgentFeedAlwaysHiddenKinds, contains('session.init'));
      expect(kAgentFeedAlwaysHiddenKinds, contains('usage'));
      expect(kAgentFeedAlwaysHiddenKinds, contains('rate_limit'));
    });

    test('does NOT include text / thought / tool_call', () {
      // The shape we're asserting: this set is for kinds whose ONLY
      // surface is the chip layer. text/thought/tool_call are the
      // first-class transcript kinds — they MUST render as bubbles.
      expect(kAgentFeedAlwaysHiddenKinds, isNot(contains('text')));
      expect(kAgentFeedAlwaysHiddenKinds, isNot(contains('thought')));
      expect(kAgentFeedAlwaysHiddenKinds, isNot(contains('tool_call')));
    });
  });

  group('kAgentBusyInferenceSkipKinds (v1.0.667 + v1.0.699)', () {
    test('includes status_line so cold-open does not pin busy(cancel)', () {
      // The bug: a cold-open status_line (the only agent event before
      // the user types anything) fell through to the default "agent
      // sent something, must be busy" branch in _isAgentBusy. With no
      // turn.result / session.init / completion to clear it, the
      // spawn stuck in busy(cancel) until the first real turn.
      expect(kAgentBusyInferenceSkipKinds, contains('status_line'));
    });

    test('still includes usage + rate_limit (v1.0.667 fix)', () {
      // Regression guard for v1.0.667 — pre-fix `usage` fell through
      // to "busy" and pinned the pill on after every turn.result
      // (wire order: turn.result → text → usage means the LATEST
      // agent event is usage by the time the strip refreshes).
      expect(kAgentBusyInferenceSkipKinds, contains('usage'));
      expect(kAgentBusyInferenceSkipKinds, contains('rate_limit'));
    });

    test('includes raw so codex resume does not pin busy(cancel)', () {
      // The bug (v1.0.717): on codex M2 resume, the post-handshake
      // tail (newest-first) was:
      //   1. system{mcp_server_startup status=ready}  ← skipped
      //   2. raw{method: thread/goal/cleared}         ← NOT skipped
      //   3. usage / session.init / lifecycle.started (would idle)
      // The walker hit raw at step 2 and returned true → cancel
      // button shown → user tapped → no active turn → turn/interrupt
      // no-op → no closing event → stuck forever.
      //
      // Confirmed from the dev-host hub DB on 2026-05-26 in session
      // 01KSH9EW... with new agent 01KSH9GG...; three raw events
      // (thread/goal/cleared, remoteControl/status/changed,
      // configWarning) all from codex notifications without profile
      // rules per agent_families.yaml:782-787.
      //
      // Class kin: same multi-consumer-dispatch-fails-open as
      // v1.0.667 (usage) and v1.0.699 (status_line). Producer
      // (driver / profile) adds a new pre-turn-active event kind;
      // consumer's skip list misses it.
      expect(kAgentBusyInferenceSkipKinds, contains('raw'));
    });

    test('does NOT include text / thought / tool_call', () {
      // These ARE turn-active signals — if any of them is the latest
      // agent event, the agent IS busy. Skipping them would make the
      // pill silent during streaming.
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('text')));
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('thought')));
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('tool_call')));
    });

    test('does NOT include session.init / turn.result / completion', () {
      // These are explicit terminal-kind branches in _isAgentBusy
      // (they return false directly — "idle"). Adding them to the
      // skip set would still work (they'd skip past, no other event
      // is found, returns false) but it'd obscure the contract.
      // Pin: the skip set is for AMBIGUOUS-not-turn-active kinds,
      // not for terminal kinds.
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('session.init')));
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('turn.result')));
      expect(kAgentBusyInferenceSkipKinds, isNot(contains('completion')));
    });
  });

  group('cross-set discipline', () {
    test('status_line appears in BOTH gates', () {
      // ADR-036 D4 contract: status_line is chip-only. That means
      // BOTH the feed-bubble layer AND the busy-state inference
      // must skip it. Adding only one would leave the other bug
      // open. This test pins the two-gate symmetry so a future
      // refactor that splits the kind out of one set fails here.
      expect(kAgentFeedAlwaysHiddenKinds, contains('status_line'));
      expect(kAgentBusyInferenceSkipKinds, contains('status_line'));
    });
  });
}
