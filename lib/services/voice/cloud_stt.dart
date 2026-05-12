import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:uuid/uuid.dart';
import 'package:web_socket_channel/io.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Streaming speech-to-text client. The audio stream owns the session
/// lifetime: when it closes, the transcript stream finalises and closes.
abstract class CloudStt {
  Stream<TranscriptUpdate> transcribeStream(
    Stream<Uint8List> audioChunks, {
    required List<String> languageHints,
  });
}

class TranscriptUpdate {
  const TranscriptUpdate({
    required this.text,
    required this.isPartial,
    required this.isFinal,
  });

  final String text;
  final bool isPartial;
  final bool isFinal;

  @override
  String toString() =>
      'TranscriptUpdate("$text", partial=$isPartial, final=$isFinal)';
}

enum DashScopeRegion {
  beijing('wss://dashscope.aliyuncs.com/api-ws/v1/inference'),
  singapore('wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference'),
  us('wss://dashscope-us.aliyuncs.com/api-ws/v1/inference');

  const DashScopeRegion(this.endpoint);
  final String endpoint;
}

enum DashScopeAsrModel {
  funAsrRealtime('fun-asr-realtime'),
  paraformerRealtimeV2('paraformer-realtime-v2');

  const DashScopeAsrModel(this.id);
  final String id;
}

typedef WebSocketChannelFactory = WebSocketChannel Function(
  Uri uri,
  Map<String, String> headers,
);

class AlibabaWebSocketStt implements CloudStt {
  AlibabaWebSocketStt({
    required this.apiKey,
    this.region = DashScopeRegion.beijing,
    this.model = DashScopeAsrModel.funAsrRealtime,
    WebSocketChannelFactory? channelFactory,
    String Function()? taskIdGenerator,
  })  : _channelFactory = channelFactory ?? _defaultFactory,
        _newTaskId = taskIdGenerator ?? _generateTaskId;

  final String apiKey;
  final DashScopeRegion region;
  final DashScopeAsrModel model;
  final WebSocketChannelFactory _channelFactory;
  final String Function() _newTaskId;

  static WebSocketChannel _defaultFactory(
    Uri uri,
    Map<String, String> headers,
  ) {
    return IOWebSocketChannel.connect(uri, headers: headers);
  }

  static String _generateTaskId() =>
      const Uuid().v4().replaceAll('-', '');

  @override
  Stream<TranscriptUpdate> transcribeStream(
    Stream<Uint8List> audioChunks, {
    required List<String> languageHints,
  }) {
    final out = StreamController<TranscriptUpdate>();
    _runSession(out, audioChunks, languageHints);
    return out.stream;
  }

  Future<void> _runSession(
    StreamController<TranscriptUpdate> out,
    Stream<Uint8List> audioChunks,
    List<String> languageHints,
  ) async {
    final taskId = _newTaskId();
    final WebSocketChannel channel;
    try {
      channel = _channelFactory(
        Uri.parse(region.endpoint),
        {'Authorization': 'Bearer $apiKey'},
      );
    } catch (e) {
      if (!out.isClosed) {
        out.addError(DashScopeAsrException('connect failed: $e'));
        await out.close();
      }
      return;
    }

    var phase = _Phase.connecting;
    StreamSubscription<Uint8List>? audioSub;
    late StreamSubscription channelSub;
    final shutdown = Completer<void>();

    Future<void> teardown({Object? error}) async {
      if (shutdown.isCompleted) return;
      shutdown.complete();
      phase = _Phase.closed;
      await audioSub?.cancel();
      await channelSub.cancel();
      try {
        await channel.sink.close();
      } catch (_) {}
      if (!out.isClosed) {
        if (error != null) out.addError(error);
        await out.close();
      }
    }

    void sendFinishTask() {
      try {
        channel.sink.add(jsonEncode({
          'header': {
            'action': 'finish-task',
            'task_id': taskId,
            'streaming': 'duplex',
          },
          'payload': {'input': <String, dynamic>{}},
        }));
      } catch (e) {
        teardown(error: e);
      }
    }

    channelSub = channel.stream.listen(
      (message) async {
        if (phase == _Phase.closed || message is! String) return;
        final Map<String, dynamic> json;
        try {
          json = jsonDecode(message) as Map<String, dynamic>;
        } catch (_) {
          return;
        }
        final header =
            (json['header'] as Map?)?.cast<String, dynamic>() ?? const {};
        final event = header['event'] as String?;
        switch (event) {
          case 'task-started':
            phase = _Phase.running;
            audioSub = audioChunks.listen(
              (chunk) {
                if (phase != _Phase.running) return;
                try {
                  channel.sink.add(chunk);
                } catch (e) {
                  teardown(error: e);
                }
              },
              onDone: () {
                if (phase != _Phase.running) return;
                phase = _Phase.finishing;
                sendFinishTask();
              },
              onError: (e) => teardown(error: e),
              cancelOnError: true,
            );
            break;
          case 'result-generated':
            final payload =
                (json['payload'] as Map?)?.cast<String, dynamic>() ?? const {};
            final output =
                (payload['output'] as Map?)?.cast<String, dynamic>() ?? const {};
            final sentence =
                (output['sentence'] as Map?)?.cast<String, dynamic>();
            if (sentence == null) break;
            final text = (sentence['text'] as String?) ?? '';
            // fun-asr-realtime emits an end_time on the closing partial of a
            // sentence; absence (or zero) means "still streaming."
            final endTime = sentence['end_time'];
            final hasEnd = endTime != null && endTime != 0;
            if (!out.isClosed) {
              out.add(TranscriptUpdate(
                text: text,
                isPartial: !hasEnd,
                isFinal: hasEnd,
              ));
            }
            break;
          case 'task-finished':
            await teardown();
            break;
          case 'task-failed':
            final code = header['error_code'] ?? 'unknown';
            final msg = header['error_message'] ?? '';
            await teardown(
              error: DashScopeAsrException('$code: $msg'),
            );
            break;
        }
      },
      onError: (e) => teardown(error: e),
      onDone: () => teardown(),
    );

    try {
      channel.sink.add(jsonEncode({
        'header': {
          'action': 'run-task',
          'task_id': taskId,
          'streaming': 'duplex',
        },
        'payload': {
          'task_group': 'audio',
          'task': 'asr',
          'function': 'recognition',
          'model': model.id,
          'parameters': {
            'format': 'pcm',
            'sample_rate': 16000,
            'language_hints': languageHints,
            'punctuation_prediction_enabled': true,
          },
          'input': <String, dynamic>{},
        },
      }));
    } catch (e) {
      await teardown(error: e);
    }

    await shutdown.future;
  }
}

enum _Phase { connecting, running, finishing, closed }

class DashScopeAsrException implements Exception {
  const DashScopeAsrException(this.message);
  final String message;
  @override
  String toString() => 'DashScopeAsrException: $message';
}
