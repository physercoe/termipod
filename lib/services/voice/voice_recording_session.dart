import 'dart:async';
import 'dart:typed_data';

import 'cloud_stt.dart';
import 'recording_controller.dart';

/// Orchestrates a single recording → ASR session. Wraps the
/// [RecordingController] (mic) and [CloudStt] (transcription) together
/// and emits [VoiceSessionEvent]s that UI surfaces (mic button, HUD)
/// listen to.
///
/// Owns the accumulation policy: partials from the ASR stream replace
/// the in-progress sentence; finals are appended to the accumulated
/// text with a single trailing space, then the next partial begins a
/// new sentence.
class VoiceRecordingSession {
  VoiceRecordingSession({
    required this.recording,
    required this.cloudStt,
    required this.languageHints,
    this.maxDuration = const Duration(seconds: 60),
  });

  final RecordingController recording;
  final CloudStt cloudStt;
  final List<String> languageHints;
  final Duration maxDuration;

  final StreamController<VoiceSessionEvent> _events =
      StreamController<VoiceSessionEvent>.broadcast();
  StreamSubscription<TranscriptUpdate>? _asrSub;
  Timer? _maxDurationTimer;
  bool _active = false;
  String _accumulatedFinals = '';
  String _currentPartial = '';

  bool get isActive => _active;

  /// Latest snapshot of `<accumulated finals> + <current partial>`,
  /// trimmed.
  String get transcriptText =>
      (_accumulatedFinals + _currentPartial).trim();

  Stream<VoiceSessionEvent> get events => _events.stream;

  /// Starts the session. Throws [VoiceRecordingException] if the
  /// microphone is unavailable; otherwise emits transcript events on
  /// [events] as the cloud ASR streams them back.
  Future<void> start() async {
    if (_active) {
      throw StateError('VoiceRecordingSession already active');
    }
    _active = true;
    _accumulatedFinals = '';
    _currentPartial = '';

    final Stream<Uint8List> pcm;
    try {
      pcm = await recording.start();
    } catch (e) {
      _active = false;
      _events.add(VoiceSessionEvent.error(e));
      rethrow;
    }

    final asrStream = cloudStt.transcribeStream(
      pcm,
      languageHints: languageHints,
    );

    _asrSub = asrStream.listen(
      _onTranscriptUpdate,
      onError: (Object e) {
        _events.add(VoiceSessionEvent.error(e));
        _teardown();
      },
      onDone: _teardown,
    );

    _maxDurationTimer = Timer(maxDuration, () {
      if (!_active) return;
      _events.add(const VoiceSessionEvent.maxDurationReached());
      stop();
    });
  }

  /// Commits the session: stops the recorder; the ASR client sees the
  /// audio stream close, sends finish-task, and emits a final
  /// transcript. [completed] fires once the ASR side fully closes.
  Future<void> stop() async {
    if (!_active) return;
    _maxDurationTimer?.cancel();
    _maxDurationTimer = null;
    await recording.stop();
    // _teardown fires via _asrSub.onDone once the ASR stream closes.
  }

  /// Cancels the session and discards any partial transcript. UI
  /// should clear its preview surface on this event.
  Future<void> cancel() async {
    if (!_active) return;
    _maxDurationTimer?.cancel();
    _maxDurationTimer = null;
    _active = false;
    await recording.cancel();
    await _asrSub?.cancel();
    _asrSub = null;
    _events.add(const VoiceSessionEvent.cancelled());
  }

  void _onTranscriptUpdate(TranscriptUpdate u) {
    if (u.isFinal) {
      final pending = u.text.trim();
      if (pending.isNotEmpty) {
        _accumulatedFinals =
            _accumulatedFinals.isEmpty ? '$pending ' : '$_accumulatedFinals$pending ';
      }
      _currentPartial = '';
    } else {
      _currentPartial = u.text;
    }
    _events.add(VoiceSessionEvent.transcriptUpdated(transcriptText));
  }

  void _teardown() {
    if (!_active && _events.isClosed) return;
    _maxDurationTimer?.cancel();
    _maxDurationTimer = null;
    _active = false;
    _events.add(VoiceSessionEvent.completed(transcriptText));
  }

  Future<void> dispose() async {
    _maxDurationTimer?.cancel();
    _maxDurationTimer = null;
    if (_active) {
      _active = false;
      await recording.cancel();
    }
    await _asrSub?.cancel();
    _asrSub = null;
    if (!_events.isClosed) await _events.close();
  }
}

/// Tagged-union event from a [VoiceRecordingSession]. Discriminate on
/// [kind] and read the corresponding field.
class VoiceSessionEvent {
  const VoiceSessionEvent._(this.kind, {this.text = '', this.error});

  const VoiceSessionEvent.transcriptUpdated(String text)
      : this._(VoiceSessionEventKind.transcriptUpdated, text: text);
  const VoiceSessionEvent.completed(String finalText)
      : this._(VoiceSessionEventKind.completed, text: finalText);
  const VoiceSessionEvent.cancelled()
      : this._(VoiceSessionEventKind.cancelled);
  const VoiceSessionEvent.maxDurationReached()
      : this._(VoiceSessionEventKind.maxDurationReached);
  const VoiceSessionEvent.error(Object error)
      : this._(VoiceSessionEventKind.error, error: error);

  final VoiceSessionEventKind kind;
  final String text;
  final Object? error;
}

enum VoiceSessionEventKind {
  transcriptUpdated,
  completed,
  cancelled,
  maxDurationReached,
  error,
}
