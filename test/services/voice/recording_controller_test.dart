import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/voice/recording_controller.dart';

class _FakeBackend implements RecorderBackend {
  _FakeBackend({this.permission = true, this.throwOnStart});

  bool permission;
  Object? throwOnStart;
  int startCalls = 0;
  int stopCalls = 0;
  int cancelCalls = 0;
  int disposeCalls = 0;
  int? lastSampleRate;
  int? lastNumChannels;

  StreamController<Uint8List>? _controller;

  void emit(Uint8List chunk) => _controller?.add(chunk);
  Future<void> closeStream() async {
    await _controller?.close();
    _controller = null;
  }

  @override
  Future<bool> hasPermission() async => permission;

  @override
  Future<Stream<Uint8List>> startStream({
    required int sampleRate,
    required int numChannels,
  }) async {
    startCalls += 1;
    lastSampleRate = sampleRate;
    lastNumChannels = numChannels;
    if (throwOnStart != null) throw throwOnStart!;
    // Broadcast so close() resolves whether or not the test subscribed —
    // single-subscription `close()` hangs awaiting onDone-delivery to a
    // listener that may never attach in the busy / dispose cases.
    _controller = StreamController<Uint8List>.broadcast();
    return _controller!.stream;
  }

  @override
  Future<void> stop() async {
    stopCalls += 1;
    await closeStream();
  }

  @override
  Future<void> cancel() async {
    cancelCalls += 1;
    await closeStream();
  }

  @override
  Future<void> dispose() async {
    disposeCalls += 1;
    await closeStream();
  }
}

void main() {
  group('RecordingController', () {
    test('start passes 16 kHz mono PCM config and returns the backend stream',
        () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);

      final stream = await controller.start();
      final received = <int>[];
      final sub = stream.listen(received.addAll);

      backend.emit(Uint8List.fromList([1, 2, 3]));
      backend.emit(Uint8List.fromList([4, 5]));
      await Future<void>.delayed(Duration.zero);

      expect(backend.lastSampleRate, 16000);
      expect(backend.lastNumChannels, 1);
      expect(received, [1, 2, 3, 4, 5]);
      expect(controller.isActive, isTrue);

      await sub.cancel();
      await controller.dispose();
    });

    test('start throws permissionDenied when the backend refuses', () async {
      final backend = _FakeBackend(permission: false);
      final controller = RecordingController(backend: backend);

      expect(
        () => controller.start(),
        throwsA(isA<VoiceRecordingException>().having(
          (e) => e.kind,
          'kind',
          VoiceRecordingErrorKind.permissionDenied,
        )),
      );
      expect(backend.startCalls, 0);
      expect(controller.isActive, isFalse);
    });

    test('start throws busy when called twice without stop', () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);
      await controller.start();

      expect(
        () => controller.start(),
        throwsA(isA<VoiceRecordingException>().having(
          (e) => e.kind,
          'kind',
          VoiceRecordingErrorKind.busy,
        )),
      );
      await controller.dispose();
    });

    test('start wraps platform failures as platformError', () async {
      final backend = _FakeBackend(throwOnStart: StateError('boom'));
      final controller = RecordingController(backend: backend);

      await expectLater(
        controller.start(),
        throwsA(isA<VoiceRecordingException>().having(
          (e) => e.kind,
          'kind',
          VoiceRecordingErrorKind.platformError,
        )),
      );
      expect(controller.isActive, isFalse);
    });

    test('stop closes the downstream stream and flips isActive', () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);
      final stream = await controller.start();
      final done = Completer<void>();
      stream.listen((_) {}, onDone: done.complete);

      await controller.stop();
      await done.future;

      expect(controller.isActive, isFalse);
      expect(backend.stopCalls, 1);
      expect(backend.cancelCalls, 0);
    });

    test('cancel closes without invoking stop', () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);
      final stream = await controller.start();
      final done = Completer<void>();
      stream.listen((_) {}, onDone: done.complete);

      await controller.cancel();
      await done.future;

      expect(controller.isActive, isFalse);
      expect(backend.cancelCalls, 1);
      expect(backend.stopCalls, 0);
    });

    test('stop is idempotent when no recording is active', () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);

      await controller.stop();
      expect(backend.stopCalls, 0);
    });

    test('dispose cancels an active recording then releases the backend',
        () async {
      final backend = _FakeBackend();
      final controller = RecordingController(backend: backend);
      await controller.start();

      await controller.dispose();

      expect(controller.isActive, isFalse);
      expect(backend.cancelCalls, 1);
      expect(backend.disposeCalls, 1);
    });
  });
}
