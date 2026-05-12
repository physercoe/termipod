import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/voice/cloud_stt.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

class _FakeWebSocketChannel implements WebSocketChannel {
  _FakeWebSocketChannel();

  final StreamController<dynamic> _serverToClient =
      StreamController<dynamic>.broadcast();
  final _FakeSink _sink = _FakeSink();
  bool get sinkClosed => _sink.closed;
  List<dynamic> get clientSent => _sink.sent;

  void serverEmit(Map<String, dynamic> headerEvent,
          [Map<String, dynamic> payload = const {}]) =>
      _serverToClient.add(jsonEncode({
        'header': headerEvent,
        'payload': payload,
      }));

  void serverClose() => _serverToClient.close();

  @override
  Stream<dynamic> get stream => _serverToClient.stream;

  @override
  WebSocketSink get sink => _sink;

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _FakeSink implements WebSocketSink {
  final List<dynamic> sent = [];
  bool closed = false;

  @override
  void add(dynamic data) => sent.add(data);

  @override
  Future<void> close([int? closeCode, String? closeReason]) async {
    closed = true;
  }

  @override
  Future<void> addStream(Stream<dynamic> stream) async {
    await for (final event in stream) {
      add(event);
    }
  }

  @override
  void addError(Object error, [StackTrace? stackTrace]) {}

  @override
  Future<void> get done async {}
}

Map<String, dynamic> _decode(dynamic frame) =>
    jsonDecode(frame as String) as Map<String, dynamic>;

void main() {
  group('AlibabaWebSocketStt', () {
    late _FakeWebSocketChannel channel;
    late AlibabaWebSocketStt client;

    setUp(() {
      channel = _FakeWebSocketChannel();
      client = AlibabaWebSocketStt(
        apiKey: 'sk-test',
        channelFactory: (uri, headers) => channel,
        taskIdGenerator: () => 'task-fixed',
      );
    });

    test('sends run-task with correct header + payload on connect', () async {
      final audio = StreamController<Uint8List>();
      final updates = <TranscriptUpdate>[];
      client.transcribeStream(audio.stream, languageHints: ['zh', 'en']).listen(
            updates.add,
          );
      await Future<void>.delayed(Duration.zero);

      expect(channel.clientSent, hasLength(1));
      final runTask = _decode(channel.clientSent.single);
      expect(runTask['header'], {
        'action': 'run-task',
        'task_id': 'task-fixed',
        'streaming': 'duplex',
      });
      final payload = runTask['payload'] as Map<String, dynamic>;
      expect(payload['model'], 'fun-asr-realtime');
      expect(payload['task'], 'asr');
      expect(payload['task_group'], 'audio');
      expect(payload['function'], 'recognition');
      final params = payload['parameters'] as Map<String, dynamic>;
      expect(params['format'], 'pcm');
      expect(params['sample_rate'], 16000);
      expect(params['language_hints'], ['zh', 'en']);
      expect(params['punctuation_prediction_enabled'], true);

      await audio.close();
    });

    test('happy path: task-started → chunks → partials + final → finish',
        () async {
      final audio = StreamController<Uint8List>();
      final updates = <TranscriptUpdate>[];
      final stream =
          client.transcribeStream(audio.stream, languageHints: ['zh', 'en']);
      final doneCompleter = Completer<void>();
      stream.listen(updates.add, onDone: doneCompleter.complete);

      await Future<void>.delayed(Duration.zero);
      channel.serverEmit({'event': 'task-started', 'task_id': 'task-fixed'});
      await Future<void>.delayed(Duration.zero);

      audio.add(Uint8List.fromList([1, 2, 3]));
      audio.add(Uint8List.fromList([4, 5, 6]));
      await Future<void>.delayed(Duration.zero);

      channel.serverEmit(
        {'event': 'result-generated'},
        {
          'output': {
            'sentence': {'text': '你好', 'end_time': null},
          },
        },
      );
      channel.serverEmit(
        {'event': 'result-generated'},
        {
          'output': {
            'sentence': {'text': '你好 world', 'end_time': 1500},
          },
        },
      );
      await Future<void>.delayed(Duration.zero);

      await audio.close();
      await Future<void>.delayed(Duration.zero);

      // Expect finish-task in client-sent frames after the run-task + audio
      // chunks. Layout: [run-task json, chunk1, chunk2, finish-task json].
      expect(channel.clientSent, hasLength(4));
      expect(channel.clientSent[1], isA<Uint8List>());
      expect(channel.clientSent[2], isA<Uint8List>());
      final finishTask = _decode(channel.clientSent[3]);
      expect(finishTask['header']['action'], 'finish-task');
      expect(finishTask['header']['task_id'], 'task-fixed');

      channel.serverEmit({'event': 'task-finished'});
      await doneCompleter.future;

      expect(updates, hasLength(2));
      expect(updates[0].text, '你好');
      expect(updates[0].isPartial, isTrue);
      expect(updates[0].isFinal, isFalse);
      expect(updates[1].text, '你好 world');
      expect(updates[1].isPartial, isFalse);
      expect(updates[1].isFinal, isTrue);
      expect(channel.sinkClosed, isTrue);
    });

    test('task-failed surfaces a DashScopeAsrException and closes', () async {
      final audio = StreamController<Uint8List>();
      Object? capturedError;
      final doneCompleter = Completer<void>();
      client.transcribeStream(audio.stream, languageHints: ['zh']).listen(
            (_) {},
            onError: (e) => capturedError = e,
            onDone: doneCompleter.complete,
          );

      await Future<void>.delayed(Duration.zero);
      channel.serverEmit({'event': 'task-started', 'task_id': 'task-fixed'});
      await Future<void>.delayed(Duration.zero);

      channel.serverEmit({
        'event': 'task-failed',
        'task_id': 'task-fixed',
        'error_code': 'InvalidParameter',
        'error_message': 'sample_rate not supported',
      });
      await doneCompleter.future;

      expect(capturedError, isA<DashScopeAsrException>());
      expect(
        capturedError.toString(),
        contains('InvalidParameter'),
      );
      expect(channel.sinkClosed, isTrue);
      await audio.close();
    });

    test('server-side close before task-finished closes the output stream',
        () async {
      final audio = StreamController<Uint8List>();
      final doneCompleter = Completer<void>();
      client.transcribeStream(audio.stream, languageHints: ['zh']).listen(
            (_) {},
            onDone: doneCompleter.complete,
          );

      await Future<void>.delayed(Duration.zero);
      channel.serverClose();
      await doneCompleter.future;
      // Don't await audio.close() — teardown already cancelled the
      // subscription so close() would park awaiting a listener that
      // is no longer there.
      unawaited(audio.close());
    });

    test('audio cancellation pre-task-started never sends finish-task',
        () async {
      final audio = StreamController<Uint8List>();
      client.transcribeStream(audio.stream, languageHints: ['zh']).listen((_) {});

      await Future<void>.delayed(Duration.zero);
      // No task-started arrives; caller closes the audio early.
      await audio.close();
      await Future<void>.delayed(Duration.zero);

      // Only the run-task frame should have been sent.
      expect(channel.clientSent, hasLength(1));
      expect(_decode(channel.clientSent.single)['header']['action'], 'run-task');
    });

    test('paraformer-realtime-v2 model id is forwarded', () async {
      final paraformer = AlibabaWebSocketStt(
        apiKey: 'sk-test',
        model: DashScopeAsrModel.paraformerRealtimeV2,
        channelFactory: (uri, headers) => channel,
        taskIdGenerator: () => 'task-fixed',
      );
      final audio = StreamController<Uint8List>();
      paraformer
          .transcribeStream(audio.stream, languageHints: ['zh'])
          .listen((_) {});

      await Future<void>.delayed(Duration.zero);
      final runTask = _decode(channel.clientSent.single);
      expect(
        (runTask['payload'] as Map)['model'],
        'paraformer-realtime-v2',
      );
      await audio.close();
    });

    test('singapore region uses dashscope-intl endpoint', () async {
      Uri? capturedUri;
      Map<String, String>? capturedHeaders;
      final intlClient = AlibabaWebSocketStt(
        apiKey: 'sk-test',
        region: DashScopeRegion.singapore,
        channelFactory: (uri, headers) {
          capturedUri = uri;
          capturedHeaders = headers;
          return channel;
        },
        taskIdGenerator: () => 'task-fixed',
      );
      final audio = StreamController<Uint8List>();
      intlClient
          .transcribeStream(audio.stream, languageHints: ['zh'])
          .listen((_) {});
      await Future<void>.delayed(Duration.zero);

      expect(capturedUri?.host, 'dashscope-intl.aliyuncs.com');
      expect(capturedHeaders?['Authorization'], 'Bearer sk-test');
      await audio.close();
    });
  });
}
