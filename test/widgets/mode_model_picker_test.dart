import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// Tests for ADR-021 W2.5 — mode + model state extraction the picker
// strip uses. Pins the contract that only the *latest*
// current_mode_update / current_model_update wins, that mode and
// model are independent (one being absent doesn't suppress the other),
// and that non-system events never contribute to the picker state.

void main() {
  Map<String, dynamic> sysEvent({
    String? currentModeId,
    List<Map<String, dynamic>>? availableModes,
    String? currentModelId,
    List<Map<String, dynamic>>? availableModels,
  }) {
    final payload = <String, dynamic>{};
    if (currentModeId != null) {
      payload['currentModeId'] = currentModeId;
      payload['availableModes'] = availableModes ?? const [];
    }
    if (currentModelId != null) {
      payload['currentModelId'] = currentModelId;
      payload['availableModels'] = availableModels ?? const [];
    }
    return {'kind': 'system', 'payload': payload};
  }

  group('modeModelStateFromEvents', () {
    test('empty event list → null (strip stays hidden)', () {
      expect(modeModelStateFromEvents(const []), isNull);
    });

    test('only non-system events → null', () {
      final events = [
        {'kind': 'text', 'payload': {'text': 'hi'}},
        {'kind': 'tool_call', 'payload': {'id': 't1'}},
      ];
      expect(modeModelStateFromEvents(events), isNull);
    });

    test('mode advertised, model absent → state with mode only', () {
      final events = [
        sysEvent(
          currentModeId: 'default',
          availableModes: [
            {'id': 'default', 'name': 'Default'},
            {'id': 'yolo', 'name': 'Yolo'},
          ],
        ),
      ];
      final state = modeModelStateFromEvents(events);
      expect(state, isNotNull);
      expect(state!['currentMode'], 'default');
      expect(state['currentModel'], isNull);
      expect((state['availableModes'] as List).length, 2);
    });

    test('latest current_mode_update wins on multiple updates', () {
      final events = [
        sysEvent(
          currentModeId: 'default',
          availableModes: [
            {'id': 'default'},
            {'id': 'yolo'},
          ],
        ),
        sysEvent(
          currentModeId: 'yolo',
          availableModes: [
            {'id': 'default'},
            {'id': 'yolo'},
          ],
        ),
      ];
      final state = modeModelStateFromEvents(events);
      expect(state!['currentMode'], 'yolo');
    });

    test('mode + model both present → state carries both', () {
      final events = [
        sysEvent(
          currentModeId: 'plan',
          availableModes: [
            {'id': 'plan'},
            {'id': 'yolo'},
          ],
          currentModelId: 'gemini-2.5-pro',
          availableModels: [
            {'id': 'gemini-2.5-pro'},
            {'id': 'gemini-2.5-flash'},
          ],
        ),
      ];
      final state = modeModelStateFromEvents(events);
      expect(state!['currentMode'], 'plan');
      expect(state['currentModel'], 'gemini-2.5-pro');
    });

    test('mode-only and model-only events on different rows merge', () {
      // Mode came in first, then model in a later notification — the
      // walker must surface both, not just the most recent kind.
      final events = [
        sysEvent(
          currentModeId: 'default',
          availableModes: [
            {'id': 'default'},
          ],
        ),
        sysEvent(
          currentModelId: 'gemini-2.5-flash',
          availableModels: [
            {'id': 'gemini-2.5-flash'},
          ],
        ),
      ];
      final state = modeModelStateFromEvents(events);
      expect(state!['currentMode'], 'default');
      expect(state['currentModel'], 'gemini-2.5-flash');
    });

    test('non-string currentModeId is ignored', () {
      // Forward-compatibility: a future ACP shape that ships
      // currentModeId as an object would silently downgrade rather
      // than crash the picker.
      final events = [
        {
          'kind': 'system',
          'payload': {
            'currentModeId': {'unexpected': 'object'},
            'availableModes': const [],
          },
        },
      ];
      expect(modeModelStateFromEvents(events), isNull);
    });
  });
}
