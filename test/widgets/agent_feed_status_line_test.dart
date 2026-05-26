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

  group('kAgentTurnActiveKinds (v1.0.721 — allowlist inversion)', () {
    // The pre-v1.0.721 contract was the inverse — a denylist
    // `kAgentBusyInferenceSkipKinds` that every new pre-turn-active
    // kind required appending to or the busy pill stuck on forever
    // (v1.0.667 `usage`, v1.0.699 `status_line`, v1.0.717 `raw`).
    // The new contract: default = idle; only allowlisted kinds
    // flip _isAgentBusy to true. See docs/discussions/consumer-side-
    // dispatch-contracts.md Fix A.

    test('includes the core turn-active kinds', () {
      // These are the kinds whose arrival LATER in the event tail
      // than the last terminal kind means the agent is actively
      // producing output. Stable list; producer-side additions are
      // almost always telemetry, not new turn-active signals.
      expect(kAgentTurnActiveKinds, contains('text'));
      expect(kAgentTurnActiveKinds, contains('tool_call'));
      expect(kAgentTurnActiveKinds, contains('thought'));
      expect(kAgentTurnActiveKinds, contains('plan'));
    });

    test('does NOT include the four historical denylist members', () {
      // Regression guards. Pre-v1.0.721 these were in
      // kAgentBusyInferenceSkipKinds — explicit "do not signal busy"
      // entries. Under the allowlist inversion they're absent by
      // default; the four bugs that prompted denylist additions
      // (v1.0.667/699/717/720) are auto-fixed by the inversion.
      expect(kAgentTurnActiveKinds, isNot(contains('usage')));
      expect(kAgentTurnActiveKinds, isNot(contains('rate_limit')));
      expect(kAgentTurnActiveKinds, isNot(contains('status_line')));
      expect(kAgentTurnActiveKinds, isNot(contains('raw')));
    });

    test('does NOT include terminal kinds (handled by explicit branches)', () {
      // _isAgentBusy has explicit short-circuit branches for these
      // kinds (return false on session.init / turn.result /
      // completion / lifecycle.exited / lifecycle.stopped). They
      // must NOT also appear in the active-kinds allowlist — that
      // would produce a contradictory contract (this is turn-active
      // AND also a terminal that returns idle).
      expect(kAgentTurnActiveKinds, isNot(contains('session.init')));
      expect(kAgentTurnActiveKinds, isNot(contains('turn.result')));
      expect(kAgentTurnActiveKinds, isNot(contains('completion')));
      expect(kAgentTurnActiveKinds, isNot(contains('lifecycle')));
    });

    test('does NOT include system / tool_result (motion-by-themselves: false)', () {
      // `system` carries telemetry like mcp_server_startup,
      // turn_started markers, etc. — none mean motion by
      // themselves.
      // `tool_result` sits between two `tool_call`s in a multi-tool
      // turn; by itself doesn't mean motion (the next `tool_call`
      // or `text` does). Keeping it out avoids a per-direction race
      // where the result lands after the next tool_call.
      expect(kAgentTurnActiveKinds, isNot(contains('system')));
      expect(kAgentTurnActiveKinds, isNot(contains('tool_result')));
    });
  });

  group('kAgentFeedAlwaysHiddenKinds cross-set discipline', () {
    test('status_line stays in always-hidden (ADR-036 D4)', () {
      // The chip-source-only kinds must stay hidden from the
      // transcript bubble layer regardless of the busy-inference
      // refactor.
      expect(kAgentFeedAlwaysHiddenKinds, contains('status_line'));
      expect(kAgentFeedAlwaysHiddenKinds, contains('usage'));
      expect(kAgentFeedAlwaysHiddenKinds, contains('rate_limit'));
      expect(kAgentFeedAlwaysHiddenKinds, contains('session.init'));
    });
  });
}
