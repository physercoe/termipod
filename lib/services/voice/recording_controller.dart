import 'dart:async';
import 'dart:typed_data';

import 'package:record/record.dart';

/// Thin wrapper around the `record` plugin that exposes a single, fixed PCM16
/// configuration for the voice-input pipeline (Path C). Hides the plugin's
/// concrete `AudioRecorder` behind a [RecorderBackend] interface so tests can
/// run without platform channels.
class RecordingController {
  RecordingController({RecorderBackend? backend})
      : _backend = backend ?? _RecordPackageBackend();

  final RecorderBackend _backend;

  bool _active = false;
  bool get isActive => _active;

  /// Starts capture and returns a one-shot PCM16 16 kHz mono stream. Closes
  /// when [stop] or [cancel] is called, or when the platform recorder
  /// terminates the stream itself.
  Future<Stream<Uint8List>> start() async {
    if (_active) {
      throw const VoiceRecordingException(
        'recording already in progress',
        VoiceRecordingErrorKind.busy,
      );
    }
    final granted = await _backend.hasPermission();
    if (!granted) {
      throw const VoiceRecordingException(
        'microphone permission denied',
        VoiceRecordingErrorKind.permissionDenied,
      );
    }
    final Stream<Uint8List> stream;
    try {
      stream = await _backend.startStream(
        sampleRate: 16000,
        numChannels: 1,
      );
    } catch (e) {
      throw VoiceRecordingException(
        'platform recorder failed to start: $e',
        VoiceRecordingErrorKind.platformError,
      );
    }
    _active = true;
    return stream;
  }

  /// Closes the recorder so the downstream consumer (the ASR client) sees the
  /// stream end and finalises its transcript.
  Future<void> stop() async {
    if (!_active) return;
    _active = false;
    await _backend.stop();
  }

  /// Closes the recorder without committing — callers must discard whatever
  /// partial transcript they have buffered.
  Future<void> cancel() async {
    if (!_active) return;
    _active = false;
    await _backend.cancel();
  }

  Future<void> dispose() async {
    if (_active) {
      _active = false;
      await _backend.cancel();
    }
    await _backend.dispose();
  }
}

/// Platform abstraction injected by [RecordingController] so tests can swap in
/// a fake. Production builds use [_RecordPackageBackend], which forwards to
/// the `record` plugin.
abstract class RecorderBackend {
  Future<bool> hasPermission();
  Future<Stream<Uint8List>> startStream({
    required int sampleRate,
    required int numChannels,
  });
  Future<void> stop();
  Future<void> cancel();
  Future<void> dispose();
}

class _RecordPackageBackend implements RecorderBackend {
  _RecordPackageBackend();

  final AudioRecorder _recorder = AudioRecorder();

  @override
  Future<bool> hasPermission() => _recorder.hasPermission();

  @override
  Future<Stream<Uint8List>> startStream({
    required int sampleRate,
    required int numChannels,
  }) {
    return _recorder.startStream(
      RecordConfig(
        encoder: AudioEncoder.pcm16bits,
        sampleRate: sampleRate,
        numChannels: numChannels,
      ),
    );
  }

  @override
  Future<void> stop() async {
    await _recorder.stop();
  }

  @override
  Future<void> cancel() async {
    await _recorder.cancel();
  }

  @override
  Future<void> dispose() async {
    await _recorder.dispose();
  }
}

enum VoiceRecordingErrorKind {
  permissionDenied,
  busy,
  platformError,
}

class VoiceRecordingException implements Exception {
  const VoiceRecordingException(this.message, this.kind);
  final String message;
  final VoiceRecordingErrorKind kind;

  @override
  String toString() => 'VoiceRecordingException($kind): $message';
}
