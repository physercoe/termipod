import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-021 W1.3 — content-stable replay dedupe in
// AgentFeed's SSE ingest. The renderer can't drop frames by event id
// (replays come back with fresh hub-side ids assigned by the resumed
// agent's event stream), so we content-key on payload shape. These
// tests pin the keying contract so a future driver change that
// preserves the same logical events under a different payload shape
// fails loudly here rather than silently double-rendering turns on
// resume.

void main() {
  group('agentEventIsReplay', () {
    test('payload.replay == true → true', () {
      expect(
        agentEventIsReplay({
          'kind': 'text',
          'payload': {'text': 'hi', 'replay': true},
        }),
        isTrue,
      );
    });

    test('payload.replay missing → false', () {
      expect(
        agentEventIsReplay({
          'kind': 'text',
          'payload': {'text': 'hi'},
        }),
        isFalse,
      );
    });

    test('payload.replay = "true" string → false (strict bool match)', () {
      // The driver always emits a real bool; a stringified value is a
      // schema drift signal, not something we want to guess at.
      expect(
        agentEventIsReplay({
          'kind': 'text',
          'payload': {'text': 'hi', 'replay': 'true'},
        }),
        isFalse,
      );
    });

    test('payload absent → false', () {
      expect(agentEventIsReplay({'kind': 'text'}), isFalse);
    });
  });

  group('agentEventReplayKey', () {
    test('text events key on length-prefixed body', () {
      final key = agentEventReplayKey({
        'kind': 'text',
        'payload': {'text': 'hello world', 'replay': true},
      });
      expect(key, 'text:11:hello world');
    });

    test('thought events use thought prefix so they do not collide '
        'with same-text text events', () {
      final textKey = agentEventReplayKey({
        'kind': 'text',
        'payload': {'text': 'same body'},
      });
      final thoughtKey = agentEventReplayKey({
        'kind': 'thought',
        'payload': {'text': 'same body'},
      });
      expect(textKey, isNotNull);
      expect(thoughtKey, isNotNull);
      expect(textKey, isNot(thoughtKey));
    });

    test('length-prefix prevents prefix collision', () {
      // Without the length prefix, "hello" and "hello world" could
      // form a key like "text:hello world" matching against just
      // "text:hello" via String.startsWith semantics if a future
      // change moves to prefix matching. The length prefix pins it.
      final shortKey = agentEventReplayKey({
        'kind': 'text',
        'payload': {'text': 'hello'},
      });
      final longKey = agentEventReplayKey({
        'kind': 'text',
        'payload': {'text': 'hello world'},
      });
      expect(shortKey, isNot(longKey));
    });

    test('tool_call keys on agent-stable id', () {
      // The replay tool_call event from the resumed agent carries the
      // SAME tool_call_id as the original (the id is agent-side, not
      // hub-side), so this is the right key.
      final orig = agentEventReplayKey({
        'kind': 'tool_call',
        'payload': {
          'id': 'tc-42',
          'name': 'Read',
          'status': 'pending',
        },
      });
      final replay = agentEventReplayKey({
        'kind': 'tool_call',
        'payload': {
          'id': 'tc-42',
          'name': 'Read',
          'status': 'pending',
          'replay': true,
        },
      });
      expect(orig, replay);
      expect(orig, 'tool_call:tc-42');
    });

    test('tool_call_update factors status into the key', () {
      final pending = agentEventReplayKey({
        'kind': 'tool_call_update',
        'payload': {'toolCallId': 'tc-1', 'status': 'in_progress'},
      });
      final done = agentEventReplayKey({
        'kind': 'tool_call_update',
        'payload': {'toolCallId': 'tc-1', 'status': 'completed'},
      });
      expect(pending, isNot(done));
    });

    test('approval_request keys on request_id', () {
      final orig = agentEventReplayKey({
        'kind': 'approval_request',
        'payload': {'request_id': 'rq-7', 'params': {}},
      });
      final replay = agentEventReplayKey({
        'kind': 'approval_request',
        'payload': {'request_id': 'rq-7', 'params': {}, 'replay': true},
      });
      expect(orig, replay);
      expect(orig, 'approval_request:rq-7');
    });

    test('null payload → null key (event passes through replay '
        'filter unchanged)', () {
      expect(agentEventReplayKey({'kind': 'text'}), isNull);
    });

    test('unknown kind → null key (raw / lifecycle / system pass through)',
        () {
      expect(
        agentEventReplayKey({
          'kind': 'lifecycle',
          'payload': {'phase': 'started'},
        }),
        isNull,
      );
      expect(
        agentEventReplayKey({
          'kind': 'raw',
          'payload': {'method': 'something/unknown'},
        }),
        isNull,
      );
    });

    test('text event with empty body → null key (no signature to match on)',
        () {
      expect(
        agentEventReplayKey({
          'kind': 'text',
          'payload': {'text': ''},
        }),
        isNull,
      );
    });

    test('tool_call without id → null key', () {
      expect(
        agentEventReplayKey({
          'kind': 'tool_call',
          'payload': {'name': 'Read'},
        }),
        isNull,
      );
    });
  });

  group('replay-filter end-to-end semantics', () {
    test('a cached event followed by a replay match yields drop', () {
      // The Set-based filter inside _AgentFeedState is exercised
      // indirectly here: any event that produces the same key as one
      // already in the set should be skipped. This test pins the
      // contract so a driver that emits its replay events with a
      // different payload shape (e.g. moves text into 'body' instead
      // of 'text') triggers a CI failure.
      final cached = {
        'kind': 'tool_call',
        'payload': {'id': 'tc-1', 'name': 'Read'},
      };
      final replay = {
        'kind': 'tool_call',
        'payload': {'id': 'tc-1', 'name': 'Read', 'replay': true},
      };
      final seen = <String>{};
      final cachedKey = agentEventReplayKey(cached);
      if (cachedKey != null) seen.add(cachedKey);

      final replayKey = agentEventReplayKey(replay);
      expect(replayKey, isNotNull);
      expect(seen.contains(replayKey), isTrue,
          reason: 'replay key must collide with cached key');
    });

    test('a replay event with no cache match is admitted', () {
      // First time we see a tool_call (replay flag set) — no cached
      // match, so the renderer admits it AND adds the key for future
      // dedup.
      final seen = <String>{};
      final replay = {
        'kind': 'tool_call',
        'payload': {'id': 'tc-fresh', 'replay': true},
      };
      final key = agentEventReplayKey(replay);
      expect(key, isNotNull);
      expect(seen.contains(key), isFalse);
      seen.add(key!);
      expect(seen, contains(key));
    });

    test('non-replay events skip the dedup gate even with collision', () {
      // The W1.3 filter only fires when payload.replay == true. A
      // live-stream event that happens to have the same key as a
      // cached one (rare but possible — a re-issued tool_call) must
      // still render. The renderer's filter checks both flags before
      // dropping.
      final cached = {
        'kind': 'tool_call',
        'payload': {'id': 'tc-1', 'name': 'Read'},
      };
      final live = {
        'kind': 'tool_call',
        'payload': {'id': 'tc-1', 'name': 'Read'}, // no replay flag
      };
      expect(agentEventIsReplay(live), isFalse);
      // The fact that agentEventReplayKey returns the same value for
      // both is fine — the renderer's filter doesn't drop on key
      // collision alone.
      expect(agentEventReplayKey(cached), agentEventReplayKey(live));
    });
  });
}
