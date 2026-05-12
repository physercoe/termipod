import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/voice/cloud_stt.dart';
import 'package:termipod/services/voice/recording_controller.dart';
import 'package:termipod/services/voice/voice_recording_session.dart';

class _FakeBackend implements RecorderBackend {
  bool permission = true;
  StreamController<Uint8List>? _ctrl;
  int stopCalls = 0;
  int cancelCalls = 0;

  Future<void> closeMic() async {
    await _ctrl?.close();
    _ctrl = null;
  }

  @override
  Future<bool> hasPermission() async => permission;

  @override
  Future<Stream<Uint8List>> startStream({
    required int sampleRate,
    required int numChannels,
  }) async {
    _ctrl = StreamController<Uint8List>.broadcast();
    return _ctrl!.stream;
  }

  @override
  Future<void> stop() async {
    stopCalls += 1;
    await closeMic();
  }

  @override
  Future<void> cancel() async {
    cancelCalls += 1;
    await closeMic();
  }

  @override
  Future<void> dispose() async {
    await closeMic();
  }
}

class _FakeStt implements CloudStt {
  final StreamController<TranscriptUpdate> emit =
      StreamController<TranscriptUpdate>.broadcast();
  bool started = false;
  List<String>? lastLanguageHints;

  @override
  Stream<TranscriptUpdate> transcribeStream(
    Stream<Uint8List> audioChunks, {
    required List<String> languageHints,
  }) {
    started = true;
    lastLanguageHints = languageHints;
    // Listen to drain the audio (otherwise the underlying recorder
    // stream would never receive the close-event subscription).
    audioChunks.listen(
      (_) {},
      onDone: () {},
      cancelOnError: true,
    );
    return emit.stream;
  }

  void emitPartial(String text) => emit.add(
        TranscriptUpdate(text: text, isPartial: true, isFinal: false),
      );

  void emitFinal(String text) => emit.add(
        TranscriptUpdate(text: text, isPartial: false, isFinal: true),
      );

  Future<void> close() => emit.close();
}

void main() {
  group('VoiceRecordingSession', () {
    late _FakeBackend backend;
    late RecordingController recording;
    late _FakeStt stt;
    late VoiceRecordingSession session;

    setUp(() {
      backend = _FakeBackend();
      recording = RecordingController(backend: backend);
      stt = _FakeStt();
      session = VoiceRecordingSession(
        recording: recording,
        cloudStt: stt,
        languageHints: const ['zh', 'en'],
        maxDuration: const Duration(seconds: 60),
      );
    });

    tearDown(() async {
      await session.dispose();
    });

    test('start opens the mic + forwards language hints to the ASR', () async {
      await session.start();
      expect(session.isActive, isTrue);
      expect(stt.started, isTrue);
      expect(stt.lastLanguageHints, ['zh', 'en']);
    });

    test('partial transcripts emit transcriptUpdated events', () async {
      final events = <VoiceSessionEvent>[];
      session.events.listen(events.add);

      await session.start();
      stt.emitPartial('你好');
      await Future<void>.delayed(Duration.zero);

      expect(events.single.kind, VoiceSessionEventKind.transcriptUpdated);
      expect(events.single.text, '你好');
      expect(session.transcriptText, '你好');
    });

    test('final transcripts accumulate; next partial extends from accumulator',
        () async {
      final events = <VoiceSessionEvent>[];
      session.events.listen(events.add);

      await session.start();
      stt.emitPartial('你好');
      stt.emitFinal('你好 world');
      stt.emitPartial('how');
      await Future<void>.delayed(Duration.zero);

      expect(session.transcriptText, '你好 world how');
      // The last event should reflect the accumulated state.
      expect(events.last.text, '你好 world how');
    });

    test('stop closes the recorder so the ASR can finalise', () async {
      await session.start();
      stt.emitPartial('hello');

      await session.stop();

      expect(backend.stopCalls, 1);
      expect(backend.cancelCalls, 0);
    });

    test('cancel emits cancelled and skips the ASR finalise', () async {
      final events = <VoiceSessionEvent>[];
      session.events.listen(events.add);

      await session.start();
      stt.emitPartial('hi');
      await Future<void>.delayed(Duration.zero);

      await session.cancel();
      expect(session.isActive, isFalse);
      expect(backend.cancelCalls, 1);
      expect(backend.stopCalls, 0);
      expect(
        events.map((e) => e.kind),
        containsAll([
          VoiceSessionEventKind.transcriptUpdated,
          VoiceSessionEventKind.cancelled,
        ]),
      );
    });

    test('completed event fires with the trimmed accumulated transcript',
        () async {
      final completedCompleter = Completer<String>();
      session.events.listen((e) {
        if (e.kind == VoiceSessionEventKind.completed) {
          completedCompleter.complete(e.text);
        }
      });

      await session.start();
      stt.emitFinal('Hello world');
      await Future<void>.delayed(Duration.zero);
      await stt.close();

      final text = await completedCompleter.future;
      expect(text, 'Hello world');
    });

    test('start throws when called twice', () async {
      await session.start();
      expect(() => session.start(), throwsStateError);
    });

    test('start rethrows permission denial as VoiceRecordingException',
        () async {
      backend.permission = false;
      expect(
        () => session.start(),
        throwsA(isA<VoiceRecordingException>().having(
          (e) => e.kind,
          'kind',
          VoiceRecordingErrorKind.permissionDenied,
        )),
      );
      expect(session.isActive, isFalse);
    });

    test('maxDuration timer fires stop() if the user holds too long',
        () async {
      // Re-create with a tiny cap to keep the test fast.
      await session.dispose();
      session = VoiceRecordingSession(
        recording: RecordingController(backend: backend),
        cloudStt: stt,
        languageHints: const ['zh'],
        maxDuration: const Duration(milliseconds: 50),
      );
      final completer = Completer<VoiceSessionEvent>();
      session.events.listen((e) {
        if (e.kind == VoiceSessionEventKind.maxDurationReached &&
            !completer.isCompleted) {
          completer.complete(e);
        }
      });

      await session.start();
      await completer.future.timeout(const Duration(seconds: 2));
      // Yield once more so stop()'s async chain (timer → events.add →
      // stop → recording.stop → backend.stop) finishes incrementing the
      // counter before we read it.
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(backend.stopCalls, 1);
    });
  });
}
