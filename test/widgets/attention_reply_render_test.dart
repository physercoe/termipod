import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Cross-language contract test: `renderAttentionReplyText` (Dart) must
// produce the same per-kind text as `formatAttentionReplyText` (Go,
// hub/internal/hostrunner/driver_stdio.go). The transcript card on
// mobile shows this string as the literal text the engine receives,
// so a drift between the two would lie to the user about what was
// sent on the wire.

void main() {
  group('renderAttentionReplyText — approval_request', () {
    test('approve without reason → "Approved."', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'approve',
      });
      expect(out, '[reply to approval_request 01KR5CT6] Approved.');
    });

    test('approve with reason → "Approved. Reason: …"', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'approve',
        'reason': 'looks safe',
      });
      expect(out,
          '[reply to approval_request 01KR5CT6] Approved. Reason: looks safe');
    });

    test('reject without reason → "Rejected."', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reject',
      });
      expect(out, '[reply to approval_request 01KR5CT6] Rejected.');
    });

    test('unknown decision falls through to bare value', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'defer',
      });
      expect(out, '[reply to approval_request 01KR5CT6] defer');
    });
  });

  group('renderAttentionReplyText — select', () {
    test('selected with option_id', () {
      final out = renderAttentionReplyText({
        'kind': 'select',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'select',
        'option_id': 'option-b',
      });
      expect(out, '[reply to select 01KR5CT6] Selected: option-b');
    });

    test('reject with reason', () {
      final out = renderAttentionReplyText({
        'kind': 'select',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reject',
        'reason': 'none fit',
      });
      expect(out,
          '[reply to select 01KR5CT6] No option chosen. Reason: none fit');
    });
  });

  group('renderAttentionReplyText — help_request', () {
    test('reply with body', () {
      final out = renderAttentionReplyText({
        'kind': 'help_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reply',
        'body': 'Use python 3.11.',
      });
      expect(out, '[reply to help_request 01KR5CT6] Use python 3.11.');
    });

    test('reject without reason', () {
      final out = renderAttentionReplyText({
        'kind': 'help_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reject',
      });
      expect(out,
          '[reply to help_request 01KR5CT6] Dismissed without reply.');
    });

    test('empty body still produces a text', () {
      final out = renderAttentionReplyText({
        'kind': 'help_request',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reply',
        'body': '',
      });
      expect(out, '[reply to help_request 01KR5CT6] (empty reply)');
    });
  });

  group('renderAttentionReplyText — short request_id + unknown kind', () {
    test('request_id under 8 chars uses full id', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'request_id': 'abc',
        'decision': 'approve',
      });
      expect(out, '[reply to approval_request abc] Approved.');
    });

    test('unknown kind falls back to body if any', () {
      final out = renderAttentionReplyText({
        'kind': 'novel_kind',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reply',
        'body': 'hello',
      });
      expect(out, '[reply to novel_kind 01KR5CT6] hello');
    });

    test('unknown kind without body falls back to decision', () {
      final out = renderAttentionReplyText({
        'kind': 'novel_kind',
        'request_id': '01KR5CT645A7KTWVZDXDWT6Y8D',
        'decision': 'reply',
      });
      expect(out, '[reply to novel_kind 01KR5CT6] reply');
    });

    test('no request_id skips the prefix', () {
      final out = renderAttentionReplyText({
        'kind': 'approval_request',
        'decision': 'approve',
      });
      expect(out, 'Approved.');
    });
  });
}
